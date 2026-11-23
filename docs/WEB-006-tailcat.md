---
name: WEB-006
description: "The web client becomes a tailcat client of a GoRex host: a device key the browser keeps, DERP over WebSocket, and the usual pairing window. Use when you change the web tailcat engine, its keys or its pairing."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# WEB-006: Web client over tailcat

Status: proposal.

This doc designs the web client's way in that [NET-002](NET-002-host-links.md) left for later ("the key of the web client", phase 7) and [NET-004](NET-004-public-relay.md) named as phase 3. The page runs tailcat compiled to WebAssembly, keeps a device key in browser storage and pairs through the same window and approval as a desktop or a phone.

## Problem

The web client reaches a host in two ways today ([REF-007](REF-007-web-client.md)):

- **The local network.** The gateway's HTTP listener, with the pairing token or an address on the legacy list. It ends at the network's edge, and it is unencrypted.
- **The page's Tailscale node** ([WEB-002](WEB-002-tailscale.md)). It needs a Tailscale account, an interactive sign-in in the page, and it makes the tab a full device of the tailnet. On a borrowed computer, or for a user with no tailnet, there is no way in.

NET-002's client table names the way in for the web client: tailcat compiled to WebAssembly, relayed only, with a key the browser keeps and the same pairing. The private relay of NET-004 gives this path a relay whose certificate a browser accepts. The piece that does not exist is the client itself: the page holds no device key, builds no tailcat engine and knows no pairing.

## Goal

- The user pastes a host's tailcat address into the page, compares a fingerprint and approves on the host, as a desktop or a phone does. No Tailscale account and no shared network is needed.
- The page works wherever the browser can reach the relay, because the whole path is `wss:`.
- The browser keeps one device key. The host's Devices list shows it, and Remove revokes it.
- One tab owns the key at a time, as one tab owns the Tailscale node.
- On this path the page is a device to the gateway, as Rex Go is. The origin allowlist of REF-007 does not apply to it.

## Non-goals

- **A direct path.** A browser has no UDP sockets. Every tailcat connection of the page is relayed, and the page says so where a desktop would name a direct path.
- **The machine as its own relay.** A browser cannot pin a certificate. An address whose region is a host's own listener fails for the page, as NET-002 sets out. The host's DERP-homed address is the one to give the page.
- **A node frame of its own.** The key lives in the origin of the page in this version, as the Tailscale node's keys do today. A separate origin for keys is a later work item, shared with the node frame of WEB-002 phase 4.
- **Scanning the QR code.** The first version takes a pasted address. A camera scan is a small later addition.
- **New listeners or protocols on the host.** Pairing on `RemotePort` and the gateway on the tailcat address exist already ([IOS-002](IOS-002-tailcat.md)). This doc adds a small display change on the desktop and nothing else.
- **Opening the team relay.** Admission to `derp.gorex.uk` stays the operator's list, as NET-004 decides.
- **The page as a host.** The web client stays a remote client only, as REF-007 fixes.

## Design

### Behavior

1. On the host, the user turns on **Let other devices connect** and opens **Add a device**, which opens the pairing window. The window's QR dialog gains the address as copyable text: the same `rexgo://tailcat?a=<address>&n=<name>` string the code encodes, with Copy. `gorex remote address` already prints the same address.
2. In the page, **Add host** takes the pasted string. The form accepts the `rexgo://tailcat` link and the bare `tc…` address. It refuses anything else.
3. The page asks to pair with a device name (`web-` and four random letters, editable, as Rex Go's `iphone-rex-kqzt`). The host shows the request with a fingerprint. The page shows the fingerprint the host's answer returned. The user compares them and approves on the host.
4. While the request waits, the host's row says so. On approval the host connects and behaves as every other host: sessions, windows, dots, Connect and Disconnect.
5. Settings shows **This browser's key**, the device key's public half, copyable. It is the key the host's Devices list shows, and the key an operator puts on the relay's list ([NET-004](NET-004-public-relay.md)). One key serves both allowlists.
6. The key persists across reloads by default, in IndexedDB. A **This tab only** choice keeps it in memory, as the Tailscale sign-in's choice does. A tab-only key makes a new device on the host each time, so the choice says so.
7. A second tab that opens the page sees that another tab holds the key and offers to take it over, as the Tailscale node's tab lock does.
8. A host whose address names the machine's own relay — a region with an IP host and a pinned certificate — fails at connect with a message that a browser cannot reach it. The message names the host's DERP-homed address as the one to use.

### The engine

`web/tailcat` is a `package main` for `GOOS=js GOARCH=wasm`, beside `web/tailnet`. It sets one global, `gorexTailcat(config)`, and shares the tailnet engine's seam: the build step, the hashed compressed asset, lazy loading and the calls `request` and `socket` that the page's `Transport` wraps. What the two engines share across the js boundary — the promise, the streamed answer and the WebSocket over a dialed connection — lives in `web/jsbridge`.

Its calls:

| Call | Does |
| --- | --- |
| `key()` | the device public key, `nodekey:` and hex, as a machine's allowlist shows it |
| `privkey()` | the device private key's text, for the page to keep in IndexedDB |
| `check(address)` | parses an address, and says when its region is a machine's own relay |
| `pair(address, name)` | one `remote.pair` to `RemotePort`, answering the fingerprint or the server's error |
| `request(address, method, path, headers, body, timeoutMs)` | HTTP/1.1 to the gateway on the tailcat address, port 7297 |
| `socket(address, path, protocol, emit)` | a WebSocket to the gateway, with `web/tailnet/wsclient` over the same connection |
| `close(address)` | drops the client of an address, for Disconnect and host removal |

The differences from the tailnet engine:

- No `ipnlocal.LocalBackend`, no control client, no login, no network map. A `tailcat.Client` (`github.com/tailscale/tailcat`, the version `go.mod` pins) per address does the work, as `ios/tailnet` does. One client per address, not per host: two clients of one key take each other's packets.
- No TLS and no `bakedroots`. The gateway's tailcat port is plain HTTP inside WireGuard, as on iOS. `DialTCPPort(7297)` gives the `net.Conn`, and `http.Transport` with `DialContext` keeps requests off the browser's `fetch`.
- No DERP map fetch. The address's `ConnInfo` carries the region, so the engine never downloads `derpmap.json`. That fetch would cross origins under the browser's rules; the design needs none of it.
- DERP goes over WebSocket with no code of ours. Under `GOOS=js`, `derphttp` sets `canWebsockets` and dials `wss://<relay>/derp` with the `derp` subprotocol. The browser checks the relay's certificate chain itself. This is why the relay needs a public certificate, and why a pinned self-signed certificate cannot serve the page.

`pair` dials `RemotePort` on the address, writes one JSON line (`Request{Op: "remote.pair", Remote: {Name}}`) and reads one JSON line back, as `pairConn` serves it (`internal/rex/remote.go`). The engine adds no third protocol.

The device key is a `key.NodePrivate` the engine makes on first use. The page persists it in IndexedDB next to the Tailscale node's state (`web/src/tailstore.ts` is the pattern). Go never writes it: the page hands it to `gorexTailcat` at start, as Rex Go hands its Keychain key to `SetTailcatKey`.

### The page

- `web/src/tailcat.ts` mirrors `web/src/tailnet.ts`: it owns the engine's lifecycle, the key store (`web/src/catstore.ts`), the tab lock (the same Web Lock and `BroadcastChannel` mechanism) and `transport(host)`, a `Transport` on the engine's `request` and `socket`, made in `web/src/catengine.ts`.
- A host entry gains `kind: "tailcat"` and `address`. Its `url` holds only the label "tailcat": the address is the pre-shared key of the host, a secret the page neither shows nor logs nor puts in a URL. The host list, Settings › Hosts, the dots and Connect/Disconnect treat it as every host.
- The pairing surface shows the fingerprint, the name it asks with and the states: waiting for approval, refused, and the relay certificate error of Behavior step 8.
- The tab lock is required, not optional: two tabs with the same key are two tailcat clients of one key.
- The `connect-src` of the page needs no change. `web/_headers` already allows `wss:` generally, which covers DERP to any relay host.

### The relay

For a relay to serve a browser client it needs three things:

1. **A certificate a browser accepts**, from a public authority for its name. `derp.gorex.uk` has one; the `sha256-raw:` pin of a machine's own relay can never have one.
2. **A WebSocket entry.** The HTTP handler must be wrapped with `derpserver.AddWebSocketSupport` (`tailscale.com/derp/derpserver`), which answers `Upgrade: websocket` requests with the `derp` subprotocol. The DERP service in `si-mods/gorex-relay` wraps its handler this way; the deployment note records it. A `derper` of a user's own needs the same, which upstream `derper` serves on its DERP endpoint.
3. **Admission, where the relay has a list.** The browser's node key goes on `derp-allowed.json` beside the team's other keys. The same key pairs the page at the host.

Nothing else about a relay changes. It sees node public keys, ciphertext, and the timing and size of the traffic, as NET-002 sets out.

### What the host keeps

- The pairing window, the fingerprint, the approval and the allowlist are unchanged. A browser is one more kind of device.
- The gateway's `identify` already answers a tailcat stream as `kind: "device"` (`internal/rexapi/tailcat.go`). The engine sends no `Origin` header, so the page needs no `web allow` entry on this path.
- Revocation is the host's Remove: the relayed connections close and the key is refused, on every client kind.

## Alternatives

| Option | Why not |
| --- | --- |
| Tailnet path only (WEB-002) | it asks for a Tailscale account and an OAuth sign-in in the page, and the tab becomes a full device of the tailnet. tailcat asks only for the host's address |
| A proxy that joins the tailnet for the page | it holds the tailnet credentials of every user on a server. WEB-002 already rejected this shape |
| Extend the push API of NET-003 to carry sessions | a POST API is not a stream. NET-004's table answers this for the same reason |
| Pair through `remote.pair` over a page-local TLS listener on the host | a second pairing endpoint for the same window. The tailcat connection to `RemotePort` is already there and needs no certificate |
| Keep the device key only in memory | a refresh would end the pairing and add a device each time. Persistence with a tab-only choice matches the Tailscale sign-in's two modes |

## Security

- **The device key sits in browser storage.** A script in the page's origin can use it while the page is open, or copy it and run the device elsewhere until the host removes it. This is the same exposure as the Tailscale node's keys today, and narrower: the key reaches only hosts that paired it, over tailcat only.
- **The address is a secret.** It carries the server's key and the pre-shared key. The page stores it with the key, sends it nowhere else and never logs it. The host entry's edit form does not show it back.
- **Admission to the host does not change.** The allowlist and the pairing approval decide, on every client kind.
- **Admission to the team relay does not change.** The operator's list decides; the page's key is one entry of it.
- **The relay sees keys, ciphertext and traffic shape only**, as NET-002 sets out for a public relay.
- **A refused or revoked key** shows on the host's row, as a refused Tailscale sign-in shows today.

## Migration

- No host changes: no saved file, no protocol, no listener.
- A host entry in `gorex.hosts` gains `kind` and `address`. An older page reads an entry it cannot use; the field names keep today's shape, and a page older than this work simply does not produce such entries.
- Removing the page's storage ends the device until the key is paired again; the host's allowlist entry stays until the user removes it.

## Tests

- **Engine (js/wasm):** it compiles, and `GOOS=js` takes the WebSocket DERP path against a `derpserver` wrapped with `AddWebSocketSupport` in a test.
- **Pairing:** against a test session server, `pair` answers the fingerprint while the window is open, the pending error while it waits, and a refusal with the window closed.
- **Request and socket:** against a test gateway over a local `derpserver`, a control WebSocket opens and a streamed answer arrives. `wsclient` is already covered.
- **Tab lock:** a second tab cannot take the key until it takes the lock; a tab that closes frees it.
- **End to end in a browser:** pair, connect, open a terminal, reload the page and reconnect, remove the device and watch the refusal. Once through the tailcat fleet, once through a `derpserver` of our own over WebSocket.
- **Ops:** `derp.gorex.uk` answers a WebSocket upgrade with the `derp` subprotocol.
- No item of `docs/invariants.md` names this path.

## Phases

1. **Spike and engine**: `web/tailcat` builds for js/wasm, pairs and opens a session through a local `derpserver` wrapped with `AddWebSocketSupport`. Settles the open questions.
2. **The page**: `web/src/tailcat.ts`, the host entry and its form, the pairing surface, the key display and the tab lock.
3. **The host and the relay**: the QR dialog's copyable address, and the `si-mods/gorex-relay` deployment note that records the WebSocket wrap.

## Open questions

1. **Does the tailcat.dev fleet accept DERP over WebSocket?** Upstream ships a browser tailcat (`web/main_js.go` in `github.com/tailscale/tailcat`), so its relays probably do. The spike checks one.
2. **Does `derp.gorex.uk` wrap its handler with `AddWebSocketSupport`?** The check is in `si-mods/gorex-relay`. If it does not, phase 3 adds it before the page can use the relay.
3. **The engine's size.** tailcat links `magicsock`, WireGuard and `netstack`, but no LocalBackend or control client, so the asset should come under the tailnet engine's. The spike measures it against the 25 MiB asset limit.
4. **The pairing name.** `web-<rand>` as proposed, the browser and OS (`web-firefox-linux`), or a name the user sets first. Decided in phase 2; the name is what Devices shows.
