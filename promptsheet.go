package main

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/rex"
	"gorex/locales"
)

// actionLimit is how long a config action may run, apart from the time its
// questions take.
const actionLimit = 5 * time.Second

// askUI asks the host's clients a question, from a config action, and
// waits for the first answer: this window shows it too, as every client
// that can (docs/MOD-009-client-prompts.md).
func (a *App) askUI(action string, opts config.UIOptions) (config.UIAnswer, error) {
	c := a.client
	if c == nil {
		return config.UIAnswer{}, errors.New("gorex.ui: no server connection")
	}
	spec := rex.PromptSpec{
		Kind:        opts.Kind,
		Title:       opts.Title,
		Placeholder: opts.Placeholder,
		Value:       opts.Value,
		Confirm:     opts.Confirm,
		Multiple:    opts.Multiple,
		AllowOther:  opts.AllowOther,
	}
	for _, it := range opts.Items {
		spec.Items = append(spec.Items, rex.PromptItem{
			ID: it.ID, Title: it.Title, Subtitle: it.Subtitle,
			Icon: it.Icon, Detail: it.Detail, Selected: it.Selected,
		})
	}
	ans, err := c.PromptOpen(action, spec, opts.Timeout)
	if err != nil {
		return config.UIAnswer{}, err
	}
	return config.UIAnswer{Text: ans.Text, IDs: ans.IDs, Cancelled: ans.Cancelled}, nil
}

// promptEvent takes a prompt event: a question to show, or the end of one
// that another device answered, or that timed out. Call it on the main
// thread.
func (a *App) promptEvent(p *rex.PromptEvent) {
	switch p.Op {
	case "requested":
		if p.Spec == nil {
			return
		}
		a.prompt = p
		a.promptText = p.Spec.Value
		a.promptOther = ""
		a.promptPicked = map[string]bool{}
		for _, it := range p.Spec.Items {
			// A question that takes one answer starts with at most one chosen.
			if it.Selected && (p.Spec.Multiple || len(a.promptPicked) == 0) {
				a.promptPicked[it.ID] = true
			}
		}
	case "ended":
		if a.prompt != nil && a.prompt.CallID == p.CallID {
			a.prompt = nil
		}
	}
	// The question comes between frames: draw it now.
	if a.win != nil {
		a.win.Update(func() {})
	}
}

// answerPrompt answers the question this window shows, and clears it.
func (a *App) answerPrompt(ans rex.PromptAnswer) {
	p := a.prompt
	a.prompt = nil
	if p == nil {
		return
	}
	c := a.client
	if c == nil {
		return
	}
	// The answer goes back on the connection that received the question.
	go func() {
		if err := c.PromptReply(p.CallID, ans); err != nil {
			log.Printf("gorex: answering a question: %v", err)
		}
	}()
}

// promptPanel shows the question this window was asked.
func (a *App) promptPanel(c *ui.Context, k *colors) {
	p := a.prompt
	if p == nil || p.Spec == nil {
		return
	}
	spec := p.Spec
	open := true
	defer func() {
		if !open && a.prompt == p {
			// The backdrop, or Escape, closed it: the action gets a cancel.
			a.answerPrompt(rex.PromptAnswer{Cancelled: true})
		}
	}()
	ui.DialogBase(c.Key("prompt"), &open, func(backdrop, panel ui.Element) {
		dialogEnter(c, backdrop, panel)
		backdrop.Background(k.backdrop)
		// Opaque, of the window's theme: the system's glass takes the system's
		// appearance, which an app theme of the other one makes unreadable.
		dialogShadow(panel.Width(460).MaxHeight(560).Padding(20).Radius(k.radius(radiusXXL)).Background(k.panel).Border(k.borderW(), k.panelBorder), k).
			Role(ui.RoleDialog).Label(spec.Title)
		if p2 := p.Action; p2 != "" {
			ui.Text(c, p2).FontSize(fontFootnote).TextColor(k.textFaint).Margin(0, 0, 4, 0)
		}
		ui.Text(c, spec.Title).FontSize(fontTitle).FontWeight(700).TextColor(k.text).Margin(0, 0, 12, 0)
		if in := c.Shortcut(0, ui.KeyEscape); in {
			open = false
			return
		}
		if spec.Kind == "input" {
			in := textField(c.Key("prompt-answer"), k, &a.promptText, fieldRegular).
				Placeholder(spec.Placeholder).AutoFocus()
			submit := in.Submitted()
			ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
				if ui.Button(c, locales.PromptCancel()).Clicked() {
					open = false
				}
				if ui.PrimaryButton(c, confirmLabel(spec)).Clicked() {
					submit = true
				}
			})
			if submit {
				open = false
				a.answerPrompt(rex.PromptAnswer{Text: a.promptText})
			}
			return
		}
		// A pick: its items, and Other when the action allows text in their
		// place. One answer shows radio rows in one radio group, several show
		// check box rows, so that the marks say how many it takes. The arrows
		// move over the rows (they choose one in a radio group), Space chooses
		// or checks the row, and Enter answers once something is picked.
		other := strings.TrimSpace(a.promptOther)
		result, ready := a.promptResult(spec, other)
		submit := c.Shortcut(0, ui.KeyEnter)
		ui.Scroll(c.Key("prompt-items")).MinHeight(0).Shrink(1).Children(func() {
			if spec.Multiple {
				ui.Column(c).Gap(4).Role(ui.RoleGroup).Label(spec.Title).FocusGroup(ui.Vertical).Children(func() {
					a.promptRows(c, k, spec)
				})
				return
			}
			ui.RadioGroup(c, func() {
				a.promptRows(c, k, spec)
			}).Gap(4).Label(spec.Title)
		})
		if spec.AllowOther {
			textField(c.Key("prompt-other"), k, &a.promptOther, fieldRegular).
				Placeholder(locales.PromptOther()).Margin(8, 0, 0, 0)
		}
		ui.Row(c).Gap(8).Justify(ui.End).AlignItems(ui.Center).Margin(14, 0, 0, 0).Children(func() {
			var why ui.Element
			if !ready {
				// The disabled confirm says why.
				why = ui.Text(c, locales.PromptPick()).FontSize(fontFootnote).TextColor(k.textFaint).Grow(1)
			}
			if ui.Button(c, locales.PromptCancel()).Clicked() {
				open = false
			}
			send := ui.PrimaryButton(c, confirmLabel(spec)).Disabled(!ready)
			if !ready {
				send.DescribedBy(why)
			}
			if send.Clicked() {
				submit = true
			}
		})
		if submit && ready {
			open = false
			a.answerPrompt(result)
		}
	})
}

// confirmLabel is the question's confirm button: the action's own, or Send.
func confirmLabel(spec *rex.PromptSpec) string {
	if spec.Confirm != "" {
		return spec.Confirm
	}
	return locales.PromptSend()
}

// promptResult is the answer of a pick, and whether it can be given: a typed
// Other text where the action allows one, else the rows chosen in their
// order. It can not while nothing is chosen, and the confirm is off.
func (a *App) promptResult(spec *rex.PromptSpec, other string) (rex.PromptAnswer, bool) {
	if spec.AllowOther && other != "" {
		return rex.PromptAnswer{Text: other}, true
	}
	var ids []string
	for _, it := range spec.Items {
		if a.promptPicked[it.ID] {
			ids = append(ids, it.ID)
		}
	}
	return rex.PromptAnswer{IDs: ids}, len(ids) > 0
}

// promptRows builds the rows of a pick, inside its radio group or its check
// box group: a radio button each when the question takes one answer, a check
// box each when it takes several. A radio group takes Tab to the row chosen,
// else the first, which also takes the focus as the sheet opens.
func (a *App) promptRows(c *ui.Context, k *colors, spec *rex.PromptSpec) {
	choice := ""
	for _, it := range spec.Items {
		if a.promptPicked[it.ID] {
			choice = it.ID
			break
		}
	}
	for i, it := range spec.Items {
		item := it
		chosen := a.promptPicked[item.ID]
		var row ui.Element
		var on bool
		if spec.Multiple {
			on = chosen
			row = ui.CheckboxBase(c.Key("prompt-"+item.ID), &on)
		} else {
			row = ui.RadioBase(c.Key("prompt-"+item.ID), &choice, item.ID).PositionInSet(i+1, len(spec.Items))
		}
		row = row.MinHeight(38).Padding(4, 10).Gap(10).Radius(concentric(k.radius(radiusXXL), 1)).
			AlignItems(ui.Center).Cursor(ui.CursorPointer).Label(item.Title)
		if desc := strings.TrimSpace(item.Subtitle + " " + item.Detail); desc != "" {
			row = row.Description(desc)
		}
		switch {
		case chosen:
			row = row.Background(k.panelSel)
		case row.Hovered():
			row = row.Background(k.hover)
		}
		if i == 0 && (spec.Multiple || choice == "") || item.ID == choice {
			row = row.AutoFocus()
		}
		row.Children(func() {
			choiceMark(c, k, spec.Multiple, chosen)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, item.Title).FontSize(fontBody).TextColor(k.text)
				if item.Subtitle != "" {
					ui.Text(c, item.Subtitle).FontSize(fontFootnote).TextColor(k.textMuted)
				}
				if item.Detail != "" {
					ui.Text(c, item.Detail).FontSize(fontFootnote).TextColor(k.textFaint)
				}
			})
		})
		if row.Changed() {
			if spec.Multiple {
				a.promptPicked[item.ID] = on
			} else {
				a.promptPicked = map[string]bool{item.ID: true}
			}
		}
	}
}

// choiceMark is the mark of a pick's row, which tells how many answers the
// question takes: a circle for one (a dot in it when chosen), a rounded
// square for several (a check in it when checked).
func choiceMark(c *ui.Context, k *colors, multiple, chosen bool) {
	r := k.capsule(choiceSize)
	if multiple {
		r = k.radius(choiceRadius)
	}
	m := ui.Row(c).Width(choiceSize).Height(choiceSize).Shrink(0).Radius(r).AlignItems(ui.Center).Justify(ui.Center)
	if !chosen {
		m.Border(choiceBorder, k.iconMuted)
		return
	}
	m.Background(k.accent).Border(choiceBorder, k.accent).Children(func() {
		if multiple {
			glyphIcon(c, "check", choiceCheck, k.accentText)
			return
		}
		ui.Row(c).Width(choiceDot).Height(choiceDot).Radius(k.capsule(choiceDot)).Background(k.accentText)
	})
}
