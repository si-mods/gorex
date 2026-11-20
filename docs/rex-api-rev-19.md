# The Rex API through revision 19

Status: proposed. Nothing built. GoRex's gateway serves Rex API revision
14; this settles revisions 15 to 19, what each one is for in GoRex, and
the order they land in.

## Problem

Rex's apps and CLI negotiate their own API revision. A current Rex
client speaks up to 19, so against GoRex it sees a server that is five
revisions behind:

- `rex forward open BLOCK` cannot list what a pane listens on: the call
  to `com.superlogical.terminal.ports` fails with `revision_too_low`,
  and the port a dev server just opened never arrives as
  `ports_changed`.
- A Rex app built for 19 that closes a pane looks for
  `focus_history.reverse` to step back, and finds `method_not_found`.
  Rex's own clients have not adopted the walk yet (Rex 0.1.0-preview.22:
  "The clients are not updated yet"), so today this is protocol parity
  and a base for GoRex's own clients.
- An event larger than one control message ends the whole connection
  with status 1009 (`internal/rexapi/control.go:570`), where a revision
  18 server sends `event_dropped` and keeps the connection.
- A config action that asks the person a question (`rex.ui`) has no way
  to reach an app, and GoRex's own gateway runs no config actions at all
  (`internal/rexapi/clients.go:221`), so revision 17 is a shape without
  a producer.

None of this is theoretical: the shipping app (build 2063, helper
`rex 0.1.0-preview.22`) reports `api: revision 19
(supports 0 to 19)`, and the binary carries one history file per
revision, from `sdk/apihistory/r0015_terminal_ports.go` to
`r0019_focus_history.go`. A client that asks for 19
negotiates down to 14 (`negotiate` in `control.go` takes the smaller of
the two).

## What Rex added

Revisions 15 to 19, from the helper's `sdk/apihistory` files and its
`/llms.txt` at revision 19:

| Revision | History file | What it adds | GoRex today |
| --- | --- | --- | --- |
| 15 | `r0015_terminal_ports.go` | `com.superlogical.terminal.ports`: the TCP ports a terminal's programs listen on, the processes holding them, `status` ok/partial/unsupported, truncation through `blob_read` | absent |
| 16 | `r0016_terminal_ports_changed.go` | the terminal event `ports_changed`, carrying the same object as `ports` | absent |
| 17 | `r0017_client_prompt.go` | `client_prompt_requested`/`client_prompt_ended`, and prompt answers in `client.reply`: `{"text"}`, `{"ids"}`, `{"cancelled": true}`, for config actions that call `rex.ui` | absent (and no producer) |
| 18 | `r0018_event_dropped.go` | `event_dropped` in place of an event too large for one message; the connection stays open | closes with 1009 |
| 19 | `r0019_focus_history.go` | `focus_history.reverse`, per-principal focus history, and focus returning to the previous pane or tab when the focused one closes | absent |

Two older gates are also missing from `internal/rexapi/revisions.go`,
although the methods work: `com.superlogical.terminal.blob_read`
(revision 6) and `com.superlogical.terminal.download_begin` (revision
13). A client at revision 12 can call both against GoRex and cannot
against a Rex server of that revision. Fixing them is part of the first
step below.

## Where GoRex stands

- `internal/rexproto/protocol.json` is the source of the constants;
  `go generate ./internal/rexproto` writes `protocol.gen.go` and the
  web client's copy. `api_revision` there is 14.
- `internal/rexapi/revisions.go` translates requests and results for
  revisions 1 to 12 (`hide`/`rename`), and hides methods and creators
  that appeared later (`methodSince`, `creatorSince`).
- `internal/rexapi/help.go` serves a snapshot of Rex's revision 10 help
  document, patched for `upload_begin`, the tunnel creator and
  `listeners`. Revision 15 to 19 entries do not exist in it.
- `internal/rexapi/control.go:457` refuses a method a connection's
  revision predates; `control.go:529` encodes every event per connection
  and closes the connection when one outgrows `maxMessage`.
- `internal/rexapi/events_blocks.go:34` derives terminal block events
  from `SessionInfo` (title, bells, program status, pwd, process); the
  gateway has no port or focus state of its own.
- `internal/rexapi` is the only Rex API server. Rex's apps and CLI
  connect to it; GoRex's own apps do not (they speak GoRex's protocol),
  so every feature here serves Rex clients first and GoRex's own clients
  only if a later change adopts it.

## Revision 15 and 16: a terminal's ports

### What Rex does

`com.superlogical.terminal.ports` answers with `status` (`ok`,
`partial`, `unsupported`), `ports` (one entry per protocol, address and
port, with `first_seen_at` and the holding `pid`), `processes`
(pid, user_id, user, name, path, invoked_path, argv0, script, cwd), and optional `truncated`
plus `id` for `blob_read`. The server looks in the background: every 10
seconds at first, less often while nothing changes, down to every 5
minutes; a call, a new terminal or a change in any terminal starts it
over. A real change sends `ports_changed` with the whole list, at most
once a second per terminal (and within a server-wide limit), and the
last event of a burst carries the final list. A terminal whose first list is empty sends nothing. The
look never wakes an idle terminal.

### Design

**Server.** The session server owns the processes, so the scan lives in
`internal/rex`, not the gateway:

- `internal/rex/server_ops.go`: a `ports` request op per session,
  answered from the last snapshot, or from a fresh look when none is
  younger than a quarter second. It returns the same object the event
  carries.
- `internal/rex/ports.go` plus one file per platform: enumerate the
  session's processes and their listening sockets, keep `first_seen_at`
  per (protocol, address, port, pid), and report `status` `ok` or
  `partial`. A program can't be looked at (another user's, a failure),
  the answer is `partial`, not empty.
- Processes are the descendants of the session's PTY child
  (`SessionInfo.PID`), walked by parent pid:
  - Linux: `/proc/<pid>/task/*/children` (or a `/proc` stat scan),
    sockets from `/proc/net/tcp` and `tcp6` joined to
    `/proc/<pid>/fd` `socket:[inode]` links.
  - macOS: libproc, as `proc_darwin.go` already loads it:
    `proc_listchildpids`, `proc_pidinfo(PROC_PIDLISTFDS)` and
    `proc_pidfdinfo(PROC_PIDFDSOCKETINFO)` for the local address and
    port.
  - Windows: a Toolhelp parent/child snapshot, and
    `GetExtendedTcpTable(TCP_TABLE_OWNER_PID_LISTENER)` for the owners.
- Limits, stated so nobody is surprised later: a program that daemonizes
  out of the tree is missed. Rex misses those on macOS too (its own
  documentation names detached daemons and tmux), but on Linux it finds
  them through the terminal's cgroup, which GoRex does not create today.
  A later change can add a cgroup per terminal on Linux and a job object
  per terminal on Windows; this design does not depend on either.
- A background loop per session (or one loop over all sessions) looks
  when the session is active: every 10 seconds while its
  `LastOutput`/`LastInput` move, backing off to 5 minutes, and restarts
  at 10 seconds when the session's call or a new terminal asks. It
  compares snapshots and emits a server `ports` event only when the list
  really changes.

**Gateway.** `internal/rexapi` adds the method and the event, gated at
their revisions:

- `com.superlogical.terminal.ports` uses the server op; a result larger
  than the reply budget is stored in the gateway's existing blob store
  (`internal/rexapi/blocks.go:988`) and answered as `truncated` with
  `id`, exactly as `format` does.
- `ports_changed` is a `block_event` whose payload is the `ports`
  object, built by `emitBlockEvent` (`events_blocks.go:128`), and sent
  only to connections at revision 16 or later: an event a revision adds
  is not sent to a connection that predates it, so `emitBlockEvent` (or
  `wants`) needs an event-since check, which the gateway has no table
  for today.
- The server's `ports` event reaches the gateway through the pump
  (`gateway.go:285`), which today reacts to `layout` and `theme`; it gains
  a `ports` kind and turns it into the block event.
- Help entries and the terminal creator's method list gain `ports`; the
  `ports_changed` schema joins `blockEventSchemas`.
- The scan never reads the emulator; an idle terminal costs a process
  and socket table look at the slow cadence.

### What it unlocks

1. **`rex forward open` against GoRex.** `rex forward` already creates
   GoRex tunnel blocks, but `forward open -b BLOCK` first lists the
   block's ports; today that call fails. With this, a user runs a dev
   server in a GoRex pane, points the Rex CLI at the gateway, and the
   phone reaches it through the tunnel.
2. **The Rex Mac app's dev-server affordance.** A client that follows
   `ports_changed` shows a port the moment Vite or Next opens it,
   instead of polling.
3. **Scripts and agents.** "Wait until 3000 is listening" stops being
   output scraping: call `ports` once, then follow the event, or poll
   the method. An agent that starts a server can prove it is ready.
4. **GoRex's own clients, later.** A pane header can show its ports and
   offer "open on this machine" or "open through the tunnel"; iOS can
   offer the same while the pane runs on a remote host. Those are
   client changes, not protocol, and are not part of this design.

### Risks

- Enumerating sockets is per-platform and cannot be unit-tested
  everywhere: tests run against recorded tables (fakes for
  `/proc/net/tcp*`, synthetic libproc results) and one real check per
  platform in CI or on the Mac mini.
- On a machine with many processes the scan costs more than a single
  session; scanning only the session's tree keeps it proportional, and
  the cadence bounds it.
- `first_seen_at` is as late as the scan, which the method's own
  documentation already admits for Rex.

## Revision 17: asking the person a question

### What Rex does

A config action that calls `rex.ui` makes the server send
`client_prompt_requested` to every app that can show it: apps only, at
revision 17 or later, connected, and not already showing a prompt. The
event carries a prompt (`kind` input or pick, title, placeholder, value,
confirm label, `multiple`, `allow_other`, items with id/title/subtitle/
icon/detail/selected), the action's name, a per-connection `call_id` and
`timeout_ms` (up to an hour). The first answer wins
(`{"text"}`, `{"ids"}`, `{"cancelled": true}`); every other app gets
`client_prompt_ended` with `answered`, `timeout` or `cancelled`. A
`not_supported` reply removes that app only; an answer the prompt can't
accept fails with `bad_request` and leaves the prompt waiting.

### The producer problem

Prompts exist only for config actions, and GoRex's gateway runs none: it
declares no actions (`client.action` serves block methods only,
`clients.go:221`), loads no Lua (`client.set_script` refuses it), and
keeps no configuration file (`clients.go:17`). GoRex's own Lua config
runs in the app, not in the server (`docs/lua.md`), so nothing in the
current architecture calls `rex.ui`.

Options:

- **a. Broker only, no producer.** Every code path exists and nothing
  ever sends a prompt. Rejected: unexercised code, and the revision
  claims a capability the server cannot reach.
- **b. Host config actions in the gateway.** Load GoRex's `init.lua`
  with `internal/config`, run its `gorex.action`s over the Rex API, and
  map `gorex.invoke` to gateway operations. This is what makes prompts
  real, and it also closes a separate gap: today no Rex client can run
  a GoRex config action. It is a design of its own (where actions run,
  which built-in actions a server can perform, how `client.perform`
  fits); it is not settled here.
- **c. Prompts in GoRex's own clients only.** A question asked and
  shown inside one GoRex app needs no Rex API. It does not help a Rex
  app, and it does not answer "answer from any device".

**Decision:** land the broker together with producer b, in the last
step, and until then keep sending no prompts. A server whose actions
never ask a question sends no `client_prompt_requested`, which the
protocol allows; `client.reply` already answers `not_found` for a
`call_id` nothing waits for, which is correct for a prompt that does
not exist. Advertising revision 19 without a producer is therefore not
a lie about shapes: no client can reach a prompt.

### The broker, when the producer lands

- A prompt registry in the gateway keyed by `call_id`, one entry per
  (prompt, connection), with the deadline and the action that waits.
- Fan-out exactly as Rex describes; a connection showing a prompt is
  skipped, so a device is never asked two questions at once.
- Validation before the action sees an answer: ids must be offered, one
  unless `multiple`, text only for input or `allow_other`; a refused
  answer fails with `bad_request` and the prompt stays open.
- `client_prompt_ended` to every other connection, and on timeout or
  when the action stops; a late reply is `not_found`.
- Error messages are shortened to 4096 bytes, as revision 18's
  `client.reply` rules say.

### What it unlocks

1. **"New worktree".** A config action asks for a branch name, creates
   the worktree on the host, and opens a pane there. The question can be
   answered on the Mac or on the phone; the first answer wins.
2. **"Open project".** A pick prompt with the projects as items
   (title, subtitle, folder icon) instead of a text prompt that needs an
   exact name.
3. **Destructive or expensive actions.** "Close 4 sessions?", "Run the
   full test suite?" with a confirm label, answered where the person is.
4. **Agents.** An agent that needs a decision can run an action that
   asks, and the person answers from any connected app; the action's
   timeout excludes the time the prompt is open.

## Revision 18: dropping an event instead of the connection

### Design

`sendChanges` (`internal/rexapi/control.go:529`) already encodes each
event once per change and appends the sequence number. When the encoded
event is larger than `maxMessage`:

- **revision 18 and later:** send an `event_dropped` event with the same
  `seq` it would have had, carrying `event_type` (the inner type for a
  session event), `session_id`, and for a `block_event` its `block_id`,
  `creator_name` and `block_event_name`, plus `size` in bytes, not
  counting the control message around it. The connection stays open; the
  client refetches the session or the server-wide state the dropped event
  was about.
- **revision 17 and earlier:** send nothing in its place, and keep the
  connection. The docs say an older connection "gets nothing in its
  place", where a revision 17 server closed it with 1009.

`change` gains the block fields the drop needs (`events_blocks.go:128`
already has them) so the dropped event can name its source without
re-parsing the payload. The 8 MiB event backlog that closes with 1008
(`control.go:575`), and the 1009 for an oversized request, stay as they
are.

### What it unlocks

1. **A phone on a slow link.** On a tailnet link, a burst of events can
   queue; one oversized one used to cost the whole control connection
   and a reconnect, and the phone lost the session it was showing. The
   marker keeps the stream and costs one refetch.
2. **A pane that prints a huge title or status.** `printf '\e]2;%s\a'`
   with a megabyte, or a status record with a huge `msg`, produces one
   oversized event. It no longer takes every connected client down.
3. **GoRex's own clients later.** The same rule can protect GoRex's
   protocol; this design only changes the Rex API side.

## Revision 19: focus history

### What Rex does

Each principal's focus changes are tracked. Closing or detaching the
focused pane, or closing a tab, puts focus back on the pane or tab the
person was in before, not on the neighbor. `focus_history.reverse`
steps back: filters (`session_id`, `not_session_id`, `window_id`,
`not_window_id`), an optional `cursor` from the previous result, and a
result that says whether focus moved (`reversed`), where, and the next
`cursor`. It runs in order with the other ordered methods. A call that
names a `session_id` needs the connection attached to it.

### Design

**Keys.** The gateway already knows each connection's principal
(`gateway.go:453`, from `internal/rexapi/origin.go`). History is keyed by
who the caller is, not by connection, so a person's devices share it
where the identity is shared:

| Principal | Key |
| --- | --- |
| Unix peer | `peer:<uid>`; where the system gives no uid, the client id |
| Built-in Tailscale | `tailscale:<user_id>` (the login id, not the login name) |
| Tailcat device | `device:<display>`, else the client id |
| Local-network or remote TCP, anonymous | `client:<client id>`, so one connection's history stays its own |

**Storage.** Per key, a bounded MRU of `{seq, session, window, block}`
(64 entries to start; the cap is an open question) and a counter for
`seq`. It lives in memory: a gateway restart starts empty, and every
call answers `reversed: false` until the user focuses something, which
a client can already show. No file format changes.

**Recording.** After every successful call from that principal that
leaves a focused block, the gateway appends where focus landed, deduped
against the newest entry: `session.focus_block`, `focus_window`,
`focus_direction`, `focus_next/previous_pane/window`,
`session.new_split`, `new_window`, `new_block`, `new_layer`, and the
closes (see below). This is also what the ordered-method list in
`internal/rexapi/protocol.go` already anticipates; `focus_history.reverse`
joins it.

**Reverse.** With the model in hand:

1. Resolve filters; an unknown `session_id` is `session_not_found`, a
   `session_id` the connection is not attached to is `conflict`.
2. Start at the current focus of that principal (or of the model when
   the principal has none yet). With a `cursor`, look only at entries
   older than the one that reply went back to.
3. Walk newest to oldest, skipping entries that no longer exist, that
   equal the current focus, or that fail a filter. A block is judged on
   its own, as Rex says.
4. Focus the first survivor with the same commands `session.focus_block`
   uses, wait for the model, and answer `reversed: true` with the
   session, window and block. Otherwise answer `{"reversed": false}`.

**Close-focus.** In `session.close_window`, `close_layer`,
`detach_block` and `block.close`, when the thing closed held the
caller's focus, the gateway focuses the newest surviving history entry
after the close and includes that change in the revision it returns. If
no entry survives, the server's own choice stands.

GoRex's own app keeps its own rule for a close it makes; changing the
desktop is a separate, user-visible change and is not a goal here. The
gateway's history still knows about a close it performed, so a Rex app
gets Rex's behavior on the same shared session.

### What it unlocks

1. **"Back", like a desktop switcher.** Bind a key to
   `focus_history.reverse` with `window_id` to mean "last pane in this
   tab", with `not_window_id` "last other tab", with `not_session_id`
   "last other session": the same walk Rex's clients are built to show.
2. **Closing returns you.** Close a pane after a quick look and land
   on the pane you were in before, not the one beside it; close a tab
   and return to the tab you came from.
3. **Cross-session work.** Jump to another session to answer an agent,
   then step back to the session you were reading.
4. **GoRex's own clients later.** The desktop and iOS can offer the
   same walk once the server keeps the history; that is where a
   `gorex` binding would go.

### Risks

- Focus is session-wide, but history is per principal; two people on
  one session can move focus under each other. Rex has the same model,
  and the walk skips whatever is not there any more.
- A close-focus command is one more layout change per close; the close
  reply waits for it, so a client sees the final state, at the cost of
  one round trip to the local server.
- The cap and restart behavior are GoRex's choices, not Rex's; they are
  listed as open questions so they can be settled against a real
  client.

## Rollout

Each step is its own PR, and each bumps `api_revision` only once the
shapes up to that revision are served. Step 2 moves past revision 17
without a prompt producer: revision 17's shapes are server-initiated
events that a server with no prompting actions never sends, and a prompt
answer is a `client.reply` result the gateway already accepts and reports
as `not_found` for a call nothing waits for. A step that changed a shape
a client at the new number could observe would break the rule; this one
changes none.

| Step | Revisions | Work | Bump |
| --- | --- | --- | --- |
| 1 | 15, 16 | server scan and `ports` op, gateway method and event, help entries, the missing `methodSince` gates for 6 and 13 | 16 |
| 2 | 18 | `event_dropped` in `sendChanges`, block fields on `change` | 18 |
| 3 | 19 | focus history, `focus_history.reverse`, close-focus | 19 |
| 4 | 17 | prompt broker with a producer (its own design) | none |

For every step: `go generate ./internal/rexproto`, the help entries in
`internal/rexapi/help.go`, a contract test at the new revision and at
the one before it, and the status line here updated in the same PR.
`docs/rex-api.md`'s "revision 12" is corrected to the number the
gateway serves as part of step 1.

## Tests

- **Revision gates.** A control client at a revision before a method's
  own answers `method_not_found` for it; at that revision the method is
  reachable (it may still fail on its arguments). An event a revision
  adds is not delivered to a connection older than it. The existing
  `internal/rexapi/revisions_test.go` pattern extends to 15, 16, 18 and
  19.
- **Ports.** Table tests per platform with recorded socket and process
  tables; one real check on macOS and Linux (a program listening on
  127.0.0.1 in a session, its pid and `first_seen_at`), and the same on
  Windows in CI. The event test runs the burst rule: a change sends one
  event, a second change within a second sends none until the limit.
- **Event drop.** A connection at 17 gets no event and stays open; at 18
  it gets `event_dropped` with the same `seq` and the block fields; the
  client's refetch produces the current state. The test forces an event
  over `maxMessage` the way `control_test.go:107` forces 1009 today.
- **Focus history.** Two principals do not see each other's entries;
  filters skip as documented; `cursor` walks back one entry at a time; a
  closed block is skipped; `session_id` without attachment is
  `conflict`; a close from a focused pane lands on the previous one.

## Non-goals

- GoRex's desktop and iOS focus, close and pane-switch rules. This
  design changes the Rex API; adopting the same walk in GoRex's own
  keymap is a later, separate change.
- Gateway-hosted config actions. Prompts land with them (step 4), not
  before.
- A cgroup or a job object per terminal on Linux and Windows. The port
  scan works without them and states what it misses.
- Persisting focus history across a gateway restart.

## Open questions

- How deep should the focus history be, and does a restart need to keep
  it? Start with 64 entries in memory; revise after a client uses it.
- Should `first_seen_at` name the scan that first saw a port, or the
  socket's own start time? Rex reports the scan; GoRex follows unless a
  client needs more.
- Does the prompt producer (step 4) run GoRex's `init.lua` in the
  gateway, or does the app run it and ask the gateway to fan out? The
  first also gives Rex clients GoRex config actions; the second keeps
  Lua where it is. Settle it in that design, not here.
