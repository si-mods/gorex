package rexapi

import (
	"math"
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
		c.g.takeOnInput(c.rec, s, b)
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
		c.g.takeOnInput(c.rec, s, b)
		if err := client.SendKey(b.sid, k); err != nil {
			return nil, errInternal("typing the key: %v", err)
		}
	case "paste":
		text, aerr := payload.reqStr("text")
		if aerr != nil {
			return nil, aerr
		}
		c.g.takeOnInput(c.rec, s, b)
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
		ms, aerr := wheelInput(payload)
		if aerr != nil {
			return nil, aerr
		}
		for _, m := range ms {
			if err := client.SendMouse(b.sid, m); err != nil {
				return nil, errInternal("sending the wheel event: %v", err)
			}
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

// mouseInput reads a pointer event's payload: its action, button, mods
// and cell, and revision 11's pixel position and held buttons.
func mouseInput(o args) (rex.MouseInput, *apiError) {
	var m rex.MouseInput
	for name := range o {
		switch name {
		case "action", "button", "mods", "column", "row", "col", "pixel", "buttons":
		default:
			return m, errBadRequest("a mouse event has no field %q", name)
		}
	}
	var aerr *apiError
	if aerr = acceptPixel(o); aerr != nil {
		return m, aerr
	}
	if m.Action, _, aerr = o.str("action"); aerr != nil {
		return m, aerr
	}
	if aerr := checkMouseAction(m.Action); aerr != nil {
		return m, aerr
	}
	if m.Button, _, aerr = o.str("button"); aerr != nil {
		return m, aerr
	}
	// Revision 11 names the buttons held after the event, which a drag
	// is when the event is a motion; the first one is the button the
	// program's mouse modes know.
	buttons, hasButtons, aerr := o.strs("buttons")
	if aerr != nil {
		return m, aerr
	}
	if hasButtons {
		if m.Button == "" || m.Button == "none" {
			for _, b := range buttons {
				switch b {
				case "left", "right", "middle":
					if m.Button == "" || m.Button == "none" {
						m.Button = b
					}
				default:
					return m, errBadRequest("a mouse event's buttons are left, right or middle")
				}
			}
		}
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

// wheelInput reads a wheel event's payload: its deltas or direction,
// mods and cell. Revision 11's wheel carries delta_mode, delta_x,
// delta_y and delta_z, which the terminal turns into its discrete wheel
// events; older clients send direction, which Rex's revision 11 schema
// no longer lists.
func wheelInput(o args) ([]rex.MouseInput, *apiError) {
	var m rex.MouseInput
	for name := range o {
		switch name {
		case "direction", "button", "mods", "column", "row", "col",
			"delta_mode", "delta_x", "delta_y", "delta_z", "pixel":
		default:
			return nil, errBadRequest("a wheel event has no field %q", name)
		}
	}
	var aerr *apiError
	if aerr = acceptPixel(o); aerr != nil {
		return nil, aerr
	}
	mode, hasMode, aerr := o.str("delta_mode")
	if aerr != nil {
		return nil, aerr
	}
	if hasMode && mode != "" {
		return deltaWheelInput(o, mode)
	}
	dir, ok, aerr := o.str("direction")
	if aerr != nil {
		return nil, aerr
	}
	if !ok || dir == "" {
		dir, _, aerr = o.str("button")
		if aerr != nil {
			return nil, aerr
		}
	}
	if aerr := checkMouseButton(dir, true); aerr != nil {
		return nil, aerr
	}
	m.Button, m.Action = dir, "press"
	if m.Mods, m.Col, m.Row, aerr = wheelModsCell(o); aerr != nil {
		return nil, aerr
	}
	return []rex.MouseInput{m}, nil
}

// deltaWheelInput turns a revision 11 wheel's deltas into the discrete
// wheel events a program's mouse modes know, one per line. A page is 10
// lines and a pixel is a 40th of one, which stand in for the sizes
// GoRex does not keep; delta_z has no axis.
func deltaWheelInput(o args, mode string) ([]rex.MouseInput, *apiError) {
	var m rex.MouseInput
	switch mode {
	case "pixel", "line", "page":
	default:
		return nil, errBadRequest("a wheel event's delta_mode is pixel, line or page")
	}
	dx, hasX, aerr := o.number("delta_x")
	if aerr != nil {
		return nil, aerr
	}
	dy, hasY, aerr := o.number("delta_y")
	if aerr != nil {
		return nil, aerr
	}
	if _, _, aerr := o.number("delta_z"); aerr != nil {
		return nil, aerr
	}
	if m.Mods, m.Col, m.Row, aerr = wheelModsCell(o); aerr != nil {
		return nil, aerr
	}
	m.Action = "press"
	linesX := wheelLines(dx, hasX, mode)
	linesY := wheelLines(dy, hasY, mode)
	var out []rex.MouseInput
	for range max(linesY, -linesY) {
		m.Button = "wheel_down"
		if linesY < 0 {
			m.Button = "wheel_up"
		}
		out = append(out, m)
	}
	for range max(linesX, -linesX) {
		m.Button = "wheel_right"
		if linesX < 0 {
			m.Button = "wheel_left"
		}
		out = append(out, m)
	}
	return out, nil
}

// maxWheelEvents is the most events one axis's delta may become, so a
// huge delta cannot make the server build an unbounded slice.
const maxWheelEvents = 64

// wheelLines turns one axis's delta into a signed count of wheel events.
// A nonzero delta is at least one event, and no delta makes more than
// maxWheelEvents.
func wheelLines(delta float64, present bool, mode string) int {
	if !present || delta == 0 {
		return 0
	}
	switch mode {
	case "pixel":
		delta /= 40
	case "page":
		delta *= 10
	}
	n := int(math.Round(math.Abs(delta)))
	if n < 1 {
		n = 1
	}
	if n > maxWheelEvents {
		n = maxWheelEvents
	}
	if delta < 0 {
		n = -n
	}
	return n
}

// wheelModsCell reads the mods and cell a wheel event carries.
func wheelModsCell(o args) (mods string, col, row int, aerr *apiError) {
	mods, _, aerr = o.str("mods")
	if aerr != nil {
		return
	}
	if aerr = checkMods("mods", mods); aerr != nil {
		return
	}
	col, ok, err := o.int("column")
	if err != nil {
		return "", 0, 0, err
	}
	if !ok {
		col, _, err = o.int("col")
		if err != nil {
			return "", 0, 0, err
		}
	}
	row, _, err = o.int("row")
	if err != nil {
		return "", 0, 0, err
	}
	if col < 0 || row < 0 {
		return "", 0, 0, errBadRequest("column and row count from zero")
	}
	return mods, col, row, nil
}

// acceptPixel validates a revision 11 event's pixel position, which a
// payload carries beside its cell. GoRex keeps no pixel size for a cell,
// so the cell stays authoritative and the position itself is not used.
func acceptPixel(o args) *apiError {
	pixel, ok, aerr := o.obj("pixel")
	if aerr != nil {
		return aerr
	}
	if !ok {
		return nil
	}
	for _, axis := range []string{"x", "y"} {
		n, present, err := pixel.number(axis)
		if err != nil {
			return err
		}
		if present && n < 0 {
			return errBadRequest("pixel counts from the block's top-left corner")
		}
	}
	for name := range pixel {
		switch name {
		case "x", "y":
		default:
			return errBadRequest("pixel takes no %s", name)
		}
	}
	return nil
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
