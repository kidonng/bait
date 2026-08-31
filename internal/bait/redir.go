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
	"let":     "use 'math' instead",
	"shopt":   "fish has no shell options",
	"caller":  "use 'status print-stack-trace' instead",
	"compgen": "use 'complete' instead",
	"compopt": "use 'complete' instead",
	"enable":  "fish does not support enabling/disabling builtins",
	"fc":      "use 'history' instead",
}

func (e *emitter) warnBashOnlyBuiltin(s *syntax.Stmt, c *syntax.CallExpr) {
	if len(c.Args) == 0 || len(c.Assigns) != 0 || c.Args[0].Pos().Col() == 0 {
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

func (e *emitter) isPairedHighFDRedir(s *syntax.Stmt, r *syntax.Redirect) bool {
	if r.Op != syntax.DplOut || r.N == nil {
		return false
	}
	fd, err := strconv.Atoi(r.N.Value)
	if err != nil || fd <= 2 {
		return false
	}
	if r.Word == nil || len(r.Word.Parts) != 1 {
		return false
	}
	lit, ok := r.Word.Parts[0].(*syntax.Lit)
	if !ok || lit.Value != "1" {
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

// redirect renders a single redirection manually: the mvdan printer
// cannot print a bare *Redirect node.
func (e *emitter) redirect(r *syntax.Redirect) string {
	s := ""
	if r.N != nil {
		s += r.N.Value
	}
	s += r.Op.String()
	word := e.renderWordSmart(r.Word)
	if e.inSubshell && r.Op == syntax.DplOut {
		if _, ok := isHighFDTarget(r); ok {
			word = "2"
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

// normalizeReadCmd adapts a bash read command to fish:
//  1. Removes the -r flag (fish read never processes backslashes, so raw mode is default).
//  2. Applies variable name mangling for target variable names to prevent colliding with
//     fish read-only or reserved variables (e.g. "_" -> "_unused", "status" -> "_status").
func normalizeReadCmd(c *syntax.CallExpr) {
	if len(c.Args) == 0 || !isLitWord(c.Args[0], "read") {
		return
	}
	filtered := c.Args[:1]
	inOptions := true
	for i := 1; i < len(c.Args); i++ {
		w := c.Args[i]
		if inOptions {
			lit, isLit := isBareLit(w)
			if isLit && lit == "--" {
				inOptions = false
				filtered = append(filtered, w)
				continue
			}
			if isLit && strings.HasPrefix(lit, "-") && len(lit) > 1 {
				// Strip 'r' from the flag if present
				cleaned := "-" + strings.ReplaceAll(lit[1:], "r", "")
				optChar := rune(lit[len(lit)-1])
				// Options taking an argument: p (prompt), u (fd), t (timeout),
				// n/N (nchars), d (delim), i (initial text), a (array name)
				takesArg := strings.ContainsRune("putnNdia", optChar)
				consumesNext := takesArg && (len(lit) == 2 || (len(lit) > 2 && lit[len(lit)-1] == byte(optChar) && !strings.ContainsRune("putnNdia", rune(lit[1]))))

				if cleaned != "-" {
					filtered = append(filtered, litWord(cleaned))
				}
				if consumesNext && i+1 < len(c.Args) {
					i++
					argWord := c.Args[i]
					if optChar == 'a' {
						if argLit, ok := isBareLit(argWord); ok {
							filtered = append(filtered, litWord(mangleVarName(argLit)))
						} else {
							filtered = append(filtered, argWord)
						}
					} else {
						// Non-variable option arguments (prompt, fd, timeout, etc.) preserved verbatim
						filtered = append(filtered, argWord)
					}
				}
				continue
			}
			inOptions = false
		}
		// Positional variable name
		if lit, ok := isBareLit(w); ok {
			filtered = append(filtered, litWord(mangleVarName(lit)))
		} else {
			filtered = append(filtered, w)
		}
	}
	c.Args = filtered
}

func isBareLit(w *syntax.Word) (string, bool) {
	if len(w.Parts) == 1 {
		if lit, ok := w.Parts[0].(*syntax.Lit); ok {
			return lit.Value, true
		}
	}
	return "", false
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
			e.warn(s.Position, "multiple here-documents on a single command are not supported; only one is converted to pipeline")
			break
		}
	}
	sc := classifyComments(s)
	e.leadingComments(sc.leading)
	origComments := s.Comments
	s.Comments = nil
	origRedirs := s.Redirs
	s.Redirs = rest
	var cmdText string
	if hasStructural(s) {
		cmdText = e.inlineStmtText(s)
	} else {
		cmdText = e.render(s)
	}
	s.Redirs = origRedirs
	s.Comments = origComments
	var prefix string
	if hdoc.Op == syntax.WordHdoc {
		wordText := e.render(hdoc.Word)
		prefix = fmt.Sprintf("printf '%%s\\n' %s", wordText)
	} else {
		body := ""
		if hdoc.Hdoc != nil {
			body = e.render(hdoc.Hdoc)
		}
		if isQuotedHdocWord(hdoc.Word) {
			escaped := strings.ReplaceAll(body, "'", `\'`)
			prefix = fmt.Sprintf("printf '%%s\\n' '%s'", escaped)
		} else {
			body = unescapeBackticks(body)
			escaped := strings.ReplaceAll(body, `"`, `\"`)
			prefix = fmt.Sprintf("printf '%%s\\n' \"%s\"", escaped)
		}
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
