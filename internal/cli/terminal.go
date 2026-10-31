package cli

import (
	"os"

	"golang.org/x/term"
)

// rawInput puts the terminal on fd in raw mode, so that every key reaches
// the Rex block as typed, and returns what puts it back.
func rawInput(f *os.File) (func(), error) {
	state, err := term.MakeRaw(int(f.Fd()))
	if err != nil {
		return nil, err
	}
	return func() { term.Restore(int(f.Fd()), state) }, nil
}

// inputSize is the terminal's size, 0, 0 when it has none.
func inputSize(f *os.File) (cols, rows int) {
	cols, rows, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0, 0
	}
	return cols, rows
}
