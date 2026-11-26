//go:build darwin || linux || windows

package rex

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// transcriptEvent is a normalized event parsed from an agent's JSONL
// transcript file (docs/MOD-016-peer-delegation.md).
type transcriptEvent struct {
	PromptAccepted bool
	AssistantText  string
	TurnDone       bool
	IsError        bool
	ErrorText      string
	SessionID      string
}

// agentTranscript locates and parses an interactive agent's local JSONL
// transcript file so delegated turns return clean assistant text without
// scraping ANSI from the PTY.
type agentTranscript interface {
	FindFile(dir, tty, home, configDir, agentSession string, startedAt time.Time) string
	ParseRow(line []byte) transcriptEvent
}

func transcriptAdapterFor(agentID string) agentTranscript {
	switch agentID {
	case "claude":
		return claudeTranscript{}
	case "omp":
		return ompTranscript{}
	case "codex":
		return codexTranscript{}
	case "agy":
		return agyTranscript{}
	case "devin":
		return devinTranscript{}
	default:
		return nil
	}
}

// claudeTranscript reads Claude Code's session JSONL at
// <configDir>/projects/<claudeSlug(dir)>/<uuid>.jsonl (matching
// yetone/magpie's internal/claudetui/transcript.go).
type claudeTranscript struct{}

func (claudeTranscript) FindFile(dir, _, home, configDir, agentSession string, _ time.Time) string {
	if agentSession == "" {
		return ""
	}
	base := configDir
	if base == "" {
		base = os.Getenv("CLAUDE_CONFIG_DIR")
	}
	if base == "" {
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".claude")
	}
	if dir != "" {
		p := filepath.Join(base, "projects", claudeSlug(dir), agentSession+".jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	matches, _ := filepath.Glob(filepath.Join(base, "projects", "*", agentSession+".jsonl"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func (claudeTranscript) ParseRow(b []byte) transcriptEvent {
	var r struct {
		Type        string          `json:"type"`
		Subtype     string          `json:"subtype"`
		IsSidechain bool            `json:"isSidechain"`
		IsMeta      bool            `json:"isMeta"`
		IsAPIError  bool            `json:"isApiErrorMessage"`
		Operation   string          `json:"operation"`
		SessionID   string          `json:"sessionId"`
		Message     json.RawMessage `json:"message"`
	}
	if json.Unmarshal(b, &r) != nil || r.IsSidechain {
		return transcriptEvent{}
	}
	ev := transcriptEvent{SessionID: r.SessionID}
	switch r.Type {
	case "queue-operation":
		if r.Operation == "enqueue" {
			ev.PromptAccepted = true
		}
	case "user":
		if !r.IsMeta && isClaudePromptMessage(r.Message) {
			ev.PromptAccepted = true
		}
	case "assistant":
		var m struct {
			Model   string `json:"model"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(r.Message, &m) != nil {
			return ev
		}
		var sb strings.Builder
		for _, c := range m.Content {
			if c.Type == "text" && c.Text != "" {
				sb.WriteString(c.Text)
			}
		}
		text := sb.String()
		if m.Model == "<synthetic>" && r.IsAPIError {
			ev.TurnDone = true
			ev.IsError = true
			ev.ErrorText = text
			return ev
		}
		ev.AssistantText = text
	case "system":
		if r.Subtype == "turn_duration" {
			ev.TurnDone = true
		}
	}
	return ev
}

func isClaudePromptMessage(raw json.RawMessage) bool {
	var m struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &m) != nil || len(m.Content) == 0 {
		return false
	}
	if m.Content[0] == '"' {
		return true
	}
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(m.Content, &blocks) != nil || len(blocks) == 0 {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return false
		}
	}
	return true
}

// ompTranscript reads oh-my-pi / Pi session JSONL files using GoRex's
// existing ompDirs and ompSlug helpers (internal/rex/omp.go).
type ompTranscript struct{}

func (ompTranscript) FindFile(dir, tty, home, _, agentSession string, startedAt time.Time) string {
	env := func(k string) string { return os.Getenv(k) }
	if tty != "" {
		if _, _, file := ompSession(env, dir, nil, tty, home); file != "" {
			return file
		}
	}
	data, _ := ompDirs(env, nil, home)
	if data == "" || dir == "" {
		return ""
	}
	sessDir := filepath.Join(data, "sessions", ompSlug(dir, home))
	ents, err := os.ReadDir(sessDir)
	if err != nil {
		return ""
	}
	var best string
	var bestMod time.Time
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if agentSession != "" && !strings.Contains(e.Name(), agentSession) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if agentSession == "" && info.ModTime().Before(startedAt.Add(-2*time.Second)) {
			continue
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = filepath.Join(sessDir, e.Name())
			bestMod = info.ModTime()
		}
	}
	return best
}

func (ompTranscript) ParseRow(b []byte) transcriptEvent {
	var r struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Message struct {
			Role       string          `json:"role"`
			StopReason string          `json:"stopReason"`
			Content    json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal(b, &r) != nil {
		return transcriptEvent{}
	}
	var ev transcriptEvent
	if r.Type == "session" && r.ID != "" {
		ev.SessionID = r.ID
		return ev
	}
	if r.Type != "message" {
		return ev
	}
	switch r.Message.Role {
	case "user":
		ev.PromptAccepted = true
	case "assistant":
		ev.AssistantText = extractContentText(r.Message.Content)
		switch r.Message.StopReason {
		case "stop", "end_turn", "length":
			ev.TurnDone = true
		case "error", "aborted":
			ev.TurnDone = true
			ev.IsError = true
			ev.ErrorText = ev.AssistantText
		}
	}
	return ev
}

// codexTranscript reads Codex rollout JSONL files under
// <CODEX_HOME or home/.codex>/sessions/.
type codexTranscript struct{}

func (codexTranscript) FindFile(_, _, home, _, agentSession string, startedAt time.Time) string {
	base := os.Getenv("CODEX_HOME")
	if base == "" {
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".codex")
	}
	root := filepath.Join(base, "sessions")
	return newestJSONL(root, agentSession, startedAt)
}

func (codexTranscript) ParseRow(b []byte) transcriptEvent {
	var r struct {
		Type    string `json:"type"`
		Payload struct {
			Type    string          `json:"type"`
			ID      string          `json:"id"`
			Role    string          `json:"role"`
			Message string          `json:"message"`
			Content json.RawMessage `json:"content"`
		} `json:"payload"`
	}
	if json.Unmarshal(b, &r) != nil {
		return transcriptEvent{}
	}
	var ev transcriptEvent
	switch r.Type {
	case "session_meta":
		ev.SessionID = r.Payload.ID
	case "response_item":
		switch r.Payload.Role {
		case "user":
			ev.PromptAccepted = true
		case "assistant":
			ev.AssistantText = extractContentText(r.Payload.Content)
		}
	case "event_msg":
		switch r.Payload.Type {
		case "user_message":
			ev.PromptAccepted = true
		case "agent_message":
			if r.Payload.Message != "" {
				ev.AssistantText = r.Payload.Message
			}
		case "task_complete", "turn_complete":
			ev.TurnDone = true
		}
	}
	return ev
}

// agyTranscript reads Antigravity CLI transcripts at
// <agyDir>/brain/<id>/.system_generated/logs/transcript_full.jsonl.
type agyTranscript struct{}

func (agyTranscript) FindFile(_, _, home, _, agentSession string, startedAt time.Time) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if home == "" {
		return ""
	}
	root := filepath.Join(home, ".gemini", "antigravity-cli", "brain")
	if agentSession != "" {
		p := filepath.Join(root, agentSession, ".system_generated", "logs", "transcript_full.jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return newestJSONL(root, "transcript_full.jsonl", startedAt)
}

func (agyTranscript) ParseRow(b []byte) transcriptEvent {
	var r struct {
		Type    string `json:"type"`
		Content string `json:"content"`
		Text    string `json:"text"`
	}
	if json.Unmarshal(b, &r) != nil {
		return transcriptEvent{}
	}
	var ev transcriptEvent
	switch r.Type {
	case "USER_INPUT":
		ev.PromptAccepted = true
	case "PLANNER_RESPONSE", "ASSISTANT_RESPONSE":
		if r.Content != "" {
			ev.AssistantText = r.Content
		} else {
			ev.AssistantText = r.Text
		}
	}
	return ev
}

// devinTranscript reads Devin CLI file-store transcripts when launched
// with DEVIN_SESSION_STORE=files at <data>/devin/cli/sessions/<id>/log.jsonl.
type devinTranscript struct{}

func (devinTranscript) FindFile(_, _, home, _, agentSession string, startedAt time.Time) string {
	base := os.Getenv("DEVIN_SESSIONS_DIR")
	if base == "" {
		if runtime.GOOS == "windows" {
			if ad := os.Getenv("APPDATA"); ad != "" {
				base = filepath.Join(ad, "devin", "cli")
			}
		} else {
			data := os.Getenv("XDG_DATA_HOME")
			if data == "" {
				if home == "" {
					home, _ = os.UserHomeDir()
				}
				if home != "" {
					data = filepath.Join(home, ".local", "share")
				}
			}
			if data != "" {
				base = filepath.Join(data, "devin", "cli")
			}
		}
	}
	if base == "" {
		return ""
	}
	root := filepath.Join(base, "sessions")
	if agentSession != "" {
		p := filepath.Join(root, agentSession, "log.jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return newestJSONL(root, "log.jsonl", startedAt)
}

func (devinTranscript) ParseRow(b []byte) transcriptEvent {
	var r struct {
		Role    string          `json:"role"`
		Type    string          `json:"type"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(b, &r) != nil {
		return transcriptEvent{}
	}
	var ev transcriptEvent
	switch r.Role {
	case "user":
		ev.PromptAccepted = true
	case "assistant":
		ev.AssistantText = extractContentText(r.Content)
	}
	if r.Type == "turn_end" || r.Type == "done" {
		ev.TurnDone = true
	}
	return ev
}

func extractContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var sb strings.Builder
	for _, blk := range blocks {
		if (blk.Type == "text" || blk.Type == "output_text" || blk.Type == "") && blk.Text != "" {
			sb.WriteString(blk.Text)
		}
	}
	return sb.String()
}

func newestJSONL(root, filter string, startedAt time.Time) string {
	var best string
	var bestMod time.Time
	cutoff := startedAt.Add(-2 * time.Second)
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".jsonl") {
			return nil
		}
		if filter != "" && !strings.Contains(p, filter) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.ModTime().Before(cutoff) {
			return nil
		}
		if best == "" || info.ModTime().After(bestMod) {
			best = p
			bestMod = info.ModTime()
		}
		return nil
	})
	return best
}
