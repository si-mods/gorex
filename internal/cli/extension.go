package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// Pi and omp (oh-my-pi, a fork of Pi) have no hooks of a settings file:
// their session-start hook is an extension, a TypeScript module each loads
// from extensions/ in its agent directory. The same file serves both: it
// tells which runs it from the executable.

// extensionMarker marks the extension this command owns; a file without it
// is the user's and is not replaced.
const extensionMarker = "// gorex extension"

// extensionSource is the extension: outside GoRex it does nothing; inside,
// as each session starts (a new one, or one resumed or forked), it runs
// gorex agent-hook without holding the start up, and gives the model what
// it printed in its system prompt from the first turn on. It prints
// nothing, which would break the agent's screen.
func extensionSource() string {
	exe, err := os.Executable()
	if err != nil {
		exe = "gorex"
	}
	quoted, _ := json.Marshal(exe)
	return extensionMarker + `: GoRex's session-start hook, installed by gorex skill setup
// and removed by gorex skill remove. It tells GoRex which conversation runs
// in the session, so that GoRex resumes it when the session restarts.
import { execFile } from "node:child_process"
import { basename } from "node:path"

const GOREX = ` + string(quoted) + `

// agentName tells omp from Pi: the process title each sets, else omp's
// extension API, else the executable's name.
function agentName(api: any): "omp" | "pi" {
	const title = basename(String(process.title ?? "")).toLowerCase()
	if (title === "omp" || title === "pi") return title
	if (api && typeof api === "object" && "typebox" in api && "runtime" in api) return "omp"
	const names = [process.execPath, process.argv[1] ?? ""].map((p) => basename(p).toLowerCase())
	return names.some((n) => n.startsWith("omp")) ? "omp" : "pi"
}

export default function (api: any) {
	if (!process.env.GOREX_SESSION) return
	const agent = agentName(api)
	let context: Promise<string> = Promise.resolve("")
	api.on("session_start", (_event: any, ctx: any) => {
		const id = ctx?.sessionManager?.getSessionId?.()
		if (typeof id !== "string" || id === "") return
		const args = ["agent-hook", "--agent", agent, "--conversation", id, "--pid", String(process.pid)]
		context = new Promise((resolve) => {
			try {
				execFile(GOREX, args, { timeout: 3000, windowsHide: true }, (err: any, stdout: any) => {
					resolve(!err && typeof stdout === "string" ? stdout.trim() : "")
				})
			} catch {
				resolve("")
			}
		})
	})
	api.on("before_agent_start", async (event: any) => {
		const text = await context
		if (!text) return
		const prompt = event?.systemPrompt
		if (Array.isArray(prompt)) return { systemPrompt: [...prompt, text] }
		if (typeof prompt === "string") return { systemPrompt: prompt + "\n\n" + text }
	})
}
`
}

// extensionAgents are the agents whose hook is an extension.
var extensionAgents = map[string]string{"pi": ".pi", "omp": ".omp"}

// extensionPath is where agent loads GoRex's extension from: its agent
// directory, PI_CODING_AGENT_DIR (which omp reads too) or ~/.pi/agent and
// ~/.omp/agent.
func extensionPath(agent string) (string, error) {
	base := os.Getenv("PI_CODING_AGENT_DIR")
	if base == "" {
		h, err := homeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(h, extensionAgents[agent], "agent")
	}
	return filepath.Join(base, "extensions", "gorex", "index.ts"), nil
}

func readExtension(path string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(b), err
}

// extensionState tells whether agent's extension is installed.
func extensionState(agent string) (string, error) {
	path, err := extensionPath(agent)
	if err != nil {
		return "", err
	}
	old, err := readExtension(path)
	switch {
	case err != nil:
		return "conflict", err
	case old == "":
		return "missing", nil
	case !strings.HasPrefix(old, extensionMarker):
		return "conflict", errors.New("an extension named gorex already exists and is not GoRex's")
	case old != extensionSource():
		// Another build's, or an older one: setup replaces it.
		return "outdated", nil
	}
	return "installed", nil
}

// setExtension installs or removes agent's extension, refusing a file it
// does not own.
func setExtension(agent string, install, dry bool) (string, error) {
	path, err := extensionPath(agent)
	if err != nil {
		return "", err
	}
	old, err := readExtension(path)
	if err != nil {
		return "conflict", err
	}
	if old != "" && !strings.HasPrefix(old, extensionMarker) {
		return "conflict", errors.New("an extension named gorex already exists and is not GoRex's")
	}
	if !install {
		switch {
		case old == "":
			return "missing", nil
		case dry:
			return "would remove", nil
		}
		if err := os.Remove(path); err != nil {
			return "failed", err
		}
		os.Remove(filepath.Dir(path))
		return "removed", nil
	}
	src := extensionSource()
	switch {
	case old == src:
		return "current", nil
	case dry && old != "":
		return "would replace", nil
	case dry:
		return "would install", nil
	}
	dir := filepath.Dir(path)
	if st, err := os.Lstat(dir); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "conflict", errors.New("the extension directory is a symlink")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "failed", err
	}
	tmp, err := os.CreateTemp(dir, ".index.ts-*")
	if err != nil {
		return "failed", err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(src); err != nil {
		tmp.Close()
		os.Remove(name)
		return "failed", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "failed", err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		os.Remove(name)
		return "failed", err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return "failed", err
	}
	return "installed", nil
}
