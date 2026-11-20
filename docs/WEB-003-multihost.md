---
name: WEB-003
description: "The web client keeps connections to several hosts at once. Use when you change hosts, connections or the lifecycle of the web client."
metadata:
  created: "2026-11-13"
  updated: "2026-11-20"
---

# WEB-003: Web client: several hosts at once

Status: built (#533, #534, #536, #537, #538, #539).

This doc describes how the web client keeps a connection to several hosts at once (issue #472, part of the umbrella issue #331). The design PR is #531. Issue #474 (stay signed in, restore the place), issue #475 (desktop layout and space management) and issue #477 (uploads) build on this work.

## Problem

The web client connects to one host at a time (docs/REF-007-web-client.md, Hosts). `web/src/main.ts` keeps one connection in module globals: `control`, `live`, `token`, `current`, `generation` and `retry`. It also keeps one set of caches: `sessions`, `titles`, `programs`, `statuses`, `seen` and `described`. `web/src/rex.ts` also keeps one transport in module globals (`transport` and `base`, set by `setHost`). `Control.open`, `DataStream.open` and `files.ts` (`hostTransport`, `apiURL`) read that transport.

So `choose(h)` in `main.ts` must close the first host before it connects to the next host. It closes the control connection. It closes the shown window and its panes. It clears every cache. It points `setHost` at the new host. The programs on the first host keep running, but the page forgets its sessions, titles and statuses. A program on the first host can wait for the user. The page then shows nothing about it.

The desktop app and Rex Go do not work this way. The desktop keeps a `hostConn` (`hosts.go`) for each saved host. All hosts are connected at once, and the desktop lists their tabs in one sidebar. Rex Go keeps a `Host` (`ios/GoRex/Rex/Host.swift`) for each host in its `HostStore`. All hosts are connected. Rex Go groups their windows by the spaces of the desktop (`ios/GoRex/Rex/Spaces.swift`).

## Goal

- One page keeps a connection to every host that the user connects. This includes direct hosts and hosts that built-in Tailscale finds, at the same time.
- The user switches from a window of host A to a window of host B. Host A stays connected. The titles, program tiles and attention dots of host A keep updating.
- A host that goes offline affects only its own rows and panes. The other hosts keep working. When the host comes back, the page attaches again to the same sessions and windows. The page never makes a window or a session to replace one that it lost.
- The sidebar gets, for each host, the data and the state that it needs. With this data, the sidebar shows the windows of every host, by host or by the spaces of the desktop across hosts.
- The user switches back to a window that the page showed a moment ago, and the switch is instant. The last four windows shown stay live, as the desktop keeps the panes of a space (#179).
- Every pane, download and upload knows its host. No call reaches a host through "the current connection".

## Non-goals

- Several clients on one host. This works today and does not change.
- One browser tab for each host as the answer. The page manages all hosts.
- The layout of the sidebar. This includes the views (by host, by space), the default view, the setting that chooses the view and its control, folding, and the space chip. A separate sidebar design, in progress, decides those. This design defines only what the sidebar needs from the multi-host model (The sidebar: what it needs).
- Keeping every window that the page ever showed live. The page keeps the last four (Kept windows).
- Hiding the second host of one machine. When one machine is two hosts, the page shows both (Terms).
- Space management (make, rename, move, close a space) and tab placement. Those are #475.
- Persistent Tailscale sign-in. That is #474. This design says what it keeps for #474 to use.

## Design

### Terms

The gateway shows the desktop layout of a host in Rex terms (docs/REF-005-rex-api.md, How it works):

| Rex API | GoRex desktop | Web client today |
|---|---|---|
| session | space | a heading in the sidebar |
| window | tab (a project tab) | a row in the sidebar. The stage shows one |
| block | pane with a terminal | a pane (`TerminalView`) |

A **host** is one gateway that the page reaches. It is one `Host` of `web/src/hosts.ts`, direct or tailnet. A **machine** is the computer behind a host. `machine_id` from `GET /api/spaces` (`internal/rexapi/spaces.go`) names it. One machine can be two hosts in the list, for example by its LAN address and by its tailnet name. The page shows both hosts, each with all its windows, so the windows of the machine show twice. The page does not merge them and does not hide one. This is the simplest rule, and the user chose it (Decisions, 3). The two names in the sidebar tell the user which address each copy uses.

The IDs of sessions, windows and blocks belong to the gateway. Two hosts can give the same IDs. They always do when they are the same machine. So the page names every session, window and block with its host: `(host, session)`, `(host, session, window)`, `(host, block)`.

### State: the page's and each host's

Today almost all state is one global for each page. After this change, one object for each host holds the state of a connection. The object is `HostConn`, in a new file `web/src/hostconn.ts`. It is the web version of the `hostConn` of the desktop and the `Host` of Rex Go.

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

This code shows `HostConn` in outline:

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

These parts move from `main.ts` into `HostConn`, with the same code:

- `connect(gen)` becomes `HostConn.connect()`, with the generation as a field. It opens `Control.open(this.transport, this.token)`. It runs `session.bootstrap` with `event_scope: "all"`. Then it runs `list()`.
- `event()`, `list()`, `refresh()`, `apply()`, `readTitle()`, `readProgram()` and `recordsOf()` move as they are. The part of `apply()` that updates the shown window moves out. `apply()` calls `events.view(this, s.id)`. The page updates `shown` when it shows the session of that host.
- `newWindow()` and `closeWindow()` become methods. The page keeps `wantWindow`, now with its host.

These parts stay in `main.ts`, changed to take a host:

- `show(conn, session, window, keys)` and `hide()`. `show()` takes a kept `WindowView` when it has one. `hide()` keeps the window that it hides (Kept windows).
- `run(name)`. The commands of the window use `shown.conn`, never a global. `next-window` and `previous-window` go through the windows of every connected host in sidebar order.
- `see()`. It marks records seen on the host of the shown window only.
- The sidebar (`sidebar()`, `sidebarBlock()`, `fillWindow()`, `windowTitle()`, `tilesRoom()`). These take a `HostConn`. They move to a new `web/src/sidebar.ts`. The reason is that `main.ts` is already 1000 lines, and the separate sidebar design builds its views there (The sidebar: what it needs).

### Transport per connection (`rex.ts`)

The module globals `transport` and `base` go. `setHost`, `hostTransport` and `apiURL` also go. A connection keeps the transport that it was opened with:

- `Control.open(transport: Transport, token?: string)`. The control keeps the transport as `readonly transport`.
- `new DataStream(transport)`. `open(ticket)` opens the socket on that transport.
- `Transport` gets an optional `url(path)`, which only `directTransport` has. It replaces `apiURL` for the download frame in `files.ts`.

The gateway binds a data ticket to the client record that asked for it (`ticket.record` in `internal/rexapi/gateway.go`). A ticket from the control of host A works only on `/api/data` of host A. So the rule is as follows. **A data stream, a download or an upload uses the transport of the `Control` that issued its ticket or names its block.** The code takes the transport from that `Control` object (`c.transport`), not from the page.

### How a window and a pane find their host

`WindowView` (`web/src/window.ts`) gets its host when the page makes it: `new WindowView(conn, session, window, stage, windowHost(conn))`. It keeps `readonly conn`. The sidebar and `run()` read the host from it.

`windowHost(conn)` in `main.ts` builds the `WindowHost` for that window. It replaces the one `host` object of the code before this work:

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

`PaneHost` (`web/src/pane.ts`) does not change. `this.app.control()` and `this.app.live()` keep their meaning. They now answer the own host of the pane, which is fixed when the page makes the pane. A pane has no "current connection" to pick up after the user switches windows. A kept pane of host A gets no keys while the stage shows a window of host B. The keyboard goes only to the panes of the shown window, and a kept pane does not own its block (Kept windows).

The one change in `pane.ts` is in `start()`: `const d = new DataStream(c.transport)`.

**Pane input stays per pane.** Issue #473 (IME and paste, in progress in `web/src/pane.ts` and `web/src/keys.ts`) works on one pane. Its keydown, composition and paste handlers end in `type()` and `write()`, which call `this.app.live()`. That keeps working unchanged. This design adds nothing to those handlers. The global keydown listener in `main.ts` keeps taking only the chords of `keys.ts`. The PR that changes `pane.ts` (PR 1) changes one line. The PR that merges second, PR 1 or #473, rebases over a small conflict.

**Files** (`web/src/files.ts`). `FileHost.control()` is already the own control of the pane (`pane.ts` passes `this.app.control()`). `begin()` returns the `Control` that it used with the ticket. `navigate()`, `saveTailnet()`, `saveBlob()` and `fetchImage()` take `c.transport`, not `hostTransport()`. The download button of the viewer asks the host of the same pane again.

**Uploads (#477).** The upload starts from a pane. The user drops a file on its canvas, picks a file from its header, or pastes an image into it. The upload captures `const c = this.app.control()` once, at the start. Every step uses `c` and `c.transport`. One path is `POST /api/upload?block_id=…` through `c.transport.fetch`. The other path is `com.superlogical.terminal.upload_begin` on `c`, with its `rex.data.bytes.v1` stream on `new DataStream(c.transport)`. The upload belongs to its `HostConn`, not to the shown window. The user can switch to a window of another host. Then the upload does not move, does not stop and does not send the file to the other host. The desktop does the same (the transfers of an upload belong to its session, #285). When `c` closes before the upload ends, the upload fails with an error. It does not run again on the new connection, because the server may have taken part of it.

### Lifecycle

#### Which hosts connect

As the desktop and Rex Go do, every host that the user keeps connects when the page starts (Decisions, 1):

- every direct host in `gorex.hosts`, except the hosts that the user disconnected (`gorex.hostsOff`, Persistence)
- every tailnet host that the user connected once (`gorex.tailnetOpened`), when built-in Tailscale finds it

The second rule is the `savedHost.TailnetOpened` of the desktop. A host that Tailscale found waits as "available" until the user opens it the first time (`hostAvailable` in `hosts.go`).

A tailnet host that Tailscale found and the user never opened shows in the host list with **Connect**, as today. It does not connect by itself.

#### Connecting and reconnecting

Each `HostConn` runs its own loop, as `connect()` does today:

1. `Control.open(transport, token)`. The hello carries the own reconnect token of the host. A host that comes back within the `reconnectWait` of the gateway (60 s, `internal/rexapi/gateway.go`) gets its client record back, with the blocks that it owned.
2. `session.bootstrap`, `session.list`, and `session.attach` for each session (`refresh(id, true)`). Then `live = true`.
3. If the shown window is on this host, its panes run `start()` again (new tickets). They also run `resume()` for what the user typed while offline.

When a connection fails, the page diagnoses the host (`diagnose(url)` or `tailnet.diagnose(h)`). The state of the host is `unreachable` with the problem. The next attempt comes after a backoff. The waits grow for each host, from 3 s up to 30 s, as the `hostBackoff` of the desktop. Today the wait is a fixed 3 s or 4 s. With many hosts down, this means many diagnoses every few seconds. `poke()` makes the next attempt now. The page pokes every host that is not connected. It does this on the `online` event of the window, when the page becomes visible again, and from the **Retry** of a host.

The page staggers the first connections. It makes at most four `Control.open` calls at once. This is the same limit that the tailnet probes use (`probesAtOnce` in `tailnet.ts`). Over the tailnet, each call is a TLS handshake in the engine, on the thread of the page.

#### Background hosts

A host whose window is not shown keeps its control connection and its `event_scope: "all"` subscription. It gets `session_created`, `session_destroyed`, `session_label_changed`, `session_view_changed` and the block events (`title_changed`, `process_changed`, `program_status_changed`, `program_status_removed`). It updates its caches as the shown host does. So its sidebar rows keep their titles, program tiles, progress rings and attention dots.

It has no data streams, except the streams of its kept windows (below). A title that changes many times a second (the spinner of Claude Code) costs one small event and one row update. `sidebarBlock()` already updates only the rows that show the block. The sidebar also batches those updates to one for each animation frame, because more hosts now send them.

#### Kept windows

The desktop keeps the panes of a space live when its window shows another space (#179). `App.park` in `workspace.go` moves the tabs of the window into the `kept` of the space, with the terminals attached. When the window shows the space again, `App.show` takes them back as they were. The desktop sets no count. It keeps every space that the window left. The kept panes of a space close (`Workspace.unkeep` in `workspace.go`) only when they cannot be right any more. These are the cases:

- Another client changed the layout of the space (`store.reconcileOn`).
- The space is removed (`store.remove`).
- The connection to the session server comes back (`store.adopt`, which calls `unkeepAll`, "Kept panes' streams were the old connection's").
- The window closes (`store.closed`).
- A host is removed (`App.removeHost` in `hosts.go`).

The web keeps the same idea with a count, because each pane costs more in a browser tab than in the desktop app (below). **The page keeps the last four windows shown live.** These are the shown window and the three shown before it, across all hosts. `kept` in `main.ts` lists them, most recently shown first.

- `hide()` keeps the window that it hides. `WindowView.park()` takes its element off the stage, stops painting, and disowns each block (`session.disown_block`, as `close(true)` does today), so the desktop sizes it again. The data streams stay open. Each terminal keeps reading its bytes. The window keeps updating from the view of its host (`events.view`), as the shown window does.
- `show()` of a kept window puts it back. Each pane takes its block again (`claim()`, `session.own_block` with `take`), sends its size and paints. There is no new ticket and no snapshot. The scroll position is as the user left it.
- When the page shows a fifth window, it evicts the window that it showed least recently. It runs `WindowView.close()`. The panes run `close(false)` (already disowned). Their streams close and their terminals are freed.
- The page also evicts a kept window, as the desktop does. The cases are these. Its window is gone from the view of its session. Its host drops (its tickets were the tickets of that connection, as `unkeepAll`). Its host is disconnected, removed or edited. A kept pane whose block is gone closes, as in the shown window.
- A window that the page evicted, or never kept, shows as today. The page makes a new `WindowView`. Each pane gets a ticket and opens its stream, which starts with the snapshot.
- The page never evicts the shown window. When its host drops, the window keeps its last frame as today (Offline, per host).

A kept pane costs three things. The first is one data stream (a browser WebSocket, or a TLS connection over the tailnet engine). The second is one libghostty-vt terminal with its scrollback in WebAssembly memory. The third is its canvas. A busy program in a kept pane makes the page parse its output on the thread of the page. Nothing paints. Four windows of a few panes each stay far below the socket caps of the browser (Limits). The count is a constant (`keptWindows = 4`), not a setting. The PR that adds it measures the heap (Phases, PR 4).

#### Offline, per host

A drop or a failure touches only its own `HostConn`:

- Its state and its rows change (The sidebar: what it needs). Its caches stay, so its windows stay in the sidebar as the page last saw them.
- Its kept windows are evicted (Kept windows).
- If the stage shows one of its windows, the panes keep their last frame. The bytes that the user types wait in the queue of each pane (4 MB, as today). A short drop shows only in the status line. A diagnosed problem shows the notice card, as today, with the name of the host, **Retry** and **Edit**.
- Other hosts do not see the drop. No global changes. No global `control`, `live` or `link` is left to change.

#### Back again, with no new windows

When a host comes back:

- The page reads `session.list` and attaches to each session again. The windows are the own windows of the host, with the same IDs. The shown window updates in place (`WindowView.update`). The panes of blocks that still exist open their streams again. The panes of blocks that are gone close.
- The connect path never calls `session.create`, `session.new_window` or `session.new_split`. Only the user calls them, by a click or a shortcut.
- A creation call can fail because the connection dropped (`closed` or `timeout`). The page does not send that call again, because the server may have run it. The next `session.list` shows whether the server ran it. The page drops `wantWindow` for that host on a drop.
- The new window and split buttons of a host that is not live are disabled, so the user cannot queue a creation for later.
- `seen` records stay in the cache, so a record that the user saw before the drop does not come back as new.

#### Removing and editing a host

- **Remove** stops its `HostConn`, closes its shown and kept windows, removes its rows, and drops the host from `gorex.hosts`. The selection moves to the last shown window of another connected host, or else to the welcome.
- **Disconnect** stops its `HostConn` and adds it to `gorex.hostsOff`. It closes its shown and kept windows. The host stays in the host list with **Connect**. The page clears its cached sessions, because nothing updates them any more.
- **Edit** of the address makes a new `HostConn` for that host only (a new transport). Other hosts do not reconnect. Edit of the name or icon only redraws.

#### Limits

| Resource | Per host | Whole page |
|---|---|---|
| Control connection | 1 | one per connected host |
| Data streams | the panes of its windows among the four live ones | the panes of the shown window and the three kept |
| Memory | the session views (JSON) and the maps: tens of KB | plus one libghostty-vt terminal and canvas per pane of the four live windows |
| Timers | one retry timer | |
| Tailnet | one TLS connection over the netstack of the engine per socket | all on one node, one page thread |

- **Number of hosts.** The page sets no hard cap. Above 8 connected hosts, the host list shows a note that many connections cost battery and startup time (Decisions, 5). Here "connected" counts every host that the page keeps a `HostConn` for, connected or retrying. A host that retries costs its attempts and diagnoses too. Only the hosts that the user disconnected do not count.
- **Browser WebSocket limits.** Browsers cap the open WebSockets for each profile. The `network.websocket.max-connections` of Firefox is 200 by default. The cap of Chrome is near 255. Browsers also open only one WebSocket handshake at a time to one address (RFC 6455, 4.1). So the panes of a host connect one after another, as today. The page stays far below the caps, with one control for each host and one stream for each pane of the four live windows. This repository does not check these caps. PRs 3 and 4 measure the real count in DevTools.
- **Built-in Tailscale.** `web/tailnet/conn.go` (`jsSocket`, `dialTLS`) dials a new TLS connection for each socket, through the netstack of the one node. Nothing in the engine limits how many connections it dials. Many hosts at once work. The cost is the TLS handshakes in WebAssembly on the thread of the page, which the stagger above spreads. The cost also includes the DERP bandwidth that all hosts share. Tailnet sockets are not browser WebSockets, so the browser caps do not apply to them.
- **Hidden tabs.** Browsers slow the timers of hidden tabs. Chrome runs them at most once a second, and once a minute after 5 minutes hidden. The retry waits get longer. This is acceptable, and the `visibilitychange` poke makes up for it. WebSocket messages still arrive. Nobody knows whether the tailnet engine answers the keepalive of the gateway while the tab is hidden for a long time. The gateway sends it every 120 s and waits 60 s for the answer (Risks, 4).

### The sidebar: what it needs

How the sidebar lays out the windows is not part of this design. A separate sidebar design, in progress, decides these points. The views are by host and by the spaces of the desktop. They match Settings › Appearance › Sidebar view of the desktop and Settings › Grouping of Rex Go. The design also decides the default view and the setting that chooses it. It also decides the control of the setting, folding, and the space chip. This section defines only what the multi-host model gives the sidebar, and the rules that hold in any view.

#### Data per host

Each `HostConn` gives this data, read only. It announces changes through `HostEvents`:

- `host`: its ID, name, icon and kind (direct or tailnet). The order of the host list is direct hosts, then tailnet hosts.
- `sessions`: the label and the view of each session. The view has its windows in order, with their blocks and layout.
- `titles`, `programs`, `statuses` and `seen`, for each block: the title of a row, its program tiles, its progress ring and its attention dot, as `fillWindow` reads them today.
- `lastWindow`: the window shown last on the host, for a click on the host and for the space keys.
- `machine` and `remote`, from `/api/spaces` (Spaces across hosts).

The page gives the shown window `(host, session, window)`, the kept windows (Kept windows) and `wantWindow`.

#### State per host

- `state` and `problem`: `connecting`, `connected` or `unreachable` (`LinkState` in `settings.ts`), and the diagnosed problem. With `attempt`, they give the state dot of a host, as the `hostSectionIcon` of the desktop does. The dot is none while the host is connected or on the first attempt. It is `--attention` while the host reconnects (`connecting` with `attempt > 0`). It is `--danger` when the last attempt failed. The state of the dot is its `title` and part of the `aria-label` of the host.
- The host panel and Settings › Hosts follow another rule. The dot there stands in the place of the check mark, and every row has one. The dot is green (`--ok`) while the host is connected. It is `--attention` while the host is connecting. This includes the first attempt. It is `--danger` when the last attempt failed (its title adds the problem). It is grey while the user keeps the host disconnected. A tailnet host that the page keeps no connection to shows another dot. This is the dot of Tailscale for the peer (online, offline, refused), in its address line. It shows no dot in the place of the check mark. So each row has one dot. The lists redraw only when the state or the problem of a host changes, not at each retry, and they keep the focus.
- `live`: whether the page read the sessions of the host since its control opened. The new window and split buttons of a host that is not live are disabled.
- Disconnected by the user (`gorex.hostsOff`): no rows, and **Connect**.
- A tailnet host while built-in Tailscale is not running: it waits. The sidebar says "Sign in to Tailscale" and opens the guide (#474 removes most of these cases).

#### An offline host

In any view, as decided (Decisions, 7):

- One muted line for the host says the title of the problem ("Can't reach the host", "The host doesn't trust this page"). It has **Retry** and **Edit**, as buttons with the `rotate-ccw` and `pencil` icons.
- Its window rows stay, from the last view, drawn as inactive. The style is the CSS form of the `dimmed` of DESIGN.md (grey, then faded to plain + 0.15): `filter: grayscale(1); opacity: 0.65`, in one `.dimmed` class. The user can still click them. The stage then shows the cached layout and the notice of the host. The user sees what will come back.
- A host that never connected since the page loaded has no rows. It shows only the host and the problem line.

All `data-*` keys that find a row carry the host: `.window[data-host="…"][data-window="…"]`.

#### Spaces across hosts

A desktop space can hold tabs of other hosts. Each gateway shows its own panes only. `GET /api/spaces` says which panes of other machines each of its spaces holds (docs/REF-005-rex-api.md, Spaces across hosts). Rex Go merges them with `CrossHost` (`ios/GoRex/Rex/Spaces.swift`). The web ports it as `web/src/spaces.ts`, with the same rules, so that a spaces view has the data. The sidebar design owns the view itself.

- Each `HostConn` reads `/api/spaces` through `c.transport.fetch` when its welcome lists `gorex.spaces`. It reads after it connects, and again when a `session_view_changed` brings a new revision. It keeps `machine` and `remote`. `CrossHost` needs `machine` to find which host has a remote block (`{machine, block_id}`).
- `spaces.ts` gives, as `CrossHost` does, three results. `rows` are the windows of other hosts that a session shows. `isMoved` says whether a window shows in the space of another host instead. `isGone` says whether a session is no space of its own. The "Desktop terminals" space of host B (`gorex-desktop-terminals` in `internal/rexapi/model.go`) usually empties this way.
- A remote block resolves to the first host of its machine in the list, as in `CrossHost`. The page does not hide the other host of the machine. That host lists its windows in its own sessions (Terms).
- The page leaves out a window of a host that is not connected, or not added.

#### Host list and Settings

- The host button at the top of the sidebar (`HostList`) names the host of the shown window. Its panel lists every host with its state dot in the place of today's check mark. A click on a connected host shows its last window. A click on a host that is not connected connects it. Each row has **Edit** (`pencil`) and a **Connect** or **Disconnect** item. Disconnect uses the Lucide `unplug` icon, which the team added to `assets/icons` from lucide.
- Settings › Hosts lists the same hosts, with the same items. The host card at the top of Settings (`SettingsApp.link()`) shows the host of the shown window. `link()` takes the host as an argument.
- `?host=URL` adds the host, connects it and shows its first window. The other hosts stay connected.

#### Look and icons

- No new surface paints its own background. The page keeps its background. New popovers use the frosted panel of `.theme-panel` (`backdrop-filter: blur(24px)`). It is the CSS form of `paintFrost`, which the host panel and the Settings dialog already use.
- Inactive rows use the one `.dimmed` class above, not ad hoc opacities. DESIGN.md says "not where color is the meaning". So the page never dims the state dot of a host.
- Every icon is a Lucide SVG from `assets/icons`, drawn with `icon(name)` from `web/src/hosts.ts`. No Unicode glyph stands in for an icon (no ✓, ×, ▾ or +). The team copies icons that are missing (`unplug`) from lucide into `assets/icons`.

#### Keyboard

The keys go in the order of the sidebar, in whichever view shows it. The sidebar module gives that order to `run()`.

- **Next and previous window** (Alt+Shift+] and Alt+Shift+[, ⌥⇧] and ⌥⇧[ on a Mac) go through the windows in the order of the sidebar, across every connected host. They skip hosts that are not connected and windows that the view does not show.
- **Next and previous space** are new. They show the last shown window of the next space, in the order of the sidebar, across hosts. The keys of the desktop are ⌃⌘] and ⌃⌘[ on a Mac, and Ctrl+Shift+PageDown and PageUp elsewhere. Browsers keep Ctrl+Shift+PageDown and PageUp to move their tabs. So the web takes Alt+Shift+PageDown and PageUp there. It takes Alt+Shift chords for the other keys that browsers keep in the same way (docs/REF-007-web-client.md, Shortcuts). On a Mac, it takes the ⌃⌘] and ⌃⌘[ of the desktop.
- The Show Space 1 to 9 of the desktop (Ctrl+Alt+digit off the Mac) clashes with AltGr on many keyboards. So it waits for editable shortcuts (#476).
- Settings › Keyboard lists the new keys (`shortcutGroups()` in `keys.ts`).

### Persistence

| Key | Now | After |
|---|---|---|
| `gorex.hosts` | the direct hosts | unchanged |
| `gorex.host` | the chosen host's ID | read once into `gorex.place`, then removed |
| `gorex.place` | (new) | `{host, session, window}` of the shown window |
| `gorex.hostsOff` | (new) | the IDs of direct hosts the user disconnected |
| `gorex.tailnetOpened` | (new) | the `ts:<StableNodeID>` IDs of tailnet hosts the user connected once |

All reads and writes keep today's `try`/`catch`. Without storage, the page works and forgets when it closes. Reset All Settings removes every `gorex.*` key, these keys too. The sidebar design adds its own keys (the view, folded hosts). They are not part of this design.

When the page starts, it connects the hosts above. It shows the place from `gorex.place` as soon as the first `session.list` of that host has that window. Sometimes the host does not connect, or the window is gone. Then the stage waits on the notice of that host until the user chooses something else. The page does not jump to another window after the user chose one.

**#474.** Today the tailnet node is ephemeral. After a reload, no tailnet host is found until the user signs in again. With this design, the tailnet hosts in `gorex.tailnetOpened` and a tailnet host in `gorex.place` still show, as hosts that wait for Tailscale. They connect on their own once the user signs in. When #474 adds a persistent node (**Stay signed in**), they connect at once on reload, with no other change here. The "restore the work position" of #474 reads `gorex.place`. It may add the focused block to it. Two tabs or PWA windows share `localStorage`. Each writes its own place, and the last one wins, which is acceptable. The Tailscale tab lock (docs/WEB-002-tailscale.md, State) belongs to #474.

### Risks

1. **One machine through two hosts.** A machine that the user added by its LAN address and that Tailscale found is two hosts with the same IDs. Its windows show twice, by choice (Decisions, 3). The user may type into one copy and see the other change. This is right but may surprise the user. Only the panes of the shown window own their blocks, so the two copies do not fight over the size of a block. Each kept copy holds its own streams, so two kept copies of one window cost twice.
2. **Block size.** Each shown pane takes its block (`session.own_block`, `take`). That is as today. A kept window holds no block, because `park()` disowns its blocks before the window leaves the stage. A bug here would size a terminal on a desktop that the user does not look at.
3. **Event load.** `event_scope: "all"` on many hosts with busy titles sends many events. The per-frame batch keeps the sidebar cheap. The socket traffic is small. Nobody measured it.
4. **Hidden tabs over the tailnet.** The engine runs on the thread of the page. When a tab is hidden for a long time, the browser slows it, and the gateway may drop the connection after its keepalive wait. This happens today with one host. With several hosts, it happens to all tailnet hosts. The reconnect on `visibilitychange` covers it.
5. **Conflict with #473.** PR 1 changes one line of `pane.ts`, near `start()`, away from the input handlers. The second PR to merge rebases.
6. **Trust.** Each host must trust the origin of the page (`gorex rexapi web allow`). One page now holds live connections to all of them at once, so a compromised page reaches all of them at once. That was already true of any one host. The hosting of the page and the CSP (docs/REF-007-web-client.md, Hosting) stay the defence.
7. **Load of kept windows.** A kept pane parses the output of its program on the thread of the page while it is hidden. Several busy kept panes can slow the shown pane. PR 4 measures it. If it is too slow, the count goes down. As an alternative, a kept pane pauses its stream and attaches from a snapshot when shown.
8. **Two designs on the sidebar.** This design and the sidebar design both touch `web/src/sidebar.ts`. PR 2 and PR 3 keep to the data and states above, and leave the layout to the sidebar design.

### Decisions

1. **Which hosts connect when the page starts.** Every saved direct host and every tailnet host connected once, with Disconnect for each host, as the desktop and Rex Go do.
2. **The sidebar view.** The separate sidebar design decides it. This design defines only what the sidebar needs for each host.
3. **One machine through two hosts.** Show both, duplicates and all. It is the simplest rule. The page does not dedupe by `machine_id`.
4. **Windows not shown.** Keep the last four windows shown live, as the desktop keeps the panes of a space (#179). The page evicts older windows. They attach from a snapshot when the page shows them again.
5. **How many hosts.** No cap. The page shows a note above 8 connected hosts.
6. **Keys for spaces.** Alt+Shift+PageDown and PageUp (⌃⌘] and ⌃⌘[ on a Mac). Show Space 1 to 9 waits for #476.
7. **The windows of an offline host.** Keep the last known rows, dimmed and clickable, with the notice of the host on the stage.
8. **Tests for pure code.** `node --test` for modules with no DOM, run in CI from PR 3. The tests cover the `gorex.host` migration (PR 3) and the `CrossHost` port, with cases from `ios/GoRexTests/SpacesTests.swift` (PR 6).

## Tests

The repository has no web test framework and no CI job for `web/` (`.github/workflows/ci.yml` runs Go only). PR 3 adds `node --test` for modules with no DOM (Decisions, 8). The team checks everything else in a browser against scratch hosts, with the dev server. Never use the data directory of the user or the installed GoRex. Set `GOREX_DIR` to a short new path. Stop only the processes that you started.

```sh
# Git Bash. Build the worktree's binary once.
T=/c/t; mkdir -p $T && go build -o $T/gorex.exe .

# Host A: the desktop app on its own data directory runs its session server;
# the gateway listens on a spare port, unauthenticated, for this machine only.
export GOREX_DIR=$T/a
go tool mygo dev -background &                      # or $T/gorex.exe
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

A data directory takes one gateway. A second `gorex rexapi` on it stops ("another Rex API gateway runs on this data directory"). More hosts of the same machine are more addresses of its one gateway. One way is a port that forwards to it (a small TCP proxy). Another way is `localhost` beside `127.0.0.1`. Use them for the limits and to see one machine as two hosts. Count the sessions of a host with `$T/gorex.exe rexapi call session.list` (same `GOREX_DIR`) before and after a reconnect. This checks that no session or window was made. Report what you checked in the PR, with screenshots of the sidebar.

The Phases section lists the checks for each PR.

## Phases

The work has six PRs, in order. Each PR builds, ships and has its own tests. PRs 1 and 2 change no behaviour.

### PR 1: `refactor(web): give each connection its own transport`

- `rex.ts`: `Control.open(transport, token)` and `Control.transport`. `new DataStream(transport)`. `Transport.url?`. Remove `setHost`, `hostTransport`, `apiURL` and the `transport` and `base` of the module.
- `pane.ts`: `new DataStream(c.transport)`, one line.
- `files.ts`: the transport of the `Control` that gave the ticket.
- `main.ts`: keeps today's one connection. `choose()` makes the transport and passes it to `Control.open`.

Check: `npm run build` (type check). On host A: panes open, type, split and resize a divider. Ctrl+click a file path (download) and an image path (viewer). Choose host B and repeat. With built-in Tailscale, if a tailnet host is at hand: check a pane and a download over it.

### PR 2: `refactor(web): move a host's connection into HostConn`

- New `web/src/hostconn.ts` with the state and functions listed above.
- New `web/src/sidebar.ts` with the sidebar functions of today, unchanged except that they take a `HostConn`. If the sidebar design made `sidebar.ts` first, this PR uses it instead.
- `WindowView` gets `conn`. `windowHost(conn)` replaces the `host` object. The `data-*` keys of the sidebar carry the host.
- `main.ts` keeps a `Map<string, HostConn>`, but `choose()` still stops every other `HostConn`. One host is connected at a time, as today. The backoff replaces the fixed waits.

Check all of PR 1. Stop the gateway of host B while its window shows. Start it again within 60 s. The panes come back, the bytes that the user typed meanwhile arrive once, and `session.list` is the same. Do it again after more than 60 s. A new client starts, the panes attach again, and no new window appears.

### PR 3: `feat(web): keep several hosts connected at once`

The core of #472.

- Start every host (Which hosts connect). When the user chooses a window of another host, the page no longer stops a `HostConn`.
- The sidebar gets the data and state of every host (The sidebar: what it needs). It has rows keyed by host, state dots, problem lines and `.dimmed` rows of an offline host. The views of the sidebar design may not have landed. Then this PR lists the sessions of each connected host in today's form under the name of the host. This is a stopgap that those views replace. The notice shows for the host of the shown window only.
- The stagger, the `online` and `visibilitychange` pokes, and the batched sidebar updates.
- Next and previous window across hosts. Next and previous space.
- `gorex.place` and `gorex.tailnetOpened`. The migration of `gorex.host` into `gorex.place` is a pure function in a new `web/src/place.ts`.
- The unit tests. `npm test` in `web/package.json` runs a new `web/test.mjs`. It bundles each `web/test/*.test.ts` with esbuild (already a dev dependency). The sources import without extensions, so Node cannot load them as they are. It runs the bundles with `node --test`. The first cases cover the `gorex.host` migration. The cases are: no key, a direct host, a tailnet host, a host that is no longer in the list, and storage that throws.
- CI: a new `web` job in `.github/workflows/ci.yml`, on `instabox-medium` with the same `if` as `build`. It runs `actions/setup-node` (Node 24), then `npm ci` and `npm test` in `web/`.
- docs/REF-007-web-client.md: Hosts ("Only the chosen host is connected" goes), Split panes, Shortcuts, How it works.

Check with hosts A and B:

1. The sidebar shows the spaces and windows of both hosts. DevTools › Network › WS shows two control sockets and one data socket for each shown pane.
2. Show a window of A. In a pane of B (the desktop app of B), run `printf '\033]0;from-b\007'`. The row of B shows the title. Run `printf '\033]7501;state=blocked:kind=question\033\\'`. The row of B shows the attention dot.
3. Stop the gateway of B. The dot of B turns, its rows dim, and its problem line shows. The panes of A take typing. Click a dimmed row of B. The stage shows its cached layout and the notice of B. Start the gateway of B. B comes back, its `session.list` count is unchanged, and no new tab appears in the app of B.
4. Stop the app and the gateway of B and reload the page. A connects and shows its place. B shows its problem line with no rows.
5. Reload with the place on B while B is up. The page shows the window of B.
6. Use a second address of the gateway of A. Both hosts show the windows of A.
7. Use five addresses of the gateway of A. The page stays responsive at startup. Note the heap in DevTools › Memory against two hosts.
8. `npm test` passes locally, and the `web` job of the PR passes.

### PR 4: `feat(web): keep the last four windows live`

- `kept` in `main.ts`, `WindowView.park()`, and `show()` and `hide()` as in Kept windows. The evictions: the fifth window shown, a window that is gone, and a host that drops, is disconnected, removed or edited.

Check with hosts A and B:

1. Show five windows in turn, of both hosts. In DevTools › Network › WS, the data sockets of the first window close when the fifth window shows. The other sockets stay open.
2. Show the second window again. No new data socket opens and no snapshot comes. The scrollback position is as the user left it. Its panes take typing.
3. While a window of A is kept, its blocks are disowned. The desktop app of A sizes them to its own panes again. Show the window again. The page sizes them.
4. Run `yes` in a pane of a kept window for a minute, then show the window. The terminal shows the latest output. The page stays responsive.
5. Stop the gateway of B. The kept windows of B close (their sockets close). Show one again after B comes back. It attaches from a snapshot.
6. Measure the heap in DevTools › Memory with four windows of four panes each, against one window. Record it in the PR.

### PR 5: `feat(web): manage hosts while connected`

- The host panel and Settings › Hosts: state dots, Connect and Disconnect, Remove, Edit, as above. `gorex.hostsOff`.
- `SettingsApp.link(host)`. The note above 8 connected hosts.

Check the following. Disconnect B: its sockets close in DevTools, its windows leave the sidebar, and A keeps working. Connect B again. Edit the address of B to a wrong port: only B reconnects and fails. Remove B while its window shows: the stage moves to A. Reload: B stays disconnected.

### PR 6: `feat(web): read the desktop's spaces across hosts`

- `web/src/spaces.ts`: read `/api/spaces` for each host, and the `CrossHost` port (`rows`, `isMoved`, `isGone`) for the spaces view of the sidebar design. This PR adds no view.
- Unit tests: the `CrossHost` cases of `ios/GoRexTests/SpacesTests.swift` (`testSpaceTakesOtherHostsWindows`, `testSessionGone`, `testOnlyHostsTheAppReaches`, `testEachWindowOnce`, `testMachineAddedTwice`). The team ports them to `web/test/spaces.test.ts`. The runner and CI job of PR 3 run them.

Check: `npm test`. Use a real pair of machines, where the space of one desktop holds a tab of the other. The grouping places that tab in the space, under the other host, and empties the "Desktop terminals" of that host. The check in the sidebar goes with the spaces view of the sidebar design.

### After these PRs

These items are not part of #472. They are listed for order:

- #477 (uploads) can start after PR 2. It needs only the own `Control` and transport of the pane.
- #474 (stay signed in, restore the place) comes after PR 3. It uses `gorex.place` and `gorex.tailnetOpened`.
- The sidebar design can build on `HostConn` after PR 2. It takes the data of every host from PR 3. It needs PR 6 for a spaces view.
- #475 (space management, tab placement) comes after PR 6 and the sidebar design.
