# Rex Go: relay and direct connections

Status: proposed. Answers the rest of
[#330](https://github.com/si-mods/gorex/issues/330), after
[built-in Tailscale](ios-tailnet.md).

## Problem

Rex Go reaches a GoRex machine away from home only through a tailnet:
the Tailscale app's VPN, or since #382 the Tailscale built into the app.
Both need a Tailscale account or a control server of one's own, and
neither works with Headscale today (ios-tailnet.md, Non-goals). The
other way in, the local network listener, is plain HTTP, open to every
device in the networks allowed, with no credential.

#330 asks for two ways that need no VPN and no tailnet:

1. **Direct**: the machine has a public address (or a port forwarded to
   it), and the phone connects to it directly, encrypted.
2. **Self-hosted relay**: the machine has no public address. It dials
   out to a relay the user runs, and the phone reaches it through the
   relay.

Both with credentials of their own: pair a phone, revoke one phone
alone, reconnect after a network change into the sessions that are
still running.

[alma-relay](https://github.com/yetone/alma-relay) (MIT) solves the
relay half for Alma: a small relay that forwards HTTP and WebSockets
from phones to a desktop that holds a WebSocket to it. Its relay sees
only ciphertext, and it deploys in minutes to Cloudflare Workers (free
plan) or to any VPS with Docker and Caddy. Its wire protocol is
documented and versioned (`docs/PROTOCOL.md`). Its end-to-end
encryption is not: that lives in Alma's own, closed, apps.

## Goal

One encrypted, authenticated channel between Rex Go and the gateway,
carried either straight to the gateway or through an alma-relay:

```
Rex Go ──WS──▶ gateway :7297 /__gorex_e2e__                      (direct)
Rex Go ──WSS─▶ relay /c/<token>/__gorex_e2e__ ◀──WSS /agent── gateway  (relay)
```

- The channel is Noise IK, as WireGuard and Tailscale's control protocol
  use. Each side has a static key. The phone learns the gateway's key
  from the pairing QR code; the gateway learns the phone's at pairing.
- Inside the channel runs plain HTTP to the gateway, as on the local
  network listener. Rex's API, the data WebSockets, `/api/spaces` and
  uploads are unchanged.
- The gateway keeps a list of paired devices. Removing one ends its
  connections and refuses its next handshake.
- The relay is alma-relay as it is, speaking its protocol 3. GoRex ships
  no relay of its own.

## Non-goals

- **A relay of GoRex's own.** alma-relay already runs on Cloudflare and
  on Docker, with TLS, access keys, flow control and tests. Should it
  stop fitting, its protocol is small enough to serve from Go later.
- **Rex's own iPhone app.** It knows neither the channel nor the relay;
  it keeps the local network and the tailnet.
- **The GoRex desktop as a client.** Remote hosts of the desktop app
  keep SSH and the tailnet. The channel is not phone-specific, so this
  can come later.
- **Push notifications.** alma-relay can send APNs for the desktop, but
  Rex Go doesn't run in the background (ios-tailnet.md, Non-goals).
- **Peer-to-peer through NAT.** Without a public address the data goes
  through the relay. That is the tailnet's job.
- **TURN.** alma-relay's TURN credentials are for WebRTC; Rex Go has
  none.

## Design

### Channel

`internal/e2e`, a Go package both ends use: the gateway, and Rex Go
through `gomobile` (Engine, below). One implementation of the crypto,
tested once.

The handshake and framing are `tailscale.com/control/controlbase`, which
GoRex already links: Noise_IK_25519_ChaChaPoly_BLAKE2s over any
`net.Conn`, returning a `net.Conn` that knows its peer's key
(`Conn.Peer()`). Its `key.MachinePrivate` / `key.MachinePublic` are the
keys. No new crypto dependency. Its prologue names Tailscale's control
protocol and a version; the channel uses a version that protocol never
uses (a constant in `internal/e2e`), and the gateway refuses any other,
so a GoRex handshake can't pass for a Tailscale one or the other way.

The channel runs over a WebSocket: each controlbase frame (at most
4 KiB) is one binary WebSocket message. That fits every limit on the
way (alma-relay takes 24 MiB per message, Cloudflare 1 MiB).

On the channel the phone speaks HTTP/1.1, and the gateway serves it
with the same handler as its other listeners. One phone connection (a
WebSocket, or a keep-alive HTTP connection) is one channel and one
handshake. alma-relay's per-stream flow control (protocol 3) then holds
back only the terminal whose phone reads slowly.

### Pairing

The gateway has one static key, `rexapi/e2e.key` in its data directory
(mode 0600), made when first needed. Rex Go has one, made at first
pairing and kept in the Keychain (this device only, not synced).

Pairing, from Settings › Remote › Rex Go devices › **Pair a phone** on
the desktop, or `gorex rexapi pair --device`:

1. The gateway makes a one-time pairing code: 128 random bits, good for
   10 minutes and one handshake.
2. It shows a QR code:

   ```
   rexgo://pair?k=<gateway key>&c=<code>&n=<machine name>
       &r=<relay URL>&t=<routing token>      (when a relay is set)
       &d=<direct URL>                        (when direct is on)
   ```

   The `rexgo` scheme is Rex Go's own (its sign-in callback already uses
   it), so Rex's app doesn't take the code for an address, and the
   camera app opens Rex Go with it.
3. Rex Go handshakes as for any connection. Its key is unknown to the
   gateway, so its first message on the channel must be
   `POST /__gorex_e2e__/pair` with the code and the phone's name
   (`rexgo-xxxx`, as ios-tailnet.md, or one typed). Anything else, a
   wrong code, or a used or expired one, and the gateway closes the
   channel.
4. The gateway adds the phone's key to `rexapi/devices.json`: key, name,
   when paired, when last seen. The desktop's dialog closes and the
   device is in the list.

The code never reaches the relay in the clear: the phone sends it only
inside the channel, to the key from the QR code. A photo of the QR code
is good for 10 minutes and one phone.

### Revoking

Settings › Remote › Rex Go devices lists the paired phones with when each
was last seen; **Remove** revokes one. `gorex rexapi devices` and
`gorex rexapi devices remove NAME|KEY` do the same. The gateway drops the
key from `devices.json`, closes that device's open channels, and refuses
its next handshake. The other phones are not touched.

The gateway's own key is not rotated by revoking. Should it leak (a
copy of the data directory), `gorex rexapi devices reset` makes a new
one and forgets every device: every phone pairs again.

### Direct

The local network listener gets one more path, `/__gorex_e2e__`, served
to every address whatever networks are allowed: the channel is its own
check. Everything else keeps the network check.

So a machine with a public address, or a port forwarded to it, turns on
the listener on `0.0.0.0:7297` and allows no network: then only paired
phones get in, and only through the channel. `gorex rexapi lan direct
URL` sets the address the QR code gives for it (`ws://203.0.113.5:7297`
or a name), as the phone can't know the public address. At home the same
pairing works over the local network address.

TLS is not needed on the way: the channel is encrypted and
authenticated end to end. A `wss://` URL (behind a reverse proxy) works
too.

### Relay

The gateway's relay agent, while a relay is set, holds a WebSocket to
`wss://<relay>/agent?th=<sha256(token)>` and speaks alma-relay's
protocol 3:

- **Register** with `tokenHash`, `name` (the machine's), `proto: 3`, and
  `key` when the relay wants one (`RELAY_REGISTRATION_KEY`, or a relay
  token from `RELAY_TOKEN_SECRET`).
- **`wopen`** for `/__gorex_e2e__`: answer `wopened`, and hand the
  bridged socket to the gateway as a connection on the channel.
  `wopen` for any other path gets `werr`; every `hreq` gets `403`. Only
  the relay itself could send those, so the relay operator can't reach
  the gateway's API.
- **Flow control**: count the bytes sent on each stream and wait at
  `window` until the relay's `ack`.
- **Keepalive**: a WebSocket ping every 30 s; any frame from the relay
  counts as alive (protocol, Flow control). Cloudflare answers the pings
  without waking the Durable Object, so an idle machine costs nothing.
- **Reconnect** with backoff after a drop. Close code `4000` (another
  agent took the token) stops it: a second machine with a copy of this
  one's data directory. Status says so.

The routing token is 256 random bits in `rexapi/relay.json`, with the
relay URL and key. The relay sees only its hash; the phone has it from
the QR code. Who knows the token can try to handshake, and fails without
a paired key. Who knows its hash (the relay operator) can register in
the gateway's place and cut it off, but not read or answer it.

Settings › Remote › Relay: the relay's URL, its key, and the state
(connected, or why not). `gorex rexapi relay set URL [--key-file F]`,
`relay off`, `relay status`. The key is saved (mode 0600), as the agent
needs it at every reconnect, and never logged.

### Gateway

A channel connection is a `net.Conn` with a peer key, from two sources
(the local network listener's `/__gorex_e2e__` upgrade, and the relay
agent), fed to one `net.Listener` the gateway serves like its others.
`http.Server.ConnContext` keeps the peer key, and `identify` gives the
caller:

```go
{"kind": "device", "transport": "e2e", "display": "<device name>",
 "device": "<key>", "path": ["relay"] /* or ["direct"] */}
```

A paired device is the owner's, as the Tailscale user who signed the
gateway in is: same access, and its sessions count as yours (Desktop
terminals). The gateway runs while the local network listener, built-in
Tailscale or a relay is on.

Each handshake and each refusal (unknown key, bad pairing code) is in
the server's log with the device's name or key.

### Engine

Rex Go reaches the channel through the Go engine, as it does the
tailnet: `ios/relay`, beside `ios/tailnet` in the same framework (the
build script binds both packages; the framework becomes
`Engine.xcframework`). It needs no tailnet and runs whether built-in
Tailscale is on or not.

| Call | Does |
| --- | --- |
| `Start(privateKey, hostsJSON)` | starts the proxy for the paired hosts |
| `SetHosts(hostsJSON)` | after a pairing, a removal or an edit |
| `Pair(hostJSON, code, name)` | the pairing handshake; returns the phone's key on success |
| `Proxy() string` | the SOCKS5 proxy: address and password |
| `Stop()` | ends the proxy and its channels |

The proxy is the tailnet's pattern (ios-tailnet.md, Transport):
`tailscale.com/net/socks5` on loopback, a random password per run. A
paired host's address in Rex Go is `http://<id>.rexgo.invalid`, where
`id` is a short hash of the gateway's key. The proxy maps the name to
the host's routes, opens a WebSocket on the first one that works,
handshakes, and pipes the channel to `URLSession`. A name it doesn't
know is refused; it dials nothing else.

Routes, in order: the direct URL, then the relay. A route that fails
moves to the next; the last one that worked goes first next time, so at
home the local address wins and away the relay does.

### Hosts

`Transport` gets a third case, `.relay`, and `HostConfig` an optional
`pairing: Pairing?` (gateway key, direct URL, relay URL and token):
optional for the same decoding reason as `transport`. The phone's
private key stays in the Keychain, never in `HostConfig`.

Scanning a `rexgo://pair` code (Add Host, or the camera app) pairs and
adds the host; an expired code says so. The host's details show its
routes and let the user remove the pairing. Removing the host on the
phone doesn't revoke it on the gateway; the desktop's list does.

`HostConfig.reach` stays the one place that picks the transport: a host
with a pairing uses `.relay`.

### Lifecycle

The channel has no state of its own beyond its WebSocket: a network
change or a suspension ends it, and the host reconnects as any host
does. Back in the foreground after more than 5 s away, the app calls
`resume()` on every relay host, as on tailnet hosts (ios-tailnet.md,
Lifecycle): the reconnect token keeps the sessions on the server.

The engine sends a WebSocket ping every 30 s on each channel to the
relay, so idle terminals keep their channel while the app is in front.

### Security

- **Relay operator** sees the token's hash, sizes and timing, and the
  phone's address. It can't read or answer anything: it never has a
  key. It can cut the gateway off (above).
- **Network on the way** (direct without TLS) sees the same, less the
  token.
- **Stolen phone**: remove it on the desktop.
- **Leaked data directory**: `devices reset`.
- **Replay** of a handshake gets nowhere: Noise IK's responder needs the
  initiator's ephemeral key to finish. A replayed pairing code is spent.
- **Forward secrecy**: each channel's keys come from fresh ephemeral
  keys. The first handshake message, which carries no secret here, is
  readable by whoever later gets the gateway's static key.

## Build

`ios/scripts/build-tailnet` becomes `build-engine` and binds
`./ios/tailnet ./ios/relay`. The relay side adds `internal/e2e` and
`controlbase`, already in the binary through `tsnet`. The TestFlight
workflow's cache key and `paths` add `internal/e2e/**` and `ios/relay/**`.

## Test plan

- `internal/e2e`: handshake, pairing (good, wrong, used and expired
  codes), refusal of an unknown key, revocation closing an open channel.
- The relay agent against a fake relay in Go: register, `wopen`,
  refused `hreq` and other paths, flow control at `window`, close
  `4000`, reconnect.
- End to end on the Mac mini: alma-relay's Node edition at a pinned
  version (`npx github:yetone/alma-relay#<tag>`), the gateway's agent,
  and Rex Go in the simulator: pair, open a terminal, background and
  foreground, revoke.
- Direct: the simulator to the gateway's listener with no network
  allowed: the channel gets in, plain HTTP gets 403.
- Rex Go's host list saved by the current version decodes after the
  update.
- On a phone: a relay on Cloudflare Workers, cellular only, Wi-Fi ↔
  cellular with a terminal open.

## Phases

1. **Channel and devices**: `internal/e2e`, the gateway's channel
   listener on the local network listener (direct), `devices.json`,
   pairing and revoking from the command line.
2. **Relay agent**: protocol 3, settings and status, the fake relay,
   the test against alma-relay.
3. **Engine and hosts**: `ios/relay`, the framework rename, the
   `.relay` transport, pairing by QR code, lifecycle.
4. **Desktop settings**: Rex Go devices (pair, list, remove) and the
   relay's card in Settings › Remote.
5. **Docs**: `docs/rex-api.md` gets Direct and Relay sections; setup
   steps for alma-relay on Cloudflare and on Docker.
