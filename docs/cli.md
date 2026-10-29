# gorex command line

`gorex` is the same binary as the app. From a terminal it drives sessions
without opening a window; with no arguments it starts the app.

Every terminal GoRex opens gets:

- `GOREX_SESSION`, the session the terminal belongs to, which commands
  use when `-s` is not given.
- `GOREX_BIN_DIR`, and that directory at the front of `PATH`, where a
  `gorex` shim points at the app binary.

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
gorex action run [-s SESSION] ACTION [KEY=VALUE...]
gorex version [--json]
```

- `new` creates a session and prints its ID.
- `send` types without attaching; `--enter` presses Return.
- `capture` prints the screen as text, or the escape sequences with
  `--raw`.
- `wait --exit` exits with the program's status; the other conditions
  exit 0, and a timeout exits 1.
- `run` creates a session, runs the command, prints what the screen shows
  when it ends, and exits with the program's status.
- `split` and `close` change the window's layout, so they need the GoRex
  window open on that machine.
- `action run` runs a built-in action by its ID, through the window.

Exit codes: 0 ok, 1 failed, 2 usage, 3 no such session or target, 4
server unreachable, 130 interrupted.

## Agents

A session's shell already has `gorex` on `PATH`, so an agent can run:

```sh
gorex new --dir /tmp/demo
gorex send -s <id> --enter 'make test'
gorex wait -s <id> --contains FAIL --timeout 120s
gorex capture -s <id>
```

Remote hosts and the agent skill installer are not part of this first
slice.
