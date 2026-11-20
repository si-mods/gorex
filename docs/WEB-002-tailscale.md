---
name: WEB-002
description: "The web client runs Tailscale in the page with WebAssembly, so it needs no Tailscale app. Use when you change the web tailnet engine."
metadata:
  created: "2026-11-12"
  updated: "2026-11-20"
---

# WEB-002: Web client: built-in Tailscale

Status: phases 1 to 3 built (#410, #546, #547). Phases 4 and 5 not built.

Phases 1 to 3 run in the origin of the page. Phase 4 (the node frame) and phase 5 (`hostinfo.SetApp`) are not in the code. See "As built" for the differences from this design. Everything after the login URL is not run yet, because it needs a signed-in tailnet.

## Problem

The web client (`docs/REF-007-web-client.md`) reaches a host by the address of its gateway. On the local network it uses plain HTTP. On the tailnet it uses `https://<machine>-gorex.<tailnet>.ts.net`. The second way needs the Tailscale app on the device that opens the page. The app must be signed in, with its VPN on. On a borrowed computer, a work laptop with its own VPN, or a tablet without the app, the page reaches nothing away from home.

Rex Go solved the same problem. It joins the tailnet inside the app (`docs/IOS-001-tailnet.md`). A web page can do the same. The Tailscale tool `cmd/tsconnect` runs Tailscale compiled to WebAssembly in a browser. It uses control over a WebSocket, DERP over WebSockets and a userspace network stack, for SSH in the browser.

## Goal

The user opens the page as it is and signs in to Tailscale inside the page. The page lists the GoRex machines of the user. The page finds them itself in the network map of the tailnet. It connects through its own in-page Tailscale node. Then it works exactly as it does over a direct connection. The user needs no Tailscale app, no VPN, and no address typed or carried in the URL.

## Non-goals

- **A login of GoRex's own.** As in Rex Go, the identity of the page is its tailnet identity. The `identify` function of the gateway checks it.
- **Headscale, for now.** The control URL is a setting. But the gateway serves only HTTPS on the tailnet, and Headscale issues no certificates (`IOS-001-tailnet.md`, Non-goals). It is an open question if Headscale takes the browser control protocol over WebSocket.
- **A `?host=` link to a tailnet host.** Tailnet hosts come from the network map. They never come from the URL (see Alternatives).
- **Direct UDP.** A browser has no UDP sockets. The node of the page therefore reaches every peer through a DERP relay, over a WebSocket. It never connects directly.
- **A change to the rules of the gateway.** `identify` decides who gets in (see Gateway).
- **Shipping it with the desktop.** The wasm is part of the hosted page only. Desktop GoRex neither builds nor serves it.

## Design

### Engine

The engine is a Go package of its own, `web/tailnet`. It is in the module of GoRex and has no `go.mod` of its own, as `ios/tailnet` has none. It uses the `tailscale.com` version that `go.mod` pins. It builds only for `GOOS=js GOARCH=wasm`. No package of the desktop imports it.

It starts from `cmd/tsconnect/wasm/wasm_js.go` (BSD-3-Clause), which the spike built unchanged. That file has `ipnlocal.LocalBackend`, `wgengine` with `netstack`, and a `tsdial.Dialer` that sends every address through the netstack. The engine drops SSH and `fetch`, and adds these calls:

| Call | Does |
| --- | --- |
| `start(config, state, events)` | starts the node: device name, control URL, persistent or ephemeral, and the state that the page read beforehand (see State) |
| `login()` / `logout()` | interactive login, and the login URL arrives as an event |
| `hosts()` | the GoRex gateways in the network map (see Finding hosts) |
| `request(host, method, path, headers, body)` | HTTPS to a gateway, and the answer has status, headers and the body as a stream |
| `socket(host, path, protocol)` | a WebSocket to a gateway, with messages both ways and close codes |
| `stop()` | stops the node |

The engine sends these events:

- the backend state (`NeedsLogin`, `NeedsMachineAuth`, `Starting`, `Running`)
- the login URL
- the user signed in
- the hosts as they change
- the state writes to persist

Each connection is TCP over the netstack to the MagicDNS name of the gateway on port 443. TLS runs in Go, and then HTTP/1.1. Three facts shape this design:

- **TLS roots.** `js/wasm` has no system roots, and `x509.SystemCertPool` is empty there. The engine verifies with the LetsEncrypt roots of `tailscale.com/net/bakedroots`. They cover the `*.ts.net` certificate of the gateway. The build tags of tsconnect omit `bakedroots`. This only turns off the fallback of `tlsdial`. The package itself still links, and the engine calls `bakedroots.Get()` directly. As anywhere, the engine refuses a name that does not match.
- **HTTP.** In `js/wasm`, `net/http` sends a request through the `fetch` of the browser, unless the transport has its own dialer (`net/http/roundtrip_js.go`). The transport of the engine sets `DialContext` to the netstack dialer. No request leaves through the browser.
- **WebSocket.** `github.com/coder/websocket` has no dialer under `js` (`dial.go` is `//go:build !js`). It wraps the WebSocket of the browser, which cannot use the netstack. The engine speaks the WebSocket protocol over the TLS connection with `golang.org/x/net/websocket` (`NewConfig`, `NewClient`). This package is already in `go.mod`. See "As built" for the client that replaced it.

Log upload is off. tsconnect sends its logs to `log.tailscale.com`. The engine logs to the console only. The CSP of the page also blocks that host (see Security).

### Where it runs

The engine runs in a dedicated Worker, not on the thread of the page. If it ran on the page thread, WireGuard, the netstack and TLS would compete with the drawing of the terminal. The Worker lives in a page of a separate origin, the **node frame** (see Security). The page embeds the frame as a hidden `<iframe>` only after the user turns Tailscale on. The page talks to the frame over a `MessageChannel`.

The page loads the wasm only then. A page that never uses Tailscale loads none of it.

### Build and size

`web/build.mjs` gets a step that runs `GOEXPERIMENT=nojsonv2 go build -trimpath -ldflags "-s -w" -tags <tags>` with `GOOS=js GOARCH=wasm` on `web/tailnet`. The step copies `lib/wasm/wasm_exec.js` of the same Go toolchain, and gzips the result. It does not run `wasm-opt`, as tsconnect does. `wasm-opt` saves 9% raw, 1.5% gzipped and nothing with brotli (see the table). The npm build of Binaryen also took over four minutes. The size work is in three places.

The tags are the list of tsconnect itself, `tailscale.com/cmd/tsconnect/wasmbuild.Tags()`. A small `go run` reads the list, so a `tailscale.com` bump keeps it current. Five more `ts_omit_` tags are added, because the web node does not need them: `c2n`, `dns`, `health`, `ipnbus` and `logtail`. The command `go run tailscale.com/cmd/featuretags --list` shows all features of the pinned version. The command with `--min --add netstack,bakedroots` gives exactly the list of tsconnect plus these five.

The web node needs none of these features:

- SSH, Taildrop, exit node and subnet routing
- serve, funnel, portmapper and captive portal
- drive, appconnectors, relay server and peerapi
- usermetrics, clientmetrics, syspolicy and the debug endpoint

The list of tsconnect already omits all of them. The node keeps `netstack` (userspace TCP to a peer) and `bakedroots` (TLS without OS roots). `tailnetlock` stays omitted, as in tsconnect. It may break tailnets that use Tailnet Lock (see Open questions).

The `main` of the engine does not import `ipnserver`, because that package pulls in `localapi` and `os/exec`. It also does not import `logpolicy` or `logtail`. It calls `NewLocalBackend` directly with a zero log ID and `log.Printf`, and starts no LocalAPI listener. The node then keeps no `log-policy` key and uploads no logs.

The build sets `GOEXPERIMENT=nojsonv2`. Go 1.27 builds `encoding/json` on `encoding/json/v2` by default. With the experiment off, std keeps its older implementation. Then `encoding/json/v2` and `jsontext` (640 KB of code) drop out, because `tailscale.com` uses its own copy (`go-json-experiment`). It is a toolchain experiment that a later Go may remove. If Go removes it, the build grows by 0.9 MB and again exceeds the limit below. The step therefore still gzips.

Measured with the pinned `tailscale.com` and Go 1.27.3:

| Build | Raw | gzip -9 | brotli 11 |
| --- | --- | --- | --- |
| tsconnect's `wasm` package, unchanged | 29.81 MB | 6.88 MB | 4.86 MB |
| the spike's engine (no SSH, with `x/net/websocket` and the roots), tsconnect's tags | 29.06 MB | 6.72 MB | 4.75 MB |
| the same, with the five more tags | 27.60 MB | 6.39 MB | 4.51 MB |
| the lean engine (no `ipnserver`, `logpolicy`, `logtail`), same tags | 27.05 MB | 6.28 MB | 4.41 MB |
| the same, with `GOEXPERIMENT=nojsonv2` (the choice) | 26.12 MB (26,121,454 bytes) | 6.08 MB | 4.28 MB |
| the choice, after `wasm-opt -Oz` (Binaryen 123) | 23.61 MB | 5.99 MB | 4.33 MB |

Each step, by raw size, is as follows. The spike engine had the five tags added to the tsconnect list one at a time:

- `logtail` saves 0.69 MB. The `main` of the spike imports `logtail`, so only part of it goes without the `main` change.
- `dns` saves 0.58 MB.
- `health` saves 0.20 MB.
- `c2n` saves 11 KB.
- `ipnbus` saves 7 KB.

Put one tag back into the set of all five. Each tag then adds only this much: `health` 190 KB, `c2n` 9 KB, `ipnbus` 8 KB, `dns` 5 KB. The tags overlap. They are worth their sum, not each alone. The `main` change saves 0.55 MB. `nojsonv2` saves 0.93 MB. The whole trim is 2.9 MB raw, 0.64 MB gzipped and 0.47 MB brotlied.

Rejected:

- `nethttpomithttp2` (a Go tag) saves another 1.2 MB. But the control client speaks HTTP/2 inside the Noise connection (`ts2021/client.go`: `SetUnencryptedHTTP2`). It was tried in the browser. The state went to `NeedsLogin`, then the `ts2021` WebSocket reconnects in a loop, and no login URL comes.
- The trimmed build ran only to the login URL. Its peers path (netmap, DERP, dial) is not run yet (see Spike, questions 5 to 7). The tags `dns`, `health` and `ipnbus` therefore stay on trial until then.

The largest packages by code size are in the table below. The numbers are function bytes from the name section of an unstripped build: 16.9 MB of code before and 15.0 MB after. The 11 MB data section is not in the numbers. It holds mostly type and function tables.

| Package | Before | After | Why it stays |
| --- | --- | --- | --- |
| `runtime` | 1197 KB | 1192 KB | Go |
| `net/http` | 725 KB | 712 KB | control and DERP clients |
| `crypto/tls` | 649 KB | 649 KB | TLS to port 443 of a peer |
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

These packages are still large and not needed. To remove them, a change in `tailscale.com` itself is necessary. The build does not patch `tailscale.com`.

- `gvisor` `ipv6`, `udp`, `icmp`, `raw` (about 430 KB): `netstack.Create` registers every protocol (`main` <- `wgengine/netstack`).
- `regexp` and `regexp/syntax` (about 150 KB): `main` <- `wgengine/netstack` <- `gvisor/pkg/refs` <- `gvisor/pkg/log`.
- `klauspost/compress/zstd`, `huff0` (about 215 KB): `main` <- `control/controlclient` <- `util/zstdframe`, for map responses that control compresses with zstd.
- `net/dns`, `net/dns/resolver`, `net/netcheck`, `net/routemanager` (about 150 KB): `ipnlocal` imports them, and no tag cuts them.

Cloudflare Workers serves a static asset of at most 25 MiB (26,214,400 bytes). The choice (26,121,454 bytes) is under that limit by 92,946 bytes, which is 0.35%. A `tailscale.com` bump may put it over the limit. The `wasm-opt` output (23.61 MB) is the way back under the limit. The build still ships the wasm compressed, as `tailnet-<hash>.wasm.gz` (about 6.1 MB). The frame unpacks it with `DecompressionStream("gzip")` into `WebAssembly.instantiateStreaming`. Cloudflare already serves `application/wasm` with brotli (`ghostty-vt.wasm` arrives with `Content-Encoding: br`). But that does not lift the limit on the stored file. The budget is at most 25 MiB stored and at most 7 MB over the network.

The name carries the hash of the content, and `_headers` keeps it for a year (`immutable`). The other files of the page revalidate on every load. A returning user downloads the engine one time for each version.

The choice, and its `wasm-opt` output, each reached `NeedsLogin` and a `https://login.tailscale.com/a/…` URL in ego lite from `http://127.0.0.1:<port>`. The URL came about 1 s after `login()`. `bakedroots` verified the `login.tailscale.com` chain.

### State

The state of the node is a few small keys: `_machinekey`, the profile with the node key, and the log policy. tsconnect keeps them in `sessionStorage`, so its node is new in every tab.

The node frame keeps them in IndexedDB. The `ipn.StateStore` of the engine is synchronous, and IndexedDB is not. The frame therefore reads every key before `start`, and the engine serves reads from memory. Each write is an event that the frame stores.

Settings has two modes:

- **Stay signed in** (default): a persistent node (`controlclient.LoginDefault`). It lasts until its key expires or the user signs out. The key expiry is the key expiry of the tailnet, 180 days by default.
- **This tab only**: an ephemeral node, as in tsconnect (`LoginEphemeral`), with its state in memory. Tailscale removes the device after it goes offline. Each new tab signs in again.

The state is not encrypted. Any key that the browser could use to encrypt the state is usable by the same code that could read the state. Encryption would therefore not stop the threat that matters (see Security).

**Device name**: `gorex-web-` and four random letters, such as `gorex-web-kqzt`. The admin console shows it with OS `js`. The user can change it in Settings. The rule of `internal/tailnode` checks the new name.

**Tabs.** Two tabs that run the same node key confuse the control server. The frame takes a Web Lock (`navigator.locks`) that is named for the node. A second tab shows "Tailscale is in use in another tab" and a button that takes it over. The first tab learns of this over a `BroadcastChannel`. It stops its node and drops its tailnet hosts.

**Sign out** calls `logout()`, so the node key expires at control. It also removes the stored state. **Forget this browser** in the admin console works the same from the other side. The next start shows `NeedsLogin`.

### Signing in

Settings gets a Tailscale section after Hosts, as Rex Go has:

- **Built-in Tailscale** on or off.
- **Control server**: empty for the server of Tailscale, or a URL. The rule `ValidControlURL` of `internal/tailnode`, ported to TypeScript, checks the URL.
- **Device name** and **Stay signed in**, as above.
- **Status**: the state, the user signed in, the name and address of this device, and each GoRex host and whether it answers.
- **Sign in** and **Sign out**.

A browser blocks a `window.open` that no click started. The login URL arrives about a second after the node asks for it. **Sign in** therefore opens a blank tab within its click. Then it sets the location of that tab when the URL arrives. When the user blocks or closes that tab, the section shows the URL as a link with `target="_blank" rel="noopener noreferrer"`. The user signs in with the identity provider of the tailnet in that tab. The page needs no redirect back. Its node moves to `Running` when control accepts the login, and the section says so. A tailnet that approves devices by hand shows `NeedsMachineAuth`. The section then says that an admin must approve `gorex-web-kqzt`.

The Hosts dialog shows a **Sign in to Tailscale** row while Tailscale is on and not running. The row opens the same section.

### Finding hosts

The network map of the node lists every peer that the node can see. The page shows a peer as a GoRex host when all of these conditions are true:

1. **Same user**: The user of the peer is the signed-in user, and the peer is untagged. `identify` lets in such a peer without `--allow-tailnet-members` (see Gateway). With that flag, the gateway lets in more peers, but the page cannot know it. A later version may list tagged peers on request.
2. **Looks like a gateway**: Its `Hostinfo.App` is `gorex` (a small gateway change, below). For a gateway without it, its host name ends in `-gorex`, the default name (`defaultHostname` in `internal/rexapi/tailnet.go`). The name alone is weak, because the user can give the device any valid name (`gorex rexapi tailscale rename NAME`, or `on --hostname NAME`).
3. **Answers as one**: The engine opens HTTPS to the MagicDNS name of the peer on port 443 and asks for `GET /api/help`. A gateway that lets the node in answers 200 with the help document of its API. A gateway that does not let the node in answers 403 with a JSON `{"error": …}` (`writeRefusal`). The page lists such a peer as a host that refuses this browser, with the message. The page does not list a peer that times out (3 s) or answers in another way. The check runs one time for each peer for each network map, for online peers only, and never for more than four peers at once.

By default, the page does not probe peers that pass conditions 1 and 3 but not 2. A probe of every peer of a large tailnet on port 443 is noise in their logs. Settings has **Look for GoRex on all my devices**, which probes them one time.

tsconnect reports the network map only when the node itself changes (`SelfChange` in its notify callback). Peers no longer ride on the IPN bus. The engine watches the bus with `NotifyPeerChanges` and rebuilds the host list on every peer change. A gateway that comes online therefore appears without a reload.

### Hosts and transport

`Host` (`web/src/hosts.ts`) gets `kind: "direct" | "tailnet"`. A direct host is today's host: a URL that the user added, saved in `localStorage`. A tailnet host is not saved. It comes from the network map, by its stable node ID, with its MagicDNS name as its address. Its ID is `ts:<StableNodeID>`, so the chosen host survives a reload. The page saves the name and icon that the user gives it under that ID. The Hosts dialog lists tailnet hosts in a group of their own, below the direct hosts, with a state dot (online, offline, refusing).

`web/src/rex.ts` gets a transport seam. Today, `setHost` sets a base URL. `Control`, `DataStream` and `files.ts` call `new WebSocket` and `fetch` on URLs that they build from it. After the change, they use this interface:

```ts
interface Transport {
  socket(path: string, protocol: string): SocketLike;
  fetch(path: string, init?: RequestInit): Promise<Response>;
}
```

- `directTransport(url)` is today's code, unchanged.
- `tailnetTransport(host)` sends both through the node frame. `SocketLike` is the part of `WebSocket` that `Control` and `DataStream` use (`binaryType`, `protocol`, `send`, `close`, `onopen`, `onmessage`, `onclose`). They therefore do not change. `fetch` answers a real `Response` that is built on a `ReadableStream` that the frame fills. `fetchImage` therefore reads it as it reads the own `Response` of the browser.

`setHost(url)` becomes `setHost(host)`, which picks the transport.

**Downloads.** Today a file that the page does not show downloads through a hidden `<iframe>`. The `<iframe>` navigates to `/api/download?ticket=…`, and the browser saves the file as it arrives (`docs/WEB-001-open-files.md`). The browser cannot navigate to a tailnet address. Over the tailnet, the page therefore gets the file through the transport into a `Blob` and saves it with an `<a download>`. That holds the whole file in memory, so the page caps it at 256 MiB. Above that size, the page shows `too_large` with the cap in the message. When the Service Worker of the page controls the page, the file streams instead, with no cap. The worker answers a same-origin URL, `save/<id>`, from the stream of the transport, chunk by chunk (`docs/REF-007-web-client.md`, Service Worker).

This puts the bytes of the host on the origin of the page as a blob. The direct path avoids this. The page never shows the blob and never navigates to it. The page saves it and revokes its URL.

### Gateway

The gateway needs no change for the page to work:

- **Who gets in**: `identify` (`internal/rexapi/tailnet.go`). The browser node is an untagged device of the user who signed it in. When that user is the user who signed the gateway in, the node gets in, as Rex Go does. A tagged browser node needs `--allow-tailnet-members` and an access rule.
- **Origin**: The engine sends `Origin` with every request. It sets `Origin` to the origin of the page, as the frame received it from the `postMessage` of the page (`event.origin`). It never sets a made-up origin. The gateway checks it as for the browser (`checkOrigin`). The user must allow the hosted page with `gorex rexapi web allow https://web.gorex.uk`, as today.
- **Host**: The engine sends the MagicDNS name of the peer as `Host` and as the TLS server name. `hostAllowed` (in `internal/rexapi/host.go`) takes the MagicDNS name of built-in Tailscale and its first label, so it passes.
- **CORS** does not apply. The engine is not a browser, and it reads every answer.

One small change helps to find hosts. The gateway sets `hostinfo.SetApp("gorex")` before its node starts, so peers see `Hostinfo.App`. The setting is process-wide. The gateway is the only `tailscale.com` node of its process. It is not verified if control passes `App` on to peers (see Open questions). The name rule covers older gateways in both cases.

## Alternatives

- **The Tailscale app on the viewing device**, with the page opened as `?host=https://<machine>-gorex.<tailnet>.ts.net`. It works today, with no new code. The user rejected it for this feature. It needs the app and its VPN on the device, which is the problem.
- **Tailscale Funnel.** It puts the gateway on the public internet. There, `identify` cannot tell who calls, because a Funnel request comes from the ingress of Tailscale and not from a tailnet device. It would need a login of GoRex's own. Rejected.
- **`@tailscale/connect`**, the npm package of tsconnect. It pins its own Tailscale and carries an SSH client. Its `fetch` answers only text. It has no WebSocket and no stream. The engine reuses its code, not its package.
- **A relay of ours** (a Worker that joins the tailnet and proxies the page). It would hold the tailnet credentials of every user on a server. Rejected.
- **tailcat in the browser.** GoRex already runs tailcat (`internal/rexapi/tailcat.go`, `docs/IOS-002-tailcat.md`). It is the data plane of Tailscale with keys from a pairing, with no control server and no login. A browser tailcat client would share most of this work:
  - the wasm build step, its size budget, lazy loading and the hashed, compressed asset
  - the core of the engine: `wgengine` with `netstack`, DERP over WebSocket, and HTTP and WebSockets over a netstack connection
    - A browser has no UDP, so a tailcat relay must accept WebSockets too.
    - HTTP inside WireGuard is plain for tailcat and uses TLS for the tailnet.
  - the transport seam in `rex.ts`, `SocketLike` and the streamed `Response`, and the blob download
  - the node frame, its storage of keys and its tab lock.

  It would differ in identity (a client key and pairing, instead of OAuth and a network map). It would also differ in finding hosts (the address from a QR code or link). The seam is designed so that a third transport plugs in beside the two.

## Security

### What the node is worth

The node is a full device of the tailnet of the user. Unless the access rules of the tailnet say otherwise, it reaches everything that the laptop of the user reaches. Examples are SSH servers, NAS shares and admin pages. Its keys sit in browser storage.

A script that runs in the origin that holds the node (an XSS) can therefore do two things:

- Use the node while the page is open, to reach any device of the user.
- Copy the machine and node keys and run the device elsewhere, until its key expires or someone removes it.

Encryption of the stored state does not change this. The code that decrypts the state runs in the same origin.

### Which origin holds the node

The node lives in its own origin, the node frame, and not in the origin of the page. The page is large and renders untrusted text (terminal output, links, file names and images). The frame is small and renders nothing.

- A second site of its own serves the frame (a separate `*.workers.dev` name, which the Public Suffix List makes a different site). Its storage is apart from the storage of the page. A script in the page cannot read the keys.
- The frame answers `postMessage` only from the origin of the page. Its `frame-ancestors` also names that origin. The frame offers only the calls above. `request` and `socket` take a host from the GoRex host list of the frame and always use port 443. A script in the page can therefore reach GoRex gateways, which it could already reach as the page, and nothing else of the tailnet.
- Browsers partition the storage of a third-party frame by the top-level site. Only the page embeds the frame, so its state is stable under that key.

A first version may run the node in the origin of the page, behind the same narrow API, to ship sooner. Until the frame exists, that version must not keep keys longer than the tab (`This tab only`).

### CSP

The policy of the node frame is:

```
default-src 'none'; script-src 'self' 'wasm-unsafe-eval';
connect-src 'self' https://controlplane.tailscale.com wss://controlplane.tailscale.com wss://*.tailscale.com;
worker-src 'self'; frame-ancestors https://web.gorex.uk;
base-uri 'none'; form-action 'none'
```

`wss://*.tailscale.com` covers the DERP servers of Tailscale (`derpN<x>.tailscale.com`). A control URL or DERP map of the user's own needs its hosts added. The policy of the frame is per deployment.

The policy of the page gains only `frame-src` for the origin of the frame. `frame-src http: https:` already allows it. The policy tightens to that origin plus what downloads need. Its `connect-src` stays as it is, because direct hosts need any `http:` and `ws:` address.

`window.open` and links are not under CSP. The page sets no `Cross-Origin-Opener-Policy`. A login tab opens (see Spike).

### Access rules

Settings help documents two setups:

- **Untagged** (default, works with no tailnet changes): The browser node is the device of the user and has the reach of the user. Keep **This tab only** on shared computers.
- **Tagged**: The policy of the tailnet defines `tag:gorex-web`, owned by the user. The frame advertises it at login. One grant lets `tag:gorex-web` reach the gateway on port 443 and nothing else. The gateway runs with `--allow-tailnet-members`. The node then reaches only GoRex, even if its keys leak. Tagged devices have no key expiry by default, so the tailnet must set one for the tag.

### Revocation

The user signs out in the page, or removes the device in the admin console. Both end the node at control at once. An ephemeral node also goes away by itself after it goes offline.

## Spike

The spike ran on Windows, Go 1.27.3, `tailscale.com` at the version that `go.mod` pins, in a scratch module that copied `go.mod` and `go.sum`. It used Chrome 156 through ego lite. Nobody signed in.

| # | Question | Result |
| --- | --- | --- |
| 1 | Builds with the pinned `tailscale.com`? | Verified. `GOOS=js GOARCH=wasm go build -tags <wasmbuild.Tags()> -trimpath -ldflags "-s -w" tailscale.com/cmd/tsconnect/wasm`: 29.81 MB raw, 6.88 MB gzip, 4.86 MB brotli. The spike's own engine: 29.06 / 6.72 / 4.75 MB. Trimmed (see Build and size): 26.12 / 6.08 / 4.28 MB. |
| 2 | Reaches a login URL from a page? | Verified, from `http://127.0.0.1:<port>` under the CSP of the hosted page. `NoState`, then `NeedsLogin` 60 ms after start. After `login()`: `GET https://controlplane.tailscale.com/key?v=147` (200, `Access-Control-Allow-Origin: *`), WebSocket `wss://controlplane.tailscale.com/ts2021?X-Tailscale-Handshake=…` with protocol `tailscale-control-protocol` (open), and a `https://login.tailscale.com/a/…` URL 1.3–2.3 s after `NeedsLogin`. Also `POST https://log.tailscale.com/c/tailnode.log.tailscale.io/…` (no-cors, logs). No DERP before login. |
| 2b | Under a tight CSP? | Verified. With `connect-src 'self' https://controlplane.tailscale.com wss://controlplane.tailscale.com wss://*.tailscale.com`, the same login URL came. CSP blocked the log upload, with no effect on login. |
| 3 | Does control or DERP check `Origin`? | Verified: neither does. For no `Origin`, `https://gorex-web.alice.workers.dev`, `http://127.0.0.1:…` and `https://evil.example`: control answered 101 and completed the Noise handshake (protocol 147). `derp20b` and `derp4f.tailscale.com` answered 101 with protocol `derp` and sent their server key frame. `/key` answers `Access-Control-Allow-Origin: *`. |
| 4 | Can the login URL open? | Verified. `window.open` with no click returns `null` (blocked). Within a click, it opens a tab, and the page can set the location of the tab later. No CSP violation. |
| 5a | TLS to `*.ts.net` in wasm? | Verified in the wasm of the browser. `x509.SystemCertPool` has 0 roots, and verification with it fails (`certificate signed by unknown authority`). With `bakedroots.Get()`, the LetsEncrypt chains of `login.tailscale.com` and `letsencrypt.org` (Let's Encrypt YE1/YE2, Root YE) pass, and the check refuses a wrong name. The spike did not try the certificate of the gateway itself. |
| 5b | HTTP and WebSocket over the netstack? | Not verified (needs a signed-in node). The `request` and `socket` of the engine build for `js/wasm`. Read in code: `net/http` skips `fetch` when `DialContext` is set, and `coder/websocket` cannot dial under `js`. |
| 5c | Gateway checks for a wasm caller? | Not verified at run time. Read in code (see Gateway). |
| 6 | Peers from the network map? | Not verified (needs a signed-in node). `peers()` of the spike reads `NetMapWithPeers()`: name, `Hostinfo.Hostname`, `Hostinfo.App`, OS, user, tags, online and capabilities. The callback of tsconnect reports it only on `SelfChange`. |
| 7 | Recognizing a gateway? | Not verified. Read in code: the default name is `<machine>-gorex`, but a user can set any name. `tsnet` sets only `Hostinfo.Package = "tsnet"`. The gateway could set `App`. |
| 8 | Fits Cloudflare? | Read in the limits of Cloudflare: 25 MiB (26,214,400 bytes) for each asset. The builds of the spike do not fit, not even after `wasm-opt -Oz` (26,525,079 bytes). The trimmed build (see Build and size) fits by 93 KB (26,121,454 bytes). Gzip (6.1 MB) fits with room. |

## Phases

1. **Engine** (built, #410): `web/tailnet`, the build step in `build.mjs`, the compressed hashed asset, and a dev page that signs in and lists hosts. This phase settles questions 5b, 6 and 7 of the spike against a real gateway.
2. **Transport** (built, #410): the seam in `rex.ts`, `tailnetTransport`, a terminal opened over the tailnet, and downloads through a blob.
3. **Settings and hosts** (built, #410, #546, #547): the Tailscale section, sign-in, the tailnet host group, the tab lock, and both state modes.
4. **Node frame** (not built): the separate origin, its CSP, and the narrow API. Until then, the design allows only **This tab only**.
5. **Gateway** (not built): `hostinfo.SetApp("gorex")`, after phase 1 shows that peers see it.

## Open questions

- Does the control server of Tailscale pass the `Hostinfo.App` of a peer to other peers? If not, the gateway needs another mark: a name rule only, or a node attribute from the policy of the tailnet.
- Does Headscale accept the control protocol over WebSocket (`/ts2021` with the handshake in the query) and answer `/key` with CORS?
- Tailnet Lock: The build of tsconnect omits `tailnetlock`, and it reports a locked-out node. A locked tailnet must sign the browser node. It is not known if that works without the feature linked.
- Safari caps script-written storage at 7 days. Does **Stay signed in** last on Safari, for the IndexedDB of a partitioned frame?
- What are the throughput and latency through DERP only, for a busy terminal? What is the cost of the engine on the battery of a phone?
- Does the Go engine run in a Worker as it runs on the page? tsconnect runs it on the page.

## As built

Phases 1 to 3 are built. They have these differences from the design above:

- **Origin and mode.** The node runs in the origin of the page, as the first version that Security allows. There is no node frame (phase 4). Both state modes exist (#546). **Stay signed in** is the default (`prefs.tailscalePersist`). It keeps the state in IndexedDB (`web/src/tailstore.ts`) through `persistStore` in `web/tailnet/main.go`, which backs `ipn.StateStore` with `mem.Store`. **This tab only** uses an ephemeral node (`LoginEphemeral`) with its state in memory. The tab lock exists. It uses a Web Lock (`navigator.locks`) and a `BroadcastChannel`, and a second tab can take the node over. #547 separates an unsigned reload from an expired Tailscale login. The Security section says that the first version must not keep keys longer than the tab until the frame exists. The code differs from this rule, because **Stay signed in** keeps keys in IndexedDB in the origin of the page.
- **Thread.** The engine runs on the thread of the page, as tsconnect does, and not in a Worker. Its events reach the page through `queueMicrotask`, so no page code runs inside a call from Go.
- **Engine.** `web/tailnet` is `package main` with `//go:build js && wasm` in the module of GoRex. `go.mod` did not change (`go mod tidy -diff` shows only line endings). It sets one global, `gorexTailnet(config, emit)`. Its calls are `start`, `login`, `logout`, `stop`, `rename`, `netmap`, `request` and `socket`. `hosts()` belongs to the page (`web/src/tailnet.ts`) and uses `netmap()`.
- **WebSocket.** The engine does not use `golang.org/x/net/websocket`. That package drops the close code (the page needs 4000, a closed block) and does not join fragments. The engine uses `web/tailnet/wsclient`, a small RFC 6455 client. It is tested against `coder/websocket` with the tests of the desktop.
- **Hosts.** The page lists only peers that answered `/api/help`. They stay listed as offline when they go offline. The empty state of the guide names the peers called `*-gorex` that did not answer. The page asks a refusing host again every 15 seconds. It asks a host that did not answer every minute. **Look for GoRex on all my devices** does not exist yet. Besides the events of the network map, the page reads the map every 15 seconds while the node runs. The `hostinfo.SetApp("gorex")` call of the gateway (phase 5) is not done.
- **Refusals.** The guide maps the 403 messages of the gateway. "does not trust web pages from" means: run `gorex rexapi web allow <origin>`. "lets in only the Tailscale user who signed it in" means: sign in with that account, or run `gorex rexapi tailscale on --allow-tailnet-members`. "built-in Tailscale is off" has its own message.
- **Settings.** There is no control server field, because the CSP of the page allows only the servers of Tailscale. The device name is in `gorex.settings` (`tailscaleName`). It is random for each tab until the user sets it.
- **Build.** The page loads everything by name from `dist/assets` under a content hash, and keeps it for a year (`docs/REF-007-web-client.md`, Cloudflare Workers). The asset manifest lists the engine (`tailnet/engine`, `tailnet/exec`, lazy: `docs/REF-007-web-client.md`, Loading on demand). The loader keeps it in its cache after it loads. The engine compiles from bytes (`WebAssembly.instantiate`) after `DecompressionStream("gzip")`. A `.gz` is not served as `application/wasm`, so `instantiateStreaming` does not apply. The page also takes a module that arrives already unpacked.
- **Sign-in tab.** The page opens it blank within the click, with its `opener` set to null. Then the page sends it to the login URL when the URL arrives. The Cancel button of the guide closes it.
