# Web client: several hosts at once

Status: design for si-mods/gorex#472, not built. Part of the umbrella
#331. #474 (stay signed in, restore the place), #475 (desktop layout and
space management) and #477 (uploads) build on it.

## Problem

The web client connects to one host at a time (docs/web-client.md,
Hosts). `web/src/main.ts` keeps one connection in module globals:
`control` (line 52), `live`, `token`, `current` (line 57), `generation`
and `retry`, and one set of caches: `sessions`, `titles`, `programs`,
`statuses`, `seen` and `described`. `web/src/rex.ts` keeps one transport
in module globals too (`transport` and `base`, set by `setHost`), which
`Control.open`, `DataStream.open` and `files.ts` (`hostTransport`,
`apiURL`) read.

So `choose(h)` (line 403) must tear the first host down before it
connects to the next one. It closes the control connection, closes the
shown window and its panes, clears every cache and points `setHost` at
the new host. The first host's programs keep running, but the page
forgets its sessions, titles and statuses. A program that waits for the
user on the first host shows nothing.

The desktop app and Rex Go do not work this way. The desktop keeps a
`hostConn` (`hosts.go`) per saved host, all connected at once, and lists
their tabs in one sidebar. Rex Go keeps a `Host` (`ios/GoRex/Rex/Host.swift`)
per host in its `HostStore`, all connected, and groups their windows by
the desktop's spaces (`ios/GoRex/Rex/Spaces.swift`).

## Goal

- One page keeps a connection to every host the user connects:
  direct hosts and hosts found by built-in Tailscale, at once.
- Switching from a window of host A to a window of host B leaves A
  connected. A's titles, program tiles and attention dots keep updating.
- A host that goes offline affects only its own rows and panes. The
  other hosts work on. When it comes back, the page attaches again to
  the same sessions and windows. It never makes a window or a session
  to replace one it lost.
- The sidebar gets, per host, the data and the state it needs to show
  every host's windows, by host or by the desktop's spaces across hosts.
- Switching back to a window shown a moment ago is instant: the last
  four windows shown stay live, as the desktop keeps a space's panes
  (#179).
- Every pane, download and upload knows its host. No call reaches a host
  through "the current connection".

## Non-goals

- Several clients on one host. That works today and does not change.
- One browser tab per host as the answer. The page manages all hosts.
- The sidebar's layout: the views (by host, by space), which one is the
  default, the setting that chooses and its control, folding, the space
  chip. A separate sidebar design, in progress, decides those. This
  design defines only what the sidebar needs from the multi-host model
  (The sidebar: what it needs).
- Keeping every window ever shown live. The page keeps the last four
  (Kept windows).
- Hiding the second host of one machine. When one machine is two hosts,
  the page shows both (Terms).
- Space management (make, rename, move, close a space) and tab
  placement. Those are #475.
- Persistent Tailscale sign-in. That is #474. This design says what it
  keeps for #474 to use.

## Terms

The gateway shows a host's desktop layout in Rex's terms
(docs/rex-api.md, How it works):

| Rex API | GoRex desktop | Web client today |
|---|---|---|
| session | space | a heading in the sidebar |
| window | tab (a project tab) | a row in the sidebar; the stage shows one |
| block | pane with a terminal | a pane (`TerminalView`) |

A **host** is one gateway the page reaches: one `Host` of
`web/src/hosts.ts`, direct or tailnet. A **machine** is the computer
behind it, named by `machine_id` from `GET /api/spaces`
(`internal/rexapi/spaces.go`). One machine can be two hosts in the list,
for example by its LAN address and by its tailnet name. The page shows
both hosts, each with all its windows, so the machine's windows show
twice. It does not merge them or hide one: that is the simplest rule,
and the user chose it (Decisions, 3). The two names in the sidebar tell
the user which address each copy uses.

Session, window and block IDs are the gateway's own. Two hosts can give
the same IDs, and they always do when they are the same machine. So the
page names every session, window and block with its host:
`(host, session)`, `(host, session, window)`, `(host, block)`.

## Design

### State: the page's and each host's

Today almost all state is one global per page. After this change, the
state of a connection is in one object per host, `HostConn`, in a new
file `web/src/hostconn.ts`. It is the web's version of the desktop's
`hostConn` and Rex Go's `Host`.

| State | Owner | Now in |
|---|---|---|
| Ghostty module `g`, fonts, theme (`chosen`, `chosenEntry`), `prefs` | page | `main.ts`, `prefs.ts` |
| Built-in Tailscale node (`Tailnet`), its guide | page | `main.ts`, `tailnet.ts` |
| Host list (`HostList`), Settings dialog, welcome | page | `main.ts`, `hosts.ts`, `settings.ts` |
| The stage: the shown window (`shown`), the notice, the status line | page | `main.ts` |
| Kept windows: the three shown before `shown`, live (`kept`) | page | (new) |
| The selection: which host's window the stage shows | page | `current` and `shown` in `main.ts` |
| Keyboard shortcuts, scheme, `applyPrefs` | page | `main.ts`, `keys.ts` |
| Transport (direct or tailnet) | host | `transport`, `base` in `rex.ts` |
| Control connection, `live`, reconnect token, retry timer, attempt count | host | `control`, `live`, `token`, `retry`, `generation` in `main.ts` |
| Link state and problem | host | `link`, `problem` in `main.ts` |
| Sessions and their views, the read in progress (`Session.reading`) | host | `sessions` in `main.ts` |
| Titles, programs, statuses, seen records, `described` | host | the maps in `main.ts` |
| A window made here and not seen yet (`wantWindow`) | host | `main.ts` |
| Machine ID and remote blocks from `/api/spaces` | host | (new) |
| Uploads and their progress (#477) | host | (new) |

`HostConn` in outline:

```ts
export class HostConn {
  readonly host: Host;                    // as hosts.ts or tailnet.ts gives it
  readonly transport: Transport;          // made once per HostConn
  control?: Control;
  live = false;                           // sessions read since control opened
  state: LinkState = "connecting";        // settings.ts's type
  problem?: Problem;
  attempt = 0;                            // for the backoff
  machine?: string;                       // machine_id, from /api/spaces, for spaces.ts
  remote = new Map<string, RemoteBlock[]>(); // by session, from /api/spaces
  readonly sessions = new Map<string, Session>();
  readonly titles = new Map<string, string>();
  readonly programs = new Map<string, string | undefined>();
  readonly statuses = new Map<string, Status[]>();
  readonly seen = new Map<string, Set<string>>();
  /** The window the stage showed last on this host, for host and space keys. */
  lastWindow?: { session: string; window: string };

  constructor(host: Host, transport: Transport, events: HostEvents);
  start(): void;      // connect, and again after a drop, until stop()
  stop(): void;       // close control, cancel the retry; caches stay
  poke(): void;       // the next attempt now (online, visible, Retry)
  call(method: string, payload?: any): Promise<{ seq: number; payload: any }>;
  newWindow(session: string): Promise<string | undefined>;
  closeWindow(session: string, window: string): Promise<void>;
}

export interface HostEvents {
  state(c: HostConn): void;                  // link state or problem changed
  sessions(c: HostConn): void;               // a session came, went or was renamed
  view(c: HostConn, session: string): void;  // a session's view was applied
  block(c: HostConn, block: string): void;   // a title, program or status changed
}
```

What moves from `main.ts` into `HostConn`, with the same code:

- `connect(gen)` becomes `HostConn.connect()`, with the generation as a
  field. It opens `Control.open(this.transport, this.token)`, runs
  `session.bootstrap` with `event_scope: "all"`, then `list()`.
- `event()`, `list()`, `refresh()`, `apply()`, `readTitle()`,
  `readProgram()` and `recordsOf()` move as they are. The part of
  `apply()` that updates the shown window moves out: `apply()` calls
  `events.view(this, s.id)`, and the page updates `shown` when it shows
  that host's session.
- `newWindow()` and `closeWindow()` become methods. The page keeps
  `wantWindow`, now with its host.

What stays in `main.ts`, changed to take a host:

- `show(conn, session, window, keys)` and `hide()`. `show()` takes a
  kept `WindowView` when it has one; `hide()` keeps the window it hides
  (Kept windows).
- `run(name)`: the window's commands use `shown.conn`, never a global.
  `next-window` and `previous-window` go through the windows of every
  connected host in sidebar order.
- `see()`: it marks records seen on the shown window's host only.
- The sidebar (`sidebar()`, `sidebarBlock()`, `fillWindow()`,
  `windowTitle()`, `tilesRoom()`). These take a `HostConn`. They move to
  a new `web/src/sidebar.ts`, because `main.ts` is already 1000 lines and
  the separate sidebar design builds its views there (The sidebar: what
  it needs).

### Transport per connection (`rex.ts`)

The module globals `transport` and `base`, and `setHost`,
`hostTransport` and `apiURL`, go. A connection keeps the transport it
was opened with:

- `Control.open(transport: Transport, token?: string)`. The control
  keeps it as `readonly transport`.
- `new DataStream(transport)`. `open(ticket)` opens the socket on that
  transport.
- `Transport` gets an optional `url(path)`, which only
  `directTransport` has. It replaces `apiURL` for the download frame in
  `files.ts`.

The gateway binds a data ticket to the client record that asked for it
(`ticket.record` in `internal/rexapi/gateway.go`). A ticket from host A's
control works only on host A's `/api/data`. So the rule is: **a data
stream, a download or an upload uses the transport of the `Control` that
issued its ticket or names its block.** The code gets this by taking the
transport from that `Control` object (`c.transport`), not from the page.

### How a window and a pane find their host

`WindowView` (`web/src/window.ts`) gets its host when it is made:
`new WindowView(conn, session, window, stage, windowHost(conn))`. It
keeps `readonly conn`. The sidebar and `run()` read the host from it.

`windowHost(conn)` in `main.ts` builds the `WindowHost` for that window.
It replaces the one `host` object of today (line 157):

```ts
function windowHost(conn: HostConn): WindowHost {
  return {
    control: () => conn.control,
    live: () => (conn.live ? conn.control : undefined),
    titleOf: (b) => conn.titles.get(b) || t("sidebarTerminal"),
    programOf: (b) => programOf(conn.programs.get(b)),
    focusBlock: (w, block) => conn.call("session.focus_block", { session_id: w.session, block_id: block }),
    resizeSplit: (w, s, ratio) => { /* as today, on conn */ },
    // g, chosen, status, scheme, action: the page's, as today
  };
}
```

`PaneHost` (`web/src/pane.ts`) does not change. `this.app.control()`
and `this.app.live()` keep their meaning, but now they answer the
pane's own host, which is fixed when the pane is made. There is no
"current connection" a pane could pick up after the user switched
windows. A kept pane of host A gets no keys while the stage shows a
window of host B: the keyboard goes only to the shown window's panes,
and a kept pane does not own its block (Kept windows).

The one change in `pane.ts` is in `start()`:
`const d = new DataStream(c.transport)`.

**Pane input stays per pane.** #473 (IME and paste, in progress in
`web/src/pane.ts` and `web/src/keys.ts`) works on one pane: its keydown,
composition and paste handlers end in `type()` and `write()`, which call
`this.app.live()`. That keeps working unchanged. This design adds
nothing to those handlers. The global keydown listener in `main.ts`
(line 941) keeps taking only the chords of `keys.ts`. The PR that
changes `pane.ts` (PR 1) changes one line, so whichever of PR 1 and #473
merges second rebases over a small conflict.

**Files** (`web/src/files.ts`). `FileHost.control()` is already the
pane's own (`pane.ts` passes `this.app.control()`). `begin()` returns the
`Control` it used with the ticket, and `navigate()`, `saveTailnet()`,
`saveBlob()` and `fetchImage()` take `c.transport`, not
`hostTransport()`. The viewer's download button asks the same pane's
host again.

**Uploads (#477).** The upload starts from a pane: a drop on its
canvas, a file picker from its header, or an image pasted into it. It
captures `const c = this.app.control()` once, at the start, and every
step uses `c` and `c.transport`: `POST /api/upload?block_id=…` through
`c.transport.fetch`, or `com.superlogical.terminal.upload_begin` on `c`
and its `rex.data.bytes.v1` stream on `new DataStream(c.transport)`. The
upload belongs to its `HostConn`, not to the shown window. Switching to
a window of another host does not move it, stop it or send the file to
the other host. This is what the desktop does (an upload's transfers
belong to its session, #285). When `c` closes before the upload ends,
the upload fails with an error. It does not go again on the new
connection, because the server may have taken part of it.

### Lifecycle

#### Which hosts connect

As the desktop and Rex Go do, every host the user keeps connects when
the page starts (Decisions, 1):

- every direct host in `gorex.hosts`, except those the user
  disconnected (`gorex.hostsOff`, Persistence);
- every tailnet host the user connected once (`gorex.tailnetOpened`),
  when built-in Tailscale finds it. This is the desktop's
  `savedHost.TailnetOpened`: a found host waits as "available" until the
  user opens it the first time (`hostAvailable` in `hosts.go`).

A tailnet host found but never opened shows in the host list with
**Connect**, as today, and does not connect by itself.

#### Connecting and reconnecting

Each `HostConn` runs its own loop, as `connect()` does today:

1. `Control.open(transport, token)`. The hello carries the host's own
   reconnect token, so a host back within the gateway's `reconnectWait`
   (60 s, `internal/rexapi/gateway.go`) gets its client record back,
   with the blocks it owned.
2. `session.bootstrap`, `session.list`, and `session.attach` for each
   session (`refresh(id, true)`). Then `live = true`.
3. If the shown window is on this host, its panes `start()` again (new
   tickets) and `resume()` what was typed while offline.

On a failure, the host is diagnosed (`diagnose(url)` or
`tailnet.diagnose(h)`), its state is `unreachable` with the problem, and
the next attempt comes after a backoff. The waits grow per host, from
3 s up to 30 s, as the desktop's `hostBackoff`. Today it is a fixed 3 s
or 4 s, which with many hosts down means many diagnoses every few
seconds. `poke()` makes the next attempt now. The page pokes every host
that is not connected on the window's `online` event, when the page
becomes visible again, and from a host's **Retry**.

The page staggers the first connections: at most four `Control.open`
calls at once, the same limit the tailnet probes use (`probesAtOnce` in
`tailnet.ts`). Over the tailnet each one is a TLS handshake in the
engine, on the page's thread.

#### Background hosts

A host whose window is not shown keeps its control connection and its
`event_scope: "all"` subscription. It gets `session_created`,
`session_destroyed`, `session_label_changed`, `session_view_changed`
and the block events (`title_changed`, `process_changed`,
`program_status_changed`, `program_status_removed`), and updates its
caches as the shown host does. So its sidebar rows keep their titles,
program tiles, progress rings and attention dots.

It has no data streams, except those of its kept windows (below). A
title that changes many times a second (Claude Code's spinner) costs one
small event and one row update. `sidebarBlock()` already updates only
the rows that show the block. The sidebar also batches those updates to
one per animation frame, because there are now more hosts sending them.

#### Kept windows

The desktop keeps a space's panes live when its window shows another
space (#179). `park()` (`workspace.go:1262`) moves the window's tabs into
the space's `kept`, terminals attached, and showing the space again
takes them back as they were (`show()`, `workspace.go:1218`). The
desktop sets no count: it keeps every space the window left. A space's
kept panes close (`unkeep()`, `workspace.go:49`) only when they cannot
be right any more: another client changed the space's layout
(`workspace.go:561`), the space is removed (`workspace.go:742`), the
connection to the session server comes back (`unkeepAll`,
`workspace.go:904`, "Kept panes' streams were the old connection's"),
the window closes (`workspace.go:1199`), or a host is removed
(`removeHost`, `hosts.go:1263`).

The web keeps the same idea with a count, because each pane costs more
in a browser tab than in the desktop app (below): **the page keeps the
last four windows shown live**, the shown one and the three shown before
it, across all hosts. `kept` in `main.ts` lists them, most recently
shown first.

- `hide()` keeps the window it hides: `WindowView.park()` takes its
  element off the stage, stops painting, and disowns each block
  (`session.disown_block`, as `close(true)` does today), so the desktop
  sizes it again. The data streams stay open. Each terminal goes on
  reading its bytes. The window goes on updating from its host's view
  (`events.view`), as the shown one does.
- `show()` of a kept window puts it back: each pane takes its block
  again (`claim()`, `session.own_block` with `take`), sends its size and
  paints. There is no new ticket and no snapshot, and the scroll position
  is as the user left it.
- When a fifth window is shown, the one shown least recently is evicted:
  `WindowView.close()`, its panes `close(false)` (already disowned), their
  streams closed and their terminals freed.
- A kept window is also evicted, as on the desktop, when its window is
  gone from its session's view, when its host drops (its tickets were
  that connection's, as `unkeepAll`), and when its host is disconnected,
  removed or edited. A kept pane whose block is gone closes, as in the
  shown window.
- A window that was evicted, or was never kept, shows as today: a new
  `WindowView`, and each pane gets a ticket and opens its stream, which
  starts with the snapshot.
- The shown window is never evicted. When its host drops, it keeps its
  last frame as today (Offline, per host).

The cost of a kept pane is one data stream (a browser WebSocket, or a
TLS connection over the tailnet engine), one libghostty-vt terminal with
its scrollback in WebAssembly memory, and its canvas. A busy program in
a kept pane makes the page parse its output on the page's thread,
although nothing paints. Four windows of a few panes each stay far below
the browser's socket caps (Limits). The count is a constant
(`keptWindows = 4`), not a setting. The PR that adds it measures the
heap (Migration plan, PR 4).

#### Offline, per host

A drop or a failure touches only its own `HostConn`:

- Its state and its rows change (The sidebar: what it needs). Its
  caches stay, so its windows stay in the sidebar as they were last
  seen.
- Its kept windows are evicted (Kept windows).
- If the stage shows one of its windows, the panes keep their last
  frame. Typed bytes wait in each pane's queue (4 MB, as today). A short
  drop shows only in the status line. A diagnosed problem shows the
  notice card, as today, with the host's name, **Retry** and **Edit**.
- Other hosts do not see it. No global changes: there is no global
  `control`, `live` or `link` left to change.

#### Back again, with no new windows

When a host comes back:

- The page reads `session.list` and attaches to each session again. The
  windows are the host's own windows, with the same IDs. The shown
  window updates in place (`WindowView.update`). Panes of blocks that
  still exist reopen their streams. Panes of gone blocks close.
- The connect path never calls `session.create`, `session.new_window` or
  `session.new_split`. Only the user does, by a click or a shortcut.
- A creation call that failed because the connection dropped (`closed`
  or `timeout`) is not sent again. The server may have run it. The next
  `session.list` shows whether it did. `wantWindow` for that host is
  dropped on a drop.
- New window and split buttons of a host that is not live are disabled,
  so the user cannot queue a creation for later.
- `seen` records stay in the cache, so a record the user saw before the
  drop does not come back as new.

#### Removing and editing a host

- **Remove** stops its `HostConn`, closes its shown and kept windows,
  removes its rows, and deletes the host from
  `gorex.hosts`. The selection moves to the last shown window of another
  connected host, else the welcome.
- **Disconnect** stops its `HostConn` and adds it to `gorex.hostsOff`.
  It closes its shown and kept windows. It stays in the host list with
  **Connect**. Its cached sessions are cleared, because they are not
  updated any more.
- **Edit** of the address makes a new `HostConn` for that host only (a
  new transport). Other hosts do not reconnect. Edit of the name or icon
  only redraws.

#### Limits

| Resource | Per host | Whole page |
|---|---|---|
| Control connection | 1 | one per connected host |
| Data streams | the panes of its windows among the four live ones | the panes of the shown window and the three kept |
| Memory | the session views (JSON) and the maps: tens of KB | plus one libghostty-vt terminal and canvas per pane of the four live windows |
| Timers | one retry timer | |
| Tailnet | one TLS connection over the engine's netstack per socket | all on one node, one page thread |

- **Number of hosts.** The page sets no hard cap. Above 8 connected
  hosts, the host list shows a note that many connections cost battery
  and startup time (Decisions, 5). "Connected" here counts every host
  the page keeps a `HostConn` for, connected or retrying: a host that
  retries costs its attempts and diagnoses too. Only the hosts the user
  disconnected do not count.
- **Browser WebSocket limits.** Browsers cap open WebSockets per
  profile (Firefox's `network.websocket.max-connections` is 200 by
  default; Chrome's cap is near 255). Browsers also open only one
  WebSocket handshake at a time to one address (RFC 6455, 4.1), so a
  host's panes connect one after another, as today. The page stays far
  below the caps: one control per host and one stream per pane of the
  four live windows. These caps are not checked in this repository;
  PRs 3 and 4 measure the real count in DevTools.
- **Built-in Tailscale.** `web/tailnet/conn.go` (`jsSocket`, `dialTLS`)
  dials a new TLS connection for each socket, through the one node's
  netstack. Nothing in the engine limits how many; many hosts at once
  works. The cost is the TLS handshakes in WebAssembly on the page's
  thread, which the stagger above spreads, and the DERP bandwidth that
  all hosts share. Tailnet sockets are not browser WebSockets, so the
  browser caps do not apply to them.
- **Hidden tabs.** Browsers slow the timers of hidden tabs (Chrome: at
  most once a second, and once a minute after 5 minutes hidden). The
  retry waits get longer, which is acceptable; the `visibilitychange`
  poke makes up for it. WebSocket messages still arrive. Whether the
  tailnet engine answers the gateway's keepalive (every 120 s, 60 s to
  answer) while the tab is hidden for long is not known (Risks, 4).

### The sidebar: what it needs

How the sidebar lays out the windows is not part of this design. A
separate sidebar design, in progress, decides the views (by host, by
the desktop's spaces, as the desktop's Settings › Appearance › Sidebar
view and Rex Go's Settings › Grouping), which one is the default, the
setting that chooses and its control, folding, and the space chip. This
section defines only what the multi-host model gives the sidebar, and
the rules that hold in any view.

#### Data per host

Each `HostConn` gives, read only, and announces changes through
`HostEvents`:

- `host`: its ID, name, icon and kind (direct or tailnet). The host
  list's order is direct hosts, then tailnet hosts.
- `sessions`: each session's label and view: its windows in order, with
  their blocks and layout.
- `titles`, `programs`, `statuses` and `seen`, per block: a row's title,
  program tiles, progress ring and attention dot, as `fillWindow` reads
  them today.
- `lastWindow`: the window shown last on the host, for a click on the
  host and for the space keys.
- `machine` and `remote`, from `/api/spaces` (Spaces across hosts).

The page gives the shown window `(host, session, window)`, the kept
windows (Kept windows) and `wantWindow`.

#### State per host

- `state` and `problem`: `connecting`, `connected` or `unreachable`
  (`LinkState` in `settings.ts`), and the diagnosed problem. With
  `attempt`, they give a host's state dot as the desktop's
  `hostSectionIcon` does: none while connected or on the first attempt,
  `--attention` while it reconnects (`connecting` with `attempt > 0`),
  `--danger` when the last attempt failed. The dot's state is its
  `title` and part of the host's `aria-label`.
- The host panel and Settings › Hosts follow another rule, because there
  the dot stands in the check mark's place and every row has one: green
  (`--ok`) while connected, `--attention` while connecting, the first
  attempt too, `--danger` when the last attempt failed (its title adds
  the problem's), grey while the user disconnected it. A tailnet host the
  page keeps no connection to shows Tailscale's dot for the peer
  (online, offline, refused) in its address line instead, and none in
  the check's place: one dot a row. The lists redraw only when a host's
  state or problem changes, not at each retry, and keep the focus.
- `live`: whether the host's sessions were read since its control
  opened. New window and split buttons of a host that is not live are
  disabled.
- Disconnected by the user (`gorex.hostsOff`): no rows, and **Connect**.
- A tailnet host while built-in Tailscale is not running: it waits; the
  sidebar says "Sign in to Tailscale" and opens the guide (#474 removes
  most of these).

#### An offline host

In any view, as decided (Decisions, 7):

- One muted line for the host says the problem's title ("Can't reach
  the host", "The host doesn't trust this page") with **Retry** and
  **Edit**, as buttons with the `rotate-ccw` and `pencil` icons.
- Its window rows stay, from the last view, drawn as inactive: the CSS
  form of DESIGN.md's `dimmed` (grey, then faded to plain + 0.15),
  `filter: grayscale(1); opacity: 0.65`, in one `.dimmed` class. They
  can still be clicked: the stage shows the cached layout and the
  host's notice. The user sees what will come back.
- A host that never connected since the page loaded has no rows: only
  the host and the problem line.

All `data-*` keys that find a row carry the host:
`.window[data-host="…"][data-window="…"]`.

#### Spaces across hosts

A desktop space can hold tabs of other hosts. Each gateway shows its own
panes only, and `GET /api/spaces` says which panes of other machines
each of its spaces holds (docs/rex-api.md, Spaces across hosts). Rex Go
merges them with `CrossHost` (`ios/GoRex/Rex/Spaces.swift`). The web
ports it as `web/src/spaces.ts`, with the same rules, so that a spaces
view has the data. The view itself is the sidebar design's.

- Each `HostConn` reads `/api/spaces` through `c.transport.fetch` when
  its welcome lists `gorex.spaces`: after it connects, and again when a
  `session_view_changed` brings a new revision. It keeps `machine` and
  `remote`. `CrossHost` needs `machine` to find which host a remote
  block (`{machine, block_id}`) is on.
- `spaces.ts` gives, as `CrossHost` does, the windows of other hosts a
  session shows (`rows`), whether a window shows in another host's
  space instead (`isMoved`), and whether a session is no space of its
  own (`isGone`). B's "Desktop terminals" space
  (`gorex-desktop-terminals` in `internal/rexapi/model.go`) usually
  empties this way.
- A remote block resolves to the first host of its machine in the list,
  as in `CrossHost`. The page does not hide the machine's other host:
  that host lists its windows in its own sessions (Terms).
- A window of a host that is not connected, or not added, is left out.

#### Host list and Settings

- The host button at the top of the sidebar (`HostList`) names the host
  of the shown window. Its panel lists every host with its state dot in
  place of today's check mark. A click on a connected host shows its
  last window. A click on a host that is not connected connects it. Each
  row has **Edit** (`pencil`) and a **Connect** or **Disconnect** item.
  Disconnect uses Lucide `unplug`, added to `assets/icons` from lucide.
- Settings › Hosts lists the same, with the same items. The host card
  at the top of Settings (`SettingsApp.link()`) shows the shown
  window's host. `link()` takes the host as an argument.
- `?host=URL` adds the host, connects it and shows its first window.
  The other hosts stay connected.

#### Look and icons

- No new surface paints its own background. The page keeps its
  background. New popovers use the frosted panel of `.theme-panel`
  (`backdrop-filter: blur(24px)`), the CSS form of `paintFrost` that the
  host panel and the Settings dialog already use.
- Inactive rows use the one `.dimmed` class above, not ad hoc opacities.
  Per DESIGN.md, "not where color is the meaning": the state dot of a
  host is never dimmed.
- Every icon is a Lucide SVG from `assets/icons`, drawn with
  `icon(name)` from `web/src/hosts.ts`. No Unicode glyph stands in for
  an icon (no ✓, ×, ▾ or +). Icons that are missing (`unplug`) are
  copied from lucide into `assets/icons`.

#### Keyboard

The keys go in the sidebar's order, whichever view shows it. The
sidebar module gives that order to `run()`.

- **Next and previous window** (Alt+Shift+] and Alt+Shift+[, ⌥⇧] and
  ⌥⇧[ on a Mac) go through the windows in the sidebar's order across
  every connected host. They skip hosts that are not connected and
  windows the view does not show.
- **Next and previous space** are new. They show the next space's last
  shown window, in the sidebar's order, across hosts. The desktop's
  keys are ⌃⌘] and ⌃⌘[ on a Mac, Ctrl+Shift+PageDown and PageUp
  elsewhere. Browsers keep Ctrl+Shift+PageDown and PageUp to move
  their tabs, so the web takes Alt+Shift+PageDown and PageUp there, as
  it takes Alt+Shift chords for the other keys that browsers keep
  (docs/web-client.md, Shortcuts). On a Mac it takes the desktop's
  ⌃⌘] and ⌃⌘[.
- The desktop's Show Space 1 to 9 (Ctrl+Alt+digit off the Mac) clashes
  with AltGr on many keyboards, so it waits for editable shortcuts
  (#476).
- Settings › Keyboard lists the new keys (`shortcutGroups()` in
  `keys.ts`).

### Persistence

| Key | Now | After |
|---|---|---|
| `gorex.hosts` | the direct hosts | unchanged |
| `gorex.host` | the chosen host's ID | read once into `gorex.place`, then removed |
| `gorex.place` | (new) | `{host, session, window}` of the shown window |
| `gorex.hostsOff` | (new) | the IDs of direct hosts the user disconnected |
| `gorex.tailnetOpened` | (new) | the `ts:<StableNodeID>` IDs of tailnet hosts the user connected once |

All reads and writes keep today's `try`/`catch`: without storage, the
page works and forgets when it closes. Reset All Settings removes every
`gorex.*` key, these too. The sidebar design adds its own keys (the
view, folded hosts); they are not part of this design.

When the page starts, it connects the hosts above and shows the place
from `gorex.place` as soon as that host's first `session.list` has that
window. If the host does not connect, or the window is gone, the stage
waits on that host's notice until the user chooses something else. The
page does not jump to another window after the user has chosen one.

**#474.** Today the tailnet node is ephemeral: after a reload, no
tailnet host is found until the user signs in again. With this design,
the tailnet hosts in `gorex.tailnetOpened` and a tailnet host in
`gorex.place` still show, as hosts that wait for Tailscale, and connect
on their own once the user signs in. When #474 adds a persistent node
(**Stay signed in**), they connect at once on reload with no other
change here. #474's "restore the work position" reads `gorex.place`;
it may add the focused block to it. Two tabs or PWA windows share
`localStorage`: each writes its own place, and the last one wins, which
is acceptable. The Tailscale tab lock (docs/web-tailscale.md, State) is
#474's.

## Migration plan

Six PRs, in order. Each one builds, ships and is tested on its own.
PRs 1 and 2 change no behaviour.

### Test setup

The repository has no web test framework and no CI job for `web/`
(`.github/workflows/ci.yml` runs Go only). PR 3 adds `node --test` for
modules with no DOM (Decisions, 8). Everything else is checked in a
browser against scratch hosts, with the dev server. Never use the
user's data directory or installed GoRex: set `GOREX_DIR` to a short new
path, and stop only the processes you started.

```sh
# Git Bash. Build the worktree's binary once.
T=/c/t; mkdir -p $T && go build -o $T/gorex.exe .

# Host A: the desktop app on its own data directory runs its session server;
# the gateway listens on a spare port, unauthenticated, for this machine only.
export GOREX_DIR=$T/a
go tool mygo dev &                                   # or $T/gorex.exe
$T/gorex.exe rexapi web allow http://127.0.0.1:5173
$T/gorex.exe rexapi --listen http://127.0.0.1:7481

# Host B: the same, with GOREX_DIR=$T/b and port 7482.

# The page.
cd web && npm run dev                                # http://127.0.0.1:5173
# Add hosts by quick link:
#   http://127.0.0.1:5173/?host=http://127.0.0.1:7481
#   http://127.0.0.1:5173/?host=http://127.0.0.1:7482

# The unit tests (from PR 3).
cd web && npm test
```

A data directory takes one gateway: a second `gorex rexapi` on it
stops ("another Rex API gateway runs on this data directory"). More
hosts of the same machine are more addresses of its one gateway: a port
that forwards to it (a small TCP proxy), or `localhost` beside
`127.0.0.1`. Use them for the limits and to see one machine as two
hosts. Count a
host's sessions with `$T/gorex.exe rexapi call session.list` (same
`GOREX_DIR`) before and after a reconnect, to check that no session or
window was made. Report what was checked in the PR, with screenshots of
the sidebar.

### PR 1: `refactor(web): give each connection its own transport`

- `rex.ts`: `Control.open(transport, token)` and `Control.transport`;
  `new DataStream(transport)`; `Transport.url?`. Remove `setHost`,
  `hostTransport`, `apiURL` and the module's `transport` and `base`.
- `pane.ts`: `new DataStream(c.transport)`, one line.
- `files.ts`: the transport of the `Control` that gave the ticket.
- `main.ts`: keeps today's one connection; `choose()` makes the
  transport and passes it to `Control.open`.

Verify: `npm run build` (type check). On host A: panes open, type,
split, resize a divider, Ctrl+click a file path (download) and an image
path (viewer). Choose host B: the same. With built-in Tailscale, if a
tailnet host is at hand: a pane and a download over it.

### PR 2: `refactor(web): move a host's connection into HostConn`

- New `web/src/hostconn.ts` with the state and functions listed above.
- New `web/src/sidebar.ts` with today's sidebar functions, unchanged but
  for taking a `HostConn`. If the sidebar design has made
  `sidebar.ts` first, this PR uses it instead.
- `WindowView` gets `conn`; `windowHost(conn)` replaces the `host`
  object. Sidebar `data-*` keys carry the host.
- `main.ts` keeps a `Map<string, HostConn>`, but `choose()` still stops
  every other `HostConn`: one host at a time, as today. The backoff
  replaces the fixed waits.

Verify: all of PR 1. Stop host B's gateway while its window shows and
start it again within 60 s: the panes come back, bytes typed meanwhile
arrive once, `session.list` is the same. Again after more than 60 s: a
new client, the panes attach again, no new window.

### PR 3: `feat(web): keep several hosts connected at once`

The core of #472.

- Start every host (Which hosts connect). Choosing a window of another
  host no longer stops a `HostConn`.
- The sidebar gets every host's data and state (The sidebar: what it
  needs): rows keyed by host, state dots, problem lines and `.dimmed`
  rows of an offline host. If the sidebar design's views have not landed,
  this PR lists each connected host's sessions in today's form under the
  host's name, as a stopgap those views replace. The notice for the
  shown window's host only.
- The stagger, the `online` and `visibilitychange` pokes, the batched
  sidebar updates.
- Next and previous window across hosts; next and previous space.
- `gorex.place` and `gorex.tailnetOpened`. The migration of `gorex.host`
  into `gorex.place` is a pure function in a new `web/src/place.ts`.
- The unit tests: `npm test` in `web/package.json` runs a new
  `web/test.mjs`, which bundles each `web/test/*.test.ts` with esbuild
  (already a dev dependency; the sources import without extensions, so
  Node cannot load them as they are) and runs the bundles with
  `node --test`. The first cases cover the `gorex.host` migration: no
  key, a direct host, a tailnet host, a host no longer in the list,
  storage that throws.
- CI: a new `web` job in `.github/workflows/ci.yml`, on `instabox-medium`
  with the same `if` as `build`: `actions/setup-node` (Node 24), then
  `npm ci` and `npm test` in `web/`.
- docs/web-client.md: Hosts ("Only the chosen host is connected" goes),
  Split panes, Shortcuts, How it works.

Verify, with hosts A and B:

1. The sidebar shows both hosts' spaces and windows. DevTools › Network
   › WS shows two control sockets and one data socket per shown pane.
2. Show a window of A. In a pane of B (B's desktop app), run
   `printf '\033]0;from-b\007'`: B's row shows the title. Run
   `printf '\033]7501;state=blocked:kind=question\033\\'`: B's row shows
   the attention dot.
3. Stop B's gateway. B's dot turns, its rows dim, its problem line
   shows. A's panes take typing. Click a dimmed row of B: the stage
   shows its cached layout and B's notice. Start B's gateway: B comes
   back; its `session.list` count is unchanged; no new tab in B's app.
4. Stop B's app and gateway; reload the page: A connects and shows its
   place; B shows its problem line with no rows.
5. Reload with the place on B while B is up: the page shows B's window.
6. A second address of A's gateway: both hosts show A's windows.
7. Five addresses of A's gateway: the page stays responsive at
   startup; note the heap in DevTools › Memory against two hosts.
8. `npm test` passes locally, and the PR's `web` job passes.

### PR 4: `feat(web): keep the last four windows live`

- `kept` in `main.ts`, `WindowView.park()`, and `show()` and `hide()`
  as in Kept windows. The evictions: the fifth window shown, a window
  gone, a host dropped, disconnected, removed or edited.

Verify, with hosts A and B:

1. Show five windows in turn, of both hosts. DevTools › Network › WS:
   the first window's data sockets close when the fifth shows; the
   others stay open.
2. Show the second window again: no new data socket, no snapshot; the
   scrollback position is as left. Its panes take typing.
3. While a window of A is kept, its blocks are disowned: A's desktop app
   sizes them to its own panes again. Show it again: the page sizes
   them.
4. Run `yes` in a pane of a kept window for a minute, then show it: the
   terminal shows the latest output. The page stays responsive.
5. Stop B's gateway: B's kept windows close (their sockets close). Show
   one again after B comes back: it attaches from a snapshot.
6. Heap in DevTools › Memory with four windows of four panes each,
   against one window. Record it in the PR.

### PR 5: `feat(web): manage hosts while connected`

- The host panel and Settings › Hosts: state dots, Connect and
  Disconnect, Remove, Edit, as above. `gorex.hostsOff`.
- `SettingsApp.link(host)`. The note above 8 connected hosts.

Verify: disconnect B (its sockets close in DevTools, its windows leave
the sidebar, A goes on); connect it again; edit B's address to a wrong
port (only B reconnects and fails); remove B while its window shows (the
stage moves to A); reload (B stays disconnected).

### PR 6: `feat(web): read the desktop's spaces across hosts`

- `web/src/spaces.ts`: read `/api/spaces` per host, and the `CrossHost`
  port (`rows`, `isMoved`, `isGone`) for the sidebar design's spaces
  view. This PR adds no view.
- Unit tests: the `CrossHost` cases of
  `ios/GoRexTests/SpacesTests.swift` (`testSpaceTakesOtherHostsWindows`,
  `testSessionGone`, `testOnlyHostsTheAppReaches`, `testEachWindowOnce`,
  `testMachineAddedTwice`), ported to `web/test/spaces.test.ts` and run
  by PR 3's runner and CI job.

Verify: `npm test`. With a real pair of machines, where one desktop's
space holds a tab of the other: the grouping places that tab in the
space, under the other host, and empties that host's "Desktop
terminals". The check in the sidebar goes with the sidebar design's
spaces view.

### After these PRs

Not part of #472, listed for order:

- #477 (uploads) can start after PR 2: it needs only the pane's own
  `Control` and transport.
- #474 (stay signed in, restore the place) after PR 3: it uses
  `gorex.place` and `gorex.tailnetOpened`.
- The sidebar design can build on `HostConn` after PR 2, takes every
  host's data from PR 3, and needs PR 6 for a spaces view.
- #475 (space management, tab placement) after PR 6 and the sidebar
  design.

## Risks

1. **One machine through two hosts.** A machine added by its LAN address
   and found by Tailscale is two hosts with the same IDs. Its windows
   show twice, by choice (Decisions, 3). The user may type into one copy
   and see the other change, which is correct but may surprise. Only the
   shown window's panes own their blocks, so the two copies do not fight
   over a block's size. Each kept copy holds its own streams, so two
   kept copies of one window cost twice.
2. **Block size.** Each shown pane takes its block (`session.own_block`,
   `take`). That is as today. A kept window holds no block: `park()`
   disowns its blocks before it leaves the stage. A bug here would size
   a terminal on a desktop the user is not looking at.
3. **Event load.** `event_scope: "all"` on many hosts with busy titles
   sends many events. The per-frame batch keeps the sidebar cheap; the
   socket traffic is small. Not measured.
4. **Hidden tabs over the tailnet.** The engine runs on the page's
   thread. When a tab is hidden for a long time, the browser slows it,
   and the gateway may drop the connection after its keepalive wait.
   This happens today with one host; with several it happens to all
   tailnet hosts. The reconnect on `visibilitychange` covers it.
5. **Conflict with #473.** PR 1 changes one line of `pane.ts`, near
   `start()`, away from the input handlers. The second to merge rebases.
6. **Trust.** Each host must trust the page's origin
   (`gorex rexapi web allow`). One page now holds live connections to
   all of them at once, so a compromised page reaches all of them at
   once. That was already true of any one host; the page's hosting and
   CSP (docs/web-client.md, Hosting) stay the defence.
7. **Kept windows' load.** A kept pane parses its program's output on
   the page's thread while hidden. Several busy kept panes can slow the
   shown one. PR 4 measures it; if it is too slow, the count goes down,
   or a kept pane pauses its stream and attaches from a snapshot when
   shown.
8. **Two designs on the sidebar.** This design and the sidebar design
   both touch `web/src/sidebar.ts`. PR 2 and PR 3 keep to the data and
   states above, and leave the layout to the sidebar design.

## Decisions

1. **Which hosts connect when the page starts.** Every saved direct
   host and every tailnet host connected once, with Disconnect per host,
   as the desktop and Rex Go do.
2. **The sidebar view.** Deferred to the separate sidebar design. This
   design defines only what the sidebar needs per host.
3. **One machine through two hosts.** Show both, duplicates and all. It
   is the simplest rule; the page does not dedupe by `machine_id`.
4. **Windows not shown.** Keep the last four windows shown live, as the
   desktop keeps a space's panes (#179); older ones are evicted and
   attach from a snapshot when shown again.
5. **How many hosts.** No cap; a note above 8 connected hosts.
6. **Keys for spaces.** Alt+Shift+PageDown and PageUp (⌃⌘] and ⌃⌘[ on a
   Mac); Show Space 1 to 9 waits for #476.
7. **An offline host's windows.** Keep the last known rows, dimmed and
   clickable, with the host's notice on the stage.
8. **Tests for pure code.** `node --test` for modules with no DOM, run
   in CI from PR 3: the `gorex.host` migration (PR 3) and the
   `CrossHost` port, with cases from `ios/GoRexTests/SpacesTests.swift`
   (PR 6).
