package rexapi

import (
	"gorex/internal/rex"
)

// gorex.dir.list lists the directories in a directory of this machine,
// for a folder picker (docs/projects.md): the GoRex server's "dir.list"
// (rex.CapabilityDirList). It is allowed to every caller that may call
// session.create, as a caller that can start a shell can read the
// directory tree already: no transport limits it, as server.stop and
// server.tailscale.* are, and a stopping gateway refuses it as it does
// session.create. The welcome lists featureDirList when the server lists
// directories.

// featureDirList is the welcome's feature of a gateway whose server lists
// directories (gorex.dir.list).
const featureDirList = "gorex.dir_list"

// dirListResult is gorex.dir.list's result.
type dirListResult struct {
	Path   string   `json:"path"`
	Parent string   `json:"parent"`
	Home   string   `json:"home"`
	Dirs   []string `json:"dirs"`
	More   bool     `json:"more"`
	Code   string   `json:"code,omitempty"`
}

// methodGorexDirList lists the directories in path, the home directory
// when it is left out.
func methodGorexDirList(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityDirList) {
		return nil, errNotSupported("the GoRex server does not list directories; update GoRex")
	}
	if aerr := a.only("path", "hidden"); aerr != nil {
		return nil, aerr
	}
	path, _, aerr := a.str("path")
	if aerr != nil {
		return nil, aerr
	}
	hidden, _, aerr := a.boolean("hidden")
	if aerr != nil {
		return nil, aerr
	}
	r, err := c.g.opt.Client.DirList(path, hidden)
	if err != nil {
		return nil, errInternal("listing %s: %v", path, err)
	}
	return dirListResult{Path: r.Path, Parent: r.Parent, Home: r.Home, Dirs: r.Dirs, More: r.More, Code: r.Code}, nil
}

// dirListEntry is gorex.dir.list's entry of the help document, in the
// shape of Rex's own.
var dirListEntry = map[string]any{
	"name":        "gorex.dir.list",
	"description": "List the directories in a directory of this machine, for a folder picker: names only, no files. GoRex's own method: use it only when the welcome's features list gorex.dir_list.",
	"invoke":      map[string]any{"method": "gorex.dir.list"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "The directory: absolute, '~' or '~/…' of the home directory, or relative to it; left out or empty for the home directory. '/' is the top of the machine; on Windows it lists the drives, as 'C:\\'."},
			"hidden": map[string]any{"type": "boolean", "description": "List hidden directories too: a name that starts with a dot, and on Windows the hidden attribute. Default false."},
		},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "The directory listed: absolute, cleaned, its symlinks resolved; '/' for Windows' drives."},
			"parent": map[string]any{"type": "string", "description": "The directory above path, empty at the top. Above a Windows drive's root it is '/'."},
			"home":   map[string]any{"type": "string", "description": "The home directory, its symlinks resolved."},
			"dirs":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The names of the directories in path, symlinks to one among them, sorted without regard to case, at most 2000. At Windows' drives, each is a drive's root, a path of its own."},
			"more":   map[string]any{"type": "boolean", "description": "The directory holds more than 2000 directories: dirs are the first 2000."},
			"code":   map[string]any{"type": "string", "enum": []any{rex.DirNotFound, rex.DirDenied}, "description": "Left out when listed. not_found: no directory at path. denied: this machine's user may not read it. path, parent and home are still set."},
		},
		"required":             []any{"path", "parent", "home", "dirs", "more"},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"path\": \"~/src\"\n}",
	"response_example": "{\n  \"path\": \"/home/user/src\",\n  \"parent\": \"/home/user\",\n  \"home\": \"/home/user\",\n  \"dirs\": [\"api\", \"web\"],\n  \"more\": false\n}",
	"validation_notes": []any{
		"A child's path is path and its name joined with '/' (the server cleans it, '\\' on Windows); at Windows' drives ('/'), a name is the path.",
		"A missing or unreadable directory is not an error: the result carries code not_found or denied.",
		"Allowed to every caller that may call session.create.",
		"A server that does not list directories, whose welcome doesn't list gorex.dir_list, fails with not_supported.",
	},
}

// llmsDirList documents gorex.dir.list, after GoRex's other methods.
var llmsDirList = []byte(`
### gorex.dir.list

Feature: ` + "`gorex.dir_list`" + `. List the directories in a directory of this machine, for a folder picker: names
only, no files.

Arguments:

- ` + "`path`" + ` (string, optional): The directory: absolute, ` + "`~`" + ` or ` + "`~/…`" + ` of the home directory, or relative to
  it; left out or empty for the home directory. ` + "`/`" + ` is the top of the machine; on Windows it lists the drives.
- ` + "`hidden`" + ` (boolean, optional): List hidden directories too: a name that starts with a dot, and on Windows
  the hidden attribute. Default false.

The result has ` + "`path`" + ` (absolute, cleaned, symlinks resolved), ` + "`parent`" + ` (empty at the top; ` + "`/`" + `
above a Windows drive's root), ` + "`home`" + `, ` + "`dirs`" + ` (names, sorted without regard to case, at most 2000),
` + "`more`" + ` (true when cut at 2000), and ` + "`code`" + ` when it could not list: ` + "`not_found`" + ` or ` + "`denied`" + `,
with path, parent and home still set.

Notes:

- A child's path is ` + "`path`" + ` and its name joined with ` + "`/`" + `; the server cleans it. At Windows' drives,
  each name is a drive's root, as ` + "`C:\\`" + `, a path of its own.
- Allowed to every caller that may call ` + "`session.create`" + `. A server that doesn't list directories fails
  with ` + "`not_supported`" + `.

Example arguments:

` + "```json" + `
{
  "path": "~/src"
}
` + "```" + `
`)
