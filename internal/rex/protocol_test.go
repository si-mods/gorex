//go:build darwin || linux || windows

package rex

import (
	"bytes"
	"slices"
	"testing"
	"time"
)

// TestProtocolV3 checks the additions of version 3: capabilities in hello,
// layout revisions, and revisions on layout events.
func TestProtocolV3(t *testing.T) {
	s, _ := testServer(t)
	c := pipe(s, "")
	defer c.Close()

	h, err := c.Hello()
	if err != nil {
		t.Fatal(err)
	}
	if h.Version != 3 {
		t.Fatalf("protocol %d, want 3", h.Version)
	}
	for _, want := range []string{"layout.revision", "events.revision", CapabilityInputEvents, CapabilityTerminalQuery} {
		if !slices.Contains(h.Capabilities, want) {
			t.Errorf("capability %q missing from %v", want, h.Capabilities)
		}
	}

	st, err := c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Revision != 1 || len(st.Layout) != 0 && string(st.Layout) != "null" {
		t.Fatalf("empty server: %+v", st)
	}

	if err := c.SetLayoutAt([]byte(`{"a":1}`), 0); err != nil {
		t.Fatal(err)
	}
	st, err = c.LayoutState()
	if err != nil {
		t.Fatal(err)
	}
	if st.Revision != 2 || !bytes.Equal(st.Layout, []byte(`{"a":1}`)) {
		t.Fatalf("after set: %+v", st)
	}

	// The same layout changes no revision.
	if err := c.SetLayoutAt([]byte(`{"a":1}`), st.Revision); err != nil {
		t.Fatal(err)
	}
	if st, _ = c.LayoutState(); st.Revision != 2 {
		t.Fatalf("same layout: revision %d, want 2", st.Revision)
	}

	// A change at the revision the client has goes through.
	if err := c.SetLayoutAt([]byte(`{"a":2}`), 2); err != nil {
		t.Fatal(err)
	}
	if st, _ = c.LayoutState(); st.Revision != 3 {
		t.Fatalf("second change: revision %d, want 3", st.Revision)
	}

	// One at an old revision does not.
	if err := c.SetLayoutAt([]byte(`{"a":3}`), 2); err == nil {
		t.Fatal("a stale revision was accepted")
	}
	if st, _ = c.LayoutState(); st.Revision != 3 || !bytes.Equal(st.Layout, []byte(`{"a":2}`)) {
		t.Fatalf("after a stale change: %+v", st)
	}

	// An event carries the revision.
	sub := pipe(s, "")
	defer sub.Close()
	if _, err := sub.Hello(); err != nil {
		t.Fatal(err)
	}
	if err := sub.Subscribe(); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayoutAt([]byte(`{"a":4}`), 3); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-sub.Events():
		if ev.Kind != "layout" {
			t.Fatalf("the event was not a layout change: %s", ev.Kind)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no layout event")
	}
	if got := sub.Revision(); got != 4 {
		t.Fatalf("event revision %d, want 4", got)
	}
}

// TestHelloBeforeHost answers a hello with no machine while hostInfo, as a
// slow system_profiler, has not finished: never what it is writing.
func TestHelloBeforeHost(t *testing.T) {
	s := &Server{hostDone: make(chan struct{})}
	go func() { s.host = HostInfo{Name: "slow"} }()
	h, err := s.do(Request{Op: "hello"}, "", nil)
	if err != nil || h.(Hello).Host != (HostInfo{}) {
		t.Fatalf("hello = %+v, %v; want no host", h, err)
	}
}
