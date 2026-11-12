# Web client: built-in Tailscale

Status: phases 1 to 3 built, in the app's own origin and "This tab only"
mode (As built, below). Everything after the login URL is not run yet:
it needs a signed-in tailnet.

## Problem

The web client (docs/web-client.md) reaches a host by its gateway's
address: on the local network over plain HTTP, or on the tailnet at
`https://<machine>-gorex.<tailnet>.ts.net`. The second needs the
Tailscale app on the device that opens the page, signed in, with its
VPN on. On a borrowed computer, a work laptop with its own VPN, or a
tablet without the app, the page reaches nothing away from home.

Rex Go solved the same problem by joining the tailnet inside the app
(docs/ios-tailnet.md). A web page can do the same: Tailscale's own
`cmd/tsconnect` runs Tailscale compiled to WebAssembly in a browser,
with control over a WebSocket, DERP over WebSockets and a userspace
network stack, for SSH in the browser.

## Goal

The user opens the page as it is, signs in to Tailscale inside it, and
sees their GoRex machines listed. The page finds them itself in the
tailnet's network map, connects through its own in-page Tailscale node,
and then works exactly as it does over a direct connection. No
Tailscale app, no VPN and no address typed or carried in the URL.

## Non-goals

- **A login of GoRex's own.** As in Rex Go, the page's identity is its
  tailnet identity, checked by the gateway's `identify`.
- **Headscale, for now.** The control URL is a setting, but the gateway
  serves only HTTPS on the tailnet and Headscale issues no certificates
  (ios-tailnet.md, Non-goals). Whether Headscale takes the browser's
  control protocol over WebSocket is an open question.
- **A `?host=` link to a tailnet host.** Tailnet hosts come from the
  network map, never from the URL (Alternatives).
- **Direct UDP.** A browser has no UDP sockets, so the page's node
  reaches every peer through a DERP relay, over a WebSocket. It never
  connects directly.
- **Changing the gateway's rules.** Who gets in stays `identify`'s
  decision (Gateway).
- **Shipping it with the desktop.** The wasm is part of the hosted page
  only. Desktop GoRex neither builds nor serves it.

## Design

### Engine

A Go package of its own, `web/tailnet`, in GoRex's module (no `go.mod`
of its own), as `ios/tailnet` is. It uses the `tailscale.com` that
`go.mod` pins, builds only for `GOOS=js GOARCH=wasm`, and no package of
the desktop imports it.

It starts from `cmd/tsconnect/wasm/wasm_js.go` (BSD-3-Clause), which the
spike built unchanged: `ipnlocal.LocalBackend`, `wgengine` with
`netstack`, a `tsdial.Dialer` that sends every address through the
netstack. It drops SSH and `fetch`, and adds:

| Call | Does |
| --- | --- |
| `start(config, state, events)` | starts the node: device name, control URL, persistent or ephemeral, the state read beforehand (State) |
| `login()` / `logout()` | interactive login; the login URL arrives as an event |
| `hosts()` | the GoRex gateways in the network map (Finding hosts) |
| `request(host, method, path, headers, body)` | HTTPS to a gateway; answers status, headers and the body as a stream |
| `socket(host, path, protocol)` | a WebSocket to a gateway; messages both ways, close codes |
| `stop()` | stops the node |

Events: the backend state (`NeedsLogin`, `NeedsMachineAuth`, `Starting`,
`Running`), the login URL, the user signed in, the hosts as they change,
and state writes to persist.

Each connection is TCP over the netstack to the gateway's MagicDNS name
on port 443, TLS in Go, then HTTP/1.1. Three facts shape this:

- **TLS roots.** `js/wasm` has no system roots: `x509.SystemCertPool`
  is empty there. The engine verifies with the LetsEncrypt roots of
  `tailscale.com/net/bakedroots`, which cover the gateway's `*.ts.net`
  certificate. tsconnect's build tags omit `bakedroots`, which only
  turns off `tlsdial`'s fallback; the package itself still links, and
  the engine calls `bakedroots.Get()` directly. A name that does not
  match is refused as anywhere.
- **HTTP.** In `js/wasm`, `net/http` sends a request through the
  browser's `fetch` unless the transport has a dialer of its own
  (`net/http/roundtrip_js.go`). The engine's transport sets
  `DialContext` to the netstack dialer, so no request leaves through
  the browser.
- **WebSocket.** `github.com/coder/websocket` has no dialer under
  `js` (`dial.go` is `//go:build !js`): it wraps the browser's
  WebSocket, which cannot use the netstack. The engine speaks the
  WebSocket protocol over the TLS connection with
  `golang.org/x/net/websocket` (`NewConfig`, `NewClient`), already in
  `go.mod`.

Log upload is off. tsconnect sends its logs to `log.tailscale.com`; the
engine logs to the console only, and the page's CSP blocks the host
anyway (Security).

### Where it runs

The engine runs in a dedicated Worker, not on the page's thread:
WireGuard, the netstack and TLS would otherwise compete with the
terminal's drawing. The Worker lives in a page of a separate origin,
the **node frame** (Security), which the app embeds as a hidden
`<iframe>` only once the user turns Tailscale on. The app talks to the
frame over a `MessageChannel`.

The wasm is loaded only then. A page that never uses Tailscale fetches
none of it.

### Build and size

`web/build.mjs` gets a step that runs
`GOEXPERIMENT=nojsonv2 go build -trimpath -ldflags "-s -w" -tags <tags>`
with `GOOS=js GOARCH=wasm` on `web/tailnet`, copies Go's
`lib/wasm/wasm_exec.js` of the same toolchain, and gzips the result. It
does not run `wasm-opt`, as tsconnect does: it saves 9% raw, 1.5% gzipped
and nothing with brotli (table), and Binaryen's npm build took over four
minutes. The size work is in three places.

The tags are tsconnect's own list,
`tailscale.com/cmd/tsconnect/wasmbuild.Tags()`, read by a small
`go run` so a `tailscale.com` bump keeps them current, plus five more
`ts_omit_` tags that the web node does not need: `c2n`, `dns`,
`health`, `ipnbus` and `logtail`. `go run tailscale.com/cmd/featuretags
--list` shows all features of the pinned version; `--min --add
netstack,bakedroots` gives exactly tsconnect's list plus these five. The
web node needs no SSH, Taildrop, exit node, subnet routing, serve,
funnel, portmapper, captive portal, drive, appconnectors, relay server,
peerapi, usermetrics, clientmetrics, syspolicy or debug endpoint, and
tsconnect's list already omits all of them. It keeps `netstack`
(userspace TCP to a peer) and `bakedroots` (TLS without OS roots).
`tailnetlock` stays omitted, as in tsconnect: dropping it may break
tailnets that use Tailnet Lock (see Open questions).

The engine's `main` does not import `ipnserver` (it pulls in
`localapi` and `os/exec`), `logpolicy` or `logtail`. It calls
`NewLocalBackend` directly with a zero log ID and `log.Printf`, and
starts no LocalAPI listener. The node then keeps no `log-policy` key and
uploads no logs.

The build sets `GOEXPERIMENT=nojsonv2`. Go 1.27 builds `encoding/json`
on `encoding/json/v2` by default; with the experiment off, std keeps its
older implementation and `encoding/json/v2` and `jsontext` (640 KB of
code) drop out, because `tailscale.com` uses its own copy
(`go-json-experiment`). It is a toolchain experiment that a later Go may
remove; if it goes, the build grows by 0.9 MB and exceeds the limit
below again, so the step still gzips.

Measured with the pinned `tailscale.com` and Go 1.27.3:

| Build | Raw | gzip -9 | brotli 11 |
| --- | --- | --- | --- |
| tsconnect's `wasm` package, unchanged | 29.81 MB | 6.88 MB | 4.86 MB |
| the spike's engine (no SSH, with `x/net/websocket` and the roots), tsconnect's tags | 29.06 MB | 6.72 MB | 4.75 MB |
| the same, with the five more tags | 27.60 MB | 6.39 MB | 4.51 MB |
| the lean engine (no `ipnserver`, `logpolicy`, `logtail`), same tags | 27.05 MB | 6.28 MB | 4.41 MB |
| the same, with `GOEXPERIMENT=nojsonv2` (the choice) | 26.12 MB (26,121,454 bytes) | 6.08 MB | 4.28 MB |
| the choice, after `wasm-opt -Oz` (Binaryen 123) | 23.61 MB | 5.99 MB | 4.33 MB |

Each step, by raw size. The five tags added to tsconnect's list one at a
time (spike engine): `logtail` -0.69 MB (the spike's `main` imports
`logtail`, so only part of it goes without the `main` change), `dns`
-0.58 MB, `health` -0.20 MB, `c2n` -11 KB, `ipnbus` -7 KB. Put back one
at a time into the set of all five, each adds only `health` 190 KB,
`c2n` 9 KB, `ipnbus` 8 KB, `dns` 5 KB, because the tags overlap: they
are worth their sum, not each alone. The `main` change takes 0.55 MB,
`nojsonv2` 0.93 MB. The whole trim is 2.9 MB raw, 0.64 MB gzipped and
0.47 MB brotlied.

Rejected:

- `nethttpomithttp2` (a Go tag) saves another 1.2 MB, but the control
  client speaks HTTP/2 inside the Noise connection
  (`ts2021/client.go`: `SetUnencryptedHTTP2`). Tried in the browser:
  `NeedsLogin`, then the `ts2021` WebSocket reconnects in a loop and no
  login URL comes.
- The trimmed build was run only to the login URL. Its peers path
  (netmap, DERP, dial) is not run yet (Spike, questions 5 to 7), so
  `dns`, `health` and `ipnbus` stay on trial until then.

Largest packages by code size (function bytes from the name section of an
unstripped build: 16.9 MB of code before, 15.0 MB after; the 11 MB data
section, mostly type and function tables, is not in it):

| Package | Before | After | Why it stays |
| --- | --- | --- | --- |
| `runtime` | 1197 KB | 1192 KB | Go |
| `net/http` | 725 KB | 712 KB | control and DERP clients |
| `crypto/tls` | 649 KB | 649 KB | TLS to a peer's port 443 |
| `ipn/ipnlocal` | 604 KB | 519 KB | the backend |
| `net/http/internal/http2` | 526 KB | 526 KB | control speaks h2 over Noise (above) |
| `gvisor tcpip/stack` | 500 KB | 500 KB | netstack |
| `wgengine/magicsock` | 456 KB | 452 KB | DERP and WireGuard paths |
| `encoding/json/v2` | 445 KB | gone | `nojsonv2` |
| `net` | 426 KB | 401 KB | |
| `klauspost/compress/zstd` | 426 KB | 149 KB | map responses (below) |
| `gvisor tcpip/transport/tcp` | 395 KB | 395 KB | netstack |
| `gvisor tcpip/network/ipv6` | 300 KB | 300 KB | below |
| `crypto/x509` | 275 KB | 273 KB | |
| `control/controlclient` | 249 KB | 247 KB | |
| `encoding/json` (older std) | | 239 KB | replaces v2 |

Still large and not needed, but they need a change in `tailscale.com`
itself, which the build does not patch:

- `gvisor` `ipv6`, `udp`, `icmp`, `raw` (about 430 KB): `netstack.Create`
  registers every protocol (`main` <- `wgengine/netstack`).
- `regexp` and `regexp/syntax` (about 150 KB): `main` <-
  `wgengine/netstack` <- `gvisor/pkg/refs` <- `gvisor/pkg/log`.
- `klauspost/compress/zstd`, `huff0` (about 215 KB): `main` <-
  `control/controlclient` <- `util/zstdframe`, for map responses that
  control compresses with zstd.
- `net/dns`, `net/dns/resolver`, `net/netcheck`, `net/routemanager`
  (about 150 KB): `ipnlocal` imports them, and no tag cuts them.

Cloudflare Workers serves a static asset of at most 25 MiB (26,214,400
bytes). The choice (26,121,454 bytes) is under that limit by 92,946
bytes, 0.35%: a `tailscale.com` bump may put it over, and the `wasm-opt`
output (23.61 MB) is the way back under. The build still ships the wasm
compressed, as `tailnet-<hash>.wasm.gz` (about 6.1 MB), and the frame
unpacks it with `DecompressionStream("gzip")` into
`WebAssembly.instantiateStreaming`. Cloudflare already serves
`application/wasm` with brotli (`ghostty-vt.wasm` arrives
`Content-Encoding: br`), but that does not lift the limit on the stored
file. The budget: at most 25 MiB stored, at most 7 MB over the network.

The name carries the content's hash and `_headers` keeps it a year
(`immutable`), unlike the page's other files, which revalidate on every
load: a returning user downloads it once per version.

The choice, and its `wasm-opt` output, each reached `NeedsLogin` and a
`https://login.tailscale.com/a/…` URL in ego lite from
`http://127.0.0.1:<port>` (about 1 s after `login()`), with `bakedroots`
verifying the `login.tailscale.com` chain.

### State

The node's state (`_machinekey`, the profile with the node key, the
log policy) is a few small keys. tsconnect keeps them in
`sessionStorage`, so its node is new in every tab.

The node frame keeps them in IndexedDB. The engine's `ipn.StateStore`
is synchronous and IndexedDB is not, so the frame reads every key
before `start` and the engine serves reads from memory; each write is
an event that the frame stores.

Two modes, in Settings:

- **Stay signed in** (default): a persistent node
  (`controlclient.LoginDefault`). It lasts until its key expires (the
  tailnet's key expiry, 180 days by default) or the user signs out.
- **This tab only**: an ephemeral node, as tsconnect's
  (`LoginEphemeral`), its state in memory. Tailscale removes the device
  after it goes offline. Each new tab signs in again.

The state is not encrypted. Any key the browser could use to encrypt
it would be usable by the same code that could read it, so encryption
would not stop the threat that matters (Security).

**Device name**: `gorex-web-` and four random letters, such as
`gorex-web-kqzt`, shown in the admin console with OS `js`. The user can
change it in Settings, checked by `internal/tailnode`'s rule.

**Tabs.** Two tabs running the same node key confuse the control
server. The frame takes a Web Lock (`navigator.locks`) named for the
node. A second tab shows "Tailscale is in use in another tab" and a
button that takes it over: the first tab, told over a
`BroadcastChannel`, stops its node and drops its tailnet hosts.

**Sign out** calls `logout()` (the node key expires at control) and
deletes the stored state. **Forget this browser** in the admin console
works the same from the other side: the next start shows `NeedsLogin`.

### Signing in

Settings gets a Tailscale section, after Hosts, as Rex Go's:

- **Built-in Tailscale** on or off.
- **Control server**: empty for Tailscale's, or a URL, checked by
  `internal/tailnode`'s `ValidControlURL` rule, ported to TypeScript.
- **Device name** and **Stay signed in**, as above.
- **Status**: the state, the user signed in, this device's name and
  address, and each GoRex host and whether it answers.
- **Sign in** / **Sign out**.

A browser blocks `window.open` that no click started, and the login URL
arrives about a second after the node asks for it. So **Sign in**
opens a blank tab within its click, then sets that tab's location
when the URL arrives. When that tab is blocked or closed, the section
shows the URL as a link with `target="_blank" rel="noopener
noreferrer"`. The user signs in with the tailnet's identity provider in
that tab. The page needs no redirect back: its node moves to
`Running` when control accepts the login, and the section says so. A
tailnet that approves devices by hand shows `NeedsMachineAuth`, and
the section says that an admin must approve `gorex-web-kqzt`.

The Hosts dialog shows a **Sign in to Tailscale** row while
Tailscale is on and not running, which opens the same section.

### Finding hosts

The node's network map lists every peer it may see. The page shows a
peer as a GoRex host when all of these hold:

1. **Same user**: the peer's user is the signed-in user, and the peer
   is untagged. That is what `identify` lets in without
   `--allow-tailnet-members` (Gateway). With that flag the gateway lets
   in more, but the page cannot know it, so a later version may list
   tagged peers on request.
2. **Looks like a gateway**: its `Hostinfo.App` is `gorex` (a small
   gateway change, below), or, for a gateway without it, its host name
   ends in `-gorex`, the default name (`internal/rexapi/tailnet.go`
   `defaultHostname`). The name alone is weak: the user can give the
   device any valid name (`gorex rexapi tailscale rename NAME`, or
   `on --hostname NAME`).
3. **Answers as one**: the engine opens HTTPS to the peer's MagicDNS
   name on port 443 and asks for `GET /api/help`. A gateway that lets
   the node in answers 200 with its API's help document; one that does
   not answers 403 with a JSON `{"error": …}` (`writeRefusal`), which
   the page lists as a host that refuses this browser, with the
   message. A peer that times out (3 s) or answers otherwise is not
   listed. The check runs once per peer per network map, for online
   peers only, never more than four at once.

Peers that pass 1 and 3 but not 2 are not probed by default: probing
every peer of a large tailnet on port 443 is noise in their logs.
Settings has **Look for GoRex on all my devices**, which probes them
once.

tsconnect reports the network map only when the node itself changes
(its notify callback's `SelfChange`); peers no longer ride on the IPN
bus. The engine watches the bus with `NotifyPeerChanges` and rebuilds
the host list on every peer change, so a gateway that comes online
appears without a reload.

### Hosts and transport

`Host` (`web/src/hosts.ts`) gets `kind: "direct" | "tailnet"`. A
direct host is today's: a URL the user added, saved in
`localStorage`. A tailnet host is not saved: it comes from the network
map, by its stable node ID, with its MagicDNS name as its address. Its
ID is `ts:<StableNodeID>`, so the chosen host survives a reload; a
user's own name and icon for it are saved under that ID. The Hosts
dialog lists tailnet hosts in a group of their own, below the direct
hosts, with a state dot (online, offline, refusing).

`web/src/rex.ts` gets a transport seam. Today, `setHost` sets a base
URL, and `Control`, `DataStream` and `files.ts` call `new WebSocket`
and `fetch` on URLs built from it. After:

```ts
interface Transport {
  socket(path: string, protocol: string): SocketLike;
  fetch(path: string, init?: RequestInit): Promise<Response>;
}
```

- `directTransport(url)` is today's code, unchanged.
- `tailnetTransport(host)` sends both through the node frame.
  `SocketLike` is the part of `WebSocket` that `Control` and
  `DataStream` use (`binaryType`, `protocol`, `send`, `close`,
  `onopen`, `onmessage`, `onclose`), so they do not change. `fetch`
  answers a real `Response` built on a `ReadableStream` that the frame
  fills, so `fetchImage` reads it as it reads the browser's own.

`setHost(url)` becomes `setHost(host)`, which picks the transport.

**Downloads.** Today a file that is not shown downloads through a
hidden `<iframe>` navigated to `/api/download?ticket=…`, and the
browser saves it as it arrives (docs/web-open-files.md). The browser
cannot navigate to a tailnet address. So over the tailnet, the page
fetches the file through the transport into a `Blob` and saves it with
an `<a download>`. That holds the whole file in memory, so it is capped
at 256 MiB (`too_large` above that, with the cap in the message). With
the page's Service Worker controlling it, the file streams instead, with
no cap: the worker answers a same-origin URL, `save/<id>`, from the
transport's stream, chunk by chunk (docs/web-client.md, Service Worker).

This puts the host's bytes on the page's origin as a blob, which the
direct path avoids. The blob is never shown and never navigated to;
it is saved and its URL revoked.

### Gateway

Nothing changes for the page to work:

- **Who gets in**: `identify` (`internal/rexapi/tailnet.go`). The
  browser node is an untagged device of the user who signed it in.
  When that is the user who signed the gateway in, it gets in, as Rex
  Go does. A tagged browser node needs `--allow-tailnet-members` and an
  access rule.
- **Origin**: the engine sends `Origin` with every request, set to
  the app's own origin as the frame received it from the app's
  `postMessage` (`event.origin`), never a made-up one. The gateway
  checks it as for the browser (`checkOrigin`): the hosted page must be
  allowed with `gorex rexapi web allow https://gorex-web.molty.workers.dev`,
  as today.
- **Host**: the engine sends the peer's MagicDNS name as `Host` and as
  the TLS server name. `hostAllowed` (`host.go`) takes built-in
  Tailscale's MagicDNS name and its first label, so it passes.
- **CORS** does not apply: the engine is not a browser, and reads every
  answer.

One small change helps find hosts: the gateway sets
`hostinfo.SetApp("gorex")` before its node starts, so peers see
`Hostinfo.App`. It is process-wide; the gateway is the only
`tailscale.com` node of its process. Whether control passes `App` on to
peers is not verified (Open questions); the name rule covers older
gateways either way.

## Security

### What the node is worth

The node is a full device of the user's tailnet. Unless the tailnet's
access rules say otherwise, it reaches everything the user's laptop
reaches: SSH servers, NAS shares, admin pages. Its keys sit in browser
storage.

So a script that runs in the origin holding the node (an XSS) can:

- use the node while the page is open, to reach any of the user's
  devices;
- copy the machine and node keys and run the device elsewhere, until
  its key expires or someone removes it.

Encrypting the stored state does not change this: the code that
decrypts it runs in the same origin.

### Which origin holds the node

The node lives in its own origin, the node frame, not in the app's.
The app is large and renders untrusted text (terminal output, links,
file names, images); the frame is small and renders nothing.

- The frame is served from a second site of its own (a separate
  `*.workers.dev` name, which the Public Suffix List makes a different
  site). Its storage is apart from the app's; a script in the app
  cannot read the keys.
- The frame answers `postMessage` only from the app's origin, which
  its `frame-ancestors` also names, and offers only the calls above.
  `request` and `socket` take a host from the frame's own GoRex host
  list and always port 443. A script in the app can therefore reach
  GoRex gateways, which it could already reach as the app, and nothing
  else of the tailnet.
- Browsers partition a third-party frame's storage by the top-level
  site. The frame is only ever embedded by the app, so its state is
  stable under that key.

A first version may run the node in the app's own origin, behind the
same narrow API, to ship sooner. It must not keep keys longer than the
tab (`This tab only`) until the frame exists.

### CSP

The node frame's policy:

```
default-src 'none'; script-src 'self' 'wasm-unsafe-eval';
connect-src 'self' https://controlplane.tailscale.com wss://controlplane.tailscale.com wss://*.tailscale.com;
worker-src 'self'; frame-ancestors https://gorex-web.molty.workers.dev;
base-uri 'none'; form-action 'none'
```

`wss://*.tailscale.com` covers Tailscale's DERP servers
(`derpN<x>.tailscale.com`). A control URL or DERP map of the user's own
needs its hosts added; the frame's policy is per deployment.

The app's policy gains only `frame-src` for the frame's origin, which
`frame-src http: https:` already allows; it tightens to that origin
plus what downloads need. Its `connect-src` stays as it is, as direct
hosts need any `http:` and `ws:` address.

`window.open` and links are not under CSP, and the app sets no
`Cross-Origin-Opener-Policy`; a login tab opens (Spike).

### Access rules

Two setups, documented in Settings' help:

- **Untagged** (default, works with no tailnet changes): the browser
  node is the user's device and has the user's reach. Keep **This tab
  only** on shared computers.
- **Tagged**: the tailnet's policy defines `tag:gorex-web`, owned by
  the user; the frame advertises it at login; one grant lets
  `tag:gorex-web` reach the gateway on port 443 and nothing else; the
  gateway runs with `--allow-tailnet-members`. The node then reaches
  only GoRex, even if its keys leak. Tagged devices have no key expiry
  by default, so the tailnet should set one for the tag.

### Revocation

Sign out in the page, or remove the device in the admin console. Both
end the node at control at once. An ephemeral node also goes away by
itself once offline.

## Alternatives

- **The Tailscale app on the viewing device**, with the page opened as
  `?host=https://<machine>-gorex.<tailnet>.ts.net`. It works today, with
  no new code. The user rejected it for this feature: it needs the app
  and its VPN on the device, which is the problem.
- **Tailscale Funnel.** It puts the gateway on the public internet,
  where `identify` cannot tell who calls (a Funnel request comes from
  Tailscale's ingress, not from a tailnet device). It would need a
  login of GoRex's own. Rejected.
- **`@tailscale/connect`**, tsconnect's npm package. It pins its own
  Tailscale, carries an SSH client, and its `fetch` answers only text.
  It has no WebSocket and no stream. The engine reuses its code, not its
  package.
- **A relay of ours** (a Worker that joins the tailnet and proxies the
  page). It would hold every user's tailnet credentials on a server.
  Rejected.
- **tailcat in the browser.** GoRex already runs tailcat
  (`internal/rexapi/tailcat.go`, docs/ios-tailcat.md): Tailscale's data
  plane with keys from a pairing, no control server and no login. A
  browser tailcat client would share most of this work:
  - the wasm build step, its size budget, lazy loading and hashed,
    compressed asset;
  - the engine's core: `wgengine` with `netstack`, DERP over WebSocket
    (a browser has no UDP, so a tailcat relay must accept WebSockets
    too), and HTTP and WebSockets over a netstack connection (plain
    HTTP inside WireGuard for tailcat, TLS for the tailnet);
  - the transport seam in `rex.ts`, `SocketLike` and the streamed
    `Response`, and the blob download;
  - the node frame, its storage of keys and its tab lock.

  It would differ in identity (a client key and pairing, instead of
  OAuth and a network map) and in finding hosts (the address from a
  QR code or link). The seam is designed so that a third transport
  plugs in beside the two.

## Spike

Run on Windows, Go 1.27.3, `tailscale.com` at the version `go.mod`
pins, in a scratch module that copied `go.mod` and `go.sum`; Chrome 156
through ego lite. Nothing was signed in.

| # | Question | Result |
| --- | --- | --- |
| 1 | Builds with the pinned `tailscale.com`? | Verified. `GOOS=js GOARCH=wasm go build -tags <wasmbuild.Tags()> -trimpath -ldflags "-s -w" tailscale.com/cmd/tsconnect/wasm`: 29.81 MB raw, 6.88 MB gzip, 4.86 MB brotli. The spike's own engine: 29.06 / 6.72 / 4.75 MB; trimmed (Build and size): 26.12 / 6.08 / 4.28 MB. |
| 2 | Reaches a login URL from a page? | Verified, from `http://127.0.0.1:<port>` under the hosted page's CSP: `NoState`, `NeedsLogin` 60 ms after start; after `login()`: `GET https://controlplane.tailscale.com/key?v=147` (200, `Access-Control-Allow-Origin: *`), WebSocket `wss://controlplane.tailscale.com/ts2021?X-Tailscale-Handshake=…` with protocol `tailscale-control-protocol` (open), and a `https://login.tailscale.com/a/…` URL 1.3–2.3 s after `NeedsLogin`. Also `POST https://log.tailscale.com/c/tailnode.log.tailscale.io/…` (no-cors, logs). No DERP before login. |
| 2b | Under a tight CSP? | Verified: with `connect-src 'self' https://controlplane.tailscale.com wss://controlplane.tailscale.com wss://*.tailscale.com`, the same login URL; the log upload was blocked by CSP without effect on login. |
| 3 | Does control or DERP check `Origin`? | Verified: neither does. For no `Origin`, `https://gorex-web.molty.workers.dev`, `http://127.0.0.1:…` and `https://evil.example`: control answered 101 and completed the Noise handshake (protocol 147); `derp20b` and `derp4f.tailscale.com` answered 101 with protocol `derp` and sent their server key frame. `/key` answers `Access-Control-Allow-Origin: *`. |
| 4 | Can the login URL open? | Verified: `window.open` with no click returns `null` (blocked); within a click it opens a tab and its location can be set later. No CSP violation. |
| 5a | TLS to `*.ts.net` in wasm? | Verified in the browser's wasm: `x509.SystemCertPool` has 0 roots and verification with it fails (`certificate signed by unknown authority`); with `bakedroots.Get()` the LetsEncrypt chains of `login.tailscale.com` and `letsencrypt.org` (Let's Encrypt YE1/YE2, Root YE) verify, and a wrong name is refused. The gateway's own certificate was not tried. |
| 5b | HTTP and WebSocket over the netstack? | Not verified (needs a signed-in node). The engine's `request` and `socket` build for `js/wasm`. Read in code: `net/http` skips `fetch` when `DialContext` is set; `coder/websocket` cannot dial under `js`. |
| 5c | Gateway checks for a wasm caller? | Not verified at run time; read in code (Gateway). |
| 6 | Peers from the network map? | Not verified (needs a signed-in node). The spike's `peers()` reads `NetMapWithPeers()`: name, `Hostinfo.Hostname`, `Hostinfo.App`, OS, user, tags, online, capabilities. tsconnect's callback reports it only on `SelfChange`. |
| 7 | Recognizing a gateway? | Not verified. Read in code: the default name is `<machine>-gorex` but any name can be set; `tsnet` sets only `Hostinfo.Package = "tsnet"`; the gateway could set `App`. |
| 8 | Fits Cloudflare? | Read in Cloudflare's limits: 25 MiB (26,214,400 bytes) per asset. The spike's builds do not, not even after `wasm-opt -Oz` (26,525,079 bytes); the trimmed build (Build and size) does, by 93 KB (26,121,454 bytes), and gzip (6.1 MB) does with room. |

## Open questions

- Whether Tailscale's control passes a peer's `Hostinfo.App` to other
  peers. If not, the gateway needs another mark: a name rule only, or
  a node attribute from the tailnet's policy.
- Whether Headscale accepts the control protocol over WebSocket
  (`/ts2021` with the handshake in the query) and answers `/key` with
  CORS.
- Tailnet Lock: tsconnect's build omits `tailnetlock`, and reports a
  locked-out node. A locked tailnet must sign the browser node; whether
  that works without the feature linked is not known.
- Safari's 7-day cap on script-written storage, for a partitioned
  frame's IndexedDB: whether **Stay signed in** lasts on Safari.
- Throughput and latency through DERP only, for a busy terminal; and
  the cost of the engine on a phone's battery.
- Whether the Go engine runs in a Worker as it runs on the page
  (tsconnect runs it on the page).

## As built

Phases 1 to 3, with these differences from the design above:

- **Origin and mode.** The node runs in the app's own origin, as the
  first version Security allows, so it is only **This tab only**: an
  ephemeral node (`LoginEphemeral`) with its state in memory
  (`mem.Store`). Reloading or closing the tab ends it. There is no
  **Stay signed in**, no IndexedDB, no tab lock and no node frame
  (phase 4).
- **Thread.** The engine runs on the page's thread, as tsconnect does,
  not in a Worker; its events reach the page through `queueMicrotask`, so
  no page code runs inside a call from Go.
- **Engine.** `web/tailnet` is `package main` with `//go:build js &&
  wasm` in GoRex's module; `go.mod` did not change (`go mod tidy -diff`
  shows only line endings). It sets one global, `gorexTailnet(config,
  emit)`, whose calls are `start`, `login`, `logout`, `stop`, `rename`,
  `netmap`, `request` and `socket`. `hosts()` is the page's
  (`web/src/tailnet.ts`), from `netmap()`.
- **WebSocket.** Not `golang.org/x/net/websocket`, which drops the close
  code (the page needs 4000, a closed block) and does not join fragments:
  `web/tailnet/wsclient`, a small RFC 6455 client, tested against
  `coder/websocket` with the desktop's tests.
- **Hosts.** Only peers that answered `/api/help` are listed, and stay
  listed as offline when they go offline. Peers named `*-gorex` that did
  not answer are named in the guide's empty state. A refusing host is
  asked again every 15 seconds, one that did not answer every minute. No
  **Look for GoRex on all my devices** yet. Besides the network map's
  events, the page reads the map every 15 seconds while running. The
  gateway's `hostinfo.SetApp("gorex")` (phase 5) is not done.
- **Refusals.** The guide maps the gateway's 403 messages: "does not
  trust web pages from" (run `gorex rexapi web allow <origin>`), "lets in
  only the Tailscale user who signed it in" (sign in with that account,
  or `gorex rexapi tailscale on --allow-tailnet-members`), and "built-in
  Tailscale is off".
- **Settings.** No control server field: the page's CSP allows only
  Tailscale's. Device name is kept in `gorex.settings`
  (`tailscaleName`), random per tab until set.
- **Build.** Everything the page loads by name is in `dist/assets`
  under a content hash and kept a year (docs/web-client.md, Cloudflare
  Workers); `tailnet.json` names the engine and is revalidated. The
  engine compiles from bytes (`WebAssembly.instantiate`), after
  `DecompressionStream("gzip")`: a `.gz` is not served as
  `application/wasm`, so `instantiateStreaming` does not apply. The page
  also takes a module that arrives already unpacked.
- **Sign-in tab.** Opened blank within the click, its `opener` set to
  null, then sent to the login URL when it arrives. The guide's Cancel
  closes it.

## Phases

1. **Engine**: `web/tailnet`, the build step in `build.mjs`, the
   compressed hashed asset, and a dev page that signs in and lists
   hosts. Settles questions 5b, 6 and 7 of the spike against a real
   gateway.
2. **Transport**: the seam in `rex.ts`, `tailnetTransport`, a terminal
   opened over the tailnet, and downloads through a blob.
3. **Settings and hosts**: the Tailscale section, sign-in, the tailnet
   host group, the tab lock, both state modes.
4. **Node frame**: the separate origin, its CSP, the narrow API; until
   then only **This tab only**.
5. **Gateway**: `hostinfo.SetApp("gorex")`, once phase 1 shows that
   peers see it.
