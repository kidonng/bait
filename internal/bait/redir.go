package bait

import (
	"fmt"
	"strconv"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// fishBuiltins lists Fish built-in commands and standard functions that
// overlap with Bash builtins or keywords. Redirections to file descriptors
// above 2 on these targets are not supported in Fish and trigger a warning.
var fishBuiltins = map[string]bool{
	"!": true, ".": true, ":": true, "[": true, "alias": true,
	"bg": true, "bind": true, "break": true, "builtin": true, "case": true,
	"cd": true, "command": true, "complete": true, "continue": true, "dirs": true,
	"disown": true, "echo": true, "else": true, "eval": true, "exec": true,
	"exit": true, "export": true, "false": true, "fg": true, "for": true,
	"function": true, "help": true, "history": true, "if": true, "jobs": true,
	"kill": true, "popd": true, "printf": true, "pushd": true, "pwd": true,
	"read": true, "return": true, "set": true, "source": true, "suspend": true,
	"test": true, "time": true, "trap": true, "true": true, "type": true,
	"ulimit": true, "umask": true, "wait": true, "while": true,
}

func isFishBuiltin(name string) bool {
	return fishBuiltins[name]
}

// warnBashOnlyBuiltin flags bash builtins that fish lacks entirely (or
// whose fish counterpart differs fatally). They are emitted verbatim
// with a warning: silent passthrough would only fail at runtime.
var bashOnlyBuiltins = map[string]string{
	"let":       "use 'math' instead",
	"shopt":     "fish has no shell options",
	"caller":    "use 'status print-stack-trace' instead",
	"compgen":   "use 'complete' instead",
	"compopt":   "use 'complete' instead",
	"enable":    "fish does not support enabling/disabling builtins",
	"fc":        "use 'history' instead",
	"mapfile":   "fish has no array-from-file builtin",
	"readarray": "fish has no array-from-file builtin",
	"times":     "fish has no shell-times builtin",
	"logout":    "fish has no login-shell builtin",
}

func (e *emitter) warnBashOnlyBuiltin(s *syntax.Stmt, c *syntax.CallExpr) {
	if len(c.Args) == 0 || len(c.Assigns) != 0 || e.synthSets[c] {
		return
	}
	name := e.render(c.Args[0])
	if hint, ok := bashOnlyBuiltins[name]; ok {
		e.warn(s.Position, "%s is a bash builtin with no fish equivalent (%s); emitted verbatim", name, hint)
	}
}

func isHighFDTarget(r *syntax.Redirect) (int, bool) {
	if r.Op != syntax.DplOut && r.Op != syntax.DplIn {
		return 0, false
	}
	if r.Word == nil || len(r.Word.Parts) != 1 {
		return 0, false
	}
	lit, ok := r.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Value)
	if err != nil || n <= 2 {
		return 0, false
	}
	return n, true
}

func hasHighFDTargetRedir(redirs []*syntax.Redirect) bool {
	for _, r := range redirs {
		if _, ok := isHighFDTarget(r); ok {
			return true
		}
	}
	return false
}

func stmtHasHighFDTargetRedir(st *syntax.Stmt) bool {
	if st == nil {
		return false
	}
	if hasHighFDTargetRedir(st.Redirs) {
		return true
	}
	found := false
	syntax.Walk(st.Cmd, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			if hasHighFDTargetRedir(s.Redirs) {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func stmtHasTargetFD(st *syntax.Stmt, target int) bool {
	if st == nil {
		return false
	}
	for _, r := range st.Redirs {
		if n, ok := isHighFDTarget(r); ok && n == target {
			return true
		}
	}
	found := false
	syntax.Walk(st.Cmd, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			for _, r := range s.Redirs {
				if n, ok := isHighFDTarget(r); ok && n == target {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}

// pairedHighFDFd reports the descriptor of a `N>&1` redirection, bash's
// way of reopening a descriptor that a command substitution will capture.
func pairedHighFDFd(r *syntax.Redirect) (int, bool) {
	if r.Op != syntax.DplOut || r.N == nil {
		return 0, false
	}
	fd, err := strconv.Atoi(r.N.Value)
	if err != nil || fd <= 2 {
		return 0, false
	}
	if r.Word == nil || len(r.Word.Parts) != 1 {
		return 0, false
	}
	lit, ok := r.Word.Parts[0].(*syntax.Lit)
	if !ok || lit.Value != "1" {
		return 0, false
	}
	return fd, true
}

func (e *emitter) isPairedHighFDRedir(s *syntax.Stmt, r *syntax.Redirect) bool {
	fd, ok := pairedHighFDFd(r)
	if !ok {
		return false
	}
	found := false
	syntax.Walk(s.Cmd, func(n syntax.Node) bool {
		if cs, ok := n.(*syntax.CmdSubst); ok {
			for _, st := range cs.Stmts {
				if stmtHasTargetFD(st, fd) {
					found = true
					return false
				}
			}
		}
		return !found
	})
	return found
}

// markPairedFDRedirs records the redirections inside a command
// substitution that an enclosing `N>&1` reopens. They are the only ones
// redirect can map to stderr, since fish cannot reach a higher descriptor
// from a substitution.
func (e *emitter) markPairedFDRedirs(s *syntax.Stmt) {
	for _, r := range s.Redirs {
		fd, ok := pairedHighFDFd(r)
		if !ok || !e.isPairedHighFDRedir(s, r) {
			continue
		}
		if e.pairedFDRedirs == nil {
			e.pairedFDRedirs = make(map[*syntax.Redirect]bool)
		}
		syntax.Walk(s.Cmd, func(n syntax.Node) bool {
			if cs, ok := n.(*syntax.CmdSubst); ok {
				for _, st := range cs.Stmts {
					markTargetFDRedirs(st, fd, e.pairedFDRedirs)
				}
			}
			return true
		})
	}
}

// markTargetFDRedirs records every redirection in st's subtree that
// points at fd.
func markTargetFDRedirs(st *syntax.Stmt, fd int, mark map[*syntax.Redirect]bool) {
	if st == nil {
		return
	}
	syntax.Walk(st, func(n syntax.Node) bool {
		if s, ok := n.(*syntax.Stmt); ok {
			for _, r := range s.Redirs {
				if target, ok := isHighFDTarget(r); ok && target == fd {
					mark[r] = true
				}
			}
		}
		return true
	})
}

// redirect renders a single redirection manually: the mvdan printer
// cannot print a bare *Redirect node.
func (e *emitter) redirect(r *syntax.Redirect) string {
	s := ""
	if r.N != nil {
		s += r.N.Value
	}
	s += r.Op.String()
	word := e.renderWordSmart(r.Word)
	if fd, ok := isHighFDTarget(r); ok && r.Op == syntax.DplOut && e.inSubshell {
		// Inside a command substitution fish has no descriptor above 2,
		// so the only faithful mapping is the paired idiom's stderr, and
		// only when the enclosing statement opened that descriptor.
		if e.pairedFDRedirs[r] {
			word = "2"
		} else {
			e.warn(r.Pos(), "redirection to file descriptor %d inside a command substitution has no fish equivalent; emitted verbatim", fd)
		}
	}
	return s + " " + word
}

// tails renders the redirections and background marker that trail a
// structural statement, for appending to its closing line.
func (e *emitter) tails(s *syntax.Stmt) string {
	out := ""
	for _, r := range s.Redirs {
		if e.isPairedHighFDRedir(s, r) {
			continue
		}
		out += " " + e.redirect(r)
	}
	if s.Background {
		out += " &"
	}
	return out
}

func (e *emitter) checkHighFDRedir(s *syntax.Stmt, kind string) {
	if e.execRewrites[s] {
		return
	}
	for _, r := range s.Redirs {
		if e.isPairedHighFDRedir(s, r) {
			continue
		}
		if r.N != nil {
			if n, err := strconv.Atoi(r.N.Value); err == nil && n > 2 {
				e.warn(r.Pos(), "redirection to file descriptor %d on %s is not supported in fish; emitted verbatim", n, kind)
			}
		}
	}
}

// normalizeExecCmd makes a redirection-only `exec` safe. bash applies the
// redirections to the shell itself and succeeds; fish's exec takes only a
// command (with none it runs its manual page), and fish cannot hold
// descriptors above 2 open at all. The statement becomes `true` with the
// same redirections, which keeps their file-open side effects, and the
// caller is warned that the shell's streams and descriptors do not change.
func (e *emitter) normalizeExecCmd(s *syntax.Stmt, c *syntax.CallExpr) {
	if len(c.Args) == 0 || !isLitWord(c.Args[0], "exec") || len(c.Args) > 1 {
		return
	}
	c.Args[0] = litWord("true")
	e.execRewrites[s] = true
	if len(s.Redirs) == 0 {
		e.warn(s.Pos(), "exec with no command is a no-op in bash; it is emitted as true")
		return
	}
	e.warn(s.Pos(), "fish cannot redirect the shell itself; the redirections apply to this statement only, so later uses of those streams or descriptors diverge")
}

// readOptionTakesArg reports whether a bash read option consumes an
// argument: p (prompt), u (fd), t (timeout), n/N (nchars), d (delimiter),
// i (readline initial text), a (array name).
func readOptionTakesArg(ch byte) bool {
	return strings.ContainsRune("putnNdia", rune(ch))
}

// normalizeReadCmd adapts a bash read command to fish:
//  1. Drops -r: fish never processes backslashes, so raw mode is default.
//  2. Renames -p to -P: fish's -p runs its argument as the prompt command,
//     while -P takes the literal prompt bash's -p does.
//  3. Drops -t (timeout), -i (readline initial text), -e (readline) and
//     -N (exact count), warning where fish cannot match the meaning.
//  4. Turns -u FD into an input redirection: fish's -u is --unexport.
//  5. Drops -a and keeps its name as a target: fish's read always splits
//     into list elements.
//  6. Mangles target names that collide with fish's reserved variables
//     (e.g. "_" -> "_unused", "status" -> "_status"), and records them so
//     emission can pin the scope bash would assign to.
//  7. Appends REPLY when no target word survives option parsing, matching
//     bash's default variable, and warns when -d is given at most one
//     target, whose fish meaning diverges.
func (e *emitter) normalizeReadCmd(s *syntax.Stmt, c *syntax.CallExpr) {
	if len(c.Args) == 0 || !isLitWord(c.Args[0], "read") {
		return
	}
	var targets []string
	// targetWords counts every positional target word, literal or not:
	// bash's `read "$v"` assigns the variable the value names, which
	// fish's `read "$v"` reproduces, so a dynamic word is a target.
	targetWords := 0
	filtered := []*syntax.Word{c.Args[0]}
	addTarget := func(w *syntax.Word) {
		targetWords++
		lit, ok := isBareLit(w)
		if !ok {
			// bash applies quote removal to a read target, so a fully
			// quoted literal is still a name: `read "version"` assigns
			// version. Only a word with expansions stays dynamic.
			if lit, ok := staticWordValue(w); ok && syntax.ValidName(lit) {
				name := mangleVarName(lit)
				targets = append(targets, name)
				filtered = append(filtered, litWord(name))
				return
			}
			filtered = append(filtered, w)
			return
		}
		name := mangleVarName(lit)
		targets = append(targets, name)
		filtered = append(filtered, litWord(name))
	}
	var delimPos syntax.Pos
	inOptions := true
	for i := 1; i < len(c.Args); i++ {
		w := c.Args[i]
		lit, isLit := isBareLit(w)
		if inOptions && isLit && lit == "--" {
			inOptions = false
			filtered = append(filtered, w)
			continue
		}
		if !inOptions || !isLit || !strings.HasPrefix(lit, "-") || len(lit) < 2 {
			inOptions = false
			addTarget(w)
			continue
		}
		// An option cluster: bash consumes the rest of the cluster, or the
		// next word, as the argument of an option that takes one.
		var kept []byte
		var argWord *syntax.Word
		argText := ""
		keepArg := false
		for j := 1; j < len(lit); j++ {
			ch := lit[j]
			if !readOptionTakesArg(ch) {
				switch ch {
				case 'r':
					// raw is fish's default; nothing to warn about
				case 'e':
					e.warn(w.Pos(), "read -e (readline) has no fish equivalent; it is dropped")
				default:
					kept = append(kept, ch)
				}
				continue
			}
			if j+1 < len(lit) {
				argText = lit[j+1:]
			} else if i+1 < len(c.Args) {
				i++
				argWord = c.Args[i]
				argText, _ = isBareLit(argWord)
			}
			switch ch {
			case 'p':
				kept = append(kept, 'P')
				keepArg = true
			case 'N':
				e.warn(w.Pos(), "read -N reads an exact character count; fish's -n stops at the delimiter")
				kept = append(kept, 'n')
				keepArg = true
			case 'a':
				// bash's -a stores the fields as an array; fish's --list
				// stores them as a list, and the name is still a target.
				filtered = append(filtered, litWord("--list"))
				if argWord != nil {
					addTarget(argWord)
				} else if argText != "" {
					targetWords++
					name := mangleVarName(argText)
					targets = append(targets, name)
					filtered = append(filtered, litWord(name))
				}
			case 'u':
				// fish's -u is --unexport, and its read does not read from
				// an inherited descriptor, so point the input at the
				// descriptor's device instead.
				e.warn(w.Pos(), "read -u reads from a file descriptor; fish's -u is --unexport, so the input is redirected to /dev/fd instead")
				if fdWord := readFDWord(argWord, argText); fdWord != nil && s != nil {
					s.Redirs = append(s.Redirs, &syntax.Redirect{Op: syntax.RdrIn, Word: fdWord})
				}
			case 't':
				e.warn(w.Pos(), "read -t has no fish equivalent (no read timeout); the timeout is dropped")
			case 'i':
				e.warn(w.Pos(), "read -i initial text has no fish equivalent; it is dropped")
			default:
				if ch == 'd' {
					delimPos = w.Pos()
				}
				kept = append(kept, ch)
				keepArg = true
			}
			break
		}
		if len(kept) > 0 {
			filtered = append(filtered, litWord("-"+string(kept)))
		}
		if keepArg {
			if argWord != nil {
				filtered = append(filtered, argWord)
			} else if argText != "" {
				filtered = append(filtered, litWord(argText))
			}
		}
	}
	if targetWords == 0 {
		// bash's read with no names assigns REPLY.
		name := mangleVarName("REPLY")
		targets = append(targets, name)
		filtered = append(filtered, litWord(name))
	}
	if delimPos.IsValid() && targetWords <= 1 {
		e.warn(delimPos, "read -d diverges: fish reads the whole record where bash strips the delimiter and returns the first field")
	}
	c.Args = filtered
	if len(targets) > 0 {
		if e.readTargets == nil {
			e.readTargets = make(map[*syntax.CallExpr][]string)
		}
		e.readTargets[c] = targets
	}
}

// readFDWord builds the input target for a bash `read -u FD`: the
// descriptor's device file, which fish's read can open even when the
// descriptor was inherited from the caller.
func readFDWord(argWord *syntax.Word, argText string) *syntax.Word {
	if argWord != nil {
		return &syntax.Word{Parts: append([]syntax.WordPart{litPart("/dev/fd/")}, argWord.Parts...)}
	}
	if argText == "" {
		return nil
	}
	return litWord("/dev/fd/" + argText)
}

// applyReadScope pins the scope bash's read assigns to. Inside a function,
// `read x` writes the global x unless the function declared a local one,
// while fish's read creates a function-scoped variable when none exists;
// an existing local is left alone so fish targets it like bash does.
func (e *emitter) applyReadScope(c *syntax.CallExpr) {
	if !e.inFunction || c == nil {
		return
	}
	targets := e.readTargets[c]
	if len(targets) == 0 {
		return
	}
	for _, name := range targets {
		if e.funcLocals[name] {
			return
		}
	}
	for _, a := range c.Args {
		if isLitWord(a, "--global") || isLitWord(a, "-g") {
			return
		}
	}
	c.Args = append([]*syntax.Word{c.Args[0], litWord("--global")}, c.Args[1:]...)
}

func isBareLit(w *syntax.Word) (string, bool) {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			return lit.Value, true
		}
	}
	return "", false
}

// staticWordValue returns a word's value when it is fully literal — plain
// and quoted literals with no expansions — mirroring the quote removal bash
// performs before a word becomes a read target name.
func staticWordValue(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			b.WriteString(x.Value)
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

// heredocDelimiterValue returns a here-document delimiter as the parser
// reports it in errors: backslashes in unquoted literal parts are removed,
// while quoted parts keep their value.
func heredocDelimiterValue(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch x := p.(type) {
		case *syntax.Lit:
			for i := 0; i < len(x.Value); i++ {
				if x.Value[i] == '\\' && i+1 < len(x.Value) {
					i++
				}
				b.WriteByte(x.Value[i])
			}
		case *syntax.SglQuoted:
			b.WriteString(x.Value)
		case *syntax.DblQuoted:
			for _, q := range x.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	if b.Len() == 0 {
		return "", false
	}
	return b.String(), true
}

func isLitWord(w *syntax.Word, val string) bool {
	if len(w.Parts) != 1 {
		return false
	}
	lit, ok := w.Parts[0].(*syntax.Lit)
	return ok && lit.Value == val
}

func isCmdChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
		c == '_' || c == '.' || c == '/'
}

// normalizeCommandName translates Bash's \cmd alias-bypass syntax to plain cmd.
// In Bash, prefixing the command word with a backslash (\cd, \alias, \pwd) quotes
// the first character to prevent alias expansion. In Fish, alias expansion does
// not exist and backslashes before alphanumeric characters trigger control escapes
// (\c -> Ctrl-D, \a -> BEL) or unknown command errors. Subsequent arguments
// are passed to the command itself and are not alias-expanded by the shell.
func normalizeCommandName(c *syntax.CallExpr) {
	if len(c.Args) == 0 || len(c.Args[0].Parts) == 0 {
		return
	}
	lit, ok := c.Args[0].Parts[0].(*syntax.Lit)
	if !ok {
		return
	}
	val := lit.Value
	if strings.HasPrefix(val, `\`) && !strings.HasPrefix(val, `\\`) && len(val) > 1 {
		if isCmdChar(val[1]) {
			lit.Value = val[1:]
		}
	}
}

func extractHdoc(redirs []*syntax.Redirect) (*syntax.Redirect, []*syntax.Redirect) {
	var hdoc *syntax.Redirect
	var rest []*syntax.Redirect
	for _, r := range redirs {
		if r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc || r.Op == syntax.WordHdoc {
			if hdoc == nil {
				hdoc = r
			} else {
				rest = append(rest, r)
			}
		} else {
			rest = append(rest, r)
		}
	}
	return hdoc, rest
}

func (e *emitter) emitHdoc(s *syntax.Stmt, hdoc *syntax.Redirect, rest []*syntax.Redirect) {
	for _, r := range rest {
		if r.Op == syntax.Hdoc || r.Op == syntax.DashHdoc || r.Op == syntax.WordHdoc {
			// bash expands every here-document and connects the last one
			// to stdin; a single pipeline cannot reproduce that, so the
			// command is kept verbatim instead of silently dropping one.
			e.warn(s.Position, "multiple here-documents on a single command cannot be translated; emitted verbatim")
			e.lines(e.render(s))
			return
		}
	}
	sc := e.prepareStmt(s)
	origComments := s.Comments
	s.Comments = nil
	origRedirs := s.Redirs
	s.Redirs = rest
	var cmdText string
	if hasStructural(s) {
		cmdText = e.stmtText(s)
	} else {
		cmdText = e.render(s)
	}
	s.Redirs = origRedirs
	s.Comments = origComments
	var prefix string
	if hdoc.Op == syntax.WordHdoc {
		wordText := e.render(hdoc.Word)
		prefix = fmt.Sprintf("printf '%%s\\n' %s", wordText)
	} else if isQuotedHdocWord(hdoc.Word) {
		body := escapeSingleQuotedText(e.hdocBodyText(hdoc))
		prefix = fmt.Sprintf("printf '%%s\\n' '%s'", body)
	} else {
		body := escapeHdocDblQuotedText(e.hdocBodyText(hdoc))
		prefix = fmt.Sprintf("printf '%%s\\n' \"%s\"", body)
	}

	trailing := sc.trailing
	if sc.headerTrailing != nil {
		trailing = append([]syntax.Comment{*sc.headerTrailing}, trailing...)
	}
	if strings.Contains(cmdText, "\n") {
		lines := strings.Split(cmdText, "\n")
		e.printf("%s | %s", prefix, lines[0])
		for _, l := range lines[1 : len(lines)-1] {
			e.printf("%s", l)
		}
		e.printLineWithTrailing(lines[len(lines)-1], trailing)
	} else {
		e.printLineWithTrailing(fmt.Sprintf("%s | %s", prefix, cmdText), trailing)
	}
}

func isQuotedHdocWord(w *syntax.Word) bool {
	if w == nil {
		return false
	}
	for _, p := range w.Parts {
		switch p.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		case *syntax.Lit:
			if strings.ContainsAny(p.(*syntax.Lit).Value, `\'"`) {
				return true
			}
		}
	}
	return false
}

// hdocBodyText assembles a here-document body from its AST parts: literal
// text stays literal, expansions stay live. A `<<-` body loses the leading
// tab of every line, and the newline before the terminator is dropped so
// the caller's `printf '%s\n'` restores exactly bash's byte stream.
func (e *emitter) hdocBodyText(hdoc *syntax.Redirect) string {
	if hdoc.Hdoc == nil {
		return ""
	}
	stripTabs := hdoc.Op == syntax.DashHdoc
	atLineStart := true
	var b strings.Builder
	for _, p := range hdoc.Hdoc.Parts {
		lit, ok := p.(*syntax.Lit)
		if !ok {
			atLineStart = false
			b.WriteString(e.render(p))
			continue
		}
		text := lit.Value
		if stripTabs {
			text = stripHdocTabs(text, &atLineStart)
		} else {
			atLineStart = strings.HasSuffix(text, "\n")
		}
		b.WriteString(text)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// stripHdocTabs removes the leading tabs bash strips from `<<-` body lines.
// atLineStart tracks whether s begins at a line start across parts.
func stripHdocTabs(s string, atLineStart *bool) string {
	if strings.Contains(s, "\t") {
		parts := strings.Split(s, "\n")
		for i, p := range parts {
			if i > 0 || *atLineStart {
				parts[i] = strings.TrimLeft(p, "\t")
			}
		}
		s = strings.Join(parts, "\n")
	}
	*atLineStart = strings.HasSuffix(s, "\n")
	return s
}

// escapeSingleQuotedText escapes literal text for a fish single-quoted
// string: backslash first, then the quote.
func escapeSingleQuotedText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return strings.ReplaceAll(s, `'`, `\'`)
}

// escapeHdocDblQuotedText escapes literal text for a fish double-quoted
// string so it reproduces bash's unquoted here-document escapes. Bash
// consumes a backslash before a backtick (fish keeps it) and keeps a
// backslash before a double quote (fish consumes it); a double quote is
// literal in the body but special in fish. Everything else — backslash
// before `$`, backslash, or a newline — already agrees.
func escapeHdocDblQuotedText(s string) string {
	if !strings.ContainsAny(s, "\\\"`") {
		return s
	}
	var b strings.Builder
	for i := range len(s) {
		switch s[i] {
		case '\\':
			if i+1 < len(s) && s[i+1] == '`' {
				continue
			}
			if i+1 < len(s) && s[i+1] == '"' {
				b.WriteString(`\\`)
				continue
			}
			b.WriteByte('\\')
		case '"':
			b.WriteString(`\"`)
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
