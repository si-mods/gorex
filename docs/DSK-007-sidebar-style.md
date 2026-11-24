---
name: DSK-007
description: "The user can set the sidebar's look, density and structure through flat dotted keys in settings.json, as VS Code's settings are. Use when you change the sidebar's colors, metrics, grouping or its settings."
metadata:
  created: "2026-11-23"
  updated: "2026-11-24"
---

# DSK-007: Sidebar style settings

Status: built (#1055).

This doc decides the settings.json keys that style the desktop sidebar. It extends the palette rules of `DESIGN.md` and the projects model of [MOD-002](MOD-002-projects.md). The keys are flat and dotted, as VS Code's settings are: `sidebar.rowHeight`, `sidebar.groupBy`, `sidebar.colorCustomizations`.

## Problem

The sidebar's placement, width and collapsed state are user settings (`tabPlacement`, `sidebarWidth`, `sidebarHidden`, `sidebarOpen` in `settings.go`). Its look is not. The colors come from `colors` in `style.go` (`groupFace`, `pillLift`, `liftShadow`, `track`, `tabSep`, `tabHover`, `tabActive`). The metrics are constants in `sidebar.go` and `projects_sidebar.go` (`sideRowH`, `sideLead`, `sideGap`, `hostHeaderH`, `projectRowH`, `foldRowH`, `groupPad`, `groupEdge`). The structure is fixed: a host section holds project rows, and each project holds its tab rows. A user who wants a denser list, a different tint or a flat list has no setting.

## Goal

- Flat `sidebar.*` keys in `settings.json` set the sidebar's metrics, grouping and colors. Every key is optional. An absent key keeps the current look.
- `sidebar.colorCustomizations` overrides only the palette entries the sidebar owns, as `workbench.colorCustomizations` overrides VS Code's `sideBar.*` tokens. Global palette entries (`text`, `accent`, `panel`, the status dots) stay global.
- The metrics take valid ranges. An out-of-range value falls back to the default.
- The structure change stays compatible with the saved `sidebarOpen` map.

## Non-goals

- No per-project or per-host styles. One style applies to the whole sidebar.
- No fonts. The sidebar keeps the app's type ramp of `tokens.go`, except `sidebar.fontSize` below.
- No Settings UI controls for the `sidebar.*` keys. [DSK-011](DSK-011-live-settings.md) adds a `settings.json` editor page instead.
- No iOS or web changes. Their sidebars keep their own settings.
- No host folding. `foldedHosts` names a state no current view reads, so a "fold hosts" option would promise a behavior that does not exist.

## Design

### Behavior

The user writes `sidebar.*` keys in `settings.json`. They take effect on the next view build, as every pref does. An unknown key or an invalid value changes nothing and adds a warning line to `app.log` once per load, not per frame.

```json
{
  "sidebar.rowHeight": 26,
  "sidebar.indent": 10,
  "sidebar.tabIndent": 8,
  "sidebar.fontSize": 12,
  "sidebar.groupPadding": 3,
  "sidebar.groupBy": "flat",
  "sidebar.groupBackground": "none",
  "sidebar.icons": "text",
  "sidebar.selection": "fill",
  "sidebar.colorCustomizations": {
    "sideBar.activeGroupBackground": "#2a2a3d",
    "sideBar.activeGroupTabBackground": { "light": "#ffffff", "dark": "#3a3a52" },
    "sideBar.trackBackground": "#00000033"
  }
}
```

The metric keys, each a number clamped to a range and reset to the default outside it:

| Key | Role | Range | Default |
| --- | --- | --- | --- |
| `sidebar.rowHeight` | `sideRowH`, the tab row | 24–48 | 32 |
| `sidebar.indent` | `sideLead`, the row's start padding | 0–24 | 9 |
| `sidebar.tabIndent` | a tab-level row's extra start padding | 0–24 | 0 |
| `sidebar.fontSize` | the sidebar's text size | 10–16 | the type ramp |
| `sidebar.groupPadding` | `groupPad`, the current-project face's inset | 0–12 | 4 |

`sidebar.groupBy` is `host` or `flat`, "" for `host`. `host` keeps a section per host as now. `flat` lists every shown host's projects and rows under the one list, without the host headers and bodies. Project rows, tab rows, fold rows and the `sidebarOpen` map all keep working.

`sidebar.icons` is `tiles` or `text`, "" for `tiles`. `text` draws no program tiles on tab rows and draws a project's folder glyph alone, without its tile face; the names' room shrinks to a tile's width so the rows still line up with the project rows' glyphs. `sidebar.selection` is `pill` or `fill`, "" for `pill`. `fill` paints a chosen row a plain face at `tabActive` with a `radiusM` corner — the look of a text-first list — instead of the glass pill of the other chosen controls.

`sidebar.groupBackground` is `face` or `none`, "" for `face`. `none` draws no face behind the current project and its tabs: the group's rows read on the sidebar's face, as every other project's do. A `sideBar.activeGroupBackground` of low alpha in `sidebar.colorCustomizations` dims the face instead of removing it.

A tab-level row (a tab's, another window's tab's, a project's New Tab row) starts at `sideLead` plus `sidebar.tabIndent`, so a project's tabs read under it. The project rows, fold rows and waiting rows keep the one leading edge.

A theme manifest can carry the same keys in a `sidebar` object, under the user's values ([DSK-008](DSK-008-theme.md)).

`sidebar.colorCustomizations` maps one of these role names to a color: `sideBar.activeGroupBackground`, `sideBar.activeGroupTabBackground`, `sideBar.activeGroupTabShadow`, `sideBar.trackBackground`, `sideBar.tabSeparator`, `sideBar.tabHoverBackground`, `sideBar.tabActiveBackground`. A value is `#RRGGBB` or `#RRGGBBAA`, or an object `{ "light": <hex>, "dark": <hex> }` for different values per appearance. A single value applies to both appearances. An unknown name or a bad value is dropped at load with a warning. The first build of this doc used the names `groupFace`, `pillLift`, `liftShadow`, `track`, `tabSep`, `tabHover` and `tabActive`. [DSK-008](DSK-008-theme.md) renamed them, and GoRex renames them in the user's file at load (`migrateSettings`; the original stays in `settings.json.bak`). A face that the user sets also sets the text on it: the resolver (`surface` in `style.go`) gives the row's text the ladder of the face's lightness.

### Model

`settings` in `settings.go` gains the flat fields with their dotted JSON names: `SidebarRowHeight`, `SidebarIndent`, `SidebarTabIndent`, `SidebarFontSize`, `SidebarGroupPad`, `SidebarGroupBy`, `SidebarGroupBackground`, `SidebarIcons`, `SidebarSelection`, `SidebarColors`. `SidebarColors` keeps the raw messages so the file round-trips the user's exact text; `checkSidebarColors` validates role names and values at load and drops the bad ones with a log line.

Resolution is read-time and cheap: `sidebarRowH`, `sidebarLead`, `sidebarTabLead`, `sidebarFont`, `sidebarGroupPad` and `sidebarGroupEdge` return the pref or the constant; `sidebarPalette(k, dark)` returns a copy of the palette with the valid overrides applied. The sidebar applies the palette once in `App.sidebar`, so every row, pill and face under it draws with it and the window's other surfaces keep the base palette. The resolved metrics are data at use, not source literals, so `TestDesignTokens` sees nothing new.

Compatibility: an older build ignores the dotted keys on read (unknown JSON fields) and drops them when it rewrites `settings.json`, the same contract every setting has. No migration for the dotted keys: every key is optional and additive. The color names are the exception: an older build does not know the role names and drops them at load with a warning.

### Code

- `settings.go`: the `sidebar.*` fields, the range checks beside the `sidebarWidth` check, `clampMetric`.
- `sidebar.go`: the metric and palette helpers, `checkSidebarColors`, `sidebarColorTokens`, `parseStyleColor`, `hexColor`; `App.sidebar` applies the palette; `sidebarIconsText`/`sidebarFillSel`/`sidebarChosen` switch the tiles and the chosen row's paint; `tilesReserve` shrinks under `sidebar.icons` "text"; `sidebarTabLead` leads the tab rows; `sidebarGroupFaced` reads `sidebar.groupBackground`.
- `projects_sidebar.go`: `projectRowH` becomes a function of `sidebarRowH`; the group face reads `sidebarGroupPad`/`sidebarGroupEdge`; the font sizes read `sidebarFont`; `projectsList` reads `sidebar.groupBy`; `projectFolderTile` draws the glyph alone under `sidebar.icons` "text".
- Tests: a parse test per field class in `settings_test.go`; a view test in `sidebar_test.go` that a styled sidebar draws the override and an invalid value falls back.

## Alternatives

- **A nested `sidebarStyle` object**: rejected. The user asked for the VS Code settings shape: flat dotted keys plus a `*Customizations` map.
- **init.lua (`gorex.sidebar_style`)**: rejected. The user asked for `settings.json`, and the Lua table would duplicate the prefs writer for one field set.
- **Theme-pack distribution**: rejected as the entry point. A pack can carry the same values later; the settings contract comes first so packs have something to write.
- **Global palette overrides**: rejected. The sidebar-specific palette entries give the requested surface without letting a sidebar setting recolor dialogs and menus.
- **`sidebar.foldHosts`**: rejected. `foldedHosts` is written but never read by a view, so the option would name a behavior that does not exist.
