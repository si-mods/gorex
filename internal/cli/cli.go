// Package cli is the gorex command line: its output and the commands that
// scripts and agents run. The commands come with M1; the JSON shape they
// share is settled here, with a golden test, so that output stays stable
// once it is public.
package cli

import (
	"encoding/json"
	"io"
)

// WriteJSON writes v as the CLI's JSON: two-space indentation, a newline,
// and no HTML escaping, which scripts read as the values they gave.
func WriteJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
