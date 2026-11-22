---
name: DSK-006
description: "The desktop shows a shared empty-state component where a surface has nothing to show: four sizes, one drawn wireframe, and the places that use them. Use when you change an empty, loading, no-result or failed surface."
metadata:
  created: "2026-11-22"
  updated: "2026-11-22"
---

# DSK-006: Empty states

Status: built.

## Problem

Several surfaces show nothing or a bare line when they have nothing to show. Each says it in its own way:

- The main area of a window with no tab is a blank gradient (`panes` in `view.go`).
- The command palette says "No matches" with no query named (`palette` in `commands.go`).
- The find bar counts "0 of 0" when a query matches nothing (`findBar` in `view.go`).
- The theme picker shows an empty grid when the filter matches nothing (`themeSettings` in `settingsview.go`).
- The project sidebar and the project picker show a header or nothing when a host has no projects (`projectsList`, `pickerList`).
- The browser pane shows one line when its page cannot be shown (`browserBody` in `view.go`).

The words come and go with no shared shape. The states do not name the next step. A user cannot tell a blank area from a broken one.

## Goal

One component draws the empty states. `empty.go` gives four sizes: `page`, `card`, `row` and `menu`. Every surface above uses one of them. Each state names itself in a title, says what the place is for in one line where room allows, and offers up to two actions where a next step exists.

## Non-goals

- No change to what counts as empty. Loading, data and navigation logic stay.
- No empty state inside a pane that has content: a terminal with no scrollback match is a find-bar state, not a pane state.
- iOS has its own surfaces and is not in scope.

## Design

### The component

`empty.go` holds the component. A call picks a size and gives a title, a note, and up to two actions.

| Size | Shape | Where |
| --- | --- | --- |
| `page` | A picture, a title in `fontTitle`, a note in `fontBody` `textFaint`, and up to two buttons. The group is centered and sits about 40% down the area. | the main area with no tab, the failed browser pane |
| `card` | One row: a 40×40 tile with a 22 DIP icon in `iconMuted`, the title, and a ghost button at the end. The tile uses `radiusM` over `k.hover`. | the project sidebar when a host has no projects |
| `row` | One line of `textFaint`, with an optional ghost button at the end. | the project picker with no entries |
| `menu` | One faint line. No picture, no button. | the command palette, the theme picker, the New Project sheet's status lines |

The primary action uses `ui.PrimaryButton`. A ghost action is a link: accent text in `fontBody` weight 500, `radiusS` hover in `k.hover`, as the disclosure rows of `remoteview.go` draw it. Every title and note carries `Label`. Every action is a `RoleButton` with `Label` and the pointer cursor. The picture is decorative and carries no label.

### The picture

The picture is a small wireframe of the place, drawn with `ui.Painter` rectangle operations, not an SVG. It shows two or three card shapes:

- A shape is `k.surface` with a 1px `k.cardBorder` outline, `radiusM` scaled to the shape, and a soft `k.shadow`.
- A title placeholder bar is `k.emptyMark` at 52% alpha.
- A body placeholder bar is `k.emptyMark` at 28% alpha.
- An empty slot is `k.emptyMark` at 14% alpha.
- At most one accent element shows, and only when the state has a next step: a round button in `k.emptyMark` with a white plus.

`k.emptyMark` is the one new color this adds: `#7a869d` in a light window, `#9aa4b8` in a dark one, the slate of the placeholder bars and the mark. For the window with no tab, the wireframe is a mini window: a tab strip of bars on top and one terminal card with a prompt line.

### Kinds of states

| Kind | Meaning | Example |
| --- | --- | --- |
| First use | Nothing exists yet; the action makes the first. | No projects yet. |
| No results | A query or filter matched nothing; the title names the query. | No matches for "x". |
| Failed | Something did not load; the action tries again. | The page could not be shown. |
| Loading | Data has not come yet; a faint line says so. | Loading… |
| None needed | The state is a good one; no action shows. | Nothing needs you. |

### Where each state shows

| Surface | Size | Title | Actions |
| --- | --- | --- | --- |
| Main area, no tab | `page` | No tab open | New Tab (primary), Command Palette… (ghost) |
| Browser pane, no page | `page` | The page could not be shown. | Reload (ghost), only when the pane has an address |
| Palette, no match | `menu` | No matches for "{query}" | none |
| Palette, needs list | `menu` | Nothing needs you | none |
| Find bar, no match | `row` | No matches | none |
| Theme picker, no match | `menu` | No themes match "{query}". | none |
| Project sidebar, no projects | `card` | No projects yet. | New Project… (ghost) |
| Project picker, no entries | `row` | No projects yet. | New Project… (ghost) |
| New Project sheet, states | `menu` | Loading… and the other status lines | none |

The picker keeps its New Project… menu row at the bottom. The sidebar's host section shows the card when it has no project groups, no waiting rows and no tabs with no project. When the window shows a session with no project and no tabs, the card takes the place of the bare group.

### Words

A title names the state in sentence case. It does not apologize and does not blame. Where a query caused the state, the title names it in quotes. All strings come from the catalog, in English and Simplified Chinese.

The same change puts the hardcoded strings of `promptsheet.go` (Cancel, Other, Send) and of the shortcut recorder in `settingsview.go` (Press the new shortcut for …, Cancel, Change) in the catalog.

## Tests

- `ui.NewTester`: the main area shows the empty-state title when the window has no tab; the palette shows "No matches for" after a query that matches nothing; the find bar shows "No matches" on zero results.
- Screenshots: the no-tab window, the palette, the theme picker and the hosts page, before and after, in light and dark.
- `TestDesignTokens` keeps the new colors out of literals: the bars, tile and shadows take tokens and `colors` fields.

## Alternatives

- One function with size options over four exported helpers: the sizes share the parts but not the shape, so four small helpers over shared pieces keep the call sites flat.
- An SVG picture: rejected, because the picture must take the window's colors. Drawn bars take `k` directly; an SVG would be tinted or redrawn per theme.
