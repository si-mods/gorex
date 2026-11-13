package rexapi

import (
	"io"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gorex/internal/rex"
)

// TestSeeBlock checks gorex.see_block: the welcome lists it, a block's
// program status carries no seen_at till a client saw its records, and
// then carries it, in program_status and in a program_status_changed
// event; a bad call fails, and the help documents describe it.
func TestSeeBlock(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the test's program is a Unix one")
	}
	f := startFixture(t)
	c, sid, block := f.sessionWith(t, rex.CreateOptions{Command: []string{"/bin/sh", "-c", `printf '\033]7501;state=done\007'; sleep 60`}})
	if features, _ := c.welcome["features"].([]any); !slices.Contains(features, any(featureSeeBlock)) {
		t.Fatalf("the welcome's features %v lack %s", features, featureSeeBlock)
	}
	var status map[string]any
	waitFor(t, "the done record", func() bool {
		status = blockCall(t, c, sid, block, "program_status", nil)
		return len(status["records"].([]any)) == 1
	})
	if _, ok := status["seen_at"]; ok {
		t.Fatalf("seen_at before any client saw it: %v", status)
	}
	updated := status["records"].([]any)[0].(map[string]any)["updated_at"].(string)

	obs := f.dialControl()
	obs.hello(apiRevision)
	callChecked(t, obs, "session.attach_all", map[string]any{"event_scope": "all"})
	res := callChecked(t, c, "gorex.see_block", map[string]any{"session_id": sid, "block_id": block, "seen_at": updated})
	if len(res) != 0 {
		t.Errorf("result %v, want {}", res)
	}
	ev := waitBlockEventNamed(t, obs, "program_status_changed")
	if p := ev["payload"].(map[string]any); p["seen_at"] != updated || ev["block_id"] != block {
		t.Errorf("program_status_changed %v, want seen_at %s", ev, updated)
	}
	if got := blockCall(t, c, sid, block, "program_status", nil)["seen_at"]; got != updated {
		t.Errorf("program_status's seen_at %v, want %s", got, updated)
	}

	for _, tc := range []struct {
		payload map[string]any
		code    string
	}{
		{map[string]any{"session_id": sid, "block_id": block, "seen_at": "yesterday"}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": block}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": block, "seen_at": updated, "extra": 1}, "bad_request"},
		{map[string]any{"session_id": sid, "block_id": "block:nope", "seen_at": updated}, "not_found"},
	} {
		if e := failure(t, c.request("bad", "gorex.see_block", tc.payload)); e["code"] != tc.code {
			t.Errorf("%v: %v, want %s", tc.payload, e, tc.code)
		}
	}

	if schemaFor(t, "gorex.see_block") == nil {
		t.Error("gorex.see_block has no result schema")
	}
	r, err := httpGet(f.srv.URL + "/llms.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if body, _ := io.ReadAll(r.Body); !strings.Contains(string(body), "### gorex.see_block") {
		t.Error("/llms.txt does not describe gorex.see_block")
	}
}
