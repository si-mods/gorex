# gorex command line

`gorex` is the same binary as the app. From a terminal it drives sessions
without opening a window; with no arguments it starts the app.

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
gorex wait [-s SESSION] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
gorex run [--dir DIR] -- COMMAND...
gorex events
gorex hosts [ls] [--json]
gorex skill show|setup|status|remove [--agent NAME] [--project] [--json]
gorex action run [-s SESSION] ACTION [KEY=VALUE...]
gorex version [--json]
```

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
  and Pi skill directories. `--project` writes into the repository;
  `--dry-run` reports first. Hooks are not installed yet.
- `action run` runs a built-in action by its ID, through the window.
  Values after the action are strings; the CLI does not guess types.

Exit codes: 0 ok, 1 failed, 2 usage, 3 no such session or target, 4
server unreachable. A signal exits 130. `run` and `wait --exit` return the
program's own status, which can collide with 1–4; the rest return the
codes above.

## Agents

A local session's shell already has `gorex` on `PATH`, so an agent can run:

```sh
gorex new --dir /tmp/demo
gorex send -s <id> --enter 'make test'
gorex wait -s <id> --contains FAIL --timeout 120s
gorex capture -s <id>
```

Remote `-S` targets, host add/remove, label/position target resolution and
agent hooks are not part of this slice.
