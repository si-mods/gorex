package rexapi

import (
	"gorex/internal/rex"
)

// gorex.dir.mkdir makes a directory, with its parents: the folder New
// Project's "Create & Add" names (docs/MOD-002-projects.md): the GoRex
// server's "dir.mkdir" (rex.CapabilityDirMkdir). It is allowed to the
// callers of gorex.dir.list — those of session.create, which may start a
// shell that makes it anyway. The welcome lists featureDirMkdir when the
// server makes directories.

// featureDirMkdir is the welcome's feature of a gateway whose server
// makes directories (gorex.dir.mkdir).
const featureDirMkdir = "gorex.dir_mkdir"

// methodGorexDirMkdir makes the directory path names, with its parents,
// as the sessions' user.
func methodGorexDirMkdir(c *control, a args) (any, *apiError) {
	if !c.g.hello.Can(rex.CapabilityDirMkdir) {
		return nil, errNotSupported("the GoRex server does not make directories; update GoRex")
	}
	if aerr := a.only("path"); aerr != nil {
		return nil, aerr
	}
	path, aerr := a.reqStr("path")
	if aerr != nil {
		return nil, aerr
	}
	if path == "" {
		return nil, errBadRequest("path is required")
	}
	if err := c.g.opt.Client.DirMkdir(path); err != nil {
		return nil, errInternal("making %s: %v", path, err)
	}
	return map[string]any{}, nil
}

// dirMkdirEntry is gorex.dir.mkdir's entry of the help document, in the
// shape of Rex's own.
var dirMkdirEntry = map[string]any{
	"name":        "gorex.dir.mkdir",
	"description": "Make a directory, with its parents: the folder a folder picker's Create & Add names. GoRex's own method: use it only when the welcome's features list gorex.dir_mkdir.",
	"invoke":      map[string]any{"method": "gorex.dir.mkdir"},
	"input_schema": map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "The directory to make: absolute, '~' or '~/…' of the home directory, or relative to it. The drives' top, a device or a pipe path is refused."},
		},
		"additionalProperties": false,
	},
	"output_schema": map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": false,
	},
	"request_example":  "{\n  \"path\": \"~/src/new\"\n}",
	"response_example": "{}",
	"validation_notes": []any{
		"The folder and any parents are made as the sessions' user, mode 0755; a folder already there is a success.",
		"Allowed to the callers that may list directories (gorex.dir.list): those of session.create.",
		"A server that does not make directories, whose welcome doesn't list gorex.dir_mkdir, fails with not_supported.",
	},
}

// llmsDirMkdir documents gorex.dir.mkdir, after GoRex's other methods.
var llmsDirMkdir = []byte(`
### gorex.dir.mkdir

Feature: ` + "`gorex.dir_mkdir`" + `. Make a directory, with its parents: the folder a folder picker's Create & Add
names.

Arguments:

- ` + "`path`" + ` (string): The directory to make: absolute, ` + "`~`" + ` or ` + "`~/…`" + ` of the home directory, or
  relative to it. The drives' top, a device or a pipe path is refused.

The folder and any parents are made as the sessions' user, mode ` + "`0755`" + `; a folder already there is a success.

Notes:

- Allowed to the callers that may list directories (` + "`gorex.dir.list`" + `): those of ` + "`session.create`" + `.
  A server that doesn't make directories fails with ` + "`not_supported`" + `.

Example arguments:

` + "```json" + `
{
  "path": "~/src/new"
}
` + "```" + `
`)
