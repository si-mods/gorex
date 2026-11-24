---
name: REF-013
description: "Importing a Rex session into GoRex as tabs and panes that mirror its blocks: `gorex rex-import`, the record file, and what is not kept. Use when you change `internal/reximp` or the import."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-013: Importing a Rex session

Status: reference. It describes the code in main.

GoRex reads a Rex server and makes the same shape here: a Rex session
becomes a project, a window a tab, and a block a pane. Each pane runs
`gorex rex-mirror`, which draws what the Rex block shows and sends what
is typed there to it, so the two show the same program.

```sh
gorex rex-import --session work            # one session, by label or ID
gorex rex-import                           # every session
gorex rex-import --shell                   # shells, not mirrors
gorex rex-import --resize                  # mirrors give their blocks the pane's size
```

The window imports without a terminal: **Project ▸ Import from Rex**, or the
command palette's *Import from Rex*, mirrors every session the record does
not have yet and says how many it made in a notification; a failure opens a
dialog. The Rex server defaults to `$REX_SERVER`, else `rex/server.sock` in
the platform's data directory; `--rex` names another. What a run imported
is recorded in `rex-import.json` in GoRex's data directory, so a second run
leaves those sessions alone. The GoRex window must be open: it makes the
projects, tabs and panes.

A mirror is a view with input. The data stream starts with a snapshot in
Ghostty's binary format, which only a Ghostty terminal restores, so the
mirror draws the screen from the escape sequences Rex renders (`format`
vt), reads past the snapshot by its record format, and passes the
program's output on as it comes; Rex's own APC messages are dropped. Keys
go to the block as text. What the block's program prints reaches the pane
as it would reach a terminal itself: the mirror does not filter its
escapes otherwise, as a terminal multiplexer would not.

A tab keeps its window's label only when a person gave it one; Rex's own
labels, such as `Window 2b63efe2`, are left out, and the tab follows its
block's title, as the Rex window does. The tab's tile is the program the
block runs — Claude Code, omp, a shell — which the mirror reports through
the program status protocol (OSC 7501) along with the status Rex keeps for
it, so a working or finished block shows as it does in Rex.

A mirror's pane is a terminal of GoRex's, so what the Rex program writes
reaches it, escape sequences and all. A layout's exact shape is not kept:
the blocks are made in layout order, each split beside the one before, so a
nested split's panes can sit in different halves than Rex put them, and
ratios are not kept.
