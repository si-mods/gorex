package main

import (
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorex/internal/rex"
)

// askPick shows a pick of three items in a's window, as the server's
// prompt event does.
func askPick(t *testing.T, a *App, tt *ui.Tester, multiple, allowOther bool) *rex.PromptSpec {
	t.Helper()
	spec := &rex.PromptSpec{
		Kind: "pick", Title: "Which branch?", Multiple: multiple, AllowOther: allowOther,
		Items: []rex.PromptItem{
			{ID: "a", Title: "alpha", Subtitle: "~/alpha"},
			{ID: "b", Title: "bravo"},
			{ID: "c", Title: "charlie", Detail: "Last used"},
		},
	}
	a.promptEvent(&rex.PromptEvent{Op: "requested", CallID: "7", Action: "Open project", Spec: spec})
	tt.Frame()
	if a.prompt == nil {
		t.Fatalf("the question did not show; texts %q", tt.Texts())
	}
	return spec
}

// roles counts the elements of the last frame that have role r.
func roles(tt *ui.Tester, r ui.Role) int {
	n := 0
	for _, info := range tt.AccessAll() {
		if info.Role == r {
			n++
		}
	}
	return n
}

// TestPromptPickOneShowsRadios checks a question that takes one answer: its
// rows are radio buttons in one radio group, each told as "n of 3", only
// the one chosen is on, and the arrow keys choose among them.
func TestPromptPickOneShowsRadios(t *testing.T) {
	a, tt := newTestApp(t)
	spec := askPick(t, a, tt, false, false)

	if got := roles(tt, ui.RoleRadio); got != 3 {
		t.Fatalf("%d radio buttons, want 3; texts %q", got, tt.Texts())
	}
	if got := roles(tt, ui.RoleCheckBox); got != 0 {
		t.Errorf("%d check boxes in a question that takes one answer", got)
	}
	if _, ok := tt.AccessByRole(ui.RoleRadioGroup, "Which branch?"); !ok {
		t.Errorf("no radio group named for the question")
	}
	for i, name := range []string{"alpha", "bravo", "charlie"} {
		info, ok := tt.AccessByRole(ui.RoleRadio, name)
		if !ok {
			t.Fatalf("no radio button %q", name)
		}
		if info.PosInSet != i+1 || info.SetSize != 3 {
			t.Errorf("%s is %d of %d, want %d of 3", name, info.PosInSet, info.SetSize, i+1)
		}
		if info.Checked {
			t.Errorf("%s is on before a pick", name)
		}
	}

	if err := tt.Click("bravo"); err != nil {
		t.Fatalf("%v; texts %q", err, tt.Texts())
	}
	tt.Frame()
	checkChosen(t, tt, ui.RoleRadio, "bravo")

	// The arrows choose the next row, and the previous one.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	checkChosen(t, tt, ui.RoleRadio, "charlie")
	tt.Key(0, ui.KeyUp)
	tt.Key(0, ui.KeyUp)
	tt.Frame()
	checkChosen(t, tt, ui.RoleRadio, "alpha")

	// Another click moves the choice: a radio group keeps one.
	if err := tt.Click("charlie"); err != nil {
		t.Fatalf("%v", err)
	}
	tt.Frame()
	checkChosen(t, tt, ui.RoleRadio, "charlie")
	if got, ok := a.promptResult(spec, ""); !ok || !slices.Equal(got.IDs, []string{"c"}) {
		t.Errorf("answer %+v (%v), want c", got, ok)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.prompt != nil {
		t.Errorf("Enter did not answer the question")
	}
}

// checkChosen checks that the row named name is the only one of role r that
// is on.
func checkChosen(t *testing.T, tt *ui.Tester, r ui.Role, name string) {
	t.Helper()
	for _, other := range []string{"alpha", "bravo", "charlie"} {
		info, ok := tt.AccessByRole(r, other)
		if !ok {
			t.Fatalf("no row %q; texts %q", other, tt.Texts())
		}
		if info.Checked != (other == name) {
			t.Errorf("%s: checked %v, want %v", other, info.Checked, other == name)
		}
	}
}

// TestPromptPickSeveralShowsCheckboxes checks a question that takes several
// answers: its rows are check boxes, which a click or Space checks and
// unchecks one by one, and the answer lists the ones checked in order.
func TestPromptPickSeveralShowsCheckboxes(t *testing.T) {
	a, tt := newTestApp(t)
	spec := askPick(t, a, tt, true, false)

	if got := roles(tt, ui.RoleCheckBox); got != 3 {
		t.Fatalf("%d check boxes, want 3; texts %q", got, tt.Texts())
	}
	if got := roles(tt, ui.RoleRadio); got != 0 {
		t.Errorf("%d radio buttons in a question that takes several answers", got)
	}
	if got := roles(tt, ui.RoleRadioGroup); got != 0 {
		t.Errorf("%d radio groups in a question that takes several answers", got)
	}

	for _, name := range []string{"charlie", "alpha"} {
		if err := tt.Click(name); err != nil {
			t.Fatalf("%v; texts %q", err, tt.Texts())
		}
		tt.Frame()
	}
	for name, want := range map[string]bool{"alpha": true, "bravo": false, "charlie": true} {
		if info, _ := tt.AccessByRole(ui.RoleCheckBox, name); info.Checked != want {
			t.Errorf("%s: checked %v, want %v", name, info.Checked, want)
		}
	}
	if got, ok := a.promptResult(spec, ""); !ok || !slices.Equal(got.IDs, []string{"a", "c"}) {
		t.Errorf("answer %+v (%v), want a and c", got, ok)
	}

	// Another click unchecks.
	if err := tt.Click("alpha"); err != nil {
		t.Fatalf("%v", err)
	}
	tt.Frame()
	if info, _ := tt.AccessByRole(ui.RoleCheckBox, "alpha"); info.Checked {
		t.Errorf("a second click left alpha checked")
	}
	if got, _ := a.promptResult(spec, ""); !slices.Equal(got.IDs, []string{"c"}) {
		t.Errorf("answer %+v, want c", got)
	}
}

// TestPromptCheckboxKeys checks the keyboard on check boxes: the first row has
// the focus as the sheet opens, Space checks it, and the down arrow moves the
// focus to the next row, which Space checks too.
func TestPromptCheckboxKeys(t *testing.T) {
	a, tt := newTestApp(t)
	spec := askPick(t, a, tt, true, false)
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if got, ok := a.promptResult(spec, ""); !ok || !slices.Equal(got.IDs, []string{"a", "b"}) {
		t.Errorf("answer %+v (%v), want a and b", got, ok)
	}
	for name, want := range map[string]bool{"alpha": true, "bravo": true, "charlie": false} {
		if info, _ := tt.AccessByRole(ui.RoleCheckBox, name); info.Checked != want {
			t.Errorf("%s: checked %v, want %v", name, info.Checked, want)
		}
	}
}

// TestPromptSendSaysWhyItIsOff checks the confirm of a pick: it is off while
// nothing is picked, with the reason beside it and as its description, and
// on once a row is picked or, where the action allows it, Other has text. It
// holds for both kinds.
func TestPromptSendSaysWhyItIsOff(t *testing.T) {
	for _, multiple := range []bool{false, true} {
		a, tt := newTestApp(t)
		askPick(t, a, tt, multiple, true)

		send, ok := tt.AccessByRole(ui.RoleButton, "Send")
		if !ok {
			t.Fatalf("multiple %v: no Send; texts %q", multiple, tt.Texts())
		}
		if !send.Disabled || !tt.HasText("Pick an answer") || send.Description != "Pick an answer" {
			t.Errorf("multiple %v: Send disabled %v, reason shown %v, description %q",
				multiple, send.Disabled, tt.HasText("Pick an answer"), send.Description)
		}
		// Enter answers nothing while the confirm is off.
		tt.Key(0, ui.KeyEnter)
		tt.Frame()
		if a.prompt == nil {
			t.Fatalf("multiple %v: Enter answered with nothing picked", multiple)
		}

		if err := tt.Click("bravo"); err != nil {
			t.Fatalf("multiple %v: %v", multiple, err)
		}
		tt.Frame()
		send, _ = tt.AccessByRole(ui.RoleButton, "Send")
		if send.Disabled || tt.HasText("Pick an answer") {
			t.Errorf("multiple %v: Send still off after a pick: disabled %v, reason shown %v",
				multiple, send.Disabled, tt.HasText("Pick an answer"))
		}

		// A typed Other answers in place of the rows.
		spec := a.prompt.Spec
		if got, ok := a.promptResult(spec, "elsewhere"); !ok || got.Text != "elsewhere" {
			t.Errorf("multiple %v: Other answered %+v (%v)", multiple, got, ok)
		}
		a.promptPicked = map[string]bool{}
		if _, ok := a.promptResult(spec, ""); ok {
			t.Errorf("multiple %v: an answer with nothing picked and no Other", multiple)
		}
		if got, ok := a.promptResult(spec, "elsewhere"); !ok || got.Text != "elsewhere" {
			t.Errorf("multiple %v: Other alone answered %+v (%v)", multiple, got, ok)
		}
	}
}

// TestPromptPickOneKeepsOneSelected checks that a question that takes one
// answer starts with at most one row chosen, whatever the action marked.
func TestPromptPickOneKeepsOneSelected(t *testing.T) {
	a, _ := newTestApp(t)
	items := []rex.PromptItem{{ID: "a", Title: "alpha", Selected: true}, {ID: "b", Title: "bravo", Selected: true}}
	a.promptEvent(&rex.PromptEvent{Op: "requested", CallID: "1", Spec: &rex.PromptSpec{Kind: "pick", Title: "Pick", Items: items}})
	if len(a.promptPicked) != 1 || !a.promptPicked["a"] {
		t.Errorf("one answer: picked %v, want only a", a.promptPicked)
	}
	a.promptEvent(&rex.PromptEvent{Op: "requested", CallID: "2", Spec: &rex.PromptSpec{Kind: "pick", Title: "Pick", Multiple: true, Items: items}})
	if len(a.promptPicked) != 2 {
		t.Errorf("several answers: picked %v, want both", a.promptPicked)
	}
}
