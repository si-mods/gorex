//go:build darwin || linux || windows

package rex

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
)

// ErrBadInput is the error of an input event the server cannot read.
var ErrBadInput = errors.New("rex: bad input event")

// modifierBits are the modifier names of an input event, as a client
// joins them with "+".
var modifierBits = map[string]terminal.Modifiers{
	"shift": terminal.ModShift, "ctrl": terminal.ModCtrl,
	"alt": terminal.ModAlt, "super": terminal.ModSuper, "meta": terminal.ModSuper,
	"caps_lock": terminal.ModCapsLock, "num_lock": terminal.ModNumLock,
	"shift_side": terminal.ModShiftRight, "ctrl_side": terminal.ModCtrlRight,
	"alt_side": terminal.ModAltRight, "super_side": terminal.ModSuperRight,
}

// ParseModifiers reads the modifiers of an input event, joined with "+".
// An empty string is none.
func ParseModifiers(s string) (terminal.Modifiers, error) {
	var m terminal.Modifiers
	if s == "" {
		return m, nil
	}
	for _, name := range strings.Split(s, "+") {
		bit, ok := modifierBits[strings.TrimSpace(name)]
		if !ok || bit == 0 {
			return 0, errors.New("rex: unknown modifier " + name)
		}
		m |= bit
	}
	return m, nil
}

// KeyCode returns the W3C KeyboardEvent.code a Rex key name is written
// as: "key_a" is "KeyA", "arrow_up" is "ArrowUp", "f12" is "F12".
func KeyCode(name string) string {
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, part := range strings.Split(name, "_") {
		if part == "" {
			b.WriteByte('_')
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// KeyEvent returns the mygo key event of an input event.
func (k KeyInput) KeyEvent() (terminal.KeyEvent, error) {
	code := KeyCode(k.Key)
	if code == "" {
		return terminal.KeyEvent{}, errors.New("rex: no key")
	}
	ev := terminal.KeyEvent{Key: terminal.ParseKey(code), Text: k.Text, Unshifted: rune(k.Unshifted), Composing: k.Composing}
	var err error
	if ev.Mods, err = ParseModifiers(k.Mods); err != nil {
		return ev, err
	}
	if ev.Consumed, err = ParseModifiers(k.Consumed); err != nil {
		return ev, err
	}
	switch k.Action {
	case "", "press":
		ev.Action = terminal.KeyPress
	case "repeat":
		ev.Action = terminal.KeyRepeat
	case "release":
		ev.Action = terminal.KeyRelease
	default:
		return ev, errors.New("rex: action is press, release or repeat")
	}
	return ev, nil
}

// MouseEvent returns the mygo pointer event of an input event.
func (m MouseInput) MouseEvent() (terminal.MouseEvent, error) {
	ev := terminal.MouseEvent{Col: m.Col, Row: m.Row}
	var err error
	if ev.Mods, err = ParseModifiers(m.Mods); err != nil {
		return ev, err
	}
	switch m.Action {
	case "", "press":
		ev.Action = terminal.MousePress
	case "release":
		ev.Action = terminal.MouseRelease
	case "motion", "move", "drag":
		ev.Action = terminal.MouseMotion
	default:
		return ev, errors.New("rex: action is press, release or motion")
	}
	switch m.Button {
	case "", "none":
		ev.Button = terminal.MouseNoButton
	case "left":
		ev.Button = terminal.MouseLeft
	case "right":
		ev.Button = terminal.MouseRight
	case "middle":
		ev.Button = terminal.MouseMiddle
	case "wheel_up", "up":
		ev.Button = terminal.MouseWheelUp
	case "wheel_down", "down":
		ev.Button = terminal.MouseWheelDown
	case "wheel_left", "left_wheel":
		ev.Button = terminal.MouseWheelLeft
	case "wheel_right", "right_wheel":
		ev.Button = terminal.MouseWheelRight
	default:
		return ev, errors.New("rex: unknown button " + m.Button)
	}
	return ev, nil
}

// Key types a key event into the program, encoded as its modes ask.
func (s *session) Key(k KeyInput) error {
	ev, err := k.KeyEvent()
	if err != nil {
		return err
	}
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return ErrSessionEnded
	}
	return s.inputProgram(s.vt.EncodeKey(ev))
}

// Mouse sends a pointer event to the program, encoded as its mouse modes
// ask.
func (s *session) Mouse(m MouseInput) error {
	ev, err := m.MouseEvent()
	if err != nil {
		return err
	}
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return ErrSessionEnded
	}
	return s.inputProgram(s.vt.EncodeMouse(ev))
}

// FocusReport tells the program its window gained or lost the focus,
// when it asked for focus reports.
func (s *session) FocusReport(in bool) error {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited {
		return ErrSessionEnded
	}
	return s.inputProgram(s.vt.EncodeFocus(in))
}

// Modes reports the modes the program set, and the screen it shows.
func (s *session) Modes() ModeStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes.status()
}

// Format returns the session's screen as text, or as the escape
// sequences that redraw it.
func (s *session) Format(o FormatOptions) (FormatResult, error) {
	kind := o.Kind
	if kind == "" {
		kind = "text"
	}
	if kind != "text" && kind != "vt" {
		return FormatResult{}, errors.New("rex: format is text or vt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res := FormatResult{Cols: s.cols, Rows: s.rows}
	if s.exited {
		res.Title = s.title
		if kind == "vt" {
			res.Content = append([]byte(nil), s.screen...)
		} else {
			res.Content = []byte(s.text)
		}
		return res, nil
	}
	snap, title := s.view()
	res.Title = title
	if kind == "vt" {
		res.Content = snap
	} else {
		res.Content = []byte(s.vt.Text())
	}
	return res, nil
}

// ClearScreen clears the scrollback and the screen above the cursor's
// line, unless a full-screen program shows, and reports whether it did.
// The windows attached to the session are told to clear too.
func (s *session) ClearScreen() bool {
	s.mu.Lock()
	exited := s.exited
	s.mu.Unlock()
	if exited || !s.vt.ClearScreen() {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// What a window clearing its own screen shows: the screen it has now.
	msg := append([]byte("\x1b[3J\x1b[2J\x1b[H"), s.vt.Snapshot()...)
	for a := range s.clients {
		if !a.send(msg) {
			a.finish()
			delete(s.clients, a)
		}
	}
	return true
}

// ResetTerminal puts the terminal back to its starting state, clearing
// the screen and scrollback: the program keeps running, and the size and
// the default colors stay. The windows attached to the session reset
// too.
func (s *session) ResetTerminal() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exited {
		return
	}
	s.vt.Feed([]byte("\x1bc"))
	s.modes = decModes{}
	for a := range s.clients {
		if !a.send([]byte("\x1bc")) {
			a.finish()
			delete(s.clients, a)
		}
	}
}

// InputMode reports how the terminal handles typed input.
func (s *session) InputMode() (InputMode, error) { return s.p.inputMode() }
