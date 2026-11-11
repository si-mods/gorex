# Web client

The Rex API gateway (`internal/rexapi`) serves a browser client at `/`.
It runs on the same origin as `/api/control` and `/api/data`. It lists
the sessions the gateway shows and their windows, shows a window's panes
live, as the desktop lays them out, and sends what you type. The terminal emulator is libghostty-vt, compiled to WebAssembly from
the Ghostty commit that GoRex's terminal binding uses. The client draws
the emulator's render state on a canvas.

This is phase 1, a prototype: one window at a time, with its split panes
(see Split panes). Its one setting is the theme (see Themes). It has no
uploads, no paste, no selection and no mouse. The wheel scrolls the
scrollback.

## Design: a remote client only

The web client runs entirely in the browser. A browser cannot start a
process or open a PTY, so the client has no shell of its own and no
local session server. It is a remote client, like Rex's iPhone app:

- Every terminal it shows lives on a GoRex host, in that host's session
  server. The client reaches it through the host's gateway over
  WebSocket (`/api/control` and `/api/data`).
- It keeps no state that a host needs. Closing the tab detaches; the
  sessions and their programs keep running on the host.
- The terminal emulator in the page (libghostty-vt in wasm) only mirrors
  the host's terminal from its snapshot and stream. It runs no program.
- Nothing in the client calls `internal/rex`, the PTY layer or anything
  else that needs an operating system. Features that need one belong on
  the host, behind the gateway's API.

Phase 1 connects only to the host that served the page, because the
gateway refuses web pages of another origin (see Origins). Connecting one
page to several hosts needs the hosts to trust that page's origin; that
is not designed yet.

## Program tiles

The sidebar's window rows and the pane headers show the program tiles of
the iOS app (`ios/GoRex/Views/ProgramTile.swift`), drawn in CSS by
`web/src/programs.ts`: its table of programs, the lookup by process name
(basename, a leading `-` dropped, a hash colour for an unknown program),
the stack of up to three tiles of a window, and the dot of a program done
and not yet seen. The page reads each block's program as the app does:
`com.superlogical.terminal.process` once, then `process_changed`; and its
records (OSC 7501) with `program_status`, `program_status_changed` and
`program_status_removed`. A window row also shows the blocked dot and the
progress ring of iOS's `WindowRow`. Differences from iOS: the SF Symbols
are the lucide SVGs of `assets/icons`, drawn at full size (iOS scales its
symbols by 0.85, the desktop its SVGs by 1); a row has no status inbox.

## Build

You need Node 22 or later, Git and Go. The script gets Zig itself.

```sh
node web/build.mjs      # Windows: the same, from PowerShell or Git Bash
go build .              # embeds what the script wrote
```

`web/build.mjs` does these steps:

1. It gets Zig 0.17.0 for the host into the cache, if `ZIG` is not set.
2. It clones Ghostty and checks out the pinned commit. This is the same
   commit as `ios/scripts/build-ghostty-vt.sh`. Pass
   `--ghostty-source <path>` to use a checkout you already have.
3. It runs
   `zig build -Demit-lib-vt -Dtarget=wasm32-freestanding -Doptimize=ReleaseSmall`.
   It keeps `ghostty-vt.wasm` (about 850 KB) in the cache, so the next
   build skips this step. `--rebuild-wasm` builds it again.
4. It runs `npm ci` in `web/` if `web/node_modules` is missing, then type-checks
   the client (`tsc --noEmit`) and bundles it with esbuild.
5. It writes `index.html`, `app.js` and `ghostty-vt.wasm` into
   `internal/webui/dist`, copies `ios/GoRex/ghostty-themes.json` there (the
   repo keeps one copy of the themes), and the JetBrains Mono fonts from `assets/fonts`
   (with their licence) into `internal/webui/dist/fonts`.
   It also copies `assets/icons` and `assets/brands` into `dist/icons` and
   `dist/brands`, the glyphs of the program tiles.

The cache is `$GOREX_WEB_CACHE`, else `$XDG_CACHE_HOME/gorex-web`, else
`~/.cache/gorex-web`.

`internal/webui/dist` is in `.gitignore`, except for a README. With that
README, `go:embed` has something to embed on a checkout that never ran the
script. Such a build serves a short page at `/` that says how to build the
client. `go build ./...` and the tests need no web build.

## Open it

Every listener of the gateway serves the client: the local network
listener, built-in Tailscale (HTTPS) and `--listen`. For development:

```sh
GoRex rexapi --listen http://127.0.0.1:7391
```

Then open `http://127.0.0.1:7391/`. The gateway applies its usual access
rules to the page. The local network listener lets in only the networks it
allows. Built-in Tailscale lets in only your own user.

## Origins

Native clients send no `Origin` header. They connect as before. A request
that has an `Origin` gets through only if its scheme, host and port are
those of the gateway that the request came to. Any other web page gets
403 on every path (`internal/rexapi/sameorigin.go`). Without this check,
any web page open in a browser on an allowed network could use the API
as that browser.

## How it works

- `web/src/rex.ts`: the control connection (`rex.control.v1`, hello at
  revision 10) and the data stream (`rex.data.terminal.v1`: HELLO with the
  ticket from `data.connect`, then WELCOME). It splits the stream into the
  GHOSTSNP snapshot, the program's output and the `rex1` APC messages, as
  the iOS app does.
- `web/src/ghostty.ts`: loads the module. It reads `ghostty_type_json` and
  checks that every struct field, enum value and cell bit the client uses
  is there, with the right width, before it uses them. All offsets and
  enum values come from that manifest. The `GHOSTTY_MODS_*` bits are
  `#define`s that the manifest does not have, so they are hard-coded.
- `web/src/terminal.ts`: decodes the snapshot into a terminal, then feeds
  it the output. It draws only the dirty rows and the cursor's, once per
  animation frame, and encodes keys with the libghostty-vt key encoder,
  set from the terminal's modes.
- `web/src/box.ts`: box drawing, block elements, sextants and Powerline's
  separators, drawn as shapes, ported from MyGo's `plugins/terminal/box.go`.
- `web/src/themes.ts`: the Ghostty themes list, the choice kept in the
  browser, and the picker.
- `web/src/main.ts`: the connection, the sessions and their views, the
  sidebar, and the window's commands.
- `web/src/window.ts`: a window's layout tree, its panes and dividers,
  and the focus (see Split panes).
- `web/src/pane.ts`: one pane: its stream, its terminal and its canvas.
  Typed bytes go to `com.superlogical.terminal.write`, one write at a
  time. The pane takes its block (`session.own_block`) and sizes it to
  its canvas (`com.superlogical.terminal.resize`); a pane that closes
  while its block stays gives it back (`session.disown_block`).
- `web/src/keys.ts`: the shortcuts (see Shortcuts).

## Split panes

The sidebar has a heading for each session (with a + button for a new
window) and a row for each window: the window's terminal title, else its
label, and its count of panes when it has more than one. Its close button
shows on hover. A click shows the window.

- **Layout.** The page draws the tiled layer's layout tree from
  `session.view`: each `split` divides its room by its `ratio`,
  `horizontal` side by side and `vertical` one above the other. The panes
  are 8px apart, as the desktop's (`gap` in `view.go`), with the desktop's
  pane border and shadow. A zoomed block (`zoomed_block_id`) fills the
  window alone.
- **Panes.** Each pane has its own data stream, its own libghostty-vt
  terminal and canvas, and owns and sizes its own block. It has a header,
  as the desktop's: the terminal's title, and on the focused or hovered
  pane the buttons Split Right, Split Down, Zoom and Close.
- **Focus.** A click or a shortcut focuses a pane: the keys go to it, its
  border and shadow are the focused ones, and its cursor is solid (hollow
  in the others). The desktop does not dim panes that are not focused, so
  this page does not either. The page tells the host
  (`session.focus_block`), and follows the host when the host's focused
  block of the window changes.
- **Live.** The page follows `session_view_changed` and reads only the
  changed session's view again (`session.view`). Panes whose blocks stay
  keep their streams; new blocks get panes and streams; panes of closed
  blocks end. A closed window shows the session's active one. Sessions
  made or destroyed elsewhere come and go in the sidebar. Titles follow
  `title_changed`.
- **Dividers.** Drag the gap between two panes to move it. The panes move
  at once; their terminals keep their sizes until the drag ends, as on the
  desktop. Then the page sends the ratio (`session.resize_pane`, with the
  two blocks beside the border, as GoRex moves a border by a block beside
  it). A double click sets the ratio to one half.
- **Commands.** Split right and down (`session.new_split`, a shell block,
  after the focused one, focused), close the pane (`block.close`), zoom
  (`session.zoom_block`), new window (`session.new_window`, not focused on
  the host) and close window (`session.close_window`).

### Shortcuts

The desktop's shortcuts (`commands.go`) where a browser lets a page have
them. A browser keeps Ctrl/Cmd+W, Ctrl/Cmd+T, Ctrl/Cmd+N and Ctrl+Tab for
itself, so those commands take Alt+Shift chords (Option+Shift on a Mac).
Keys are matched by place (`KeyboardEvent.code`). Every other key goes
to the terminal.

| Command | macOS | Windows, Linux |
|---|---|---|
| Split right | ⌘D | Ctrl+Shift+D |
| Split down | ⇧⌘D | Ctrl+Shift+E |
| Zoom | ⇧⌘↩ | Ctrl+Shift+Enter |
| Focus the pane left, right, up, down | ⌥⌘ and an arrow | Alt and an arrow |
| Move the divider | ⌃⌘ and an arrow | Alt+Shift and an arrow |
| Close the pane (desktop: ⌘W, Ctrl+Shift+W) | ⌥⇧W | Alt+Shift+W |
| New window (desktop: ⌘T, Ctrl+Shift+T) | ⌥⇧T | Alt+Shift+T |
| Close the window (desktop: ⇧⌘W) | ⌥⇧Q | Alt+Shift+Q |
| Next, previous window (desktop: Ctrl+Tab, Ctrl+Shift+Tab) | ⌥⇧] and ⌥⇧[ | Alt+Shift+] and Alt+Shift+[ |

A key moves the divider of the nearest split of that direction around the
focused pane by 5%. As on the desktop on Windows and Linux, Alt and an
arrow (word movement in some shells) is the focus's there, not the
terminal's.

### Not in the web client

- Floating layers (floats): only the tiled layer shows. A block that is
  only in a float has no pane here.
- Dragging a pane to another place, swapping panes, renaming windows and
  sessions, moving windows, and making or closing sessions.
- The desktop's pane animations, the pane's program icon and its
  activity badge.

## Drawing, as the desktop draws

The terminal draws as the desktop's (MyGo's `plugins/terminal/paint.go`)
with GoRex's defaults and no Ghostty configuration:

- Font: JetBrains Mono, served with the client, then `SF Mono`, `Menlo`,
  `monospace` (`defaultFontFamily`), at 11.6 CSS pixels (`defaultFontSize`).
  A cell is the widest ASCII advance wide and the font's ascent and descent
  high, each rounded up to whole device pixels, with 6 by 4 CSS pixels of
  padding. The canvas follows `devicePixelRatio` and lays out again when it
  changes (zoom, another display). Bold is weight 700.
- Text: a run of narrow characters in one style is one `fillText`,
  stretched from the font's advance to the cells, so the font's ligatures
  show as on the desktop (letter spacing would turn them off). Wide
  characters, emoji and graphemes with marks are centred in their cells,
  smaller when too wide. Faint text is at half opacity.
- Lines: underlines (single, double, curly, dotted, dashed) in the
  underline colour, strikethrough and overline, at the desktop's places and
  thickness.
- Colours: GoRex's dark terminal theme (`darkTerm` in `style.go`) over
  xterm's 256 colours, and the desktop's minimum contrast of 3 for text
  (not for box drawing and blocks). A stream's `theme` message (from
  `set_theme`) goes over it. No API gives the host's own theme, so a host
  with a Ghostty theme still shows GoRex's colours here, unless you choose a
  theme (see Themes).
- Cursor: block, bar or underline from the terminal (DECSCUSR), a hollow
  block while the page does not have the focus, blinking every 600 ms when
  the terminal asks for it.
- Scrolling: the wheel scrolls the scrollback; in a full-screen program it
  sends the arrow keys (alternate scroll), and to a program that takes the
  mouse it sends nothing yet. Typing scrolls back to the screen.

## Themes

The Theme button at the bottom of the sidebar opens a picker for the
terminal's colours, as the iOS app's Themes settings does. It lists the 861
Ghostty themes of `ios/GoRex/ghostty-themes.json` (`web/build.mjs` copies
the file into the build; `ios/scripts/ghostty-themes` makes it). The picker has
a preview of the chosen theme, an All, Dark and Light filter (by the
background's luma, as iOS tells it), a search by name, and a row for each
theme with a swatch (its background with colours 1 to 5) and a check mark on
the chosen one. A click applies the theme at once. Escape closes the
picker; the arrow keys move in the list.

- The first row, Host Colors, keeps what the host's stream sends. This is
  GoRex's colours when the host sends none.
- The default is Gruvbox Dark, as on iOS. The choice is in `localStorage`
  (`gorex.theme`, "" for Host Colors) and is per browser. Without storage
  the page works and the choice lasts until it closes.
- A chosen theme acts as a stream `theme` message: it goes over GoRex's
  colours, and a later `theme` message from the host replaces it. Choosing
  Host Colors goes back to the host's last message.
- The page loads the list while it loads the wasm, and the first frame has
  the chosen colours. If the list does not load, the client keeps GoRex's
  colours and shows no Theme button.
- The minimum contrast of 3 stays for every theme.
- The pane and its padding take the terminal's background. When that is
  light (by the same luma), the page's own colours (sidebar, status, frame
  and picker) change to a light set (`data-scheme="light"` in
  `web/index.html`). This follows the terminal's background as drawn, so a
  host theme or a program's OSC 11 change it too.
- Not in the web client: the theme's cursor text and selection colours (the
  client has no selection).

What differs: the desktop's panes are the window's glass, which a page
cannot show, so the pane here is the theme's background. There is no
scroll bar, selection or link underline.
