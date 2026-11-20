# Drawn context menus on Windows and Linux

Status: built (#153). The Mac comparison and the Windows 11 check are open.

MyGo's side is in the fork (`si-mods/mygo`, branch `feat/draw-menus`) and GoRex's side is in `menus.go`. Two real-window checks are open. One is the comparison on a Mac with `GOREX_DRAW_MENUS=1`. The other is the check on Windows 11.

## Problem

On macOS, the context menus of GoRex are the menus of the system: Liquid Glass, rounded, with the type of the system. On Windows they are Win32 popup menus. MyGo draws their items in a Fluent style on Windows 11 (`internal/windows/menudraw.go`). But they still look out of place next to the glass windows of GoRex. On Linux they are the menus of the toolkit.

Before #153 the code worked as follows:

- `Element.ContextMenu(build)` fills a `ui.Menu` and hands it to the host, which shows the menu of the system (`engine.showMenu` → `host.popupMenu` → `Backend.PopupMenu`, and `TrackPopupMenuEx` on Windows).
- `Element.PopoverMenu(build)` (`ui/popovermenu.go`) takes the same `build` and draws the menu in the window, as an overlay. It already has everything that a menu needs:
  - Submenus, check and radio marks, shortcuts, disabled items and separators.
  - Keys: arrows, Home, End, type to select, Enter, Escape, and Left and Right for submenus.
  - Focus taken and given back.
  - A press outside closes the menu.
  - Flipping where the window has no room, and scrolling when the menu is taller than the window.
  - The menu roles for assistive technology.
- Its look comes from the theme only: `t.Background`, `t.Border`, `t.Radius + 2`, a fixed shadow, and the accent on the highlighted item. An app cannot change it.
- GoRex calls `ContextMenu` in these places:
  - The tab (`tabs.go`).
  - The pane header and the browser header (`view.go`).
  - The canvas (`canvas.go`).
  - The host and project rows of the sidebar (`projects_sidebar.go`).
  - The color mark of a space (`spaces.go`).

  Other menus come from the own elements of MyGo:
  - The terminal (`plugins/terminal/view.go`: Copy, Paste, …).
  - Text inputs and selectable text (`engine.editMenu`: Cut, Copy, Paste, Select All).
  - Markdown and rich text, which notes use.

  Every one of these goes through `ContextMenu` or `openMenu`.

## Goal

On Windows and Linux, GoRex draws its context menus itself. The menu is a glass panel that looks like the macOS menu, so that the app looks the same on every platform. macOS keeps the menus of the system.

## Design

The design has two parts. MyGo (the fork, `si-mods/mygo`) gets a switch and a style hook. GoRex gets the glass look and turns the switch on.

### MyGo: draw every context menu

A setting of the `ui.Context` of the window makes the window draw every context menu that it would hand to the system. The view sets it each frame (a call that changes nothing is free):

```go
// SetDrawMenus makes the window draw its context menus, those of
// ContextMenu and of MyGo's own elements (text editing, the terminal),
// as PopoverMenu draws one, rather than show the system's.
func (c *Context) SetDrawMenus(on bool)
func (c *Context) DrawMenus() bool
```

- `ContextMenu` on a window with the switch on behaves as `PopoverMenu`: the same `build`, the same `Chosen` in the frame after.
- `editMenu` and `openMenu` build a `platform.Menu` without a `build` function. The window draws these menus too. `drawMenu` then takes the `platform.Menu` and the `commands` of the shown menu instead of calling `build`. A chosen editing command goes to `menuCommand` as before.
- The menu opens where the menu of the system would open. For a secondary click it opens at the pointer. For the menu key or Shift+F10 it opens below the focused element.
- Not drawn: the own menu of the web view (the context menu of a page in a browser pane, `WindowHandler.ContextMenu` with `Native` items). Those items run inside WebView2, and only the menu of the system can carry them. The menu bar and the tray menu are not context menus, so they keep the menus of the system too.

### MyGo: a style for drawn menus

The theme gets a style for drawn menus. An app sets the parts that it wants. MyGo keeps its own look for the parts that stay at zero:

```go
type MenuStyle struct {
	// Panel styles each menu's panel after MyGo set its layout: its
	// face, border, radius, shadow and padding. nil keeps MyGo's.
	Panel func(panel Element, level int)
	// RowHeight is the least height of an item's row, RowRadius the
	// radius of its highlight and RowPadding the room before and after
	// its content (horizontal only). 0 keeps MyGo's.
	RowHeight, RowRadius, RowPadding float32
	// Highlight and HighlightText color the item the pointer or the keys
	// are on. Zero keeps the accent.
	Highlight, HighlightText Color
	// Separator colors the line between groups. Zero keeps the border.
	Separator Color
	// TextSize is the size of an item's text. 0 keeps the theme's.
	TextSize float32
}
```

`Theme.Menu *MenuStyle` holds it. A nil value keeps the look of MyGo. `menuDraw.panel` and `menuDraw.item` read it. They do not change how the menu works.

### GoRex: the look

GoRex sets `Theme.Menu` from `colors` (`menuStyleOf` and `menuTheme` in `menus.go`). The look matches the macOS 26 menu. The values below are the values of the code. They started as a first guess. Compare them with a native menu on a Mac and match it:

| Part | Value |
|---|---|
| Panel | `glassPanel(panel, k.panel)` ([DESIGN.md, Glass](../DESIGN.md#glass)), radius 12 (`radiusL`), padding 6, border 1 `k.panelBorder`, shadow as the popovers' (`popoverShadow`) |
| Row | least height 24, radius `concentric(radiusL, 6)`, horizontal padding 10 |
| Text | 13, `k.text`. Disabled `k.textFaint`. Shortcut `k.textMuted`, right-aligned |
| Highlight | `k.accent`, with the text color of `accentText(dark)`: dark on the light accent of a dark window, white on the other |
| Separator | 1, `k.panelBorder`, inset 10, margin 5 above and below |
| Submenu | opens beside its item, its first row level with it. The item stays tinted (`k.tabHover`) while the pointer is in the submenu |

- With the Glass setting off, or where the window shows no glass (`windowGlass`), the panel takes the flat `k.panel` face. The other popovers do the same (`glassPanel` does this already).
- Animation is not built. The menu shows and closes at once. MyGo has no hook for the motion of a menu in `MenuStyle`. The motion of the popovers is the own transition of GoRex on its overlays, and a menu of MyGo does not go through it. Animation can follow if `MenuStyle` gets a hook for it.

### GoRex: the switch

`menus.go`: `drawMenusOn(goos, env)` is true on Windows and Linux. On macOS it is true only where `GOREX_DRAW_MENUS=1`, to compare the drawn menu side by side with the native menu on a Mac. `setupMenus` passes the result to `c.SetDrawMenus`. The view of every window (the main windows and Settings) calls `setupMenus` each frame. The theme of the window (`widgetTheme`, or `menuTheme`) carries `Theme.Menu`.

The `ContextMenu` calls of GoRex do not change.

### Limits

- **In the window.** A drawn menu cannot go past the edges of the window, as the menu of the system can. `PopoverMenu` already flips to the other side where the window has no room, and scrolls when the menu is taller than the window. In a very small window a long menu scrolls.
- **One window.** A menu shows in the window that it opened in. It does not float over other windows.
- **Pages.** The menu of a web page in a browser pane stays the menu of the system.

## Tests

MyGo (`ui`, headless, with `NewTester`):

- With `SetDrawMenus(true)`, a `ContextMenu` opens drawn, and the `Chosen` of its item reports in the next frame. Do the same for the editing menu of a text input (Copy reaches the editor) and for the menu of the terminal.
- `MenuStyle`: zero values give the panel and rows of the `PopoverMenu` of before (its existing tests pass unchanged). Set values reach the panel and the rows.
- With the switch off, nothing changes: the `popupMenu` of the host is called.

GoRex:

- With drawn menus on, the menus of the pane header and of the tab open drawn. Choosing "Rename" starts the rename (`ui.NewTester`). `Theme.Menu` is set (`TestDrawnMenus`, `TestMenuStyle`).
- `TestDrawMenusOn`: on by default on Windows and Linux. Off on macOS unless `GOREX_DRAW_MENUS=1` is set.

Real windows:

- A Mac, with `GOREX_DRAW_MENUS=1`: take a screenshot of the drawn menu next to the native menu (tab, pane header, terminal). Do this dark and light, glass on and off. Match the metrics.
- A Windows 11 test PC: the user checks the menus on the desktop. The agent cannot capture it, because the session of the user there is disconnected.

## Phases

1. MyGo fork: `SetDrawMenus`, drawn editing and terminal menus, `MenuStyle`, tests. This is a PR to `si-mods/mygo`. The user opens the upstream PR if wanted.
2. GoRex: bump the fork, set `Theme.Menu`, turn the switch on, tests, Mac screenshots. This is a PR to `si-mods/gorex` (#153).
