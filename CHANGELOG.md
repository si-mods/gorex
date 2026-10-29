# Changelog

## Unreleased

- Kitty graphics sent by file path (`t=f`) are read on the machine that
  runs the program, rewritten in-band, and replayed when a window attaches.
- Find bar: Cmd+F/Ctrl+Shift+F searches the focused pane, with match count,
  next and previous, and Escape to close; search colors come from Ghostty's
  `search-*` options. Command-click opens URLs and file links, resolving
  relative paths against the pane's program directory.

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
