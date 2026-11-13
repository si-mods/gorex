# Web client

GoRex's web client is a standalone web page (`web/`) that connects to
GoRex hosts through their Rex API gateway. GoRex does not ship it: the
desktop app and its gateway serve no page. The client is hosted on its
own, on Cloudflare or from a local dev server (see Hosting). It keeps
every host connected at once, lists their sessions and windows, shows a
window's panes live, as the desktop lays them out, and sends what you type. The terminal
emulator is libghostty-vt, compiled to WebAssembly from the Ghostty
commit that GoRex's terminal binding uses. The client draws the
emulator's render state on a canvas.

This is phase 1, a prototype: one window shown at a time, with its split panes
(see Split panes). Its settings are in one Settings dialog (see
Settings): the hosts, the theme, the page's appearance and the terminal's
font, cursor and contrast. It selects and copies text, and reports the
mouse to programs that track it (see Selection and the mouse). It types
what an input method composes and what a soft keyboard gives, and pastes
text (see Typing and paste). It has no uploads. The wheel scrolls the
scrollback.

## Design: a remote client only

The web client runs entirely in the browser. A browser cannot start a
process or open a PTY, so the client has no shell of its own and no
local session server. It is a remote client, like Rex's iPhone app:

- Every terminal it shows lives on a GoRex host, in that host's session
  server. The client reaches it through the host's gateway over
  WebSocket (`/api/control` and `/api/data`), at the address the user
  added. No URL is the page's own: every connection goes through its
  host's transport (`Transport` in `web/src/rex.ts`), which the
  control connection keeps and its data streams and downloads take
  (`Control.transport`): the browser's own WebSocket and fetch to its
  address, or, for a host found by built-in Tailscale, the page's
  Tailscale node.
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
(`http(s)://host[:port]`, nothing else) and an icon. The list is in
`localStorage` (`gorex.hosts`), per browser. Without storage the page
works, and the list lasts until the page closes. The button at the top
of the sidebar shows the host of the shown window and opens the list:
choose a host (its last shown window shows, else its first), add one, or
edit or remove one (the pencil). `?host=URL` in the page's address is a
quick link: it adds that host, or chooses it when the list has it, shows
its first window, and is then taken out of the address bar.

Every host is connected at once, as on the desktop and in Rex Go
(docs/web-multihost.md): each host of the list, and each host built-in
Tailscale found that the user connected once (`gorex.tailnetOpened`,
when Tailscale finds it again). Each host has its own connection
(`HostConn`, `web/src/hostconn.ts`), its own reconnects and its own
sessions. Showing a window of another host leaves the first connected:
its titles, program tiles and attention dots keep updating in the
sidebar. A host that goes offline touches only its own rows; when it
comes back, the page attaches again to the same sessions and windows and
makes none. The page opens at most four control connections at a time as
it starts; an open that takes over 3 s no longer counts, and one that is
not welcomed in 10 s fails and waits like any failure, so hosts that do
not answer do not hold the others back. A host that waits to try again tries at once when the
network comes back (`online`) or the page shows again
(`visibilitychange`). There is no cap on the number of hosts. Two
addresses of one computer (its LAN address and its tailnet name, say)
are two hosts, each with all its windows.

The last four windows shown stay live, across hosts: the shown one and
the three shown before it (`kept` and `keptWindows` in `main.ts`), as the
desktop keeps a space's panes when its window shows another. A window
the stage leaves is kept (`WindowView.park`): it goes off the stage and
paints nothing, and its panes give their blocks back
(`session.disown_block`), so that the desktop sizes them again. Their
streams stay open, their terminals go on taking the programs' output,
and the window follows its session's view. Showing it again puts it
back as it was: each pane takes its block again (`session.own_block`,
`take`), sends its size and paints, with no new ticket and no snapshot,
and its scrollback where the user left it. A kept window ends when a
fifth window shows (the one shown least recently), when it or its
session is gone, and when its host drops, is removed or its address
changes; shown after that, it opens from a snapshot. The shown window
never ends this way. A window of a host that is not connected is not
kept as it leaves the stage. When a pane ends with typed bytes still
waiting to be written (its host was offline), the status line says how
many were lost.

The page keeps where it was, `gorex.place`: the host, session and window
shown last. A reload connects every host and shows that window as soon
as its host has read its sessions (its first window when the window is
gone); while that host does not connect, the stage shows its notice. A
tailnet host's place waits for built-in Tailscale to find it. The chosen
host of a page from before (`gorex.host`) moves into `gorex.place` once
(`web/src/place.ts`), and is removed.

With no host, the page shows a welcome (`web/src/welcome.ts`), centred in
the stage: the app's icon, what the page is, and two ways in. **Sign in
with Tailscale**, the recommended one, opens the sign-in guide; **Connect
by Address** shows the host form in the welcome's place. A **What do I
need?** disclosure lists what a computer needs, in plain words. The
sidebar shows the app's name in place of the host button until there is
a host to choose. The welcome does not say how a host trusts the page:
the page says it when a host refuses it (below), with the command,
`gorex rexapi web allow <origin>` with the page's own `location.origin`.

The list also has a **Tailscale** group: the GoRex hosts that built-in
Tailscale found on your tailnet (see Built-in Tailscale), each with a
dot for its state (online, offline, refuses this browser). They are not
saved. Before Tailscale is signed in, the group has one row, **Add Hosts
from Tailscale** (or **Sign in to Tailscale**), which opens the sign-in
guide. When a host does not connect, the page tells why (the notice
over the stage, for the shown window's host only, and a line under the
host in the sidebar, with Retry and Edit), and tries again, so it
connects once the cause is gone. It waits 3 s
before the first retry and twice as long before each next one, up to
30 s: the desktop's doubling, from 3 s where the desktop starts at 1 s.
A connection that stays up 10 s forgets the failures before it:

- **The host doesn't trust this page.** The gateway refused the page's
  origin (see Trusting a page). The page shows the command to run on the
  host, in plain words, and the technical why under **Learn more**.
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

## Built-in Tailscale

The page can join your tailnet itself, with no Tailscale app and no VPN
on the device (docs/web-tailscale.md). It then lists the GoRex hosts on
the tailnet and connects to them through Tailscale.

1. Open the host list and choose **Add Hosts from Tailscale**, or
   Settings, Tailscale, and turn **Built-in Tailscale** on. A guide opens.
2. **About**: what happens. The tab joins the tailnet as a device of its
   own, `gorex-web-` and four letters (Settings, Tailscale, Device name
   changes it). **Continue**.
3. **Download**: the page downloads Tailscale's engine, about 6 MB
   gzipped, with a progress bar. A failure shows the reason and **Try
   Again**.
4. **Sign in**: **Sign in with Tailscale** opens a new tab, and the page
   sends it to `https://login.tailscale.com/a/…` when control answers.
   Sign in there with the account of your GoRex hosts. The page notices
   by itself when you are done. When the browser blocked the tab, the
   guide shows the link to open, and a box to copy it. **Cancel** closes
   the tab and stops waiting. A tailnet that approves devices by hand
   waits for an admin.
5. **Hosts**: the account and tailnet, then "Looking for GoRex hosts…"
   and each host found, with **Connect**. A host that refuses this
   browser says why and what to run on it.

A host is found when it is your own untagged device, its name ends in
`-gorex` (built-in Tailscale's default) or it reports itself as GoRex,
and it answers `GET /api/help` on port 443 within 3 seconds. For a host
to be found, on the host:

```sh
gorex rexapi tailscale on                  # sign it in with the same account
gorex rexapi tailscale status              # wait for https: available
gorex rexapi web allow <this page's origin>
```

MagicDNS and HTTPS must be on in the tailnet's admin console. When the
host lets in only the account that signed it in and this browser signed
in with another, run `gorex rexapi tailscale on --allow-tailnet-members`
on the host (the tailnet's access rules then decide).

In this version the sign-in lasts for this tab only: the node is
ephemeral and kept in memory, so reloading or closing the tab signs this
browser out, and Tailscale removes the device soon after. Sign Out in
Settings does the same at once and removes the tailnet hosts from the
list. A tailnet with Tailnet Lock is not supported, nor a control server
other than Tailscale's.

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
  scripts (`script-src 'self'`, which covers the script's ES module
  chunks that `import()` loads, with `'wasm-unsafe-eval'` for
  libghostty-vt), fonts and images (and `data:` for the empty favicon,
  `blob:` for the image viewer's checked images, for images only);
  its inline `<style>` (`style-src 'self' 'unsafe-inline'`; the styles
  the script sets go through the CSSOM, which the policy does not
  restrict); no `<base>`, no forms, no framing elsewhere. `connect-src`
  is `'self' http: https: ws: wss:`: the hosts are the user's, added in
  the page, so the policy cannot name them. The policy's defence is that
  no script but the page's own runs; a narrower `connect-src` would need
  the hosts at build time. `frame-src http: https:` lets the hidden frame
  that takes a host's download (docs/web-open-files.md) load it.
- `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
- The script and its code chunks, the terminal's wasm, the themes and
  the Tailscale engine are in `/assets` under names that carry a hash of
  their content (`app-<hash>.js`, `<module>-<hash>.js` and
  `chunk-<hash>.js` with esbuild's hash, `ghostty-vt-<hash>.wasm`,
  `manifest-<hash>.json`, `maple-index-<hash>.json`, and in `lazy/`
  `ghostty-themes-<hash>.json`, `maple-<face><n>-<hash>.woff2`,
  `tailnet-<hash>.wasm.gz`, `tailnet-<hash>.js`), kept a year
  (`public, max-age=31536000, immutable`): after the first visit, a
  reload takes them from the browser's cache without asking the server.
  `/` and `index.html`, which names the script, are `no-cache`
  (revalidated, with Cloudflare's ETag, on every load). The
  fonts, icons and brand marks are kept for a day. Cloudflare gives a path
  that two rules match both rules' headers, so no two rules set the same
  header on one path.
- `manifest-src 'self'` and `worker-src 'self'`: the app's manifest
  and its Service Worker (see App). `sw.js` and `manifest.webmanifest`
  are `no-cache`, so that a new build is found at the next load; the
  manifest is sent as `application/manifest+json`. The app icons in
  `/pwa` are kept for a day.
- `connect-src` names `https://controlplane.tailscale.com`,
  `wss://controlplane.tailscale.com` and `wss://*.tailscale.com` (DERP)
  for built-in Tailscale; `http: https: ws: wss:` already allow them.
  The engine uploads no logs.

### Local dev server

```sh
cd web
npm run dev                  # http://127.0.0.1:5173
npm run dev -- --port 7478   # another port; --host ADDR to listen elsewhere
```

`web/dev.mjs` builds `web/dist` once (`build.mjs`, with its type check),
then serves it with esbuild. It bundles `src/` again on every change (not
minified, with a source map, split into chunks as the build is), without
the type check, and copies `index.html` again when it changes. The
script keeps its plain name, `app.js`, in the dev server; its chunks go
in `dist/assets` with esbuild's hash, and the other files keep the
hashed names the build gave them (the manifest's `js/` entries are the
build's). It builds the Tailscale engine again when a Go file of
`web/tailnet` changes (a few seconds with Go's build cache) and lists it
in the asset manifest, written again under the name the script knows;
reload the page to load it. A change of `build.mjs` or `dev.mjs` needs
a restart of the dev server. It prints the command that makes a
host trust it. It is plain http on loopback, so it reaches both kinds of
gateway: `http://` on the local network and `https://` on the tailnet. It
sends none of `_headers`' headers. The page registers no Service Worker
there (see App).

## App

The page is an installable web app (a PWA): Chrome and Edge offer to
install it, and iOS adds it to the Home Screen. Installed, it opens in a
window of its own, with its own icon.

- **Manifest**: `web/manifest.webmanifest`, linked from `index.html`:
  GoRex, `start_url` and `scope` the page's directory, `display:
  standalone`, and the dark page's colour (`#1c1c1e`) as its theme and
  background. `main.ts` keeps `<meta name="theme-color">` to the
  page's scheme (`#1c1c1e` dark, `#ececf0` light), which colours the
  app window's title bar. There is no `window-controls-overlay`: the
  page would go under the title bar's buttons, and would need a drag
  region of its own.
- **Icons**: `web/pwa`, PNGs committed to the repository and drawn by
  `go run ./tools/mkicon -web` from the icon's own drawing: the macOS
  icon at 192 and 512 pixels (`any`), a maskable one whose gradient
  fills the square and whose panes keep within the middle circle a
  launcher's mask leaves (192, 512), and iOS's 180-pixel
  `apple-touch-icon.png`. The build copies them into `dist/pwa`.
- **Install**: Settings, About, shows Install GoRex with an Install App
  button when the browser offered its install prompt
  (`beforeinstallprompt`, Chrome and Edge); the button shows the
  browser's prompt. In a browser of iOS or iPadOS it shows how to Add to
  Home Screen instead. Installed, or in a browser that offers neither
  (Firefox, Safari on the Mac), it shows neither.
- **Keyboard**: in the app's window (`display-mode: standalone`), the
  page asks for Keyboard Lock (`navigator.keyboard.lock`, Chrome and
  Edge) for every key but Escape, so that the browser's own chords
  (Ctrl+W, Ctrl+T, Ctrl+N, Ctrl+Tab) can reach the terminal. A browser
  holds the locked keys only in fullscreen; outside it they stay the
  browser's, so in an app window that is not fullscreen they may still
  close the window or open a tab. Escape is never locked, so it exits
  fullscreen at once, as ever, and still closes the page's dialogs. The
  page's own shortcuts (Ctrl+, for Settings, Ctrl+Shift+C for Copy and
  Ctrl+Shift+V for Paste) are the page's either way. A tab asks for no lock.

### Service Worker

`web/sw.js` is the page's Service Worker, at the root of its scope. The
build (`writeWorker` in `build.mjs`) writes into `dist/sw.js` this
build's version, a hash of `web/sw.js`, the page and its files, and the
files it keeps: all of `dist` but `index.html`, `_headers`, `sw.js` and
`assets/lazy/` (the themes, the Tailscale engine and the CJK font's
chunks; see Loading on demand), about 2.5 MB in all, the page's code
chunks among them: all of them, about 46 KB that load on demand, so
that Settings, the guide and the viewer open offline too. A build that
changes none of them is the same worker.

- **Install**: it fetches the page (the scope's root, checked to name
  this build's script, so that a deploy between the two fetches fails
  the install rather than mixing versions) and the files into a cache of
  its version, `gorex-<version>`. **Activate** deletes the other
  `gorex-` caches, but the loader's `gorex-assets-v<n>`, and takes the
  open pages.
- **Answers**: the page (a navigation to the root or `index.html`) and
  `/assets` (hashed names) from the cache first; an `/assets` file
  not kept joins the cache when fetched.
  `/assets/lazy/` it leaves to the page's loader, which keeps those
  files in its own cache (Loading on demand). The
  rest of the page's origin (fonts, icons) comes from
  the network, and from the cache when the network fails. So the page
  opens offline, at the version the worker holds.
- **Leaves alone**: every request to another origin (the GoRex hosts,
  Tailscale's control server and relays), every request that is not a
  GET, and `/api/`. It answers none of them, so the browser sends them
  as it would without a worker. WebSockets never pass through a worker.
- **Updates**: the browser checks `sw.js` (`no-cache`, registered with
  `updateViaCache: "none"`) at each load, and the page asks again when
  its tab comes back and hourly; Settings, About, Check for Updates
  asks at once and says what it found. A new version installs beside the
  running one and waits; the page then shows "A new version of GoRex is
  ready." with Reload, a glass panel in the corner, as the host list's,
  that takes no keys. Nothing reloads by itself: a reload ends the
  terminals' connections and this tab's Tailscale sign-in. Reload asks the
  waiting worker to take over (`skip-waiting`), and the page reloads
  once when it has (`controllerchange`). The × hides the panel until
  the next load. The first worker installed is not a new version and
  shows nothing.
- **Downloads over the tailnet** (docs/web-tailscale.md, Downloads): with
  a worker controlling the page, a tailnet host's file streams to the
  browser's own download, with no limit of size. The page posts
  `save` with a random id, the file's name and a `MessagePort`; the
  worker makes a stream that asks the page for each chunk in turn, and a
  hidden frame opens `save/<id>`, which the worker answers once, as an
  attachment (`Content-Disposition` with the name, `nosniff`); a
  second fetch of it is 404. The page sends `ping` every 10 s while it
  lasts, which keeps the worker awake. Cancelling in the browser's
  download list cancels the read from the host. Without a worker (the
  first load, the dev server) it is the blob of before, capped at
  256 MiB.
- **Dev server**: `dev.mjs` builds with `__GOREX_SW__` false, so the
  page registers no worker, and serves a `sw.js` that removes itself and
  the `gorex-` caches: a worker a build left on the same origin goes at
  the next load instead of serving that build's page.
- **Another directory**: `GOREX_WEB_DIST=<dir> npm run build` builds
  into `<dir>` rather than `web/dist`, as a check beside a running dev
  server.

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

You need Node 22 or later and Git. The script gets Zig itself. Go (the
version `go.mod` names) builds the Tailscale engine; without it the build
skips the engine with a message, and the page works without built-in
Tailscale.

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
   It keeps `ghostty-vt.wasm` (about 910 KB) in the cache, so the next
   build skips this step. `--rebuild-wasm` builds it again.
4. It runs `npm ci` in `web/` if `web/node_modules` is missing, checks
   the catalogs and writes `src/locales.gen.ts` (`web/i18n.mjs`; see
   Languages), then type-checks the client (`tsc --noEmit`).
5. It empties `web/dist` and writes in `dist/assets`, under hashed
   names (see Cloudflare Workers), `ghostty-vt.wasm` and, in
   `dist/assets/lazy`, `ios/GoRex/ghostty-themes.json` (the repo keeps
   one copy of the themes). It cuts Maple Mono NF CN into chunks (see
   Fonts) and writes them to `dist/assets/lazy`, its index to
   `dist/assets` and its licence to `dist/fonts/MapleMono-OFL.txt`.
6. With Go, it builds the Tailscale engine (`web/tailnet`,
   docs/web-tailscale.md): `GOOS=js GOARCH=wasm`, tsconnect's build tags
   and five more, `GOEXPERIMENT=nojsonv2`, `-trimpath -ldflags "-s -w"`.
   It writes only the gzipped module, `dist/assets/lazy/tailnet-<hash>.wasm.gz`
   (about 6.1 MB; the raw module, about 26 MB, is not shipped), and Go's
   `wasm_exec.js` as `dist/assets/lazy/tailnet-<hash>.js`.
7. It bundles the client with esbuild into ES modules, split at each
   `import()` (see Loading on demand), and lists all of these files in
   the asset manifest, `dist/assets/manifest-<hash>.json`, which the
   script names. esbuild runs twice: once to learn the chunks, which the
   manifest lists, then with the manifest's name, which only the script
   (`src/main.ts`) reads; the build fails if a chunk changed between the
   two. `index.html`, which it writes with `_headers`, names the script.
   It copies the JetBrains Mono fonts from `assets/fonts` (with their
   licence) into `dist/fonts`, and `assets/icons` and `assets/brands`
   into `dist/icons` and `dist/brands`, the glyphs of the program tiles
   and of the hosts.

As the page starts, it loads the terminal's wasm (about 910 KB) with a
progress bar over the window's room: the bytes received of the size the
build wrote in, then "Starting…" while it compiles. The bar shows only
when the load takes more than 150 ms, so a load from the cache shows
none. A failure shows the reason and **Retry**. The loader fetches it
(`response`, Loading on demand), so a network error or a 5xx answer is
tried again first. Served as `application/wasm`, the module compiles as
it arrives (`WebAssembly.instantiateStreaming` on the fetch's own
response, which lets Chrome keep the compiled code with the cached file)
while the loader counts a clone's bytes. The Tailscale engine's download
in the sign-in guide uses the same bar (`web/src/progress.ts`).

The cache is `$GOREX_WEB_CACHE`, else `$XDG_CACHE_HOME/gorex-web`, else
`~/.cache/gorex-web`. `go build ./...` and the Go tests need no web build.

`npm test` runs the unit tests of the modules with no DOM
(`web/test/*.test.ts`, by `web/test.mjs`); it needs `npm ci`, not the
build. CI's `web` job runs it on every pull request.

## Fonts

The terminal's font is JetBrains Mono, which the page serves from
`dist/fonts` as the desktop embeds it, or a family Settings chooses. Its
CJK font is always Maple Mono NF CN
([subframe7536/maple-font](https://github.com/subframe7536/maple-font),
SIL Open Font License 1.1, as the iOS app's `Maple.swift` downloads it):
the font stack is `"<chosen>", "Maple Mono NF CN", monospace`, and the
system's CJK font never draws. Maple is also a choice of its own in
Settings, its Latin with its CJK. What neither font has (Hangul, which
Maple CN lacks; emoji) the system draws.

**Build.** `web/fonts/maple.mjs` (which `build.mjs` runs; `node
web/fonts/maple.mjs` alone lists the chunks) takes the four faces
(Regular, Bold, Italic, Bold Italic) of the pinned v7.9 release, by HTTP
ranges of `MapleMono-NF-CN.zip` (the zip's central directory, then each
face, inflated and checked against its CRC), checks each face's SHA-256
against the one pinned in the script, and keeps them, with the release's
`LICENSE.txt`, in `<cache>/maple-v7.9/`. It cuts them with harfbuzz's
subsetter (harfbuzzjs) and compresses them to woff2 (wawoff2), both
pinned in `package.json`, a worker a face (about a minute), into
`<cache>/maple-v7.9/chunks-<key>/`, `key` a hash of the script, the order
file, the faces and the tools. A build after the first takes the chunks
from there, with no network. A build that can neither find the faces nor
fetch them goes on without Maple, with a warning; that page draws no CJK
text at all, and Settings says so. Nothing of it is committed but the
script and `web/fonts/cjk-order.txt`.

**Chunks.** By unicode-range, as Google Fonts cuts its CJK families. The
same 95 ranges for every face: chunk 0 is U+0000 to U+1FFF (Latin, Greek,
Cyrillic), the next two the symbols up to U+2E7F, then CJK punctuation,
kana, Bopomofo and fullwidth forms; then the most frequent CJK
characters, 200 a chunk, in the order of `cjk-order.txt` (the frequency
slices of Google Fonts' simplified and traditional Chinese slicing,
Apache 2.0); then the rest of CJK by code point, 400 a chunk, then the
Nerd Font icons and what remains. The chunks are not hinted. In all, 32.1
MB: Regular 7.68 MB, Bold 7.85 MB, Italic 8.22 MB, Bold Italic 8.36 MB;
a chunk is 5 to 170 KB. A screen of everyday Chinese takes some 6 to 10
chunks of its face, 300 to 600 KB.

**Loading.** The page reads the chunks' index (`maple/index`, 81 KB) as
it starts. The browser does not load a font's chunk for canvas text, and
draws a system font meanwhile, so `web/src/maple.ts` adds each chunk to
`document.fonts` as a `FontFace` with its range only once it has loaded
(through the loader), and the terminal draws a character only when the
chunk of its face (bold text takes the Bold face's) is in: until then its
cells show nothing, and the chunk is asked for. The chunks a frame asks
for load together; as they arrive, the panes draw again, once a frame.
Wide characters keep two cells: Maple's CJK is twice its Latin advance,
centred in the cells and smaller when the chosen font's cells are
narrower. When Maple is the chosen font its Latin chunk of every face
loads before the cells are measured. The Settings preview shows once the
chunks of its text are in.

## Loading on demand

The page loads at start only what its first frame needs, and the rest
when it is first used. `web/src/loader.ts` loads both: the files of the
asset manifest by name, and the page's code chunks.

### What loads when

Sizes are a build's (raw, then brotli, as Cloudflare serves them).

| When | What | Size |
| --- | --- | --- |
| Boot | `index.html` (its styles inline) | 49 KB, 9 KB |
| Boot | The script, `app-<hash>.js`, and the 5 chunks it imports (code it shares with the chunks below) | 198 KB, 59 KB |
| Boot | The asset manifest | 53 KB, 7 KB |
| Boot | The terminal's wasm, `ghostty-vt` | 914 KB, 235 KB |
| Boot | Maple's index, `maple/index` | 81 KB, 17 KB |
| Boot | JetBrains Mono, four faces (`dist/fonts`, as the CSS asks for them) | 1.1 MB |
| First visit, no host | The welcome screen, `js/welcome` | 2.5 KB |
| First open | Settings, `js/settings` | 18 KB, 6 KB |
| First open of Settings › Theme | The theme list, `ghostty-themes` (lazy) | 195 KB, 58 KB |
| First open | The Tailscale guide, `js/guide` | 10 KB |
| Tailscale turned on | `js/tailengine`, then Go's `wasm_exec.js` (`tailnet/exec`, lazy) and the engine (`tailnet/engine`, lazy) | 3 KB, 17 KB, 6.06 MB |
| First link opened in a pane | `js/files` | 5.6 KB |
| First image shown | The image viewer, `js/viewer` | 6 KB |
| Never, unless used | Maple's chunks (lazy), each as the terminal draws its characters | 5 to 170 KB each, 32 MB in all |

Before this, a boot took the script whole (236 KB), the theme list and
the manifest: 1,425 KB of script, wasm and JSON (373 KB brotli), now
1,246 KB (318 KB brotli), in 9 requests rather than 5.

### The manifest

`build.mjs` lists the files in one asset manifest,
`assets/manifest-<hash>.json`, which the script names
(`__ASSET_MANIFEST__`):

```json
{ "version": 1, "assets": { "maple/r/12": { "url": "assets/lazy/maple-r12-<hash>.woff2", "bytes": 64496, "hash": "<hash>", "kind": "font", "lazy": true } } }
```

`hash` is the first 16 hex digits of the file's SHA-256, which its name
carries (a code chunk's name carries esbuild's hash instead). `kind` is
`wasm`, `json`, `font`, `script` or `data`. A `lazy` file is under
`assets/lazy/`. It lists `ghostty-vt`, `maple/index`, the lazy
`ghostty-themes`, `tailnet/engine`, `tailnet/exec` and
`maple/<face>/<n>` (`r`, `b`, `i`, `bi`), and the code chunks as
`js/<module>` (`js/settings`) and `js/chunk-<hash>` (shared code). The
script also carries the entries it needs at boot (`__BOOT_ASSETS__`:
`ghostty-vt`, `maple/index`), so the wasm starts to load before the
manifest is read.

### Code chunks

esbuild bundles the client as ES modules (`<script type="module">`),
split (`splitting`): each dynamic `import()` is a chunk, in
`dist/assets` as `<module>-<hash>.js`, and the code that chunks share
with the script, or with each other, goes in `chunk-<hash>.js`, which the
script imports at once. `index.html` names only the script; nothing is
preloaded. The chunks are the Settings dialog, the Tailscale guide, the
welcome screen, the code that runs only with the Tailscale engine
(`tailengine.ts`), opening a file (`files.ts`) and the image viewer.
What every frame uses (the terminal, panes, links and the pointer, the
host list, the catalogs) stays in the script. `script-src 'self'`
covers the chunks.

`chunks.ts`'s `Chunk` loads one the first time it is used
(`loader.module`); later uses run at once, from memory. A load that
takes more than 150 ms shows a small panel in the corner, "Loading
Settings…" with a busy bar (`progress.ts`); a failure shows "Couldn't
load Settings", the reason, **Retry** (which runs what waited, as
opening the dialog) and ×. Opening a file reports a failure of its chunk
on the status line, as its other errors.

### The loader

The page's loader is `loader` (devtools and tests: `gorexLoader`):

- `configure(url, boot)`: the manifest's name and the boot entries
  (`main.ts`); `init()` reads the manifest once.
- `asset(name)`, `names(prefix)`: the manifest's entries.
- `load(name, { signal, progress })`: the file's bytes. One request for
  all its callers at once; a network error or a 5xx answer is tried
  again after 0.5, 1 and 2 s; each caller's `signal` ends its own wait,
  and the request stops when no caller waits; `progress(received, total)`
  hears the bytes (`progress.ts`, as the progress bars do). The bytes
  stay in memory until `release(name)`.
- `json(name)`: the same, parsed.
- `response(name, { progress })`: a boot file's response, for
  `WebAssembly.instantiateStreaming`, tried again as `load` is; a
  clone's bytes give the progress.
- `module(name, importer)`: a code chunk, by its `import()`, once; a
  failed one is tried again as a file is, and again at the next call.
  `script(name)`: a classic script of the manifest (`wasm_exec.js`),
  once.
- `stats()`: each load (name, bytes, `memory`, `cache` or `network`,
  milliseconds, tries; a file the page fetched counts as `cache` when
  its resource timing shows no bytes transferred), the bytes from the
  network and from the cache, the bytes in memory, the loads under way
  and the modules in.

### Who caches what

- **The Service Worker** (see App) keeps the page and everything in
  `dist` but `assets/lazy/`: the script and all its chunks (about 46 KB
  of them load on demand; all are kept so that every part of the page
  opens offline), the wasm, the manifest, Maple's index, the fonts and
  icons. Its cache is per version, `gorex-<version>`.
- **The loader** keeps the lazy files in Cache Storage,
  `gorex-assets-v1`, across versions, and reads them there first: the
  themes, the Tailscale engine and Maple's chunks. A file read there or
  fetched is checked against its hash (with `crypto.subtle`, in a secure
  context). `init` deletes the entries the manifest no longer lists and
  the caches of older numbers. The worker leaves `assets/lazy/` and this
  cache alone: it would fetch all of them (38 MB) at install and drop
  them with each version.
- **The browser's HTTP cache** keeps every `/assets` file a year
  (`immutable`), and is what keeps the lazy files without Cache Storage
  (plain `http` on a host other than loopback, as a dev server on a LAN
  address).

## Languages

The page is in English and Simplified Chinese. Settings › Appearance ›
Language chooses System, English or 简体中文 (each language by its own
name), kept as `language` in `gorex.settings`. System takes the first of
the browser's languages (`navigator.languages`) that the page has: `zh`,
`zh-CN`, `zh-SG` and `zh-Hans` are Simplified Chinese; `zh-TW`, `zh-HK`
and `zh-Hant` are not, and get English, as the desktop gives them (MyGo's
`zh-Hant` falls back to the source language). A change applies at once,
with no reload: the sidebar, the status line, the open dialogs, the
guide and the viewer say their words again, and `<html lang>` follows.
System follows a change of the browser's languages too
(`languagechange`).

The catalogs are `web/locales/en.json` (the source) and
`web/locales/zh-Hans.json`, ARB-style JSON as the desktop's `locales/`:
`"@@locale"`, the messages by camelCase key in ICU MessageFormat
(`{name}`, `{n, plural, one {…} other {…}}`, `{x, select, … other {…}}`,
`#` in a plural, apostrophe quoting; `web/src/icu.ts`), and in `en.json`
an `"@key"` for each with a description of where it shows and its
placeholders (`String`, `int` or `num`). Both catalogs are in the bundle:
without the metadata they are about 7 KB gzipped each, less than the
request a lazy load would cost, and they need no Service Worker change.
Numbers, sizes, lists and dates go through `Intl` in the page's language
(`formatNumber`, `formatSize`, `formatList`, `formatDate` in
`web/src/i18n.ts`). Commands to copy (`gorex rexapi …`), product names
(GoRex, Tailscale, Ghostty), program and theme names, host and file
names, the keys of a shortcut, and what a host or the browser says (an
error's own words) stay as they are.

`web/i18n.mjs` checks the catalogs and writes `web/src/locales.gen.ts`
(not kept in git): each language's messages and the type of every key's
arguments, so that `t("key", { … })` with a key `en.json` lacks, or
without an argument its message uses, fails `tsc`. The build runs it
before `tsc`, and `npm run dev` again when a catalog changes. It fails on
a message that does not parse, an `en.json` message whose placeholders
its `"@key"` does not declare exactly, a key of another language that
`en.json` lacks, and a translation whose placeholders differ from
`en.json`'s. A key missing from a translation is a warning, and the page
says it in English; with `$CI` set, or `--ci`, it fails the build.

To add a string:

1. Add the key and its English to `web/locales/en.json`, with its
   `"@key"`: where it shows, and each placeholder with its type and an
   example.
2. Add its translation to `web/locales/zh-Hans.json`, with the same
   placeholders. Use the desktop's wording (`locales/zh-Hans.json`) for
   the same thing.
3. Say it with `t("key", { … })` from `web/src/i18n.ts`. A string kept
   to show later (a status line, an error) is a function, `() => t(…)`
   (`Msg`), so that it is said again in a new language; an element whose
   words do not change otherwise takes them in `localize(el, …)`.

To add a language: add `web/locales/<tag>.json` (a BCP 47 tag, as
`zh-Hant` or `de`) with `"@@locale"` and every key of `en.json`, its
`languageName` in that language, and teach `supported()` in
`web/src/i18n.ts` which browser languages are it. Settings lists it at
once.

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
- `web/src/tailnet.ts`: built-in Tailscale: loading the engine, the
  node's state and sign-in, finding hosts in the network map, and the
  tailnet transport (`SocketLike` and a streamed `Response` over the
  engine). `web/src/guide.ts`: the sign-in guide. `web/src/progress.ts`:
  a load's progress bar.
- `web/tailnet`: the engine, in Go (docs/web-tailscale.md).
- `web/src/hostconn.ts`: one host's connection (`HostConn`): its
  control connection and reconnect token, the retries and their
  backoff, the sessions and their views, and the titles, programs and
  records of its blocks. It tells the page what changed (`HostEvents`).
  At most four connections open at a time, page-wide (`opensAtOnce`),
  each holding its turn 3 s at most (`slotWait`); `Control.open` gives up
  after 10 s (`openWait` in `rex.ts`).
- `web/src/sidebar.ts`: the tabs as Tab placement puts them (the bar
  above the panes, or the sidebar by space or by host), drawn from every
  `HostConn`: each host's state dot and problem line, and its rows, dimmed
  while it is not connected. Each row's `data-host` and `data-window`
  name it, and a dragged row or pane names its host. It updates once per
  animation frame, for every host.
- `web/src/main.ts`: the hosts' connections, all at once; the selection
  (the host of the shown window), the shown window, the notice and the
  status line of the selected host, what the sidebar's rows and menus do,
  and the window's commands. A window (`WindowView.conn`) and its panes
  use their own host's connection. The windows shown before the shown one
  stay live in `kept` (see Hosts).
- `web/src/place.ts`: `gorex.place` and `gorex.tailnetOpened`, and the
  move of `gorex.host` into `gorex.place`. It has no DOM: `npm test`
  (`web/test.mjs`) bundles each `web/test/*.test.ts` with esbuild and runs
  it with `node --test`, as CI's `web` job does.
- `web/src/window.ts`: a window's layout tree, its panes and dividers in
  its own room on the stage (`.window-room`), the focus (see Split
  panes), and `park` and `unpark`, which take a kept window's room off the
  stage and put it back.
- `web/src/manage.ts`: the menus of a right click or a long press, the
  field that renames in place, Close Space's question, and the drags of
  a window's row and a pane's header (see Spaces, windows and moves).
  main.ts says what each item does.
- `web/src/pane.ts`: one pane: its stream, its terminal and its canvas,
  and the text field that holds its keys (see Typing and paste).
  Typed bytes go to `com.superlogical.terminal.write`, one write at a
  time, 128 KB at most in one (the gateway reads 1 MiB a message), so a
  large paste goes in pieces in order. Bytes typed while the connection is
  down wait (up to 4 MB, as in the iOS app) and go a second later or when
  it is back; a paste that would pass that bound is dropped whole, with a
  status message. The pane takes
  its block (`session.own_block`) and sizes it to its canvas
  (`com.superlogical.terminal.resize`); a pane that closes, or is kept off
  the stage (`park`), while its block stays gives it back
  (`session.disown_block`). A kept pane reads its stream but does not
  paint, own or size its block until it shows again (`unpark`). A pane
  that closes with typed bytes not yet written says how many in the
  status line.
- `web/src/keys.ts`: the shortcuts (see Shortcuts).
- `web/src/pointer.ts`: the pointer over a pane's grid: selecting, the
  handles and the Copy and Paste menu, the wheel and a finger's swipe, and the mouse
  events a program gets (see Selection and the mouse).

## Split panes

The sidebar has a heading for each host (its state dot while it
reconnects or fails, and a line with its problem, **Retry** and **Edit**),
a heading for each of its sessions (with a + button for a new window,
off while the host is not connected) and a row for each window: the name the user gave it, else its
focused pane's name or terminal's title, else its label, and its count of panes when it has
more than one. A label is the user's when it is not the host's default,
`Window ` and the last 8 characters of the window's ID, as the iOS app
tells. Its close button shows on hover. A click shows the window. While
its host is not connected, the host's rows stay as last seen, grey and
faded (DESIGN.md's `dimmed`, the `.dimmed` class), and a click shows the
window's last layout with the host's notice. Where
the sidebar is, and whether there is one, is the Tab placement setting's
(see Tab placement and the sidebar view).

- **Layout.** The page draws the tiled layer's layout tree from
  `session.view`: each `split` divides its room by its `ratio`,
  `horizontal` side by side and `vertical` one above the other. The panes
  are 8px apart, as the desktop's (`gap` in `view.go`), with the desktop's
  pane border and shadow. A zoomed block (`zoomed_block_id`) fills the
  window alone; the hidden panes keep their blocks' sizes, and send them
  again when they show.
- **Panes.** Each pane has its own data stream, its own libghostty-vt
  terminal and canvas, and owns and sizes its own block. It has a header,
  as the desktop's: the pane's name, else the terminal's title, and on the focused or hovered
  pane the buttons Split Right, Split Down, Zoom and Close.
- **Animations.** As the desktop's `paneTransition` (`animation.go`), a
  change of the window's layout from the host (a split, a closed pane, a
  zoom, a move) or a key that moves a divider moves the panes to their new
  places, and a new pane and a gone one pop (scale 0.92, faded), with the
  Animation style setting: Snappy (320 ms, a little overshoot), Natural
  (500 ms, none) or Off. A dragged divider, the window shown first, and a
  system that asks for less motion (`prefers-reduced-motion`) move at
  once. While the panes move their terminals keep their sizes, and send
  the new ones once they rest, as on the desktop (`window.ts`, `moving`).
- **Focus.** A click or a shortcut focuses a pane: the keys go to it, its
  border and shadow are the focused ones, and its cursor is solid (hollow
  in the others). The desktop does not dim panes that are not focused, so
  this page does not either. The page tells the host
  (`session.focus_block`), and follows the host when the host's focused
  block of the window changes.
- **Live.** The page follows every host's `session_view_changed` and
  reads only the changed session's view again (`session.view`). Panes whose blocks stay
  keep their streams; new blocks get panes and streams; panes of closed
  blocks end. Kept windows follow their views too. A closed window shows the session's active one. Sessions
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

### Spaces, windows and moves

A session is the desktop's Space, and a window its project tab (GoRex's
gateway maps them so: `internal/rexapi/doc.go`). The page manages them
with the desktop's commands, words and entry points (`tabMenu` in
`tabs.go`, `spaceMenu` and `workspaceConfirm` in `workspaceview.go`,
`paneMenu` in `view.go`, `panedrop.go`), and the iOS app's calls
(`ios/GoRex/Rex/Host.swift`). A right click opens a menu, and so does a
touch held for half a second (`web/src/manage.ts`, `onMenu`). Every
change goes to the host; the sidebar and the panes follow the session's
new view (`session_view_changed`, `session_label_changed`), as for a
change made elsewhere.

| What | Where | Method |
|---|---|---|
| New Space: a session with one window running the shell, shown; the host names it | The + at the right of the sidebar's foot (the desktop's space strip has it), Alt+Shift+N | `session.create` with `initial_windows` |
| Rename Space: a field in the heading; Enter or leaving it keeps the name, Escape goes back, an empty name keeps the one it has | The heading's menu | `session.set_label` |
| Close Space: asks first, as the desktop does, how many terminals end; the page then shows another session's window | The heading's menu | `session.destroy` |
| Rename Window: a field in the row; an empty name gives the window its terminal's title again | The row's menu, a second click on the row within half a second (the desktop's double click), Alt+Shift+R for the shown window | `session.set_window_label` |
| Reset Name | The row's menu, when the window has a name | `session.set_window_label` with `""` |
| Move a window up or down in its session | The row's menu (Move Up, Move Down); drag the row onto another row of its session: the upper half puts it before, the lower half after, a 2px line shows where | `session.move_window` with `before_window_id`, none for the end |
| New Window, Close Window, Close Other Windows | The row's menu | `session.new_window`, `session.close_window` |
| Swap two panes of the window | Drag a pane by its header onto the middle of another (0.2 of its width and height each way of its centre, as `paneDropZone`), which shows the zone | `session.swap_blocks`; the dragged pane has the focus |
| Move a pane into another window of its session, beside that window's focused pane | The header's menu (Move to Window), or drop the header on that window's row, which shows a ring | `session.move_block`, `horizontal`, `after` |
| Rename Pane: a field in the pane's header; Enter or leaving it keeps the name, Escape goes back, an empty name gives the pane its terminal's title again | The header's menu (Rename Pane…) | `session.set_block_label` |
| Reset Pane Name | The header's menu, when the pane has a name | `session.set_block_label` with `""` |
| Move a pane to a window of its own, just after its window; the shown window stays (the desktop's Move Pane to New Tab, `movePaneToNewTab`) | The header's menu (Move Pane to New Window), in a window of two panes or more, not zoomed | `gorex.move_block_to_new_window` |
| Merge a window into another: its layout goes at the right of the other's, half each, and that window shows (the desktop's `mergeTab`) | Drag the row onto the middle half of another row of its session, which shows a ring; the outer quarters order the windows as above | `gorex.merge_windows` |

A pane's name is the block's label in the view; the gateway gives an
unnamed pane the label `shell`, so the page reads `shell` as no name (a
pane its user named `shell` shows its title).

A move keeps the blocks: their programs go on and their terminals keep
what they show. A pane that stays in the window keeps its data stream; a
pane that leaves the shown window closes its stream without giving its
block back. A dragged row or header is dimmed (grey, 0.4), as the
desktop's `dimmed`. While a field renames or a row drags, the sidebar
holds still and is made again after. A failure shows in the status line.

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
| Next, previous window, across hosts (desktop: Ctrl+Tab, Ctrl+Shift+Tab) | ⌥⇧] and ⌥⇧[ | Alt+Shift+] and Alt+Shift+[ |
| Next, previous space, across hosts (desktop: Ctrl+Shift+PageDown and PageUp, the browser's tab moves) | ⌃⌘] and ⌃⌘[ | Alt+Shift+PageDown and Alt+Shift+PageUp |
| Rename the window (desktop: ⇧⌘R, Ctrl+Shift+R, which reload the page) | ⌥⇧R | Alt+Shift+R |
| New Space (desktop: ⇧⌘N, Ctrl+Shift+N, the browser's new window) | ⌥⇧N | Alt+Shift+N |
| Settings | ⌘, | Ctrl+, |
| Copy the selection | ⌘C | Ctrl+Shift+C |
| Paste | ⌘V | Ctrl+Shift+V, Shift+Insert; on Windows also Ctrl+V (see Typing and paste) |

Next and previous window go through the windows in the sidebar's order,
every connected host's; a host that is not connected is skipped. Next
and previous space show the next space's window shown last (else its
active one), in the same order.

A key moves the divider of the nearest split of that direction around the
focused pane by 5%. As on the desktop on Windows and Linux, Alt and an
arrow (word movement in some shells) is the focus's there, not the
terminal's.

Two moves need a newer gateway, and the page shows them only when the
gateway's welcome lists `gorex.move_window` (`gateway` in
`web/src/main.ts`); GoRex's gateway took both in the same change set:

- Move to Space (the desktop's `moveTabToSpace`), in the window row's menu
  (Move to Space, a submenu of the other spaces): `gorex.move_window` with
  `session_id`, `window_id` and `to_session_id`. The source session is
  destroyed when the move empties it.
- Dropping a pane on another pane's edge, to go beside it in the same
  window (the desktop's `movePaneBeside`; `paneDropZone`, `zoneSplit`):
  `session.move_block` with the target as anchor, a `direction` and a
  `side` (before or after). Without the feature the edge zones are off and
  only the middle (swap) takes a drop.

Merging windows and moving a pane to a window of its own are GoRex's own
methods too (`internal/rexapi/mergewindows.go`), as Rex's
`session.move_block` can neither put a window's tree beside another's
nor give a block a window of its own. The page offers each only when
the welcome lists its feature: `gorex.merge_windows` (a GoRex server
that takes a window's layout, `tab.set`), and
`gorex.move_block_to_new_window` (every gateway that has the method).
Without `gorex.merge_windows` a row dropped anywhere on another orders
them, by its halves, as before. The gateway refuses a merge (`conflict`,
in the status line) of windows with panes of different hosts, of a
window that holds a note or a browser pane (`tab.set` moves only a
terminal's pane), and of windows changed since it read them (one
`layout.apply` checks both windows' revisions). The view shows neither
other hosts' panes nor those of no terminal, so the page cannot tell
first, and its drop shows the ring.

### Tab placement and the sidebar view

The Tab placement setting (see Settings) places the windows of the
sessions as the desktop places its tabs (`placements` in `sidebar.go`),
in `web/index.html` by `body[data-tabs]`:

- **Above**, the default, as the desktop's: a bar over the panes with the
  host button, a chip with the shown space's name, the space's windows as
  rows side by side, a + for a new window, and Settings and New Space at
  its end. The chip opens the spaces' menu, as the desktop's workspace
  chip: every space (a click shows its active window), New Space, Rename
  Space and Close Space; its right click has the space's own menu. Rows
  order and merge as in the sidebar, by their left and right quarters
  and middle half.
- **Left Side**: the sidebar at the left of the panes, as before.
- **Right Side**: the sidebar at the right; the host list opens at the
  right. On a phone's width the sidebar stays over the panes.

While the tabs are in a sidebar, the Sidebar view setting chooses what it
lists, as the desktop's (`sidebarViews`): **Hosts** lists each host of the
host list and those built-in Tailscale found, under its name, each with
its spaces and windows (the desktop's `hostSessions`); a tailnet host
never connected is a button that connects to it. **Spaces** lists every
connected host's spaces and their windows, under the host's name once
there is more than one host. The desktop groups the spaces of all hosts
into its own spaces; the page does that once it reads `/api/spaces`
(docs/web-multihost.md, PR 6). The bar above lists the shown host's
space.

### Not in the web client

- Floating layers (floats): only the tiled layer shows. A block that is
  only in a float has no pane here.
- A space's color, and the desktop's space strip.
- The pane's program icon and its activity badge; the animations of the
  tabs and of the sidebar showing and hiding (the page's sidebar does not
  hide).

## Typing and paste

Each pane has a hidden text field (`textarea.pane-input`, in
`web/src/pane.ts`) that holds the pane's keyboard focus: a click on the
pane, or the window's focus following to it, focuses the field. The field
is transparent, takes no click, is out of the layout (absolute, clipped by
the pane) and stays empty. After each draw the pane moves it to the
terminal's cursor cell (`Terminal.cursorCell`), so that an input method's
candidate window opens at the cursor, and a phone shows its keyboard for
it. The page's window shortcuts (`main.ts`) still see the keys of this
field, which is not a form field for them.

- **Keys.** A key still goes through the encoder (`keydown`, as above) and
  its default action is cancelled, so the field gets nothing from it. A
  key that an input method took (`isComposing`, or `keyCode` 229 as Safari
  and soft keyboards report it) is not encoded.
- **Input methods (pinyin and others).** While composing
  (`compositionstart`, `compositionupdate`) nothing goes to the program;
  the page draws what has been composed, underlined, at the cursor (the
  field itself is transparent). `compositionend` sends the committed text
  as UTF-8, and clears the field. A cancelled composition has no text and
  sends nothing. A browser that says the same text again as plain input
  just after the composition ended (Safari, Firefox) is not sent twice.
- **Soft keyboards.** Text typed that no key reported (`beforeinput`,
  `insertText`) goes to the program, a line break as a carriage return
  and `deleteContentBackward` as Backspace (0x7f). The page cancels every
  `beforeinput`, so that the field stays empty.
- **Paste.** Cmd+V on a Mac, Ctrl+Shift+V and Shift+Insert elsewhere, and
  the browser's own Paste menu send no key: the browser pastes into the
  field, and the `paste` event's `text/plain` goes to the program. As the
  desktop's (`internal/rex/paste.go`), lines end with a carriage return;
  a program that enabled bracketed paste (mode 2004) gets the text between
  ESC [ 200 ~ and ESC [ 201 ~ with any ESC [ 201 ~ inside it removed, so
  that it does not run the lines as commands; another program gets the
  text without control characters other than Tab and the line ends. Paste
  clears the selection and shows the end of the screen, as typing does.
- **Ctrl+V.** The desktop's terminal binds Ctrl+Shift+V everywhere and
  Cmd+V on a Mac, and, on Windows only, Ctrl+V when the clipboard holds
  text (MyGo's `plugins/terminal/keybind.go`, a performable bind). The
  page does the same: on Windows Ctrl+V pastes text, and with no text on
  the clipboard (empty, or an image) the program gets ^V. On a Mac and
  on Linux Ctrl+V is a key and goes to the program.
- **Paste in the pane's menu.** The right-click menu (and the menu after a
  finger's selection) has Paste next to Copy where
  `navigator.clipboard.readText` exists (a secure context); it reads the
  clipboard and pastes as above. Where the browser refuses, the status
  line says so; where there is no `readText`, the item is not shown (the
  keys still paste).

Not in the web client: pasting images or files.

## Drawing, as the desktop draws

The terminal draws as the desktop's (MyGo's `plugins/terminal/paint.go`)
with GoRex's defaults and no Ghostty configuration:

- Font: JetBrains Mono, served with the client (`defaultFontFamily`), then
  Maple Mono NF CN for CJK (see Fonts), then `monospace`, at 15 CSS
  pixels (`defaultFontSize`). Settings can choose another family and size
  (see Settings).
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
- Scrolling: as Ghostty's wheel (see Selection and the mouse). Typing
  scrolls back to the screen.
- Selection: the selected cells in the theme's selection colours
  (`selection-background` and `selection-foreground`), else inverted, as
  Ghostty draws them when the colours are unset. The host's colours have
  none, so they invert. (The desktop draws its foreground at 0.3 over
  them instead.)

## Selection and the mouse

As the iOS app (`TerminalView+Selection.swift`) and Ghostty. libghostty-vt
does the work: its selection gesture (`ghostty_selection_gesture_*` in
`selection.h`) turns presses and drags into a selection by cell, word or
line, the terminal keeps it (`GHOSTTY_TERMINAL_OPT_SELECTION`) as tracked
references, so that it follows the text as output comes and the scrollback
moves, the render state gives each row's selected columns
(`GHOSTTY_RENDER_STATE_ROW_DATA_SELECTION`), and a formatter
(`formatter.h`) given the selection (`GHOSTTY_FORMATTER_OPT_SELECTION`)
gives the text as Ghostty copies it (plain, wrapped lines joined,
trailing spaces trimmed). Its mouse
encoder (`ghostty_mouse_encoder_*` in `mouse/encoder.h`) encodes what a
program gets.

- **Mouse.** A press and drag selects by cell; a double click selects a
  word and the drag extends it by words; a triple click a line, by lines
  (Ghostty's 500 ms and one cell between clicks). A drag past the top or
  bottom edge scrolls the scrollback a row every 15 ms and extends the
  selection. A single click clears it.
- **Finger.** A long press (500 ms, within 10 CSS pixels) selects the word
  under the finger, as iOS's, and the drag that follows extends it by
  words, scrolling on past an edge. Two handles stand at the ends, a stem a
  line high with a knob, and drag each end; a menu with Copy shows above
  the selection (and Paste, where the browser lets the page read the
  clipboard). A tap elsewhere clears it. A one-finger swipe scrolls as
  the wheel does; two fingers zoom the page.
- **Copy.** ⌘C on a Mac, Ctrl+Shift+C elsewhere, copies the selection.
  The key never goes to the program, so that it does not interrupt one as
  Ctrl+C would. A right click shows the menu with Copy (greyed with no
  selection) and Paste (see Typing and paste). Copy on selection (Settings, Terminal; off by default)
  copies a selection when the button or finger lets go. The page writes
  with `navigator.clipboard.writeText`; where there is none, as on a LAN
  page over http, which is not a secure context, it copies a hidden text
  area with `document.execCommand("copy")` within the same click or key.
  If both fail the status line says so.
- **Clearing.** Typing a key that goes to the program clears the
  selection, as Ghostty's `selection-clear-on-typing`; so does the wheel
  while a program tracks the mouse. Ghostty clears it too when the text
  under it goes.
- **A program that tracks the mouse** (modes 9, 1000, 1002, 1003, in
  X10, UTF-8 1005, SGR 1006, urxvt 1015 or SGR pixels 1016, as the
  encoder reads them from the terminal): presses and releases of the left,
  middle and right buttons, with Ctrl, Alt and Meta, go to it, written as
  typed keys are. Motion goes too when the mode asks (1002 with a button
  held, 1003 always), once a cell, over the grid, or outside it while a
  button is held; a release always goes. Shift selects instead, and is not
  reported, as on iOS; the right button with Shift shows the menu. A tap
  of a finger is a left click; a long press still selects.
- **Focus.** A click into a pane that does not have the keyboard only
  gives it the keyboard: a program that tracks the mouse does not get it,
  as Ghostty's macOS app consumes the click that moves the focus between
  splits. A click that selects selects at once.
- **Wheel.** With Shift it always scrolls the scrollback. Else, to a
  program that tracks the mouse, a wheel button (4 up, 5 down) a row at
  the pointer; on the alternate screen with alternate scroll (1007, on by
  default) the arrow keys; else the scrollback.
- **Links.** Hold Ctrl (⌘ on a Mac) with the mouse over a link: the
  link's cells are underlined in its text's colour and the pointer is a
  hand; pressing or letting go of the key with the mouse still adds or
  takes away the underline. A click with the key held opens the link in a
  new tab (`window.open` with `noopener,noreferrer`). That press is the
  link's: it does not select, a program that tracks the mouse does not get
  it, and its moves and release go to no one. Ctrl+click (⌘-click) on
  anything else selects or goes to the program as before. As the desktop's
  terminal (MyGo's `links.go`, `view.go` `linkAt`) and GoRex's
  `find.go`, `web/src/links.ts` finds, in this order:
  - a hyperlink (OSC 8), read with `ghostty_grid_ref_hyperlink_uri`; its
    cells are the cells around the pointer with the same URI on the row.
    A hyperlink whose cells print its URL followed by prose a program took
    into it (a Markdown autolink, as `https://x.y/a。工单`) underlines and
    opens only the URL, also on the row it wraps onto. A press with the key
    over a hyperlink's cells with no link to open goes to no one either;
  - a URL printed as text (`http`, `https`, `ftp`, `file`, `mailto:`),
    ended at the first character that is not ASCII, with the sentence's
    punctuation and the brackets it did not open trimmed. A URL printed as
    text is found on one row only, as on the desktop;
  - a web address printed without its scheme, as
    `support.github.com/tickets`, found as the desktop finds a file path
    and opened over https, `:line:col` trimmed;
  - on a host whose welcome lists `gorex.download`, a file path printed in
    the pane (the ported `pathAt`): as the desktop's `isLink` in a host's
    pane, any path but a folder's (one ending in `/`), not checked on the
    host, so a missing one answers "No such file" when it is opened. A path
    written with backslashes (`C:\dir\x`) is not a link, as on the desktop.

  The page opens `http`, `https`, `ftp` and `mailto:` links in a new tab.
  On a host with `gorex.download`, a `file://` URL (a hyperlink, as
  `ls --hyperlink` prints, or printed as text) is a link to the host's
  file, as the desktop's `download` reads it: its host ignored, its path
  percent-decoded, and `file:///C:/dir/x` read as `C:/dir/x`; elsewhere it
  is not a link. Without `gorex.download` file paths are not links. No
  modifier on a touch screen: a finger does not open links.

  A click on a file link opens the host's file (`web/src/files.ts`, the
  design in docs/web-open-files.md): the page asks for a ticket with
  `com.superlogical.terminal.download_begin` (the path with `:line:col`
  trimmed), and a second click on the same path while one is under way
  does nothing. An image (`png`, `jpg`/`jpeg`, `gif`, `webp`, `avif`,
  `bmp`, `ico`, `svg`, by the type the gateway answers) of at most 50 MiB
  is fetched from `/api/download?ticket=…`; when its first bytes are that
  image's signature (for SVG, `<svg` after a byte order mark, white space
  and the XML prolog), it shows in the viewer from a blob of that image
  type only. Any other file, a larger image, or one whose bytes do not
  match is the browser's own download: the page navigates a hidden link to
  `/api/download?ticket=…` (a new ticket after a mismatch), which the
  gateway answers as an attachment, so no blob of it exists on the page.
  Errors show in the status line: "No such file", "A folder is not
  downloaded", "Not a regular file", "Permission denied", "Too large",
  "The host does not send files", else the gateway's message.

  The viewer (`web/src/viewer.ts`) is a dialog over the window, as the
  Settings dialog: a frosted panel over a backdrop that only dims. It shows
  the image fitted to the window at first (never above its actual size),
  its name, pixel size and file size, and lucide buttons: zoom out
  (`zoom-out`), zoom in (`zoom-in`), actual size (`scan`), download
  (`download`, a new ticket and the browser's download) and close (`x`).
  Ctrl+wheel and a trackpad's pinch, which arrives as Ctrl+wheel, zoom at
  the pointer, with a listener that is not passive so that the page does
  not zoom; a plain wheel and a drag pan an image larger than the window;
  two fingers zoom on a touch screen. Escape, a click outside the panel or
  a click beside the image closes it, and the keyboard returns to the
  pane. An image larger than 100 megapixels is not decoded; the viewer
  offers the download instead. The blob URL is revoked when the viewer
  closes.

  Differences from the desktop: the desktop downloads a host's file into
  the Downloads folder, resumes it and opens it with the system, and shows
  transfers in a list; the web client hands any file but an image it shows
  to the browser's download and shows only errors. The desktop tells a web address by the
  public suffix list (`golang.org/x/net/publicsuffix`); the page does not
  ship that list and takes a host name whose last label is a two-letter
  country code (every one the list has) or one of about a hundred common
  generic top-level domains, such as `com`, `org`, `net`, `dev` and
  `app`, and not a registry's own name such as `co.uk`. So an address under
  a rarer generic domain is not a link here, and one under a private
  suffix (`user.github.io/x`), which the desktop leaves out, is.
- Not in the web client: Shift+click to extend a selection, block
  (rectangle) selection, horizontal wheel buttons (6, 7), middle-click
  paste, scrolling on while a handle is dragged past an edge, Select All,
  and the loupe.

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
- The list (190 KB) loads on demand, when the Theme section first opens
  (Loading on demand), with a progress bar after 150 ms and Retry when
  it fails. The first frame has the chosen colours without it: the
  standard theme's are in the script (`__STANDARD_THEME__`, from the
  same file), and a chosen theme's are kept with its name
  (`themeColors` in `gorex.settings`). Only a choice kept without its
  colours, by an older version, loads the list as the page starts; if it
  does not load, the client keeps GoRex's colours.
- The minimum contrast (3 by default) applies to every theme.
- The pane and its padding take the terminal's background. When that is
  light (by the same luma), the page's own colours (sidebar, status, frame
  and picker) change to a light set (`data-scheme="light"` in
  `web/index.html`). This follows the terminal's background as drawn, so a
  host theme or a program's OSC 11 change it too. The Window setting
  (see Settings), Light or Dark, overrides it.
- The theme's selection colours colour the selection (see Drawing). Not
  in the web client: the theme's cursor text colour.

What differs: the desktop's panes are the window's glass, which a page
cannot show, so the pane here is the theme's background. There is no
scroll bar.

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
| Host (the card) | The host of the shown window, its address and its connection (connected, connecting, unreachable, with the reason), this page's origin, and the command that makes a host trust it. |
| Appearance | Window: System, Light or Dark for the page's colours; System follows the terminal's background, as the desktop's System follows the theme's colours. Tab placement: Above (until set), Left Side or Right Side; Sidebar view: Spaces or Hosts, shown while the tabs are in a sidebar (see Tab placement and the sidebar view). Show pane headers: off hides the headers only while the tabs are at the left, as the desktop's. Animation style: Snappy (until set), Natural or Off (see Split panes). Text size, 8 to 32 whole CSS pixels, 15 until set, with Reset. Language: System, English or 简体中文 (see Languages). |
| Terminal | Font: JetBrains Mono or Maple Mono NF CN, which the page brings, or a common monospaced family installed on the device (told by measuring text in it), with a preview in the theme's colours; CJK is Maple's in every font (see Fonts). Cursor style (Block, Bar, Underline) and Cursor blink. Copy on selection (off until set). Minimum contrast: the default (3), Off, 2, 3, 4.5 (WCAG AA) or 7 (WCAG AAA). Reset to Defaults forgets the font, cursor, copy on selection and contrast. |
| Theme | The theme picker (see Themes). |
| Keyboard | The shortcuts (see Shortcuts), with this system's keys. |
| Hosts | The hosts: choose one, add one, edit its address, name and icon, or remove it, with the same form as the host list's. |
| Tailscale | Built-in Tailscale on or off, its status (Not signed in, Signing in, Connected as the account on the tailnet, Expired, Tailnet Lock, Error), the account, tailnet and hosts found, Sign In (the guide) and Sign Out, the device name, and how long the sign-in lasts. |
| About | The client's commit and build date and the Ghostty commit of the wasm (`web/build.mjs` writes them in), and Reset All Settings, which forgets every `gorex.*` key of the page in this browser and reloads it. |

The cursor is set as the desktop sets it (`setCursor`): with the
sequence a program would send (DECSCUSR), into each pane's terminal when
it changes and after each snapshot, so that a program can still set its
own. Until a cursor is chosen, the pane keeps the host's.

Not in the web client: the desktop's Glass and Terminal opacity (a page
cannot show the desktop through it), the Dock badge, hiding the sidebar
and its width, the desktop's update settings (the
page updates through its Service Worker), the settings file (the page
keeps `localStorage`), and recording shortcuts.
