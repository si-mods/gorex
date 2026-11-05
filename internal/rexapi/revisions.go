package rexapi

import (
	"encoding/json"
	"strconv"
	"strings"
)

// revisionChange is one change Rex's API revision history documents
// (internal/rexapi/testdata is the revision 10 shape):
//
//	r1 renames session.new_split's anchor_block_id to anchor_id;
//	r2 hides the API revisions server.status reports;
//	r6 hides terminal format's paging fields from clients that predate them;
//	r8 hides session.bootstrap's data_tickets from clients that predate it;
//	r9 hides a layer's state from clients that predate minimized layers;
//	r10 hides Tailscale's control_url from clients that predate it.
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
	// Revision 9 adds a floating layer's state, minimized or normal, to
	// every view that carries layers. A client that predates it sees the
	// layer as normal, since it knows no state, and draws it; that is
	// what Rex's own translation does with the member removed.
	{at: 9, kind: "hide", method: "session.attach", paths: []string{
		"/view/windows/-/layers/-/state",
	}},
	{at: 9, kind: "hide", method: "session.bootstrap", paths: []string{
		"/attached_sessions/-/view/windows/-/layers/-/state",
		"/selected_session/view/windows/-/layers/-/state",
	}},
	{at: 9, kind: "hide", method: "session.view", paths: []string{
		"/windows/-/layers/-/state",
	}},
	// Revision 10 adds Tailscale's control server to every status that
	// names the device, and the control_url a call may start it with.
	{at: 10, kind: "hide", method: "server.status", paths: []string{"/tailscale/control_url"}},
	{at: 10, kind: "hide", method: "server.tailscale.disable", paths: []string{"/tailscale/control_url"}},
	{at: 10, kind: "hide", method: "server.tailscale.enable", paths: []string{
		"/control_url", "/tailscale/control_url",
	}},
	{at: 10, kind: "hide", method: "server.tailscale.login", paths: []string{"/tailscale/control_url"}},
	{at: 10, kind: "hide", method: "server.tailscale.logout", paths: []string{"/tailscale/control_url"}},
	{at: 10, kind: "hide", method: "server.tailscale.rename", paths: []string{"/tailscale/control_url"}},
	{at: 10, kind: "hide", method: "session.bootstrap", paths: []string{
		"/server_status/tailscale/control_url",
	}},
	{at: 10, kind: "hide", method: "tailscale_status_changed", paths: []string{"/control_url"}},
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
	"session.set_layer_state":         9,
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

// deletePointer removes the value at path, when it is there. A "-"
// segment walks every item of an array, as Rex's revision history
// writes a path that hides a member of each item.
func deletePointer(v any, path string) {
	deleteAt(v, pointerParts(path))
}

func deleteAt(v any, parts []string) {
	if len(parts) == 0 {
		return
	}
	switch x := v.(type) {
	case map[string]any:
		if len(parts) == 1 {
			delete(x, parts[0])
			return
		}
		deleteAt(x[parts[0]], parts[1:])
	case []any:
		if parts[0] == "-" {
			for _, item := range x {
				deleteAt(item, parts[1:])
			}
			return
		}
		if i, err := strconv.Atoi(parts[0]); err == nil && i >= 0 && i < len(x) {
			deleteAt(x[i], parts[1:])
		}
	}
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
