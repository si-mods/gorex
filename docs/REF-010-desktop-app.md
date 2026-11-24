---
name: REF-010
description: "What the user of the desktop app sees and does: panes of every kind, persistent sessions, spaces and projects, program activity, Settings and the notes for Windows. Use when you change a feature of the app."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-010: The desktop app

Status: reference. It describes the code in main.

The desktop app is the MyGo application of the root package (`gorex` with no arguments). This doc describes what its user sees and does. The session server behind it is [REF-006](REF-006-session-server.md), remote access is [REF-012](REF-012-remote-access.md), and the Ghostty configuration it reads is [REF-011](REF-011-ghostty-config.md).

## Tabs and split panes

Every pane is a card over the window's gradient,
with its program's icon, name and directory in its header, and buttons to
split right, split down, zoom and close. Drag the gaps between panes to
resize them (double-click resets a split to half).

## Browser panes

New Browser Tab, or Split with Browser, opens a pane
that shows a web page in the system's web view: an address field, back,
forward and reload in its header, and the page's title on its tab. The
page lives while its tab does, and opens again at its URL when GoRex
starts. A browser pane belongs to this machine, so a remote tab takes
none, and Find searches terminals alone. Only http and https pages
open; a page is untrusted, so it reaches no GoRex code, asks for no
permission, opens no window of its own and downloads nothing.

## Note panes

New Note Tab, or Split with Note, opens a pane that is a
sticky note: free text you type in place, named by its first line. The
layout keeps the text, so it is there when GoRex starts. A note pane
belongs to this machine, so a remote tab takes none, peers and the API
never see it, and Find searches terminals alone. With a note focused, the
text-size keys (`⌘+`, `⌘−`, `⌘0`) resize its text, a note at a time, and the
eye button or `⌘E` shows it as rendered Markdown (links open in the browser,
images show their descriptions); the pencil button or `⌘E` edits again. On
Windows and Linux the key is `Ctrl+Shift+M`, and a terminal keeps it unless
a note has the focus.

## Persistent sessions

Shells run in a session server, a background
process of the same binary (`GoRex -server`). Quit GoRex and everything
keeps running; open it again and every tab, split and pane comes back as
it was, programs still running. The server keeps each session's screen in
a headless libghostty-vt emulator and sends a snapshot on attach, so
full-screen programs (lazygit, vim, htop…) come back exactly. A server
restarted (`gorex server restart`, or the app on Windows replacing an
older one once asked) brings its sessions back as new shells at their
panes' directories; Quit and End All ends them. How the server keeps
the screens: [REF-006](REF-006-session-server.md).

## Kitty graphics

Images sent by file path are read where the program
runs, including on a remote host, and are shown in-band; a window that
attaches again replays them.

## Find and links

Cmd+F searches the focused pane's screen and
scrollback, with the match count and next/previous; Escape closes it.
Command-click (Control-click elsewhere) opens URLs through the system,
and file paths relative to the pane's program.

## Spaces, hosts, projects, tabs, panes

The model is in [MOD-002](MOD-002-projects.md). A
project is a folder on one host; its tabs, splits, floating panes and
browser panes live in that host's layout, so every client of the host
(this app on another machine, Rex Go, the web client) sees the same
tabs. A space (Work, Life) is a name on projects.
- The sidebar has the space picker atop (New Space…, Edit Spaces… to
  rename, color and order them), then each host you added as a section
  header with its state and a `+` for a new project, and under it its
  projects of the space, flush, each with its color dot and tab count
  (the folder in its tooltip), latest used first, those unused for 30
  days under a folded **Older**, then its **No Project** (tabs with no project), which
  every space shows. A click on the project shown, or on a project's dot,
  opens its tabs one step in, joined by a guide line; its `+` (and ⌘T
  while it is shown) opens a tab in its folder. Its menu has New Tab, Rename…,
  Color, Move to Space, Reveal Folder (this machine's) and Close
  Project…. A host's menu has Rename… (its alias) and New Tab with No
  Project. Drag a tab onto a project of its host to move it there, its
  programs running.
- With the tabs on top, the title bar shows `Space ▾` and `host ›
  project ▾`, whose menu lists the space's hosts, their projects with
  their tab counts, the tabs with no project, and New Project….
- **New Project…** (the `+` of a host's header, the sidebar's foot, the
  palette, the Project menu, ⇧⌘N) opens the folder sheet on that host:
  browse its folders, name the project (the folder's name by default),
  Create. The host makes the project in the space shown and its first
  tab in the folder. A folder that is a project already shows that
  project, and offers to move it to this space.
- A host that is away shows its projects greyed, as GoRex last saw them;
  nothing in them opens until it connects.
- Each window keeps the project it shows, the space shown, the sidebar's
  open rows and the spaces' colors and order in `settings.json`, for
  this machine only.
- A layout of v0.38 to v0.40 is migrated once: this machine's projects
  stay, old spaces' tabs become tabs with no project, and the panes
  another host ran in this machine's tabs go to that host as it
  connects (the sidebar says "Waiting for" the host until then), into
  the project they were homed in there, with their splits, and no
  program restarts.

## Single instance

GoRex runs once: opened again, it brings the
window used last to the front, or opens one. The installed app and
`mygo dev`'s GoRex Dev each run once, apart, and an app given a
`GOREX_DIR` of its own runs beside them.

## Program activity

The server watches each terminal's foreground
process (`tcgetpgrp`, `sysctl`, `proc_pidinfo`; on Windows, the shell's
newest child, its command line and directory): headers and tabs name the
program (`Node`, `Git Changes` for lazygit, `Codex`, `SSH host`…) and its
working directory, a green dot shows a program printing, and an orange dot
marks a pane whose program finished or rang the bell out of sight. When
the window is in the background, a long command finishing posts a
notification. A session's most urgent program status shows in the
session picker and the sidebar.

## Agent skill and hooks

`gorex skill setup` finds Claude Code, Codex,
Antigravity CLI, Devin, OpenCode and Pi, installs a `gorex` skill into
each, and adds a session-start hook to Claude Code, Codex, Antigravity
CLI and Devin that tells the agent its session and pane and how to
drive GoRex from the command line. It adds
only its own hook and leaves other settings, hooks and skills as they
were; `gorex skill status` says what is installed, and `gorex skill
remove` removes only what it added. `--project` keeps the skill in the
repository, and `--dry-run` reports first.

## Tab icons

stack the tiles of a tab's programs, the focused pane's in
front, as Rex does. Tabs reorder by dragging, rename by double-click, and
have a context menu.

## Vertical tabs

Settings ▸ Appearance places the tabs above the panes,
or in a sidebar on the left or the right side of the window. Drag the
sidebar's inner edge to resize it (double-click resets it), and Toggle
Sidebar (⌃⌘S) hides and shows it. "Show pane headers" off hides the
panes' headers while the sidebar shows on the left; they always show
when it is hidden or on the right, as in Rex.

## Pane animations

Panes spring into place when they move, split or
zoom, and new or closing panes pop in and out. Settings ▸ Appearance ▸
Animation style chooses Snappy, Natural or Off, which also sets how the
sidebar shows and hides. While the system's Reduce Motion is on,
animations stay off.

## Infinite canvas (experimental)

Settings ▸ Appearance ▸ Experimental
▸ Infinite canvas lays every tab out as free boxes on an endless plane,
each pane where it showed. Scroll or drag the empty plane to pan, or
hold Space over it and drag anywhere; pinch, or hold Ctrl or Cmd and
scroll, to zoom (25% to 200%), and Reset Canvas View in the palette goes
back to the start. Right-click the empty plane for a new terminal,
browser or note there. Move and resize a box by its header and edges; a
split puts a box of the same size right of or below the focused one. A box keeps its size as the window resizes or the canvas
zooms, so its terminal keeps its columns and rows. Turning it off docks
every box back into the tab's splits. It may change or go away.

## Host chip

with the machine's name and model; its popover shows the
chip, memory, OS and the session server, and a remote host that is not
connected with a button to Settings › Remote Hosts (Add Remote Host… while
none is saved).

## Command palette

(⇧⌘P, Ctrl+Shift+P, or the ⌘ button): every command, and every pane
to jump to, fuzzy-matched.

## Appearance

Light and dark appearances (View ▸ Appearance), text size (⌘+ ⌘− ⌘0),
JetBrains Mono embedded.

## Languages

English and Simplified Chinese (简体中文): Settings ▸ Appearance ▸ Language
follows the system's language or chooses one. The command palette finds a
command by its English title in either.

## Settings

Settings… (⌘, on macOS, Ctrl+, elsewhere; Shell menu and command palette)
opens a window of its own with a sidebar of sections: Appearance (light,
dark or system; tab placement; pane headers; animation style; text size;
language), Terminal (font, cursor style and blink, copy on
selection, minimum contrast), Theme (any of Ghostty's themes, or follow the config),
Keyboard (GoRex's shortcuts, read only), Remote (see
[remote access](REF-012-remote-access.md) and [remote hosts](REF-012-remote-access.md#remote-hosts)),
Network (see [remote hosts](REF-012-remote-access.md#remote-hosts) and [Tailscale discovery](REF-012-remote-access.md#tailscale-discovery-desktop))
and About (version, Check for Updates…).
The choices go to `settings.json`, as do the sidebar's width and whether
it is hidden.

`appearance.theme` in `settings.json` names a theme for GoRex's own
colors (not the terminal's): a manifest of colors at
`<config>/themes/<name>.json`, beside `init.lua`
([DSK-008](DSK-008-theme.md)). `gorex pack install <id>` installs a
theme pack of the registry there. Settings > Extensions lists, installs
and uses themes too, and installs icon packs. Leave it empty for the
built-in colors.

Remote turns the server's remote access on and off, and manages it:
pairing a new device (Start pairing opens a five-minute window; each
request shows its name and fingerprint, to Approve or Reject), the list
of allowed clients (revoke one, or add one by name and public key), this
machine's own key and its fingerprint, and, while listening, the address,
hidden until you show it, with Copy and Regenerate (which asks first). The
page asks the server every two seconds while it shows. Its Remote Hosts
group is the other side: the machines this one connects to (see
[remote hosts](REF-012-remote-access.md#remote-hosts)).

## Windows

GoRex runs on Windows 10 1809 or later, whose ConPTY runs the shells; its
data directory is `%AppData%\GoRex`. Panes run `$SHELL` when it names a
Windows program (`nu`, `C:\Program Files\Git\bin\bash.exe`), else
PowerShell (`pwsh`, then `powershell`), else `cmd`. PowerShell's `cd`
leaves its process's directory where it was, so GoRex starts it with a
prompt that sets it, wrapping the profile's own prompt: the header then
follows it, and new panes open there. The window's title bar is hidden
and the system's buttons with it: the title bar draws minimize, maximize and
close with the other buttons, and the window still resizes from every edge,
its top included.

A ConPTY's input drops the OSC that a terminal's paste event is, so a
program there that reads the clipboard itself, as agents do, is never told
of a paste. GoRex writes the paste to the session as text instead, through
the session's server, which brackets it when the program asked for
bracketed pastes (`gorex paste`); a pane of a remote client pastes the same
in a Windows session. An image alone has no text to write: on Windows an
image reaches a program as a file it opens, not as clipboard data.
