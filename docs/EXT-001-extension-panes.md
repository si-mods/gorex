---
name: EXT-001
description: "Extension packs give pane kinds. A pack holds a web page that runs in a web view of the pane and calls a scoped bridge. The Notes and Browser panes move out of the app into two registry packs. Use when you change the extension runtime, a pane kind, the bridge API or the pack format."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
---

# EXT-001: Extension panes

Status: proposal.

This doc decides the shape of functional extensions. An extension pack installs through `gorex pack install`, like an icon pack or a theme pack. A pack gives pane kinds. Each pane of that kind shows the pack's page in a web view. The page calls a scoped bridge for storage, pane state and navigation. The Notes and Browser panes move out of `package main` into two packs on the registry. This doc extends the pack system of [DSK-008](DSK-008-theme.md) and the pane model of [MOD-002](MOD-002-projects.md).

## Problem

The app has two pane kinds beside the terminal: the note and the browser. They are code in `package main`:

- `note.go` and `notesave.go` hold the note pane and its file writer. `browser.go` holds the browser pane.
- `state.go` gives `Pane` the fields `browser` and `note`. Code tests them with `!= nil` in `view.go`, `closeconfirm.go`, `teleport.go`, `paste.go`, `commands.go` and more. A new pane kind needs a change in each place.
- `layout.Node` carries one field set for each kind: `Kind`, `Note`, `NoteSize`, `NotePreview` for a note, `URL` and `ID` for a browser.

The pack system ships data only. The registry's `index.json` lists icon packs and theme packs. A pack cannot give behavior. The Lua configuration registers actions, bindings and event handlers, but no pane and no drawn surface (see [REF-002](REF-002-lua.md)).

The goal is a pane kind that comes from a pack. The Notes pane and the Browser pane become the first two packs.

## Goal

- The registry's `index.json` lists packs of `kind: "extension"`. `gorex pack install` installs one into `<config>/exts/<id>/`. The Extensions page lists it with enable and remove.
- An enabled pack registers pane kinds. Its kinds appear in the New Tab menu, the Split menu, the palette and `gorex action run`.
- A pack's pane shows the pack's page in a trusted web view. A pack that asks for a `web` slot gets a second, untrusted web view for pages from the internet.
- The page calls a scoped bridge: its own storage, the pane's state, whitelisted actions, and the web slot. It can reach nothing else.
- The Notes and Browser features keep their behavior and their serialized layout shape after the move.
- A pane whose pack is absent or disabled shows a placeholder. The placeholder offers to install or enable the pack.

## Non-goals

- The infinite canvas stays in `package main`. A canvas is a tab layout mode that draws native panes, not a pane kind. A web view cannot draw a terminal pane. A scriptable layout engine is a separate doc, if ever.
- No Lua pane kinds. The Lua configuration keeps its scope: actions, bindings, modes, events and icons.
- iOS and the web client draw no extension panes. Their server sends the `Unavailable` placeholder for a kind they do not have, as it does for a note today.
- No pack signing and no review flow in this phase. The registry lists packs that its owners accept by hand, as it does for icon packs. A later doc decides trust for third-party packs.
- Extension panes open no session, take no resume hint and join no teleport.

## Design

### Behavior

A person installs a pack with `gorex pack install gorex-notes`, or from the Extensions page, which shows the registry's extension packs in a Featured section. The pack lands in `<config>/exts/gorex-notes/` and registers its pane kinds at the next start. The New Tab menu and the Split menu then list "Note" as they do today, with the pack's own commands beside them.

A disabled or absent pack leaves its panes as placeholder cards. The card names the pack and offers Enable or Install. The layout keeps the pane's node and its state, so a pack that comes back finds its pane as it was.

The Notes pack's pane shows a text editor in a web view. It reads and writes `notes/<id>.txt` in GoRex's data directory through the bridge, as the note pane does today. The Browser pack's pane shows its chrome, an address field, in the pack's own page. The page under it is a separate untrusted web view that the pack steers through the bridge.

### Model

**The manifest.** An extension pack is a folder with `ext.json`, `index.html` and its assets:

```json
{
  "id": "gorex-notes",
  "name": "Notes",
  "version": "1.0.0",
  "engine": "webview",
  "api": 1,
  "panes": [
    {
      "kind": "note", "title": "Note", "icon": "sticky-note",
      "fields": ["note", "noteSize", "notePreview"],
      "files": ["{note}.txt"],
      "opaque_tiled": false
    }
  ],
  "storage": { "dir": "notes" },
  "permissions": ["state", "storage"],
  "commands": [
    { "id": "new", "title": "New Note Tab", "kind": "note", "placement": "tab" },
    { "id": "split", "title": "Split with Note", "kind": "note", "placement": "split" },
    { "id": "preview", "title": "Toggle Note Preview", "event": "toggle-preview" }
  ]
}
```

- `id` names the pack. It is the folder name under `<config>/exts/` and the id in the registry.
- `api` is the bridge version. A pack that needs a newer bridge than the app's loads as disabled.
- `panes[].kind` is the pane-kind name this pack serves. The name is the `Kind` field of `layout.Node` (see Serialization).
- `panes[].fields` names the `layout.Node` fields the pack's state covers: `note`, `noteSize`, `notePreview`, `url`, `id`. A field the layout keeps is the page's state; `id` is the pane's own, made at spawn, and no SetState of the page's.
- `panes[].files` names the storage files a pane owns, patterns over its fields such as `{note}.txt`. Closing the pane for good removes them, as a note's file went.
- `panes[].opaque_tiled` is whether the pane draws opaque over the glass window. The default is true; the Notes pack says false, as the note pane did.
- A `browser` kind's node passes the layout filter to other clients, as a browser's `url` did; another kind's node arrives as Unavailable. The session server applies the rule by kind name — it sees no packs — so a `shareable` manifest key waits on the server learning of kinds.
- `panes[].chrome_height` is the pack page's height over a `web` slot, 0 for the pane header's height.
- `storage.dir` names the pack's data directory inside GoRex's data directory. The default is `exts/<id>/data`. The Notes pack declares `notes`, so it reads the notes that exist today.
- `slots` (absent here) names extra surfaces. The only slot is `web`: an untrusted web view under the pack's page, for pages from the internet.
- `permissions` lists the bridge areas the pack may call: `state`, `storage`, `invoke`, `web`, `env`.
- `commands` declares the pack's commands. `placement` puts a pane-spawning command in the New Tab or Split menu. `action` delegates to a command id the bridge's Invoke allows. `event` names a string the page of the pack's focused pane gets as `ext.command`, for the commands a page answers itself, as a preview toggle's. `action` and `event` are exclusive.

**The pane host.** A pane of a registered kind gets an `extPane` on `Pane`, which replaces `browser` and `note`. The host holds:

- a trusted web view that shows the pack's `index.html`,
- an untrusted web view when the pack declares the `web` slot, drawn under the page in the pane's body,
- the pane's serialized `state`, which the page reads and writes through the bridge.

The host draws the pack's page and the web slot in the pane's body, as `browserBody` placed the browser's web view. The pane's header stays native: it shows the pane's title, icon and buttons, as every pane's does. The Browser pack puts its address field in its page, `chrome_height` tall, below the native header.

**The bridge.** The app binds one service, `Extension`, for its trusted pages. A call names the method, and the bridge resolves the caller: `mygo`'s `CallerPage` gives the page, the page's web view gives the pane, and the pane gives the pack. A call the pack has no permission for fails. The bridge's first surface:

| Call | Permission | Does |
| --- | --- | --- |
| `State()` | `state` | The pane's `state` object. |
| `SetState(state)` | `state` | Replaces the pane's `state` and saves the layout. Other clients see the new state as they see a browser's URL today. |
| `ReadFile(name)` | `storage` | The file `name` in the pack's storage directory. |
| `WriteFile(name, data)` | `storage` | Writes the file. Names are confined to the directory. |
| `DeleteFile(name)` | `storage` | Removes the file. |
| `ListFiles()` | `storage` | The storage directory's file names. |
| `Invoke(action, args)` | `invoke` | Runs a built-in action the manifest lists, as `gorex.invoke` does in Lua. |
| `WebNavigate(url)` | `web` | Loads `url` in the pane's web slot. The same `browserURL` rule as today decides what may load. |
| `WebURL()`, `WebBack()`, `WebForward()`, `WebReload()` | `web` | Where the slot is, and its navigation. |
| `Env()` | `env` | The locale, the appearance and the theme's surface colors, for the page to match the chrome. |

Events reach the page through `mygo`'s page events: `ext.state` carries the pane's fields when a layout another client wrote changes them, `ext.web` reports the slot's page, and `ext.command` carries a command's `event` string.

**Serialization.** A pane's `Kind` field is the pane-kind name a pack registers. The two first packs keep the layout shape the app writes today, so old builds read their panes as they do now:

- A note pane stays `{"kind":"note","note":"<id>","noteSize":n,"notePreview":b}`. The Notes pack registers kind `note`.
- A browser pane gets `{"kind":"browser","url":"<url>","id":"<id>"}`. The `kind` field is new, and old builds ignore it and read `url` as before. The Browser pack registers kind `browser`.

A pack declares which fields of the node are its state, so the code that writes and reads a layout asks the pack's manifest, not a switch in `state.go`. A later doc adds a generic `state` object for third-party kinds that need more than the fixed fields. See Serialization options.

A pane of a kind no enabled pack serves becomes a placeholder pane. The node and its state stay in the layout.

**Sharing.** The server's `filterLayoutNode` treats a leaf with no session by its kind:

- A `browser` node passes, with its state fields, as a `url` pane did; other kinds arrive as Unavailable.
- Any other kind becomes `Unavailable`, as a `note` becomes today.

The server passes a `browser` node by kind name, as it did the built-in's; a `shareable` manifest key arrives with the server learning of kinds.

### Code

- `internal/ext` (new): the manifest type and its validation; the pack registry (`kind` to pack); `extPane`, the host that owns a pane's web views; the bound `Extension` service; the storage jail.
- `internal/iconpack`: `KindExtension`, the `extensions` array of `RegistryIndex`, install and remove for `<config>/exts/`.
- `internal/cli/pack.go`: `gorex pack install` and `list` learn the extension kind.
- `state.go`: `Pane.ext` replaces `browser` and `note`. `load` resolves `Kind` and `URL` through the registry. `paneKey` keys an ext pane by `ext:<kind>:<node id>`.
- `view.go`: the pane body and the header ask the kind's host. The `!= nil` tests become `p.kind()` queries.
- `commands.go`: pack commands join the palette and the menus on registration, as Lua actions do.
- `note.go`, `notesave.go`, `browser.go`: removed as their packs land. The notes' file writer moves into the pack's page and the bridge's `WriteFile`.
- `internal/rex/layout_filter.go`: the shareable rule above.
- The registry (`si-mods/gorex-registry`): an `extensions` array in `index.json`; the `gorex-notes` and `gorex-browser` repositories.

## Serialization options

This change writes two things an older build reads: a `kind` value it does not know on the browser pane, and, in a later phase, a generic `state` for third-party kinds. `docs/irreversible.md` applies. The options, and the one this doc takes:

1. **Recommended: keep the legacy field set for the two first packs.** `kind` is the registry's namespace. A note keeps `kind:"note"` with `note`, `noteSize` and `notePreview`; a browser keeps `url` and `id` and gains `kind:"browser"`. Old builds read both as today. A downgrade loses nothing. Third-party kinds get `{"kind":"ext","ext":"<pack>","state":{...}}` later, when one ships.
2. **Move every ext pane to `{"kind":"ext","ext","state"}` now.** One shape for all extension panes. An old build reads `kind:"ext"` as a dead terminal pane, and a save from that build drops `ext` and `state`, which erases a note's link to its file and a browser's page. Rejected: the uniform shape is not worth the downgrade damage.
3. **Add `ext` and `state` beside the legacy fields and write both.** Old builds keep working and new fields survive a downgrade's save only if `layout.Node` keeps unknown members on a round trip, which it does not today. Rejected: it duplicates state for no gain over option 1, and the unknown-member retention is its own format change.

Check afterwards: a build before this change opens a layout the new build wrote and shows the same panes, and the new build reads a pre-change layout into the packs' panes. If a check fails, the pack that the failed kind belongs to stays absent and its panes show the placeholder, with no data written.

## Security

- A pack's page is a trusted page, so it can call bound Go services. The `Extension` bridge is the only service the app exposes to it. The bridge resolves the caller's pane and pack and refuses a call outside the manifest's `permissions`. A page of another origin or another window has no pane and gets no bridge.
- `ReadFile` and `WriteFile` stay inside the pack's storage directory. `storage.dir` is a name the manifest declares and the app confines under GoRex's data directory; `..` and absolute paths fail.
- The `web` slot is an untrusted web view: it calls no Go method, asks no permission and downloads nothing, as the browser pane's page does today.
- `SetState` caps the state at a fixed size and stores only JSON values.
- A registry pack is code that runs in the app. The registry accepts packs by hand in this phase. Signing and review are a later doc (Open questions).

## Migration

- Existing layouts need no migration. `kind:"note"` and `url` panes read into the packs' panes when the packs are installed, and into placeholders when they are not.
- `notes/*.txt` stays where it is. The Notes pack's `storage.dir` points at it.
- A fresh install has no Notes or Browser pane kinds until the packs install. The Extensions page offers them in its Featured section, and a placeholder pane offers the same install.

## Tests

- Layout round trip: a layout with `kind:"note"`, `kind:"browser"` and `url` nodes reads into the same panes a build before this change made, and writes the same fields back.
- Missing pack: a layout with `kind:"note"` and no Notes pack gives placeholder panes, and keeps the node and its fields on save.
- Disabled pack: same as missing, plus an enable action on the placeholder.
- Bridge scope: a call from a page with no pane fails; a call outside the manifest's permissions fails; `WriteFile` cannot leave the storage directory.
- Web slot: `WebNavigate` applies the `browserURL` rule; the slot's page cannot call the bridge.
- Notes parity: the pack's pane edits, previews and saves `notes/<id>.txt` as the note pane did (behavior test through the debug hooks).
- Browser parity: the pack's pane navigates, shows its address field and syncs `url` to a second client as the browser pane did.

## Phases

1. `internal/ext` with the manifest, the host, the bridge and the storage jail; `KindExtension` in `internal/iconpack` and the CLI; the registry's `extensions` array; the placeholder pane; `Pane.ext` and the registry dispatch in `state.go` and `view.go`. No feature moves yet. The placeholder path is proven by an absent kind.
2. The `gorex-notes` pack: the registry repo, the manifest, the page and the bridge's `storage` and `state` areas. The app removes `note.go` and `notesave.go`; `kind:"note"` resolves through the registry.
3. The `gorex-browser` pack: the `web` slot and the `web` bridge area; `kind:"browser"` and the kept `url`/`id` fields; the app removes `browser.go`.
4. Clean-up: `Pane.browser` and `Pane.note` are gone, the `!= nil` tests are gone, and the placeholder and pane-kind dispatch cover every non-terminal pane. A `docs/lessons.md` entry records what the extraction changed.

## Open questions

1. A generic `{"kind":"ext","ext","state"}` node for third-party kinds. Decided when the first third-party kind ships, by its own doc and `docs/irreversible.md` stop.
2. Pack trust: signing, a review flow, or a prompt on install. Decided with the first third-party pack.
3. The pack header surface: whether a pack draws its own pane header instead of the native one, and how. The Browser pack starts with its chrome in the page.
4. Bridge versioning: how `api` bumps and what an old pack on a new bridge gets. Answer with the first breaking bridge change.
