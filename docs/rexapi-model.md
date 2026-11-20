# The gateway's model

Status: design. Tracked in #879.

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
last published one. The design rests on one invariant, which today is
almost true:

> **Every change to the bytes `Client.Layout()` returns is preceded or
> followed by a `layout` event.**

Checked against `internal/rex`: `setLayout` is the only runtime writer
that mutates the arrangement, and it notifies on every byte difference;
the load and migration writers run before subscriptions exist. The one
gap is `Server.refresh()`'s fact stamp. The design adds
`s.notify("layout")` to it: the revision already stays the same — the
event only means "read the layout again" — and a `refreshSoon` batch
already caps the stamp at ~1 Hz. That one line both closes the gap and
makes title updates reach remote subscribers at the stamp's cadence
instead of riding the next layout change.

### pump is the only builder

`reread` stays as it is — the socket read, `listSessions`, the build,
the diff, the broadcast, all under `refreshMu` — but only `pump` calls
the `reread(true)` form, on layout events and on the safety tick.
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

`current()` goes away. Handlers call `modelNow()` (`gateway.go:442`),
which already returns the last published immutable model.

Two guards keep a snapshot honest where today's read could fail:

- **Not ready yet**: `New` returns once the client's hello answers; it
  does not wait for the first publish, which may be slow on a loaded
  server. Until the first publish lands, a `ready` flag is unset and
  methods that need the model answer a 503-class error
  ("the gateway is not ready") — a slow answer, not the wrong
  `session_not_found`. The flag sets on the first publish and never
  clears.
- **Connection gone**: `pump` returns on `Client.Closed()` and nothing
  starts it again — the gateway does not reconnect its client. `pump`
  marks the gateway as it leaves, and reads that needed `current()`'s
  error get the same 500 ("no connection to the GoRex server") instead
  of a stale answer. `reread`'s own failure keeps the last model,
  exactly as today.

### Read your own writes

`current()` gave every caller read-your-writes for free; snapshots must
give it back per path. The write paths sort into three groups:

1. **Layout commands** — the ~37 `g.command(...)` sites (`layout.go`,
   `projects.go`, `spaces_sync.go`, `movewindow.go`, `mergewindows.go`).
   Each is already followed by `afterCommand(shows)`. The method waits
   on `changed` until `shows(m)` or `commandWait`. To keep this at least
   as fast as today's first 10 ms poll, `pump` skips the `settle` burst
   while a waiter is registered and reads the layout as soon as the
   event arrives. The answer then lands at event time: no socket reads
   on the request path, no polling. (A `shows(modelNow())` check before
   the wait is only an early exit for the case where a publish raced
   ahead of the command's reply; it is usually false, since the snapshot
   moves only on publish.)

2. **Gateway-local writes that feed the model** — ownership and
   attachment changes live in the gateway, so no layout event announces
   them. The sites that rebuild for them today keep a **synchronous**
   `reread(false)` — no socket read; it rebuilds from `g.raw` with the
   owners as they are now, and the caller answers from the model it just
   published: `methodSessionOwnBlock`/`methodSessionDisownBlock`
   (`layout.go:2048`, `:2064`), the `claimCreated` read
   (`layout.go:405`), `keepAttached` (`movewindow.go:109`),
   `cleanupClient`/`cleanupClientSession` (`lease.go:15`, `:24`),
   `resumeControl` (`gateway.go:594`), `dropControl` (`gateway.go:646`).
   An asynchronous `kick()` was considered and rejected for exactly this
   reason: these callers answer from the model, so a coalesced publish
   would let a client read its own write as stale. `refreshMu`
   serializes these rebuilds with `pump`'s, keeping broadcast order.

3. **Everything else is a pure read** and takes `modelNow()`:
   `sessionArg`/`attachedArg`/`attachedSessionArg`, `session.list`,
   `session.attach`/`attach_all`/`detach`, `status`, the projects,
   spaces, target, upload, voice and client methods. `sessionsNow()`
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
- The wire protocol: clients see the same events in the same order.

## Steps

1. `internal/rex`: `Server.refresh()` notifies `layout` when a stamp
   lands, with its test. This is the invariant the rest stands on.
2. Gateway plumbing: `seq`, `changed`, the `ready` flag and the
   closed-connection mark. `current()` still exists; nothing else
   changes.
3. `afterCommand` waits on `changed`; `pump` skips `settle` while a
   waiter is registered.
4. Switch the pure-read sites to `modelNow()` and the group-2 sites to a
   synchronous `reread(false)` (renamed, e.g. `rebuild`); delete
   `current()` and `refresh()`.

Steps 1–3 are independent of the call-site sweep in step 4, which is
mechanical but not safe to split the other way: doing step 4 before
steps 2–3 would make the post-local-change replies drop the change.

## Tests

- A fake client that counts `Layout()` calls: a burst of read requests
  publishes nothing and makes zero socket reads; `afterCommand` makes
  none on the request path.
- Read-your-writes: a write followed at once by a read on the same
  connection sees the write; with two clients and a fake client whose
  layout event arrives late, the writer's `afterCommand` still resolves
  on publish, and the second client sees the change after the publish
  and not before.
- `Server.refresh()`: a stamp publishes a `layout` event at an unchanged
  revision, and a subscriber's re-read sees the new facts.
- The settle skip: a registered waiter wakes on the event, not after
  `settle`.
- Staleness: a closed client turns the next read into the 500
  `current()` used to return, and a read before the first publish gets
  "not ready", never a wrong `session_not_found`.
- Group-2 ordering: two owner changes produce two publishes and two
  ordered sets of events per connection.
- The existing suite is the guard for the rest: `diffModels`' output and
  the methods' answers do not change.
