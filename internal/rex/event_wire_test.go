package rex

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"
	"time"
)

// oldEvent and oldResponse are Event and Response as they were before
// Response embedded Event (released 0.15–0.21): their lines are the wire.
type oldEvent struct {
	Kind     string         `json:"event"`
	Revision uint64         `json:"revision,omitempty"`
	Sessions []EventSession `json:"sessions,omitempty"`
	Transfer *Transfer      `json:"transfer,omitempty"`
}

type oldResponse struct {
	ID       int64           `json:"id"`
	Error    string          `json:"error,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	ETag     string          `json:"etag,omitempty"`
	Event    string          `json:"event,omitempty"`
	Revision uint64          `json:"revision,omitempty"`
	Sessions []EventSession  `json:"sessions,omitempty"`
	Transfer *Transfer       `json:"transfer,omitempty"`
}

var wireEvents = []struct {
	old  oldEvent
	new  Event
	line string // as the code before wrote it
}{
	{oldEvent{Kind: "layout", Revision: 7}, Event{Kind: "layout", Revision: 7},
		`{"event":"layout","revision":7}`},
	{oldEvent{Kind: "sessions", Sessions: []EventSession{{ID: "s1"}, {ID: "s2"}}},
		Event{Kind: "sessions", Sessions: []EventSession{{ID: "s1"}, {ID: "s2"}}},
		`{"event":"sessions","sessions":[{"id":"s1"},{"id":"s2"}]}`},
	{oldEvent{Kind: "sessions", Sessions: []EventSession{}}, Event{Kind: "sessions", Sessions: []EventSession{}},
		`{"event":"sessions"}`},
	{oldEvent{Kind: "transfer", Transfer: &Transfer{ID: "t1"}}, Event{Kind: "transfer", Transfer: &Transfer{ID: "t1"}},
		`{"event":"transfer","transfer":{"id":"t1","sid":"","name":"","direction":"","size":0,"done":0,"state":""}}`},
}

// TestEventWire checks that an Event and a Response write the bytes they
// wrote as two structs, so servers and clients of either kind still talk.
func TestEventWire(t *testing.T) {
	for _, w := range wireEvents {
		o, _ := json.Marshal(w.old)
		n, _ := json.Marshal(w.new)
		if string(o) != w.line || string(n) != w.line {
			t.Errorf("event: old %s, new %s, want %s", o, n, w.line)
		}
	}
	responses := []struct {
		old  oldResponse
		new  Response
		line string
	}{
		{oldResponse{}, Response{}, `{"id":0}`},
		{oldResponse{ID: 3, Data: json.RawMessage(`{"x":1}`)}, Response{ID: 3, Data: json.RawMessage(`{"x":1}`)}, `{"id":3,"data":{"x":1}}`},
		{oldResponse{ID: 4, Error: "nope"}, Response{ID: 4, Error: "nope"}, `{"id":4,"error":"nope"}`},
		{oldResponse{ID: 5, ETag: "e1"}, Response{ID: 5, ETag: "e1"}, `{"id":5,"etag":"e1"}`},
		{oldResponse{Error: "rex: refused"}, Response{Error: "rex: refused"}, `{"id":0,"error":"rex: refused"}`},
		{oldResponse{Event: "layout", Revision: 2}, Response{Event: Event{Kind: "layout", Revision: 2}}, `{"id":0,"event":"layout","revision":2}`},
	}
	for _, r := range responses {
		o, _ := json.Marshal(r.old)
		n, _ := json.Marshal(r.new)
		if string(o) != r.line || string(n) != r.line {
			t.Errorf("response: old %s, new %s, want %s", o, n, r.line)
		}
		// An old client reads a new server's answer as before.
		var back oldResponse
		if err := json.Unmarshal(n, &back); err != nil || !reflect.DeepEqual(back, r.old) {
			t.Errorf("old client read %s as %+v, want %+v", n, back, r.old)
		}
	}
}

// TestEventFromOldServer feeds the lines an old server writes to a client,
// which hands each on as the Event it tells of.
func TestEventFromOldServer(t *testing.T) {
	a, b := net.Pipe()
	c := newClient(a, nil)
	defer c.Close()
	go func() {
		for _, w := range wireEvents {
			line, _ := json.Marshal(w.old)
			b.Write(append(line, '\n'))
		}
	}()
	for _, w := range wireEvents {
		select {
		case ev := <-c.Events():
			want := w.new
			if len(want.Sessions) == 0 {
				want.Sessions = nil // omitted on the wire
			}
			if !reflect.DeepEqual(ev, want) {
				t.Errorf("got %+v, want %+v", ev, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("no %s event", w.new.Kind)
		}
	}
	if got := c.Revision(); got != 7 {
		t.Errorf("revision %d, want 7", got)
	}
}
