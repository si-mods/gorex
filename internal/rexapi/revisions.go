package rexapi

import (
	"encoding/json"
	"strings"
)

// revisionChange is one change Rex's API revision history documents
// (internal/rexapi/testdata is the revision 8 shape):
//
//	r1 renames session.new_split's anchor_block_id to anchor_id;
//	r2 hides the API revisions server.status reports;
//	r6 hides terminal format's paging fields from clients that predate them.
//
// A client that speaks revision R sees the shape that was current at R:
// requests are translated up to the shape this server implements, and
// results back down to the client's shape.
type revisionChange struct {
	at     int
	kind   string // "rename" or "hide"
	method string
	from   string
	to     string
	paths  []string
}

var revisionChanges = []revisionChange{
	{at: 1, kind: "rename", method: "session.new_split", from: "/anchor_block_id", to: "/anchor_id"},
	{at: 2, kind: "hide", method: "server.status", paths: []string{"/api_revision", "/api_min_revision"}},
	{at: 2, kind: "hide", method: "session.bootstrap", paths: []string{
		"/server_status/api_revision", "/server_status/api_min_revision",
	}},
	{at: 6, kind: "hide", method: "com.superlogical.terminal.format", paths: []string{
		"/args/max_bytes", "/id", "/total", "/eof",
	}},
	// Revision 8 is GoRex's own: session.bootstrap's data_tickets did not
	// exist on the Rex revision 7 servers the clients were built for.
	{at: 8, kind: "hide", method: "session.bootstrap", paths: []string{"/data_tickets"}},
}

// creatorSince is when a block creator first appeared. A client that
// speaks an older revision does not know it.
var creatorSince = map[string]int{
	"com.superlogical.onboarding": 7,
}

// methodSince is when a method first appeared. A client that speaks an
// older revision does not know it, so asking for it is method_not_found,
// as on a server of that revision.
var methodSince = map[string]int{
	"com.superlogical.terminal.modes": 3,
	"server.stop":                     4,
	"server.apple.privacy_status":     5,
}

// methodAvailable tells whether a client at revision rev may call method.
func methodAvailable(rev int, method string) bool {
	since, ok := methodSince[method]
	return !ok || rev >= since
}

// changesFor returns the changes that a client at rev predates.
func changesFor(rev int, method string) []revisionChange {
	var out []revisionChange
	for _, ch := range revisionChanges {
		if ch.method == method && rev < ch.at {
			out = append(out, ch)
		}
	}
	return out
}

// transformRequest translates a request payload from a client's revision
// to the shape this server implements.
func transformRequest(rev int, method string, raw json.RawMessage) json.RawMessage {
	changes := changesFor(rev, method)
	if len(changes) == 0 || len(raw) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	for _, ch := range changes {
		switch ch.kind {
		case "rename":
			renamePointer(v, ch.from, ch.to)
		case "hide":
			for _, p := range ch.paths {
				deletePointer(v, p)
			}
		}
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

// transformResult translates a result from this server's shape back to
// the client's revision.
func transformResult(rev int, method string, payload any) any {
	changes := changesFor(rev, method)
	if len(changes) == 0 || payload == nil {
		return payload
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return payload
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return payload
	}
	for _, ch := range changes {
		switch ch.kind {
		case "rename":
			renamePointer(v, ch.to, ch.from)
		case "hide":
			for _, p := range ch.paths {
				deletePointer(v, p)
			}
		}
	}
	return v
}

// pointerParts splits a JSON pointer into its segments.
func pointerParts(path string) []string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	parts := strings.Split(path, "/")
	for i, p := range parts {
		p = strings.ReplaceAll(p, "~1", "/")
		parts[i] = strings.ReplaceAll(p, "~0", "~")
	}
	return parts
}

// pointerParent walks to the map that holds the last segment of path.
func pointerParent(v any, path string) (map[string]any, string, bool) {
	parts := pointerParts(path)
	if len(parts) == 0 {
		return nil, "", false
	}
	cur, ok := v.(map[string]any)
	if !ok {
		return nil, "", false
	}
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			return nil, "", false
		}
		cur = next
	}
	return cur, parts[len(parts)-1], true
}

// deletePointer removes the value at path, when it is there.
func deletePointer(v any, path string) {
	parent, key, ok := pointerParent(v, path)
	if !ok {
		return
	}
	delete(parent, key)
}

// renamePointer moves the value at from to to, when from is there.
func renamePointer(v any, from, to string) {
	src, key, ok := pointerParent(v, from)
	if !ok {
		return
	}
	value, ok := src[key]
	if !ok {
		return
	}
	delete(src, key)
	dst, dstKey, ok := pointerParent(v, to)
	if !ok {
		return
	}
	dst[dstKey] = value
}
