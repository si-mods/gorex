package rexapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"gorex/internal/config"
)

// promptItem is one choice of a pick prompt, as the event carries it.
type promptItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Selected bool   `json:"selected,omitempty"`
}

// promptSpec is the question, as the event carries it.
type promptSpec struct {
	Kind        string       `json:"kind"`
	Title       string       `json:"title"`
	Placeholder string       `json:"placeholder,omitempty"`
	Value       string       `json:"value,omitempty"`
	Confirm     string       `json:"confirm,omitempty"`
	Multiple    bool         `json:"multiple,omitempty"`
	AllowOther  bool         `json:"allow_other,omitempty"`
	Items       []promptItem `json:"items,omitempty"`
}

// promptCall is one question, offered to the apps that may show it.
type promptCall struct {
	action string
	spec   promptSpec
	answer chan promptResult
	done   bool
}

// promptResult is the answer that the waiting action gets.
type promptResult struct {
	answer config.UIAnswer
	err    error
}

// promptEntry is one connection's copy of a pending question.
type promptEntry struct {
	call   *promptCall
	callID string
	conn   *control
}

// promptRevision is when prompts first appeared.
const promptRevision = 17

// openPrompt sends the question to every eligible app and waits for the
// first answer, a cancel or the timeout.
func (g *Gateway) openPrompt(ctx context.Context, action string, opts config.UIOptions) (config.UIAnswer, error) {
	spec := promptSpec{
		Kind: opts.Kind, Title: opts.Title, Placeholder: opts.Placeholder,
		Value: opts.Value, Confirm: opts.Confirm, Multiple: opts.Multiple,
		AllowOther: opts.AllowOther,
	}
	for _, item := range opts.Items {
		spec.Items = append(spec.Items, promptItem{
			ID: item.ID, Title: item.Title, Subtitle: item.Subtitle,
			Icon: item.Icon, Detail: item.Detail, Selected: item.Selected,
		})
	}
	g.mu.Lock()
	conns := make([]*control, 0, len(g.conns))
	for c := range g.conns {
		conns = append(conns, c)
	}
	g.mu.Unlock()
	call := &promptCall{action: action, spec: spec, answer: make(chan promptResult, 1)}
	var sent int
	g.promptsMu.Lock()
	for _, c := range conns {
		if c.rec == nil || !c.rec.canPrompt(c.rev) {
			continue
		}
		g.promptSeq++
		callID := strconv.FormatInt(g.promptSeq, 10)
		if g.prompts == nil {
			g.prompts = map[string]*promptEntry{}
		}
		g.prompts[callID] = &promptEntry{call: call, callID: callID, conn: c}
		c.rec.setPrompting(true)
		sent++
		c.sendChanges([]change{{server: true, typ: "client_prompt_requested", since: promptRevision, payload: map[string]any{
			"call_id": callID, "action": action, "prompt": spec,
			"timeout_ms": opts.Timeout.Milliseconds(),
		}}})
	}
	g.promptsMu.Unlock()
	if sent == 0 {
		return config.UIAnswer{}, errors.New("no app can show the prompt")
	}
	timer := time.NewTimer(opts.Timeout)
	defer timer.Stop()
	select {
	case res := <-call.answer:
		return res.answer, res.err
	case <-timer.C:
		g.endPrompt(call, "timeout", "")
		return config.UIAnswer{}, errors.New("the prompt timed out")
	case <-ctx.Done():
		g.endPrompt(call, "cancelled", "")
		return config.UIAnswer{}, ctx.Err()
	}
}

// endPrompt closes a call, tells every other connection that its copy
// ended and forgets it.
func (g *Gateway) endPrompt(call *promptCall, reason, except string) {
	g.promptsMu.Lock()
	defer g.promptsMu.Unlock()
	g.endPromptLocked(call, reason, except)
}

func (g *Gateway) endPromptLocked(call *promptCall, reason, except string) {
	if call.done {
		return
	}
	call.done = true
	for callID, e := range g.prompts {
		if e.call != call {
			continue
		}
		delete(g.prompts, callID)
		e.conn.rec.setPrompting(false)
		if callID == except {
			continue
		}
		e.conn.sendChanges([]change{{server: true, typ: "client_prompt_ended", since: promptRevision,
			payload: map[string]any{"call_id": callID, "reason": reason}}})
	}
}

// cancelPrompts ends every pending question when the gateway stops.
func (g *Gateway) cancelPrompts() {
	g.promptsMu.Lock()
	defer g.promptsMu.Unlock()
	calls := map[*promptCall]bool{}
	for _, e := range g.prompts {
		calls[e.call] = true
	}
	for call := range calls {
		g.endPromptLocked(call, "cancelled", "")
		select {
		case call.answer <- promptResult{err: errors.New("the gateway is stopping")}:
		default:
		}
	}
}

// replyPrompt answers a pending question from one connection.
func (g *Gateway) replyPrompt(c *control, callID string, rawResult json.RawMessage, hasResult bool, rawError json.RawMessage, hasError bool) *apiError {
	g.promptsMu.Lock()
	defer g.promptsMu.Unlock()
	e := g.prompts[callID]
	if e == nil || e.conn != c {
		// A question is answered on the connection that received it.
		return errNotFound("nothing is waiting for that call")
	}
	if hasError && string(rawError) != "null" {
		var failure struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rawError, &failure)
		delete(g.prompts, callID)
		e.conn.rec.setPrompting(false)
		if g.pendingCount(e.call) == 0 {
			g.endPromptLocked(e.call, "cancelled", "")
			msg := failure.Message
			if msg == "" {
				msg = failure.Code
			}
			select {
			case e.call.answer <- promptResult{err: fmt.Errorf("no app could show the prompt: %s", msg)}:
			default:
			}
		}
		return nil
	}
	if !hasResult {
		return errBadRequest("send a result or an error, not both")
	}
	ans, aerr := answerFor(e.call.spec, rawResult)
	if aerr != nil {
		return aerr
	}
	delete(g.prompts, callID)
	e.conn.rec.setPrompting(false)
	g.endPromptLocked(e.call, "answered", callID)
	select {
	case e.call.answer <- promptResult{answer: ans}:
	default:
	}
	return nil
}

// pendingCount counts a call's copies still waiting.
func (g *Gateway) pendingCount(call *promptCall) int {
	n := 0
	for _, e := range g.prompts {
		if e.call == call {
			n++
		}
	}
	return n
}

// answerFor checks a prompt result against the question.
func answerFor(spec promptSpec, raw json.RawMessage) (config.UIAnswer, *apiError) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return config.UIAnswer{}, errBadRequest("the result is an object")
	}
	text, hasText := fields["text"]
	ids, hasIDs := fields["ids"]
	cancelled, hasCancelled := fields["cancelled"]
	if btoi(hasText)+btoi(hasIDs)+btoi(hasCancelled) != 1 {
		return config.UIAnswer{}, errBadRequest("send text, ids or cancelled")
	}
	if hasCancelled {
		var v bool
		if json.Unmarshal(cancelled, &v) != nil || !v {
			return config.UIAnswer{}, errBadRequest("cancelled is true")
		}
		return config.UIAnswer{Cancelled: true}, nil
	}
	if hasText {
		if spec.Kind != "input" && !(spec.Kind == "pick" && spec.AllowOther) {
			return config.UIAnswer{}, errBadRequest("this prompt takes no text")
		}
		var v string
		if json.Unmarshal(text, &v) != nil {
			return config.UIAnswer{}, errBadRequest("text is a string")
		}
		return config.UIAnswer{Text: v}, nil
	}
	if spec.Kind != "pick" {
		return config.UIAnswer{}, errBadRequest("this prompt takes no ids")
	}
	var list []string
	if json.Unmarshal(ids, &list) != nil {
		return config.UIAnswer{}, errBadRequest("ids is an array of strings")
	}
	if !spec.Multiple && len(list) != 1 {
		return config.UIAnswer{}, errBadRequest("choose one item")
	}
	offered := map[string]bool{}
	for _, item := range spec.Items {
		offered[item.ID] = true
	}
	for _, id := range list {
		if !offered[id] {
			return config.UIAnswer{}, errBadRequest("%s is not one of the items", id)
		}
	}
	return config.UIAnswer{IDs: list}, nil
}

// btoi is 1 for true.
func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}
