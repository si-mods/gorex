---
name: MOD-010
description: "Each GoRex host detects installed agent CLIs and clients offer them as quick launchers on the New Tab row under each project. Use when changing agent CLI detection, gorex.agents.list, or project launcher rows."
metadata:
  created: "2026-11-22"
  updated: "2026-11-22"
---

# MOD-010: Agent CLI launchers under each project

Status: accepted.

This doc adds per-host detection of installed agent CLIs and extends the `New Tab` row under each project with quick launchers for them across iOS, desktop, and web. It extends [`MOD-002`](MOD-002-projects.md).

## Problem

Opening an agent CLI in a project takes two steps. First, the user taps `New Tab` under the project to start a shell in the project folder. Second, the user types the agent command such as `claude` or `codex`. On iOS, Rex Go minimizes soft-keyboard use, so typing a launch command on every new tab adds friction. The project list (`ios/GoRex/Views/HostSection.swift`, `projects_sidebar.go`, `web/src/projectsidebar.ts`) already knows each project folder and already carries brand icons for the supported agent CLIs (`programs.go`, `ios/Shared/Program.swift`, `web/src/programs.ts`).

## Goal

- Each GoRex server detects which known agent CLIs exist on its host: `claude`, `codex`, `agy`, `omp`, and `devin`.
- Clients fetch the installed agent CLIs per host and extend the `New Tab` row under each open project: the row keeps its plain-shell affordance on the left, and gains a right-side cluster of the project's pinned agent glyphs plus a `▾` button that opens a menu of every installed agent CLI.
- Tapping a pinned agent glyph or an agent's menu item opens a new tab in the project folder running that agent CLI directly. Exiting the agent CLI ends the session and closes the tab.
- Pinned agent glyphs are monochrome brand marks at the same visual weight as the row's `+` and `▾`; full brand tiles stay reserved for running tabs so launchers never outshine live sessions.
- Each project can configure which installed agent CLIs are pinned in its quick-launcher row (`Workspace.Launchers`), synced through the host server across desktop, iOS, and web.

## Non-goals

- Custom arbitrary shell commands or custom CLI arguments per launcher.
- Keeping a shell open after the agent CLI exits.
- Adding new brand SVGs beyond the agent CLIs already in `programs.go`, `ios/Shared/Program.swift`, and `web/src/programs.ts`.

## Design

### Behavior

1. When a client connects to a host that advertises agent detection, the client fetches the host's installed agent CLIs once per connection.
2. Under each expanded project (a workspace with a folder), if the host has one or more installed agent CLIs, the `New Tab` row gains a right-side cluster:
   - Up to the first three of the project's pinned agent CLIs, each a monochrome brand glyph button that opens a tab running that agent CLI.
   - A `▾` (chevron) button that opens a menu listing every installed agent CLI with its brand tile and name and a check on the pinned ones, plus the pin configuration (the same controls as the project menu's `Quick Launchers` submenu).
   - With no pinned agents the cluster is just the `▾`; with no installed agents at all the row is plain `New Tab`.
   - On the desktop the row exists only under the shown project's tabs — the desktop sidebar has no `New Tab` row of its own, so it appears only where `New Tab` has no other home; the other projects keep their header `+`.
3. Tapping a pinned glyph or a menu item verifies the project folder (as `New Tab` does) and opens a new tab in that folder running the agent CLI.
4. When the agent CLI exits, its PTY process ends and GoRex closes the tab (`internal/rex/layout_life.go`, `watchExit`).
5. Under `No Project`, on offline hosts, on hosts with zero detected agent CLIs, or on older servers without the capability, the row stays `New Tab`.

### Model

Each detected agent is an `AgentInfo` record:

| Field | Type | Meaning |
| --- | --- | --- |
| `id` | string | Canonical program key matching `programs.go` (`claude`, `codex`, `agy`, `omp`, `devin`) |
| `name` | string | Human-readable display name (`Claude Code`, `Codex`, `Antigravity`, `omp`, `Devin`) |
| `command` | `[]string` | Exact argv to pass to `tab.create` or `session.new_window` |

Detection runs on the host server:

- **Unix (macOS and Linux)**: GoRex builds the search `PATH` by combining the session user's login interactive shell `PATH` (probed with a 2-second timeout and cached for 60 seconds), the server process `PATH`, and common user binary directories (`~/.local/bin`, `$BUN_INSTALL/bin`, `~/.bun/bin`, `~/.npm-global/bin`, `~/.volta/bin`, `~/.local/share/pnpm`, mise and asdf shims, `~/.cargo/bin`, `/opt/homebrew/bin`, `/usr/local/bin`). For each known agent `id`, GoRex checks for a regular executable file in that `PATH`. When the user's shell is a known POSIX or fish shell (`zsh`, `bash`, `sh`, `fish`, `ksh`, `dash`), `command` is `[shell, "-lic", "exec '<resolvedPath>'"]` so the agent inherits the user's login environment and replaces the shell process. For other shells, `command` is `[resolvedPath]`.
- **Windows**: GoRex resolves each known agent `id` with `exec.LookPath`, then falls back to the Windows registry `PATH` and common user tool directories (`%APPDATA%\npm`, `~/.local/bin`, `$BUN_INSTALL\bin`, `~/.bun/bin`, `%LOCALAPPDATA%\Volta\bin`, `%LOCALAPPDATA%\pnpm`, mise shims), accepting only `.exe` and `.cmd` files. `command` is `[resolvedPath]` for an `.exe`, and `[comspec, "/c", resolvedPath]` for a `.cmd`, since CreateProcess cannot run a batch file (npm installs CLIs as `.cmd` shims on Windows).

### Code

- **Session server (`internal/rex`)**:
  - `internal/rex/proto.go`: add `CapabilityAgents = "agents.list"`, `AgentInfo`, and `Response.Agents`.
  - `internal/rex/agents.go`, `internal/rex/agents_unix.go`, `internal/rex/agents_windows.go`: detect installed known agent CLIs with short caching and login-shell `PATH` probing on Unix.
  - `internal/rex/server_ops.go`, `internal/rex/client.go`, `internal/rex/remote.go`: handle `"agents.list"` locally and for authorized remote clients alongside `"dir.list"`.
- **Rex API (`internal/rexapi`)**:
  - `internal/rexapi/agents.go`: add `featureAgents = "gorex.agents"`, `methodGorexAgentsList` (`gorex.agents.list`), `agentsListEntry` for help, and `llmsAgents` for `/llms.txt`.
  - `internal/rexapi/upload.go`, `internal/rexapi/methods.go`, `internal/rexapi/help.go`: advertise `gorex.agents` when `rex.CapabilityAgents` is present, register `gorex.agents.list`, and expose its docs.
- **iOS (`ios/GoRex`)**:
  - `ios/GoRex/Rex/Host.swift`: add `Host.Agent`, `Host.agentsFeature = "gorex.agents"`, fetch `gorex.agents.list` on connect, and extend `newWindow(in:cwd:command:)` and `shell(cwd:command:)`.
  - `ios/GoRex/Rex/Demo.swift`: return demo agents (`claude`, `codex`, `agy`) in Demo Mode.
  - `ios/GoRex/Views/HostSection.swift`: render the `New Tab` row's pinned-glyph cluster and `▾` menu for projects when `!host.agents.isEmpty`, and pass `command` through `newProjectTab(_:command:)`.
- **Desktop (root package)**:
  - `hostconn.go`, `app.go`: fetch and cache `[]rex.AgentInfo` for the local server and each remote `hostConn`.
  - `state.go`, `projects_sidebar.go`: add `newTabInWithCommand` / `newTabOnWithCommand` and render the `New Tab` row's pinned-glyph cluster and `▾` menu under open projects when agents are available.
- **Web (`web/src`)**:
  - `web/src/hostconn.ts`: add `agentsFeature = "gorex.agents"`, `Agent` interface, fetch `gorex.agents.list` on connect, and accept optional `command` in `newWindow`.
  - `web/src/projectops.ts`, `web/src/projectsidebar.ts`, `web/index.html`: pass optional `command` in `newTab`, render the `New Tab` row's pinned-glyph cluster and `▾` menu for projects when `c.agents.length > 0`, and style the cluster.

## Alternatives

- **Stay in a shell after exit (`claude; exec zsh -l`)**: rejected by user decision. Exiting the agent CLI should close the tab directly.
- **Icon with text label per agent**: rejected by user decision. Pure icon buttons keep the launcher row compact and fit on narrow phone screens without wrapping.
- **A horizontal bar of full brand tiles instead of `New Tab`** (implemented first, then revised): rejected by user decision. Saturated tiles made the launcher row outshine the running tabs above it, which the tiles exist to represent, and the bar wrapped or crowded once a host had many agent CLIs. The `New Tab` row with monochrome pinned glyphs plus a `▾` menu keeps one-tap access without the noise.
- **Agents only inside the `▾` menu, no pinned glyphs**: rejected by user decision. Pinned glyphs keep the one or two everyday agents at one tap.
- **Client-side hardcoded agent list without server detection**: rejected because clients would show icons for CLIs that are not installed on a given host.

## Security

`gorex.agents.list` reveals only which of five fixed known agent CLI binaries exist in the session user's `PATH`. It accepts no arbitrary paths or commands and is restricted to the same authorized callers as `gorex.dir.list` and `session.create`.

## Migration

The feature is additive. Older servers do not advertise `agents.list` or `gorex.agents`, so newer clients leave `agents` empty and render the existing `New Tab` row. Older clients ignore the new capability and never call `gorex.agents.list`.

## Tests

- `internal/rex/agents_test.go`: unit tests for detection on fake `PATH` directories, non-executable files, missing binaries, and generated launch commands.
- `internal/rexapi/agents_test.go`: unit tests for `gorex.agents.list`, welcome feature gating, help schema, `/llms.txt`, and `not_supported` on servers without the capability.
- Desktop `projectui_test.go`: `ui.NewTester` test verifying the launcher row renders and opens a tab with the agent command, plus real-window screenshot.
- iOS `ios/GoRexTests/`: unit tests for `Host.Agent` parsing and `newWindow` command payload, plus iOS Simulator screenshot of the session list with agent launcher icons.
- Web `web/src/`: `npm test` and typecheck (`npm run check`).
