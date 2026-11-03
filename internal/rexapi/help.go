package rexapi

import (
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
	return map[string]any{"methods": methods, "events": blockEventSchemas}
}

// terminalCreatorJSON is the terminal creator exactly as a Rex server
// lists it (rex 0.0.1793289060), with its host_id left empty: Rex's apps
// decode block_creator.list strictly, and close the connection on a
// creator that leaves out what theirs has.
//
//go:embed testdata/rex-terminal-creator.json
var terminalCreatorJSON []byte

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
