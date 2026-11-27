//go:build darwin || linux || windows

package rex

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorex/internal/layout"
)

// Tunable timings for the interactive TUI driver; tests shorten these so
// fake TUI tests finish in milliseconds.
var (
	delegateReadyWait          = 20 * time.Second
	delegateReadyPoll          = 80 * time.Millisecond
	delegateReadyQuiet         = 400 * time.Millisecond
	delegateReadyFallbackQuiet = 1500 * time.Millisecond
	delegatePasteDelay         = 200 * time.Millisecond
	delegateEnterWait          = 2 * time.Second
	delegatePollInterval       = 100 * time.Millisecond
	delegateDefaultTimout      = 10 * time.Minute
)

// delegatedRun tracks one delegated session's live turn state and
// transcript tailing on the server (docs/MOD-016-peer-delegation.md).
type delegatedRun struct {
	mu           sync.Mutex
	sid          string
	agent        string
	mode         string
	dir          string
	home         string
	agentSession string
	resumed      bool
	startedAt    time.Time
	adapter      agentTranscript
	excludeFiles map[string]struct{}
	filePath     string
	fileOffset   int64

	turnSeq     int
	promptCount int
	promptAckCh chan struct{}
	status      string // "running", "completed", "blocked", "failed", "timeout"
	result      string
	errText     string
	turnDone    chan struct{}
	stopCh      chan struct{}
	stopOnce    sync.Once
}

func (d *delegatedRun) stop() {
	d.stopOnce.Do(func() { close(d.stopCh) })
}

func (d *delegatedRun) beginTurn() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.turnSeq++
	d.status = "running"
	d.result = ""
	d.errText = ""
	ch := make(chan struct{})
	d.turnDone = ch
	return ch
}

func (d *delegatedRun) finishTurn(status, result, errText string) bool {
	d.mu.Lock()
	changed := false
	// Allow a blocked turn to transition to completed/failed once approved
	// by the parent agent or user.
	if d.status == "running" || (d.status == "blocked" && (status == "completed" || status == "failed")) {
		d.status = status
		changed = true
		if result != "" {
			d.result = result
		}
		if status == "completed" {
			d.errText = ""
		} else if errText != "" {
			d.errText = errText
		}
		if d.turnDone != nil {
			close(d.turnDone)
			d.turnDone = nil
		}
	}
	d.mu.Unlock()
	return changed
}

func (d *delegatedRun) snapshot(ss *session) DelegateResult {
	d.mu.Lock()
	res := DelegateResult{
		Session:      d.sid,
		Agent:        d.agent,
		Mode:         d.mode,
		Status:       d.status,
		AgentSession: d.agentSession,
		Result:       d.result,
		Error:        d.errText,
	}
	d.mu.Unlock()
	if ss != nil {
		in := ss.info()
		if in.Exited {
			res.ExitCode = in.ExitCode
			if res.Status == "running" {
				if in.ExitCode == 0 {
					res.Status = "completed"
				} else {
					res.Status = "failed"
				}
			}
		}
		if res.Result == "" || res.Status == "blocked" || res.Status == "failed" {
			res.RawCapture = sessionPlainText(ss)
		}
	}
	return res
}

// doDelegate handles the "delegate" op: launches an interactive agent TUI
// via MOD-010 quick launcher, attaches it to the matching project tab via
// layout, delivers the prompt via bracketed paste + Return, and tails its
// transcript JSONL for clean results.
func (s *Server) doDelegate(req Request, peer string, _ *keptConn) (any, error) {
	if req.Delegate == nil {
		return nil, errors.New("rex: delegate requires options")
	}
	opt := *req.Delegate
	if strings.TrimSpace(opt.Prompt) == "" {
		return nil, errors.New("rex: delegate requires a non-empty prompt")
	}
	mode := opt.Mode
	if mode == "" {
		mode = "full"
	}
	if mode != "full" && mode != "read-only" && mode != "yolo" {
		return nil, fmt.Errorf("rex: invalid delegate mode %q (want full, read-only, or yolo)", mode)
	}

	home, _ := s.homeDir()
	shell := s.spawner().shell()
	dir := opt.Dir
	if dir == "" {
		dir = home
	}

	agentID, resolvedPath, err := resolveDelegateAgent(opt.Agent, mode, home, shell)
	if err != nil {
		return nil, err
	}

	agentSession := opt.Resume
	if agentID == "claude" && agentSession == "" {
		agentSession = newDelegateUUID()
	}

	extraArgs, extraEnv, err := buildDelegateLaunch(agentID, mode, opt.Resume, agentSession)
	if err != nil {
		return nil, err
	}
	cmd := agentCommandWithArgs(shell, agentID, resolvedPath, extraArgs)

	adapter := transcriptAdapterFor(agentID)
	startedAt := time.Now()
	var excludeFiles map[string]struct{}
	if opt.Resume == "" && adapter != nil {
		excludeFiles = adapter.ExistingFiles(dir, home, "")
	}

	tabName := delegateTabName(agentID, opt.From, opt.Name, opt.Prompt)
	ss, err := s.createDelegatedTab(dir, tabName, cmd, extraEnv, peer, opt.From, agentID)
	if err != nil {
		return nil, err
	}

	run := &delegatedRun{
		sid:          ss.id,
		agent:        agentID,
		mode:         mode,
		dir:          dir,
		home:         home,
		agentSession: agentSession,
		resumed:      opt.Resume != "",
		startedAt:    startedAt,
		adapter:      adapter,
		excludeFiles: excludeFiles,
		status:       "running",
		stopCh:       make(chan struct{}),
	}
	s.registerDelegate(run)

	turnDone := run.beginTurn()

	// Wait for the interactive TUI to finish drawing its input prompt before
	// pasting so Windows PowerShell / ConPTY init bytes do not trigger early paste.
	waitTUIReady(ss, agentID, delegateReadyWait)
	if in := ss.info(); in.Exited {
		run.finishTurn("failed", "", "delegated agent exited before accepting prompt")
		return run.snapshot(ss), nil
	}

	// For --resume, start tailing at the current end of the resumed transcript
	// file so prior turns never falsely ack or complete the new turn.
	if run.resumed && run.adapter != nil {
		run.seekResumedTranscript(ss)
	}

	go s.tailDelegate(run, ss)
	if err := deliverTurnPrompt(run, ss, opt.Prompt); err != nil {
		run.finishTurn("failed", "", err.Error())
		return run.snapshot(ss), nil
	}

	if opt.Async {
		return run.snapshot(ss), nil
	}

	timeout := delegateDefaultTimout
	if opt.TimeoutMs > 0 {
		timeout = time.Duration(opt.TimeoutMs) * time.Millisecond
	}
	res := s.waitDelegateTurn(run, ss, turnDone, timeout, opt.CancelOnTimeout)
	if res.Status == "completed" && !opt.Keep {
		s.closeDelegatedTab(ss.id)
	}
	return res, nil
}

// doDelegateSend handles "delegate.send": sends a warm follow-up prompt
// into an already-running delegated interactive TUI session.
func (s *Server) doDelegateSend(req Request, _ string, _ *keptConn) (any, error) {
	if req.DelegateSend == nil {
		return nil, errors.New("rex: delegate.send requires options")
	}
	opt := *req.DelegateSend
	if strings.TrimSpace(opt.SID) == "" || strings.TrimSpace(opt.Prompt) == "" {
		return nil, errors.New("rex: delegate.send requires sid and prompt")
	}
	ss, err := s.session(opt.SID)
	if err != nil {
		return nil, err
	}
	if ss.info().Exited {
		return nil, fmt.Errorf("rex: session %q has exited; use --resume instead", opt.SID)
	}
	run := s.getDelegate(opt.SID)
	if run == nil {
		return nil, fmt.Errorf("rex: session %q is not a delegated session", opt.SID)
	}
	turnDone := run.beginTurn()
	if err := deliverTurnPrompt(run, ss, opt.Prompt); err != nil {
		run.finishTurn("failed", "", err.Error())
		return run.snapshot(ss), nil
	}
	if opt.Async {
		return run.snapshot(ss), nil
	}
	timeout := delegateDefaultTimout
	if opt.TimeoutMs > 0 {
		timeout = time.Duration(opt.TimeoutMs) * time.Millisecond
	}
	return s.waitDelegateTurn(run, ss, turnDone, timeout, false), nil
}

// doDelegateResult handles "delegate.result": returns the latest extracted
// result and status of a delegated session.
func (s *Server) doDelegateResult(req Request, _ string, _ *keptConn) (any, error) {
	if strings.TrimSpace(req.SID) == "" {
		return nil, errors.New("rex: delegate.result requires sid")
	}
	run := s.getDelegate(req.SID)
	ss, _ := s.session(req.SID)
	if run == nil {
		if ss == nil {
			return nil, fmt.Errorf("no session %q", req.SID)
		}
		in := ss.info()
		status := "running"
		if in.Exited {
			if in.ExitCode == 0 {
				status = "completed"
			} else {
				status = "failed"
			}
		}
		return DelegateResult{
			Session:    ss.id,
			Agent:      in.DelegatedAgent,
			Status:     status,
			ExitCode:   in.ExitCode,
			RawCapture: sessionPlainText(ss),
		}, nil
	}
	return run.snapshot(ss), nil
}

func (s *Server) registerDelegate(d *delegatedRun) {
	s.mu.Lock()
	if s.delegates == nil {
		s.delegates = make(map[string]*delegatedRun)
	}
	s.delegates[d.sid] = d
	s.mu.Unlock()
}

func (s *Server) getDelegate(sid string) *delegatedRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.delegates == nil {
		return nil
	}
	return s.delegates[sid]
}

// resolveDelegateAgent picks the installed agent binary on this host.
func resolveDelegateAgent(requested, mode, home, shell string) (id, binPath string, err error) {
	if requested != "" && requested != "auto" {
		if mode == "read-only" && !agentSupportsReadOnly(requested) {
			return "", "", fmt.Errorf("rex: agent %q has no read-only flag; refusing --mode read-only", requested)
		}
		if mode == "yolo" && !agentSupportsYolo(requested) {
			return "", "", fmt.Errorf("rex: agent %q has no auto-approve flag; refusing --mode yolo", requested)
		}
		p, ok := findAgentBinary(requested, home, shell)
		if !ok {
			return "", "", fmt.Errorf("rex: agent %q is not installed on this host", requested)
		}
		return requested, p, nil
	}
	order := []string{"claude", "omp", "codex", "agy", "devin"}
	for _, cand := range order {
		if mode == "read-only" && !agentSupportsReadOnly(cand) {
			continue
		}
		if mode == "yolo" && !agentSupportsYolo(cand) {
			continue
		}
		if p, ok := findAgentBinary(cand, home, shell); ok {
			return cand, p, nil
		}
	}
	if mode == "read-only" {
		return "", "", errors.New("rex: no installed agent on this host supports --mode read-only")
	}
	if mode == "yolo" {
		return "", "", errors.New("rex: no installed agent on this host supports --mode yolo")
	}
	return "", "", errors.New("rex: no known coding agent CLI is installed on this host")
}

func agentSupportsReadOnly(agentID string) bool {
	switch agentID {
	case "claude", "codex":
		return true
	}
	return false
}

func agentSupportsYolo(agentID string) bool {
	switch agentID {
	case "claude", "codex":
		return true
	}
	return false
}

// buildDelegateLaunch returns the extra CLI arguments and environment
// overrides for launching agentID in interactive TUI mode.
func buildDelegateLaunch(agentID, mode, resume, agentSession string) (args []string, env []string, err error) {
	if mode == "read-only" && !agentSupportsReadOnly(agentID) {
		return nil, nil, fmt.Errorf("rex: agent %q has no read-only flag; refusing --mode read-only", agentID)
	}
	if mode == "yolo" && !agentSupportsYolo(agentID) {
		return nil, nil, fmt.Errorf("rex: agent %q has no auto-approve flag; refusing --mode yolo", agentID)
	}
	switch agentID {
	case "claude":
		if resume != "" {
			args = append(args, "--resume", resume)
		} else if agentSession != "" {
			args = append(args, "--session-id", agentSession)
		}
		switch mode {
		case "read-only":
			args = append(args, "--permission-mode", "plan")
		case "yolo":
			args = append(args, "--permission-mode", "bypassPermissions")
		}
		// Strip nested-session markers (as in yetone/magpie's cleanClaudeEnv)
		// without overriding entrypoint.
		env = []string{"CLAUDECODE=", "CLAUDE_CODE_ENTRYPOINT=", "CLAUDE_CODE_SSE_PORT="}
	case "omp":
		if resume != "" {
			args = append(args, "--resume", resume)
		}
	case "codex":
		if resume != "" {
			args = append(args, "resume", resume)
		}
		switch mode {
		case "read-only":
			args = append(args, "--sandbox", "read-only")
		case "yolo":
			args = append(args, "--full-auto")
		}
	case "devin":
		env = []string{"DEVIN_SESSION_STORE=files"}
	}
	return args, env, nil
}

func delegateTabName(agentID, from, explicitName, prompt string) string {
	if s := strings.TrimSpace(explicitName); s != "" {
		return s
	}
	if s := summarizeDelegatePrompt(prompt, 28); s != "" {
		return s
	}
	if from != "" {
		return fmt.Sprintf("%s (%s)", agentID, from)
	}
	return agentID
}

// summarizeDelegatePrompt turns the first non-empty line of a prompt into
// a concise sidebar tab label (at most maxRunes runes).
func summarizeDelegatePrompt(prompt string, maxRunes int) string {
	line := strings.TrimSpace(prompt)
	if idx := strings.IndexAny(line, "\r\n"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	line = strings.Join(strings.Fields(line), " ")
	if line == "" {
		return ""
	}
	rs := []rune(line)
	if len(rs) <= maxRunes {
		return line
	}
	return strings.TrimSpace(string(rs[:maxRunes])) + "…"
}

// createDelegatedTab starts the agent session and attaches it to the
// matching project in the layout (or ungrouped tabs) so desktop/iOS/web
// show the live interactive TUI immediately.
func (s *Server) createDelegatedTab(dir, name string, cmd, env []string, peer, from, agentID string) (*session, error) {
	var created *session
	var createErr error

	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	s.mu.Lock()
	raw, rev := s.layout, s.layoutRev
	s.mu.Unlock()
	st := layout.Parse(raw)

	w := matchProjectForDir(&st, dir)
	if w == nil {
		w = noProject(&st)
	}
	cols, rows := 120, 36
	if w.Active >= 0 && w.Active < len(w.Tabs) {
		if c, r := s.area(w.Tabs[w.Active].Root); c > 0 && r > 0 {
			cols, rows = c, r
		}
	} else if c, r := s.largest(w); c > 0 && r > 0 {
		cols, rows = c, r
	}

	ss, err := s.create(CreateOptions{
		Command: cmd,
		Dir:     dir,
		Env:     env,
		Cols:    cols,
		Rows:    rows,
	}, peer)
	if err != nil {
		return nil, err
	}
	ss.mu.Lock()
	ss.delegatedFrom = from
	ss.delegatedAgent = agentID
	ss.mu.Unlock()
	created = ss

	node := &layout.Node{SID: ss.id, Dir: dir}
	t := layout.Tab{ID: layout.NewTabID(), Name: layout.Label(name), Root: node}
	insertTab(w, afterLocal(w), t, false)
	projectUsed(w)

	next, err := json.Marshal(&st)
	if err != nil {
		createErr = err
	} else if _, err := s.commitLayout(next, rev); err != nil {
		createErr = err
	}
	if createErr != nil {
		_ = s.killSession(created.id)
		return nil, createErr
	}
	return created, nil
}

// matchProjectForDir finds the project whose folder equals or contains dir.
func matchProjectForDir(st *layout.Store, dir string) *layout.Workspace {
	if dir == "" {
		return nil
	}
	clean := filepath.Clean(dir)
	var best *layout.Workspace
	for i := range st.Sessions {
		w := &st.Sessions[i]
		if w.Dir == "" {
			continue
		}
		if layout.SameDir(w.Dir, clean) {
			return w
		}
		rel, err := filepath.Rel(filepath.Clean(w.Dir), clean)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if best == nil || len(w.Dir) > len(best.Dir) {
				best = w
			}
		}
	}
	return best
}

func (s *Server) closeDelegatedTab(sid string) {
	if d := s.getDelegate(sid); d != nil {
		d.stop()
	}
	_ = s.killSession(sid)
}

func (s *Server) killSession(sid string) error {
	_, err := s.doKill(Request{Op: "kill", SID: sid}, "", nil)
	return err
}

type tuiSnapshot struct {
	exited     bool
	idle       bool
	output     uint64
	lastOutput time.Time
	bracketed  bool
	plainText  string
}

func waitTUIReady(ss *session, agentID string, maxWait time.Duration) {
	waitTUIReadyWith(func() tuiSnapshot {
		in := ss.info()
		return tuiSnapshot{
			exited:     in.Exited,
			idle:       in.Status != nil && in.Status.State == "idle",
			output:     in.Output,
			lastOutput: in.LastOutput,
			bracketed:  ss.bracketedPaste(),
			plainText:  sessionPlainText(ss),
		}
	}, agentID, maxWait, time.Now, time.Sleep)
}

func waitTUIReadyWith(sample func() tuiSnapshot, agentID string, maxWait time.Duration, nowFn func() time.Time, sleepFn func(time.Duration)) {
	deadline := nowFn().Add(maxWait)
	var prevOut uint64
	var prevTxt string
	var lastChange time.Time
	seenVisible := false

	for {
		now := nowFn()
		snap := sample()
		if snap.exited || snap.idle {
			return
		}

		txt := snap.plainText
		if txt != "" {
			if !seenVisible {
				seenVisible = true
				prevOut = snap.output
				prevTxt = txt
				if !snap.lastOutput.IsZero() && snap.lastOutput.Before(now) {
					lastChange = snap.lastOutput
				} else {
					lastChange = now
				}
			} else if snap.output != prevOut || txt != prevTxt {
				prevOut = snap.output
				prevTxt = txt
				lastChange = now
			}

			quiet := now.Sub(lastChange)
			promptReady := hasPromptIndicator(txt)

			if agentID == "claude" {
				if promptReady && quiet >= delegateReadyQuiet {
					return
				}
			} else {
				if (snap.bracketed || promptReady) && quiet >= delegateReadyQuiet {
					return
				}
				if quiet >= delegateReadyFallbackQuiet {
					return
				}
			}
		}

		if !now.Before(deadline) {
			return
		}
		sleep := delegateReadyPoll
		if rem := deadline.Sub(now); rem < sleep {
			sleep = rem
		}
		if sleep > 0 {
			sleepFn(sleep)
		}
	}
}

// hasPromptIndicator reports whether visible terminal text contains an
// interactive TUI input prompt glyph (❯, ›, or >).
func hasPromptIndicator(txt string) bool {
	if strings.Contains(txt, "❯") || strings.Contains(txt, "›") || strings.Contains(txt, "> ") {
		return true
	}
	for _, line := range strings.Split(txt, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == ">" || strings.HasPrefix(trimmed, "> ") {
			return true
		}
	}
	return false
}

func deliverTurnPrompt(d *delegatedRun, ss *session, prompt string) error {
	d.mu.Lock()
	before := d.promptCount
	if d.promptAckCh == nil {
		d.promptAckCh = make(chan struct{}, 1)
	}
	ackCh := d.promptAckCh
	d.mu.Unlock()
	// Drain any stale ack signal.
	select {
	case <-ackCh:
	default:
	}

	if err := ss.paste(prompt); err != nil {
		return err
	}
	time.Sleep(delegatePasteDelay)

	// Retry Return up to 4 times until the transcript confirms the prompt
	// arrived (matching yetone/magpie's claudetui/tui.go:1399) or the turn
	// already completed.
	for try := 0; try < 4; try++ {
		ss.write([]byte("\r"))
		if d.adapter == nil {
			return nil
		}
		timer := time.NewTimer(delegateEnterWait)
		select {
		case <-ackCh:
			timer.Stop()
			return nil
		case <-ss.done:
			timer.Stop()
			return nil
		case <-timer.C:
			d.mu.Lock()
			accepted := d.promptCount > before || d.status != "running"
			d.mu.Unlock()
			if accepted {
				return nil
			}
		}
	}
	return nil
}

func (s *Server) tailDelegate(d *delegatedRun, ss *session) {
	ticker := time.NewTicker(delegatePollInterval)
	defer ticker.Stop()

	var partial strings.Builder
	for {
		select {
		case <-d.stopCh:
			return
		case <-ss.done:
			// Drain any final lines written right before process exit.
			s.pollTranscriptOnce(d, ss, &partial)
			in := ss.info()
			status := "completed"
			if in.ExitCode != 0 {
				status = "failed"
			}
			if d.finishTurn(status, partial.String(), "") && ss != nil {
				ss.notify()
			}
			return
		case <-ticker.C:
			s.pollTranscriptOnce(d, ss, &partial)
			if st := ss.info().Status; st != nil && st.State == "blocked" {
				if d.finishTurn("blocked", partial.String(), st.Message()) && ss != nil {
					ss.notify()
				}
			}
		}
	}
}

func (d *delegatedRun) seekResumedTranscript(ss *session) {
	if d.adapter == nil {
		return
	}
	d.mu.Lock()
	dir, home, agentSid := d.dir, d.home, d.agentSession
	d.mu.Unlock()

	tty, cfgDir := "", ""
	if ss != nil {
		if ss.p != nil {
			tty = ss.p.tty()
		}
		cfgDir = ss.info().AgentConfigDir
	}
	filePath := d.adapter.FindFile(dir, tty, home, cfgDir, agentSid, time.Time{}, nil)
	if filePath == "" {
		return
	}
	st, err := os.Stat(filePath)
	if err != nil || st.IsDir() {
		return
	}
	d.mu.Lock()
	d.filePath = filePath
	d.fileOffset = st.Size()
	d.mu.Unlock()
}

func (s *Server) pollTranscriptOnce(d *delegatedRun, ss *session, partial *strings.Builder) {
	if d.adapter == nil {
		return
	}
	d.mu.Lock()
	filePath := d.filePath
	offset := d.fileOffset
	dir, home, agentSid, startedAt := d.dir, d.home, d.agentSession, d.startedAt
	exclude := d.excludeFiles
	resumed := d.resumed
	agent := d.agent
	d.mu.Unlock()

	if filePath == "" {
		tty, cfgDir := "", ""
		if ss != nil {
			if ss.p != nil {
				tty = ss.p.tty()
			}
			in := ss.info()
			cfgDir = in.AgentConfigDir
			// Only trust ss.info().AgentSession for claude (which inspects the
			// exact foreground PID). Other agents like omp fall back to the
			// newest file in the directory on Windows and would poison agentSid
			// with an old session ID before the new session writes its file.
			if agent == "claude" && agentSid == "" && in.AgentSession != "" {
				agentSid = in.AgentSession
				d.mu.Lock()
				d.agentSession = agentSid
				d.mu.Unlock()
			}
		}
		if resumed {
			filePath = d.adapter.FindFile(dir, tty, home, cfgDir, agentSid, time.Time{}, nil)
		} else {
			filePath = d.adapter.FindFile(dir, tty, home, cfgDir, agentSid, startedAt, exclude)
		}
		if filePath == "" {
			return
		}
		d.mu.Lock()
		d.filePath = filePath
		if agent == "omp" && d.agentSession == "" {
			if id := ompID(filepath.Base(filePath)); id != "" {
				d.agentSession = id
			}
		}
		d.mu.Unlock()
	}

	f, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
	}

	r := bufio.NewReader(f)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && line[len(line)-1] == '\n' {
			offset += int64(len(line))
			ev := d.adapter.ParseRow(line)
			if ev.PromptAccepted {
				d.mu.Lock()
				d.promptCount++
				ch := d.promptAckCh
				d.mu.Unlock()
				if ch != nil {
					select {
					case ch <- struct{}{}:
					default:
					}
				}
			}
			if ev.SessionID != "" {
				d.mu.Lock()
				if d.agentSession == "" {
					d.agentSession = ev.SessionID
				}
				d.mu.Unlock()
			}
			if ev.AssistantText != "" {
				if partial.Len() > 0 {
					partial.WriteByte('\n')
				}
				partial.WriteString(ev.AssistantText)
			}
			if ev.TurnDone {
				text := partial.String()
				partial.Reset()
				var changed bool
				if ev.IsError {
					changed = d.finishTurn("failed", text, ev.ErrorText)
				} else {
					changed = d.finishTurn("completed", text, "")
				}
				if changed && ss != nil {
					ss.notify()
				}
			}
		}
		if err != nil {
			break
		}
	}
	d.mu.Lock()
	d.fileOffset = offset
	d.mu.Unlock()
}

func (s *Server) waitDelegateTurn(d *delegatedRun, ss *session, turnDone <-chan struct{}, timeout time.Duration, cancelOnTimeout bool) DelegateResult {
	var timer <-chan time.Time
	if timeout > 0 {
		t := time.NewTimer(timeout)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-turnDone:
		return d.snapshot(ss)
	case <-ss.done:
		// Wait briefly for tailDelegate to finish draining.
		select {
		case <-turnDone:
		case <-time.After(300 * time.Millisecond):
		}
		return d.snapshot(ss)
	case <-timer:
		if cancelOnTimeout {
			s.closeDelegatedTab(ss.id)
			res := d.snapshot(ss)
			res.Status = "timeout"
			res.Error = "timed out waiting for delegated turn (session cancelled)"
			return res
		}
		res := d.snapshot(ss)
		res.Status = "running"
		res.Error = "timed out waiting for delegated turn (session still running)"
		return res
	}
}

func sessionPlainText(ss *session) string {
	if ss == nil {
		return ""
	}
	return ss.plainText()
}

func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b && i+1 < len(s) {
			next := s[i+1]
			switch next {
			case '[': // CSI
				i += 2
				for i < len(s) && (s[i] < 0x40 || s[i] > 0x7e) {
					i++
				}
				continue
			case ']', 'P', '_', '^': // OSC, DCS, APC, PM
				i += 2
				for i < len(s) && s[i] != 0x07 {
					if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
						i++
						break
					}
					i++
				}
				continue
			default:
				// Intermediate bytes (e.g. \x1b(B) followed by final byte 0x30..0x7e,
				// or 2-byte ESC sequence (e.g. \x1bH, \x1bM, \x1b=, \x1b>).
				if next >= 0x20 && next <= 0x2f {
					i += 2
					for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
						i++
					}
					continue
				}
				if next >= 0x30 && next <= 0x7e {
					i++
					continue
				}
			}
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
}

func newDelegateUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	var out [36]byte
	hex.Encode(out[0:8], b[0:4])
	out[8] = '-'
	hex.Encode(out[9:13], b[4:6])
	out[13] = '-'
	hex.Encode(out[14:18], b[6:8])
	out[18] = '-'
	hex.Encode(out[19:23], b[8:10])
	out[23] = '-'
	hex.Encode(out[24:36], b[10:16])
	return string(out[:])
}
