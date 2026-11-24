---
name: REF-010
description: "What the user of the desktop app sees and does: panes of every kind, persistent sessions, spaces and projects, program activity, Settings and the notes for Windows. Use when you change a feature of the app."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-010: The desktop app

Status: reference. It describes the code in main.

The desktop app is the MyGo application of the root package (`gorex` with no arguments). This doc describes what its user sees and does. The session server behind it is [REF-006](REF-006-session-server.md). Remote access is [REF-012](REF-012-remote-access.md). The Ghostty configuration it reads is [REF-011](REF-011-ghostty-config.md).

## Tabs and split panes

Every pane is a card over the window's gradient. Its header shows the program's icon, name and directory. The header has buttons to split right, split down, zoom and close. Drag the gaps between panes to resize them. A double-click resets a split to half.

## Browser panes

New Browser Tab, or Split with Browser, opens a pane that shows a web page in the system's web view. Its header has an address field, back, forward and reload. The page's title shows on its tab. The page lives while its tab does. It opens again at its URL when GoRex starts. A browser pane belongs to this machine. A remote tab takes none, and Find searches terminals alone. Only http and https pages open. A page is untrusted. It reaches no GoRex code, asks for no permission, opens no window of its own and downloads nothing.

## Note panes

New Note Tab, or Split with Note, opens a pane that is a sticky note. It is free text that you type in place, named by its first line. The layout keeps the text, so it is there when GoRex starts. A note pane belongs to this machine. A remote tab takes none, peers and the API never see it, and Find searches terminals alone. With a note focused, the text-size keys (`⌘+`, `⌘−`, `⌘0`) resize its text, a note at a time. The eye button or `⌘E` shows it as rendered Markdown. Links open in the browser, and images show their descriptions. The pencil button or `⌘E` edits again. On Windows and Linux the key is `Ctrl+Shift+M`. A terminal keeps it unless a note has the focus.

## Persistent sessions

Shells run in a session server, a background process of the same binary (`GoRex -server`). Quit GoRex and everything keeps running. Open it again and every tab, split and pane returns as it was, its programs still running. The server keeps each session's screen in a headless libghostty-vt emulator. It sends a snapshot on attach, so full-screen programs (lazygit, vim, htop…) return exactly. A restarted server (`gorex server restart`, or the app on Windows replacing an older one once asked) restores its sessions as new shells in the panes' directories. Quit and End All ends them. How the server keeps the screens: [REF-006](REF-006-session-server.md).

## Kitty graphics

Images sent by file path are read where the program runs, including on a remote host. They are shown in-band. A window that attaches again replays them.

## Find and links

Cmd+F searches the focused pane's screen and scrollback. It shows the match count and next/previous. Escape closes it. Command-click (Control-click elsewhere) opens URLs through the system, and file paths relative to the pane's program.

## Spaces, hosts, projects, tabs, panes

The model is in [MOD-002](MOD-002-projects.md). A project is a folder on one host. Its tabs, splits, floating panes and browser panes live in that host's layout. Every client of the host (this app on another machine, Rex Go, the web client) sees the same tabs. A space (Work, Life) is a name on projects.

- The sidebar has the space picker on top. New Space… makes one, and Edit Spaces… renames, colors and orders them. Each host you added is a section header with its state and a `+` for a new project. Under the header are the host's projects of the space, flush. Each shows its color dot and tab count, and the folder in its tooltip. Latest used comes first. Projects unused for 30 days go under a folded **Older**. Then its **No Project** (tabs with no project), which every space shows. A click on the project shown, or on a project's dot, opens its tabs one step in, joined by a guide line. Its `+` (and ⌘T while it is shown) opens a tab in its folder. Its menu has New Tab, Rename…, Color, Move to Space, Reveal Folder (this machine's) and Close Project…. A host's menu has Rename… (its alias) and New Tab with No Project. Drag a tab onto a project of its host to move it there. Its programs keep running.
- With the tabs on top, the title bar shows `Space ▾` and `host › project ▾`. Its menu lists the space's hosts, their projects with their tab counts, the tabs with no project, and New Project….
- **New Project…** (the `+` of a host's header, the sidebar's foot, the palette, the Project menu, ⇧⌘N) opens the folder sheet on that host. Browse its folders, name the project (the folder's name by default), Create. The host makes the project in the space shown and its first tab in the folder. A folder that is a project already shows that project, and offers to move it to this space.
- A host that is away shows its projects greyed, as GoRex last saw them. Nothing in them opens until it connects.
- Each window keeps the project it shows, the space shown, the sidebar's open rows and the spaces' colors and order in `settings.json`, for this machine only.
- A layout of v0.38 to v0.40 is migrated once. This machine's projects stay. Old spaces' tabs become tabs with no project. The panes another host ran in this machine's tabs go to that host as it connects (the sidebar says "Waiting for" the host until then), into the project they were homed in there, with their splits. No program restarts.

## Single instance

GoRex runs once. Opened again, it brings the window used last to the front, or opens one. The installed app and `mygo dev`'s GoRex Dev each run once, apart. An app given a `GOREX_DIR` of its own runs beside them.

## Program activity

The server watches each terminal's foreground process (`tcgetpgrp`, `sysctl`, `proc_pidinfo`, or on Windows the shell's newest child with its command line and directory). Headers and tabs name the program (`Node`, `Git Changes` for lazygit, `Codex`, `SSH host`…) and its working directory. A green dot shows a program printing. An orange dot marks a pane whose program finished or rang the bell out of sight. When the window is in the background, a long command finishing posts a notification. A session's most urgent program status shows in the session picker and the sidebar.

## Agent skill and hooks

`gorex skill setup` finds Claude Code, Codex, Antigravity CLI, Devin, OpenCode and Pi. It installs a `gorex` skill into each. It adds a session-start hook to Claude Code, Codex, Antigravity CLI and Devin. The hook tells the agent its session and pane and how to drive GoRex from the command line. It adds only its own hook. Other settings, hooks and skills stay as they were. `gorex skill status` says what is installed. `gorex skill remove` removes only what it added. `--project` keeps the skill in the repository, and `--dry-run` reports first.

## Tab icons

A tab's icon stacks the tiles of the tab's programs, the focused pane's in front, as Rex does. Tabs reorder by dragging, rename by double-click, and have a context menu.

## Vertical tabs

Settings ▸ Appearance places the tabs above the panes, or in a sidebar on the left or the right side of the window. Drag the sidebar's inner edge to resize it. A double-click resets it. Toggle Sidebar (⌃⌘S) hides and shows it. "Show pane headers" off hides the panes' headers while the sidebar shows on the left. They always show when the sidebar is hidden or on the right, as in Rex.

## Pane animations

Panes spring into place when they move, split or zoom. New or closing panes pop in and out. Settings ▸ Appearance ▸ Animation style chooses Snappy, Natural or Off. The choice also sets how the sidebar shows and hides. While the system's Reduce Motion is on, animations stay off.

## Infinite canvas (experimental)

Settings ▸ Appearance ▸ Experimental ▸ Infinite canvas places every tab as free boxes on an endless plane, each pane where it showed. Scroll or drag the empty plane to pan, or hold Space over it and drag anywhere. Pinch, or hold Ctrl or Cmd and scroll, to zoom (25% to 200%). Reset Canvas View in the palette returns to the start. Right-click the empty plane for a new terminal, browser or note there. Move and resize a box by its header and edges. A split puts a box of the same size right of or below the focused one. A box keeps its size as the window resizes or the canvas zooms, so its terminal keeps its columns and rows. Turning the canvas off returns every box to the tab's splits. The feature may change or be removed.

## Host chip

The host chip shows the machine's name and model. Its popover shows the chip, memory, OS and the session server. For a remote host that is not connected it has a button to Settings › Remote Hosts. Add Remote Host… shows while none is saved.

## Command palette

The command palette (⇧⌘P, Ctrl+Shift+P, or the ⌘ button) lists every command, and every pane to jump to, fuzzy-matched.

## Appearance

Light and dark appearances (View ▸ Appearance), text size (⌘+ ⌘− ⌘0), JetBrains Mono embedded.

## Languages

English and Simplified Chinese (简体中文): Settings ▸ Appearance ▸ Language follows the system's language or chooses one. The command palette finds a command by its English title in either.

## Settings

Settings… (⌘, on macOS, Ctrl+, elsewhere. Also the Shell menu and command palette) opens a window of its own with a sidebar of sections. Appearance: light, dark or system, tab placement, pane headers, animation style, text size, language. Terminal: font, cursor style and blink, copy on selection, minimum contrast. Theme: any of Ghostty's themes, or follow the config. Keyboard: GoRex's shortcuts, read only. Remote: see [remote access](REF-012-remote-access.md) and [remote hosts](REF-012-remote-access.md#remote-hosts). Network: see [remote hosts](REF-012-remote-access.md#remote-hosts) and [Tailscale discovery](REF-012-remote-access.md#tailscale-discovery-desktop). About: version, Check for Updates…. The choices go to `settings.json`, as do the sidebar's width and whether it is hidden.

`appearance.theme` in `settings.json` names a theme for GoRex's own colors (not the terminal's). The theme is a manifest of colors at `<config>/themes/<name>.json`, beside `init.lua` ([DSK-008](DSK-008-theme.md)). `gorex pack install <id>` installs a theme pack of the registry there. Settings > Extensions lists, installs and uses themes too, and installs icon packs. Leave it empty for the built-in colors.

Remote turns the server's remote access on and off, and manages it. It pairs a new device: Start pairing opens a five-minute window, and each request shows its name and fingerprint, to Approve or Reject. It lists the allowed clients, to revoke one or to add one by name and public key. It shows this machine's own key and its fingerprint. While the server listens, it shows the address, hidden until you show it, with Copy and Regenerate (which asks first). The page asks the server every two seconds while it shows. Its Remote Hosts group is the other side: the machines this one connects to (see [remote hosts](REF-012-remote-access.md#remote-hosts)).

## Windows

GoRex runs on Windows 10 1809 or later. ConPTY runs the shells there. The data directory is `%AppData%\GoRex`. Panes run `$SHELL` when it names a Windows program (`nu`, `C:\Program Files\Git\bin\bash.exe`), else PowerShell (`pwsh`, then `powershell`), else `cmd`. PowerShell's `cd` leaves its process's directory where it was. GoRex starts it with a prompt that sets it, wrapping the profile's own prompt. The header then follows it, and new panes open there. The window's title bar is hidden, and the system's buttons with it. The title bar draws minimize, maximize and close with the other buttons. The window still resizes from every edge, its top included.

A ConPTY's input drops the OSC that a terminal's paste event is. A program there that reads the clipboard itself, as agents do, is never told of a paste. GoRex writes the paste to the session as text instead, through the session's server. The server brackets it when the program asked for bracketed pastes (`gorex paste`). A pane of a remote client pastes the same in a Windows session. An image alone has no text to write. On Windows an image reaches a program as a file it opens, not as clipboard data.
