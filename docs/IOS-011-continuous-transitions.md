---
name: IOS-011
description: "Rex Go carries the element that the user touches into the next state, in nine places where the app now jumps from one state to the next. Use when you change a navigation, sheet, cover, pane bar or list animation in the iOS app."
metadata:
  created: "2026-11-20"
  updated: "2026-11-20"
---

# IOS-011: Continuous transitions between states

Status: proposal.

This doc finds the places in the iOS app where one state replaces the next with no relation between them. It defines a continuous transition for nine of them. In a continuous transition, the element that the user touches becomes a part of the next state. The doc changes only how one state becomes the next. Each action, each screen and each destination stays the same. It extends [`IOS-005`](IOS-005-pane-switching.md) (pane slides) and [`IOS-007`](IOS-007-pane-gallery.md) (gallery and pager).

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

**7. Activity ↔ All Tabs.** A tab (a window) that shows in both lists moves from its place in one list to its place in the other. Activity can show more than one row for one window, one for each pane with activity. Only the first of those rows moves. The other rows and the headers fade.

**8. Sheets from buttons.** The Settings sheet grows out of the button that opened it: the gear in the bottom bar, or Add Host in `NoHostsView`. The New Project sheet grows out of the `+` button in the host's header. Edit Spaces opens from an item in the space picker's menu. The menu closes before the sheet opens, so Edit Spaces has no source on screen. It keeps the standard sheet (see Open questions).

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
| Reduced motion | The system zoom handles it. The app animations use a 150 ms cross-fade and no movement or scale. They read `accessibilityReduceMotion`, as `ProgramTile` does. |
| Focus and VoiceOver | Focus and announcements stay as they are now. A sheet or cover gets focus when it opens, and focus goes back to the source when it closes. |

### Code

| # | Files | Change |
| --- | --- | --- |
| 1 | `HostListView.swift`, `HostSection.swift`, `StatusInbox.swift` | A `@Namespace` in `HostListView`, passed to the rows. `matchedTransitionSource(id:in:)` on each `WindowRow` button in `HostSection.rows` and each `StatusRow` button in `StatusInbox`. The id is the host, session and block of the target that the row opens. It is not the whole `TerminalTarget`, because `title` and `explicit` can differ. The row's `open` sets a `zoomSource` state in `HostListView`. `navigationDestination` applies `.navigationTransition(.zoom(sourceID:in:))` only when `zoomSource` matches the target, and `.automatic` in all other cases. |
| 2 | `TerminalScreen.swift` | A `@Namespace`. `matchedTransitionSource` on the principal `ToolbarItem` of the pill. `.navigationTransition(.zoom)` on the `SessionSheet` content. |
| 3 | `PaneSwitcher.swift` | A `@Namespace` in `PaneBar`. `matchedGeometryEffect(id: "current", in:)` on the highlight `RoundedRectangle`. The bar's `.animation(.snappy, value: current)` stays. |
| 4 | `GalleryView.swift`, `GalleryPager.swift` | `matchedTransitionSource(id: entry.id, in:)` on `GalleryCell`. A new `@State shown` in `GalleryView` holds the image on screen. The pager's `current` becomes a `@Binding` to `shown`. `viewing` does not change on a swipe, because a new `viewing` presents the cover again. `.navigationTransition(.zoom)` on the cover uses `shown` as the source id. A `ScrollViewReader` scrolls the grid to `shown` before the close. |
| 5 | `HostListView.swift` | `.animation(.snappy, value: query)` on the list's `LazyVStack`. |
| 6 | `HostListView.swift` | On regular width only, `.transition(.opacity)` on the detail's `TerminalScreen`. `withAnimation` goes in the sidebar rows' `open` closure only, not in `go`, because `go` has many other callers. |
| 7 | `HostListView.swift`, `HostSection.swift`, `StatusInbox.swift` | `withAnimation(.snappy)` on `inbox.toggle()`. A second `@Namespace`, apart from the one of item 1. `matchedGeometryEffect` on the rows of both lists, with the host, session and `window_id` as the id. A `StatusRow` uses `item.window`. In `StatusInbox`, only the first row of each window gets the id. |
| 8 | `HostListView.swift`, `HostSection.swift` | The Settings button moves out of the `ToolbarItemGroup` into its own `ToolbarItem`, which gets `matchedTransitionSource`. The Add Host button of `NoHostsView` is a second source. A state records which button opened Settings. The `+` button of `HostSection.header` is the New Project source. `.navigationTransition(.zoom)` on `SettingsView` and `ProjectSheet`. |
| 9 | `PaneSwitcher.swift` | `.transition(.scale.combined(with: .opacity))` on each tile. `.animation(.snappy, value: cells.map(\.block))` on the bar. |

The data, the protocol and the saved files do not change. No item touches `docs/irreversible.md`.

## Alternatives

- **A custom `UIViewControllerAnimatedTransitioning` for item 1**: it gives full control of timing and the morph of the title into the pill. It needs a UIKit navigation controller in place of `NavigationStack`, which is a navigation change. It lost.
- **`matchedGeometryEffect` from the row to the pill for item 1**: it works only inside one view tree. The list and the terminal are in different navigation destinations. It lost.
- **A morph of each part for items 1 and 2**: an example is the title into the title. The system zoom moves the whole source as one shape. A morph of each part needs the custom transition above. It lost for the same reason.
- **A fixed duration of 450 to 700 ms for the system zoom**: the request for this work proposed it. The API does not accept a duration. Other iOS apps use the system timing, so it lost.

## Tests

- Each item: a simulator recording in its PR, as `docs/lessons.md` ("iOS layout, animation or gesture") requires. The recording shows open, close, and the first and last frames, with no jump at the hand-over.
- Data: long and short titles, and a gallery image that fails to load. Also the first and last row of a card, an empty gallery and a scrolled list.
- Interruption: fast repeated taps, Back during the animation, a drag down or an edge swipe cancelled half way.
- iPhone and iPad, light and dark, reduced motion on, VoiceOver, and a hardware keyboard (⌘T, ⌘D).
- Item 1: the terminal's scroll and two-finger pane drag still work with the zoom's dismiss gestures present. A notification, a widget and the session sheet still open with the standard push.
- Headless screenshots with the `gorex-ui-testing` skill: at rest, each screen is the same as before.
- The current `GoRexTests` and UI tests pass. The change is visual only, so it adds no unit test.
- `docs/invariants.md` names no behavior that this change touches.

## Phases

Each item is one PR. The order is by value and cost.

1. Item 3, pane bar highlight.
2. Item 2, title pill → session sheet.
3. Item 1, tab row → terminal.
4. Item 4, gallery thumbnail ↔ pager.
5. Item 5, search tabs.
6. Item 6, iPad detail cross-fade.
7. Item 9, split and close in the pane bar.
8. Item 7, Activity ↔ All Tabs.
9. Item 8, Settings and New Project sheets.

## Open questions

1. **The zoom's dismiss gestures on the terminal (item 1).** The zoom adds a drag down and a pinch to close. These can conflict with the terminal's scroll and two-finger pane drag. Options: (a) keep them, (b) allow only the edge swipe, if the iOS 26 SDK has a modifier for it, (c) keep the standard push. The user decides after a simulator recording.
2. **Back after a tab switch (item 1).** The user can switch tabs inside the terminal, so the tab shown can differ from the row that opened it. If SwiftUI reads the source id when the terminal closes, Back shrinks into the row of the tab shown. If not, Back shrinks into the row that opened it. A simulator test finds out. The user decides whether (b) is acceptable.
3. **The highlight's stretch (item 3).** Options: (a) a plain slide, (b) the leading edge moves first and the trailing edge follows, with a squash of about 15%. Option (a) is the recommendation. Option (b) needs a custom animation. The user decides.
4. **Edit Spaces (item 8).** It opens from a system menu that closes first. Options: (a) keep the standard sheet, (b) use the menu's button as the source. The recommendation is (a). The user decides.
5. **Matched rows in item 7.** Rows out of view in a `LazyVStack` have no frame, so they can only fade. Options: (a) match the rows on screen and fade the rest, (b) fade the whole list. The recommendation is (a). The user decides.
