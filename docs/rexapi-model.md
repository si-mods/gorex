# The gateway's model

Status: design. Tracked in #879.

`GoRex rexapi` shows the app's layout to Rex clients as a model of
sessions, windows and blocks (`internal/rexapi/model.go`). This document
describes who builds that model, when, and what a request reads. Today
every request rebuilds it; the design below builds it once per change
and serves requests from the last built one.

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

Three kinds of callers pay for this:

- **Reads** (`sessionArg`, `session.list`, `session.attach`, `status`,
  about thirty sites): they want the model as it is, and turn a failed
  layout read into a 500.
- **After a write** (`afterCommand`, `layout.go:205`): the app answers a
  layout command before the layout event arrives, so the method polls
  `current()` every 10 ms until the model shows the change, up to
  `commandWait`. Each poll is a full socket read, build, diff and
  broadcast — the most expensive loop in the package.
- **After a gateway-local change** (`resumeControl`, `dropControl`,
  ownership moves): owners and watches live in the gateway, not in the
  layout, so no event announces them. The code calls `refresh()` to fold
  them into the model.

## Problems

- Every request pays a socket round trip and a ~1,000-line model build,
  plus a diff and a fan-out it did not ask for. The cost is per request,
  not per change.
- `refreshMu` serializes request handlers: N clients' reads wait behind
  whichever read rebuilds first.
- A read-only method carries a broadcast side effect: the first request
  after a change sends every connection its events. Who fires the
  fan-out depends on timing, not on the code.
- `afterCommand` multiplies all of it: a 10 ms poll of a full rebuild
  for up to `commandWait`.

## Design

One goroutine builds and publishes the model; everything else reads the
last published one.

### pump is the only builder

`reread` stays as it is — the socket read, `listSessions`, the build,
the diff, the broadcast, all under `refreshMu` — but only `pump` calls
it, on layout events, on the safety tick, and on a kick (below).
`refreshMu` keeps its real job, ordering the events sent to
connections; it no longer appears in a request handler.

Each publish swaps `g.model` (it stays an `atomic.Pointer`) and bumps a
`seq` counter. A `changed` channel is closed on each publish and
replaced, so a waiter reads `modelNow` plus the current channel, then
waits on it: the pattern a condition variable gives without one.

### Requests read the snapshot

`current()` goes away. Handlers call `modelNow()` (`gateway.go:442`),
which already returns the last published immutable model.

Two guards keep a snapshot honest where today's read could fail:

- **Not ready yet**: before the first publish a handler sees an empty
  model and would answer `session_not_found` for a session that exists.
  `New` waits for the first publish (bounded by `helloWait`) before it
  returns, so a running gateway is never in this state; a request that
  arrives while the client reconnects waits out the first publish too.
- **Connection gone**: when `pump` leaves on `Client.Closed`, it marks
  the gateway; a read that needed `current()`'s error gets the same 500
  ("no connection to the GoRex server") instead of a stale answer.

`reread`'s own failure keeps the last model, exactly as today.

### Writes wait on the sequence, not on polls

`afterCommand(shows)` becomes:

1. `shows(modelNow())` — often true already, since the app's apply
   answer lands after the layout changed.
2. Otherwise wait on `changed` until `shows` or `commandWait`. The wait
   wakes on the publish the layout event caused: no socket round trips,
   no 10 ms loop, and the answer comes at event time rather than at the
   next poll tick.

### Local changes kick a rebuild

A gateway-local change — an owner taken or released, a client resumed
or dropped, a watch pruned — calls `kick()`, which asks `pump` for a
`reread(false)` through a channel, coalescing kicks the way the burst
timer coalesces events. `reread(false)` already does the right thing:
it skips the socket read, reuses `g.raw`, and rebuilds with the owners
and sessions as they are now. The event stream cannot see these
changes, so the kick replaces the `refresh()` calls in `resumeControl`,
`dropControl` and the ownership paths — and the broadcast still happens
in `pump`, in `reread`'s order.

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

1. Add `seq`, `changed` and the readiness/staleness guards; make `New`
   wait for the first publish; switch the read call sites to
   `modelNow()` and delete `current()`.
2. Rewrite `afterCommand` on `changed`.
3. Replace the remaining `refresh()` calls with `kick()`.

Each step compiles and passes the suite on its own; step 1 carries the
behavior change, steps 2–3 are mechanical.

## Tests

- A fake client that counts `Layout()` calls: a burst of read requests
  makes one publish, not one read per request; `afterCommand` makes none.
- `kick()` coalescing: two owner changes inside one settle window produce
  one rebuild and one ordered set of events per connection.
- Staleness: closing the client turns the next read into the 500
  `current()` used to return.
- The existing suite is the guard for the rest: `diffModels`' output and
  the methods' answers do not change.
