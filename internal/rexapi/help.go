package rexapi

import (
	"bytes"
	_ "embed"
	"encoding/json"
)

// blockEventSchemas are the events a GoRex block sends, as a
// block_creator's api.events lists them. Rex's help document lists none of
// the terminal's events, so these are GoRex's own, with the fields
// block_event events carry.
var blockEventSchemas = map[string]any{
	"bell": map[string]any{"type": "object"},
	"title_changed": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string", "description": "The title the program set."},
		},
		"required": []string{"title"},
	},
	"program_status_changed": map[string]any{
		"type":        "object",
		"description": "The status the programs reported, as com.superlogical.terminal.program_status reports it.",
		"properties": map[string]any{
			"records": map[string]any{"type": "array"},
		},
		"required": []string{"records"},
	},
	"child_exited": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"exit_code": map[string]any{"type": "integer", "description": "The program's exit code."},
		},
		"required": []string{"exit_code"},
	},
	"pwd_changed": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pwd":   map[string]any{"type": "string", "description": "The working directory, as a file:// URL."},
			"owner": map[string]any{"type": "object", "description": "The process that reported it."},
		},
		"required": []string{"pwd"},
	},
	"process_changed": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"generation": map[string]any{"type": "integer", "description": "Counts changes of the foreground process."},
			"foreground": map[string]any{"type": "object", "description": "The program in the foreground."},
			"ancestors":  map[string]any{"type": "array", "description": "The processes above it."},
		},
		"required": []string{"generation", "foreground"},
	},
	"program_status_removed": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"ids":    map[string]any{"type": "array", "description": "The records that were removed."},
			"reason": map[string]any{"type": "string", "description": "Why they were removed."},
		},
		"required": []string{"ids"},
	},
}

// terminalBlockType is the help document's block type of the creator
// GoRex makes blocks with.
type terminalBlockType struct {
	Creator string   `json:"creator"`
	Flavors []string `json:"flavors"`
	Methods []struct {
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		ArgsSchema   json.RawMessage `json:"args_schema"`
		OutputSchema json.RawMessage `json:"output_schema"`
	} `json:"methods"`
}

// terminalBlock returns the help document's description of the terminal
// creator.
func terminalBlock() (*terminalBlockType, bool) {
	var doc struct {
		BlockTypes []terminalBlockType `json:"block_types"`
	}
	if err := json.Unmarshal(helpJSON, &doc); err != nil {
		return nil, false
	}
	for i := range doc.BlockTypes {
		if doc.BlockTypes[i].Creator == creatorName {
			return &doc.BlockTypes[i], true
		}
	}
	return nil, false
}

// blockCreatorAPI returns the methods and events every GoRex block
// provides, as block_creator.list and session.describe_block report them.
func blockCreatorAPI() map[string]any {
	methods := map[string]any{}
	if bt, ok := terminalBlock(); ok {
		for _, m := range bt.Methods {
			if _, ours := blockMethods[m.Name]; !ours {
				continue
			}
			entry := map[string]any{"description": m.Description}
			if len(m.ArgsSchema) > 0 {
				entry["input_schema"] = json.RawMessage(m.ArgsSchema)
			}
			if len(m.OutputSchema) > 0 {
				entry["output_schema"] = json.RawMessage(m.OutputSchema)
			}
			methods[m.Name] = entry
		}
	}
	// upload_begin postdates the snapshot the method list comes from;
	// describe_block reports it like block_creator.list does.
	methods["upload_begin"] = uploadBeginEntry
	return map[string]any{"methods": methods, "events": blockEventSchemas}
}

// terminalCreatorJSON is the terminal creator exactly as a Rex server
// lists it (rex 0.0.1793289060), with its host_id left empty: Rex's apps
// decode block_creator.list strictly, and close the connection on a
// creator that leaves out what theirs has.
//
//go:embed testdata/rex-terminal-creator.json
var terminalCreatorJSON []byte

// uploadBeginEntry is the method entry revision 11's terminal creator
// adds, which the revision 10 snapshot predates. Its prose and schemas
// come from Rex 0.1.0 (1861)'s own doc; the limits the schemas leave out
// are GoRex's, in upload.go.
var uploadBeginEntry = map[string]any{
	"description": "Reserve a file to upload to the terminal's machine, such as one the user dropped on the terminal, and get a ticket for the rex.data.bytes.v1 data stream that carries its bytes. Once every file of the batch has arrived, the terminal types their paths as if the user had pasted them.",
	"input_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"ref": map[string]any{
				"type":        "string",
				"description": "The upload's ref: upload: followed by a new UUID in lower-case canonical form.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "The file's name, which is its last path component only.",
			},
			"size": map[string]any{
				"type":        "integer",
				"description": "The file's exact size in bytes.",
			},
			"batch": map[string]any{
				"type":        "string",
				"description": "An ID the client chooses for a group of files whose paths are typed together.",
			},
			"index": map[string]any{
				"type":        "integer",
				"description": "The file's position in its batch, from 0.",
			},
			"count": map[string]any{
				"type":        "integer",
				"description": "How many files the batch has. Required with batch.",
			},
		},
		"required": []any{"ref", "name", "size"},
	},
	"output_schema": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"ticket": map[string]any{
				"type":        "string",
				"description": "The ticket of the rex.data.bytes.v1 stream that carries the file, as data.connect's is.",
			},
			"expires_in_ms": map[string]any{
				"type":        "integer",
				"description": "How long the ticket works, in milliseconds.",
			},
		},
		"required": []any{"ticket", "expires_in_ms"},
	},
}

// listenersSchema is revision 12's listeners member of server.status,
// which the revision 10 document predates. Its descriptions come from
// Rex 0.1.0-preview.14's schema.
var listenersSchema = map[string]any{
	"type":        "array",
	"description": "Each endpoint the server accepts connections on, primary first. Servers older than API revision 12 leave it out. Doesn't include built-in Tailscale, which the tailscale member describes.",
	"items": map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"endpoint": map[string]any{
				"type":        "string",
				"description": "The endpoint the listener was configured with, such as unix:///run/rex.sock, 127.0.0.1:7297, or tailnet://:7297.",
			},
			"addresses": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "The addresses the listener is bound to, such as a socket path or 127.0.0.1:54321. They show the port that an endpoint like 127.0.0.1:0 got. Left out when the server can't tell.",
			},
			"transport": map[string]any{
				"type":        "string",
				"description": "How the listener identifies its callers: 'unix', 'named_pipe', 'tcp', or 'tailscale'. Show a value you don't know as it is.",
			},
			"admission": map[string]any{
				"type":        "string",
				"description": "Which callers the listener lets in: 'peer_credentials', 'anonymous', 'tailscale_owner', 'tailscale_identified', or 'login_server'. Left out when the server can't tell. Show a value you don't know as it is.",
			},
			"primary": map[string]any{
				"type":        "boolean",
				"description": "True for the server's primary endpoint, where commands such as rex server status and rex server stop look for the server. Left out for the other listeners.",
			},
		},
		"required": []any{"endpoint", "transport"},
	},
}

// withListenersSchema adds listenersSchema to the help document's
// server status: server.status's result and session.bootstrap's
// server_status. A document it cannot read is returned as it is.
func withListenersSchema(raw []byte) []byte {
	var doc map[string]any
	if json.Unmarshal(raw, &doc) != nil {
		return raw
	}
	methods, _ := doc["methods"].([]any)
	for _, m := range methods {
		m, _ := m.(map[string]any)
		out, _ := m["output_schema"].(map[string]any)
		switch m["name"] {
		case "server.status":
		case "session.bootstrap":
			props, _ := out["properties"].(map[string]any)
			out, _ = props["server_status"].(map[string]any)
		default:
			continue
		}
		if props, ok := out["properties"].(map[string]any); ok {
			props["listeners"] = listenersSchema
		}
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if enc.Encode(doc) != nil {
		return raw
	}
	return b.Bytes()
}

// methodBlockCreatorList lists the creators GoRex can make blocks with:
// Rex's terminal, as Rex describes it. A method or option it lists that
// GoRex lacks fails when it is called, with the code doc.go gives.
func methodBlockCreatorList(c *control, a args) (any, *apiError) {
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	var creator map[string]any
	if err := json.Unmarshal(terminalCreatorJSON, &creator); err != nil {
		return nil, errInternal("terminal creator: %v", err)
	}
	creator["host_id"] = c.g.host
	// Revision 11's terminal creator lists upload_begin, which the
	// snapshot predates; a client that decodes the creator strictly
	// closes the connection on a method its own has and this one leaves
	// out.
	if api, ok := creator["api"].(map[string]any); ok {
		if methods, ok := api["methods"].(map[string]any); ok {
			methods["upload_begin"] = uploadBeginEntry
		}
	}
	return map[string]any{"creators": []any{creator}}, nil
}

// helpDocument returns the help document as this server serves it, limited
// to what it implements and to the methods a client at rev knows; withHTTP
// adds the http field session.help has.
func helpDocument(rev int, withHTTP bool) map[string]json.RawMessage {
	src := helpDoc()
	doc := map[string]json.RawMessage{}
	var methodsDoc []json.RawMessage
	if raw, ok := src["methods"]; ok {
		json.Unmarshal(raw, &methodsDoc)
	}
	kept := make([]json.RawMessage, 0, len(methodsDoc))
	for _, raw := range methodsDoc {
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if !implementedMethods[m.Name] || !methodAvailable(rev, m.Name) {
			continue
		}
		kept = append(kept, raw)
	}
	doc["methods"], _ = json.Marshal(kept)
	var blocks []json.RawMessage
	if raw, ok := src["block_types"]; ok {
		json.Unmarshal(raw, &blocks)
	}
	var keptBlocks []json.RawMessage
	for _, raw := range blocks {
		var b struct {
			Creator string `json:"creator"`
		}
		if json.Unmarshal(raw, &b) == nil && b.Creator == creatorName {
			keptBlocks = append(keptBlocks, raw)
		}
	}
	doc["block_types"], _ = json.Marshal(keptBlocks)
	if withHTTP {
		if raw, ok := src["http"]; ok {
			doc["http"] = raw
		}
	}
	return doc
}

// helpAllDocument is help.all's answer: every method Rex's document lists,
// not only the implemented ones, limited to the methods a client at rev
// knows.
func helpAllDocument(rev int) map[string]json.RawMessage {
	src := helpDoc()
	doc := map[string]json.RawMessage{}
	var methodsDoc []json.RawMessage
	if raw, ok := src["methods"]; ok {
		json.Unmarshal(raw, &methodsDoc)
	}
	kept := make([]json.RawMessage, 0, len(methodsDoc))
	for _, raw := range methodsDoc {
		var m struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &m) != nil || !methodAvailable(rev, m.Name) {
			continue
		}
		kept = append(kept, raw)
	}
	doc["methods"], _ = json.Marshal(kept)
	var blocks []json.RawMessage
	if raw, ok := src["block_types"]; ok {
		json.Unmarshal(raw, &blocks)
	}
	keptBlocks := make([]json.RawMessage, 0, len(blocks))
	for _, raw := range blocks {
		var b struct {
			Creator string `json:"creator"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		if since, ok := creatorSince[b.Creator]; ok && rev < since {
			continue
		}
		keptBlocks = append(keptBlocks, raw)
	}
	doc["block_types"], _ = json.Marshal(keptBlocks)
	return doc
}

// implementedMethods are the methods of the methods table, for the help
// documents: filled in init, so that the help document does not depend on
// the table it is named in.
var implementedMethods = map[string]bool{}

func init() {
	for name := range methods {
		implementedMethods[name] = true
	}
}
