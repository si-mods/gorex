# gorex command line

`gorex` is the same binary as the app. From a terminal it drives sessions
without opening a window; with no arguments it starts the app.

`-S HOST` before a command uses a saved remote host from `hosts.json`, or
an address, instead of this machine's server. Local-only commands (`hosts`,
`skill`, `remote`) and window-only commands (`split`, `close`) do not
apply remotely. `action` applies remotely when the host turns Remote
Control on.

Every local terminal GoRex opens gets:

- `GOREX_SESSION`, the session the terminal belongs to, which commands
  use when `-s` is not given.
- `GOREX_BIN_DIR`, and that directory at the front of `PATH`, where a
  `gorex` shim points at the app binary.
- `GOREX_SERVER`, the server's socket.
- A pane made by the app also gets `GOREX_PANE`, and `GOREX_HOST` for a
  remote host's pane or `GOREX_CLIENT` for a local one.

A session a remote client created does not get the shim: its shell is
already reachable from another machine, and the shim would make this
machine's local socket look local. It still gets `GOREX_SESSION`.

## Commands

```sh
gorex ls [--json]
gorex clients [--json]
gorex new [--dir DIR] [--json] [-- COMMAND...]
gorex split [-s SESSION] [--down] [--dir DIR]
gorex close [-s SESSION] [--tab]
gorex send [-s SESSION] [--enter] [--json] TEXT...
gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
gorex wait [-s SESSION] [--json] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
gorex run [--json] [--dir DIR] -- COMMAND...
gorex events
gorex hosts [ls] [--json]
gorex skill show|setup|status|remove [--agent NAME] [--project] [--json]
gorex remote status [--json] | remote control on|off
gorex action run [-s SESSION] [--json] ACTION [KEY=VALUE...]
gorex version [--json]
```

Targets are a full session ID, a workspace or tab label, a position in
`ls` (1 is the first), or the end of an ID (at least four characters). An
ambiguous target exits 3.

- `new` creates a session and prints its ID.
- `send` types without attaching; `--enter` presses Return.
- `capture` prints the screen as text, or the escape sequences with
  `--raw`.
- `wait --exit` exits with the program's status; the other conditions
  exit 0, and a timeout exits 1. `--timeout 0` (the default) waits forever.
- `run` creates a session, runs the command, prints what the screen shows
  when it ends, and exits with the program's status.
- `split` and `close` change the window's layout, so they need the GoRex
  window open on that machine.
- `events` prints each layout or session change as one JSON line.
- `hosts` lists saved remote hosts; addresses stay hidden.
- `skill setup` installs the GoRex guide into Claude Code, Codex, OpenCode
  and Pi skill directories, and a `SessionStart` context hook for Claude
  Code and Codex. `--project` writes the skill into the repository without
  touching settings; `--dry-run` reports first; `skill remove` removes
  only what GoRex installed.
- `remote status` shows the local server's remote access without its
  address; `remote control on|off` lets paired clients run app actions
  through the window. It applies to every paired client and every member
  of a joined network, and persists even while remote access is off.
  Destructive actions stay refused.
- `action run` runs a built-in action by its ID, through the window.
  Values after the action are strings; the CLI does not guess types.

Exit codes: 0 ok, 1 failed, 2 usage, 3 no such session or target, 4
server unreachable. A signal exits 130. `run` and `wait --exit` return the
program's own status, which can collide with 1–4; the rest return the
codes above. `wait --json`, `run --json` and `action run --json` print a
JSON object while keeping the same exit status.

## Agents

A local session's shell already has `gorex` on `PATH`, so an agent can run:

```sh
gorex new --dir /tmp/demo
gorex send -s <id> --enter 'make test'
gorex wait -s <id> --contains FAIL --timeout 120s
gorex capture -s <id>
```

Host add/remove and remote `split`/`close` are not part of this slice.
