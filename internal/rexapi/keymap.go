package rexapi

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// keymapBinding is a binding of client.keymap, in a request's layer or in
// the result.
type keymapBinding struct {
	Keys   []string        `json:"keys"`
	Action string          `json:"action"`
	Args   json.RawMessage `json:"args,omitempty"`
	Source string          `json:"source,omitempty"`
	Mode   string          `json:"mode,omitempty"`
}

// keymapRemoval takes bindings off the layers beneath its own.
type keymapRemoval struct {
	Keys   []string `json:"keys"`
	Action string   `json:"action,omitempty"`
	Mode   string   `json:"mode,omitempty"`
}

// keymapLayer is one of the layers a client stacks: its defaults below
// the configuration file, its overrides above.
type keymapLayer struct {
	Name     string          `json:"name"`
	Bindings []keymapBinding `json:"bindings"`
	Removals []keymapRemoval `json:"removals"`
}

// keymapMode is a mode of the result.
type keymapMode struct {
	Name      string     `json:"name"`
	Exclusive bool       `json:"exclusive"`
	Blocked   [][]string `json:"blocked,omitempty"`
}

// keymapDiagnostic is a problem of the result.
type keymapDiagnostic struct {
	Severity string `json:"severity"`
	Source   string `json:"source,omitempty"`
	Message  string `json:"message"`
	Action   string `json:"action,omitempty"`
	Key      string `json:"key,omitempty"`
	Mode     string `json:"mode,omitempty"`
}

var modeName = regexp.MustCompile(`^[a-z0-9_-]+$`)

// resolveKeymap stacks the client's layers: its defaults, then the
// configuration file and the client's scripts, which GoRex has none of,
// then its overrides. Each layer's removals take bindings off the layers
// beneath it, never its own; then its bindings go on top. The result
// lists the bindings to try first first: a higher layer's before a lower
// one's, and within a layer, a later binding before an earlier one.
func resolveKeymap(defaults, overrides []keymapLayer) ([]keymapBinding, []keymapMode, []keymapDiagnostic, *apiError) {
	seen := map[string]bool{}
	for _, l := range slices.Concat(defaults, overrides) {
		switch {
		case l.Name == "":
			return nil, nil, nil, errBadRequest("a layer's name is empty")
		case strings.Contains(l.Name, ":"):
			return nil, nil, nil, errBadRequest("layer name %s contains a colon", l.Name)
		case seen[l.Name]:
			return nil, nil, nil, errBadRequest("layer name %s is used by another layer", l.Name)
		}
		seen[l.Name] = true
		for _, b := range l.Bindings {
			if b.Mode != "" && (b.Mode == "root" || !modeName.MatchString(b.Mode)) {
				return nil, nil, nil, errBadRequest("mode name %s is not valid", b.Mode)
			}
		}
		for _, r := range l.Removals {
			if r.Mode != "" && (r.Mode == "root" || !modeName.MatchString(r.Mode)) {
				return nil, nil, nil, errBadRequest("mode name %s is not valid", r.Mode)
			}
			if len(r.Keys) == 0 && r.Mode != "" && r.Action != "" {
				return nil, nil, nil, errBadRequest("removing mode %s takes no action", r.Mode)
			}
		}
	}
	var stack []keymapBinding
	modes := map[string]*keymapMode{}
	var diags []keymapDiagnostic
	for i, l := range slices.Concat(defaults, overrides) {
		inDefaults := i < len(defaults)
		for _, r := range l.Removals {
			if len(r.Keys) == 0 && r.Mode != "" {
				stack = slices.DeleteFunc(stack, func(b keymapBinding) bool { return b.Mode == r.Mode })
				delete(modes, r.Mode)
				continue
			}
			before := len(stack)
			stack = slices.DeleteFunc(stack, func(b keymapBinding) bool {
				return b.Mode == r.Mode && slices.Equal(b.Keys, r.Keys) && (r.Action == "" || b.Action == r.Action)
			})
			if r.Mode != "" && r.Action == "" {
				m := mode(modes, r.Mode)
				m.Blocked = append(m.Blocked, r.Keys)
			}
			if len(stack) == before && !inDefaults && !(r.Mode != "" && r.Action == "") {
				diags = append(diags, keymapDiagnostic{
					Severity: "warning", Source: l.Name, Action: r.Action,
					Key: strings.Join(r.Keys, " "), Mode: r.Mode,
					Message: "The removal removes nothing.",
				})
			}
		}
		for _, b := range l.Bindings {
			b.Source = l.Name
			if b.Keys == nil {
				b.Keys = []string{}
			}
			stack = append(stack, b)
			if b.Mode != "" {
				mode(modes, b.Mode)
			}
		}
	}
	slices.Reverse(stack)
	var ms []keymapMode
	for _, m := range modes {
		ms = append(ms, *m)
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].Name < ms[j].Name })
	if stack == nil {
		stack = []keymapBinding{}
	}
	if diags == nil {
		diags = []keymapDiagnostic{}
	}
	return stack, ms, diags, nil
}

func mode(modes map[string]*keymapMode, name string) *keymapMode {
	m := modes[name]
	if m == nil {
		m = &keymapMode{Name: name}
		modes[name] = m
	}
	return m
}

// keymapAction is an action client.keymap lists.
type keymapAction struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`
	Title       string          `json:"title,omitempty"`
	Description string          `json:"description,omitempty"`
	Category    string          `json:"category,omitempty"`
	Keywords    string          `json:"keywords,omitempty"`
	Args        json.RawMessage `json:"args,omitempty"`
}

// terminalActions are the server's actions, as Rex lists them: GoRex has
// no configuration file, so they are the terminal's, sorted by name. A
// method with block actions is listed as those actions, with a title and
// no args; any other method by its description, with its input schema as
// args, to bind but not to show in a menu.
func terminalActions() []keymapAction {
	var creator struct {
		Name string `json:"name"`
		API  struct {
			Methods map[string]struct {
				Description string          `json:"description"`
				Input       json.RawMessage `json:"input_schema"`
				Actions     []struct {
					Name        string `json:"name"`
					Title       string `json:"title"`
					Description string `json:"description"`
					Category    string `json:"category"`
					Keywords    string `json:"keywords"`
				} `json:"actions"`
			} `json:"methods"`
		} `json:"api"`
	}
	if json.Unmarshal(terminalCreatorJSON, &creator) != nil {
		return nil
	}
	var out []keymapAction
	for name, m := range creator.API.Methods {
		if len(m.Actions) == 0 {
			out = append(out, keymapAction{Name: creator.Name + "." + name, Kind: "block", Description: m.Description, Args: m.Input})
			continue
		}
		for _, act := range m.Actions {
			out = append(out, keymapAction{
				Name: creator.Name + "." + act.Name, Kind: "block", Title: act.Title,
				Description: act.Description, Category: act.Category, Keywords: act.Keywords,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
