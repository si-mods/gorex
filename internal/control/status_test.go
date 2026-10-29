package control

import (
	"testing"

	"gorex/internal/rex"
)

func TestAgentStatus(t *testing.T) {
	b := &bridge{infos: map[string]rex.SessionInfo{
		"a": {Status: &rex.ProgramStatus{State: "working"}},
		"b": {Status: &rex.ProgramStatus{State: "blocked"}},
	}}
	tb := &tab{id: "t", root: &node{a: &node{sid: "a"}, b: &node{sid: "b"}}}
	b.tabs = []*tab{tb}
	if got := b.tabStatus(tb); got != "blocked" {
		t.Errorf("tab status %q", got)
	}
	if got := b.tabInfo(tb)["agent_status"]; got != "blocked" {
		t.Errorf("tab info %v", got)
	}
	p := b.paneInfo(tb, "a")
	if p["agent_status"] != "working" {
		t.Errorf("pane status %v", p["agent_status"])
	}
}
