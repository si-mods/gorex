# GoRex

A replica of [Superlogical's Rex](https://www.superlogical.com/updates/public-testing-beginning)
terminal, written in Go with [MyGo](https://mygo.egoist.dev)'s native UI and its
terminal plugin (Ghostty's libghostty-vt). No webview, no cgo: one ~15 MB app.

![GoRex](docs/screenshot.png)

## What it does

- **Tabs and split panes.** Every pane is a card over the window's gradient,
  with its program's icon, name and directory in its header, and buttons to
  split right, split down, zoom and close. Drag the gaps between panes to
  resize them (double-click resets a split to half).
- **Persistent sessions.** Shells run in a session server, a background
  process of the same binary (`GoRex -server`). Quit GoRex and everything
  keeps running; open it again and every tab, split and pane comes back as
  it was, programs still running. The server keeps each session's screen in
  a headless libghostty-vt emulator and sends a snapshot on attach, so
  full-screen programs (lazygit, vim, htop…) come back exactly.
- **Sessions.** As in Rex, a session is a set of tabs of its own, with a
  name such as `icy-ridge`, and each window shows one. The session chip
  in the title bar, or atop the sidebar, lists the sessions with their
  tabs and the window that shows them, and makes, renames and closes
  them; so do the Session menu and the command palette, whose Change
  Session… finds one by its name. New Window opens a window of a new
  session, and Move Tab to New Window moves a tab to one. Choosing a
  session another window shows brings that window to the front. Closing
  a window keeps its session, its programs running; Close Session asks,
  then ends its terminals. GoRex opens again the windows open when it
  quit, each on its session; terminals that no session has, as after a
  crash, get tabs in the first.
- **Single instance.** GoRex runs once: opened again, it brings the
  window used last to the front, or opens one. The installed app and
  `mygo dev`'s GoRex Dev each run once, apart, and an app given a
  `GOREX_DIR` of its own runs beside them.
- **Program activity.** The server watches each terminal's foreground
  process (`tcgetpgrp`, `sysctl`, `proc_pidinfo`; on Windows, the shell's
  newest child, its command line and directory): headers and tabs name the
  program (`Node`, `Git Changes` for lazygit, `Codex`, `SSH host`…) and its
  working directory, a green dot shows a program printing, and an orange dot
  marks a pane whose program finished or rang the bell out of sight. When
  the window is in the background, a long command finishing posts a
  notification.
- **Tab icons** stack the tiles of a tab's programs, the focused pane's in
  front, as Rex does. Tabs reorder by dragging, rename by double-click, and
  have a context menu.
- **Vertical tabs.** Settings ▸ Appearance places the tabs above the panes,
  or in a sidebar on the left or the right side of the window. Drag the
  sidebar's inner edge to resize it (double-click resets it), and Toggle
  Sidebar (⌃⌘S) hides and shows it. "Show pane headers" off hides the
  panes' headers while the sidebar shows on the left; they always show
  when it is hidden or on the right, as in Rex.
- **Host chip** with the machine's name and model; its popover shows the
  chip, memory, OS and the session server.
- **Command palette** (⇧⌘P, Ctrl+Shift+P, or the ⌘ button): every command, and every pane
  to jump to, fuzzy-matched.
- Light and dark appearances (View ▸ Appearance), text size (⌘+ ⌘− ⌘0),
  JetBrains Mono embedded.
- English and Simplified Chinese (简体中文): Settings ▸ Appearance ▸ Language
  follows the system's language or chooses one. The command palette finds a
  command by its English title in either.

## Shortcuts

| macOS | Windows, Linux | |
|---|---|---|
| ⌘T | Ctrl+Shift+T | New tab |
| ⌘D / ⇧⌘D | Ctrl+Shift+D / Ctrl+Shift+E | Split right / down |
| ⌘W / ⇧⌘W | Ctrl+Shift+W | Close pane / tab |
| ⇧⌘↩ | Ctrl+Shift+Enter | Zoom the pane |
| ⌥⌘ arrows | Alt+arrows | Focus the pane in that direction |
| ⌃⌘ arrows | Alt+Shift+arrows | Move the nearest divider |
| ⌃⌘= | | Equalize panes |
| ⌘1…⌘9, ⇧⌘[ ⇧⌘], ⌃Tab | Ctrl+Shift+1…9, Ctrl+PageUp/PageDown, Ctrl+Tab | Switch tabs |
| ⇧⌘R | Ctrl+Shift+R | Rename tab |
| ⌘N | Ctrl+Shift+N | New window, of a new session |
| ⇧⌘N | | New session |
| ⇧⌘O | Ctrl+Shift+O | Change session |
| ⌃⌘] ⌃⌘[ | Ctrl+Shift+PageDown/PageUp | Next / previous session |
| ⌃⌘S | Ctrl+Shift+S | Hide or show the tabs' sidebar |
| ⇧⌘P, ⌘P | Ctrl+Shift+P | Command palette |
| ⌘K | Ctrl+Shift+K | Clear |
| ⌘+ ⌘− ⌘0 | Ctrl+= Ctrl+- Ctrl+0 | Text size |
| ⌥⌘Q | | Quit and end all sessions |

Elsewhere than on macOS, Control and a letter are the terminal's (Ctrl+C,
Ctrl+D, Ctrl+W…), so GoRex's shortcuts take Shift too, as copying and
pasting do (Ctrl+Shift+C, Ctrl+Shift+V), and none take Control and Alt,
which type characters (AltGr) on many keyboards.

### Settings

Settings… (⌘, on macOS, Ctrl+, elsewhere; Shell menu and command palette)
opens a window of its own with a sidebar of sections: Appearance (light,
dark or system; tab placement; pane headers; text size; language), Terminal (font, cursor style and blink, copy on
selection), Theme (any of Ghostty's themes, or follow the config),
Keyboard (GoRex's shortcuts, read only), Remote (see
[Remote access](#remote-access)), Remote Hosts (see
[Remote hosts](#remote-hosts)) and About (version, Check for Updates…).
The choices go to `settings.json`, as do the sidebar's width and whether
it is hidden.

Remote turns the server's remote access on and off, and manages it:
whether allowed clients share the sessions open here, pairing a new device (Start pairing opens a five-minute window; each
request shows its name and fingerprint, to Approve or Reject), the list
of allowed clients (revoke one, or add one by name and public key), this
machine's own key and its fingerprint, and, while listening, the address,
hidden until you show it, with Copy and Regenerate (which asks first). The
page asks the server every two seconds while it shows. Remote Hosts is the
other side: the machines this one connects to (see
[Remote hosts](#remote-hosts)).

Precedence: what you set in Settings, over Ghostty's config, over GoRex's
defaults. "Reload Ghostty Config" reads the config again but never
replaces a choice made in Settings; "Use Ghostty config" (Terminal)
forgets the font, cursor, and copy-on-selection choices, and Theme's
"Follow Ghostty config" the theme's. A theme chosen in Settings also
replaces the config's `background`, `foreground`, `palette`, and the other
colors, which belonged to its own theme. Font, theme, copy on selection,
and the cursor apply to open panes at once.

### Ghostty configuration

GoRex reads Ghostty's config: `$XDG_CONFIG_HOME/ghostty/config.ghostty`
(`config`), else `%LOCALAPPDATA%` on Windows or `~/.config`; on macOS also
`~/Library/Application Support/com.mitchellh.ghostty/`. `config-file`
includes work. `GOREX_GHOSTTY_CONFIG` names one file instead.

Supported: `font-family`, `font-size`, `font-feature`, `font-thicken`,
`adjust-cell-height` (percent), `theme` (also `light:…,dark:…`),
`background`, `foreground`, `cursor-color`, `cursor-text`,
`selection-background`, `selection-foreground`, `palette` (0–15),
`cursor-style`, `cursor-style-blink`, `scrollback-limit`,
`macos-option-as-alt`, `copy-on-select` (`clipboard`; `true` is
Linux's selection clipboard, so it copies nothing), `right-click-action`,
`minimum-contrast`, `keybind`. `keybind` takes `clear`, `performable:`,
and the actions unbind, ignore, copy_to_clipboard, paste_from_clipboard,
select_all, clear_screen, increase_font_size, decrease_font_size,
reset_font_size, and scroll_page_up, scroll_page_down, scroll_to_top,
scroll_to_bottom; `global:`, `all:`, and `unconsumed:` binds, sequences
(`a>b`), and other actions are skipped. GoRex's own shortcuts come
before keybinds. A text size set with ⌘+ ⌘− wins over `font-size`;
⌘0 goes back to it. With a theme or background, panes draw its
background, and unless View ▸ Appearance names one, the window turns
dark or light by it, as Ghostty's `window-theme = auto` does (not with
`light:…,dark:…`, which follows the system). "Reload Ghostty Config"
(command palette, View menu) applies fonts, themes, `copy-on-select`,
and `minimum-contrast` to open panes; the other options apply to new
panes.

## Remote access

The session server can also listen on a
[tailcat](https://github.com/tailscale/tailcat) address: Tailscale's data
plane without its control plane, so WireGuard end to end, through a DERP
relay or directly, with no Tailscale account. Remote access is off by
default. Each machine has a client key (`remote-client.json`), and the
server serves the keys of its allowlist only: it reads the key of every
connection, and closes those of other keys at once, saying first that
they are not allowed (so that a client tells a refusal from a network
that dropped). With an empty
allowlist nobody gets in. The server keeps its keys (`remote.json`, mode
0600), so its address stays the same; regenerating them makes a new one,
and the old address stops working.

Pairing adds a client without copying its key by hand:

1. On the server, turn remote access on and Start pairing: a window
   opens for five minutes.
2. On the client, ask to pair with the server's address and a name
   (`remotectl pair ADDRESS alice-laptop`). It prints a fingerprint, as
   `01234567-cdef`.
3. The request shows on the server with its name and fingerprint.
   Approve it only if the fingerprint is the one the client printed:
   anyone with the address can ask too.

While the window is open, a client not allowed may ask to pair and
nothing else; at most 8 requests wait, one per key, and they are
forgotten when the window closes. They are never saved.

What to know before turning it on:

- The address is a secret: it holds the server's key and the WireGuard
  pre-shared key. Do not paste it where others read it. GoRex never logs
  it, nor any key.
- The allowlist is what keeps others out. A leaked address alone gets
  nobody in, but it lets anyone ask to pair while a window is open:
  compare fingerprints, and regenerate a leaked address anyway.
- An allowed key means running commands as this user: a client may
  start any program with any environment. From a shell there it can
  reach the local socket, and so change the layout, remote access, and
  sharing itself. Trust it as you would an SSH key.
- Share open terminals (Settings › Remote, off by default, kept in
  `remote.json`) decides what else a client sees. Off, each client lists,
  attaches to, resizes and kills only the sessions it created; the others
  (this machine's window's, other clients') are refused. On, every allowed
  client sees and uses every session. Turning it off detaches clients
  from the sessions they did not create. The server remembers which key
  created a session, in memory, and keeps it across an in-place upgrade;
  a session survives a revoke and approval of the same key as its own.
  Local connections always see every session.
- Over the connection itself, a remote client may list, create, kill,
  resize and attach to sessions only. It may not shut the server down,
  read or set the window layout, or see or change remote access
  (`remote.*`, pairing included): those are for the local socket only.
- Every connection is limited: a line of requests of at most 1 MiB, a
  terminal of 1 to 1000 columns and rows, and 8 kills at once.

### Remote hosts

Settings › Remote Hosts connects this machine to other machines' session
servers, whose sessions then open in tabs of the window beside its own.
Add Remote Host… asks for the host's address (the one its Settings ›
Remote shows; the field hides it), a name for it, and the name this
machine pairs as (the computer's name, at first):

1. On the host, turn remote access on and Start pairing.
2. On this machine, Add Remote Host… with its address. A host that allows
   this machine already is saved at once; otherwise GoRex asks to pair
   and shows this machine's fingerprint.
3. On the host, approve the request that shows the same fingerprint. The
   sheet tries again every three seconds and saves the host once it is
   let in.

The hosts are kept in `hosts.json` beside the settings, readable by the
user only, and not in `settings.json`: their addresses are secrets, which
GoRex never logs nor shows. Each host's row shows its state (Connecting,
Connected, Reconnecting (attempt N), or why it is not connected) with
Retry, which tries at once, and Remove. Removing a host closes its tabs
in the window and leaves its sessions running on the remote server.

GoRex keeps a connection to each host and connects again when it drops,
waiting longer after each failure, up to 30 seconds. New Tab opens on the
active tab's host, and a split on its pane's; New Tab on a host (the
command palette, the host panel, or the + of a host's name in the
sidebar) picks one. Closing a tab or a pane ends its session on its host,
once connected again if it is not now. With hosts saved, the sidebar lists
the tabs under their host's name, this machine's first, and tabs above
show a host's name on its tabs. A pane whose host is away keeps its
screen under "Reconnecting to …", drops what is typed meanwhile, and
attaches again by itself; one whose session ended there closes. The
window's layout keeps the host of each pane, so a host's tabs come back
when GoRex opens again, attached once the host answers. Sessions the host
lists that no tab shows (opened in the host's own window, or by another
client, while the host shares its sessions) get a tab each, last among
the host's; a tab closed here ends its session as any other and does not
come back. When the host stops sharing, the tabs of sessions this machine
did not create close here and their sessions go on there.
`go run ./tools/remotectl pair ADDRESS NAME` asks to pair from a machine
without the app.

## rootshell

[rootshell](https://github.com/kitknox/rootshell), on iOS and the Mac,
shows this machine's tabs over SSH as it shows herdr's in its full control
mode: GoRex plays herdr. `tools/rootshell/herdr`, installed on the host,
runs `GoRex control` in herdr's place, a bridge to the server of the GoRex
already open. The layout stays the desktop's: rootshell shows it, and
changes it through the GoRex window. Setup on macOS and Windows hosts, the steps in
rootshell, limits and troubleshooting: [docs/rootshell.md](docs/rootshell.md).

## Develop and build

GoRex needs [Go](https://go.dev/dl/) 1.27+ and MyGo 0.6.2, whose CLI
`go.mod` pins as a tool:

```sh
go tool mygo dev     # GoRex Dev, rebuilt and restarted as the code changes
go test ./...        # the server, and the view without a window
go tool mygo build   # build/darwin-arm64/GoRex.app and a .dmg
go run ./tools/mkicon  # redraw resources/icon.png
```

`go get -tool github.com/egoist/mygo/cmd/mygo@latest` updates MyGo and its
CLI together.

### Release

Releases update installed apps, signed with a key that only the
maintainer holds. Once, run `go tool mygo keygen` and put the contents of
`mygo-update.pub` in `updates.publicKey` of `mygo.json`; keep
`mygo-update.key` out of the repository. For each release, add a
`## 0.1.0` section (the version of `mygo.json`) to `CHANGELOG.md`, which
becomes the release notes, then build and upload to a draft GitHub release
(it needs `gh` or `GH_TOKEN`), and publish the draft:

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat path/to/mygo-update.key)" go tool mygo build -upload
```

Development builds and apps that cannot write where they are installed
do not update; "Check for Updates…" is in the Shell menu and the command
palette.

The app keeps its state in its data directory: the server's socket and
log, `layout.json` and `settings.json`, in `~/Library/Application
Support/GoRex` for the built app and `GoRex Dev` for `mygo dev`'s, so
that developing never touches the sessions of the app you use
(`GOREX_DIR` names another directory). The sessions outlive the app, and
a new build: on macOS and Linux, a build that finds a server an older
build started has it run the new build in its place, in the same
process, its shells still running and their screens kept. On Windows a
development build replaces such a server, ending its sessions, which
then start again in the same directories, and the built app keeps it.

### Windows

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

## Layout

| | |
|---|---|
| `main.go` | the app, its window, and `-server` |
| `state.go` | tabs, the tree of splits, panes, saving and restoring the layout |
| `workspace.go`, `workspaceview.go` | sessions of tabs (workspaces in the code) and the windows showing them; the session chip, its list, and Close Session's question |
| `view.go`, `tabs.go`, `sidebar.go`, `host.go`, `commands.go` | the interface: title bar, tabs and their sidebar, panes, host popover, menus and palette |
| `programs.go` | how programs show: names, glyphs, tile colors |
| `style.go`, `settings.go` | colors, fonts, terminal themes; appearance, text size and the user's choices over Ghostty's |
| `settingsview.go`, `remoteview.go` | the Settings window, and its Remote page |
| `locales` | the messages of the interface, `en.json` and `zh-Hans.json`, and `catalog_gen.go` (written by `go tool mygo generate`; commit it with the catalogs) |
| `tools/remotectl` | runs and probes the session server without the app: allow, enable, probe over tailcat |
| `CHANGELOG.md` | release notes: a `## <version>` section per release |
| `internal/rex` | the session server and its client: PTYs, foreground processes, headless screens, the socket protocol |
| `internal/control`, `tools/rootshell` | `GoRex control`, the bridge rootshell drives as herdr, and the `herdr` shim that runs it |

Rex's connections to servers on other machines are only half there: the
server side exists (remote access, above), but the window cannot open
remote hosts yet.

Icons: [Lucide](https://lucide.dev) (ISC) and [Simple Icons](https://simpleicons.org)
(CC0). Font: [JetBrains Mono](https://www.jetbrains.com/lp/mono/) (OFL).
