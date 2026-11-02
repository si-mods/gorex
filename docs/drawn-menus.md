# Drawn context menus on Windows and Linux

Status: built. MyGo's side is in the fork (`si-mods/mygo`, branch
`feat/draw-menus`) and GoRex's is in `menus.go`. Real-window checks are
open: the Mac mini comparison with `GOREX_DRAW_MENUS=1` and the Windows
11 check.

On macOS, GoRex's context menus are the system's menus: Liquid Glass,
rounded, with the system's type. On Windows they are Win32 popup menus.
MyGo draws their items in a Fluent style on Windows 11
(`internal/windows/menudraw.go`), but they still look out of place next to
GoRex's glass windows. On Linux they are the toolkit's menus.

On Windows and Linux, GoRex will draw its context menus itself: a glass
panel that looks like the macOS menu, so that the app looks the same on
every platform. macOS keeps the system's menus.

## What there is now

- `Element.ContextMenu(build)` fills a `ui.Menu` and hands it to the host,
  which shows the system's menu (`engine.showMenu` → `host.popupMenu` →
  `Backend.PopupMenu`; `TrackPopupMenuEx` on Windows).
- `Element.PopoverMenu(build)` (`ui/popovermenu.go`) takes the same
  `build` and draws the menu in the window, as an overlay. It already has
  everything a menu needs: submenus, check and radio marks, shortcuts,
  disabled items, separators, keys (arrows, Home, End, type to select,
  Enter, Escape, Left and Right for submenus), focus taken and given back,
  a press outside closes it, flipping where the window has no room,
  scrolling when it is taller than the window, and the menu roles for
  assistive technology.
- Its look comes from the theme only: `t.Background`, `t.Border`,
  `t.Radius + 2`, a fixed shadow, the accent on the highlighted item. An
  app cannot change it.
- GoRex calls `ContextMenu` in four places: the tab (`tabs.go`), the pane
  header and the browser header (`view.go`), and the workspace row
  (`workspaceview.go`). Other menus come from MyGo's own elements:
  - the terminal (`plugins/terminal/view.go`: Copy, Paste, …);
  - text inputs and selectable text (`engine.editMenu`: Cut, Copy, Paste,
    Select All);
  - markdown and rich text, which notes use.

  Every one of them goes through `ContextMenu` or `openMenu`.

## Design

Two parts: a switch and a style hook in MyGo (the fork,
`si-mods/mygo`), and the glass look and the switch turned on in GoRex.

### MyGo: draw every context menu

A setting of the window's `ui.Context`, which the view sets each frame
(a call that changes nothing is free), makes the window draw every
context menu that it would hand to the system:

```go
// SetDrawMenus makes the window draw its context menus, those of
// ContextMenu and of MyGo's own elements (text editing, the terminal),
// as PopoverMenu draws one, rather than show the system's.
func (c *Context) SetDrawMenus(on bool)
func (c *Context) DrawMenus() bool
```

- `ContextMenu` on a window with the switch on behaves as `PopoverMenu`:
  the same `build`, the same `Chosen` in the frame after.
- `editMenu` and `openMenu`, which build a `platform.Menu` without a
  `build` function, show it drawn too. `drawMenu` then takes the
  `platform.Menu` and the `commands` of the shown menu instead of calling
  `build`, and a chosen editing command goes to `menuCommand` as now.
- The menu opens where the system's would: at the pointer for a secondary
  click, and below the focused element for the menu key or Shift+F10.
- Not drawn: the web view's own menu (a page's context menu in a browser
  pane, `WindowHandler.ContextMenu` with `Native` items). Those items run
  inside WebView2, and only the system's menu can carry them. The menu bar
  and the tray menu are not context menus, so they keep the system's
  menus too.

### MyGo: a style for drawn menus

The theme gets a style for drawn menus. An app sets the parts it wants,
and MyGo keeps its own look for the parts left at zero:

```go
type MenuStyle struct {
	// Panel styles each menu's panel after MyGo set its layout: its
	// face, border, radius, shadow and padding. nil keeps MyGo's.
	Panel func(panel Element, level int)
	// RowHeight is the least height of an item's row, RowRadius the
	// radius of its highlight and RowPadding the room before and after
	// its content (horizontal only); 0 keeps MyGo's.
	RowHeight, RowRadius, RowPadding float32
	// Highlight and HighlightText color the item the pointer or the keys
	// are on; zero keeps the accent.
	Highlight, HighlightText Color
	// Separator colors the line between groups; zero keeps the border.
	Separator Color
	// TextSize is the size of an item's text; 0 keeps the theme's.
	TextSize float32
}
```

`Theme.Menu *MenuStyle` holds it; nil keeps MyGo's look. `menuDraw.panel` and `menuDraw.item`
read it. They do not change how the menu works.

### GoRex: the look

GoRex sets `Theme.Menu` from `colors`, matching the macOS 26 menu. These
values are a first guess; compare them with a native menu on the Mac mini
and match it:

| Part | Value |
|---|---|
| Panel | `glassPanel(panel, k.panel)` ([DESIGN.md, Glass](../DESIGN.md#glass)), radius 12, padding 5, border 1 `k.panelBorder`, shadow as the popovers' |
| Row | least height 24, radius 7, horizontal padding 10 |
| Text | 13, `k.text`; disabled `k.textFaint`; shortcut `k.textMuted`, right-aligned |
| Highlight | `k.accent`, text white, as macOS shows it |
| Separator | 1, `k.panelBorder`, inset 10, margin 5 above and below |
| Submenu | opens beside its item, its first row level with it; the item stays tinted (`k.tabHover`) while the pointer is in the submenu |

- With the Glass setting off, or where the window shows no glass
  (`windowGlass`), the panel takes the flat `k.panel` face, as the other
  popovers do (`glassPanel` already does this).
- Animation is not built: the menu shows and closes at once. MyGo has no
  hook for a menu's motion in `MenuStyle`, and the popovers' motion is
  GoRex's own transition on its overlays, which a menu of MyGo's does not
  go through. It can follow if `MenuStyle` gets one.

### GoRex: the switch

`menus.go`: `drawMenusOn(goos, env)` is true on Windows and Linux, and on
macOS only where `GOREX_DRAW_MENUS=1`, to compare the drawn menu side by
side with the native one on the Mac mini. `setupMenus`, called each frame
from the root of every window's view (the main windows and Settings),
passes it to `c.SetDrawMenus` and sets `Theme.Menu`. The highlight is
`k.accent` with white text.

GoRex's four `ContextMenu` calls do not change.

## Limits

- **In the window.** A drawn menu cannot go past the window's edges, as
  the system's can. `PopoverMenu` already flips to the other side where
  the window has no room, and scrolls when the menu is taller than the
  window. In a very small window a long menu scrolls.
- **One window.** A menu shows in the window it opened in; it does not
  float over other windows.
- **Pages.** A web page's menu in a browser pane stays the system's.

## Tests

MyGo (`ui`, headless, with `NewTester`):

- With `SetDrawMenus(true)`, a `ContextMenu` opens drawn and its item's
  `Chosen` reports in the next frame. Do the same for a text input's
  editing menu (Copy reaches the editor) and for the terminal's menu.
- `MenuStyle`: zero values give the panel and rows of today's
  `PopoverMenu` (its existing tests pass unchanged); set values reach the
  panel and the rows.
- With the switch off, nothing changes: the host's `popupMenu` is called.

GoRex:

- With drawn menus on, the pane header's and tab's menus open drawn, and
  choosing "Rename" starts the rename (`ui.NewTester`), and `Theme.Menu` is set (`TestDrawnMenus`,
  `TestMenuStyle`).
- `TestDrawMenusOn`: on by default on Windows and Linux, off on
  macOS unless `GOREX_DRAW_MENUS=1` is set.

Real windows:

- Mac mini, with `GOREX_DRAW_MENUS=1`: screenshot the drawn menu next to
  the native one (tab, pane header, terminal), dark and light, glass on
  and off. Match the metrics.
- Yibo-PC (Windows 11): the user checks the menus on their desktop. The
  agent cannot capture it: the user's session there is disconnected.

## Order of work

1. MyGo fork: `SetDrawMenus`, drawn editing and terminal menus,
   `MenuStyle`, tests. A PR to `si-mods/mygo`; the user opens the
   upstream PR if wanted.
2. GoRex: bump the fork, set `Theme.Menu`, turn the switch on, tests, Mac
   mini screenshots. A PR to `si-mods/gorex`.
