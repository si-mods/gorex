package push

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"gorex/internal/rex"
)

// The limits of TerminalActivity (ios/Shared/TerminalActivity.swift):
// the most characters and UTF-8 bytes of a line, and of a title or a
// name. The whole request to the relay stays under its 4 KB with them.
const (
	MaxLine      = 80
	MaxLineBytes = 240
	MaxText      = 200
	MaxTextBytes = 400
)

// ReferenceDate is unix seconds of Apple's 2001-01-01 reference date:
// Swift's Codable encodes a Date as the seconds since it, and a pushed
// record's updated is read back as one.
const ReferenceDate = 978307200

// Record is the pushed form of a program-status record: the fields of
// TerminalActivity.Record.
type Record struct {
	// ID is the record's, "" for the root record: the phone reports it
	// seen (gorex.push.seen), so it goes even when empty.
	ID    string `json:"id"`
	State string `json:"state"`
	Kind  string `json:"kind,omitempty"`
	// Progress is nil when the record reports none; Updated the seconds
	// since ReferenceDate, nil when the server does not say.
	Progress *int     `json:"progress,omitempty"`
	Title    string   `json:"title,omitempty"`
	Updated  *float64 `json:"updated,omitempty"`
}

// ContentState is TerminalActivity.ContentState as the host builds it
// (NET-003, Content state): no screen line, so a program that reports no
// message gets none.
type ContentState struct {
	Title     string  `json:"title"`
	Program   string  `json:"program,omitempty"`
	Record    *Record `json:"record,omitempty"`
	Line      string  `json:"line"`
	Connected bool    `json:"connected"`
}

// StateFor computes the content state of info's block, as the app's
// TerminalLive.state does: the window's title (its label, then the
// block's, then the session's), the record's program or the block's,
// the most urgent record — an idle one has no record, only its message —
// and the line its message, or for a finished one its last message,
// gives.
func StateFor(info *rex.SessionInfo, windowTitle, blockProgram, blockLabel, sessionLabel string) ContentState {
	u := rex.MostUrgent(info.Statuses, info.StatusSeen)
	var rec *Record
	program := cleanText(blockProgram)
	if u != nil && u.State != "idle" {
		rec = &Record{ID: u.ID, State: u.State, Kind: u.Kind}
		if u.Progress >= 0 {
			p := u.Progress
			rec.Progress = &p
		}
		if t := cleanText(u.Title); t != "" {
			rec.Title = t
		}
		if !u.Updated.IsZero() {
			sec := float64(u.Updated.Unix() - ReferenceDate)
			rec.Updated = &sec
		}
		if app := cleanText(u.App); app != "" {
			program = app
		}
	}
	line := ""
	if u != nil {
		line = statusText(u.Msg)
		if line == "" && (u.State == "done" || u.State == "error") {
			line = statusText(u.LastMsg)
		}
	}
	return ContentState{
		Title:     cutTitle(windowTitle, blockLabel, sessionLabel),
		Program:   program,
		Record:    rec,
		Line:      cut(line, MaxLine, MaxLineBytes),
		Connected: true,
	}
}

// cleanText cleans s with ShownText, trims whitespace, and cuts to MaxText.
func cleanText(s string) string {
	t := strings.TrimSpace(ShownText(s))
	if t == "" {
		return ""
	}
	return cut(t, MaxText, MaxTextBytes)
}

// cutTitle returns the first of the labels that is not blank, cleaned, trimmed
// and cut to MaxText.
func cutTitle(labels ...string) string {
	for _, l := range labels {
		if l = cleanText(l); l != "" {
			return l
		}
	}
	return ""
}

// statusText is a record's message as one line, "" when it is blank, collapsing
// CR/LF runs into single spaces before ShownText (matching TerminalLive.swift).
func statusText(s string) string {
	joined := strings.Join(strings.FieldsFunc(s, func(r rune) bool {
		return r == '\n' || r == '\r'
	}), " ")
	return strings.TrimSpace(ShownText(joined))
}

// cut returns s cut to at most max characters and at most bytes UTF-8
// bytes, whole characters only, as TerminalActivity.cut does. A
// character here is a grapheme cluster the way terminals draw them:
// combining marks, variation selectors, emoji modifiers, keycaps and
// tags join the rune before them, a ZWJ joins the rune after it, and a
// regional indicator pairs with one more — the rules of UAX #29 the
// texts a terminal shows carry.
func cut(s string, max, bytes int) string {
	// A cluster count never exceeds the rune count, so a string within
	// the rune limit is within max.
	if len(s) <= bytes && utf8.RuneCountInString(s) <= max {
		return s
	}
	n, used, end := 0, 0, 0
	for end < len(s) {
		next := clusterEnd(s, end)
		if n == max || used+(next-end) > bytes {
			break
		}
		n++
		used += next - end
		end = next
	}
	return s[:end]
}

// clusterEnd returns the end of the grapheme cluster at i, as cut counts
// it.
func clusterEnd(s string, i int) int {
	first, size := utf8.DecodeRuneInString(s[i:])
	j := i + size
	// flag tracks a run of regional indicators, joined pairwise, and
	// pairs those joined: a flag is two.
	flag := regional(first)
	pairs := 0
	for j < len(s) {
		r, size := utf8.DecodeRuneInString(s[j:])
		switch {
		case r == 0x200D:
			// A ZWJ and the rune it joins are the cluster's.
			j += size
			flag = false
			if j < len(s) {
				_, sz := utf8.DecodeRuneInString(s[j:])
				j += sz
			}
		case extends(r):
			j += size
		case flag && regional(r) && pairs%2 == 0:
			j += size
			pairs++
		default:
			return j
		}
	}
	return j
}

// regional tells whether r is a regional indicator (a flag's letter).
func regional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// extends tells whether r continues the cluster before it: a combining
// mark, a variation selector, an emoji modifier, a keycap or a tag.
func extends(r rune) bool {
	switch {
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Mc, r):
		return true
	case r >= 0xFE00 && r <= 0xFE0F: // variation selectors
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // variation selectors, supplementary
		return true
	case r == 0x20E3: // keycap
		return true
	case r >= 0x1F3FB && r <= 0x1F3FF: // emoji modifiers
		return true
	case r >= 0xE0020 && r <= 0xE007F: // tag characters
		return true
	}
	return false
}
