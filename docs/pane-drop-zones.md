# Dragging a pane onto another pane

Status: built; real-window checks on the Mac mini are open.

A pane (a block, in Rex's API) can be dragged by its header and dropped
on another pane of the window. Where the pointer is in the target pane
decides where the dragged pane goes: beside it on one of its four sides,
or in its place. The tree of splits changes, the sessions keep running,
and the panes move to their new places with the layout's animation.

This follows Rex (Rex Beta, `SuperSplit`: `SuperSplitPaneDropDestination`,
`SuperSplitPaneDropIndicator`, `dropZone`, `allowsLocalCenterDrop`,
`swapAnimation`).

## What there is now

- A tiled pane's header drags the pane (`paneHeader`, `browserHeader`:
  `h.Drag(p)`). A float's header moves the float instead (`dragFloat`).
- A pane card takes a dropped `*Pane` (`paneCard`, `view.go`) and calls
  `movePaneToPane`. That always splits the target left and right, with the
  dragged pane on the right, at half the target's width.
- Over a card, the drag shows only a border on the whole card
  (`k.busy.Alpha(0.6)`), so the user cannot see where the pane will go.
- A tab's label takes a dropped `*Pane` and moves it to that tab
  (`movePaneToTab`).
- `pane.swap` and `pane.move` (Rex API, `layoutcmd.go`) swap two panes and
  move a pane to another tab's pane, but only to the right or below.

The change is mostly in the target: zones, an indicator, and a placement
that can put the pane before the target.

## Behavior

### Zones

The target card is divided into five zones by the pointer's position in
it, as a fraction of the card's width and height (`nx`, `ny`, 0 to 1):

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

- **Center**: `|nx - 0.5| < 0.2` and `|ny - 0.5| < 0.2`. The two panes
  swap places (`swapPanes`). Only within one tab, as Rex's "local center
  drop"; from another tab there is no center zone, and the nearest edge
  wins.
- **Edges**: elsewhere, the nearest edge: the smallest of `nx`, `1 - nx`,
  `ny`, `1 - ny`. The diagonals divide them. The dragged pane goes on that
  side of the target, and takes half of the target's room.

| Zone | Split | Dragged pane |
|---|---|---|
| left | left and right (`Vertical: false`) | A (first) |
| right | left and right | B |
| top | above and below (`Vertical: true`) | A |
| bottom | above and below | B |

The zones are a function of the card's bounds from the last frame and the
pointer (`e.PointerPosition`, as `tabDropZoneAt` does for tabs). The
constants (0.2 for the center, the diagonals) are a first guess: compare
them with Rex Beta on the Mac mini and match it.

### Indicator

While a pane is over a target, the target shows where the pane would go:

- A rounded rectangle over the half of the card that the dragged pane
  would take (the left half for left, and so on), or over the whole card
  for center.
- Fill `k.busy.Alpha(0.18)`, border 1.5 `k.busy.Alpha(0.6)`, inset 4 from
  the card, radius `cardR - 4`, so that its corners nest in the card's.
- Drawn with `DrawOver` on the card. No shader or `Effect`: the card holds
  a terminal, and `DESIGN.md` forbids effects on panes.
- The rectangle moves between zones with a short spring when an animation
  style is set (`motion(c)`), and at once without one.
- It replaces the border the card shows now.

### Source

- The pane being dragged keeps its place until the drop. Its header is
  `dimmed(h, 0.4)`, as a dragged tab is. The card itself is not dimmed:
  it holds a terminal.
- MyGo draws a copy of the header under the pointer (`Element.Drag`).
  Nothing to add.
- Escape cancels the drag (MyGo). Nothing moves.

### What does not take the drop

The card takes the drag but shows no indicator, and the drop does
nothing, when:

- the target is the dragged pane;
- the target is a float, or the dragged pane is a float (a float's header
  moves the float; floats keep their own drag);
- the two panes are on different hosts (`p.host != target.host`), as
  `movePaneBeside` already refuses;
- the target's tab is zoomed (only one pane shows; this does not happen
  in the window, since the zoomed pane is the only target).

Dropping a pane on a sibling that is its only neighbor still works: the
tree is `[A | B]`, A dropped on B's top gives `[A / B]`.

### After the drop

- The dragged pane takes focus (`setFocus`, `focusReq`), as now.
- The layout is saved (`changed`).
- Panes move to their new places with `paneTransition`, the animation
  that splits and closes use. A swap moves both panes.
- Each terminal resizes once, when it is at rest again (`held` in
  `paneCell`), as for other layout changes.

## Code

### State

`state.go`:

- `placePane(t, p, target, vertical, ratio)` gets a `before bool`. With
  `before`, the new split has the pane in A and the target in B, and the
  ratio is the pane's share. Callers that exist pass `false`.
- `movePaneBeside(p, target, vertical, before, ratio)` passes it on.
  `movePaneToPane` keeps its behavior (right, half).
- A new `dropPaneOn(p, target, zone)` (`dropPane` already closes a pane) does the drop: `swapPanes` for center,
  else `movePaneBeside` with the zone's split and side, ratio 0.5. It does
  the checks of "What does not take the drop" and returns whether it moved
  anything.

`detachPane` runs before `placePane`, and `unlinkTiled` copies the
sibling into its parent node, so `target.Node` may change during the move.
`unlinkTiled` already sets the pane's `Node` to its new node; `placePane`
reads `target.Node` after the detach, so this works. A test covers it
(dragging a pane onto its only sibling, each side).

### View

`view.go`, `paneCard`:

```go
if dragged, over := ui.DragOver[*Pane](card); over {
	if z, ok := paneDropZoneAt(card, dragged, p); ok {
		drawPaneDrop(card, k, z)
	}
}
if dragged, ok := ui.Drop[*Pane](card); ok {
	if z, ok := paneDropZoneAt(card, dragged, p); ok {
		a.dropPane(dragged, p, z)
	}
}
```

- `paneDropZone(nx, ny float32, center bool) dropZone` is pure, and is
  tested as `tabDropZone` is.
- `paneDropZoneAt(e, dragged, target)` reads the pointer and bounds, and
  answers `ok == false` for the cases that do not take the drop.
- `drawPaneDrop(e, k, zone)` draws the indicator.
- The header of the source: `if h.Dragging() { dimmed(h, 0.4) }` in
  `paneHeader` and `browserHeader`.

The new code goes in a new file, `panedrop.go`, with its tests in
`panedrop_test.go`, so that `view.go` and `state.go` grow by a few lines.

### Rex API

Not part of this change. `pane.move` takes `direction` `right` or `down`
only; with `before`, it could take `left` and `up` too. That is a separate
PR, with the API contract tests.

## Tests

Headless (`ui.NewTester`, `float_test.go` and `layoutcmd_test.go` show
how):

- `paneDropZone`: each zone, the diagonals, the center with and without
  `center`, a zero-size card.
- `dropPane`: each edge on a pane in a nested tree gives the expected tree
  (orientation, A/B, ratio 0.5); center swaps; the cases that do not take
  the drop change nothing; a pane dropped on its only sibling, each side;
  a pane from another tab (the source tab closes when emptied, as now).
- A drag in the tester: press a header, move onto another card's left
  edge, release; the tree has the pane on the left and the layout was
  saved.

Real window, on the Mac mini with cua-driver (not on this MacBook):

- Drag a pane onto each zone of another, with colored output in the
  terminals; screenshot the indicator in each zone, dark and light, glass
  on and off.
- Record the screen for one drop and check that the move animates without
  a frame of the panes at the wrong size (`verify glitches with screen
  recording`).
- Compare with Rex Beta side by side: zone sizes, indicator look.

## Risks

- **Browser panes.** A web view is a native view over the window; the
  pointer over it may not reach MyGo, so the card under a page may not see
  the drag. The header of a browser pane will take the drop; the page may
  not. Check on the Mac mini. If the page does not, show the indicator
  only over the header, or overlay a transparent drop layer over the web
  view while a pane drag is on.
- **Hit testing.** The innermost element that takes `*Pane` gets the
  drag. Today only cards and tab labels take it. Something added inside a
  card that takes `*Pane` would take drags from the card.

## Later

Rex does these too. They are not in this change:

- **Spring loading.** Holding a dragged pane over a tab for a moment
  selects that tab, so that the pane can be dropped on one of its panes
  (`PaneDragTabSpringLoader`).
- **New tab.** Dropping a pane on the empty part of the tab strip moves it
  to a new tab (`PaneDragNewTabDrop`, `PaneDropPlaceholderView`).
- **Edges of the room.** Dropping on the outer edge of the tab's room
  splits at the root, so the pane takes a whole side of the tab.
