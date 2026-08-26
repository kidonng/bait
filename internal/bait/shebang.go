package bait

import (
	"bytes"
	"path/filepath"
	"strings"
)

// fishShebang replaces shell interpreters on translated scripts.
const fishShebang = "#!/usr/bin/env fish"

// shellInterpreters lists interpreter basenames whose scripts are treated
// as bash-family input and therefore get their shebang rewritten.
var shellInterpreters = map[string]bool{
	"sh":   true,
	"bash": true,
	"ash":  true,
	"dash": true,
}

// translateShebang rewrites a shell shebang line to "#!/usr/bin/env fish".
// Scripts without a shebang, with a fish shebang, or with a non-shell
// interpreter are returned unchanged. Interpreter flags after the program
// name are dropped.
func translateShebang(src []byte) []byte {
	if !bytes.HasPrefix(src, []byte("#!")) {
		return src
	}
	line := src
	if i := bytes.IndexByte(src, '\n'); i >= 0 {
		line = src[:i]
	}

	fields := strings.Fields(string(line[2:]))
	if len(fields) == 0 {
		return src
	}
	interp := fields[0]
	if filepath.Base(interp) == "env" && len(fields) > 1 {
		interp = fields[1]
	}
	if !shellInterpreters[filepath.Base(interp)] {
		return src
	}

	out := make([]byte, 0, len(fishShebang)+len(src)-len(line))
	out = append(out, fishShebang...)
	out = append(out, src[len(line):]...)
	return out
}
