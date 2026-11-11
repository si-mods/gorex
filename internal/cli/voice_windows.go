package cli

import "io"

// Voice handles nothing on Windows, whose Claude Code records with no rec.
func Voice(args []string, out, errOut io.Writer) (code int, handled bool) { return 0, false }
