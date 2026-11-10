# Rex Go over tailcat

Status: proposed. Answers the rest of
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
- the coordinator (`gcoord`) for many machines;
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
- **Joining the coordinator's network from the phone.** A phone pairs
  with each machine. Being a member could come later; nothing here
  stands in its way.
- **A relay of GoRex's own.** `derper` is the relay.

## Design

### Server: the gateway on the tailcat address

The session server's tailcat server listens on a second port, Rex's
API port 7297, beside `RemotePort` (7464). The allowlist is the
server's, so an unknown key gets nothing on either port; pairing stays
on `RemotePort`.

The gateway is a separate process and keeps no tailcat of its own: one
address per machine. It takes these connections from the session server
the way a client's dial already goes through it (`remote.tailcatDial`,
`relayTailcat`), in the other direction: the gateway asks the session
server, on its socket, to accept for port 7297
(`remote.tailcatAccept`), and gets each connection as a stream on its
local socket, first a line with the peer's key and name, then the
bytes. The local socket is this user's only, so the line can be trusted.

The gateway serves these streams with its usual handler, and
`identify` gives:

```go
{"kind": "device", "transport": "tailcat", "display": "<client name>",
 "device": "<key>", "path": ["tailcat"]}
```

What a paired client may do on the gateway is Rex's API, layout
included, as the Tailscale user who signed the gateway in may. That is
more than a desktop may do on `RemotePort` (sessions only, no layout).
A desktop already has the same trust in practice (an allowed key runs
any program as this user, README), so the line is a convention, not a
boundary; but it moves. Settings shows which clients are phones.

The gateway runs while remote access is on, as it does while the local
network listener or built-in Tailscale is.

### Server: the relay in the address

Remote access today picks a region of tailcat's default DERP map. It
gets a setting, **Relay**: default (tailcat's), or a host of one's own,
`derp.example.com`. Given one, the session server sets `Server.Region`
to a region of that one node, and the address embeds it: clients need
no setting and never fetch a DERP map. Changing the relay changes the
address (the keys stay), so paired clients take the new one from a new
QR code.

Self-hosting is then `derper --hostname=derp.example.com` on any VPS,
with 443/TCP and 3478/UDP open. The relay sees keys and ciphertext only.

### Pairing a phone

Settings › Remote › Remote Hosts › **Rex QR…** shows, while remote
access is on, the tailcat address in a `rexgo://` code and opens the
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

| Call | Does |
| --- | --- |
| `SetTailcatHosts(json)` | the paired hosts: id, address; one `tailcat.Client` each |
| `TailcatPair(address, name) string` | the pairing request; returns the fingerprint |
| `TailcatKey() string` | this phone's public key, for Settings |

The client key is passed in from the Keychain at start, never written
by Go.

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
reset after a suspension rebinds it. The reset reaches the WireGuard
device and `magicsock` sockets through unexported fields today; for
tailcat the fields differ, so the first phase measures whether the
reset carries over or tailcat needs a hook upstream (tailcat is
Tailscale's, open to one). Then `Host.resume()`, which keeps the
sessions on the server.

## Size

tailcat links `magicsock`, WireGuard and `netstack`, which the engine
has already. Phase 1 measures the Release binary with and without it.

## Test plan

- `internal/rex`: port 7297 accepts allowed keys only; `remote.tailcatAccept`
  hands a stream with the right key; Remove closes a phone's gateway
  stream; the relay setting puts the region in the address.
- `internal/rexapi`: a tailcat stream's `identify`; a refused key gets
  nothing.
- End to end on the Mac mini: Rex Go in the simulator scans the code,
  pairs, opens a terminal, backgrounds and foregrounds, is removed. Once
  through tailcat's relays, once through a `derper` in a container.
- On a phone: cellular only, no Tailscale anywhere; Wi-Fi ↔ cellular
  with a terminal open.

## Phases

1. **Spike**: a tailcat client in the engine, a host reached through
   the proxy in the simulator, the socket reset after a suspension, the
   size.
2. **Server**: port 7297 on the tailcat address, `remote.tailcatAccept`,
   the gateway's tailcat streams, the relay setting.
3. **Rex Go**: the `.tailcat` transport, the `rexgo://tailcat` code,
   pairing, the client key, lifecycle.
4. **Docs**: `docs/rex-api.md` and the README's Remote access: Rex Go
   over tailcat, and a `derper` of one's own.
