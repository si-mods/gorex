//go:build darwin || linux || windows

package rex

import (
	"strings"
	"testing"
	"time"
)

// promptClients starts a server and returns a client that asks, and n
// clients that show and answer questions, each subscribed.
func promptClients(t *testing.T, n int) (*Client, []*Client) {
	t.Helper()
	t.Setenv("GOREX_DIR", t.TempDir())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Serve()
	}()
	t.Cleanup(func() {
		// The server holds the layout file open, which Windows will not
		// let the test remove: stop it through its socket.
		if c, err := ConnectSocket(SocketPath()); err == nil {
			_ = c.Shutdown()
			c.Close()
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the server did not stop")
		}
	})
	var first *Client
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, err := ConnectSocket(SocketPath())
		if err == nil {
			first = c
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connecting: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var clients []*Client
	for i := 0; i < n; i++ {
		c := first
		if i > 0 {
			var err error
			c, err = ConnectSocket(SocketPath())
			if err != nil {
				t.Fatal(err)
			}
		}
		c.DeclareCapabilities(CapabilityPrompts)
		if _, err := c.Hello(); err != nil {
			t.Fatal(err)
		}
		if err := c.Subscribe(); err != nil {
			t.Fatal(err)
		}
		clients = append(clients, c)
	}
	return first, clients
}

// readPromptEvent waits for a prompt event of op from c.
func readPromptEvent(t *testing.T, c *Client, op string) *PromptEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-c.Events():
			if ev.Kind == "prompt" && ev.Prompt != nil && ev.Prompt.Op == op {
				return ev.Prompt
			}
		case <-deadline:
			t.Fatalf("no prompt %s event", op)
		}
	}
}

type promptOut struct {
	answer PromptAnswer
	err    error
}

// TestPromptFirstAnswerWins: every client that can show the question
// shows it, the first answer ends it everywhere and reaches the asker.
func TestPromptFirstAnswerWins(t *testing.T) {
	asker, clients := promptClients(t, 2)
	got := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Name"}, 10*time.Second)
		got <- promptOut{ans, err}
	}()
	mine := readPromptEvent(t, clients[0], "requested")
	theirs := readPromptEvent(t, clients[1], "requested")
	if mine.CallID == theirs.CallID {
		t.Fatalf("both clients got the call %q", mine.CallID)
	}
	if mine.Spec == nil || mine.Spec.Title != "Name" || mine.TimeoutMS != 10000 {
		t.Fatalf("event = %+v", mine)
	}
	if err := clients[1].PromptReply(theirs.CallID, PromptAnswer{Text: "alpha"}); err != nil {
		t.Fatal(err)
	}
	res := <-got
	if res.err != nil || res.answer.Text != "alpha" {
		t.Fatalf("answer = %+v, %v", res.answer, res.err)
	}
	ended := readPromptEvent(t, clients[0], "ended")
	if ended.Reason != "answered" {
		t.Errorf("the other client's end = %q", ended.Reason)
	}
	// The question is gone: a late answer is refused.
	if err := clients[0].PromptReply(mine.CallID, PromptAnswer{Text: "late"}); err == nil {
		t.Error("a late answer was taken")
	}
}

// TestPromptTimeout: no answer ends the question everywhere and fails the
// asker's request.
func TestPromptTimeout(t *testing.T) {
	asker, clients := promptClients(t, 1)
	start := time.Now()
	_, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Name"}, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Error("the question did not end at its time")
	}
	if ended := readPromptEvent(t, clients[0], "ended"); ended.Reason != "timeout" {
		t.Errorf("end = %q", ended.Reason)
	}
}

// TestPromptCancel: a person closing the question answers cancelled, and
// the asker continues with that.
func TestPromptCancel(t *testing.T) {
	asker, clients := promptClients(t, 2)
	got := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Name"}, 10*time.Second)
		got <- promptOut{ans, err}
	}()
	mine := readPromptEvent(t, clients[0], "requested")
	readPromptEvent(t, clients[1], "requested")
	if err := clients[0].PromptReply(mine.CallID, PromptAnswer{Cancelled: true}); err != nil {
		t.Fatal(err)
	}
	res := <-got
	if res.err != nil || !res.answer.Cancelled {
		t.Fatalf("answer = %+v, %v", res.answer, res.err)
	}
}

// TestPromptUnsupported: a client that cannot show the question leaves
// it, and the others keep it.
func TestPromptUnsupported(t *testing.T) {
	asker, clients := promptClients(t, 2)
	got := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Name"}, 10*time.Second)
		got <- promptOut{ans, err}
	}()
	mine := readPromptEvent(t, clients[0], "requested")
	theirs := readPromptEvent(t, clients[1], "requested")
	if err := clients[0].PromptReply(mine.CallID, PromptAnswer{Unsupported: true}); err != nil {
		t.Fatal(err)
	}
	if err := clients[1].PromptReply(theirs.CallID, PromptAnswer{Text: "beta"}); err != nil {
		t.Fatal(err)
	}
	if res := <-got; res.err != nil || res.answer.Text != "beta" {
		t.Fatalf("answer = %+v, %v", res.answer, res.err)
	}
}

// TestPromptUnsupportedByAll: a question no client can show fails at once.
func TestPromptUnsupportedByAll(t *testing.T) {
	asker, clients := promptClients(t, 1)
	got := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Name"}, 10*time.Second)
		got <- promptOut{ans, err}
	}()
	event := readPromptEvent(t, clients[0], "requested")
	if err := clients[0].PromptReply(event.CallID, PromptAnswer{Unsupported: true}); err != nil {
		t.Fatal(err)
	}
	res := <-got
	if res.err == nil || !strings.Contains(res.err.Error(), "no client can show") {
		t.Fatalf("err = %v", res.err)
	}
}

// TestPromptPickChecks: a pick takes ids it offered, and only those.
func TestPromptPickChecks(t *testing.T) {
	asker, clients := promptClients(t, 1)
	spec := PromptSpec{Kind: "pick", Title: "Project", Items: []PromptItem{
		{ID: "a", Title: "Alpha"}, {ID: "b", Title: "Beta"},
	}}
	got := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", spec, 10*time.Second)
		got <- promptOut{ans, err}
	}()
	event := readPromptEvent(t, clients[0], "requested")
	if err := clients[0].PromptReply(event.CallID, PromptAnswer{IDs: []string{"c"}}); err == nil {
		t.Fatal("an item that was not offered was taken")
	}
	if err := clients[0].PromptReply(event.CallID, PromptAnswer{IDs: []string{"b"}}); err != nil {
		t.Fatal(err)
	}
	if res := <-got; res.err != nil || len(res.answer.IDs) != 1 || res.answer.IDs[0] != "b" {
		t.Fatalf("answer = %+v, %v", res.answer, res.err)
	}
}

// TestPromptBusyClient: a client that shows a question is not asked
// another, and gets the next once it answers.
func TestPromptBusyClient(t *testing.T) {
	asker, clients := promptClients(t, 1)
	first := make(chan promptOut, 1)
	go func() {
		ans, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "One"}, 10*time.Second)
		first <- promptOut{ans, err}
	}()
	event := readPromptEvent(t, clients[0], "requested")
	if _, err := asker.PromptOpen("test", PromptSpec{Kind: "input", Title: "Two"}, 200*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "no client can show") {
		t.Fatalf("a busy client was asked: %v", err)
	}
	if err := clients[0].PromptReply(event.CallID, PromptAnswer{Text: "one"}); err != nil {
		t.Fatal(err)
	}
	if res := <-first; res.err != nil || res.answer.Text != "one" {
		t.Fatalf("answer = %+v, %v", res.answer, res.err)
	}
}

// TestPromptSpecChecks: a question that cannot be shown is refused before
// it reaches a client.
func TestPromptSpecChecks(t *testing.T) {
	asker, _ := promptClients(t, 1)
	for _, spec := range []PromptSpec{
		{Kind: "slider", Title: "Size"},
		{Kind: "input"},
		{Kind: "pick", Title: "Pick"},
		{Kind: "pick", Title: "Pick", Items: []PromptItem{{ID: "a", Title: "A"}, {ID: "a", Title: "B"}}},
	} {
		if _, err := asker.PromptOpen("test", spec, time.Second); err == nil {
			t.Errorf("spec %+v was taken", spec)
		}
	}
}
