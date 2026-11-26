---
name: MOD-016
description: "Cross-host agent delegation over existing peer links: host capability probing, gorex delegate, per-peer access ceilings, and an optional MCP bridge. Use when adding or changing remote agent delegation, gorex hosts --probe, gorex delegate, or peer access levels."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# MOD-016: Peer delegation for coding agents

Status: proposal.

This doc defines peer delegation so a coding agent on one GoRex host can discover peer hosts, launch a coding agent on a target host, and collect the result. It extends [`NET-002`](NET-002-host-links.md) and [`MOD-010`](MOD-010-agent-launchers.md).

## Problem

GoRex already links hosts over encrypted tailcat and Tailscale connections (`internal/rex/remote.go`, `internal/hosts/hosts.go`) and runs CLI commands on saved hosts with `gorex -S HOST` (`internal/cli/commands.go`). Each host also detects its installed coding agent CLIs through `agents.list` (`internal/rex/agents.go`).

An agent on `alice-laptop` still cannot reliably delegate a task to `build-linux` or `win-desk`:

- `gorex hosts --json` prints only `id`, `name`, and `alias`. An agent cannot see whether a peer host is online, what OS it runs, which projects are open, or which agent CLIs are installed.
- `agents.list` has no CLI subcommand. An agent must guess which CLI binary exists on the target host and how to invoke it on POSIX or Windows shells.
- `gorex run` blocks until exit and returns raw terminal screen text. Long output truncates, spinner frames pollute the text, and the caller gets no structured result or async handle.
- `RemoteConfig.Allowed` (`internal/rex/remote.go`) is binary. A paired peer either has full shell execution or no access, with no read-only ceiling for untrusted or exploratory agent delegation.
- `gorexGuide` (`internal/cli/skill.go`) mentions `-S HOST` in one line. Agents do not know how to inspect peers or delegate work across hosts.

## Goal

- An agent can inspect paired peers with one command and see reachability, OS, open projects, and installed agent CLIs.
- An agent can delegate a prompt to a peer host in synchronous or asynchronous mode and receive structured output, exit status, and session ID.
- A delegated task runs as a normal GoRex session on the target host so the user can watch the live pane or intervene from desktop, iOS, or web.
- A host owner can cap a peer's access level to `read-only` or `full` in `remote.json`, and the server enforces the cap on every remote call.
- Peer delegation reuses existing tailcat and Tailscale links with no new server, account, or transport stack.

## Non-goals

- Automatic git workspace synchronization before delegation. [`DSK-003`](DSK-003-teleport.md) already moves a pane and its git state between hosts. Combining teleport with delegation is separate work.
- Multi-hop routing from host A through host B to host C. Each host talks directly to peers in its own `hosts.json` or tailnet list.
- A built-in agent loop or LLM runner inside GoRex. GoRex delegates to the agent CLIs already installed on the target host.
- Replacing `gorex` CLI with MCP. CLI plus `gorexGuide` is the primary interface, and an optional stdio MCP bridge wraps it in a later phase.

## Design

### Behavior

#### 1. Peer discovery

An agent lists peers and their capabilities:

```sh
gorex hosts --probe --json
gorex -S build-linux agents --json
```

- `gorex hosts --probe --json` dials each saved host in parallel with a short timeout (5 seconds default, configurable via `--timeout`). For each reachable host, it collects `hello`, `agents.list`, and `getLayout` to report `online`, `os`, `machine`, `agents`, `projects`, and `access` (the caller's granted access level when reported). Unreachable hosts appear with `online: false` and an `error` string.
- `gorex hosts --json` without `--probe` keeps its current instant, offline output so existing scripts stay fast.
- `gorex -S build-linux agents --json` prints the `[]rex.AgentInfo` list from `agents.list`.

#### 2. Delegating a task

An agent delegates a prompt to a target host:

```sh
# Synchronous: waits for completion and prints structured JSON
gorex -S build-linux delegate --dir ~/repos/app --agent auto --json -- "Run the Linux test suite and summarize any failures"

# Asynchronous: starts the session and returns its handle immediately
gorex -S win-desk delegate --async --dir C:\repos\app --agent claude --json -- "Verify the Windows build"
```

- `--agent` accepts `auto` (default) or a known agent ID (`claude`, `codex`, `agy`, `omp`, `devin`). With `auto`, the server picks the first installed agent that supports headless delegation in display order (`knownAgents`).
- `--dir` sets the working directory on the target host. When omitted, the target host uses its home directory.
- `--mode` accepts `full` (default) or `read-only`. When the peer's server-side access ceiling is `read-only`, the server forces `read-only` mode regardless of the flag.
- `--timeout` bounds synchronous waits (default `10m`, `0` waits until exit).
- In synchronous mode, `delegate` creates a real session on the target host, streams no raw ANSI to stdout, waits for exit, extracts the final result, and prints one JSON object: `host`, `session`, `agent`, `mode`, `exitCode`, `result`, and `rawCapture` (fallback text when the agent has no structured output). The session stays closed on clean exit unless `--keep` is passed.
- In asynchronous mode (`--async`), `delegate` returns `{host, session, agent, mode, status: "running"}` immediately. The caller tracks it with `gorex -S HOST wait -s SESSION --exit` and reads output with `gorex -S HOST capture -s SESSION --history 65536`.
- On the target host, the delegated session appears in GoRex like any other session, with a `DelegatedFrom` label (source host name and session ID). A human user can inspect the pane live or type into it at any time.

#### 3. Access ceilings

A host owner sets a peer's access level when allowing or updating a client:

```sh
gorex remote allow --access read-only alice-laptop <KEY>
gorex remote access alice-laptop read-only
```

- `full` (default): keeps today's remote behavior.
- `read-only`: the server allows only read-only ops (`hello`, `ping`, `list`, `capture`, `clients`, `format`, `dir.list`, `agents.list`, `getLayout`, `getLayoutState`, `subscribe`) plus `delegate` with forced `read-only` mode. The server refuses `create`, `input`, `paste`, `kill`, `dir.mkdir`, and `file.put` with a clear `access denied: read-only peer` error.

#### 4. Agent skill guidance

`gorexGuide` (`internal/cli/skill.go`) adds a short "Peer hosts" section showing `gorex hosts --probe --json`, `gorex -S HOST agents --json`, and `gorex -S HOST delegate`. Agents with `gorex skill setup` installed learn peer delegation automatically after upgrading GoRex.

#### 5. Optional MCP bridge

In Phase 4, `gorex mcp` starts a thin stdio MCP server exposing four tools: `list_peers`, `delegate_task`, `wait_task`, and `read_task`. Each tool calls the same Go functions as the CLI subcommands with no extra daemon or HTTP listener.

### Model

#### Delegation metadata

A delegated session carries optional metadata on `SessionInfo`:

- `DelegatedFrom`: `string`, formatted as `<host>/<session>` (empty for normal sessions).
- `DelegatedAgent`: `string`, the agent ID that ran (`claude`, `codex`, etc.).

The session server keeps this metadata in memory and in the session journal alongside the session's `Command` and `Dir`, so a server upgrade preserves it (Invariant 1).

#### Agent headless adapters

`MOD-010` detects installed agents and builds interactive launch commands. Peer delegation extends each `knownAgent` with a headless invocation builder for `full` and `read-only` modes:

| Agent ID | Headless `full` command | Headless `read-only` command | Structured output |
| --- | --- | --- | --- |
| `claude` | `claude -p <prompt> --output-format json` | `claude -p <prompt> --output-format json --permission-mode plan` | JSON stdout (`result` field) |
| `codex` | `codex exec --json <prompt>` | `codex exec --json --sandbox read-only <prompt>` | JSONL stdout (final agent message) |
| `agy` | Fallback PTY or CLI flags once stable | Refused if no read-only flag | Screen capture |
| `omp` | Fallback PTY or CLI flags once stable | Refused if no read-only flag | Screen capture |
| `devin` | Fallback PTY or CLI flags once stable | Refused if no read-only flag | Screen capture |

Rules for read-only enforcement:

- If a caller requests `read-only` mode (or the server enforces `read-only` access) and the selected agent has no verified read-only flag, the server refuses the delegation instead of falling back to a writable mode.
- Prompt strings travel as argv elements (or via a temp file when exceeding Windows command-line limits), never interpolated into a shell string.

#### Access levels

| Level | Allowed ops in `remote.go` | `delegate` behavior |
| --- | --- | --- |
| `full` (default) | All current `remoteAllowed` ops + `delegate` | Honors requested `--mode` (`full` or `read-only`) |
| `read-only` | Read-only subset (`hello`, `ping`, `list`, `capture`, `clients`, `format`, `dir.list`, `agents.list`, `getLayout`, `getLayoutState`, `subscribe`) + `delegate` | Forces `read-only` mode; rejects `full` |

### Code

- **Session server (`internal/rex`)**:
  - `internal/rex/proto.go`: add `CapabilityDelegate = "delegate"`, `DelegateOptions`, `DelegateResult`, optional `DelegatedFrom` on `SessionInfo`, and optional `Access` on `Hello` (the caller's effective access level on this server).
  - `internal/rex/agents.go`, `internal/rex/agents_unix.go`, `internal/rex/agents_windows.go`: add `Headless` capability flag to `AgentInfo` and `buildDelegateCommand(agent, mode, prompt, home, shell)` with safe argv handling on Unix and Windows.
  - `internal/rex/delegate.go`: handle `"delegate"` op on the server, enforce peer access ceiling, create the session with `DelegatedFrom`, and parse structured JSON output when supported.
  - `internal/rex/remote.go`: add `Access string` (`json:"access,omitempty"`) to `RemoteClient`, default empty to `"full"`, and replace static `remoteAllowed(op)` with `remoteAllowedFor(op, access)`.
  - `internal/rex/client.go`: add `Client.Agents()` (if not already present) and `Client.Delegate(DelegateOptions)`.
- **CLI (`internal/cli`)**:
  - `internal/cli/commands.go`: extend `hostsCmd` with `--probe` and `--timeout`, add `agentsCmd`, add `delegateCmd`, and add `remote allow --access` / `remote access`.
  - `internal/cli/skill.go`: update `gorexGuide` with peer discovery and delegation examples.
  - `internal/cli/mcp.go` (Phase 4): stdio MCP server wrapping `hosts --probe`, `delegate`, `wait`, and `capture`.
- **Desktop / iOS / Web (optional follow-up)**:
  - Render a subtle badge on sessions where `DelegatedFrom != ""` so users can distinguish delegated panes from manual tabs.

## Alternatives

- **HTTP + OAuth + MCP peer server (t3code style)**: rejected because GoRex already has end-to-end encrypted WireGuard links (`tailcat`) and device-key allowlists (`NET-002`). Adding an HTTP/OAuth peer layer would duplicate trust, origin checks, and NAT traversal.
- **Client-side shell command construction over `gorex run`**: rejected as the sole design because the caller would need to know target OS quoting rules, login shell `PATH`, and per-agent flags, and the target server could not enforce a `read-only` ceiling.
- **Background headless process outside GoRex sessions**: rejected because running delegated agents inside normal PTY sessions lets users watch progress live, inspect scrollback, and take over from desktop or iOS with zero extra UI plumbing.

## Security

- **Transport and identity**: peer delegation uses only existing `tailcat` (WireGuard over DERP) and Tailscale peer links authenticated by device key (`internal/rex/remote.go`). No new network port or listener opens.
- **Server-side enforcement**: the target server checks `RemoteClient.Access` before executing any op. A `read-only` peer cannot bypass its ceiling by sending raw `create` or `input` requests.
- **Prompt injection boundary**: `gorexGuide` already instructs agents to treat captured output as task data, not instructions. Structured JSON extraction further isolates the final result from raw terminal control sequences.
- **No secret leakage**: `gorex hosts --probe --json` never prints host addresses, node keys, or preshared keys (`internal/hosts/hosts.go`).
- **Argument safety**: prompts pass as explicit argv slices (never shell-interpolated strings) so shell metacharacters in a prompt cannot execute arbitrary commands on Unix or Windows.

## Migration

- **Older `remote.json` files**: `RemoteClient.Access` omits empty values and defaults to `"full"` when absent. Existing allowlists continue to work unchanged.
- **Older servers**: servers without `CapabilityDelegate` do not advertise `"delegate"` in `Hello.Capabilities`. `gorex -S HOST delegate` detects the missing capability and prints a clear upgrade message. `gorex hosts --probe` skips `agents.list` on servers without `CapabilityAgents`.
- **Older clients**: newer fields on `Hello` (`Access`) and `SessionInfo` (`DelegatedFrom`) are additive JSON fields that older clients ignore.

## Tests

- **Peer probing**: unit test in `internal/cli/commands_test.go` with mock local and remote servers verifying `gorex hosts --probe --json` returns `online`, `agents`, and `projects`, and handles unreachable hosts without blocking.
- **Headless command builder**: unit tests in `internal/rex/agents_test.go` covering `claude` and `codex` on Unix and Windows, verifying argv escaping and refusal of `read-only` on agents without a read-only flag.
- **Server-side delegation and access ceiling**: unit tests in `internal/rex/delegate_test.go` and `internal/rex/remote_test.go` verifying that a `read-only` peer can run `list`, `capture`, `agents.list`, and `read-only` `delegate`, and receives `access denied` on `create`, `input`, and `full` `delegate`.
- **Backward compatibility**: unit test reading an old `remote.json` without `access` and verifying it loads as `full` (`docs/irreversible.md`).
- **Invariants touched** (`docs/invariants.md`):
  - Invariant 1 (a window comes back on its sessions): verify `DelegatedFrom` survives in-place server upgrade (`internal/rex/upgrade_test.go`).
  - Invariant 6 (a pane ends only when its session ends): verify synchronous `delegate` cleans up its session on exit and `--async` keeps the session until closed.

## Phases

1. **Phase 1 (Discovery & Skill)**: add `gorex hosts --probe --json`, `gorex -S HOST agents --json`, and update `gorexGuide` in `internal/cli/skill.go`.
2. **Phase 2 (Core Delegation)**: add `CapabilityDelegate`, `internal/rex/delegate.go`, headless agent adapters in `internal/rex/agents.go`, and `gorex -S HOST delegate`.
3. **Phase 3 (Access Ceilings)**: add `RemoteClient.Access` (`full` / `read-only`) in `remote.json`, enforce `remoteAllowedFor` in `internal/rex/remote.go`, and add `gorex remote access`.
4. **Phase 4 (Optional MCP Bridge)**: add `gorex mcp` stdio server wrapping Phases 1–3 for non-shell MCP clients.

## Open questions

1. **Saved format change (`remote.json`, per `docs/irreversible.md`)**: How should per-peer access levels be stored?
   - **Option 1 (Recommended)**: Add optional `access` (`""` / `"full"` / `"read-only"`) to `RemoteClient` in `remote.json`. Empty defaults to `"full"`, so old files and old builds stay compatible with zero migration.
   - **Option 2**: Store peer policies in a separate `remote-policy.json` file. Avoids touching `remote.json` but splits one peer's identity and permission across two files.
   - **Owner**: User decides before Phase 3 starts.

2. **Delegation execution path (wire protocol, per `docs/irreversible.md`)**: Should `delegate` be a server-side op (`CapabilityDelegate`) or a pure client-side wrapper over `agents.list` + `create`?
   - **Option 1 (Recommended)**: Additive server-side op (`CapabilityDelegate`) inside `ProtocolVersion = 3`. Allows the target server to enforce `read-only` ceilings, handle Windows/Unix argv quoting safely, and extract structured agent output cleanly.
   - **Option 2**: Pure client-side wrapper over `create`. Ships faster with no server change, but cannot enforce `read-only` ceilings on the target host and leaks OS-specific quoting logic into the caller.
   - **Owner**: User decides before Phase 2 starts.

3. **Result capture for structured agents**: When an agent outputs JSON (`claude -p --output-format json`, `codex exec --json`), should GoRex still run it inside a PTY (so desktop/iOS users can watch it live) or capture stdout via a pipe?
   - **Option 1 (Recommended)**: Run inside a normal PTY session and pass `--output-format json` (or tee to a temp result file on the target host) so the session remains observable in GoRex UI while `delegate` reads clean structured output.
   - **Option 2**: Run headless with pipes only. Simpler JSON parsing, but invisible in GoRex UI and impossible for a user to watch or intervene.
   - **Owner**: User decides before Phase 2 starts.
