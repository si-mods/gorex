# The gateway's model

Status: implemented in #893. Tracked in #879.

`GoRex rexapi` shows the app's layout to Rex clients as a model of
sessions, windows and blocks (`internal/rexapi/model.go`). This document
describes who builds that model, when, and what a request reads. Today
every request rebuilds it; the design below builds it once per change
and serves requests from the last built one — while keeping the promise
that a caller always reads its own writes.

## Today

Two paths rebuild the model (`internal/rexapi/gateway.go`):

- `pump` (`gateway.go:285`) listens on the client connection's events.
  A burst of events settles for 20 ms, then `reread(layout)` runs; a
  ticker repeats it every 500 ms on a server that pushes no events, and
  every 30 s as a safety net on one that does.
- Every request handler calls `current()` (`gateway.go:327`), which is
  `reread(true)`: a forced read of the layout over the local socket,
  whatever the events said.

`reread` (`gateway.go:338`) holds `refreshMu` for its whole run: the
socket read, `listSessions`, the model build, `diffModels` against the
last model, and `sendChanges` to every connection. `refreshMu` exists so
changes reach connections in the order they were read.

One server detail matters below: `internal/rex`'s `setLayout`
(`server.go:826`) is the runtime write path, and it sends a `layout`
event (`server.go:833`) whenever the bytes differ. But `Server.refresh()`
(`layout_life.go:36-56`) also writes `s.layout` — it stamps the facts the
sessions sampled (title, directory, program) at the *same* revision,
deliberately with no event: "the next change takes them along". Today's
per-request `current()` hides that: any request picks up the newest
stamps. The startup and migration writers (`server.go:363`,
`migrateLayout` in `projects.go`) run before the server listens, so a
subscriber always sees them in its first read.

## Problems

- Every request pays a socket round trip and a ~1,000-line model build,
  plus a diff and a fan-out it did not ask for — across ~30 call sites.
  The cost is per request, not per change.
- `afterCommand` (`layout.go:205`) multiplies it: it polls `current()`
  every 10 ms for up to `commandWait` — `rex.CommandTimeout` (6 s) + 1 s —
  so a slow write can mean ~700 socket reads, builds, diffs and
  broadcasts for one method call.
- `refreshMu` serializes request handlers: N clients' reads wait behind
  whichever read rebuilds first.
- A read-only method carries a broadcast side effect: the first request
  after a change sends every connection its events. Who fires the
  fan-out depends on timing, not on the code.
- Per-request reads also mask the missing `refresh()` event above, so
  stamped facts look fresher than the event stream actually delivers.

## Design

One goroutine builds and publishes the model; everything else reads the
last published one. The design rests on one invariant, checked against
`internal/rex`:

> **Every change to the layout's arrangement is preceded or followed by
> a `layout` event.**

`setLayout` is the only runtime writer that mutates the arrangement, and
it notifies on every byte difference; the load and migration writers
(`server.go:363`, `migrateLayout`) run before subscriptions exist, so a
subscriber always sees them in its first read.

The fact stamp is the deliberate exception: `Server.refresh()`
(`layout_life.go:36-56`) rewrites `s.layout` at the same revision with
the facts the sessions sampled — directory, size, foreground program,
agent session (`stamp`, `layout_apply.go:1495`) — and sends no event.
Adding one would change the wire protocol: today a `layout` event always
carries a new revision (`server.go:827-833`), so an unchanged-revision
event would be a new behavior for every subscriber, and
`docs/irreversible.md` puts the protocol behind a decision process this
doc does not need. The gateway copes on its own side instead: the stamp
lands at most `refreshWait` (~1 s) after the `sessions` event that
scheduled it (`refreshSoon`, `layout_life.go:26`), so on each `sessions`
event `pump` also arms a `facts` timer `factsWait` out; when it fires
it runs one `reread(true)`. `factsWait` is `refreshWait` plus 200 ms, a
constant in `gateway.go`: `refreshWait` is unexported in `internal/rex`.
The margin is enough because the server arms `refreshSoon` inside
`notify`, before it sends the `sessions` event, so its timer always
starts before the gateway's; the margin only has to cover the stamp's
own run. Stamped facts reach clients at the stamp's
own ~1 Hz cadence, at a cost of at most one layout read per second, and
the safety tick covers whatever a quiet session misses. Reusing the
session list for these facts was considered and dropped: `stamp` also
writes size and agent-session fields the session list does not carry.

### pump is the only builder

`reread` stays as it is — the socket read, `listSessions`, the build,
the diff, the broadcast, all under `refreshMu` — but only `pump` calls
the `reread(true)` form: on layout events, on the `facts` timer armed by
`sessions` events, and on the safety tick. A `sessions` event itself
still costs only a `reread(false)`, as today.
`refreshMu` keeps its real job, ordering the events sent to connections
and guarding `raw`/`desktop`; request handlers stop taking it for the
model, and `sessionsNow()` keeps it for the session list exactly as
today (`sessionsNow` is an etag-gated `ListSince`, not a rebuild — the
cost this doc removes is the layout read and the build, not that list).

Each publish swaps `g.model` (it stays an `atomic.Pointer`), bumps a
`seq` counter, and closes the current `changed` channel before
installing a fresh one. A waiter reads `modelNow` plus the channel once,
then blocks on the channel — the condition-variable pattern, without a
condition variable. Waiters also wake when `pump` exits on
`Client.Closed()` and when `g.done` closes, so a dead connection never
holds a waiter for the full `commandWait`.

### Requests read the snapshot

`current()` goes away. Handlers call `modelReq()`, which returns the
last published immutable model (`modelNow()`) behind the two guards
below.

Two guards keep a snapshot honest where today's read could fail:

- **Not ready yet**: `New` returns once the client's hello answers; it
  does not wait for the first publish, which may be slow on a loaded
  server. Until the first publish lands, a `ready` flag is unset;
  methods that need the model wait for it up to `commandWait`, then
  answer with an existing 500-class code (`internal`) and a "not ready"
  message — a slow answer, not the wrong
  `session_not_found`, and no new error code on the wire. The flag sets
  on the first publish and never clears.
- **Connection gone**: `pump` returns on `Client.Closed()` and nothing
  starts it again — the gateway does not reconnect its client. `pump`
  marks the gateway as it leaves (a read also checks
  `Client.Closed()`, so it need not wait for `pump`), and reads that
  needed `current()`'s error get the same 500 ("reading the layout: the
  GoRex server is gone") instead of a stale answer. `reread`'s own
  failure keeps the last model, exactly as today.

### Read your own writes

`current()` gave every caller read-your-writes for free; snapshots must
give it back per path. The write paths sort into three groups:

1. **Layout commands** — the `g.command(...)` sites (`layout.go`,
   `projects.go`, `spaces_sync.go`, `movewindow.go`,
   `mergewindows.go`): 37 calls, 30 of them already followed by
   `afterCommand(shows)`; the rest are rollbacks and intermediate focus
   steps the last `afterCommand` covers. (`session.new_window`'s focus
   steps run after its `afterCommand`, so it calls `afterCommand(nil)`
   once more for their revision.) `afterCommand` reads the layout once
   on the request path, a `reread(true)` that also publishes: the server
   usually applies a command before it answers it, so that one read
   answers at once, and a no-op command, which sends no layout event and
   so publishes nothing, does not wait `commandWait` for a publish that
   never comes. `afterCommand(nil)` is that read alone. Only when
   `shows` is still false does the method wait on `changed` until
   `shows(m)` or `commandWait`. For that wait, `pump` skips the
   `settle` burst while a waiter is registered and reads the layout as
   soon as the event arrives: no polling, and at most the one read on
   the request path.

2. **Gateway-local writes that feed the model** — ownership and
   attachment changes live in the gateway, so no layout event announces
   them. The sites that rebuild for them today keep a **synchronous**
   `reread(false)` — no socket read; it rebuilds from `g.raw` with the
   owners as they are now, and the caller answers from the model it just
   published: `methodSessionOwnBlock`/`methodSessionDisownBlock`
   (`layout.go:2048`, `:2064`), the `claimCreated` read
   (`layout.go:405`), `keepAttached` (`movewindow.go:109`),
   `cleanupClient`/`cleanupClientSession` (`lease.go:15`, `:24`),
   `resumeControl` (`gateway.go:594`), `dropControl` (`gateway.go:646`),
   `renewOwned` (`owners.go:460`, on the `renewLocks` goroutine) and
   `takeOnInput` (`owners.go:476`, on the input path). The last two call
   no `refresh()` today — the next request's `current()` picked their
   change up — and without a rebuild their owner changes would go unseen
   for up to `safetyEvery`. An asynchronous `kick()` was considered and
   rejected for exactly this reason: these callers answer from the
   model, so a coalesced publish would let a client read its own write
   as stale. `refreshMu` serializes these rebuilds with `pump`'s,
   keeping broadcast order.

3. **Everything else is a pure read** and takes `modelReq()`:
   `sessionArg`/`attachedArg`/`attachedSessionArg`, `session.list`,
   `session.attach`/`attach_all`/`detach`, `status`, the projects,
   spaces, target, upload, voice and client methods, and `splitsOf`
   (`session.new_split` reads the splits before its command from the
   same snapshot its arguments came from). `server.stop`'s idle check
   is the exception: it needs the layout as it is now, so it still runs
   `reread(true)`. `sessionsNow()`
   (`projects.go:273`) is unchanged. Writes that change neither the
   layout nor the model — `server.config_reload`'s generation, upload
   and download reservations, block themes (`g.themes` never enters
   `build`) — need no model path at all.

A second client reading right after another's write sees the model as
of the last publish: at most `settle`-plus-event latency old — the same
staleness window its own change stream already carries. That is the one
semantic change versus today, where any request forced a fresh read; it
is bounded, and the alternative is the per-request rebuild this doc
removes.

## What stays the same

- `model.go`'s mapper and `diffModels`: the model is still rebuilt whole
  each time. An incremental update on top of the raw layout diff is a
  possible later step, not part of this one.
- `listSessions` keeps its etag and stays inside `reread`; `sessionsNow`
  keeps `refreshMu`.
- `watchBlocks`, `pruneOwners`, `pruneAttachments` still run in
  `reread`, on the publishing side.
- The poll fallback for a server without `Subscribe`: `pump` reads every
  `pollEvery`, publishes, and waiters wake the same way.
- The wire protocol: no new events and no new error codes, so clients
  see the same events in the same order; a `layout` event keeps meaning
  "the revision changed".

## Steps

1. Gateway plumbing: `seq`, `changed`, the `ready` flag, the
   closed-connection mark, and `pump`'s `facts` timer. `current()`
   still exists; nothing else changes.
2. `afterCommand` waits on `changed`; `pump` skips `settle` while a
   waiter is registered.
3. Switch the pure-read sites to `modelNow()` and the group-2 sites to a
   synchronous `reread(false)` (renamed, e.g. `rebuild`); delete
   `current()` and `refresh()`.

Steps 1–2 are independent of the call-site sweep in step 3, which is
mechanical but not safe to split the other way: doing step 3 first
would make the post-local-change replies drop the change. Nothing in
`internal/rex` or the protocol changes.

## Tests

- A fake client that counts `Layout()` calls: a burst of read requests
  publishes nothing and makes zero socket reads; `afterCommand` makes
  one on the request path.
- Read-your-writes: a write followed at once by a read on the same
  connection sees the write; with two clients and a fake client whose
  layout event arrives late, the writer's `afterCommand` still resolves
  on publish, and the second client sees the change after the publish
  and not before.
- The `facts` timer: a `sessions` event arms it, a publish after
  ~`refreshWait` picks up the facts `Server.refresh()` stamped, and a
  second `sessions` event inside the window costs no extra layout read.
- The settle skip: a registered waiter wakes on the event, not after
  `settle`.
- Staleness: a closed client turns the next read into the 500
  `current()` used to return, and a read before the first publish gets
  "not ready", never a wrong `session_not_found`.
- Group-2 ordering: two owner changes produce two publishes and two
  ordered sets of events per connection.
- The existing suite is the guard for the rest: `diffModels`' output and
  the methods' answers do not change.
