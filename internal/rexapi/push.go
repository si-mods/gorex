package rexapi

import (
	"encoding/base64"

	"gorex/internal/rex"
)

// gorex.push.register binds the control connection to the caller's
// device key, for the host to push Live Activity updates of a block
// through the relay of docs/NET-003-push-relay.md (internal/push); the
// result carries the host's push key and the relay's URL, which the
// phone binds its activity's push tokens to.
// gorex.push.unregister drops the binding, as when the terminal closes
// or pairing ends. The connection's end also sends one last update,
// connected false, and drops it (dropControl). The welcome lists
// featurePush when the server pushes.

// featurePush is the welcome's feature of a gateway that pushes Live
// Activity updates (gorex.push.register, gorex.push.unregister).
const featurePush = "gorex.push"

// methodGorexPushRegister binds the connection to a device key, with or
// without the terminal the pushes follow.
func methodGorexPushRegister(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityPush) {
		return nil, errNotSupported("the GoRex server does not push Live Activities; update GoRex")
	}
	if aerr := a.only("device", "session_id", "block_id", "live_activities"); aerr != nil {
		return nil, aerr
	}
	device, aerr := a.reqStr("device")
	if aerr != nil {
		return nil, aerr
	}
	if len(device) > 200 {
		return nil, errBadRequest("device is longer than 200 characters")
	}
	if _, err := base64.StdEncoding.DecodeString(device); err != nil {
		return nil, errBadRequest("device is not a base64 public key")
	}
	sid, hasSession, aerr := a.str("session_id")
	if aerr != nil {
		return nil, aerr
	}
	bid, hasBlock, aerr := a.str("block_id")
	if aerr != nil {
		return nil, aerr
	}
	if hasSession != hasBlock {
		return nil, errBadRequest("session_id and block_id come together or not at all")
	}
	if _, _, aerr := a.boolean("live_activities"); aerr != nil {
		return nil, aerr
	}
	if hasSession {
		s, aerr := c.sessionArg(a)
		if aerr != nil {
			return nil, aerr
		}
		if s.findBlock(bid) == nil {
			return nil, errNotFound("no block %s in %s", bid, s.id)
		}
	}
	host, err := c.g.push.Register(c, device, sid, bid)
	if err != nil {
		return nil, errInternal("%v", err)
	}
	return map[string]any{"host": host, "relay": c.g.push.Relay()}, nil
}

// methodGorexPushUnregister drops the connection's binding.
func methodGorexPushUnregister(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityPush) {
		return nil, errNotSupported("the GoRex server does not push Live Activities; update GoRex")
	}
	if aerr := a.reject(); aerr != nil {
		return nil, aerr
	}
	c.g.push.Unregister(c)
	return struct{}{}, nil
}

// pushRegisterEntry is gorex.push.register's entry of the help document,
// in the shape of Rex's own.
var pushRegisterEntry = map[string]any{
	"name":        "gorex.push.register",
	"description": "Register the connection's device key for pushes of a terminal's Live Activity updates through the push relay, and learn the host's push key and the relay's URL. GoRex's own method: use it only when the welcome's features list gorex.push.",
	"invoke":      map[string]any{"method": "gorex.push.register"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"device":          map[string]any{"type": "string", "description": "The phone's device key: its public P-256 key in base64, at most 200 characters. The pushes are addressed to it, and it signs the token bindings at the relay."},
			"session_id":      map[string]any{"type": "string", "description": "The session of the terminal on screen, with block_id; both, or neither."},
			"block_id":        map[string]any{"type": "string", "description": "The block of the terminal on screen, with session_id; both, or neither."},
			"live_activities": map[string]any{"type": "boolean", "description": "Whether the phone has Live Activities on."},
		},
		"required":             []any{"device"},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"host":  map[string]any{"type": "string", "description": "The host's push key: its public P-256 key, PKIX DER in base64. The phone binds it to its activity tokens at the relay."},
			"relay": map[string]any{"type": "string", "description": "The push relay's URL."},
		},
		"required":             []any{"host", "relay"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"device\": \"BJrd…\",\n  \"session_id\": \"session:<uuid>\",\n  \"block_id\": \"block:<uuid>\",\n  \"live_activities\": true\n}",
	"response_example": "{\n  \"host\": \"BEvm…\",\n  \"relay\": \"https://relay.gorex.uk\"\n}",
	"validation_notes": []any{
		"Call it on every connect and when the terminal on screen changes; the new call moves the binding to the new target. The connection's end sends one last update, connected false, and drops the binding; gorex.push.unregister drops it without one.",
		"Each push goes to the relay signed with the host key and addressed to the device key; the host never sees a push token.",
		"A server that does not push, whose welcome doesn't list gorex.push, fails with not_supported.",
	},
}

// pushUnregisterEntry is gorex.push.unregister's entry of the help
// document, in the shape of Rex's own.
var pushUnregisterEntry = map[string]any{
	"name":        "gorex.push.unregister",
	"description": "Drop the connection's push registration, as when the terminal closes or pairing ends. GoRex's own method: use it only when the welcome's features list gorex.push.",
	"invoke":      map[string]any{"method": "gorex.push.unregister"},
	"input_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
	},
	"request_example":  "{}",
	"response_example": "{}",
	"validation_notes": []any{
		"The result is an empty object.",
		"A server that does not push, whose welcome doesn't list gorex.push, fails with not_supported.",
	},
}

// llmsPush documents gorex.push.register and gorex.push.unregister,
// after GoRex's other methods.
var llmsPush = []byte(`
### gorex.push.register

Feature: ` + "`gorex.push`" + `. Register the connection's device key for pushes of a terminal's Live Activity
updates through the push relay (docs/NET-003-push-relay.md), and learn the host's push key and the relay's URL.

Arguments:

- ` + "`device`" + ` (string, required): The phone's device key: its public P-256 key in base64, at most 200 characters.
- ` + "`session_id`" + `, ` + "`block_id`" + ` (strings): The terminal on screen. Both, or neither; an unknown session fails with
  ` + "`session_not_found`" + `, an unknown block with ` + "`not_found`" + `.
- ` + "`live_activities`" + ` (boolean, optional): Whether the phone has Live Activities on.

The result has ` + "`host`" + `, the host's public P-256 key (PKIX DER in base64) to bind activity tokens to at the
relay, and ` + "`relay`" + `, the relay's URL.

Notes:

- Call it on every connect and when the terminal on screen changes; the new call moves the binding. The
  connection's end sends one last update, ` + "`connected: false`" + `, and drops the binding.
- Each push goes to the relay signed with the host key and addressed to the device key; the host never sees a
  push token.
- A server that doesn't push fails with ` + "`not_supported`" + `.

Example arguments:

` + "```json" + `
{
  "device": "BJrd…",
  "session_id": "session:<uuid>",
  "block_id": "block:<uuid>",
  "live_activities": true
}
` + "```" + `

### gorex.push.unregister

Feature: ` + "`gorex.push`" + `. Drop the connection's push registration, as when the terminal closes or pairing
ends. Takes no arguments; the result is an empty object.
`)
