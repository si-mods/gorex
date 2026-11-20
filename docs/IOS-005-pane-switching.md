---
name: IOS-005
description: "Rex Go reaches every pane of a tab and keeps a last-viewed record on each device. Use when you change pane switching on iOS."
metadata:
  created: "2026-11-16"
  updated: "2026-11-20"
---

# IOS-005: Rex Go iOS: panes inside a tab, and a view record on each device

Status: phase 1 built (#639, #653, #665). Phases 2 and 3 not built.

This doc lets Rex Go on iOS reach all panes of a tab, and keeps a separate "last viewed" record on each device. It relates to three issues. #521 asks to keep the split layout across clients, and to reach all panes of the group in Rex Go on iOS. #518 covers cross-host Space groups. #520 covers keeping tabs and pane names across clients. The doc compares the design with Rex Beta 0.1.0 build 1987 and Rex Go 0.1.0 build 1577. It also has a dedupe audit (section 2.2).

## 1. Problem

On the desktop, one tab can split into several panes (for example "training / logs / GPU"). The server `layout` stores the full split tree, the directions, the ratios, the pane names and the revision. The web client already renders this tree and drags the dividers.

The iOS app of GoRex reads only `windows[].focused_block_id` from `session.view`. It does not read the other panes of the same tab. It has no way to switch panes. The results are:

- The phone can open only the focused pane of each tab. The other panes are not reachable.
- The phone cannot see which other panes the group has, what they are called, or what state they are in.
- Suppose the app implements "switch pane" as `session.focus_block`. That call also changes the focus of the desktop, because the focus (`Tab.Focus`) is shared. This breaks rule 5 of #521.

This doc solves only two problems. The first is access on iOS to the terminal panes of one machine. The second is "which pane the user views" without a write to the shared layout. Panes of other hosts, browser or note panes, and live geometry follow of another desktop are not in this phase (section 7).

## 2. Check of the current state (main)

| Capability | Current state | Where |
|---|---|---|
| Source of truth for the layout | The server stores `layout.Store`: the `Tab.Root` tree, `Vertical` and `Ratio`, `Tab.Focus`, `Tab.Zoom`, `Rev` | `Store`, `Tab` and `Node` in `internal/layout/layout.go` |
| Layout writes | Only the desktop app (`tab.set` and `windows.set`) and the layout commands. `layout.apply` checks conflicts with `Base` | `internal/rex/proto.go`, `internal/rex/layout_apply.go` |
| Rex API exposure | The `layer.layout` of `session.view` is `{rect, split:{split_id,direction,ratio,before,after}}`. `blocks[]` follows the layout order | `layerView`, `layoutView` and `splitView` in `internal/rexapi/model.go` |
| Pane operations | `new_split / move_block / swap_blocks / resize_pane / focus_block / zoom_block / set_block_label / close_block` | the method table in `internal/rexapi/methods.go` |
| Web | It renders nested splits, dividers and ratios. A click on a pane calls `session.focus_block`, and the web client follows the focus of the host | `WindowView` in `web/src/window.ts`, and the `session.focus_block` call in `web/src/main.ts` |
| GoRex iOS | It reads only `windows[].focused_block_id` and `layers[].blocks`. It lists and switches by window. It has no pane list | `SessionView.Window` in `ios/GoRex/Rex/SessionView.swift`, and `SessionSheet` in `ios/GoRex/Views/SessionSheet.swift` |
| iOS view sync | `session_view_changed` makes the app read `session.view` again | `Host` in `ios/GoRex/Rex/Host.swift` |
| iOS input and size | When a pane opens, the app sends `own_block take` and `resize`. When it closes, the app sends `disown_block` | `ios/GoRex/Terminal/TerminalSession.swift` |

iOS already has the data that this phase needs:

- `SessionView.Window.tiled` was the list of **block IDs** of all tiled panes of the tab, **in layout order**. The server builds it in the order of `w.blocks` (`internal/rexapi/model.go`). This phase renamed it to `panes` and kept no alias.
- The label of each block is in `layers[].blocks[].label`.
- `Host.describe` already fetches the title, program and status of each block. `block_event` updates them.
- The server already pushes events when the session revision changes. iOS already reads the view again.

### 2.1 Reference versions of Rex, and the conclusion

References:

- The installed `Rex Beta.app` 0.1.0 (build **1987**, `snapshot`, commit `6c4ecb4`). Its helper is `rex 0.1.0-preview.19`, with **API revision 14**.
- The payload of `~/Downloads/Rex.dmg` is 0.1.0 (build **1642**, `preview`, commit `a92aace`). It is older than the installed version. This doc takes the behavior from the installed build 1987. For a strict match with the dmg, note the difference of 345 builds.
- The `rex version` of the helper in the dmg could not be read on a read-only mount. Gatekeeper and syspolicyd held the image, and the command timed out. The API revision comes from the installed helper.
- The dmg has only the macOS client and the Rex Server helper. It has **no Rex Go iOS binary**. The phone interaction cannot be checked against the dmg. This doc follows the public test of the Rex Go interaction (section 2.1.1). It is not a pixel-by-pixel port from the dmg.

The API documents and strings inside the Rex binaries give three facts:

1. **The focus of Rex is shared session state. It is not state for each client.** The description of `session.focus_block` is "Focus a block, and make its window the active window if it isn't." `session.focus_direction`, `focus_next_pane` and `focus_previous_pane` all change the focus of the session directly. Suppose iOS uses `session.focus_block` to "switch the view". The desktop focus and the active window then change without fail.
2. **The protocol has no focus of the view for each client.** The `ClientView` of `client.list` and `client.inspect` has only `client_id`, `principal`, `info`, `connected_at`, `connection_state`, `session_ids` and `actions`. It has no field for "which block or window this client is looking at". The `focused` of `session.attach` means size ownership (typist). It does not mean a viewing position.
3. **Rex clients do keep navigation state locally.** The macOS side has `WorkspaceLastFocusedSessionStore`, `WorkspaceSessionOrderStore` and `WorkspaceOpenWindowsStore`. The command palette has `PaneNavigatorCommandPalettePage` ("go to anything pane block tab session…"). On a Mac, all panes show on one screen, and a selected pane calls the shared focus directly. A phone shows one pane at a time. Only a phone needs an extra local record of "which one I am viewing".

The conclusion: **the "local view record" is not a second focus model. It is a client cache.** It takes no part in the layout or the active window, and it does not sync with other devices. The server still has one truth: `layout` and `Tab.Focus`. Rex keeps window and session navigation state locally. This is a precedent for the same dividing line.

### 2.1.1 Rex Go on the phone (external evidence)

The dmg has no iOS binary. The alpha of Rex Go is distributed through TestFlight, and a public test recorded its interface (DevelopersIO, 2026-10-31, `Rex Dev 0.1.0 (1577)`):

- One session can have several tabs, and each tab has several panes (a user said so). The public screenshots show only the state with one tab open.
- The user switches the panes of one tab with a **horizontal swipe**. The article says: "Switching panes is intuitive with a horizontal swipe".
- The top right shows a **thumbnail of the whole split layout**, with the current pane highlighted. The screenshot caption says: "The pane layout is shown at the top right and the pane switching buttons at the bottom".
- The bottom has a row of **pane switch buttons** (program icons, the current one highlighted) and a `+` button. The screenshot with 3 panes has 3 buttons.
- The top shows the title of the current tab (program plus terminal title) and the session name (`gentle-moor ⌄`). The back arrow returns to the upper list. This means the tab has its own list axis [INFERENCE].
- The same article states that the user can switch among the session lists of several hosts (MacBook and Mac mini). In `rex client ls` of the CLI, the iPhone appears with `platform=ios`.

The conclusion: the model of Rex Go is **session → window (tab) → pane, with two axes**. It is not "one tab can show only one pane". The iOS app of GoRex does not need to invent a third presentation. It follows this interaction directly. The tab axis keeps the existing `SessionSheet`. The pane axis adds a thumbnail, a bottom button bar and a swipe. The thumbnail reads the rect and split of `layer.layout`, **read-only**. It never writes the layout.

The article and the screenshots do not describe some details. These are the gesture thresholds, the sort rule of the bottom buttons and the exact meaning of `+`. They were not observed in the reference versions. This doc infers them from the existing GoRex implementation and the layout order (marked `[INFERENCE]`). Check them on a real device when you implement them.

### 2.2 Dedupe audit (this design adds no second system)

| Concern | Existing source or mechanism | How this design treats it | New duplicate? |
|---|---|---|---|
| Layout structure, order, direction, ratio | `layout.Store` (server) | Read `session.view` only | No |
| Focus, keyboard, active window | `Tab.Focus` and `active_window_id` | No write. Use it only as a fallback when the local record is missing | No |
| Pane set | `SessionView.Window.tiled` (already in layout order) | **Rename** to `panes` and update all call sites | No (no alias) |
| Pane name | `blocks[].label` and the `Host.title` chain | Move `Host.title` out into a function with a block parameter. The window title and the pane button share it | No (no second fallback) |
| Pane state | `Host.status([block])` | Reuse it directly | No |
| View position for each device | None | A small local cache (`UserDefaults`) | New, but not a source of truth |
| Persistence | The existing `UserDefaults` use. (`SpaceStore`, which this audit named, is removed. Projects replace it, `docs/MOD-002-projects.md`) | Reuse `UserDefaults`. Build no new sync system | No |
| Tab axis UI | The existing `SessionSheet` (window list) | Keep it. No change in this phase | No |
| Pane axis UI | None | Thumbnail, bottom buttons and swipe. All come from the same `layer.layout` and `panes` | New view, but not a new data source |
| Protocol and server | Rex API | No change | No |

Options that the design rejects:

- **A server field `view_focus` for each client.** Rex has none. It would become a second focus, and the design would need rules for conflicts and cleanup. Only the case "a new phone also remembers" needs it. This phase does not do it.
- **`Window.panes` next to `tiled`.** This duplicates the API, and the two would drift. The design renames instead.
- **A separate resolver for name and state.** It would drift from `Host.title` and `Host.status`. The design only extracts shared functions.
- **Treat each pane as a fake window list.** This gives duplicate entries and a risk of a duplicate attach. It also breaks the meaning of "the same group".
- **A separate pane list page.** Rex Go already gives two complementary entries, the thumbnail and the bottom bar. A third list would only repeat them.
- **iCloud sync of the view position.** This is the opposite of "each device is independent".

## 3. Goals and non-goals

### Goals

1. Under the tab (window) axis of a session, iOS lists all tiled panes of the current tab (thumbnail and bottom bar). The current pane shows its name. Every pane has a state mark and an accessible name (tooltip and VoiceOver). The tab axis keeps the existing window list.
2. iOS switches panes as Rex Go does: the layout thumbnail at the top right (current pane highlighted), the bottom pane buttons and the swipe. After a switch, the pane stays connected to the original session. The app creates no new terminal and loses no ownership.
3. A switch affects only what this iPhone shows. It does not change `Focus`, `Zoom`, `Root` or `Ratio` of the shared layout. It does not force the desktop to switch.
4. Each iPhone remembers independently "the pane last viewed in each window". After a reopen, it returns to its own pane.
5. A layout change on the desktop (a new, closed or renamed pane, a ratio change) reaches the iOS list through the existing revision. The desktop structure stays unchanged.
6. Rotation, keyboard appearance and full-screen view are local presentation. They do not write the layout.

### Non-goals

- Do not bring the desktop grid to the phone as an editable split layout. The thumbnail at the top right shows only the direction and ratio, read-only. iOS has no divider drag.
- Do not edit the shared layout on the iPhone. Closing a pane, renaming a pane and dragging a divider stay on the desktop. iOS only creates one split (adds one pane), at the same level as a new tab.
- Do not show panes of other hosts, or browser and note panes, in full (section 7).
- Do not do the live geometry follow after another GoRex desktop adopts a remote layout (section 7.3).
- Do not change the input and size semantics of a PTY that several clients share (section 5.7 describes the current state).

## 4. Terms: layout focus and viewed block

| Concept | Owner | Role | Changed in this phase? |
|---|---|---|---|
| **layout focus** | the server `layout.Tab.Focus` | Desktop keyboard focus. The fallback for the tab title. The anchor of split, move and close. The `focused_block_id` of the client view. The anchor of the active window | No |
| **viewed block** | a one-cell cache on each client | "The block that this client viewed last" | New on iOS. UI memory only |

Rules:

- The viewed block is not a focus, not session state and not a layout field. It has no server representation and takes no part in the revision.
- The viewed block of iOS **never writes** `session.focus_block`, `session.zoom_block` or any `LayoutCommand`.
- iOS uses the layout focus only as a fallback (when the device has no record, or the record is invalid).
- The desktop keeps the layout focus as its own "which pane is being viewed". The web client shows all panes on one screen and does not need a viewed block. A click on a pane in the web client stays an explicit remote operation (no change).

## 5. Design

### 5.1 Data source and order

Two things are separate:

1. **Members and order.** `SessionView.Window.tiled` was already the list of block IDs of all tiled panes of the tab, in layout order. This phase **renamed** it to `panes` and updated all call sites (`Host.swift`, `HostListView.swift`, `Inbox.swift`). It kept **no alias**:

```swift
var panes: [String] {
    layers?.first?.blocks?.map(\.block_id) ?? []
}
```

2. **Geometry.** For the thumbnail at the top right and for hit testing, the app decodes the `layout` that the tiled layer already has. The web client uses the same data. The design adds no server field.

```swift
struct PaneLayout: Decodable {
    var rect: [Double]?          // [x, y, w, h]
    var block_id: String?
    var split: PaneSplit?
}
struct PaneSplit: Decodable {
    var direction: String        // "horizontal" | "vertical"
    var ratio: Double
    var before: PaneLayout
    var after: PaneLayout
}
```

- The app uses `layout` only to **draw the thumbnail and for hit testing**. Members, order, names and states still come from `blocks` and `panes`. The app does not parse a second pane list out of `layout`.
- The thumbnail draws only the blocks in `panes`. While the app walks `layout`, it skips a leaf that is not in `panes` (a float, or a filtered pane). So the thumbnail and the bottom bar always show the same set. The thumbnail never has a cell that the bar lacks.
- When `rect` is missing, the app computes it recursively from `split.direction` and `ratio` (a pure function, in `paneCells` in `ios/GoRex/Rex/PaneGeometry.swift`). The thumbnail must not be skipped because the server omitted a rect.
- The design adds no `Layer.kind`. `rexapi` always outputs the tiled layer first and the floating layers after it (`internal/rexapi/model.go`, where the window view fills `Layers`). So `layers.first` is the tiled layer. A `kind` is needed only when floats join the pane group later.
- This phase does not list floats in the pane group. A canvas tab (no tiled layer) makes no thumbnail and no pane list.

### 5.2 Name and state

The design adds no second fallback chain. It extracts the "pane name" part of the window title into one function. The window title and the pane button share it. (The current `Host.title` chain is `window.name` → `blockName(focused)` → terminal title → `window.label`.)

```swift
// One chain for both a window's title and a pane row.
func paneTitle(_ block: String) -> String?   // blockName → blocks[block].title → program
func title(_ w: SessionView.Window) -> String // w.name → paneTitle(resolved block) → w.label
```

The name priority stays as it is now:

1. `blocks[].label` (the name that the user gave. `SessionView.blockName` already filters out the default `"shell"`).
2. `host.blocks[block]?.title` (the terminal title).
3. The program name.

The pane button falls back last to `"Terminal"`. The window row still falls back to `window.label`, as today.

State: for one block, the app calls the existing `host.status([block])` (`Host.status`). It shows the result as the same `ProgramStatusMark` that the window row uses. The window row still shows the most urgent state of the whole window. The design adds no aggregator.

### 5.3 The local cache of the viewed block, and its resolution

`ios/GoRex/Rex/ViewedBlock.swift` is a cache. It is not a focus system:

```swift
struct ViewedBlockKey: Hashable {
    let machine: String   // Host.cacheKey: "host:<UUID>", the host by its ID
    let session: String
    let window: String
}

@MainActor
final class ViewedBlockStore {
    static let shared = ViewedBlockStore()  // UserDefaults; by App install, not synced
    func block(for key: ViewedBlockKey) -> String?
    func set(_ block: String, for key: ViewedBlockKey)
    func forget(_ key: ViewedBlockKey)
}
```

- Storage location: `UserDefaults`. The build keeps all entries in one dictionary under the key `viewedBlocks.v1`. The dictionary key joins the parts `machine`, `session` and `window` with a unit separator. This is the same mechanism as the existing local preferences such as `backgrounds` (the `UserDefaults` use in `TerminalSession.swift`). The cache does not write to iCloud, because the requirement is "each device is independent".
- Authority: the cache decides only "which pane shows first". It **never** decides whether a block exists, or its name, order or state. All of these come from `session.view`. An invalid record is dropped.
- Resolution order (when a window opens):
  1. The explicit navigation target (a block that a notification, the Inbox or a deep link passes).
  2. The record of `ViewedBlockStore` on this device, when that block is still in `panes` of the window.
  3. `window.focused_block_id`.
  4. `panes.first`.
- When the app writes: the user selects a pane from the thumbnail, the bottom bar or the swipe. It also writes when the user opens a window from the window list and the app resolves a block.
- Invalidation: when a block is not in `panes`, the app ignores it and cleans up that entry. When a window disappears, the app cleans up the entry of that window (it can clean up lazily after it receives a revision).
- The `machine` part of the key is `Host.cacheKey`, which is `host:<UUID>`. The UUID is the ID of the host in the app. The key never changes when the app learns the `machine_id` later. (An earlier version of this doc used `spaceMachineKey`, which was `host:<UUID>` before `/api/spaces` reported the `machine_id`. That function no longer exists.)

### 5.4 Switch flow

All pane entries (horizontal swipe, thumbnail tap, bottom button) go through the same `selectPane(block)`:

1. `ViewedBlockStore.set(block, key)`.
2. `switchTo(TerminalTarget(host:session:block:))`. The existing implementation (`switchTo` in `ios/GoRex/Views/TerminalScreen.swift`) first starts a new `TerminalSession`, slides it in, and then closes the old stream.
3. **The app calls no** layout command (`session.focus_block`, `zoom_block` and `pane.*` are all not called).

A tab switch does not go through `selectPane`. It still uses the existing window list and the pill gesture. Each tab keeps its own viewed block.

Note: `switchTo` currently computes the direction from the window index. A switch between panes then gives `+1`. When you implement this, decide the slide direction from the index difference of the panes in `panes`. When the indexes are equal (in theory this does not happen), fade in. When the user slides to the first or last pane, the slide stops. It does not loop into another tab. The slide of Rex Go stays inside the current tab.

### 5.5 UI (as in Rex Go)

The two axes are separate:

- **Tab axis.** It keeps the window list of the existing `SessionSheet` and the window gesture of the pill. The app does not expand pane rows inside it. This avoids a repeat of the pane axis.
- **Pane axis.** The current tab has two entries. They share `panes` and `selectPane`:
  1. **The bottom pane bar.** Each pane has one program icon button (`ProgramTile` at its native ratio of 21×16). The current item has a light background that fits close around its tile. The app overlays the state dot of that pane on the button (blocked yellow, error red, done blue, working small gray dot). The `+` is a separate round button outside the capsule. `accessibilityLabel` uses `paneTitle(block)` plus the state.
  2. **Two-finger swipe.** On the terminal page, the user flicks up, down, left or right with two fingers. This switches to the adjacent pane in that direction. The app finds it by the position in `PaneLayout`. The content follows the fingers: a left swipe goes to the pane on the right, and an up swipe goes to the pane below. When no pane exists in that direction, nothing moves. It never crosses into another tab. One finger still scrolls, selects and taps. The gesture yields to the terminal in three cases. The first case is selected text. The second case is a full-screen program that captures the mouse (vim, Claude Code). The third case is a switch in progress.
- **Layout thumbnail.** There is no separate panes icon. After a pane switch, the thumbnail pops up once on the right of the toolbar (below the ellipsis). It shows the rect and split of `PaneLayout`. The current pane uses the color of its program tile, and the other panes are gray. After about 4 seconds, it fades out by itself. The thumbnail is only an indicator. It takes no taps and no swipes, and touches pass through it to the terminal. The two-finger swipe on the terminal page changes the direction (a 45° cone finds the nearest pane). The color of the current cell fades in and out in 0.25 s. A tap on the bottom bar also switches. A tab switch does not trigger it.
- **A single pane.** When `panes.count == 1`, the app hides the icon and the bottom bar, and keeps the pane as it is today. The Split Pane item of the toolbar menu (and ⌘D) is still an entry.
- **Title.** It keeps the existing pill. It shows the `paneTitle` or program of the current pane and the tab name. **It drops the `ProgramTiles` stack in the title**, because the thumbnail and the bottom bar already show the group information. It keeps one icon of the current program.
- **Split entry.** The current pane splits in place and creates a new shell (`session.new_split`, `direction: horizontal`, `side: after`, `focus: false`). The app then switches to the new pane. The desktop layout focus does not change. Three entries share one implementation. They are the `+` outside the capsule of the bottom bar and the Split Pane item of the toolbar menu. The third entry is ⌘D on an external keyboard, the same key as Split Right on the desktop. A window with one pane has only the last two. `+` no longer means New Tab.
- **Bottom safe area.** The pane bar is above the KeyBar. When the keyboard appears, the pane bar moves up with the keyboard, and the KeyBar stays below it. The `paneInset` of the terminal keeps the cursor above both.

The thumbnail shows **position, direction and ratio**. The bottom bar gives **order and switching**. Both come from the same `panes` and `PaneLayout`. The design adds no second copy of the pane data.

### 5.6 Sync and invalidation

- A server layout change sends `session_view_changed`, and iOS reads the view again (the existing chain).
- The thumbnail and the bottom bar update with `panes` and `PaneLayout`. A new, closed, renamed or reordered pane, and a ratio drag, all show.
- The viewed block closes:
  - The data stream ends with code 4000 (the `onDrop` handler that `ios/GoRex/Terminal/TerminalSession.swift` sets on the stream).
  - Behavior in this phase: the view stays in the ended state. It does not switch pane by itself. The user can go back to the list and reopen another pane of the same group.
- The desktop moves the viewed block to another window: the block still exists in the session, and `holds` (`SessionView.holds`) matches the new window. The app keeps the current connection. The thumbnail and the bottom bar show the new window membership.
- Desktop zoom: `Tab.Zoom` affects only what the desktop shows. iOS can still view the other panes. It does not change the desktop zoom.
- When the app opens or switches a pane, it calls the existing `host.see(session:block:)`. This marks the done and error records of that block as read. This "read" is shared server state (`Host.see`). It is not layout or focus. The user did see the pane, so this phase keeps it. Suppose a requirement says "a view on the phone must not clear the read mark on the desktop". That needs a separate seen state for each client, and it is not in this phase.

### 5.7 Shared input and size (current semantics, to state in the PR description and in help)

This phase does not change the behaviors below. Reviewers and users must still know them:

1. **Several clients can type at the same time.** The server does not check the owner for input. It queues writes in order (`(*session).input` in `internal/rex/session.go` and `internal/rexapi/input.go`). An iPhone and a desktop can type at the same time, and the characters interleave. No second PTY is created.
2. **The size is shared.** When iOS opens a pane, it sends `own_block take` (`ios/GoRex/Terminal/TerminalSession.swift`). The gateway holds the size lock of that session for iOS (`(*Gateway).tookOwnership` in `internal/rexapi/owners.go`). The PTY takes the grid of the phone. The desktop ignores its own resizes during that time. When the app closes the pane and sends `disown_block`, the desktop size returns (`(*session).endSizeLockLocked` in `internal/rex/session.go`).
3. So a "pane switch" makes the desktop terminal **reflow its content**. This is not a change of the layout or the split structure. The design keeps this current state so the phone stays readable.
4. The keyboard does not resize (the comment and the code of `keyboard(_:)` in `ios/GoRex/Terminal/TerminalView.swift`). A rotation calls `fit` and then `resize` (`fit` in `ios/GoRex/Terminal/TerminalSession.swift`).
5. An alternative (later, not in this phase): for a read-only preview, skip `own_block` and `resize` and use the current size of the session. Take the size when the user first types. This needs a separate UX decision.

## 6. Code change list

iOS (all changes of this phase):

- `ios/GoRex/Rex/SessionView.swift`: rename `tiled` to `panes` (update all call sites). Add the read-only `PaneLayout` and `PaneSplit`, which decode the `layout` of the tiled layer. Extract `paneTitle`.
- `ios/GoRex/Rex/ViewedBlock.swift` (new): `ViewedBlockKey` and `ViewedBlockStore` (the local cache).
- `ios/GoRex/Rex/Host.swift`: `paneTitle` and the existing `title(_:)` share code. No new API call.
- `ios/GoRex/Rex/PaneGeometry.swift` (new): `PaneCell`, `PaneRecord`, `paneCells` and `paneNeighbor`, with no UI dependency.
- `ios/GoRex/Views/PaneSwitcher.swift` (new): `PaneMiniMap` (the floating map, an indicator only) and `PaneBar` (the bottom icon bar and the compact state dots).
- `ios/GoRex/Views/TerminalScreen.swift`: resolve at open as section 5.3 says. Add `selectPane` and the page-level two-finger swipe in four directions. Add the thumbnail that pops up and fades out after a pane switch. Add the bottom bar, and coordinate it with the bottom inset of the KeyBar.
- `ios/GoRex/Views/SessionSheet.swift`: keep the window and session list. Do not expand pane rows.
- `ios/GoRexTests/PaneSwitcherTests.swift` (new): nested split geometry, row fallback, a missing block, the pane step boundary, and persistence of the viewed block.

Go and web: **no change**. If section 7.2 is done later, `internal/rexapi` changes then.

## 7. Boundaries and follow-up

### 7.1 Explicitly not done in this phase

- **Panes of other hosts.** `filterNode` drops the panes that have `Host != ""` (`internal/rexapi/model.go`). `GET /api/spaces` gives only `machine_id + block_id` (`internal/rexapi/spaces.go`). It has no tab, order or name. So suppose a tab on one machine mixes in panes of other hosts. iOS cannot rebuild "all panes of the same group".
- **Browser and note panes.** They have no session. The app cannot "connect to the original running session". It can only show a placeholder.
- **Floats and canvas.** The list in this phase has only tiled panes. A canvas tab is not listed.

### 7.2 Panes of other hosts (needs a protocol extension, a separate design)

Choose one of two options:

1. Extend `/api/spaces`: for each tab, give `remote_panes[{machine_id, block_id, session_id, label, index, ratio}]`. iOS then connects directly to the corresponding machine and reads the state.
2. Add a read-only method `gorex.tab_panes`. The server merges the information of this machine and other machines. This needs the machine mapping that the desktop `hosts.json` already has. The code does not have `gorex.tab_panes` today. It was only an option in this doc and was never built.

Risk: two servers each maintain the state and name of a cross-host pane, and their revisions do not match. The design must define a merge rule and an expiry rule.

### 7.3 Geometry follow on another desktop

When a remote desktop adopts a layout, it keeps the direction, ratio and name (the adoption code in `adoptions.go` and `internal/layout`). After that, it syncs only the name. It does not sync the ratio, direction or order. Suppose "switch to another computer" in #521 needs a live mirror. The code then needs a new layout reconcile. It conflicts with local zoom, focus and drag. We recommend a separate design.

## 8. Tests and acceptance

### Unit tests

- Fixture JSON has three levels of nested splits, floats, and default and custom labels. The test checks the order of `panes`, the `paneTitle` fallback, the rect and split of `PaneLayout`, and the state of a single pane.
- `ViewedBlockStore`: no record → focused. A valid record → the record. An invalid record → focused. A missing focused → first. Persistent read and write. Cleanup.
- The switch resolver calls no network method (check with dependency injection or a spy).

### Manual and end-to-end (a Mac mini with CuaDriver, by the private test process)

1. On the desktop, open a tab and split it into 3 panes. Name each one. Then open a second tab (one pane or two panes).
2. Connect an iPhone (simulator or real device) to the same host:
   - Open the first tab. The thumbnail at the top right has the same shape as the desktop, and the current pane is highlighted. The bottom has 3 program buttons, and the current one is highlighted.
   - Tap another cell of the thumbnail, tap another bottom button, or swipe horizontally. All three ways switch to the matching pane.
   - Switch to the second tab and then back to the first. Each tab returns to its own viewed block.
3. Record these values of the desktop `layout.json` before and after the pane switch on the iPhone: `tabs[].focus`, `zoom`, the `ratio/vertical` of `root`, and `rev`. They must not change (`rev` must not increase because of an iOS view).
4. Close and reopen the iPhone app, or kill it. Each tab returns to its own last pane. The focused pane of the desktop does not change.
5. On the desktop, add, close or rename a pane, and drag a divider. After the revision, the thumbnail and the bottom bar on the iPhone update (shape, ratio, name, state). The desktop structure does not change.
6. Type on both ends at the same time. Both can write to the same session (the expected result is interleaving). No second session appears.
7. Rotate the iPhone and show the keyboard. The layout does not change. The PTY size changes as section 5.7 says, and it recovers after the disconnect.
8. In vim or Claude Code, select text or turn on mouse capture, and swipe horizontally. The app must not switch panes by mistake. Normal scroll and select have priority.

### Acceptance criteria (for #521)

- [ ] The members, order, direction and ratio of the group panes are visible in the iOS thumbnail and match the desktop. The desktop structure stays unchanged.
- [ ] The iPhone shows one pane at a time. It can see and switch to the other panes of the group (thumbnail, bottom button, horizontal swipe).
- [ ] The current pane shows its name. The bottom bar and the thumbnail show a state mark and an accessible name for each pane. The pane connects to the original running session, with no duplicate and no lost ownership.
- [ ] A switch on the phone does not change the desktop layout focus, zoom or split structure.
- [ ] Each tab remembers its own pane independently. After the desktop adjusts the layout, the iOS thumbnail and bottom bar sync.

## 9. Phases

1. **Phase 1 (this doc, built in #639, #653 and #665)**: the `panes` rename, `PaneLayout` decode, the thumbnail, the bottom pane bar and the swipe. It also has the local viewed block cache with its invalidation fallback. It has no Go or protocol change.
2. **Phase 2**: group floats and canvas. More presentation of pane names (for example a tooltip on long press). Match more details of the Rex Go interaction.
3. **Phase 3 (a separate design)**: panes of other hosts (section 7.2) and the geometry follow on another desktop (section 7.3).

## 10. Risks and open questions

1. **Size grab.** Opening a pane changes the PTY size of the desktop. A user may think that "the phone changed my layout". The app needs text or help that explains this. Another option is to build "a read-only preview that does not take the size".
2. **Typing at the same time.** A shared PTY is existing product semantics. But a user who types on the phone while watching may disturb the work on the desktop. Open question: does the iPhone need a hint "another person is typing / sharing"?
3. **Auto switch when a pane closes.** It may interrupt the reading of the output. The alternative is to stay on the ended screen and offer an entry to "other panes of the same group".
4. **Gesture conflict.** The page-level horizontal swipe must yield to text selection, scrolling and full-screen programs that capture the mouse. Otherwise vim and Claude Code switch panes by mistake. The work needs a test of gesture recognizer priority.
5. **Thumbnail hit test.** When there are many panes, the cells are small, and a tap on one cell may be hard. The bottom pane bar is the fallback entry, with a 44 pt target.
6. **A local record conflicts with explicit navigation.** A notification or the Inbox that opens a specific block must have priority. The implementation needs one central resolver. Each entry must not write its own.
7. **The cache is not a source of truth.** The implementation must not use the cache to decide whether a block exists or is visible. It must check against the latest `session.view` each time. Otherwise a drift appears: "the thumbnail has it, the server does not".
8. **Rex Go is still alpha.** The reference version is 0.1.0 (1577). We cannot tell whether Rex Go calls `session.focus_block` on a swipe. This design explicitly does not call it, to meet "do not force the desktop" of #521. If upstream later changes to a shared focus, we do not follow.
9. Open question: should floats join the "group panes"? Product must decide.

## 11. Related

- #521: keep the terminal split layout across clients, and reach all panes of the group in Rex Go iOS.
- #518: all devices share one grouping by purpose. `SpaceStore` once carried it. [MOD-002-projects.md](MOD-002-projects.md) replaced it. This design no longer reuses `spaceMachineKey`. It uses `Host.cacheKey` as the persistent key.
- #520: keep tabs and pane names across clients (the name data is already there, and this phase shows it. The cross-client sync belongs to #520).
- `docs/REF-005-rex-api.md`: the semantics of layout, spaces and size locks in the Rex API.
- `docs/IOS-004-centralized-spaces.md`: history. It described the central grouping on iOS and the `machine_id` convention. [MOD-002-projects.md](MOD-002-projects.md) replaced it.
- Local reference: `Rex Beta.app` 0.1.0 (build 1987, snapshot, commit `6c4ecb4`), helper 0.1.0-preview.19, API revision 14. `~/Downloads/Rex.dmg` is 0.1.0 (build 1642, preview, commit `a92aace`). The evidence comes from the API documents and strings inside the bundle, not from a changelog.
- Rex Go on the phone: TestFlight `Rex Dev 0.1.0 (1577)`. For its interface and interaction, see the DevelopersIO test of 2026-10-31: <https://dev.classmethod.jp/en/articles/reona-superlogical-rex-beta-try/> (thumbnail, bottom pane bar, horizontal swipe, several hosts).
