# Rex Go over tailcat

Status: phases 1 (Spike), 2 (Server) and 3 (Rex Go) done; phase 4 proposed. Answers the rest of
[#330](https://github.com/si-mods/gorex/issues/330), after
[built-in Tailscale](ios-tailnet.md).

## Problem

#330 asks that Rex Go reach a GoRex machine with no VPN on the phone, in
two cases: directly, when the machine has a public address, and through
a relay the user runs, when it has none. Both encrypted end to end, with
devices paired and revoked one by one, and reconnects that keep the
running sessions.

Built-in Tailscale (#369–#382) covers the case of a user who has a
tailnet. It needs a control server, Tailscale's or Headscale, and the
gateway serves the tailnet only over HTTPS, which Headscale can't give
it. Earlier drafts of this document tried to fill the gap from there: a
second end-to-end channel with its own pairing and an alma-relay agent,
then plain HTTP on a Headscale tailnet with a join code. Each added
machinery that GoRex already has.

## First principles

To reach a terminal, a phone needs four things, each exactly once:

1. **Trust**: the phone knows the machine's key, the machine knows the
   phone's, and the machine can forget it again.
2. **Encryption** of everything between the two keys.
3. **A path**: direct when the network allows it, through a relay when
   it doesn't.
4. **Rendezvous**: a place where the two find each other, when neither
   can be dialed.

A control server (Tailscale, Headscale) exists to hand out keys (1)
between many machines that never met. A phone and a computer on the
same desk don't need one: **the QR code is the control plane**. One
scan moves the machine's key to the phone, and the phone's key can come
back the same way.

Then:

- (2) and (3) are WireGuard and `magicsock`: direct through NAT when it
  can, DERP when it can't.
- (4) is a DERP server, which addresses peers by their keys. It knows
  nothing else: no accounts, no users, no state on disk. Tailscale's
  `derper` is one binary.

That is exactly **tailcat**: Tailscale's data plane without its control
plane, the address carrying the server's key, a pre-shared key and the
relay to meet at.

And GoRex already runs it. The session server listens on a tailcat
address (README, Remote access), with:

- a client key per machine (`remote-client.json`);
- an allowlist of keys, checked on every connection, with refusal
  before close;
- pairing: a five-minute window, a request with a name, a fingerprint
  compared on both screens, Approve;
- revocation: Remove closes that key's connections at once;
- the free tailcat DERP relays, or the user's own.

The desktop apps reach each other this way today. Rex Go is just one
more client that doesn't speak it yet.

## Goal

Rex Go becomes a tailcat client of the GoRex machine, like a GoRex
desktop:

- **One address per machine.** The session server's tailcat address
  serves the gateway too. No second listener, port forward or
  certificate.
- **One trust system.** The phone has a client key like a desktop's,
  pairs through the same window and fingerprint, shows in the same
  allowlist, and is removed with the same button.
- **One relay.** The DERP region in the address: tailcat's free relays
  by default, or the user's own `derper`, written into the address so
  the phone needs no setting.

No control server, no account, no certificate, no new crypto, no new
protocol.

| #330 asks | Here |
| --- | --- |
| no VPN on the phone | tailcat runs in Rex Go's process, for its connections only |
| direct to a public address | `magicsock` goes direct whenever a path exists |
| a self-hosted relay | one `derper`, named in the address |
| end-to-end encryption | WireGuard, with the address's pre-shared key |
| pair, revoke one device | the remote allowlist, as for desktops |
| keep sessions across reconnects | `Host.resume()`, as on the tailnet |

## Non-goals

- **Replacing built-in Tailscale.** It stays for users who have a
  tailnet, and for Rex servers, which have no tailcat address.
- **Rex's own iPhone app.** It knows no tailcat; it keeps the local
  network and the tailnet.
- **A relay of GoRex's own.** `derper` is the relay.

## Design

### Server: the gateway on the tailcat address

The session server's tailcat server listens on a second port, Rex's
API port 7297, beside `RemotePort` (7464). The allowlist is the
server's, so an unknown key gets nothing on either port; pairing stays
on `RemotePort`. Rex's API commands the layout, as a client may on
`RemotePort` too, so 7297 lets in an allowed key.
Remove closes its 7297 connections.

The gateway is a separate process and keeps no tailcat of its own: one
address per machine. It takes these connections from the session server
the way a client's dial already goes through it (`remote.tailcatDial`,
`relayTailcat`), in the other direction: the gateway listens on a
socket of its own, `rexapi-tailcat.sock` in the data directory, and the
session server dials it for each connection to 7297, writes a line with
the peer's key and name, then splices the bytes. The socket is this
user's only, so the line can be trusted.

The gateway serves these streams with its usual handler, and
`identify` gives:

```go
{"kind": "device", "transport": "tailcat", "display": "<client name>",
 "path": ["tailcat"]}
```

The key stays out of it: Rex's principal allows no member for it, and a
client that decodes one strictly would refuse it.

With no gateway on the socket, as while it starts, the connection closes
at once and its client dials again. `server.status` leaves this
listener out: its address is a secret.

What a paired client may do on the gateway is Rex's API, layout
included, as the Tailscale user who signed the gateway in may. That is
more than a desktop may do on `RemotePort` (sessions only, no layout).
A desktop already has the same trust in practice (an allowed key runs
any program as this user, README), so the line is a convention, not a
boundary; it is drawn where a desktop may command the layout over
`RemotePort`.

The gateway runs while remote access is on, as it does while the local
network listener or built-in Tailscale is.

### Server: the relay in the address

Remote access picks a region of tailcat's default DERP map once, and
keeps it (`remote.json`, `region`). It has a setting, **Relay**: default
(tailcat's), or a host of one's own, `derp.example.com`, with `:PORT`
when not 443, by name and not IP address, as `derper --hostname` serves
a certificate for a name (Settings › Remote, `gorex remote relay
HOST|default`, `remotectl relay HOST|default`; `remote.relaySet` on the
local socket). Given
one, the session server sets `Server.Region` to a region of that one
node (DERP on its port, STUN on 3478), and the address embeds it:
clients need no setting and never fetch a DERP map. The picked region
stays saved, so clearing the relay gives the old address back. Changing
the relay changes the address (the keys stay), so paired clients take
the new one from a new QR code.

Self-hosting is then `derper --hostname=derp.example.com` on any VPS,
with 443/TCP and 3478/UDP open. The relay sees keys and ciphertext only.

### Pairing a phone

Settings › Remote › Devices › **Rex Go QR…** shows, while remote
access listens, the tailcat address in a `rexgo://` code and opens the
pairing window:

```
rexgo://tailcat?a=<tailcat address>&n=<machine name>
```

The address is a secret (README): the code is shown, never logged or
copied, and the dialog says so.

Rex Go, given the code (Add Host's scanner, or the camera app through
the `rexgo` scheme it registers already):

1. makes its client key if it has none (Keychain, this device only);
2. asks to pair with its device name (`rexgo-xxxx`, ios-tailnet.md);
3. shows the fingerprint; the desktop shows the request with the same
   one; the user approves there, as for a desktop;
4. adds the host, which connects as soon as the key is allowed.

Revoking is the allowlist's Remove: the phone's connections close, its
key is refused, nothing else changes. Removing the host on the phone
doesn't revoke it.

### Rex Go: the engine

`ios/tailnet` gains a tailcat client beside the tailnet node, in the
same framework: tailcat is built on the same `tailscale.com` packages,
so it adds little. One SOCKS5 proxy on loopback serves both (ios-tailnet.md,
Transport): a name under `.tailcat.invalid` dials that host's tailcat
address on port 7297 with `tailcat.Client.DialTCPPort`; tailnet names
dial the tailnet as today; anything else is refused.

The proxy is one per process, not per run of the tailnet node, so it
serves tailcat hosts with the node stopped. iOS can reclaim a suspended
app's listening socket (TN2277); a proxy whose listener died is replaced
by the next `Proxy()`, of a new port and password, and the hosts' next
session goes through it.

| Call | Does |
| --- | --- |
| `SetTailcatKey(key)` / `NewTailcatKey() string` | the client key from the Keychain; a new one for it |
| `SetTailcatHosts(json)` | the paired hosts: id, address |
| `TailcatPair(address, name) string` | the pairing request; returns the fingerprint |
| `TailcatKey() string` | this phone's public key, for Settings |

There is one `tailcat.Client` per address, not per host id: two clients
of one key take each other's packets (#212), as a machine added twice
would have. As `internal/rex`'s pool, it counts its open connections and
closes after 3 minutes with none, so an idle phone holds no DERP
connection. Pairing a machine not listed yet uses a client of its own,
for that request only.

The client key is passed in from the Keychain at start, never written
by Go.

A tailcat dial has its own bounds, as `internal/rex`'s: 15 s in all,
and 3 s for a try of a client that connected before. The SOCKS server's
5 s would cut a client's first dial, which connects to DERP and shakes
hands.

tailcat's `createEngine` turns `netns` off for the whole process, so
once a tailcat client starts the tailnet node no longer binds its
sockets to the default interface: the system's routes pick. That is
harmless without a VPN; with a system VPN on (the Tailscale app, say),
the node's traffic could enter it. Unverified; an upstream ask could
make it optional.

`URLSession` reaches a tailcat host at `http://<id>.tailcat.invalid`:
plain HTTP inside WireGuard, like the gateway's own streams. Rex Go
allows arbitrary loads already.

### Rex Go: hosts

`Transport` gets `.tailcat`; `HostConfig` an optional `tailcat`
(address, machine name), optional for the decoding reason the transport
is. `HostConfig.reach` stays the one place that picks: a host with a
tailcat address uses it, whether or not built-in Tailscale is on.

### Lifecycle

The tailcat client is `magicsock`, so the phone's network and
suspension hit it as they hit the tailnet node (ios-tailnet.md,
Lifecycle): the path monitor's news goes to it too, and the socket
reset after a suspension rebinds it. The reset carries over with no
hook upstream: a tailcat client's `lb.sys` is the same `tsd.System` as
tsnet's `Server.Sys()`, reached the same way, through unexported fields
whose tests fail when they move. Should they, the smallest upstream ask
is `(*Client).Sys()`. Then `Host.resume()`, which keeps the sessions on
the server.

A machine whose server restarted, as on an upgrade, forgot the client,
and the client still trusts its old direct path and WireGuard session:
left alone, it waits for WireGuard's rekey, 15 s and more. So a dial
does as `internal/rex`'s: a client that connected before and fails its
try pings the server and tries once more; failing again, with no other
connection on it, it is dropped and a new client dials, within the
15 s. The socket resets of a suspension run side by side, one per
client and the node's, so a stuck one holds up no other.

## Size

tailcat links `magicsock`, WireGuard and `netstack`, which the engine
has already. Measured on Rex Go's Release device binary, stripped, with
and without it: +0.26 MB installed, +0.11 MB compressed (gzip -9).

## Test plan

- `internal/rex`: port 7297 accepts allowed keys only; the gateway's
  socket gets a stream with the right key; Remove and remote off close a
  phone's gateway stream; the
  relay setting puts the region in the address.
- `internal/rexapi`: a tailcat stream's `identify`; a refused key gets
  nothing.
- End to end on the Mac mini: Rex Go in the simulator scans the code,
  pairs, opens a terminal, backgrounds and foregrounds, is removed. Once
  through tailcat's relays, once through a `derper` in a container.
- On a phone: cellular only, no Tailscale anywhere; Wi-Fi ↔ cellular
  with a terminal open.

## Phases

1. **Spike** (done): a tailcat client in the engine, a host reached
   through the proxy (Go tests, against a local DERP; not yet in the
   simulator), the socket reset after a suspension, the size.
2. **Server** (done): port 7297 on the tailcat address, the gateway's
   socket for its streams, the relay setting.
3. **Rex Go** (done): the `.tailcat` transport, the `rexgo://tailcat`
   code, pairing, the client key, lifecycle. Findings:
   - The button sits in Devices, beside Pair New Device…, as Rex QR…
     already names the tailnet's and the local network's code. Its
     dialog shows the image only: no address, no Copy.
   - Rex Go registered no URL scheme before; it now registers `rexgo`.
     The Tailscale login's `ASWebAuthenticationSession` keeps its own
     callback, which its page never calls.
   - A host's URL is `http://<id>.tailcat.invalid:7297`, the id its
     UUID; the port is Rex's API's, as the proxy dials the port asked.
   - The paired host is added at once and retries every 3 s until the
     key is allowed, for 5 minutes, the pairing window; Cancel takes it
     back.
   - With built-in Tailscale off, the path monitor runs while a host is
     paired over tailcat, and the foreground's socket reset runs too.
   - The address is kept in the app's UserDefaults with the host: it
     lets in nothing the machine has not approved, and the client key,
     which it has, is in the Keychain.
   - End to end in the simulator on the Mac mini, through tailcat's
     relays: pair, approve, sessions listed, background and foreground,
     revoke. Not yet: opening a terminal, a `derper` of one's own, a
     phone.
4. **Docs**: `docs/rex-api.md` and the README's Remote access: Rex Go
   over tailcat, and a `derper` of one's own. The server's side and the
   relay are written with phase 2; Rex Go's steps follow phase 3.
