---
name: IOS-011
description: "Rex Go carries the element that the user touches into the next state, in nine places where the app jumped before. Use when you change a navigation, sheet, cover, pane bar or list animation in the iOS app."
metadata:
  created: "2026-11-20"
  updated: "2026-11-20"
---

# IOS-011: Continuous transitions between states

Status: phase 1 built (#916). The device check of phase 1 is open. Phase 2 is a proposal (#930, see "Revision: row push and steady list scheme").

This doc finds the places in the iOS app where one state replaces the next with no relation between them. It defines a continuous transition for nine of them. In a continuous transition, the element that the user touches becomes a part of the next state. The doc changes only how one state becomes the next. Each action, each screen and each destination stays the same. It extends [`IOS-005`](IOS-005-pane-switching.md) (pane slides) and [`IOS-007`](IOS-007-pane-gallery.md) (gallery and pager). Phase 2 changes item 1 back to the standard push, and keeps the list's colour scheme when a terminal closes.

## Problem

The terminal's own changes are continuous already. A pane or tab switch slides the panes (`promote` in `ios/GoRex/Views/TerminalScreen.swift`), and a two-finger drag moves them with the fingers. The title pushes from the side of the switch. Collapse, expand, reorder and the space picker animate with `.snappy`.

The changes between the list, the terminal, the sheets and the covers are not continuous. The table lists them. "Both" means iPhone and iPad.

| # | Where | Action | What happens now |
| --- | --- | --- | --- |
| 1 | `HostSection.rows`, `StatusInbox` (iPhone) | Tap a tab's row | A standard push from the right edge. The row has no relation to the terminal. |
| 2 | `TerminalScreen.principalTitlePill` (both) | Tap or long-press the title | `SessionSheet` comes up from the bottom edge. The pill stays. |
| 3 | `PaneBar` in `ios/GoRex/Views/PaneSwitcher.swift` (both) | Change the current pane | The highlight fades out on one tile and fades in on the other. |
| 4 | `GalleryView` → `GalleryPager` (both) | Tap a thumbnail, close the pager | A full-screen cover from the bottom. On close, it drops away. The image has no relation to its cell. |
| 5 | `.searchable` in `HostListView` (both) | Type in "Search tabs" | The list changes on each key with no animation. Projects open at once. |
| 6 | `NavigationSplitView` detail (iPad) | Tap a row in the sidebar | A hard cut, because the detail has `.id(t)`. |
| 7 | Bottom bar of `HostListView` (both) | "Show Activity" or "Show All Tabs" | `inbox.toggle()` replaces the whole list with no animation. |
| 8 | Settings button, Add Host, New Project `+`, Edit Spaces (both) | Open the sheet | A standard sheet from the bottom edge. |
| 9 | `PaneBar` + button, Close Pane, ⌘D (both) | Split or close a pane | The terminal slides (`switchTo`). The bar's tiles appear and go away with no animation when `session.view` changes. |

## Goal

- For items 1, 2, 4 and 8, the sheet, cover or terminal grows out of the element that the user touched. On close, it shrinks back into that element.
- For items 3, 5, 7 and 9, each element that is in both states moves from its old place to its new place. Elements that are in one state only fade.
- For item 6, the iPad detail cross-fades inside its own pane.
- At rest, each screen looks exactly as it does now. No action, destination or screen reader announcement changes.

## Non-goals

- The terminal's pane and tab slides do not change. [`IOS-005`](IOS-005-pane-switching.md) owns them.
- System menus and context menus do not change.
- Window resizing and size-class changes are not transitions, so they get no animation.
- The web client and the desktop app are not in scope.
- The doc adds no dependency and no change to navigation (`path`, `go`, `NavigationStack` or `NavigationSplitView`).

## Design

### Behavior

**1. Tab row → terminal (iPhone).** The row grows into the terminal. Back, or the edge swipe, shrinks the terminal into the row. Only a tap on a row in the list or in Activity uses the zoom. A zoom with no source starts from the centre of the screen. So all other opens keep the standard push: a notification, a widget, the session sheet and `switchTo` to another session or host. On iPad, item 6 applies.

**2. Title pill → session sheet.** The sheet grows out of the glass pill. Close, a drag down or a choice in the sheet shrinks it back into the pill. A choice of another tab in the same session then slides the panes, as it does now. A choice of a tab in another session replaces the path (`go`). On iPhone, that open keeps the standard push (item 1). On iPad, it keeps the hard cut (item 6).

**3. Pane bar highlight.** One highlight moves from the old tile to the new tile. The tiles do not move. The highlight follows each pane change: a tap, a swipe, a two-finger drag that completes, a split and a close.

**4. Gallery thumbnail ↔ pager.** The tapped thumbnail grows into the full image. When the user swipes to another image, the source changes to that image's cell. Close, or the pull down, shrinks the image into its own cell in the grid. Before the close, the grid scrolls that cell into view.

**5. Search tabs.** While the user types, the rows that still match slide to their new places. The other rows fade out. The projects that open for the search open with the same motion. The search field does not change. The system already moves it (`.searchToolbarBehavior(.minimize)`).

**6. iPad sidebar → detail.** After a tap on a row in the sidebar, the old terminal cross-fades into the new one, inside the detail pane only. The title pill is part of the new terminal, so it cross-fades with it. The sidebar does not move. All other opens keep the hard cut. Examples are Back, a notification, a widget, the session sheet and `switchTo` to another session.

**7. Activity ↔ All Tabs.** A tab (a window) that shows in both lists moves from its place in one list to its place in the other. Activity shows at most one row for each window. The other rows and the headers fade. Each card of All Tabs clips its rows. So a row that moves into a card shows only inside the card.

**8. Sheets from buttons.** The Settings sheet grows out of the button that opened it: the gear in the bottom bar, or Add Host in `NoHostsView`. The New Project sheet grows out of the `+` button in the host's header. Edit Spaces opens from an item in the space picker's menu. The menu closes before the sheet opens, so Edit Spaces has no source on screen. It keeps the standard sheet (see Decisions, 4).

**9. Split and close in the pane bar.** On a split, the new tile grows in at its place, and the tiles after it move to make room. Then the new pane slides in, as it does now. On a close, the tile shrinks away, and the other tiles close the gap. The bar shows only when the window has two or more panes. So a split from one pane to two keeps the slide of the whole bar from the bottom. A close from two panes to one does the same.

### Model

The rules below apply to all nine items.

| Rule | Value |
| --- | --- |
| One element | The element that moves is the same view, or a view that the system matches by id. The animation uses transforms only. It does not animate a frame that changes the layout. |
| Content in one state only | Content that exists only at the end fades in after about 50% of the move. Content that exists only at the start fades out in the first 30%. |
| Text that changes size | It scales. The app does not cross-fade two copies of it. |
| Layers | A sheet or cover that grows is opaque during the whole move. The moving element stays above its neighbours, and goes back to its place in the stack when it lands. |
| Corners | The radius changes smoothly. Shapes keep `.continuous` corners, as the app uses now. |
| Timing, system zoom (items 1, 2, 4, 8) | The system sets the duration and the curve. The app cannot change them. |
| Timing, app animations (items 3, 5, 6, 7, 9) | `.snappy`, as the list uses now. Item 6 uses `.easeInOut(duration: 0.2)`. |
| Interruption | A new action starts from the current frame. No animation blocks input. |
| Reduced motion | The system zoom handles it. The app animations use a 150 ms cross-fade and no movement or scale. They read `accessibilityReduceMotion`, as `ProgramTile` does. Search (item 5) changes at once, because a fade alone still moves the rows that stay. |
| Focus and VoiceOver | Focus and announcements stay as they are now. A sheet or cover gets focus when it opens, and focus goes back to the source when it closes. |

### Decisions

These are the answers to the open questions of the proposal.

1. **The zoom's dismiss gestures (items 1 and 4).** The iOS 26 SDK has no SwiftUI modifier that allows only the edge swipe. A probe build on the CI Mac showed this. So a `ZoomGuard` on the terminal and on the gallery image holds the zoom's drag down and pinch. The edge swipe back stays. The terminal keeps its scroll, its pinch for the font size and its two-finger pane drag. In a terminal that the zoom opened, the full-width swipe back works only from the edge.
2. **Back after a tab switch (item 1).** Back shrinks the terminal into the row that opened it. A tab switch, a split or a focus change inside the terminal does not change the source. A Done or Failed row leaves Activity when its terminal opens, because the terminal marks it as seen. Then Back has no row, and the system closes the terminal without a source.
3. **The highlight (item 3).** A plain slide, with no stretch.
4. **Edit Spaces (item 8).** It keeps the standard sheet, because it opens from a menu that closes first.
5. **Rows out of view (item 7).** The rows on screen move. The rows out of view fade.

### Code

| # | Files | Change |
| --- | --- | --- |
| 1 | `HostListView.swift`, `HostSection.swift`, `StatusInbox.swift`, `TerminalScreen.swift`, `TerminalView.swift`, `ZoomGuard.swift` | `ZoomSource` names each zoom source: `.row(host:session:window:)`, `.settings`, `.addHost` and `.newProject`. A row id names the window, because the window's focused pane can change while the terminal is open. `matchedTransitionSource` goes on each `WindowRow` button in `HostSection.rows` and each `StatusRow` button in `StatusInbox`. A row tap calls `openRow`, which sets `zoomSource` after `go`. `go` clears `zoomSource` for any other target, so Back still closes into the row. `ZoomFrom` applies `.navigationTransition(.zoom)` only when `zoomSource` matches the target, and `.automatic` in all other cases. `ZoomFrom` also sets the `zoomed` environment value. Then `TerminalHost` turns on its `ZoomGuard` (`ZoomGuard.swift`). The pans and pinches of the views above wait for the guard: the zoom's drag down and pinch never start in the terminal. The edge swipe back and scroll views are not held. A standard push leaves the guard off. |
| 2 | `TerminalScreen.swift` | A `@Namespace`. `matchedTransitionSource` on the principal `ToolbarItem` of the pill. `.navigationTransition(.zoom)` on the `SessionSheet` content. |
| 3 | `PaneSwitcher.swift` | A `@Namespace` in `PaneBar`. `matchedGeometryEffect(id: "current", in:)` on the highlight `RoundedRectangle`. The bar's `.animation(.snappy, value: current)` stays. |
| 4 | `GalleryView.swift`, `GalleryPager.swift` | `matchedTransitionSource(id: entry.id, in:)` on `GalleryCell`. A new `@State shown` in `GalleryView` holds the image on screen. The pager's `current` becomes a `@Binding` to `shown`. `viewing` does not change on a swipe, because a new `viewing` presents the cover again. `.navigationTransition(.zoom)` on the cover uses `shown` as the source id. A `ScrollViewReader` scrolls the grid to `shown` before the close. The pager's `ZoomScroll` has a `ZoomGuard`, always on, so its own pull down and pinch are the only ones. |
| 5 | `HostListView.swift` | `.animation(.snappy, value: query)` on the list's `LazyVStack`. |
| 6 | `HostListView.swift`, `TerminalScreen.swift`, `GoRexApp.swift` | On regular width, `openRow` wraps `go` in `.easeInOut(duration: 0.2)`, and the detail's `TerminalScreen` has `.transition(.opacity)`. `go` itself has no animation, because it has many other callers. The old screen lives on during the fade, so `ScreenStyle` records which screen set the scheme, and only that screen clears it. |
| 7 | `HostListView.swift`, `HostSection.swift`, `StatusInbox.swift` | `withAnimation(.snappy)` on `inbox.toggle()`. A second `@Namespace`, `places`, apart from the zoom namespace. `MatchedPlace` applies `matchedGeometryEffect` with a `RowPlace` id: host, session and `window_id`. Under Reduce Motion, `places` is nil, so no row moves. |
| 8 | `HostListView.swift`, `HostSection.swift` | The Settings button is now a `ToolbarItem` of its own, with `matchedTransitionSource`. The Add Host button of `NoHostsView` is a second source. `settingsSource` records which button opened Settings. The `+` button of `HostSection.header` is the New Project source. `.navigationTransition(.zoom)` on `SettingsView` and `ProjectSheet`. |
| 9 | `PaneSwitcher.swift` | `.transition(.scale.combined(with: .opacity))` on each tile. `.animation(.snappy, value: cells.map(\.block))` on the bar. |

The data, the protocol and the saved files do not change. No item touches `docs/irreversible.md`.

### Revision: row push and steady list scheme

Phase 2, a proposal. This part replaces item 1 above, and the parts of Decisions 1 and 2 that are about the terminal.

#### Problem

- **The zoom of item 1 looks wrong in both ways.** A user of #916 reported that the open and the close both look awkward. The terminal fills the screen, and its row is a strip about 54 points high. On open, the zoom stretches the strip into the screen. On close, it squashes the screen into the strip, much more in height than in width. So the terminal looks like a dark bar that grows or collapses, not like the row. The zoom works well only when the source and the destination have a similar shape. Examples are a thumbnail and its image (item 4), and the title pill and its sheet (item 2).
- **The list can change its colour scheme in one frame.** A user reported that the return to the list feels abrupt. The probable cause is in the code. Nobody saw it on a device yet. While a terminal is open, `ScreenStyle` sets the colour scheme of the whole window to the terminal's scheme (`.preferredColorScheme` in `GoRexApp.swift`). The list behind the terminal gets that scheme too.
  - On open, `TerminalScreen` sets the scheme when it appears. So the list changes at the first frame of the push.
  - On close, `TerminalScreen.onDisappear` clears the scheme after the pop ends. So the list changes at the last frame.
  - This occurs only when the terminal's scheme differs from the app's scheme.

#### Behavior

1. A tap on a tab's row opens the terminal with the standard push, as before #916. Back, the edge swipe and the full-width swipe back close it with the standard pop. A swipe follows the finger, and the user can cancel it half way. A user reported that the interactive swipe back of the #916 build is very smooth, and it must stay so (see Open questions, 1).
2. On iPhone, the list and its bars always show the app's own scheme: Settings › Interface, else the system's scheme. A terminal with another scheme changes neither the list nor its bars, during the push or the pop. The terminal and its bar keep the terminal's scheme, as now.
3. Only the status bar follows the window's scheme. It changes when the terminal appears, and when the path becomes empty.
   - The system Back button, the custom Back button, Close Tab, a removed host and a widget empty the path when the pop starts. So the status bar changes at the start of the pop.
   - The edge swipe and the full-width swipe empty the path only when the pop completes. SwiftUI tells the app nothing when a swipe starts. So the status bar changes at the end of a swipe.
4. On iPad, the sidebar keeps its current behaviour: it follows the scheme of the terminal beside it. The list and the terminal show at the same time there, so one scheme for the window looks better.
5. Items 2 to 9 do not change. The gallery keeps its zoom and its `ZoomGuard`. Item 6 keeps its fade on iPad.

#### Code

| File | Change |
| --- | --- |
| `HostListView.swift` | Remove `ZoomFrom`, `ZoomRecord`, `ZoomSource.row` and `ZoomSource.row(of:in:)`, and the `zoomed` state. `openRow` keeps the fade on regular width, and calls `go` on compact width. `.onChange(of: path)` releases `ScreenStyle` when the path becomes empty, whatever emptied it. On compact width, `list` gets the app's own scheme with `.environment(\.colorScheme, …)` and `.toolbarColorScheme(…, for: .navigationBar)` and `.toolbarColorScheme(…, for: .bottomBar)`. |
| `HostSection.swift`, `StatusInbox.swift` | Remove `matchedTransitionSource` from the `WindowRow` and `StatusRow` buttons. Item 7's `MatchedPlace` stays. |
| `TerminalView.swift`, `TerminalScreen.swift` | Remove `TerminalHost.zoomGuard` and the line in `TerminalRepresentable` that turns it on. |
| `ZoomGuard.swift` | Remove the `zoomed` environment value. The comment names only the gallery. |
| `GoRexApp.swift` | `ScreenStyle` gets `release()`, which clears the scheme for every screen. `clear(by:)` stays for `onDisappear`. `ScreenStyle` also keeps the system's scheme as observable state. It reads the window scene's trait collection, which the window's `preferredColorScheme` does not change. `registerForTraitChanges` on the scene updates it when the system changes between light and dark. |
| `docs/lessons.md` | The phase 2 code PR undoes a part of #916, so it adds a lesson. A zoom transition needs a source and a destination of a similar shape. |

## Alternatives

- **A custom `UIViewControllerAnimatedTransitioning` for item 1**: it gives full control of timing and the morph of the title into the pill. It needs a UIKit navigation controller in place of `NavigationStack`, which is a navigation change. It lost.
- **`matchedGeometryEffect` from the row to the pill for item 1**: it works only inside one view tree. The list and the terminal are in different navigation destinations. It lost.
- **A morph of each part for items 1 and 2**: an example is the title into the title. The system zoom moves the whole source as one shape. A morph of each part needs the custom transition above. It lost for the same reason.
- **Phase 2: a zoom out of the host's card**: the card is taller than a row, but still much shorter than the screen. The squash stays. It lost.
- **Phase 2: a zoom on open only, and a standard pop on close**: SwiftUI uses one transition for both ways. It lost.
- **Phase 2: the row's title moves into the title pill**: the list and the terminal are in different navigation destinations. So no shared element crosses the push. It lost for the same reason as the morph above.
- **A fixed duration of 450 to 700 ms for the system zoom**: the request for this work proposed it. The API does not accept a duration. Other iOS apps use the system timing, so it lost.

## Tests

- Each item: a simulator recording in its PR, as `docs/lessons.md` ("iOS layout, animation or gesture") requires. The recording shows open, close, and the first and last frames, with no jump at the hand-over.
- Data: long and short titles, and a gallery image that fails to load. Also the first and last row of a card, an empty gallery and a scrolled list.
- Interruption: fast repeated taps, Back during the animation, a drag down or an edge swipe cancelled half way.
- iPhone and iPad, light and dark, reduced motion on, VoiceOver, and a hardware keyboard (⌘T, ⌘D).
- Item 1: the terminal's scroll and two-finger pane drag still work with the zoom's dismiss gestures present. A notification, a widget and the session sheet still open with the standard push.
- Headless screenshots with the `gorex-ui-testing` skill: at rest, each screen is the same as before.
- The current `GoRexTests` and UI tests pass. The change is visual only, so it adds no unit test.
- Build: the Mac mini runner builds the app and runs `GoRexTests` for the branch (`.github/workflows/ios-test.yml`).
- Open check on a device: the zoom's pinch and drag down against the terminal's pinch, scroll and two-finger pane drag. Also the recording that `docs/lessons.md` asks for. No Mac with a simulator was available for #916.
- Phase 2, on a device: open a terminal from a row in All Tabs and in Activity. Close it with the system Back button, the edge swipe and the full-width swipe. Do this with a terminal scheme that is the same as the app's scheme, and with one that differs. The list and its bars must not change at the first frame of the push or at the last frame of the pop. Also change the system scheme with the list on screen, while Settings › Interface follows the system. On iPad, the sidebar must still follow the terminal's scheme. Also check that SwiftUI empties the path at the start of a pop from a button, as Behavior 3 expects. The gallery's zoom, close and pinch must work as in phase 1.
- Phase 2: the Mac mini runner builds the branch and runs `GoRexTests` (`ios-test.yml`).
- `docs/invariants.md` names no behavior that this change touches.

## Phases

1. Items 1 to 9: #916.
2. Revision: row push and steady list scheme: proposal.

## Open questions

1. **Which swipe back the user wants to keep (phase 2).** The #916 build has two interactive swipes back. A terminal that a row opened uses the zoom's swipe: the terminal shrinks toward its row under the finger. A terminal that a notification, a widget or the session sheet opened uses the standard swipe: the terminal slides to the right with the finger. Options: (a) the standard swipe for every terminal, as Behavior 1 says, (b) keep the zoom for item 1, with its swipe and its shrink into the row. The user decides.
