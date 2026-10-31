# Changelog

## Unreleased

- Rex's API, so that Rex's clients can use GoRex. The session server answers
  HTTP and WebSockets on its socket, beside its own JSON lines: a Rex
  session is a space, a Rex window is a tab, and a Rex block is a pane. The
  `rex` CLI lists, inspects, captures, types into, splits, renames, zooms
  and closes GoRex's panes through it, `rex events` shows the changes, and
  Rex.app's protocol, method for method, is answered where GoRex has the
  operation, with `not_supported` where it has not (floating layers,
  detached blocks placed later, block ownership, host and Tailscale
  methods). Data connections stream a pane's snapshot and then its
  program's output. A layout command now names the space it acts on, runs a
  command in a new pane, and makes a new tab the active one; `space.create`,
  `space.rename` and `space.close` let a client make and close spaces, as
  `session.create` and `session.destroy` need. A tab now keeps an ID in the
  layout, so a Rex window's ID stays while its tab does, even after the pane
  that came first closes.

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
