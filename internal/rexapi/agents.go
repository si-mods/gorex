package rexapi

import "gorex/internal/rex"

// gorex.agents.list lists the known agent CLIs installed on this machine
// (docs/MOD-010-agent-launchers.md): the GoRex server's "agents.list"
// (rex.CapabilityAgents). Allowed to the same callers as gorex.dir.list
// and session.create. The welcome lists featureAgents when the server
// supports agent detection.

// featureAgents is the welcome's feature of a gateway whose server detects
// installed agent CLIs (gorex.agents.list).
const featureAgents = "gorex.agents"

// agentsListResult is gorex.agents.list's result.
type agentsListResult struct {
	Agents []rex.AgentInfo `json:"agents"`
}

// methodGorexAgentsList lists the known agent CLIs installed on this host.
func methodGorexAgentsList(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityAgents) {
		return nil, errNotSupported("the GoRex server does not list agent CLIs; update GoRex")
	}
	if aerr := a.only(); aerr != nil {
		return nil, aerr
	}
	r, err := c.g.opt.Client.AgentsList()
	if err != nil {
		return nil, errInternal("listing agents: %v", err)
	}
	agents := r.Agents
	if agents == nil {
		agents = []rex.AgentInfo{}
	}
	return agentsListResult{Agents: agents}, nil
}

// agentsListEntry is gorex.agents.list's entry of the help document.
var agentsListEntry = map[string]any{
	"name":        "gorex.agents.list",
	"description": "List the known agent CLIs installed on this machine (claude, codex, agy, omp, devin), with the command to launch each directly. GoRex's own method: use it only when the welcome's features list gorex.agents.",
	"invoke":      map[string]any{"method": "gorex.agents.list"},
	"input_schema": map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"agents": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":      map[string]any{"type": "string", "description": "Canonical program key matching GoRex's brand tiles."},
						"name":    map[string]any{"type": "string", "description": "Human-readable display name."},
						"command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Argv to pass as block options.command so exiting the agent closes the tab."},
					},
					"required":             []any{"id", "name", "command"},
					"additionalProperties": false,
				},
				"description": "Installed known agent CLIs in display order.",
			},
		},
		"required":             []any{"agents"},
		"additionalProperties": false,
	},
	"request_example":  "{}",
	"response_example": "{\n  \"agents\": [\n    {\"id\": \"claude\", \"name\": \"Claude Code\", \"command\": [\"/bin/zsh\", \"-lic\", \"exec '/Users/alice/.local/bin/claude'\"]}\n  ]\n}",
	"validation_notes": []any{
		"Allowed to the same callers as gorex.dir.list and session.create.",
		"A server whose welcome does not list gorex.agents fails with not_supported.",
	},
}

// llmsAgents documents gorex.agents.list for /llms.txt.
var llmsAgents = []byte(`
### gorex.agents.list

Feature: ` + "`gorex.agents`" + `. List the known agent CLIs installed on this machine (` + "`claude`" + `, ` + "`codex`" + `, ` + "`agy`" + `, ` + "`omp`" + `, ` + "`devin`" + `).

Arguments: none.

The result has ` + "`agents`" + `, an array of objects with ` + "`id`" + `, ` + "`name`" + `, and ` + "`command`" + ` (argv for ` + "`session.new_window`" + ` ` + "`options.command`" + `).
`)
