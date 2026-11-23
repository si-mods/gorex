// Package config reads GoRex's Lua configuration: keybindings, modes and
// custom actions.
package config

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// Arg is one typed argument of a custom action.
type Arg struct {
	Name     string
	Type     string
	Optional bool
}

// Action is a custom action defined in Lua.
type Action struct {
	Name        string
	Title       string
	Description string
	Args        []Arg
	run         *lua.LFunction
}

// Binding is one key or sequence bound to an action.
type Binding struct {
	Key    string
	Action string
	Mode   string
	Args   map[string]any
}

// EventHandler is a function gorex.on registered for an event.
type EventHandler struct {
	Name string
	fn   *lua.LFunction
}

// Mode is a named key mode.
type Mode struct {
	Name      string
	Exclusive bool
	Once      bool
}

// UIOptions is one question an action asks the person.
type UIOptions struct {
	Kind        string // "input" or "pick"
	Title       string
	Placeholder string
	Value       string
	Confirm     string
	Multiple    bool
	AllowOther  bool
	Items       []UIItem
	Timeout     time.Duration
}

// UIItem is one choice of a pick prompt.
type UIItem struct {
	ID       string
	Title    string
	Subtitle string
	Icon     string
	Detail   string
	Selected bool
}

// UIAnswer is the person's answer: text, one or more item IDs, or a
// cancel.
type UIAnswer struct {
	Text      string
	IDs       []string
	Cancelled bool
}

// ProgramIconRule is an explicit program icon override from init.lua.
type ProgramIconRule struct {
	Name   string
	Icon   string
	Format string
	Mode   string
	Bg     string
	Fg     string
}

// Config is a Lua configuration and the state its custom actions run in.
type Config struct {
	Bindings []Binding
	Actions  []Action
	Modes    []Mode
	Warnings []string
	// IconPacks are the active community icon pack IDs in priority order.
	IconPacks []string
	// IconPackPriority is "pack" (default) or "osc", or "" when unset.
	IconPackPriority string
	// ProgramIcons are explicit icon overrides registered via gorex.program_icon.
	ProgramIcons []ProgramIconRule
	// Events are the handlers gorex.on registered.
	Events   []EventHandler
	state    *lua.LState
	invoke   func(string, map[string]any) error
	ui       func(UIOptions) (UIAnswer, error)
	invoking int
	retire   bool
}

// Load reads path. A missing file is an empty configuration.
func Load(path string) (*Config, error) { return load(path, false) }

// LoadCheck reads path without os, io or file loading, for config check.
func LoadCheck(path string) (*Config, error) { return load(path, true) }

func load(path string, sandbox bool) (*Config, error) {
	l := lua.NewState(lua.Options{SkipOpenLibs: true})
	c := &Config{state: l}
	if sandbox {
		for _, lib := range []struct {
			name string
			fn   lua.LGFunction
		}{
			{lua.BaseLibName, lua.OpenBase}, {lua.TabLibName, lua.OpenTable},
			{lua.StringLibName, lua.OpenString}, {lua.MathLibName, lua.OpenMath},
			{lua.CoroutineLibName, lua.OpenCoroutine},
		} {
			l.Push(l.NewFunction(lib.fn))
			l.Push(lua.LString(lib.name))
			l.Call(1, 0)
		}
		for _, name := range []string{"dofile", "loadfile", "load", "require"} {
			l.SetGlobal(name, lua.LNil)
		}
	} else {
		l.OpenLibs()
	}
	l.SetGlobal("gorex", c.table())
	if path == "" {
		return c, nil
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	l.SetContext(ctx)
	err := l.DoFile(path)
	l.RemoveContext()
	if err != nil {
		c.closeNow()
		return nil, err
	}
	c.check()
	return c, nil
}

// Close frees the Lua state, unless it is running now: the close waits for
// the call to return.
func (c *Config) Close() {
	if c == nil {
		return
	}
	if c.invoking > 0 {
		c.retire = true
		return
	}
	c.closeNow()
}

func (c *Config) closeNow() {
	if c.state != nil {
		c.state.Close()
		c.state = nil
	}
}

// SetInvoke gives custom actions their way to run a built-in action.
func (c *Config) SetInvoke(fn func(string, map[string]any) error) { c.invoke = fn }

// SetUI gives custom actions their way to ask the person a question.
func (c *Config) SetUI(fn func(UIOptions) (UIAnswer, error)) { c.ui = fn }

func (c *Config) table() *lua.LTable {
	t := c.state.NewTable()
	t.RawSetString("bind", c.state.NewFunction(c.bind))
	t.RawSetString("unbind", c.state.NewFunction(c.unbind))
	t.RawSetString("mode", c.state.NewFunction(c.mode))
	t.RawSetString("action", c.state.NewFunction(c.action))
	t.RawSetString("invoke", c.state.NewFunction(c.invokeFn))
	t.RawSetString("on", c.state.NewFunction(c.on))
	t.RawSetString("icon_packs", c.state.NewFunction(c.iconPacksFn))
	t.RawSetString("icon_pack_priority", c.state.NewFunction(c.iconPackPriorityFn))
	t.RawSetString("program_icon", c.state.NewFunction(c.programIconFn))
	t.RawSetString("ui", c.uiTable())
	return t
}

// uiTable is gorex.ui: the questions an action may ask.
func (c *Config) uiTable() *lua.LTable {
	t := c.state.NewTable()
	t.RawSetString("input", c.state.NewFunction(c.uiInput))
	t.RawSetString("pick", c.state.NewFunction(c.uiPick))
	return t
}

// uiInput asks for one line of text, and returns it, or nil after a
// cancel.
func (c *Config) uiInput(l *lua.LState) int {
	opts := parseUIOptions(l, "input")
	if c.ui == nil {
		l.RaiseError("gorex.ui: no prompt host")
		return 0
	}
	ans, err := c.ui(opts)
	if err != nil {
		l.RaiseError("gorex.ui.input: %v", err)
		return 0
	}
	if ans.Cancelled {
		l.Push(lua.LNil)
		return 1
	}
	l.Push(lua.LString(ans.Text))
	return 1
}

// uiPick asks the person to choose, and returns one ID, the text they
// typed, a table of IDs with multiple, or nil after a cancel.
func (c *Config) uiPick(l *lua.LState) int {
	opts := parseUIOptions(l, "pick")
	if c.ui == nil {
		l.RaiseError("gorex.ui: no prompt host")
		return 0
	}
	ans, err := c.ui(opts)
	if err != nil {
		l.RaiseError("gorex.ui.pick: %v", err)
		return 0
	}
	if ans.Cancelled {
		l.Push(lua.LNil)
		return 1
	}
	if opts.Multiple {
		t := l.NewTable()
		for _, id := range ans.IDs {
			t.Append(lua.LString(id))
		}
		l.Push(t)
		return 1
	}
	if len(ans.IDs) > 0 {
		l.Push(lua.LString(ans.IDs[0]))
		return 1
	}
	l.Push(lua.LString(ans.Text))
	return 1
}

// parseUIOptions reads a prompt's table.
func parseUIOptions(l *lua.LState, kind string) UIOptions {
	t := l.CheckTable(1)
	opts := UIOptions{
		Kind:        kind,
		Title:       lua.LVAsString(t.RawGetString("title")),
		Placeholder: lua.LVAsString(t.RawGetString("placeholder")),
		Value:       lua.LVAsString(t.RawGetString("value")),
		Confirm:     lua.LVAsString(t.RawGetString("confirm")),
		Multiple:    lua.LVAsBool(t.RawGetString("multiple")),
		AllowOther:  lua.LVAsBool(t.RawGetString("allow_other")),
		Timeout:     5 * time.Minute,
	}
	if n, ok := t.RawGetString("timeout_ms").(lua.LNumber); ok && n > 0 {
		opts.Timeout = time.Duration(float64(n) * float64(time.Millisecond))
		if opts.Timeout > time.Hour {
			opts.Timeout = time.Hour
		}
	}
	if items, ok := t.RawGetString("items").(*lua.LTable); ok {
		items.ForEach(func(_, v lua.LValue) {
			row, ok := v.(*lua.LTable)
			if !ok {
				return
			}
			item := UIItem{
				ID:       lua.LVAsString(row.RawGetString("id")),
				Title:    lua.LVAsString(row.RawGetString("title")),
				Subtitle: lua.LVAsString(row.RawGetString("subtitle")),
				Icon:     lua.LVAsString(row.RawGetString("icon")),
				Detail:   lua.LVAsString(row.RawGetString("detail")),
				Selected: lua.LVAsBool(row.RawGetString("selected")),
			}
			if item.ID != "" {
				opts.Items = append(opts.Items, item)
			}
		})
	}
	return opts
}

func (c *Config) on(l *lua.LState) int {
	name := strings.TrimSpace(l.CheckString(1))
	fn, ok := l.Get(2).(*lua.LFunction)
	if !ok || name == "" {
		l.RaiseError("gorex.on: an event name and a function are required")
		return 0
	}
	c.Events = append(c.Events, EventHandler{Name: name, fn: fn})
	return 0
}

// Emit calls the handlers of event with a table of fields, on the thread
// that runs the configuration.
func (c *Config) Emit(event string, fields map[string]string) {
	if c == nil || c.state == nil {
		return
	}
	c.invoking++
	defer func() {
		c.invoking--
		if c.invoking == 0 && c.retire {
			c.closeNow()
		}
	}()
	var handlers []EventHandler
	for _, h := range c.Events {
		if h.Name == event {
			handlers = append(handlers, h)
		}
	}
	for _, h := range handlers {
		l := c.state
		t := l.NewTable()
		for k, v := range fields {
			t.RawSetString(k, lua.LString(v))
		}
		l.Push(h.fn)
		l.Push(t)
		if err := l.PCall(1, 0, nil); err != nil {
			c.Warnings = append(c.Warnings, "event "+event+": "+err.Error())
		}
	}
}

func (c *Config) bind(l *lua.LState) int {
	key := l.CheckString(1)
	name := l.CheckString(2)
	b := Binding{Key: strings.TrimSpace(key), Action: strings.TrimSpace(name)}
	if i := strings.Index(b.Key, "/"); i > 0 {
		b.Mode, b.Key = b.Key[:i], b.Key[i+1:]
	}
	if t := l.OptTable(3, nil); t != nil {
		b.Args = tableMap(t)
	}
	if b.Key == "" || b.Action == "" {
		l.RaiseError("gorex.bind: key and action are required")
		return 0
	}
	c.Bindings = append(c.Bindings, b)
	return 0
}

func (c *Config) unbind(l *lua.LState) int {
	key := strings.TrimSpace(l.CheckString(1))
	name := ""
	if l.GetTop() >= 2 {
		name = strings.TrimSpace(l.CheckString(2))
	}
	mode := ""
	if i := strings.Index(key, "/"); i > 0 {
		mode, key = key[:i], key[i+1:]
	}
	c.Bindings = slicesDelete(c.Bindings, func(b Binding) bool {
		return b.Mode == mode && b.Key == key && (name == "" || b.Action == name)
	})
	return 0
}

func (c *Config) mode(l *lua.LState) int {
	name := strings.TrimSpace(l.CheckString(1))
	if name == "" {
		l.RaiseError("gorex.mode: name is required")
		return 0
	}
	m := Mode{Name: name}
	if t := l.OptTable(2, nil); t != nil {
		m.Exclusive = lua.LVAsBool(t.RawGetString("exclusive"))
		m.Once = lua.LVAsBool(t.RawGetString("once"))
	}
	c.Modes = slicesDelete(c.Modes, func(o Mode) bool { return o.Name == name })
	c.Modes = append(c.Modes, m)
	return 0
}

func (c *Config) action(l *lua.LState) int {
	t := l.CheckTable(1)
	name := strings.TrimSpace(lua.LVAsString(t.RawGetString("name")))
	if name == "" {
		l.RaiseError("gorex.action: name is required")
		return 0
	}
	if strings.Contains(name, ".") {
		l.RaiseError("gorex.action: a custom action name has no dot")
		return 0
	}
	fn, ok := t.RawGetString("run").(*lua.LFunction)
	if !ok {
		l.RaiseError("gorex.action: run must be a function")
		return 0
	}
	a := Action{
		Name: name, Title: lua.LVAsString(t.RawGetString("title")),
		Description: lua.LVAsString(t.RawGetString("description")), run: fn,
	}
	if at, ok := t.RawGetString("args").(*lua.LTable); ok {
		var err error
		if a.Args, err = parseArgs(at); err != nil {
			l.RaiseError("gorex.action %s: %v", name, err)
			return 0
		}
	}
	c.Actions = slicesDelete(c.Actions, func(o Action) bool { return o.Name == name })
	c.Actions = append(c.Actions, a)
	return 0
}

func (c *Config) invokeFn(l *lua.LState) int {
	name := l.CheckString(1)
	args := map[string]any{}
	if t := l.OptTable(2, nil); t != nil {
		args = tableMap(t)
	}
	if c.invoke == nil {
		l.RaiseError("gorex.invoke: no action host")
		return 0
	}
	if err := c.invoke(name, args); err != nil {
		l.RaiseError("gorex.invoke %s: %v", name, err)
		return 0
	}
	return 0
}

// Invoke runs a custom action with ctx and args, for at most 5 seconds.
func (c *Config) Invoke(name string, ctxVals, args map[string]any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.InvokeWithin(ctx, name, ctxVals, args)
}

// InvokeWithin runs a custom action with ctxVals and args, and stops it
// when ctx ends. It returns the action's error, or one naming the action.
func (c *Config) InvokeWithin(ctx context.Context, name string, ctxVals, args map[string]any) error {
	if c == nil || c.state == nil {
		return errors.New("config: no Lua state")
	}
	c.invoking++
	defer func() {
		c.invoking--
		if c.invoking == 0 && c.retire {
			c.closeNow()
		}
	}()
	for _, a := range c.Actions {
		if a.Name != name {
			continue
		}
		c.state.Push(a.run)
		c.state.Push(goTable(c.state, ctxVals))
		c.state.Push(goTable(c.state, args))
		c.state.SetContext(ctx)
		err := c.state.PCall(2, 0, nil)
		c.state.RemoveContext()
		return err
	}
	return fmt.Errorf("config: no action %q", name)
}

// check records bindings that shadow or prefix another in their mode.
func (c *Config) check() {
	for i, b := range c.Bindings {
		for j, o := range c.Bindings {
			if i == j || b.Mode != o.Mode {
				continue
			}
			bk, ok := normalizeKey(b.Key), normalizeKey(o.Key)
			if bk == ok && i < j {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s: %s replaces %s", b.Key, o.Action, b.Action))
			}
			if strings.HasPrefix(ok, bk+">") {
				c.Warnings = append(c.Warnings, fmt.Sprintf("%s is a prefix of %s; it will not run alone", b.Key, o.Key))
			}
		}
	}
}

func normalizeKey(s string) string {
	parts := strings.Split(s, ">")
	for i := range parts {
		parts[i] = strings.ToLower(strings.TrimSpace(parts[i]))
	}
	return strings.Join(parts, ">")
}

// Path returns the configuration file: GOREX_CONFIG, else the usual XDG
// path.
func Path() string {
	if p := strings.TrimSpace(getenv("GOREX_CONFIG")); p != "" {
		return p
	}
	base := strings.TrimSpace(getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home := strings.TrimSpace(getenv("HOME"))
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "gorex", "init.lua")
}

// Dir returns the configuration directory holding init.lua, or "" when none.
func Dir() string {
	p := Path()
	if p == "" {
		return ""
	}
	return filepath.Dir(p)
}

func (c *Config) iconPacksFn(l *lua.LState) int {
	var packs []string
	if l.GetTop() == 1 {
		if tbl, ok := l.Get(1).(*lua.LTable); ok {
			tbl.ForEach(func(_, v lua.LValue) {
				if s := strings.TrimSpace(v.String()); s != "" {
					packs = append(packs, s)
				}
			})
			c.IconPacks = packs
			return 0
		}
	}
	for i := 1; i <= l.GetTop(); i++ {
		if s := strings.TrimSpace(l.CheckString(i)); s != "" {
			packs = append(packs, s)
		}
	}
	c.IconPacks = packs
	return 0
}

func (c *Config) iconPackPriorityFn(l *lua.LState) int {
	p := strings.ToLower(strings.TrimSpace(l.CheckString(1)))
	if p != "pack" && p != "osc" {
		l.ArgError(1, "expected 'pack' or 'osc'")
		return 0
	}
	c.IconPackPriority = p
	return 0
}

func (c *Config) programIconFn(l *lua.LState) int {
	name := strings.TrimSpace(l.CheckString(1))
	if name == "" {
		l.ArgError(1, "program name must not be empty")
		return 0
	}
	t := l.CheckTable(2)
	rule := ProgramIconRule{
		Name:   name,
		Icon:   strings.TrimSpace(lua.LVAsString(t.RawGetString("icon"))),
		Format: strings.TrimSpace(lua.LVAsString(t.RawGetString("format"))),
		Mode:   strings.TrimSpace(lua.LVAsString(t.RawGetString("mode"))),
		Bg:     strings.TrimSpace(lua.LVAsString(t.RawGetString("bg"))),
		Fg:     strings.TrimSpace(lua.LVAsString(t.RawGetString("fg"))),
	}
	if rule.Icon == "" {
		l.ArgError(2, "icon path is required")
		return 0
	}
	c.ProgramIcons = append(c.ProgramIcons, rule)
	return 0
}

// getenv is a variable for tests.
var getenv = os.Getenv

func parseArgs(t *lua.LTable) ([]Arg, error) {
	var out []Arg
	var err error
	t.ForEach(func(k, v lua.LValue) {
		if err != nil {
			return
		}
		name := lua.LVAsString(k)
		typ := strings.TrimSpace(lua.LVAsString(v))
		optional := strings.HasSuffix(typ, "?")
		typ = strings.TrimSuffix(typ, "?")
		switch typ {
		case "string", "number", "integer", "boolean", "any":
		default:
			err = fmt.Errorf("argument %s has unknown type %q", name, typ)
			return
		}
		out = append(out, Arg{Name: name, Type: typ, Optional: optional})
	})
	return out, err
}

func tableMap(t *lua.LTable) map[string]any {
	m := map[string]any{}
	t.ForEach(func(k, v lua.LValue) { m[lua.LVAsString(k)] = luaValue(v) })
	return m
}

func luaValue(v lua.LValue) any {
	switch x := v.(type) {
	case *lua.LTable:
		return tableMap(x)
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	default:
		return nil
	}
}

func goTable(l *lua.LState, m map[string]any) *lua.LTable {
	t := l.NewTable()
	for k, v := range m {
		switch x := v.(type) {
		case string:
			t.RawSetString(k, lua.LString(x))
		case bool:
			t.RawSetString(k, lua.LBool(x))
		case float64:
			t.RawSetString(k, lua.LNumber(x))
		case int:
			t.RawSetString(k, lua.LNumber(x))
		case int64:
			t.RawSetString(k, lua.LNumber(x))
		case []any:
			a := l.NewTable()
			for i, v := range x {
				a.RawSetInt(i+1, lua.LString(fmt.Sprint(v)))
			}
			t.RawSetString(k, a)
		case map[string]any:
			t.RawSetString(k, goTable(l, x))
		}
	}
	return t
}

func slicesDelete[T any](s []T, drop func(T) bool) []T {
	out := s[:0]
	for _, v := range s {
		if !drop(v) {
			out = append(out, v)
		}
	}
	return out
}
