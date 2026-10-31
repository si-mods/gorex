# Changelog

## 0.3.5

### Features

* **pane:** float panes over a tab's tiled layout ([#113](https://github.com/si-mods/gorex/pull/113))

### Bug Fixes

* **fonts:** draw a title's Nerd Font icons, and register a family macOS hides ([#116](https://github.com/si-mods/gorex/pull/116))
* **pane:** resize terminals live while dragging a divider ([#121](https://github.com/si-mods/gorex/pull/121))
* **remote:** open a host's new split panes in the tab already adopted ([#120](https://github.com/si-mods/gorex/pull/120))
* **sidebar:** drop the host chip beside a hidden sidebar ([#119](https://github.com/si-mods/gorex/pull/119))
* **tabs:** keep a faded tab label inside its row ([#115](https://github.com/si-mods/gorex/pull/115))

## 0.3.4

- A session another machine's client opens on this machine gets a tab in
  this window too, last among this machine's tabs and without the focus,
  rather than running unseen: a pane or tab added from another GoRex
  through Settings › Remote Hosts shows up here. A session this machine's
  command line opens stays as it was.

## 0.3.3

- A pane header reads on its own background: when a pane takes a dark
  terminal background in a light window, or the other way around, its
  title and buttons take the palette of that background, so a hovered icon
  no longer turns black on a dark header.
- Icons are SF Symbols on macOS: the buttons of the window and its pane
  headers, the tabs, the sidebar and the settings window draw the system's
  own symbol at their size and color, as Rex does, which follows the
  design of the macOS they run on and keeps them sharp on any display.
  Windows and Linux, and a macOS too old for a symbol, draw the SVGs of
  `assets/icons` as before; brand marks are always SVGs, since SF Symbols
  has no logos. Nothing of Apple's artwork is in the app or its build.
- Browser panes. New Browser Tab (the command palette or the Shell
  menu) opens a tab whose pane shows a web page in the system's web
  view, with an address field, back, forward and reload in its header;
  Split with Browser puts one beside the focused pane. The page's title
  names its tab, and the layout keeps its URL, so it opens again when
  GoRex starts. A browser pane belongs to this machine: a remote tab
  takes none, and Find searches terminals alone. Only http and https
  pages open, and a page is untrusted: it reaches no GoRex code, asks
  for no permission, opens no window and downloads nothing.

- Importing a Rex session. `gorex rex-import` reads a Rex server's sessions
  and makes them in GoRex: a space per session, a tab per window, a pane per
  block, each named as Rex names it. Each pane runs `gorex rex-mirror`, which
  draws what the Rex block shows, from the escape sequences Rex renders (the
  data stream's snapshot is Ghostty's binary format, which the mirror reads
  past), and sends what is typed to the block; `--shell` runs a shell in each
  block's directory instead, and `--resize` gives the block the pane's size.
  Where a session came from is recorded in `rex-import.json`, so a second run
  leaves it alone. The window imports too: **Space ▸ Import from Rex**, and
  the palette's *Import from Rex*, mirror every session not recorded yet and
  report the count as a notification (a failure as a dialog). Rex's default
  labels are not copied: a window called `Window 2b63efe2` and a block called
  `shell` leave tab and pane to follow the block's title. A mirrored pane
  shows the tile of the program its Rex block runs, and its status, through
  the status protocol (OSC 7501, `app=`), so Claude Code's tile and a working
  or finished state show as they do in Rex; a pane whose program is not a
  shell names itself by its title even while it is the program its session
  started with.
- Layout commands for a client: the space a command acts on, the command a
  new pane runs, and whether a new tab takes focus; `space.create` makes a
  space, and `space.close` closes one, ending the sessions of a space no
  window shows. Closing a space, or a tab, ends its programs as before.
- A pane header reads on its own background: when a pane takes a dark
  terminal background in a light window, or the other way around, its
  title and buttons take the palette of that background, so a hovered icon
  no longer turns black on a dark header.
- Icons are SF Symbols on macOS: the buttons of the window and its pane
  headers, the tabs, the sidebar and the settings window draw the system's
  own symbol at their size and color, as Rex does, which follows the
  design of the macOS they run on and keeps them sharp on any display.
  Windows and Linux, and a macOS too old for a symbol, draw the SVGs of
  `assets/icons` as before; brand marks are always SVGs, since SF Symbols
  has no logos. Nothing of Apple's artwork is in the app or its build.
- Paste into a program that runs on Windows reaches it again. A ConPTY
  drops the OSC that a terminal's paste event is, so a program that reads
  the clipboard itself, as agents do, never learned of a paste, and
  nothing came of it. A paste now goes to the session's server instead,
  which writes it as text, bracketed when the program asked for bracketed
  pastes; `gorex paste` does the same from the command line, and a pane of
  a remote client pastes the same in a Windows session. An image alone has
  no text to write, so an image still reaches a Windows program only as a
  file it opens.
- Pane animations. Panes now spring into place when they move, split or
  zoom, and new or closing panes pop in and out. A new Animation style
  setting in Settings › Appearance lets you choose Snappy, Natural or Off,
  and it also controls how the sidebar shows and hides. While Reduce
  Motion is on, animations stay off.
- A session server restarted on purpose brings its sessions back, as one
  that crashed already does: the server after it starts the sessions of the
  layout again, with the same ids and at the directories their panes were
  in, so the windows attach to them. `gorex server restart` does it without
  starting a server itself: the open app reconnects and starts the next one,
  else the next `gorex` command or launch does; run inside a pane, the
  command says so, then its shell ends with the server. The app on Windows
  restarts a server of an older build this way, since Windows servers cannot
  upgrade in place, after asking whether to, since each pane comes back as a
  new shell; a newer build is compared first, so that two copies of the app
  do not restart each other. What the old sessions printed is lost. Quit and
  End All still ends them, and so does the idle exit. A server too old to
  know a restart is not restarted: quit it once with Quit and End All, or
  kill it.

## 0.3.2

- Sessions are spaces now: the interface says Space / 空间, and each space
  has a color — nine presets, Default, or a custom `#rrggbb` — drawn in
  the window's gradient, the active tab and the accent. The sidebar's foot
  shows a dot per space, its own ringed and other windows' dimmed, and
  Ctrl+1 to Ctrl+9 (Ctrl+Alt elsewhere) show the nth space.
- A pane's header has a button that moves the pane to a new tab.
- A remote host's tab opens here as one tab, with its splits: the sessions
  it shows are adopted in the tab the host's shared layout names, so two
  panes side by side there are not two tabs here. A session the host has
  not saved into its layout yet waits a poll or two for the save, and the
  host's focused pane is focused here even when its layout leaves panes
  out.
- A session attached by several clients takes its size from the client
  that typed last, so a viewer attaching, or a frame reporting its focus,
  no longer resizes the session under the typist; the client left takes
  the size once the typist leaves.
- Dragging a window's edge gives each session one resize, 150ms after the
  size settles, instead of one per step: one reflow, one SIGWINCH, and no
  stale prompt per step from zsh's right prompt.
- Popovers are opaque, and the app's text fields share one look in two
  sizes; a focused field takes the space's accent, and the rename fields
  no longer clip the text.

## 0.3.1

- A session server that dies — a crash, a `kill`, a reboot — no longer
  takes the windows with it. The server keeps every layout the app sends in
  an append-only journal beside the layout, and the server that starts
  after one that left no stop record starts the sessions of that layout
  again: the same ids, at the directories their panes were in, at their
  panes' sizes, so every window attaches to the sessions it had. A pane
  whose stream ends while the server still has its session stays open
  instead of closing its tab, and the app connects again on its own,
  attaching the window to the sessions that came back. A run that stops — the
  app asking it to, an idle exit, an upgrade — records why, with the sessions
  it held, so that the server after it starts nothing.

- Dropping a tab on the middle half of another tab grafts its panes beside
  the target tab's, with their sessions; the outer quarters still reorder,
  and tabs of different hosts only reorder.

## 0.3.0

- rootshell's pane actions work: dragging a split's divider
  (`pane.resize`), swapping (`pane.swap`), renaming (`pane.rename`) and
  moving a pane to another tab or a new tab (`pane.move`). Pane labels
  stay in snapshots.
- A paste of a copied image file, or of text that is one absolute path to
  an image here (quoted or not), offers the image's bytes ahead of the
  path: a program that reads the clipboard itself (Kitty's paste events)
  gets the picture even over SSH or on a remote host, where a path of this
  machine means nothing. A path pasted as text still types as the path, and
  a clipboard whose read fails whole pastes its text.
- A remote client can be given every session, or the layout, from
  Settings › Remote Hosts per client, while others keep only their own
  sessions.
- Remote hosts can share their layout read-only: Settings › Remote Hosts
  shows the host's workspaces, tabs and panes with Open, Focus, resize,
  New Tab and Close Tab commands, `gorex -S HOST layout`
  reads it, and `gorex -S HOST events` follows layout changes when Share
  Layout is on. The server filters the layout to sessions the client may
  use, shows the rest as unavailable placeholders, and drops panes of
  other hosts.
- Kitty graphics sent by file path (`t=f`) are read on the machine that
  runs the program, rewritten in-band, and replayed when a window attaches.
- Find bar: Cmd+F/Ctrl+Shift+F searches the focused pane, with match count,
  next and previous, and Escape to close; search colors come from Ghostty's
  `search-*` options. Command-click opens URLs and file links, resolving
  relative paths against the pane's program directory.
- Ghostty `clipboard-read`, `clipboard-write`, window padding, padding
  color and image-storage-limit now reach the terminal; the README lists
  which options map, are unsupported, or are ignored, and `gorex config
  check` warns about the ignored ones.
- Remote identities carry the paired name, key and created/last-seen
  times; a remote client's hello says which principal it is, and old
  allowlists get created times. `gorex clients` shows the principal and
  how long each attachment has been live.
- A login/PAM proposal documents the path from one-account shared mode to
  per-user isolation; it is not implemented.
- A Linux shared-server service and `gorex remote` administration commands
  (enable, address, pair, approve, revoke, share) are documented in
  `docs/shared-server.md`.
- Sessions carry the principal that created them; `gorex ls` shows local
  or the paired client's name.
- Program status changes notify subscribers and carry their session IDs and
  records in events; the session picker and sidebar show a session's most
  urgent status; rootshell gets the pane's and tab's `agent_status`; and
  status records survive an in-place server upgrade.

## 0.2.1

- Upgraded MyGo from v0.6.2 to v0.7.2. In the terminal, the scroll bar now
  shows where the viewport is as macOS's do, and drags it; Shift-click
  extends a selection; and a trackpad's wheel momentum no longer zooms the
  text under the pointer.
- The disk image is named after the app's slug, `gorex-0.2.1-<arch>.dmg`,
  as MyGo v0.7.0 changed, and `install.sh` installs on macOS as well as
  Linux.

## 0.2.0

- Program status (OSC 7501): tabs, panes and the palette show when a
  program such as Claude Code works, waits for you, finishes or fails, and
  a notification tells you when it happens out of sight.
- Sessions of tabs, one per window, as in Rex; GoRex runs a single
  instance.
- Connect to remote GoRex hosts from the app, pair devices by approval on
  the host, and let a host share its open sessions with paired clients.
- Vertical tabs, in a left or right sidebar, with the host chip at its foot.
- Simplified Chinese.
- The session server upgrades in place and keeps the sessions.
- Fixes: a query's answer no longer lands on the prompt when a session is
  open in several windows; remote panes get their size once attached; dead
  remote connections are dropped.

## 0.1.0

- First release, with automatic updates.
