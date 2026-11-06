# gorex command line

`gorex` is the same binary as the app. From a terminal it drives sessions
without opening a window; with no arguments it starts the app.

`-S HOST` before a command uses a saved remote host from `hosts.json`, or
an address, instead of this machine's server. Local-only commands (`hosts`,
`remote`, `server`, `update`) and window-only commands (`split`, `close`) do
not apply remotely. `action` applies remotely when the host turns Remote
Control on; `layout` reads a host's layout when it turns Share Layout on.

Every local terminal GoRex opens gets:

- `GOREX_SESSION`, the session the terminal belongs to, which commands
  use when `-s` is not given.
- `GOREX_BIN_DIR`, and that directory at the front of `PATH`, where a
  `gorex` shim points at the app binary.
- `GOREX_SERVER`, the server's socket.
- A pane made by the app also gets `GOREX_PANE`, and `GOREX_HOST` for a
  remote host's pane or `GOREX_CLIENT` for a local one. A pane the server
  made for a client with no window open gets `GOREX_CLIENT` only.

A session a remote client created does not get the shim: its shell is
already reachable from another machine, and the shim would make this
machine's local socket look local. It still gets `GOREX_SESSION`.

## Commands

```sh
gorex ls [--json]
gorex clients [--json]
gorex [-S HOST] layout [--json]
gorex new [--dir DIR] [--json] [-- COMMAND...]
gorex split [-s SESSION] [--down] [--dir DIR] [-- COMMAND...]
gorex close [-s SESSION] [--tab]
gorex rename [-s SESSION] NAME
gorex send [-s SESSION] [--enter] [--json] TEXT...
gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
gorex wait [-s SESSION] [--json] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
gorex run [--json] [--dir DIR] -- COMMAND...
gorex events
gorex hosts [ls] [--json]
gorex skill show|setup|status|remove [--agent NAME] [--project] [--dry-run] [--json]
gorex remote status [--json] | remote control on|off
gorex server restart | upgrade
gorex update
gorex config path | config check [FILE] | config reload
gorex action run [-s SESSION] [--json] ACTION [KEY=VALUE...]
gorex float [new|toggle|hide-all|raise|lower|cycle|move|resize|dock] [-s SESSION] [--json] [KEY=VALUE...]
gorex teleport [--json] PANE HOST
gorex rex-import [--rex SOCKET] [--session LABEL|ID] [--shell] [--resize] [--json]
gorex rex-mirror --session ID --block ID [--label LABEL] [--socket PATH] [--resize]
gorex version [--json]
```

Targets are a full session ID, a workspace, tab or pane label, a position
in `ls` (1 is the first), or the end of an ID (at least four characters).
`-b` chooses a pane label, `-w` a tab label; `-b` wins over `-w`, which
wins over `-s`. An ambiguous target exits 3. `rename` gives a pane a label.

- `new` creates a session and prints its ID.
- `send` types without attaching; `--enter` presses Return.
- `capture` prints the screen as text, or the escape sequences with
  `--raw`.
- `wait --exit` exits with the program's status; the other conditions
  exit 0, and a timeout exits 1. `--timeout 0` (the default) waits forever.
- `run` creates a session, runs the command, prints what the screen shows
  when it ends, and exits with the program's status.
- `split` changes the layout; the command after `--` runs in the new
  pane, or a shell does. The session server makes the change, and an
  open window shows it at once, a closed one as it opens. `close` and
  `rename` work the same way.
- `events` prints each layout or session change as one JSON line; with a
  host that shares its layout, it subscribes to that host's layout too.
- `clients` prints the principal, peer key, whether it is remote, through a
  kept connection, and how many seconds it has been attached.
- `hosts` lists saved remote hosts; addresses stay hidden.
- `remote status` shows the local server's remote access without its
  address; `remote control on|off` lets paired clients run app actions
  through the window. It applies to every paired client and every member
  of a joined network, and persists even while remote access is off.
  Destructive actions stay refused.
- `server restart` stops the local server, and the next one starts the
  sessions of the layout again, as one after a crash does: new shells at
  the directories their panes were in, with the same ids, so the windows
  attach to them; what the old ones printed is gone. Quit and End All
  still ends sessions. It starts no server itself: the open app
  reconnects and starts the next one, else the next `gorex` command or
  the next launch does, which brings the sessions back; until then none
  runs. Run inside a pane, it tells of the new shell first, then ends
  with the pane. It exits 0 when it is done, 1 when the server is too old
  to bring its sessions back (quit it once with Quit and End All, or kill
  it), and 4 when no server runs or the server did not stop.
- `server upgrade` hands the local server over to the executable you
  invoked on macOS or Linux. Shell processes and their output survive
  while clients reconnect. The executable's modification time must be
  newer than the server's, as for an app-initiated upgrade. The exact
  build already running is a no-op; an older build, or a different path
  with the same modification time, is refused.
- `update` checks the signed release feed for the Linux per-user install.
  It installs the update without opening a window and hands over the
  running server when there is one. Use the same `GOREX_DIR` as the
  server if you set one. On macOS use Check for Updates in the app.
- `rex-import` reads a Rex server and makes its sessions in GoRex: a space
  per session, a tab per window, a pane per block. Each pane runs this
  program's `rex-mirror`, which draws the Rex block and types into it, or
  runs a shell in its directory with `--shell`; `--resize` lets a mirror
  give the block the pane's size. Sessions already imported are recorded
  in `rex-import.json` in the data directory and left alone.
- `rex-mirror` is what those panes run; it needs the GoRex window open for
  the import, not to mirror.
- The window's **Space ▸ Import from Rex**, and the palette's *Import from
  Rex*, do what `rex-import` does with mirrors, for every session not
  recorded yet.
- `action run` runs a built-in action by its ID, through the window.
  Values after the action are strings; the CLI does not guess types.
- `float` floats or docks panes through the window: `new` opens a floating
  terminal, `dock` puts the focused float back among the tiled panes, and
  the other subcommands name the `float.*` actions.
- `teleport` moves a pane of this machine to a host
  ([teleport.md](teleport.md)), named as `-S` names one: by its name as
  Remote Hosts shows it, or its address. It prints the new session's ID
  there, and has the window show the pane as that session; with no window
  showing the pane, its session ends and the new one waits on the host;
  when the window cannot show the new session, both stay and it exits 1.

`gorex rexapi` is not a command of this line: it serves Rex's API to Rex
clients from the server the app already runs. The server runs that
gateway itself while the local network listener or built-in Tailscale is
on; `gorex rexapi` alone runs one of its own, for development.
`gorex rexapi lan on|off|status`, `gorex rexapi lan listen HOST:PORT` and
`gorex rexapi lan allow|deny NETWORK` set the listener on the local
network; `gorex rexapi tailscale on|off|login|logout|rename|status`,
`gorex rexapi pair` and `gorex rexapi call METHOD [JSON]` talk to the
gateway. See [rex-api.md](rex-api.md).

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

### The skill and its hooks

`gorex skill setup` installs a `gorex` skill that points at the guide the
installed binary prints, so upgrading GoRex updates what agents are told.
It finds Claude Code (`~/.claude`), Codex (`~/.codex`), OpenCode
(`~/.config/opencode`, or `$XDG_CONFIG_HOME/opencode`), and Pi
(`~/.pi/agent`, or `$PI_CODING_AGENT_DIR`). A named `--agent` is set up
even when its directory is absent; an unknown one is an error.

The skill goes into Claude Code's own `skills/gorex` directory and into
the shared `~/.agents/skills/gorex` directory that Codex, OpenCode and Pi
read. With no agent found, only the shared skill is written.

Claude Code and Codex also get a session-start hook, added to
`~/.claude/settings.json` and `~/.codex/hooks.json`. The hook runs
`gorex __internal session-start --agent NAME`, which prints the session,
pane and connection for the agent's context, and labels the pane when
`--pane-naming` is on. Only GoRex's own hook is added or removed:
every other setting and hook is left as it was, and a settings file that
is not valid JSON is reported as a conflict instead of rewritten.

- `gorex skill status` reports each agent and whether its skill and hook
  are missing, current, outdated or in conflict. `--json` has the same
  information for scripts.
- `gorex skill remove` removes GoRex's skill and hook from every known
  agent, including agents no longer installed, and cleans up the skill
  and Pi/omp extension older builds wrote. It never removes a file GoRex
  did not write.
- `gorex skill setup --dry-run` reports without changing anything;
  `--force` replaces a `gorex` skill the user wrote.
- `--project` writes into the current repository instead: the shared
  `.agents/skills/gorex`, or with `--agent claude`, `.claude/skills/gorex`
  and a hook in `.claude/settings.local.json`. Codex project hooks are
  skipped because Codex asks the user to trust the project and each hook;
  set those up for the account instead.
- `gorex skill show` prints the same guide the skill tells agents to read.
  `gorex __internal skill` and `gorex __internal session-start` are the
  commands the installed files run and are not part of the documented
  command line.

Host add/remove and remote `split`/`close` are not part of this slice.
