# Web client

GoRex's web client is a standalone web page (`web/`) that connects to
GoRex hosts through their Rex API gateway. GoRex does not ship it: the
desktop app and its gateway serve no page. The client is hosted on its
own, on Cloudflare or from a local dev server (see Hosting). It lists the
sessions of the chosen host and their windows, shows a window's panes
live, as the desktop lays them out, and sends what you type. The terminal
emulator is libghostty-vt, compiled to WebAssembly from the Ghostty
commit that GoRex's terminal binding uses. The client draws the
emulator's render state on a canvas.

This is phase 1, a prototype: one window at a time, with its split panes
(see Split panes). Its settings are in one Settings dialog (see
Settings): the hosts, the theme, the page's appearance and the terminal's
font, cursor and contrast. It has no uploads, no paste, no selection and no mouse. The
wheel scrolls the scrollback.

## Design: a remote client only

The web client runs entirely in the browser. A browser cannot start a
process or open a PTY, so the client has no shell of its own and no
local session server. It is a remote client, like Rex's iPhone app:

- Every terminal it shows lives on a GoRex host, in that host's session
  server. The client reaches it through the host's gateway over
  WebSocket (`/api/control` and `/api/data`), at the address the user
  added. No URL is the page's own: every API URL is built from the
  host's address (`setHost` and `apiURL` in `web/src/rex.ts`).
- It keeps no state that a host needs. Closing the tab detaches; the
  sessions and their programs keep running on the host.
- The terminal emulator in the page (libghostty-vt in wasm) only mirrors
  the host's terminal from its snapshot and stream. It runs no program.
- Nothing in the client calls `internal/rex`, the PTY layer or anything
  else that needs an operating system. Features that need one belong on
  the host, behind the gateway's API.
- The page's own files (the script, the wasm, the fonts, the icons and
  the themes) come from where the page is hosted, never from a host.

## Hosts

The page has a list of hosts, as the iOS app has
(`ios/GoRex/Rex/Host.swift`): a name, the gateway's address
(`http(s)://host[:port]`, nothing else) and an icon. The list and the
chosen host are in `localStorage` (`gorex.hosts`, `gorex.host`), per
browser. Without storage the page works, and the list lasts until the
page closes. The button at the top of the sidebar shows the chosen host
and opens the list: choose a host, add one, or edit or remove one (the
pencil). `?host=URL` in the page's address is a quick link: it adds that
host, or chooses it when the list has it, and is then taken out of the
address bar.

Only the chosen host is connected. Choosing another one closes the
connection and the panes, forgets the first host's sessions, and connects
to the other. The first host's sessions keep running. One connection at
a time keeps one sidebar and one set of shortcuts, as before; several
hosts at once, as iOS shows them, is for later.

With no host, the page says how to add one, with the command that makes
a host trust this page: `gorex rexapi web allow <origin>`, with the page's
own `location.origin`. When a host does not connect, the page tells why,
and tries again every few seconds, so it connects once the cause is gone:

- **The host doesn't trust this page.** The gateway refused the page's
  origin (see Trusting a page). The page shows the command to run on the
  host.
- **The host refused this browser.** The page is trusted, but the
  gateway's access rule is not met: the local network listener lets in
  only the networks it allows, built-in Tailscale only your user. The page
  shows the gateway's message.
- **The host can't be reached.** Nothing answered: a wrong address, no
  gateway listening there, or the browser stopped the request (see Local
  Network Access).
- **Mixed content.** An https page cannot open `http://` or `ws://`
  connections (see Mixed content).

A WebSocket that fails tells the page nothing of why, so the page asks
the gateway's `/api/help` twice (`diagnose` in `web/src/hosts.ts`): as a
CORS request, which a gateway that trusts the page answers (a refusal of
the access rule comes with its message), and as a `no-cors` request,
which any server that is there answers without the page reading it.
Answered only the second way, the gateway refused the page's origin;
answered neither way, it was not reached.

## Trusting a page

A browser sends a page's origin (`scheme://host[:port]`) with every
request it makes for the page, WebSockets included. Native clients (Rex's
apps, the GoRex desktop, the CLI) send none. The gateway
(`checkOrigin` in `internal/rexapi/web.go`) serves a request:

- without an `Origin`, as before, for native clients;
- with an `Origin` in the host's list of trusted origins.

It refuses any other with 403 on every path, before its access rule,
`Origin: null` included. A trusted page still meets the access rule: the
networks of the local network listener, your user on built-in Tailscale.
Without this check any web page open in a browser on an allowed network
could use the API as that browser, since the access rule judges the
caller's address, not the page.

The list is empty by default. Set it on the host:

```sh
gorex rexapi web allow https://gorex-web.<account>.workers.dev
gorex rexapi web allow http://127.0.0.1:5173    # the local dev server
gorex rexapi web deny http://127.0.0.1:5173
gorex rexapi web status
```

An origin is `http` or `https`, a host and maybe a port, nothing else: no
path, wildcard, query or user name. The command writes it as browsers
send it: lower case, without the scheme's default port
(`https://Example.com:443/` is `https://example.com`). The list is in
`rexapi/web.json` in GoRex's data directory, as `{"origins": [...]}`. The
gateway reads it again within a second of a change, without a restart.
Every listener of the gateway uses the same list. The commands change
the file only, as `gorex rexapi lan` does, so they work with no gateway
running; the gateway has no control method for the list.

For a trusted page, the gateway's plain HTTP answers carry
`Access-Control-Allow-Origin` with the page's origin, and
`Vary: Origin`, so that the page can read them (a refusal's message, an
upload's answer). The gateway answers such a page's CORS preflight
(`OPTIONS` with `Access-Control-Request-Method`, after the access rule):
it allows `GET` and `POST` and the `Content-Type` header, which an upload
(`POST /api/upload` of `application/octet-stream`) needs. It answers no
preflight of any other page.

Trust only pages you control. A trusted page can do anything the API can,
as anyone who opens it in a browser that the access rule lets in.

## Mixed content

A page loaded over https may open only https and `wss://` connections:
browsers block `http://` and `ws://` from it as mixed content. So:

- The hosted page (Cloudflare, https) reaches a host by its tailnet
  address, `https://<machine>-gorex.<tailnet>.ts.net`, from built-in
  Tailscale.
- A gateway's local network listener is plain http
  (`http://192.168.1.2:7297`). Reach it from an http page: the local dev
  server.
- Chrome's Local Network Access (below) lets an https page open `ws://`
  to a private or loopback IP address, `localhost` or a `.local` name,
  once the user allows it. Other browsers may still block it. The page
  tries such an address; it does not try any other `http://` host from an
  https page, and shows the mixed content message at once.

## Local Network Access

Chrome asks the user before a public website reaches the local network or
the loopback address (Local Network Access). It replaced Private Network
Access and its `Access-Control-Allow-Private-Network` preflights, so the
gateway does not answer those. Confirmed from Chrome's documentation
(<https://developer.chrome.com/blog/local-network-access>,
<https://developer.chrome.com/docs/modern-web-guidance/guides/security/local-network-access>):

- The prompt launched in Chrome 142, for `fetch`, subresources and
  frames. The guide says that it now governs WebSockets too, and that
  `ws://` from an https page to a recognizably local host (a private or
  loopback IP address, `localhost`, `.local`) is exempt from mixed content
  blocking behind the prompt. Which Chrome version added WebSockets is not
  confirmed from Chrome's own release notes.
- Local addresses are RFC 1918's, link-local and IPv6 unique local ones;
  loopback is `127.0.0.0/8`, `::1` and `localhost`. A page served from
  loopback (the dev server on `127.0.0.1`) is not public, so its
  connections to the local network are not gated.
- The permission is refused at once to an insecure (http) top-level page.

Not confirmed: how Chrome classifies Tailscale's `100.64.0.0/10`
addresses, which a `*.ts.net` name resolves to. If Chrome counts them as
local, the hosted page asks for the permission before it reaches a
tailnet host; allow it.

## Hosting

The page is static files: `web/dist`, which `npm run build` (that is,
`node build.mjs`) writes. `web/dist` is not in the repository
(`web/.gitignore`).

### Cloudflare Workers

`web/wrangler.jsonc` deploys `web/dist` as a Worker with static assets
only, no Worker code, named `gorex-web`, at
`https://gorex-web.<account>.workers.dev`. From `web/`:

```sh
npm run deploy    # node build.mjs, then wrangler deploy
```

`npm run deploy` runs Wrangler 4.152.0 with `npx`, which asks you to sign
in to Cloudflare the first time. Wrangler is not a dependency of the
page, so `npm ci` in a build does not fetch it and its runtime. Then
trust the page's origin on each host:
`gorex rexapi web allow https://gorex-web.<account>.workers.dev`.

`web/_headers`, which the build copies into `dist`, sets the files'
headers on Cloudflare:

- `Content-Security-Policy`: `default-src 'none'`; the page's own
  scripts (`script-src 'self'`, with `'wasm-unsafe-eval'` for
  libghostty-vt), fonts and images (and `data:` for the empty favicon);
  its inline `<style>` (`style-src 'self' 'unsafe-inline'`; the styles
  the script sets go through the CSSOM, which the policy does not
  restrict); no `<base>`, no forms, no framing elsewhere. `connect-src`
  is `'self' http: https: ws: wss:`: the hosts are the user's, added in
  the page, so the policy cannot name them. The policy's defence is that
  no script but the page's own runs; a narrower `connect-src` would need
  the hosts at build time.
- `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
- The file names carry no hash. So `/`, `index.html`, `app.js` and the
  wasm are `no-cache` (revalidated, with Cloudflare's ETag, on every
  load), and the fonts, icons, brand marks and themes are kept for a day.
  Cloudflare gives a path that two rules match both rules' headers, so no
  two rules set the same header on one path.

### Local dev server

```sh
cd web
npm run dev                  # http://127.0.0.1:5173
npm run dev -- --port 7478   # another port; --host ADDR to listen elsewhere
```

`web/dev.mjs` builds `web/dist` once (`build.mjs`, with its type check),
then serves it with esbuild. It bundles `src/` again on every change (not
minified, with a source map), without the type check, and copies
`index.html` again when it changes. It prints the command that makes a
host trust it. It is plain http on loopback, so it reaches both kinds of
gateway: `http://` on the local network and `https://` on the tailnet. It
sends none of `_headers`' headers.

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

You need Node 22 or later and Git. The script gets Zig itself. Go is not
needed.

```sh
cd web
npm run build     # node build.mjs; Windows: the same, from PowerShell or Git Bash
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
4. It runs `npm ci` in `web/` if `web/node_modules` is missing, then
   type-checks the client (`tsc --noEmit`) and bundles it with esbuild.
5. It empties `web/dist` and writes `index.html`, `app.js`, `_headers` and
   `ghostty-vt.wasm` there, copies `ios/GoRex/ghostty-themes.json` (the
   repo keeps one copy of the themes), and the JetBrains Mono fonts from
   `assets/fonts` (with their licence) into `dist/fonts`. It also copies
   `assets/icons` and `assets/brands` into `dist/icons` and
   `dist/brands`, the glyphs of the program tiles and of the hosts.

The cache is `$GOREX_WEB_CACHE`, else `$XDG_CACHE_HOME/gorex-web`, else
`~/.cache/gorex-web`. `go build ./...` and the Go tests need no web build.

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
- `web/src/hosts.ts`: the hosts, kept in the browser; their list and
  form; why a host does not connect (see Hosts).
- `web/src/main.ts`: the chosen host's connection, the sessions and their
  views, the sidebar, and the window's commands.
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
| Settings | ⌘, | Ctrl+, |

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
  Settings can choose another family and size (see Settings).
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
  xterm's 256 colours, and the desktop's minimum contrast of 3 (Settings can
  change it) for text
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

The Theme section of Settings has a picker for the terminal's colours,
as the iOS app's Themes settings does. It lists the 861
Ghostty themes of `ios/GoRex/ghostty-themes.json` (`web/build.mjs` copies
the file into the build; `ios/scripts/ghostty-themes` makes it). The picker has
a preview of the chosen theme, an All, Dark and Light filter (by the
background's luma, as iOS tells it), a search by name, and a row for each
theme with a swatch (its background with colours 1 to 5) and a check mark on
the chosen one. A click applies the theme at once. The arrow keys move in
the list.

- The first row, Host Colors, keeps what the host's stream sends. This is
  GoRex's colours when the host sends none.
- The default is Gruvbox Dark, as on iOS. The choice is in `localStorage`
  (`theme` in `gorex.settings`, "" for Host Colors) and is per browser.
  Without storage the page works and the choice lasts until it closes.
- A chosen theme acts as a stream `theme` message: it goes over GoRex's
  colours, and a later `theme` message from the host replaces it. Choosing
  Host Colors goes back to the host's last message.
- The page loads the list while it loads the wasm, and the first frame has
  the chosen colours. If the list does not load, the client keeps GoRex's
  colours, and the Theme section says so.
- The minimum contrast (3 by default) applies to every theme.
- The pane and its padding take the terminal's background. When that is
  light (by the same luma), the page's own colours (sidebar, status, frame
  and picker) change to a light set (`data-scheme="light"` in
  `web/index.html`). This follows the terminal's background as drawn, so a
  host theme or a program's OSC 11 change it too. The Window setting
  (see Settings), Light or Dark, overrides it.
- Not in the web client: the theme's cursor text and selection colours (the
  client has no selection).

What differs: the desktop's panes are the window's glass, which a page
cannot show, so the pane here is the theme's background. There is no
scroll bar, selection or link underline.

## Settings

The Settings button at the bottom of the sidebar, or Ctrl+, (⌘, on a
Mac), opens the Settings dialog (`web/src/settings.ts`), as the desktop's
settings window (`settingsview.go`): the chosen host's card and the
sections at the left, each with its tile of colour, and the chosen
section's settings at the right, in grouped cards. It is a modal dialog
(`<dialog>`), a frosted panel over a dimmed page. The × button, Escape,
the same keys again or a click outside close it, and the keyboard goes
back to the terminal. On a phone's width the sections go in a row above
the page.

The choices are one object in `localStorage`, `gorex.settings`
(`web/src/prefs.ts`), per browser; the theme chosen before it
(`gorex.theme`) moves into it once. Each change applies at once to the
page and every open pane.

| Section | Settings |
|---|---|
| Host (the card) | The chosen host, its address and its connection (connected, connecting, unreachable, with the reason), this page's origin, and the command that makes a host trust it. |
| Appearance | Window: System, Light or Dark for the page's colours; System follows the terminal's background, as the desktop's System follows the theme's colours. Show pane headers. Text size, 8 to 32 CSS pixels, 11.6 (the desktop's) until set, with Reset. |
| Terminal | Font: JetBrains Mono, or a common monospaced family installed on the device (told by measuring text in it), with a preview in the theme's colours. Cursor style (Block, Bar, Underline) and Cursor blink. Minimum contrast: the default (3), Off, 2, 3, 4.5 (WCAG AA) or 7 (WCAG AAA). Reset to Defaults forgets the font, cursor and contrast. |
| Theme | The theme picker (see Themes). |
| Keyboard | The shortcuts (see Shortcuts), with this system's keys. |
| Hosts | The hosts: choose one, add one, edit its address, name and icon, or remove it, with the same form as the host list's. |
| About | The client's commit and build date and the Ghostty commit of the wasm (`web/build.mjs` writes them in), and Reset All Settings, which forgets every `gorex.*` key of the page in this browser and reloads it. |

The cursor is set as the desktop sets it (`setCursor`): with the
sequence a program would send (DECSCUSR), into each pane's terminal when
it changes and after each snapshot, so that a program can still set its
own. Until a cursor is chosen, the pane keeps the host's.

Not in the web client: the desktop's Glass and Terminal opacity (a page
cannot show the desktop through it), the Dock badge, Tab placement and
Sidebar view (the page has one sidebar and one window), Animation style
(the page does not animate the panes), Language (the page is in English),
updates and the settings file (the page is what its host serves, and
keeps `localStorage`), Copy on selection (no selection yet), and
recording shortcuts.
