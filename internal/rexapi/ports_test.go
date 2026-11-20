package rexapi

import (
	"encoding/json"
	"testing"

	"gorex/internal/rex"
)

// TestPortsMethodFollowsRevision: revision 15 answers ports, and 14 does
// not know the method.
func TestPortsMethodFollowsRevision(t *testing.T) {
	f := startFixture(t)
	_, sid, block := f.sessionWith(t, rex.CreateOptions{})
	for _, tc := range []struct {
		rev int
		ok  bool
	}{{14, false}, {15, true}, {16, true}} {
		c := f.dialControl()
		w := c.hello(tc.rev)
		if got := int(w["api_revision"].(float64)); got != tc.rev {
			t.Fatalf("revision %d negotiated %d", tc.rev, got)
		}
		callChecked(t, c, "session.attach", map[string]any{"session_id": sid})
		res := c.request("ports", "com.superlogical.terminal.ports", map[string]any{
			"session_id": sid, "block_id": block, "args": map[string]any{},
		})
		if !tc.ok {
			if e := failure(t, res); e["code"] != "method_not_found" {
				t.Fatalf("revision %d: %v", tc.rev, e)
			}
			continue
		}
		r := result(t, res)
		if r["status"] != "ok" {
			t.Fatalf("revision %d: status %v", tc.rev, r["status"])
		}
		if _, ok := r["ports"].([]any); !ok {
			t.Fatalf("revision %d: ports %v", tc.rev, r["ports"])
		}
		if _, ok := r["processes"].([]any); !ok {
			t.Fatalf("revision %d: processes %v", tc.rev, r["processes"])
		}
		if tc.rev == 15 {
			list := result(t, c.request("creators", "block_creator.list", nil))
			if !creatorHasPorts(list) {
				t.Fatalf("revision 15: the terminal creator does not list ports: %v", list)
			}
		}
	}
}

// creatorHasPorts tells whether block_creator.list's terminal creator
// lists the ports method.
func creatorHasPorts(list map[string]any) bool {
	creators, _ := list["creators"].([]any)
	for _, raw := range creators {
		creator, _ := raw.(map[string]any)
		if creator["name"] != creatorName {
			continue
		}
		api, _ := creator["api"].(map[string]any)
		methods, _ := api["methods"].(map[string]any)
		_, ok := methods["ports"]
		return ok
	}
	return false
}

// TestPortsChangedFollowsRevision: a connection at 16 receives the event,
// and one at 15 does not.
func TestPortsChangedFollowsRevision(t *testing.T) {
	rec := &clientRecord{id: "c1", all: true, scope: "all", attached: map[string]bool{}}
	older := &control{rec: rec, rev: 15, out: make(chan outMsg, 4), done: make(chan struct{})}
	newer := &control{rec: rec, rev: 16, out: make(chan outMsg, 4), done: make(chan struct{})}
	chs := []change{{session: "S1", typ: "block_event", since: 16,
		payload: map[string]any{"block_id": "b1", "name": "ports_changed"}}}
	older.sendChanges(chs)
	newer.sendChanges(chs)
	if len(older.out) != 0 {
		t.Fatalf("revision 15 received %d events", len(older.out))
	}
	if len(newer.out) != 1 {
		t.Fatalf("revision 16 received %d events", len(newer.out))
	}
}

// TestPortsReplyTruncates: a list too large for one message keeps its
// start and names the whole answer through blob_read.
func TestPortsReplyTruncates(t *testing.T) {
	f := startFixture(t)
	res := rex.PortsResult{Status: "ok"}
	for i := 0; i < 8000; i++ {
		res.Ports = append(res.Ports, rex.PortEntry{
			Port: 1000 + i, Address: "127.0.0.1", Protocol: "tcp",
			FirstSeenAt: "2026-01-01T00:00:00Z", PID: 100 + i,
		})
		res.Processes = append(res.Processes, rex.PortProcess{
			PID: 100 + i, UserID: 501, User: "alice", Name: "node",
			Path: "/usr/bin/node", Cwd: "/tmp/alice-project",
		})
	}
	out := f.g.portsReply(res)
	if !out.Truncated || out.ID == 0 {
		t.Fatalf("not truncated: %+v", out)
	}
	if len(out.Ports) >= len(res.Ports) {
		t.Fatalf("kept every port: %d", len(out.Ports))
	}
	data, _ := json.Marshal(out)
	if len(data) > maxBlobBytes {
		t.Fatalf("truncated reply is %d bytes", len(data))
	}
	blob, ok := f.g.getBlob(out.ID)
	if !ok {
		t.Fatal("no stored answer")
	}
	var whole rex.PortsResult
	if err := json.Unmarshal([]byte(blob.text), &whole); err != nil {
		t.Fatal(err)
	}
	if len(whole.Ports) != len(res.Ports) {
		t.Fatalf("stored %d ports, want %d", len(whole.Ports), len(res.Ports))
	}
}
