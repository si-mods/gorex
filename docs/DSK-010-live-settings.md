---
name: DSK-010
description: "GoRex watches settings.json and applies a change while it runs, and the Settings window gains a settings.json page that edits the file in a code editor. Use when you change settings load, save or watch, or the settings.json editor."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# DSK-010: Live settings and the settings.json page

Status: built (#1071).

This doc decides two changes that work together. The app watches `settings.json` and applies an outside edit while it runs. The Settings window gains a settings.json page that edits the file in a code editor (`ui/editor`). It extends [DSK-007](DSK-007-sidebar-style.md), whose non-goals named the follow-up.

## Problem

`loadSettings` runs once, at the start of the app (`main.go`). A user who edits `settings.json` by hand must restart the app to see the change. The file's own watcher problem is worse: the app writes the file too (`saveSettings`, the migration of [DSK-009](DSK-009-terminal-settings.md)), so a naive reload can re-read the app's own write, and a half-written file can damage `prefs` in place.

The keys that have no control in the Settings window — the `sidebar.*` keys of DSK-007 among them — are edited only by hand, in an outside editor, against a file the user must find first.

## Goal

- An outside edit of `settings.json` applies while the app runs, with no restart.
- A write of the app itself does not reload: no loop, no double apply.
- A file that is not valid JSON changes nothing and logs one line.
- The Settings window has a page that shows `settings.json` in a code editor, with syntax colors. Saving the text writes the file and applies it, as an outside edit does.
- The page follows an outside change: a clean buffer reloads the file, a dirty one says the file changed and offers Revert.

## Non-goals

- No structured controls for the `sidebar.*` keys. The user chose the file's own editor over a form; a form can follow if the fields hold.
- No schema help in the editor: no completions or diagnostics from a language server.
- No watch of other files. `hosts.json` and `layout.json` have their own rules.
- No web or iOS settings. Their settings keep their own stores.
- No merge of a dirty buffer and an outside change. Revert discards the buffer's edits; the user says when.

## Design

### Behavior

The app starts the watch when the store starts, after `loadSettings`. The user edits `settings.json` in an editor outside the app. The sidebar, the terminals, the appearance and the language take the new values within the debounce of the watch, under a second. An edit the user makes in the Settings window writes the file and applies through `setPref`/`setWindowPref`, as today; the watch sees the write, finds the app's own bytes, and does nothing.

A reload does what a start does: migrate the file when it holds old keys, decode it, check each value, then apply. A file that does not decode keeps the settings that run now and logs one line. A second event with the same bytes does not decode again.

The Settings window's sidebar gains a `file` section. Its page has a bar over a code editor:

- The bar shows the hint `Edits apply on save.`, the buffer's state (`Unsaved`, `Saved` for a breath, `Not valid JSON` after a failed Save, or `Changed on disk` after an outside write), a Save button and a Revert button.
- The editor shows the file's text with `editor.View`, JSON colors, line numbers and the default bindings of `ui/editor`. Its `Theme` stays nil, so `editor.View` resolves the window's default code theme every frame and follows the appearance. It is editable, not read-only.
- Save decodes the buffer's text. A text that does not decode writes nothing and marks the bar. A good text writes the file with `writeFileAtomic`, applies the decoded settings as an outside edit does, and records the buffer's version as saved.
- Revert reads the file into the buffer and clears the dirty and conflict marks.
- An outside change while the page shows: a buffer at its saved version takes the file's new text; a dirty buffer keeps its text and the bar says `Changed on disk` until Save or Revert.

### Model

`saveSettings` records the bytes it wrote (`settingsWritten`, a hash, in a mutex). The watcher's goroutine, on a batch of `FileEvent`s that touch `settings.json`, reads the file and compares the hash: equal means the app's own write, or a rewrite with the same bytes, and the work stops. Otherwise it decodes:

1. `migrateSettingsFile(b)` migrates the file as at load. When it writes the migrated file, the hash of the result is recorded, so the event that the write fires is skipped.
2. Decode into a new `settings` value and run the checks of `loadSettings` on it. A decode error logs one line per distinct content and keeps `prefs`.
3. The result crosses to the main thread through `mygo.RunOnMain` and applies.

Applying assigns `prefs`, then does what `loadSettings` does after the checks (`loadGhostty`, `applyGhostty`, `applyAppearance`, `setLocale`) and what `a.applyPrefs` does to every pane (font, theme, copy-on-select, minimum contrast), then redraws every window, the Settings window too, and resyncs the editor's buffer as Behavior says.

`loadSettings` keeps its contract for the start of the app: the reload shares its decode and check steps, but reads them on a copy, so a bad file cannot leave `prefs` half-written.

The watch runs on `rex.Dir()`: a file watch on `settings.json` cannot report its creation when the file does not exist yet. An `Ignore` keeps the snapshot to the file's own names (`settings.json`, `settings.json.bak`, the temporary file of `writeFileAtomic`); the handler acts only on events for `settings.json` itself, including a rename onto it.

The editor's state lives in `settingsUI.json`: an `editor.State`, the buffer's saved `Version()`, and the conflict mark. Save writes the buffer's exact text — the file keeps the user's keys, and a load's migration renames them later, as DSK-009 decides. The written bytes are recorded so the write does not reload. A `saveSettings` write — the app's own, skipped by the watcher — raises `settingsFileStale`; the page resyncs its buffer on its next draw, as it does for an outside change.

### Code

- `settings.go`: the decode and the checks of `loadSettings` move into `decodeSettings(b []byte) (settings, bool)` and `checkSettings(*settings)`, which `loadSettings` and the reload share. `saveSettings` records `settingsWritten` and raises `settingsFileStale`.
- `settingswatch.go`: `watchSettings(ctx)` opens `mygo.WatchFiles` on `rex.Dir()` and loops on `w.Next`; `settingsFileChanged()` reads, hash-compares, decodes and posts `applySettingsFile(next)`; `applySettingsFile` runs on the main thread and applies, then resyncs the editor. `st.start` starts the watch with the app's lifetime.
- `sidebar.go`: `checkSidebarColors` takes the settings it checks; `validStyleColor` accepts a `{light,dark}` object with a side absent, which a hand edit may write, and `parseStyleColor` resolves only the sides present.
- `settingsview.go`: a `file` entry in `sections`; `jsonSettings(c, k)` draws the bar and `editor.View(c, st)` fills the page, in place of the page's scroll column. `settingsUI.json` holds the editor's state.
- `locales/en.json`, `locales/zh-Hans.json`: the section label, the bar's states and buttons, and the hint; `go tool mygo generate` writes `catalog_gen.go`.

## Alternatives

- **Poll the file's modification time**: rejected. The user asked for an event-driven reload, and `mygo.WatchFiles` is already in the tree on every platform, so a poll adds nothing but delay.
- **fsnotify**: rejected. A new dependency for what `mygo.WatchFiles` already does.
- **Watch only when the Settings window is open**: rejected. The point is that a hand edit applies whenever it happens.
- **Structured controls per key**: deferred. The first build of this doc drew a Sidebar page of steppers, segmented controls and hex fields. The user chose the file's own editor: one page edits every key, old and new, with no control per key to maintain.
- **Autosave on change**: rejected. A JSON file is invalid most of the time while it is typed; a Save boundary keeps a half-typed object out of `prefs`.
- **A `TextInput` per file**: rejected for this page; `editor.View` gives the file the editor it is.

## Migration

- Nothing in `settings.json` changes shape. The watch reads the same file `loadSettings` reads.
- An older build ignores nothing new: this change adds no key.
- The reload migrates a file with old keys, as the start does.

## Tests

- **Decode copy**: `decodeSettings` on a torn file returns false and `prefs` keeps its values.
- **Hash skip**: a write recorded by `saveSettings` makes `settingsFileChanged` a no-op; a different write applies.
- **Migration on reload**: a file with an old key reloads with the new key, and the rewrite it caused is skipped by hash.
- **Half-pair color**: `{"dark": "#222222"}` loads, resolves only its dark side.
- **JSON page**: a view test (`ui.NewTester`) sees the editor and the bar; a save writes the file and applies; an invalid text writes nothing; an outside change resyncs a clean buffer and marks a dirty one.

## Phases

1. **Reload and the settings.json page**: the watch, the decode split, the page and its locales, in one PR.
