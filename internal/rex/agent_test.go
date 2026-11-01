//go:build darwin || linux

package rex

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gorex/internal/layout"
)

func TestResumeCommand(t *testing.T) {
	for _, tc := range []struct {
		a    AgentSession
		want string
	}{
		{AgentSession{Name: "claude", Conversation: "0f6c2a8e-1b2c-4d5e-8f90-123456789abc"}, "claude --resume 0f6c2a8e-1b2c-4d5e-8f90-123456789abc"},
		{AgentSession{Name: "omp", Conversation: "01a19639-cc01"}, "omp --resume=01a19639-cc01"},
		{AgentSession{Name: "pi", Conversation: "01a19639_d141.x"}, "pi --session 01a19639_d141.x"},
		{AgentSession{Name: "codex", Conversation: "abc"}, ""},
		{AgentSession{Name: "claude", Conversation: ""}, ""},
		{AgentSession{Name: "claude", Conversation: "abc; rm -rf ~"}, ""},
		{AgentSession{Name: "claude", Conversation: "$(id)"}, ""},
		{AgentSession{Name: "claude", Conversation: "-rf"}, ""},
		{AgentSession{Name: "claude", Conversation: "a b"}, ""},
		{AgentSession{Name: "claude", Conversation: "a'b"}, ""},
		{AgentSession{Name: "claude", Conversation: strings.Repeat("a", 129)}, ""},
	} {
		if got := ResumeCommand(tc.a); got != tc.want {
			t.Errorf("ResumeCommand(%+v) = %q, want %q", tc.a, got, tc.want)
		}
	}
}

// TestJournalAgents checks which agents the journal says run still: the
// last record of each session's, until it ended or a run stopped and
// ended its sessions; a restart keeps them.
func TestJournalAgents(t *testing.T) {
	a := &AgentSession{Name: "claude", Conversation: "c1", PID: 10}
	b := &AgentSession{Name: "pi", Conversation: "p1"}
	agent := func(id string, a *AgentSession) journalEntry {
		return journalEntry{Event: eventAgent, Session: &journalSession{ID: id, Agent: a}}
	}
	got := journalAgents([]journalEntry{
		{Event: eventStart}, agent("one", a), agent("two", b), agent("three", a), agent("three", nil),
		{Event: eventStop, Reason: stopRestart}, {Event: eventStart},
	})
	if len(got) != 2 || got["one"] != *a || got["two"] != *b {
		t.Fatalf("agents %+v", got)
	}
	got = journalAgents([]journalEntry{agent("one", a), {Event: eventStop, Reason: stopRequest}, {Event: eventStart}})
	if len(got) != 0 {
		t.Fatalf("agents after a stop %+v", got)
	}
	// One the hook could not have reported is no agent.
	got = journalAgents([]journalEntry{agent("one", &AgentSession{Name: "claude", Conversation: "$(id)"})})
	if len(got) != 0 {
		t.Fatalf("agents of a bad id %+v", got)
	}
}

// TestCompactJournalKeepsAgents checks that compaction keeps the record of
// an agent that runs still, however old.
func TestCompactJournalKeepsAgents(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOREX_DIR", dir)
	j := openJournal()
	j.agent("old", &AgentSession{Name: "claude", Conversation: "kept"})
	j.agent("gone", &AgentSession{Name: "claude", Conversation: "ended"})
	j.agent("gone", nil)
	for i := range journalKeep + 10 {
		j.layout(uint64(i+1), testLayout(&layout.Node{SID: "x", Dir: strconv.Itoa(i)}))
	}
	j.close()
	compactJournal(journalPath())
	entries := readJournal()
	if len(entries) != journalKeep+1 {
		t.Fatalf("%d entries", len(entries))
	}
	got := journalAgents(entries)
	if len(got) != 1 || got["old"].Conversation != "kept" {
		t.Fatalf("agents after compaction %+v", got)
	}
}

// fakeAgents writes programs named as the agents, in a bin directory as
// installed ones are: each writes its process and arguments to a file of
// dir, and runs until it is killed.
func fakeAgents(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "omp", "pi"} {
		script := "#!/bin/sh\necho \"$$ $*\" > " + filepath.Join(dir, name+".ran") + ".tmp; mv " +
			filepath.Join(dir, name+".ran") + ".tmp " + filepath.Join(dir, name+".ran") + "\nsleep 600\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// A wrapper that runs the agent as its child, as caffeinate does, and
	// an agent that runs another through its tool's shell.
	scripts := map[string]string{
		"caffeinate": "#!/bin/sh\n\"$@\"\n",
		"codex":      "#!/bin/sh\nsh -c 'claude; true'\n",
	}
	for name, script := range scripts {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return bin
}

// waitRan waits for fake agent name to run, and returns its process and
// arguments.
func waitRan(t *testing.T, dir, name string) (int, string) {
	t.Helper()
	path := filepath.Join(dir, name+".ran")
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		b, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(string(b), "\n") {
			continue
		}
		pid, args, _ := strings.Cut(strings.TrimSpace(string(b)), " ")
		n, _ := strconv.Atoi(pid)
		os.Remove(path)
		return n, args
	}
	t.Fatalf("%s did not run", name)
	return 0, ""
}

// listed returns session id of the server's list.
func listed(t *testing.T, c *Client, id string) SessionInfo {
	t.Helper()
	l, err := c.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range l {
		if in.ID == id {
			return in
		}
	}
	t.Fatalf("no session %s in %+v", id, l)
	return SessionInfo{}
}

// TestCrashResumesAgent runs an agent in a session, which its hook reports,
// and kills the server the way a crash does: the server after it starts
// the session again and types the command that resumes the conversation.
// The record ends as the agent does, and is not resumed again.
func TestCrashResumesAgent(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rexagent")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	bin := fakeAgents(t, dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, h := connectTest(t)
	in, err := c.Create(CreateOptions{Dir: dir, Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.SetLayout(testLayout(&layout.Node{SID: in.ID, Dir: dir, Cols: 80, Rows: 24})); err != nil {
		t.Fatal(err)
	}
	if err := c.Input(in.ID, []byte("claude\r")); err != nil {
		t.Fatal(err)
	}
	pid, _ := waitRan(t, dir, "claude")

	// A process that is not the session's foreground program, as an agent
	// another runs, is not recorded.
	r, err := c.ReportAgent(in.ID, AgentSession{Name: "claude", Conversation: "conv-1", PID: os.Getpid()})
	if err != nil || r.Recorded {
		t.Fatalf("a report from outside the session: %+v %v", r, err)
	}
	if _, err := c.ReportAgent(in.ID, AgentSession{Name: "claude", Conversation: "a;b", PID: pid}); err == nil {
		t.Fatal("a bad conversation id was taken")
	}
	r, err = c.ReportAgent(in.ID, AgentSession{Name: "claude", Conversation: "conv-1", PID: pid})
	if err != nil || !r.Recorded {
		t.Fatalf("report: %+v %v", r, err)
	}
	if a := listed(t, c, in.ID).Agent; a == nil || a.Name != "claude" || a.Conversation != "conv-1" || a.PID != pid {
		t.Fatalf("the list's agent is %+v", a)
	}
	if got := journalAgents(readJournal()); got[in.ID].Conversation != "conv-1" {
		t.Fatalf("the journal's agents %+v", got)
	}

	// The crash.
	c.Close()
	if err := syscall.Kill(h.PID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitStopped(t)
	syscall.Kill(pid, syscall.SIGKILL)
	next, err := Connect()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		next.Shutdown()
		next.Close()
		waitStopped(t)
	})
	pid, args := waitRan(t, dir, "claude")
	if args != "--resume conv-1" {
		t.Fatalf("the resumed agent ran with %q", args)
	}
	// The resumed agent's process is learned from the foreground.
	var a *AgentSession
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if a = listed(t, next, in.ID).Agent; a != nil && a.PID == pid {
			break
		}
	}
	if a == nil || a.PID != pid || a.Conversation != "conv-1" {
		t.Fatalf("the resumed agent is %+v, its process %d", a, pid)
	}

	// The agent ends: the pane is back at its shell, and its record ends.
	syscall.Kill(pid, syscall.SIGTERM)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if a = listed(t, next, in.ID).Agent; a == nil {
			break
		}
	}
	if a != nil {
		t.Fatalf("the agent that ended is still %+v", a)
	}
	// The journal hears of it after agentEndDelay.
	got := journalAgents(readJournal())
	for deadline := time.Now().Add(agentEndDelay + 5*time.Second); len(got) != 0 && time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		got = journalAgents(readJournal())
	}
	if len(got) != 0 {
		t.Fatalf("the journal keeps %+v", got)
	}
	if in := listed(t, next, in.ID); in.Exited {
		t.Fatal("the session ended with its agent")
	}
}

// TestResumeAgentGivesUp checks that an agent a restart resumes, which
// does not start, is given up after agentGrace.
func TestResumeAgentGivesUp(t *testing.T) {
	ss, err := newSession("giveup", CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer ss.kill()
	var gone []string
	ss.agentGone = func(id string) { gone = append(gone, id) }
	ss.mu.Lock()
	ss.agent = &AgentSession{Name: "pi", Conversation: "nope"}
	ss.agentTyped = time.Now().Add(-agentGrace - time.Second)
	ss.mu.Unlock()
	if a := ss.info().Agent; a != nil || len(gone) != 1 || gone[0] != "giveup" {
		t.Fatalf("agent %+v, gone %v", a, gone)
	}
}

// TestHandoffKeepsAgent checks that an upgrade hands the agent on.
func TestHandoffKeepsAgent(t *testing.T) {
	a := &AgentSession{Name: "omp", Conversation: "o1", PID: 1234}
	ss, err := resumeSession(sessionState{ID: "h", Shell: "sh", Master: -1, Exited: true, Agent: a}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if ss.agent == nil || *ss.agent != *a {
		t.Fatalf("the agent after the handoff is %+v", ss.agent)
	}
}

// TestAgentUnderWrapper checks that an agent a wrapper runs as its child
// (caffeinate claude, npx) is the session's, and one another agent runs
// through its tool's shell is not.
func TestAgentUnderWrapper(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "rexwrap")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	t.Setenv("GOREX_DIR", dir)
	bin := fakeAgents(t, dir)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	c, _ := connectTest(t)
	for _, tc := range []struct {
		run  string
		want bool
	}{{"caffeinate claude", true}, {"codex", false}} {
		in, err := c.Create(CreateOptions{Command: []string{"/bin/sh"}, Dir: dir, Cols: 80, Rows: 24})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Input(in.ID, []byte(tc.run+"\r")); err != nil {
			t.Fatal(err)
		}
		pid, _ := waitRan(t, dir, "claude")
		r, err := c.ReportAgent(in.ID, AgentSession{Name: "claude", Conversation: "w1", PID: pid})
		if err != nil || r.Recorded != tc.want {
			t.Errorf("%s: report %+v %v", tc.run, r, err)
		}
		if a := listed(t, c, in.ID).Agent; (a != nil) != tc.want || (a != nil && a.PID != pid) {
			t.Errorf("%s: agent %+v, its process %d", tc.run, a, pid)
		}
		c.Kill(in.ID)
	}
}

// TestAgentEndsWithSession checks that a session that exits ends its
// agent, without info looking.
func TestAgentEndsWithSession(t *testing.T) {
	ss, err := newSession("ends", CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	gone := make(chan string, 1)
	ss.mu.Lock()
	ss.agentGone = func(id string) { gone <- id }
	ss.mu.Unlock()
	ss.setAgent(AgentSession{Name: "claude", Conversation: "c", PID: os.Getpid()})
	ss.kill()
	select {
	case id := <-gone:
		if id != "ends" {
			t.Fatalf("gone %q", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the session ended, its agent did not")
	}
}

// TestAgentEndedLater checks that the end of an agent is journaled after
// agentEndDelay, when the server is still there, and not when the session
// runs an agent again by then.
func TestAgentEndedLater(t *testing.T) {
	t.Setenv("GOREX_DIR", t.TempDir())
	defer func(d time.Duration) { agentEndDelay = d }(agentEndDelay)
	agentEndDelay = 100 * time.Millisecond
	ss, err := newSession("again", CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer ss.kill()
	s := &Server{sessions: map[string]*session{"again": ss}, journal: openJournal()}
	defer s.journal.close()
	a := &AgentSession{Name: "pi", Conversation: "p"}
	s.journal.agent("gone", a)
	s.journal.agent("again", a)
	s.agentEnded("gone")
	s.agentEnded("again")
	ss.setAgent(*a)
	if got := journalAgents(readJournal()); len(got) != 2 {
		t.Fatalf("journaled at once: %+v", got)
	}
	time.Sleep(300 * time.Millisecond)
	if got := journalAgents(readJournal()); len(got) != 1 || got["again"] != *a {
		t.Fatalf("after the delay %+v", got)
	}
}

// TestResumeAgentTypesOnce checks that a restart types the resume command
// only into a shell at its prompt, and not for an agent that reported
// itself meanwhile; a shell that does not settle gives the agent up.
func TestResumeAgentTypesOnce(t *testing.T) {
	defer func(d time.Duration) { resumeWaitMost = d }(resumeWaitMost)
	resumeWaitMost = time.Second
	screen := func(ss *session) string {
		snap := ss.capture(0).Screen
		return string(snap)
	}

	ss, err := newSession("reported", CreateOptions{Command: []string{"/bin/sh"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer ss.kill()
	ss.resumeAgent(AgentSession{Name: "claude", Conversation: "r1"})
	ss.setAgent(AgentSession{Name: "claude", Conversation: "r1", PID: os.Getpid()})
	time.Sleep(resumeWaitMost + 500*time.Millisecond)
	if strings.Contains(screen(ss), "--resume") {
		t.Fatalf("typed for an agent that reported itself: %q", screen(ss))
	}

	busy, err := newSession("busy", CreateOptions{Command: []string{"/bin/sh", "-c", "while :; do echo x; sleep 0.05; done"}, Cols: 80, Rows: 24}, "", func() {})
	if err != nil {
		t.Fatal(err)
	}
	defer busy.kill()
	gone := make(chan string, 1)
	busy.mu.Lock()
	busy.agentGone = func(id string) { gone <- id }
	busy.mu.Unlock()
	busy.resumeAgent(AgentSession{Name: "claude", Conversation: "b1"})
	select {
	case <-gone:
	case <-time.After(resumeWaitMost + 3*time.Second):
		t.Fatal("a shell that did not settle kept its agent")
	}
	if strings.Contains(screen(busy), "--resume") {
		t.Fatal("typed into a shell that did not settle")
	}
}
