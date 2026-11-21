package main

import (
	"errors"
	"log"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"gorex/internal/config"
	"gorex/internal/rex"
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
			if it.Selected {
				a.promptPicked[it.ID] = true
			}
		}
	case "ended":
		if a.prompt != nil && a.prompt.CallID == p.CallID {
			a.prompt = nil
		}
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
		backdrop.Background(k.backdrop)
		dialogShadow(glassPanel(panel.Width(460).MaxHeight(560).Padding(20).Radius(radiusXXL), k.panel).Border(1, k.panelBorder)).
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
				if ui.Button(c, "Cancel").Clicked() {
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
		// A pick: its items, a check on the chosen ones, and Other when the
		// action allows text in their place.
		submit := false
		ui.Scroll(c.Key("prompt-items")).MinHeight(0).Shrink(1).Children(func() {
			ui.Column(c).Gap(4).Children(func() {
				for _, it := range spec.Items {
					item := it
					row := ui.Row(c.Key("prompt-"+item.ID)).Height(38).Padding(0, 10).Gap(10).
						Radius(concentric(radiusXXL, 1)).AlignItems(ui.Center).Cursor(ui.CursorPointer)
					chosen := a.promptPicked[item.ID]
					if chosen {
						row = row.Background(k.panelSel)
					}
					row.Children(func() {
						ui.Column(c).Grow(1).Gap(1).Children(func() {
							ui.Text(c, item.Title).FontSize(fontBody).TextColor(k.text)
							if item.Subtitle != "" {
								ui.Text(c, item.Subtitle).FontSize(fontFootnote).TextColor(k.textMuted)
							}
							if item.Detail != "" {
								ui.Text(c, item.Detail).FontSize(fontFootnote).TextColor(k.textFaint)
							}
						})
						if chosen {
							glyphIcon(c, "check", 15, k.accent)
						}
					})
					if row.Clicked() {
						if spec.Multiple {
							a.promptPicked[item.ID] = !chosen
						} else {
							a.promptPicked = map[string]bool{item.ID: true}
						}
					}
				}
			})
		})
		if spec.AllowOther {
			textField(c.Key("prompt-other"), k, &a.promptOther, fieldRegular).
				Placeholder("Other").Margin(8, 0, 0, 0)
		}
		chosen := len(a.promptPicked) > 0
		other := strings.TrimSpace(a.promptOther)
		ui.Row(c).Gap(8).Justify(ui.End).Margin(14, 0, 0, 0).Children(func() {
			if ui.Button(c, "Cancel").Clicked() {
				open = false
			}
			disabled := !chosen && !(spec.AllowOther && other != "")
			if ui.PrimaryButton(c, confirmLabel(spec)).Disabled(disabled).Clicked() && !disabled {
				submit = true
			}
		})
		if submit {
			open = false
			if spec.AllowOther && other != "" {
				a.answerPrompt(rex.PromptAnswer{Text: other})
			} else {
				ids := make([]string, 0, len(spec.Items))
				for _, it := range spec.Items {
					if a.promptPicked[it.ID] {
						ids = append(ids, it.ID)
					}
				}
				a.answerPrompt(rex.PromptAnswer{IDs: ids})
			}
		}
	})
}

// confirmLabel is the question's confirm button: the action's own, or Send.
func confirmLabel(spec *rex.PromptSpec) string {
	if spec.Confirm != "" {
		return spec.Confirm
	}
	return "Send"
}
