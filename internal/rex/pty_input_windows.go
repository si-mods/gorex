package rex

import "errors"

// errNoInputMode is what a console that keeps no line discipline reports:
// a ConPTY has none.
var errNoInputMode = errors.New("rex: a ConPTY has no line discipline")

// inputMode reports how the terminal handles typed input: a ConPTY has
// no line discipline to ask.
func (p *pty) inputMode() (InputMode, error) { return InputMode{}, errNoInputMode }
