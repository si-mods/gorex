# The gateway's model

Status: built (#893).

`GoRex rexapi` shows the layout of the app to Rex clients as a model of sessions, windows and blocks (`internal/rexapi/model.go`). The work is tracked in #879. This document describes who builds that model, when, and what a request reads. Before #893 every request rebuilt the model. The design below builds the model once for each change and serves requests from the last built model. A caller still reads its own writes.

## Problem

### Before #893

Two paths rebuilt the model (`internal/rexapi/gateway.go`):

- `pump` listened on the events of the client connection. A burst of events settled for 20 ms, then `reread(layout)` ran. A ticker repeated it every 500 ms on a server that pushes no events. On a server that does push events, the ticker ran every 30 s as a safety net.
- Every request handler called `current()`, which was `reread(true)`. This was a forced read of the layout over the local socket, whatever the events said.

`reread` holds `refreshMu` for its whole run. It does the socket read, `listSessions`, the model build, `diffModels` against the last model, and `sendChanges` to every connection. `refreshMu` exists so that changes reach connections in the order that the gateway read them.

One server detail matters below. In `internal/rex`, `commitLayout` (`server.go`) is the runtime write path of the layout. The `setLayout` op calls it. It sends a `layout` event whenever the bytes differ. But `Server.refresh()` (`layout_life.go`) also writes `s.layout`. It stamps the facts that the sessions sampled (title, directory, program) at the *same* revision. It sends no event on purpose: "the next change takes them along". The per-request `current()` hid this, because any request picked up the newest stamps. The startup writer (in `server.go`) and the migration writer (`migrateLayout` in `projects.go`) run before the server listens. So a subscriber always sees them in its first read.

### Costs

- Every request paid a socket round trip and a model build of about 1,000 lines. It also paid a diff and a fan-out that it did not ask for. This happened across about 30 call sites. The cost was for each request, not for each change.
- `afterCommand` (`internal/rexapi/layout.go`) multiplied the cost. It polled `current()` every 10 ms for up to `commandWait`, which is `rex.CommandTimeout` (6 s) + 1 s. So a slow write could mean about 700 socket reads, builds, diffs and broadcasts for one method call.
- `refreshMu` serialized the request handlers. The reads of N clients waited behind the first read that rebuilt the model.
- A read-only method carried a broadcast side effect. The first request after a change sent every connection its events. Timing decided who fired the fan-out, not the code.
- Per-request reads also masked the missing `refresh()` event above. Stamped facts looked fresher than the event stream actually delivered.

## Design

One goroutine builds and publishes the model. Everything else reads the last published model. The design rests on one invariant, checked against `internal/rex`:

> **Every change to the layout's arrangement is preceded or followed by a `layout` event.**

`commitLayout` is the only runtime writer that mutates the arrangement, and it notifies on every byte difference. The load and migration writers (in `server.go`, and `migrateLayout`) run before subscriptions exist. So a subscriber always sees them in its first read.

The fact stamp is the deliberate exception. `Server.refresh()` (`layout_life.go`) rewrites `s.layout` at the same revision with the facts that the sessions sampled: directory, size, foreground program, and agent session (`stamp` in `layout_apply.go`). It sends no event. An added event would change the wire protocol. A `layout` event always carries a new revision (`commitLayout`). So an event with an unchanged revision would be a new behavior for every subscriber. Also, `docs/irreversible.md` puts the protocol behind a decision process that this doc does not need. The gateway copes on its own side instead:

- The stamp lands at most `refreshWait` (about 1 s) after the `sessions` event that scheduled it (`refreshSoon`, `layout_life.go`).
- On each `sessions` event, `pump` also arms a `facts` timer `factsWait` out. When the timer fires, `pump` runs one `reread(true)`.
- `factsWait` is `refreshWait` plus 200 ms. It is a constant in `gateway.go`, because `refreshWait` is unexported in `internal/rex`.
- The margin is enough. The server arms `refreshSoon` inside `notify`, before it sends the `sessions` event. So its timer always starts before the timer of the gateway. The margin only has to cover the own run of the stamp.
- Stamped facts reach clients at the own cadence of the stamp, about 1 Hz. The cost is at most one layout read each second. The safety tick covers what a quiet session misses.
- The design does not reuse the session list for these facts. `stamp` also writes size and agent-session fields that the session list does not carry.

### pump is the only builder

`reread` stays as it is: the socket read, `listSessions`, the build, the diff and the broadcast, all under `refreshMu`. But only `pump` calls the `reread(true)` form. It does so on layout events, on the `facts` timer armed by `sessions` events, and on the safety tick. A `sessions` event itself still costs only a `reread(false)`, as before.

`refreshMu` keeps its real job. It orders the events sent to connections and guards `raw` and `desktop`. Request handlers do not take it for the model. `sessionsNow()` keeps it for the session list, exactly as before. `sessionsNow` is an etag-gated `ListSince`, not a rebuild. The cost that this doc removes is the layout read and the build, not that list.

Each publish swaps `g.model` (it stays an `atomic.Pointer`), bumps a `seq` counter, and closes the current `changed` channel before it installs a fresh channel. A waiter reads `modelNow` and the channel once, then blocks on the channel. This is the condition-variable pattern, without a condition variable. Waiters also wake when `pump` exits on `Client.Closed()` and when `g.done` closes. So a dead connection never holds a waiter for the full `commandWait`.

### Requests read the snapshot

`current()` is gone. Handlers call `modelReq()`. It returns the last published immutable model (`modelNow()`) behind the two guards below.

Two guards keep a snapshot honest where the old read could fail:

- **Not ready yet**: `New` returns once the hello of the client answers. It does not wait for the first publish, which may be slow on a loaded server. Until the first publish lands, a `ready` flag is unset. Methods that need the model wait for the flag, up to `commandWait`. Then they answer with an existing 500-class code (`internal`) and a "not ready" message. This is a slow answer, not the wrong `session_not_found`, and it adds no error code on the wire. The flag sets on the first publish and never clears.
- **Connection gone**: `pump` returns on `Client.Closed()` and nothing starts it again, because the gateway does not reconnect its client. `pump` marks the gateway as it leaves. A read also checks `Client.Closed()`, so it need not wait for `pump`. Reads that needed the error of `current()` get the same 500 ("reading the layout: the GoRex server is gone") instead of a stale answer. The own failure of `reread` keeps the last model, exactly as before.

### Read your own writes

`current()` gave every caller read-your-writes for free. Snapshots must give it back for each path. The write paths sort into three groups:

1. **Layout commands**: the `g.command(...)` sites (`layout.go`, `projects.go`, `spaces_sync.go`, `movewindow.go`, `mergewindows.go`). There are 37 calls. 30 of them are already followed by `afterCommand(shows)`. The rest are rollbacks and intermediate focus steps that the last `afterCommand` covers. (The focus steps of `session.new_window` run after its `afterCommand`, so it calls `afterCommand(nil)` once more for their revision.)
   - `afterCommand` reads the layout once on the request path. This is a `reread(true)` that also publishes. The server usually applies a command before it answers it, so that one read answers at once. A no-op command sends no layout event and so publishes nothing. It does not wait `commandWait` for a publish that never comes. `afterCommand(nil)` is that read alone.
   - Only when `shows` is still false does the method wait on `changed` until `shows(m)` or `commandWait`.
   - For that wait, `pump` skips the `settle` burst while a waiter is registered. It reads the layout as soon as the event arrives. There is no polling, and there is at most the one read on the request path.

2. **Gateway-local writes that feed the model**: ownership and attachment changes live in the gateway, so no layout event announces them. The sites that rebuild for them keep a **synchronous** `rebuild` (it was `reread(false)` in the first design). It makes no socket read. It rebuilds from `g.raw` with the owners as they are now, and the caller answers from the model that it just published. The sites are:
   - `methodSessionOwnBlock` and `methodSessionDisownBlock` (`layout.go`).
   - The `claimCreated` read in `methodSessionCreate` (`layout.go`).
   - `keepAttached` in `methodGorexMoveWindow` (`movewindow.go`).
   - `cleanupClient` and `cleanupClientSession` (`lease.go`).
   - `resumeControl` and `dropControl` (`gateway.go`).
   - `renewOwned` (`owners.go`, on the `renewLocks` goroutine).
   - `takeOnInput` (`owners.go`, on the input path).

   The last two called no `refresh()` before #893. The next request's `current()` picked up their change. Without a rebuild, their owner changes would go unseen for up to `safetyEvery`. The design considered an asynchronous `kick()` and rejected it for this reason. These callers answer from the model. So a coalesced publish would let a client read its own write as stale. `refreshMu` serializes these rebuilds with the rebuilds of `pump`, which keeps the broadcast order.

3. **Everything else is a pure read** and takes `modelReq()`:
   - `sessionArg`, `attachedArg` and `attachedSessionArg`.
   - `session.list`, `session.attach`, `attach_all` and `detach`, and `status`.
   - The projects, spaces, target, upload, voice and client methods.
   - `splitsOf`. `session.new_split` reads the splits before its command from the same snapshot that its arguments came from.

   `server.stop` has an idle check that is the exception. It needs the layout as it is now, so it still runs `reread(true)`. `sessionsNow()` (`gateway.go`) is unchanged. Some writes change neither the layout nor the model: the generation of `server.config_reload`, upload and download reservations, and block themes (`g.themes` never enters `build`). These need no model path at all.

A second client that reads right after the write of another client sees the model as of the last publish. That model is at most `settle`-plus-event latency old. This is the same staleness window that its own change stream already carries. It is the one semantic change from the old design, where any request forced a fresh read. The window is bounded. The alternative is the per-request rebuild that this doc removes.

### What stays the same

- The mapper of `model.go` and `diffModels`: the model is still rebuilt whole each time. An incremental update on top of the raw layout diff is a possible later step, not part of this one.
- `listSessions` keeps its etag and stays inside `reread`. `sessionsNow` keeps `refreshMu`.
- `watchBlocks`, `pruneOwners` and `pruneAttachments` still run in `reread`, on the publishing side.
- The poll fallback for a server without `Subscribe`: `pump` reads every `pollEvery`, publishes, and waiters wake the same way.
- The wire protocol: no new events and no new error codes. Clients see the same events in the same order. A `layout` event keeps meaning "the revision changed".

## Tests

- A fake client that counts `Layout()` calls: a burst of read requests publishes nothing and makes zero socket reads. `afterCommand` makes one on the request path.
- Read-your-writes: a write followed at once by a read on the same connection sees the write. With two clients and a fake client whose layout event arrives late, the `afterCommand` of the writer still resolves on publish. The second client sees the change after the publish and not before.
- The `facts` timer: a `sessions` event arms it. A publish after about `refreshWait` picks up the facts that `Server.refresh()` stamped. A second `sessions` event inside the window costs no extra layout read.
- The settle skip: a registered waiter wakes on the event, not after `settle`.
- Staleness: a closed client turns the next read into the 500 that `current()` used to return. A read before the first publish gets "not ready", never a wrong `session_not_found`.
- Group-2 ordering: two owner changes produce two publishes and two ordered sets of events for each connection.
- The existing suite is the guard for the rest: the output of `diffModels` and the answers of the methods do not change.

## Phases

1. Gateway plumbing: `seq`, `changed`, the `ready` flag, the closed-connection mark, and the `facts` timer of `pump`. `current()` still exists. Nothing else changes. Built (#893).
2. `afterCommand` waits on `changed`. `pump` skips `settle` while a waiter is registered. Built (#893).
3. Switch the pure-read sites to `modelNow()` and the group-2 sites to a synchronous `reread(false)` (renamed `rebuild`). Remove `current()` and `refresh()`. Built (#893).

Steps 1 and 2 are independent of the call-site sweep in step 3. The sweep is mechanical, but it is not safe to split the other way. If step 3 came first, the replies after a local change would drop the change. Nothing in `internal/rex` or the protocol changes.
