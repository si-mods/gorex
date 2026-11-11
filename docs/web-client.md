# Web client

The Rex API gateway (`internal/rexapi`) serves a browser client at `/`.
It runs on the same origin as `/api/control` and `/api/data`. It lists
the sessions the gateway shows, opens one terminal live and sends what you
type. The terminal emulator is libghostty-vt, compiled to WebAssembly from
the Ghostty commit that GoRex's terminal binding uses. The client draws
the emulator's render state on a canvas.

This is phase 1, a prototype: one terminal at a time. It has no settings,
no uploads, no paste, no selection and no mouse. The wheel scrolls the
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
   `internal/webui/dist`, and the JetBrains Mono fonts from `assets/fonts`
   (with their licence) into `internal/webui/dist/fonts`.

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
- `web/src/main.ts`: the list and the view. Typed bytes go to
  `com.superlogical.terminal.write`, one write at a time. The view takes the
  block (`session.own_block`) and sizes it to the canvas
  (`com.superlogical.terminal.resize`).

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
  with a Ghostty theme still shows GoRex's colours here.
- Cursor: block, bar or underline from the terminal (DECSCUSR), a hollow
  block while the page does not have the focus, blinking every 600 ms when
  the terminal asks for it.
- Scrolling: the wheel scrolls the scrollback; in a full-screen program it
  sends the arrow keys (alternate scroll), and to a program that takes the
  mouse it sends nothing yet. Typing scrolls back to the screen.

What differs: the desktop's panes are the window's glass, which a page
cannot show, so the pane here is the theme's background. There is no
scroll bar, selection or link underline.
