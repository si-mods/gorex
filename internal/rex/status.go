package rex

import (
	"cmp"
	"encoding/base64"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// statusReply answers a program asking whether the terminal knows the
// protocol.
var statusReply = []byte("\x1b]7501;?\x1b\\")

// Limits of the protocol: a report past one is dropped whole.
const (
	maxStatusSeq     = 4096
	maxStatusRecords = 64
)

// statusRecord is a record a program reported; at orders the updates.
type statusRecord struct {
	ProgramStatus
	at uint64
}

// programStatus reads a session's output for status reports, and keeps
// the records they make, by id ("" for the root).
type programStatus struct {
	// carry is the start of a sequence a read cut, joined to the next.
	carry   []byte
	records map[string]*statusRecord
	updates uint64
	// seen is when the latest done or error record a client showed the
	// user was updated: those till then were seen (MostUrgent).
	// It only moves forward.
	seen time.Time
}

// feed reads output for reports, prompts and resets, and returns how many
// times the program asked whether the terminal knows the protocol.
func (st *programStatus) feed(p []byte) int {
	return feedOSC(
		&st.carry,
		p,
		"7501",
		maxStatusSeq,
		func() {
			if len(st.records) > 0 {
				st.updates++
			}
			clear(st.records)
		},
		st.drop,
		st.report,
	)
}

// drop drops the records of what was running, as its program ended or a
// prompt came: done and error stay until seen.
func (st *programStatus) drop() {
	changed := false
	for id, r := range st.records {
		if r.State != "done" && r.State != "error" {
			delete(st.records, id)
			changed = true
		}
	}
	if changed {
		st.updates++
	}
}

// see raises seen to at, the time of a done or error record a client
// showed, but past no such record's: a later one was not seen, and
// others do not need to be. It tells whether seen moved.
func (st *programStatus) see(at time.Time) bool {
	var latest time.Time
	for _, r := range st.records {
		if (r.State == "done" || r.State == "error") && r.Updated.After(latest) {
			latest = r.Updated
		}
	}
	if at.After(latest) {
		at = latest
	}
	if !at.After(st.seen) {
		return false
	}
	st.seen = at
	return true
}

// report applies a report's body, unless it breaks a rule.
func (st *programStatus) report(body string) {
	kv := map[string]string{}
	for _, pair := range strings.Split(body, ":") {
		k, v, ok := strings.Cut(pair, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !isStatusKey(k) || !isStatusValue(v) {
			continue // a malformed pair is skipped
		}
		if len(k) > 16 {
			return
		}
		kv[k] = v
	}
	id, hasID := kv["id"]
	if hasID && !isStatusID(id) {
		return
	}
	r := ProgramStatus{ID: id, State: kv["state"], Progress: -1, Updated: time.Now()}
	switch r.State {
	case "clear":
		st.updates++
		if !hasID {
			clear(st.records)
			return
		}
		for k := range st.records {
			if k == id || strings.HasPrefix(k, id+"/") {
				delete(st.records, k)
			}
		}
		return
	case "idle", "working", "blocked", "done", "error":
	default:
		return
	}
	if app, ok := kv["app"]; ok {
		if len(app) > 32 {
			return
		}
		if isSegment(app) {
			r.App = app
		}
	}
	var ok bool
	if r.Title, ok = decodeStatusText(kv["title"], 256, 192); !ok {
		return
	}
	if r.Msg, ok = decodeStatusText(kv["msg"], 2732, 2048); !ok {
		return
	}
	if r.State == "working" || r.State == "blocked" {
		if n, err := strconv.Atoi(kv["progress"]); err == nil && n >= 0 && n <= 100 {
			r.Progress = n
		}
	}
	if k := kv["kind"]; r.State == "blocked" && (k == "permission" || k == "question" || k == "auth") {
		r.Kind = k
	}
	old := st.records[id]
	if isAntigravityIdleArtifact(r, st.appOf(id, r.App)) {
		switch {
		case old == nil:
			r.State, r.Kind, r.Msg, r.Progress = "idle", "", "", -1
		case old.State == "idle" || old.State == "done" || old.State == "error":
			return
		default:
			r.State, r.Kind, r.Msg, r.Progress = "done", "", "", -1
		}
	}
	if !r.Updated.After(st.seen) {
		r.Updated = st.seen.Add(time.Nanosecond)
	}
	if st.records == nil {
		st.records = map[string]*statusRecord{}
	}
	if old == nil && len(st.records) >= maxStatusRecords {
		var oldest *statusRecord
		var oldestID string
		for k, rec := range st.records {
			if oldest == nil || rec.at < oldest.at {
				oldest, oldestID = rec, k
			}
		}
		delete(st.records, oldestID)
	}
	if old != nil {
		r.LastMsg = old.LastMsg
	}
	if r.State == "working" {
		r.LastMsg = cmp.Or(r.Msg, r.LastMsg)
	}
	st.updates++
	st.records[id] = &statusRecord{r, st.updates}
}

// decodeStatusText decodes a title or message, base64 of UTF-8 text
// without control characters; ok is false when it breaks a limit.
func decodeStatusText(v string, maxEncoded, maxDecoded int) (string, bool) {
	if len(v) > maxEncoded {
		return "", false
	}
	b, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(v, "="))
	if err != nil || len(b) > maxDecoded || !utf8.Valid(b) {
		return "", false
	}
	s := string(b)
	for _, r := range s {
		if r < 0x20 || r >= 0x7f && r < 0xa0 {
			return "", false
		}
	}
	// Bidi controls would turn what the window shows around it.
	return strings.Map(func(r rune) rune {
		if r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069 {
			return -1
		}
		return r
	}, s), true
}

func isStatusKey(k string) bool {
	return k != "" && strings.Trim(k, "abcdefghijklmnopqrstuvwxyz") == ""
}

func isStatusValue(v string) bool {
	return strings.Trim(v, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.,+/=-") == ""
}

func isSegment(s string) bool {
	return s != "" && len(s) <= 32 && strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_.+-") == ""
}

// isStatusID tells an id of 1 to 8 segments, of 128 bytes at most.
func isStatusID(id string) bool {
	segs := strings.Split(id, "/")
	if len(id) > 128 || len(segs) > 8 {
		return false
	}
	for _, s := range segs {
		if !isSegment(s) {
			return false
		}
	}
	return true
}

// appOf returns app when set, else the nearest ancestor record's app.
func (st *programStatus) appOf(id, app string) string {
	for app == "" && id != "" {
		id = id[:max(strings.LastIndexByte(id, '/'), 0)]
		if p, ok := st.records[id]; ok {
			app = p.App
		}
	}
	return app
}

// isAntigravityIdleArtifact reports Antigravity CLI's idle turn-end report
// when an artifact exists in the conversation ("1 artifact · /artifact to
// review"): it sends state=blocked, kind=permission, msg="Approve plan" at its
// ">" prompt instead of done or idle.
func isAntigravityIdleArtifact(r ProgramStatus, app string) bool {
	return r.State == "blocked" && r.Kind == "permission" &&
		strings.EqualFold(app, "antigravity") && r.Msg == "Approve plan"
}

// list returns the records by id. A record without app takes its
// nearest ancestor's.
func (st *programStatus) list() []ProgramStatus {
	var out []ProgramStatus
	for id, r := range st.records {
		s := r.ProgramStatus
		s.App = st.appOf(id, s.App)
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b ProgramStatus) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// StatusRank orders the states by how much they need the user: blocked,
// error, done, working, idle; 0 for none.
func StatusRank(state string) int {
	return map[string]int{"idle": 1, "working": 2, "done": 3, "error": 4, "blocked": 5}[state]
}

// MostUrgent returns the record that most needs the user, the latest of
// those alike, nil without one. Done and error records updated by seen
// were seen, and do not count.
func MostUrgent(rs []ProgramStatus, seen time.Time) *ProgramStatus {
	var best *ProgramStatus
	for i, r := range rs {
		if (r.State == "done" || r.State == "error") && !r.Updated.After(seen) {
			continue
		}
		if best == nil || StatusRank(r.State) > StatusRank(best.State) ||
			StatusRank(r.State) == StatusRank(best.State) && r.Updated.After(best.Updated) {
			best = &rs[i]
		}
	}
	return best
}
