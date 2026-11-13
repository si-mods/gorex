package rexapi

import (
	"encoding/json"
	"time"

	"gorex/internal/rex"
)

// gorex.see_block tells that the user saw a block's program status, as the
// desktop's pane does when it shows: the GoRex server keeps when the
// records were seen till, per session (rex.CapabilityStatusSeen), and
// every client reads it back as seen_at, GoRex's own, beside the records
// of com.superlogical.terminal.program_status and program_status_changed.
// Done and error records updated by seen_at were seen. The welcome lists
// featureSeeBlock when the server keeps it.

// featureSeeBlock is the welcome's feature of a gateway whose server keeps
// when a block's status was seen (gorex.see_block, seen_at).
const featureSeeBlock = "gorex.see_block"

// statusPayload is program_status's result and program_status_changed's
// payload: the records, and seen_at once a client saw some.
func statusPayload(info *rex.SessionInfo) map[string]any {
	out := map[string]any{"records": statusRecords(info)}
	if !info.StatusSeen.IsZero() {
		out["seen_at"] = connectedAtString(info.StatusSeen)
	}
	return out
}

// seenAtSchema is seen_at's schema, beside program_status's records.
var seenAtSchema = map[string]any{
	"type": "string", "format": "date-time",
	"description": "GoRex's own: when the latest done or error record a client saw was updated (gorex.see_block). A done or error record whose updated_at is not after it was seen. Left out until a client saw some.",
}

// withSeenAt adds seen_at to program_status's result in a help document,
// whose schema Rex closes to other properties.
func withSeenAt(doc map[string]json.RawMessage) {
	var blocks []map[string]any
	if json.Unmarshal(doc["block_types"], &blocks) != nil {
		return
	}
	for _, b := range blocks {
		methods, _ := b["methods"].([]any)
		for _, raw := range methods {
			m, _ := raw.(map[string]any)
			if b["creator"] != creatorName || m == nil || m["name"] != "program_status" {
				continue
			}
			out, _ := m["output_schema"].(map[string]any)
			if props, _ := out["properties"].(map[string]any); props != nil {
				props["seen_at"] = seenAtSchema
			}
		}
	}
	doc["block_types"], _ = json.Marshal(blocks)
}

// methodGorexSeeBlock tells the server that the user saw the block's
// status records updated till seen_at. updated_at and seen_at have whole
// seconds, so a time of a whole second covers that second.
func methodGorexSeeBlock(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityStatusSeen) {
		return nil, errNotSupported("the GoRex server does not keep what was seen; update GoRex")
	}
	if aerr := a.only("session_id", "block_id", "seen_at"); aerr != nil {
		return nil, aerr
	}
	s, aerr := c.attachedArg(a)
	if aerr != nil {
		return nil, aerr
	}
	id, aerr := a.reqStr("block_id")
	if aerr != nil {
		return nil, aerr
	}
	b := s.findBlock(id)
	if b == nil {
		return nil, errNotFound("no block %s in %s", id, s.id)
	}
	raw, aerr := a.reqStr("seen_at")
	if aerr != nil {
		return nil, aerr
	}
	at, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, errBadRequest("seen_at is not an RFC 3339 time: %v", err)
	}
	if at.Nanosecond() == 0 {
		at = at.Add(time.Second - time.Nanosecond)
	}
	if err := c.g.opt.Client.SeeStatus(b.sid, at); err != nil {
		return nil, errNotFound("block %s has no session", b.id)
	}
	return struct{}{}, nil
}

// seeBlockEntry is gorex.see_block's entry of the help document, in the
// shape of Rex's own.
var seeBlockEntry = map[string]any{
	"name":        "gorex.see_block",
	"description": "Tell that the user saw a block's program status, so that every client stops showing its done and error records as new. GoRex's own method: use it only when the welcome's features list gorex.see_block.",
	"invoke":      map[string]any{"method": "gorex.see_block"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "The session the block is in."},
			"block_id":   map[string]any{"type": "string", "description": "The block the user saw."},
			"seen_at": map[string]any{
				"type": "string", "format": "date-time",
				"description": "The updated_at of the latest done or error record the user saw. A time of a whole second covers that second.",
			},
		},
		"required":             []any{"session_id", "block_id", "seen_at"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"session_id\": \"session:<uuid>\",\n  \"block_id\": \"block:<uuid>\",\n  \"seen_at\": \"2026-11-13T09:30:00Z\"\n}",
	"response_example": "{}",
	"validation_notes": []any{
		"The block's seen_at only moves forward, and never past its latest done or error record: a call with an earlier time changes nothing.",
		"com.superlogical.terminal.program_status and program_status_changed carry seen_at beside the records once a client saw some. A done or error record whose updated_at is not after seen_at was seen.",
		"A change of seen_at sends program_status_changed.",
		"The connection must be attached to session_id, or hear of every session's events, or the call fails with conflict. An unknown block fails with not_found.",
		"A server that does not keep what was seen, whose welcome doesn't list gorex.see_block, fails with not_supported.",
	},
}

// llmsSeeBlock documents gorex.see_block, after GoRex's other methods.
var llmsSeeBlock = []byte(`
### gorex.see_block

Feature: ` + "`gorex.see_block`" + `. Tell that the user saw a block's program status, as the GoRex desktop's pane
does when it shows, so that every client stops showing its done and error records as new.

Arguments:

- ` + "`session_id`" + ` (string, required): The session the block is in.
- ` + "`block_id`" + ` (string, required): The block the user saw.
- ` + "`seen_at`" + ` (string, required): The ` + "`updated_at`" + ` of the latest done or error record the user saw. A time of a whole
  second covers that second.

The result is an empty object.

Notes:

- The block's seen time only moves forward, and never past its latest done or error record: a call with an earlier
  time changes nothing.
- ` + "`com.superlogical.terminal.program_status`" + `'s result and ` + "`program_status_changed`" + `'s payload carry ` + "`seen_at`" + `
  beside ` + "`records`" + ` once a client saw some. A done or error record whose ` + "`updated_at`" + ` is not after
  ` + "`seen_at`" + ` was seen. A change of ` + "`seen_at`" + ` sends ` + "`program_status_changed`" + `.
- The connection must be attached to ` + "`session_id`" + `, or hear of every session's events, or the call fails
  with ` + "`conflict`" + `. An unknown block fails with ` + "`not_found`" + `. A server that doesn't keep what was seen
  fails with ` + "`not_supported`" + `.

Example arguments:

` + "```json" + `
{
  "session_id": "session:<uuid>",
  "block_id": "block:<uuid>",
  "seen_at": "2026-11-13T09:30:00Z"
}
` + "```" + `
`)
