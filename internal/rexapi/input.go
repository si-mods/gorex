package rexapi

import (
	"strings"
	"unicode/utf8"

	"gorex/internal/rex"
)

// keyModifiers are the modifiers of a key event, which a client joins
// with "+".
var keyModifiers = map[string]bool{
	"shift": true, "ctrl": true, "alt": true, "super": true,
	"caps_lock": true, "num_lock": true,
	"shift_side": true, "ctrl_side": true, "alt_side": true, "super_side": true,
}

// checkMods checks a mods or consumed_mods string.
func checkMods(name, s string) *apiError {
	if s == "" {
		return nil
	}
	for _, part := range strings.Split(s, "+") {
		if !keyModifiers[part] {
			return errBadRequest("%s names no modifier: %q", name, part)
		}
	}
	return nil
}

// keyInput reads a key event from a JSON object, as client.key's key and
// a key event's payload carry it.
func keyInput(o args) (rex.KeyInput, *apiError) {
	var k rex.KeyInput
	known := map[string]bool{
		"key": true, "mods": true, "consumed_mods": true, "text": true,
		"unshifted_codepoint": true, "composing": true, "action": true,
	}
	for name := range o {
		if !known[name] {
			return k, errBadRequest("key has no field %q", name)
		}
	}
	var aerr *apiError
	if k.Key, aerr = o.reqStr("key"); aerr != nil {
		return k, aerr
	}
	if k.Mods, aerr = o.reqStr("mods"); aerr != nil {
		return k, aerr
	}
	if aerr := checkMods("mods", k.Mods); aerr != nil {
		return k, aerr
	}
	if k.Consumed, aerr = o.reqStr("consumed_mods"); aerr != nil {
		return k, aerr
	}
	if aerr := checkMods("consumed_mods", k.Consumed); aerr != nil {
		return k, aerr
	}
	if k.Text, aerr = o.reqStr("text"); aerr != nil {
		return k, aerr
	}
	n, ok, aerr := o.int("unshifted_codepoint")
	if aerr != nil {
		return k, aerr
	}
	if !ok {
		return k, errBadRequest("unshifted_codepoint is required")
	}
	k.Unshifted = n
	c, ok, aerr := o.boolean("composing")
	if aerr != nil {
		return k, aerr
	}
	if !ok {
		return k, errBadRequest("composing is required")
	}
	k.Composing = c
	if k.Action, aerr = o.reqStr("action"); aerr != nil {
		return k, aerr
	}
	switch k.Action {
	case "press", "release", "repeat":
	default:
		return k, errBadRequest("action is press, release or repeat")
	}
	return k, nil
}

// methodClientKey sends a key to a block, giving Lua key bindings the
// first chance at it. GoRex has no Lua bindings, so every key goes to the
// block, and handled is always false.
func methodClientKey(c *control, a args) (any, *apiError) {
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	raw, ok, aerr := a.obj("key")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("key is required")
	}
	k, aerr := keyInput(raw)
	if aerr != nil {
		return nil, aerr
	}
	prefix, present, aerr := a.strs("prefix")
	if aerr != nil {
		return nil, aerr
	}
	if present && prefix != nil {
		for _, p := range prefix {
			if strings.TrimSpace(p) == "" || strings.ContainsFunc(p, isControl) {
				return nil, errBadRequest("prefix holds no key: %q", p)
			}
		}
	}
	var b *mBlock
	if id, given, aerr := a.str("block_id"); aerr != nil {
		return nil, aerr
	} else if given && id != "" {
		if b = s.findBlock(id); b == nil {
			return nil, errNotFound("no block %s in %s", id, s.id)
		}
	} else {
		b = s.focused()
	}
	if b != nil {
		if err := c.g.opt.Client.SendKey(b.sid, k); err != nil {
			return nil, errInternal("typing the key: %v", err)
		}
	}
	// The keys of a sequence reached no binding, so none of them, the
	// last one included, went to the block.
	return map[string]any{"handled": false}, nil
}

// isControl tells whether r is a control character.
func isControl(r rune) bool { return r < 0x20 || r == 0x7f }

// methodBlockEvent sends an input event to a block.
func methodBlockEvent(c *control, a args) (any, *apiError) {
	s, aerr := c.sessionArg(a)
	if aerr != nil {
		return nil, aerr
	}
	id, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	b := s.findBlock(id)
	if b == nil {
		return nil, errNotFound("no block %s in %s", id, s.id)
	}
	ev, ok, aerr := a.obj("event")
	if aerr != nil {
		return nil, aerr
	}
	if !ok {
		return nil, errBadRequest("event is required")
	}
	typ, aerr := ev.reqStr("type")
	if aerr != nil {
		return nil, aerr
	}
	payload, _, aerr := ev.obj("payload")
	if aerr != nil {
		return nil, aerr
	}
	if payload == nil {
		payload = args{}
	}
	client := c.g.opt.Client
	switch typ {
	case "key":
		k, aerr := keyInput(payload)
		if aerr != nil {
			return nil, aerr
		}
		if err := client.SendKey(b.sid, k); err != nil {
			return nil, errInternal("typing the key: %v", err)
		}
	case "paste":
		text, aerr := payload.reqStr("text")
		if aerr != nil {
			return nil, aerr
		}
		if err := client.Paste(b.sid, text); err != nil {
			return nil, errInternal("pasting: %v", err)
		}
	case "mouse":
		m, aerr := mouseInput(payload)
		if aerr != nil {
			return nil, aerr
		}
		if err := client.SendMouse(b.sid, m); err != nil {
			return nil, errInternal("sending the pointer event: %v", err)
		}
	case "wheel":
		m, aerr := wheelInput(payload)
		if aerr != nil {
			return nil, aerr
		}
		if err := client.SendMouse(b.sid, m); err != nil {
			return nil, errInternal("sending the wheel event: %v", err)
		}
	case "focus":
		action, aerr := payload.reqStr("action")
		if aerr != nil {
			return nil, aerr
		}
		var in bool
		switch action {
		case "in":
			in = true
		case "out":
			in = false
		default:
			return nil, errBadRequest("a focus event's action is in or out")
		}
		if err := client.SendFocus(b.sid, in); err != nil {
			return nil, errInternal("reporting the focus: %v", err)
		}
	default:
		return nil, errBadRequest("%q is an event only the server sends", typ)
	}
	return struct{}{}, nil
}

// mouseInput reads a pointer event's payload: its action, button, mods and
// cell.
func mouseInput(o args) (rex.MouseInput, *apiError) {
	var m rex.MouseInput
	for name := range o {
		switch name {
		case "action", "button", "mods", "column", "row", "col":
		default:
			return m, errBadRequest("a mouse event has no field %q", name)
		}
	}
	var aerr *apiError
	if m.Action, _, aerr = o.str("action"); aerr != nil {
		return m, aerr
	}
	if aerr := checkMouseAction(m.Action); aerr != nil {
		return m, aerr
	}
	if m.Button, _, aerr = o.str("button"); aerr != nil {
		return m, aerr
	}
	if aerr := checkMouseButton(m.Button, false); aerr != nil {
		return m, aerr
	}
	if m.Mods, _, aerr = o.str("mods"); aerr != nil {
		return m, aerr
	}
	if aerr := checkMods("mods", m.Mods); aerr != nil {
		return m, aerr
	}
	col, ok, aerr := o.int("column")
	if aerr != nil {
		return m, aerr
	}
	if !ok {
		col, _, aerr = o.int("col")
		if aerr != nil {
			return m, aerr
		}
	}
	row, _, aerr := o.int("row")
	if aerr != nil {
		return m, aerr
	}
	if col < 0 || row < 0 {
		return m, errBadRequest("column and row count from zero")
	}
	m.Col, m.Row = col, row
	return m, nil
}

// wheelInput reads a wheel event's payload: its direction, mods and cell.
func wheelInput(o args) (rex.MouseInput, *apiError) {
	var m rex.MouseInput
	for name := range o {
		switch name {
		case "direction", "button", "mods", "column", "row", "col":
		default:
			return m, errBadRequest("a wheel event has no field %q", name)
		}
	}
	var aerr *apiError
	dir, ok, aerr := o.str("direction")
	if aerr != nil {
		return m, aerr
	}
	if !ok || dir == "" {
		dir, _, aerr = o.str("button")
		if aerr != nil {
			return m, aerr
		}
	}
	if aerr := checkMouseButton(dir, true); aerr != nil {
		return m, aerr
	}
	m.Button, m.Action = dir, "press"
	if m.Mods, _, aerr = o.str("mods"); aerr != nil {
		return m, aerr
	}
	if aerr := checkMods("mods", m.Mods); aerr != nil {
		return m, aerr
	}
	col, ok, aerr := o.int("column")
	if aerr != nil {
		return m, aerr
	}
	if !ok {
		col, _, aerr = o.int("col")
		if aerr != nil {
			return m, aerr
		}
	}
	row, _, aerr := o.int("row")
	if aerr != nil {
		return m, aerr
	}
	if col < 0 || row < 0 {
		return m, errBadRequest("column and row count from zero")
	}
	m.Col, m.Row = col, row
	return m, nil
}

// checkMouseAction checks a pointer event's action.
func checkMouseAction(action string) *apiError {
	switch action {
	case "", "press", "release", "motion":
		return nil
	}
	return errBadRequest("a mouse event's action is press, release or motion")
}

// checkMouseButton checks a pointer event's button, or a wheel's
// direction when wheel is set.
func checkMouseButton(button string, wheel bool) *apiError {
	if wheel {
		switch button {
		case "up", "down", "left", "right",
			"wheel_up", "wheel_down", "wheel_left", "wheel_right":
			return nil
		}
		return errBadRequest("a wheel event's direction is up, down, left or right")
	}
	switch button {
	case "", "none", "left", "right", "middle":
		return nil
	}
	return errBadRequest("a mouse event's button is none, left, right or middle")
}

// validText tells whether s is valid UTF-8 without control characters,
// as a label must be.
func validText(s string, max int) *apiError {
	if !utf8.ValidString(s) {
		return errBadRequest("the text is not valid UTF-8")
	}
	if len(s) > max {
		return errBadRequest("the text is longer than %d bytes", max)
	}
	if strings.ContainsFunc(s, isControl) {
		return errBadRequest("the text contains a control character")
	}
	return nil
}
