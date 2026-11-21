package rex

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// promptTimeout is how long a question waits for an answer when the action
// asks for no time, and promptMaxTimeout is the most it may ask for
// (docs/MOD-009-client-prompts.md).
var (
	promptTimeout    = newDuration(5 * time.Minute)
	promptMaxTimeout = newDuration(time.Hour)
)

// promptCall is one question, offered to the clients that can show it.
type promptCall struct {
	spec   PromptSpec
	answer chan promptResult
	// places are the connections the question is open on, each with its
	// call ID.
	places map[*subscriber]string
	done   bool
}

type promptResult struct {
	answer PromptAnswer
	err    error
}

// promptPlace is one connection's copy of a question.
type promptPlace struct {
	call *promptCall
	sub  *subscriber
	id   string
}

// setPromptCapable records whether a connection's hello lists
// CapabilityPrompts.
func (s *Server) setPromptCapable(sub *subscriber, capable bool) {
	if sub == nil {
		return
	}
	s.promptMu.Lock()
	sub.prompts = capable
	s.promptMu.Unlock()
}

// openPrompt asks every eligible client the question, and waits for the
// first answer, the timeout or the asking connection's end. It returns
// the answer, or an error that names why no answer came.
func (s *Server) openPrompt(spec *PromptSpec, action string, timeout time.Duration, gone <-chan struct{}) (PromptAnswer, error) {
	if spec == nil {
		return PromptAnswer{}, errors.New("prompt.open: a prompt is required")
	}
	if err := checkPromptSpec(spec); err != nil {
		return PromptAnswer{}, err
	}
	switch {
	case timeout <= 0:
		timeout = promptTimeout.get()
	case timeout > promptMaxTimeout.get():
		timeout = promptMaxTimeout.get()
	}
	call := &promptCall{spec: *spec, answer: make(chan promptResult, 1), places: map[*subscriber]string{}}
	// The subscribers are snapshotted under subMu, and whether one may be
	// asked is decided under promptMu, which guards both flags.
	s.subMu.Lock()
	subs := make([]*subscriber, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.subMu.Unlock()
	type send struct {
		sub *subscriber
		b   []byte
	}
	var sends []send
	s.promptMu.Lock()
	if s.prompts == nil {
		s.prompts = map[string]*promptPlace{}
	}
	for _, sub := range subs {
		if !sub.prompts || sub.busy {
			continue
		}
		s.promptSeq++
		id := strconv.FormatInt(s.promptSeq, 10)
		sub.busy = true
		call.places[sub] = id
		s.prompts[id] = &promptPlace{call: call, sub: sub, id: id}
		b, _ := json.Marshal(Event{Kind: "prompt", Prompt: &PromptEvent{
			Op: "requested", CallID: id, Action: action, Spec: spec, TimeoutMS: int(timeout / time.Millisecond),
		}})
		sends = append(sends, send{sub, b})
	}
	s.promptMu.Unlock()
	if len(sends) == 0 {
		return PromptAnswer{}, errors.New("no client can show the question")
	}
	for _, snd := range sends {
		snd.sub.write(snd.b)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-call.answer:
		return res.answer, res.err
	case <-timer.C:
		s.endPrompt(call, "timeout", "")
		return PromptAnswer{}, errors.New("the prompt timed out")
	case <-gone:
		s.endPrompt(call, "cancelled", "")
		return PromptAnswer{}, errors.New("the connection asking the question ended")
	}
}

// answerPrompt answers the question callID from sub, which must be the
// connection the question was offered on, and ends it everywhere.
func (s *Server) answerPrompt(sub *subscriber, callID string, ans *PromptAnswer) error {
	s.promptMu.Lock()
	place := s.prompts[callID]
	if place == nil || place.sub != sub {
		s.promptMu.Unlock()
		return errors.New("nothing is waiting for that call")
	}
	if ans != nil && ans.Unsupported {
		s.refusePromptLocked(sub, place)
		s.promptMu.Unlock()
		return nil
	}
	if err := checkPromptAnswer(place.call.spec, ans); err != nil {
		s.promptMu.Unlock()
		return err
	}
	sends := s.endPromptLocked(place.call, "answered", callID)
	s.promptMu.Unlock()
	for _, snd := range sends {
		snd.sub.write(snd.b)
	}
	select {
	case place.call.answer <- promptResult{answer: *ans}:
	default:
	}
	return nil
}

// refusePromptLocked removes sub from its question, as one that cannot
// show it, and fails the question when no connection shows it any more.
// Its mu is held.
func (s *Server) refusePromptLocked(sub *subscriber, place *promptPlace) {
	delete(s.prompts, place.id)
	delete(place.call.places, sub)
	sub.busy = false
	if len(place.call.places) > 0 || place.call.done {
		return
	}
	s.endPromptLocked(place.call, "cancelled", "")
	select {
	case place.call.answer <- promptResult{err: errors.New("no client can show the question")}:
	default:
	}
}

// endPrompt ends call for every connection that shows it, except the one
// named, and tells the others why.
func (s *Server) endPrompt(call *promptCall, reason, except string) {
	s.promptMu.Lock()
	sends := s.endPromptLocked(call, reason, except)
	s.promptMu.Unlock()
	for _, snd := range sends {
		snd.sub.write(snd.b)
	}
}

type promptSend struct {
	sub *subscriber
	b   []byte
}

// endPromptLocked forgets call's places, frees each connection, and
// returns what to tell the connections other than except. Its mu is held.
func (s *Server) endPromptLocked(call *promptCall, reason, except string) []promptSend {
	if call.done {
		return nil
	}
	call.done = true
	var sends []promptSend
	for sub, id := range call.places {
		delete(s.prompts, id)
		sub.busy = false
		if id == except {
			continue
		}
		b, _ := json.Marshal(Event{Kind: "prompt", Prompt: &PromptEvent{Op: "ended", CallID: id, Reason: reason}})
		sends = append(sends, promptSend{sub, b})
	}
	return sends
}

// dropPrompts takes sub out of every question it shows, as its connection
// closes, and fails a question no connection shows any more.
func (s *Server) dropPrompts(sub *subscriber) {
	if sub == nil {
		return
	}
	s.promptMu.Lock()
	var sends []promptSend
	seen := map[*promptCall]bool{}
	for id, place := range s.prompts {
		if place.sub != sub {
			continue
		}
		delete(s.prompts, id)
		delete(place.call.places, sub)
		if seen[place.call] || len(place.call.places) > 0 || place.call.done {
			continue
		}
		seen[place.call] = true
		sends = append(sends, s.endPromptLocked(place.call, "cancelled", "")...)
		select {
		case place.call.answer <- promptResult{err: errors.New("no client can show the question")}:
		default:
		}
	}
	sub.busy = false
	s.promptMu.Unlock()
	for _, snd := range sends {
		snd.sub.write(snd.b)
	}
}

// checkPromptSpec checks a question before it goes to a client.
func checkPromptSpec(spec *PromptSpec) error {
	switch spec.Kind {
	case "input", "pick":
	default:
		return fmt.Errorf("prompt.kind is \"input\" or \"pick\", not %q", spec.Kind)
	}
	if strings.TrimSpace(spec.Title) == "" {
		return errors.New("prompt.title is required")
	}
	if spec.Kind == "input" {
		if len(spec.Items) > 0 {
			return errors.New("an input has no items")
		}
		if spec.Multiple {
			return errors.New("multiple is for a pick")
		}
		return nil
	}
	if len(spec.Items) == 0 {
		return errors.New("a pick needs items")
	}
	seen := make(map[string]bool, len(spec.Items))
	for _, it := range spec.Items {
		if strings.TrimSpace(it.ID) == "" {
			return errors.New("each item needs an id")
		}
		if seen[it.ID] {
			return fmt.Errorf("item id %q is offered twice", it.ID)
		}
		seen[it.ID] = true
		if strings.TrimSpace(it.Title) == "" {
			return errors.New("each item needs a title")
		}
	}
	return nil
}

// checkPromptAnswer checks an answer against the question that asked it.
func checkPromptAnswer(spec PromptSpec, ans *PromptAnswer) error {
	if ans == nil {
		return errors.New("an answer is required")
	}
	if ans.Cancelled {
		if ans.Text != "" || len(ans.IDs) > 0 {
			return errors.New("a cancelled answer carries no text or ids")
		}
		return nil
	}
	switch spec.Kind {
	case "input":
		if len(ans.IDs) > 0 {
			return errors.New("an input takes text")
		}
		return nil
	case "pick":
		if len(ans.IDs) > 0 {
			if spec.AllowOther && ans.Text != "" {
				return errors.New("send ids or text, not both")
			}
			offered := make(map[string]bool, len(spec.Items))
			for _, it := range spec.Items {
				offered[it.ID] = true
			}
			if !spec.Multiple && len(ans.IDs) > 1 {
				return errors.New("this prompt takes one item")
			}
			for _, id := range ans.IDs {
				if !offered[id] {
					return fmt.Errorf("item %q is not offered", id)
				}
			}
			return nil
		}
		if spec.AllowOther {
			return nil
		}
		return errors.New("this prompt takes ids")
	}
	return errors.New("the prompt has an unknown kind")
}
