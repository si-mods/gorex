# Rex Go on a self-hosted tailnet

Status: proposed. Answers the rest of
[#330](https://github.com/si-mods/gorex/issues/330), after
[built-in Tailscale](ios-tailnet.md).

## Problem

#330 asks that Rex Go reach a GoRex machine with no VPN on the phone, in
two cases: directly, when the machine has a public address, and through
a relay the user runs, when it has none. Both encrypted end to end, with
devices that can be paired and revoked one by one, and reconnects that
keep the running sessions.

Built-in Tailscale (#369–#382) already does most of this:

| #330 asks | Built-in Tailscale |
| --- | --- |
| no VPN on the phone | Tailscale runs inside Rex Go, for Rex Go's connections only |
| end-to-end encryption | WireGuard, between the phone and the gateway |
| direct to a public address | WireGuard goes direct whenever a path exists, public address or not |
| through a relay | DERP, when no direct path exists |
| a credential per device | each device's WireGuard key, known to the control server |
| revoke one device | remove its node from the control server |
| keep sessions across reconnects | `Host.resume()` after a suspension or a network change |

What it lacks is the "self-hosted" half. The control server and the
relays are Tailscale's, unless the user runs Headscale, and the gateway
can't serve a Headscale tailnet: it serves the tailnet only over HTTPS,
on port 443, and Headscale issues no certificates. `ListenTLS` then
fails, and built-in Tailscale turns off with the error
(ios-tailnet.md, Non-goals).

## First principles

A connection to a terminal needs four things: a way to find the
machine, a path to it, keys that encrypt the path, and a check of who is
on the other end. Every design for #330 has to supply each once.

Tailscale supplies all four, and GoRex already links it on both ends:
the gateway's device, and the engine in Rex Go. A second design (an
end-to-end channel of GoRex's own, its own pairing, its own relay, as an
earlier draft of this document proposed) would supply all four again:
a second key per device, a second list of devices, a second proxy in the
app, a second relay to run. Two systems that do the same thing, to
secure, test and explain twice.

So this design adds no second system. It removes what stops the one
GoRex has from running on a tailnet the user hosts:

- **Find** is the control server, Tailscale's or Headscale. Headscale is
  one binary or container.
- **Path** is WireGuard, direct when it can be. The relay is DERP, which
  Headscale serves itself (`derp.server.enabled`): the "self-hosted
  relay" of #330 is the same container.
- **Keys** are WireGuard's: per device, made on the device, never on the
  server.
- **Who** is the gateway's `identify`, by `WhoIs` over the tailnet.

TLS on top of WireGuard adds nothing to any of the four: the tailnet
already encrypts each connection and names its peer. HTTPS on the
tailnet exists so that browsers and Rex's own app, which expect it, get
a certificate. It shouldn't be a requirement.

## Goal

- The gateway serves the tailnet over plain HTTP too, so it works on a
  tailnet without certificates.
- Rex Go joins a Headscale tailnet by scanning one QR code.
- The docs show how to host the whole thing: Headscale with its own
  DERP, on a VPS or on the GoRex machine itself.

## Non-goals

- **A GoRex channel, pairing or relay of its own.** See above.
- **A relay on Cloudflare Workers.** DERP needs a long-lived TCP server
  and UDP for STUN, which Workers don't offer, so self-hosting takes a
  VPS (or any machine the phone can reach). This is what choosing one
  system costs; alma-relay, which runs on Workers, would bring the
  second system with it.
- **No control server at all.** WireGuard needs the peers' keys and
  addresses, and someone has to hand them out. A machine with a public
  address can run Headscale itself.
- **Rex's own iPhone app.** It reaches a Headscale tailnet only through
  the Tailscale app's VPN, as today; the plain HTTP address works for it
  too.
- **Changing the local network listener.** It stays for Rex's app
  without Tailscale. Rex Go on the tailnet doesn't need it: at home,
  WireGuard finds the direct path on the local network by itself.

## Design

### Gateway: plain HTTP on the tailnet

While built-in Tailscale is on, the gateway listens on two ports of its
tailnet address:

- **80, plain HTTP**, always, as soon as the device runs.
- **443, HTTPS**, as today, when the tailnet issues certificates.

Both serve the same handler, and both check each caller with
`identify`: who gets in doesn't change. A failed `ListenTLS` no longer
turns built-in Tailscale off: it is logged, and `tailscale status` shows
`https: unavailable (<why>)` while HTTP serves.

`tailnode.Node` gets `Listen(port)` beside `ListenTLS`; `tailnetNode`
in `internal/rexapi` gets it too, and the test's fake.

The pairing address (`gorex rexapi pair`, Rex QR…) is the HTTPS one when
it is ready, as today, and otherwise `http://<device>.<suffix>`, the
MagicDNS name. On Tailscale's tailnets nothing changes.

Is a plain HTTP port on the tailnet safe? It is as safe as the HTTPS
one: the tailnet is the only way to the port, WireGuard encrypts and
authenticates it, and `identify` is the same. Tailscale's own services
(`tailscale serve` with HTTP, the LocalAPI peer API) rely on the same.

### Rex Go: HTTP hosts on the tailnet

Rex Go needs no change to reach the HTTP address: the engine's proxy
dials any port on a tailnet peer, `URLSession` takes `http` (Rex Go
allows arbitrary loads already), and `HostConfig.reach` picks `.tailnet`
for a name under the MagicDNS suffix whatever the scheme. A test pins
that an `http://` host on the suffix reaches the engine.

### Joining by QR code

On Tailscale's own tailnet the phone signs in with the user's account
(ios-tailnet.md, Settings). On Headscale that sign-in ends at a page
that asks an administrator to run `headscale nodes register`: no way to
pair a phone.

So the desktop can show a code that does both at once:

```
rexgo://join?control=<control URL>&key=<auth key>&host=<pairing address>
```

Rex Go, given it (QR scanner, or the camera app through the `rexgo`
scheme it already registers), turns built-in Tailscale on with that
control server, starts the engine with the auth key (the engine's
`Start` takes one already, used once and never saved), and adds the
host. One scan: the phone is on the tailnet and at the machine.

The auth key is one-time and short-lived. The desktop mints it from the
control server's API, when it has a credential for it:

- **Headscale**: an API key (`headscale apikeys create`). The gateway
  calls `POST /api/v1/preauthkey` for the user its own device belongs
  to, not reusable, expiring in 10 minutes.
- **Tailscale**: an API access token. The gateway calls
  `POST /api/v2/tailnet/-/keys` for a key that is not reusable, not
  tagged, preauthorized, expiring in 10 minutes. Not an OAuth client:
  its keys must carry tags, and `identify` refuses tagged devices.

The key is untagged and belongs to the same user as the gateway's
device, so `identify` lets the phone in with no extra rule. The API
credential is saved with the gateway's settings (mode 0600) and never
logged or shown again. Without one, the desktop shows the plain address
code, as today, and the phone signs in by hand.

Settings › Remote › Rex QR… gets **Include a sign-in key** when a
credential is set; `gorex rexapi pair --join` does the same. A code that
holds a key says so, and how long it is good for.

### Revoking

A phone is a node of the tailnet. Removing it (Tailscale's admin
console, `headscale nodes delete`) ends its WireGuard sessions, and its
key is refused from then on; nothing else changes. With an API
credential set, Settings › Remote could list the user's `rexgo-*` nodes
with a Remove button; that is later work, not this design.

### Hosting it

`docs/rex-api.md` gets a section for a self-hosted tailnet:

1. **Headscale** on a VPS, or on the GoRex machine itself when it has a
   public address: the container, a domain with HTTPS for Headscale
   itself (Caddy), `dns.magic_dns: true` and a `base_domain`.
2. **Its DERP**: `derp.server.enabled: true` and, to rely on no one
   else, `derp.urls: []`. Ports 443/TCP and 3478/UDP open.
3. The GoRex machine: `gorex rexapi tailscale on --control-url
   https://headscale.example.com`, then `headscale nodes register` once
   for the gateway's device, or an auth key.
4. Rex Go: scan the join code.

The data goes direct between the phone and the machine whenever NAT
lets it (the usual case with a public address), and through the
Headscale box's DERP otherwise.

## Test plan

- `internal/rexapi`: with a fake node whose `ListenTLS` fails, the
  gateway serves HTTP, stays on, and reports HTTPS unavailable; with
  both, both serve and both `identify`; the pairing address falls back
  to HTTP.
- The Headscale and Tailscale key requests against fake servers:
  request shape, expiry, refusal of a tagged result, errors shown.
- End to end on the Mac mini: Headscale with its DERP in a container,
  the gateway joined to it, Rex Go in the simulator joins by the code,
  opens a terminal, backgrounds and foregrounds.
- On a phone: cellular only, the Headscale box's DERP the only relay;
  Wi-Fi ↔ cellular with a terminal open.

## Phases

1. **Gateway HTTP on the tailnet**: port 80, `ListenTLS` no longer
   fatal, the pairing address. Rex Go on Headscale works from here,
   with sign-in by hand.
2. **Join code**: the API credential, minting the key, `rexgo://join`
   in Rex Go.
3. **Docs**: hosting Headscale with its DERP.
