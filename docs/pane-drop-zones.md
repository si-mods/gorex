# Dragging a pane onto another pane

Status: built (#148, #475). Real-window checks on a Mac are open.

The user can drag a pane (a block, in Rex's API) by its header and drop it on another pane of the window. The position of the pointer in the target pane decides where the dragged pane goes. It goes beside the target on one of its four sides, or in its place. The tree of splits changes and the sessions keep running. The panes move to their new places with the animation of the layout.

This follows Rex (Rex Beta, `SuperSplit`: `SuperSplitPaneDropDestination`, `SuperSplitPaneDropIndicator`, `dropZone`, `allowsLocalCenterDrop`, `swapAnimation`).

## Problem

Before #148 the code worked as follows:

- The header of a tiled pane drags the pane (`paneHeader`, `browserHeader`: `h.Drag(p)`). The header of a float moves the float instead (`dragFloat`).
- A pane card takes a dropped `*Pane` (`paneCard`, `view.go`) and calls `movePaneToPane`. That always splits the target left and right. The dragged pane goes on the right, at half the width of the target.
- Over a card, the drag shows only a border on the whole card (`k.busy.Alpha(0.6)`). So the user cannot see where the pane will go.
- The label of a tab takes a dropped `*Pane` and moves it to that tab (`movePaneToTab`).
- `pane.swap` and `pane.move` (Rex API, `layoutcmd.go`) swap two panes and move a pane to the pane of another tab. But they move it only to the right or below.

The change is mostly in the target: zones, an indicator, and a placement that can put the pane before the target.

## Non-goals

Rex does these things too. They are not in this change:

- **Spring loading.** The user holds a dragged pane over a tab for a moment. This selects that tab, so that the user can drop the pane on one of its panes (`PaneDragTabSpringLoader`).
- **New tab.** The user drops a pane on the empty part of the tab strip. This moves the pane to a new tab (`PaneDragNewTabDrop`, `PaneDropPlaceholderView`).
- **Edges of the room.** The user drops on the outer edge of the room of the tab. This splits at the root, so the pane takes a whole side of the tab.

## Design

### Behavior

#### Zones

The pointer position in the target card divides the card into five zones. The position is a fraction of the width and height of the card (`nx`, `ny`, 0 to 1):

```
+-----------------------+
|\         top         /|
|  \                 /  |
|    +-------------+    |
|left|   center    |right
|    +-------------+    |
|  /                 \  |
|/        bottom       \|
+-----------------------+
```

- **Center**: `|nx - 0.5| < 0.2` and `|ny - 0.5| < 0.2`. The two panes swap places (`swapPanes`). This works only within one tab, as Rex's "local center drop". From another tab there is no center zone, and the nearest edge wins.
- **Edges**: everywhere else, the nearest edge wins. It is the smallest of `nx`, `1 - nx`, `ny`, `1 - ny`. The diagonals divide the edges. The dragged pane goes on that side of the target, and takes half of the room of the target.

| Zone | Split | Dragged pane |
|---|---|---|
| left | left and right (`Vertical: false`) | A (first) |
| right | left and right | B |
| top | above and below (`Vertical: true`) | A |
| bottom | above and below | B |

The zones are a function of the bounds of the card from the last frame and the pointer (`e.PointerPosition`, as `tabDropZoneAt` does for tabs). The constants (0.2 for the center, the diagonals) are a first guess. Compare them with Rex Beta on a Mac and match it.

#### Indicator

While a pane is over a target, the target shows where the pane would go:

- A rounded rectangle covers the half of the card that the dragged pane would take (the left half for left, and so on). For center, it covers the whole card.
- Fill `k.busy.Alpha(0.18)`, border 1.5 `k.busy.Alpha(0.6)`, inset 4 from the card, radius `cardR - 4`, so that its corners nest in the corners of the card.
- GoRex draws it with `DrawOver` on the card. It uses no shader or `Effect`, because the card holds a terminal and `DESIGN.md` forbids effects on panes.
- The rectangle moves between zones at once. A spring between zones is left for later, because MyGo has no simple way to spring one painted rectangle.
- It replaces the border that the card showed before.

#### Source

- The pane that the user drags keeps its place until the drop. Its header is `dimmed(h, 0.4)`, as a dragged tab is. The card itself is not dimmed, because it holds a terminal.
- MyGo draws a copy of the header under the pointer (`Element.Drag`). Nothing to add.
- Escape cancels the drag (MyGo). Nothing moves.

#### What does not take the drop

In these cases the card takes the drag but shows no indicator, and the drop does nothing:

- The target is the dragged pane.
- The target is a float, or the dragged pane is a float. The header of a float moves the float, and floats keep their own drag.
- The two panes are on different hosts (`p.host != target.host`), as `movePaneBeside` already refuses.
- The tab of the target is zoomed. Only one pane shows, and this does not happen in the window, because the zoomed pane is the only target.

A drop on a sibling that is the only neighbor of the dragged pane still works. The tree is `[A | B]`. A dropped on the top of B gives `[A / B]`.

#### After the drop

- The dragged pane takes focus (`setFocus`, `focusReq`), as before.
- GoRex saves the layout (`changed`).
- The panes move to their new places with the element transition that splits and closes use (`tr` in `paneCell`). A swap moves both panes.
- Each terminal resizes once, when it is at rest again (`held` in `paneCell`), as for other layout changes.

### Code

#### State

`state.go`:

- `placePane(t, p, target, vertical, before, ratio)` takes a `before bool`. With `before`, the new split has the pane in A and the target in B, and the ratio is the share of the pane. Callers that existed before #148 pass `false`.
- `movePaneBeside(p, target, vertical, before, ratio)` passes it on. `movePaneToPane` keeps its behavior (right, half).

`panedrop.go`: `dropPaneOn(p, target, zone)` does the drop (`dropPane` already closes a pane). It calls `swapPanes` for center. Otherwise it calls `movePaneBeside` with the split and side of the zone, ratio 0.5. It does the checks of "What does not take the drop" and returns whether it moved anything.

`detachPane` runs before `placePane`, and `unlinkTiled` copies the sibling into its parent node. So `target.Node` may change during the move. `unlinkTiled` already sets the `Node` of the pane to its new node. `placePane` reads `target.Node` after the detach, so this works. A test covers it (dragging a pane onto its only sibling, each side).

#### View

`view.go`, `paneCard`:

```go
if dragged, over := ui.DragOver[*Pane](card); over {
	if z, ok := paneDropZoneAt(card, dragged, p); ok {
		drawPaneDrop(card, k, z)
		a.paneDrop = shownDrop{p, z}
	}
}
if dragged, ok := ui.Drop[*Pane](card); ok {
	z, ok := a.paneDrop.zone, a.paneDrop.target == p
	if !ok {
		z, ok = paneDropZoneAt(card, dragged, p)
	}
	a.paneDrop = shownDrop{}
	if ok {
		a.later(c, func() { a.dropPaneOn(dragged, p, z) })
	}
}
```

- **The drop takes the zone last shown** (`App.paneDrop`), not a zone found again from the pointer. By the frame that sees the drop, the pointer may have left the card, or the card may have moved.
- **The drop runs after the pass** (`a.later`). Suppose the drop ran while the pass builds the panes. A swap would show the card of the target again in the same pass, still holding the drop, and swap the panes back (`TestDragPaneToCenterSwaps`).
- `paneDropZone(nx, ny float32, center bool) dropZone` is pure. Tests cover it as they cover `tabDropZone`.
- `paneDropZoneAt(e, dragged, target)` reads the pointer and bounds. It answers `ok == false` for the cases that do not take the drop.
- `drawPaneDrop(e, k, zone)` draws the indicator.
- The header of the source: `if h.Dragging() { dimmed(h, 0.4) }` in `paneHeader` and `browserHeader`.

The new code is in a new file, `panedrop.go`, with its tests in `panedrop_test.go`. So `view.go` and `state.go` grow by a few lines.

#### Rex API

This work was done separately (#475). `pane.move` takes `direction` `left`, `right`, `up` or `down`. This is the side of the target that the pane goes on. The target may be in the own tab of the pane. `ratio` is the share of the first side, as `movePaneBeside` takes it. So `session.move_block` takes `side` `before`, and an anchor in the own window of the block, as the document of Rex has them.

### Risks

- **Browser panes.** A web view is a native view over the window. On a Mac, a pane dropped on a page still lands. On the left edge it went left of the browser pane, and at the center the two swapped. It is not checked yet whether the indicator shows over the page, under the native view. If it does not, show it over the header, or overlay a drop layer over the web view while a pane drag is on.
- **Hit testing.** The innermost element that takes `*Pane` gets the drag. Today only cards and tab labels take it. An element that takes `*Pane` and is added inside a card would take drags from the card.

## Tests

Headless (`ui.NewTester`, `float_test.go` and `layoutcmd_test.go` show how):

- `paneDropZone`: each zone, the diagonals, the center with and without `center`, a zero-size card.
- `dropPaneOn`:
  - Each edge on a pane in a nested tree gives the expected tree (orientation, A/B, ratio 0.5).
  - Center swaps.
  - The cases that do not take the drop change nothing.
  - A pane dropped on its only sibling, each side.
  - A pane from another tab. The source tab closes when it is empty, as before.
- A drag in the tester: press a header, move onto the left edge of another card, release. The tree has the pane on the left and GoRex saved the layout.

Real window, on a Mac with cua-driver (not on the development laptop):

- Drag a pane onto each zone of another pane, with colored output in the terminals. Take a screenshot of the indicator in each zone, dark and light, glass on and off.
- Record the screen for one drop. Check that the move animates without a frame of the panes at the wrong size (`verify glitches with screen recording`).
- Compare with Rex Beta side by side: zone sizes, indicator look.
