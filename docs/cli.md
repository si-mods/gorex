# gorex command line

`gorex` is the same binary as the app. From a terminal it drives sessions
without opening a window; with no arguments it starts the app.

`-S HOST` before a command uses a saved remote host from `hosts.json`, or
an address, instead of this machine's server. Local-only commands (`hosts`,
`skill`, `remote`, `server`) and window-only commands (`split`, `close`) do
not apply remotely. `action` applies remotely when the host turns Remote
Control on; `layout` reads a host's layout when it turns Share Layout on.

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
gorex [-S HOST] layout [--json]
gorex new [--dir DIR] [--json] [-- COMMAND...]
gorex split [-s SESSION] [--down] [--dir DIR]
gorex close [-s SESSION] [--tab]
gorex rename [-s SESSION] NAME
gorex send [-s SESSION] [--enter] [--json] TEXT...
gorex capture [-s SESSION] [--history BYTES] [--raw] [--json]
gorex wait [-s SESSION] [--json] (--exit | --idle | --status STATE | --contains TEXT) [--timeout D]
gorex run [--json] [--dir DIR] -- COMMAND...
gorex events
gorex hosts [ls] [--json]
gorex skill show|setup|status|remove [--agent NAME] [--project] [--json]
gorex remote status [--json] | remote control on|off
gorex server restart
gorex config path | config check [FILE] | config reload
gorex action run [-s SESSION] [--json] ACTION [KEY=VALUE...]
gorex float [new|toggle|hide-all|raise|lower|cycle|move|resize|dock] [-s SESSION] [--json] [KEY=VALUE...]
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
- `split` and `close` change the window's layout, so they need the GoRex
  window open on that machine.
- `events` prints each layout or session change as one JSON line; with a
  host that shares its layout, it subscribes to that host's layout too.
- `clients` prints the principal, peer key, whether it is remote, through a
  kept connection, and how many seconds it has been attached.
- `hosts` lists saved remote hosts; addresses stay hidden.
- `skill setup` installs the GoRex guide into Claude Code, Codex, OpenCode,
  Pi and omp skill directories, and a session-start hook for Claude Code,
  Codex, Pi and omp (see [Agents](#agents)). `--project` writes the skill
  into the repository without touching settings; `--dry-run` reports
  first; `skill remove` removes only what GoRex installed.
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

### Session-start hooks

`gorex skill setup` installs a hook that each agent runs as a session
starts:

- Claude Code: a `SessionStart` hook in `$CLAUDE_CONFIG_DIR/settings.json`
  (`~/.claude/settings.json`), beside the hooks already there. It reads
  the conversation's `session_id` from the JSON the hook gets on its input.
- Codex: a `SessionStart` hook in `$CODEX_HOME/hooks.json`. It gives the
  context only: GoRex does not resume Codex.
- Pi and omp: an extension, `extensions/gorex/index.ts` in the agent
  directory (`PI_CODING_AGENT_DIR`, which both read, else `~/.pi/agent`
  and `~/.omp/agent`). It runs the hook in the background as a session
  starts, new, resumed or forked, prints nothing, and adds what the hook
  printed to the system prompt.

Outside a GoRex session the hook does nothing. Inside, it runs
`gorex agent-hook --agent NAME [--conversation ID] [--pid PID]`, which
tells the session's server the agent and its conversation, and prints the
session's ID and, when its pane has no name yet, asks the agent to name
it after its task with `gorex rename` (two to four lowercase words joined
by hyphens). It never fails the agent's start: it exits 0, and gives up on
a server that does not answer within two seconds.

The server keeps the agent of each session while the agent's process
runs: the hook's report is kept only when the agent (the first process
above the hook that is not a shell) is the program in the session's
foreground or runs under it, as `caffeinate claude` or `npx` run it. One
that runs under another agent, a subagent started through its tool's
shell, does not replace it. `gorex ls --json` shows it as `agent` (`name`,
`conversation`, `pid`). The record ends when that process does, or the
session; a pane back at its shell is not resumed later. The journal hears
of that end 5 seconds later, if the server still runs: as the machine
shuts down every process gets SIGTERM, and an agent that ends a moment
before the server is still resumed. An in-place
upgrade hands the records to the new build, and the layout journal keeps
them, so that a server that starts the sessions
again — after a crash, a reboot or `gorex server restart` — starts the
shell in the pane's directory, waits for its prompt, and types the
command that resumes the conversation, then Return:

| Agent       | Command                |
|-------------|------------------------|
| Claude Code | `claude --resume ID`   |
| omp         | `omp --resume=ID`      |
| Pi          | `pi --session ID`      |

When the agent exits, the pane is back at its shell. Nothing is typed
for an agent that reported itself meanwhile; a shell that does not go
quiet within 10 seconds, or that runs another program by then, gives the
agent up. `skill status` shows a hook of an older build as `outdated`,
and `setup --dry-run` as `would replace`. A conversation ID
is taken only of letters, digits, `.`, `_` and `-`, starting with a
letter or digit, so the command is typed unquoted and reads the same in
every shell GoRex starts, PowerShell and `cmd` on Windows included. An
agent the server does not see in the foreground within 30 seconds of the
command is given up.

Host add/remove and remote `split`/`close` are not part of this slice.
