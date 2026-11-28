//go:build darwin || linux || windows

package rex

import (
	"errors"
	"fmt"
	"strconv"
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

// chordModifiers are the modifier names ParseKeyChord reads, by the
// name of an input event's.
var chordModifiers = map[string]string{
	"shift": "shift", "ctrl": "ctrl", "control": "ctrl",
	"alt": "alt", "opt": "alt", "option": "alt",
	"super": "super", "cmd": "super", "command": "super", "meta": "super", "win": "super",
}

// chordKeys are the key names ParseKeyChord reads besides a character,
// by their W3C KeyboardEvent.code.
var chordKeys = map[string]string{
	"enter": "Enter", "return": "Enter", "esc": "Escape", "escape": "Escape",
	"tab": "Tab", "backspace": "Backspace", "delete": "Delete", "del": "Delete",
	"insert": "Insert", "ins": "Insert", "home": "Home", "end": "End",
	"pageup": "PageUp", "page_up": "PageUp", "pgup": "PageUp",
	"pagedown": "PageDown", "page_down": "PageDown", "pgdn": "PageDown",
	"up": "ArrowUp", "down": "ArrowDown", "left": "ArrowLeft", "right": "ArrowRight",
}

// chordPunctuation are the codes of the punctuation keys of a US
// keyboard, by the character each types without shift.
var chordPunctuation = map[byte]string{
	'-': "Minus", '=': "Equal", '[': "BracketLeft", ']': "BracketRight",
	'\\': "Backslash", ';': "Semicolon", '\'': "Quote", '`': "Backquote",
	',': "Comma", '.': "Period", '/': "Slash",
}

// ParseKeyChord reads a key as a user names it on the command line,
// "alt+m", "ctrl+c", "shift+tab", "escape", "f5", or a W3C code ("KeyM",
// "arrow_up"), into the key event of "key", which the server encodes as
// the program's modes ask. A character key gets the code point it types,
// which the encoder needs for alt and for the text of a plain key; a
// shifted character other than a letter is refused, as the character it
// types depends on the keyboard layout.
func ParseKeyChord(s string) (KeyInput, error) {
	var k KeyInput
	parts := strings.Split(strings.TrimSpace(s), "+")
	name := strings.TrimSpace(parts[len(parts)-1])
	if name == "" {
		return k, fmt.Errorf("rex: no key in %q", s)
	}
	held := map[string]bool{}
	var mods []string
	for _, p := range parts[:len(parts)-1] {
		m, ok := chordModifiers[strings.ToLower(strings.TrimSpace(p))]
		if !ok {
			return k, fmt.Errorf("rex: unknown modifier %q in %q", p, s)
		}
		if !held[m] {
			held[m] = true
			mods = append(mods, m)
		}
	}
	// Names ignore case, as "Alt+M" is written for alt+m: shift is a
	// modifier of its own.
	k.Mods = strings.Join(mods, "+")
	plain := !held["ctrl"] && !held["alt"] && !held["super"]
	lower := strings.ToLower(name)
	if c, ok := chordChar(name); ok {
		switch {
		case c >= 'a' && c <= 'z':
			k.Key = "Key" + strings.ToUpper(string(c))
		case c >= '0' && c <= '9':
			k.Key = "Digit" + string(c)
		default:
			k.Key = chordPunctuation[c]
		}
		isLetter := c >= 'a' && c <= 'z'
		if held["shift"] && !isLetter {
			return k, fmt.Errorf("rex: %q: type the shifted character as text", s)
		}
		k.Unshifted = int(c)
		if plain {
			k.Text = string(c)
			if held["shift"] {
				k.Text = strings.ToUpper(k.Text)
				k.Consumed = "shift"
			}
		}
		return k, nil
	}
	if len(name) == 1 {
		return k, fmt.Errorf("rex: unknown key %q in %q", name, s)
	}
	if lower == "space" {
		k.Key, k.Unshifted = "Space", ' '
		if plain {
			k.Text = " "
		}
		return k, nil
	}
	if code, ok := chordKeys[lower]; ok {
		k.Key = code
		return k, nil
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(lower, "f")); err == nil && lower[0] == 'f' && n >= 1 && n <= 24 {
		k.Key = "F" + strconv.Itoa(n)
		return k, nil
	}
	code := KeyCode(name)
	if terminal.ParseKey(code) == terminal.KeyUnidentified {
		return k, fmt.Errorf("rex: unknown key %q in %q", name, s)
	}
	k.Key = code
	return k, nil
}

// chordChar returns the character a character key types without shift,
// named by the character ("m", "/") or by its W3C code ("KeyM", "Digit1",
// "Slash"): the encoder needs it for alt and for a plain key's text.
func chordChar(name string) (byte, bool) {
	if len(name) == 1 {
		c := strings.ToLower(name)[0]
		ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || chordPunctuation[c] != ""
		return c, ok
	}
	code := KeyCode(name)
	switch {
	case len(code) == 4 && strings.HasPrefix(code, "Key") && code[3] >= 'A' && code[3] <= 'Z':
		return code[3] - 'A' + 'a', true
	case len(code) == 6 && strings.HasPrefix(code, "Digit") && code[5] >= '0' && code[5] <= '9':
		return code[5], true
	}
	for c, p := range chordPunctuation {
		if p == code {
			return c, true
		}
	}
	return 0, false
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

// IsTypingPress reports whether k is a non-release, non-modifier key press
// that produces terminal input when typed by a user.
func (k KeyInput) IsTypingPress() bool {
	if k.Action == "release" {
		return false
	}
	if _, err := k.KeyEvent(); err != nil {
		return false
	}
	switch KeyCode(k.Key) {
	case "ShiftLeft", "ShiftRight",
		"ControlLeft", "ControlRight",
		"AltLeft", "AltRight",
		"SuperLeft", "SuperRight",
		"MetaLeft", "MetaRight",
		"CapsLock", "NumLock":
		return false
	}
	return true
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
	return s.keyFromActor(k, ActorHuman, "", "")
}

func (s *session) keyFromActor(k KeyInput, actor, agent, parentSession string) error {
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
	encoded := s.vt.EncodeKey(ev)
	if k.Action == "release" {
		return s.inputProgram(encoded)
	}
	if actor == "" || actor == ActorHuman {
		if HasHumanInputBytes(encoded) {
			s.clearAgent("key")
		}
		return s.inputProgram(encoded)
	}
	return s.inputFromActor(encoded, actor, agent, parentSession, "key")
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
		a.send(msg)
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
		a.send([]byte("\x1bc"))
	}
}

// InputMode reports how the terminal handles typed input.
func (s *session) InputMode() (InputMode, error) { return s.p.inputMode() }
