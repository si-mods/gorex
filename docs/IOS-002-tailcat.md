---
name: IOS-002
description: "Rex Go connects to a GoRex machine as a tailcat client, as a desktop does. Use when you change tailcat on iOS or the gateway on the tailcat address."
metadata:
  created: "2026-11-10"
  updated: "2026-11-22"
---

# IOS-002: Rex Go over tailcat

Status: built (#396, #398, #400, #507).

This doc answers the rest of [#330](https://github.com/si-mods/gorex/issues/330), after [built-in Tailscale](IOS-001-tailnet.md). It makes Rex Go a tailcat client of the GoRex machine, as a GoRex desktop is. The README ("Remote access") and `docs/REF-005-rex-api.md` describe the result, with a `derper` of one's own.

## Problem

#330 asks that Rex Go reach a GoRex machine with no VPN on the phone, in two cases. In the first case, the machine has a public address and the phone connects directly. In the second case, the machine has no public address and the phone connects through a relay that the user runs. #330 also asks for these properties in both cases:

- Everything is encrypted end to end.
- The user pairs and revokes devices one by one.
- A reconnect keeps the running sessions.

Built-in Tailscale (#369 to #382) covers the case of a user who has a tailnet. It needs a control server, either the server of Tailscale or Headscale. The gateway serves the tailnet only over HTTPS, which Headscale cannot give it. Earlier drafts of this document tried to fill the gap from there. One draft added a second end-to-end channel with its own pairing and an alma-relay agent. A second draft used plain HTTP on a Headscale tailnet with a join code. Each draft added machinery that GoRex already has.

## First principles

To reach a terminal, a phone needs four things. Each thing must exist exactly once:

1. **Trust**: the phone knows the key of the machine, the machine knows the key of the phone, and the machine can forget the key again.
2. **Encryption** of everything between the two keys.
3. **A path**: direct when the network allows it, through a relay when it does not.
4. **Rendezvous**: a place where the two find each other, when neither can be dialed.

A control server (Tailscale, Headscale) exists to hand out keys (1) between many machines that never met. A phone and a computer on the same desk do not need a control server. **The QR code is the control plane.** One scan moves the key of the machine to the phone. The key of the phone can come back the same way.

Then:

- (2) and (3) are WireGuard and `magicsock`. They go direct through NAT when they can, and use DERP when they cannot.
- (4) is a DERP server, which addresses peers by their keys. It knows nothing else. It has no accounts, no users and no state on disk. `derper` of Tailscale is one binary.

That is exactly **tailcat**: the data plane of Tailscale without its control plane. The address carries the key of the server, a pre-shared key and the relay where the two meet.

GoRex already runs tailcat. The session server listens on a tailcat address (README, Remote access). It has these parts:

- a client key for each machine (`remote-client.json`).
- an allowlist of keys. The server checks it on every connection and refuses before close.
- pairing: a window of five minutes, a request with a name, a fingerprint that both screens show for comparison, and Approve.
- revocation: Remove closes the connections of that key at once.
- the free tailcat DERP relays, or the relay of the user.

The desktop apps reach each other this way today. Rex Go is one more client. Before this work it did not speak tailcat.

## Goal

Rex Go becomes a tailcat client of the GoRex machine, as a GoRex desktop is:

- **One address for each machine.** The tailcat address of the session server serves the gateway too. The machine has no second listener, no port forward and no certificate.
- **One trust system.** The phone has a client key like the key of a desktop. It pairs through the same window and fingerprint. It shows in the same allowlist. The user removes it with the same button.
- **One relay.** The DERP region is in the address. By default it is one of the free relays of tailcat. The user can use a `derper` of their own. GoRex writes the relay into the address, so the phone needs no setting.

The design adds no control server, no account, no certificate, no new crypto and no new protocol.

| #330 asks | Here |
| --- | --- |
| no VPN on the phone | tailcat runs in the process of Rex Go, for its connections only |
| direct to a public address | `magicsock` goes direct whenever a path exists |
| a self-hosted relay | one `derper`, named in the address |
| end-to-end encryption | WireGuard, with the pre-shared key of the address |
| pair, revoke one device | the remote allowlist, as for desktops |
| keep sessions across reconnects | `Host.resume()`, as on the tailnet |

## Non-goals

- **Replacing built-in Tailscale.** It stays for users who have a tailnet. It also stays for Rex servers, which have no tailcat address.
- **The iPhone app of Rex.** It does not know tailcat. It keeps the local network and the tailnet.
- **A relay of GoRex's own.** `derper` is the relay.

## Design

### Server: the gateway on the tailcat address

The tailcat server of the session server listens on a second port. This port is the API port of Rex, 7297. It is beside `RemotePort` (7464). The allowlist belongs to the server. An unknown key gets nothing on either port. Pairing stays on `RemotePort`. Rex's API commands the layout, as a client may on `RemotePort` too. So 7297 lets in an allowed key. Remove closes the 7297 connections of that key.

The gateway is a separate process. It keeps no tailcat of its own, so each machine has one address. The gateway takes these connections from the session server. This is the same way that the dial of a client already goes through the session server (`remote.tailcatDial`, `relayTailcat`), in the other direction. The gateway listens on a socket of its own, `rexapi-tailcat.sock` in the data directory. The session server dials this socket for each connection to 7297. It writes a line with the key and the name of the peer, then splices the bytes. The socket belongs to this user only, so the gateway can trust the line.

The gateway serves these streams with its usual handler. `identify` gives:

```go
{"kind": "device", "transport": "tailcat", "display": "<client name>",
 "path": ["tailcat"]}
```

The key stays out of this value. The principal of Rex allows no member for it. A client that decodes the value strictly would refuse it.

Suppose no gateway is on the socket, as while the gateway starts. The connection then closes at once, and its client dials again. `server.status` leaves this listener out, because its address is a secret.

A paired client may do on the gateway what Rex's API allows, layout included. The Tailscale user who signed in the gateway may do the same. This is more than a desktop may do on `RemotePort` (sessions only, no layout). A desktop already has the same trust in practice, because an allowed key runs any program as this user (README). So the line is a convention, not a boundary. The line is drawn where a desktop may command the layout over `RemotePort`.

The gateway runs while remote access is on. It runs in the same way while the local network listener or built-in Tailscale is on.

### Server: the relay in the address

Remote access picks a region of the default DERP map of tailcat once, and keeps it (`remote.json`, `region`). It has a setting, **Relay**. The value is default (the relay of tailcat) or a host of one's own, such as `derp.example.com`, with `:PORT` when the port is not 443. The value is a name and not an IP address, because `derper --hostname` serves a certificate for a name. The user sets it in Settings › Server › Show Details, with `gorex remote relay HOST|default`, or with `remote.relaySet` on the local socket.

When the user gives a relay, the session server sets `Server.Region` to a region of that one node (DERP on its port, STUN on 3478). The address embeds this region. Clients need no setting and never download a DERP map. The picked region stays saved, so clearing the relay gives the old address back. Changing the relay changes the address, but the keys stay. Paired clients take the new address from a new QR code.

Self-hosting is then `derper --hostname=derp.example.com` on any VPS, with 443/TCP and 3478/UDP open. The relay sees keys and ciphertext only.

### Pairing a phone

Settings › Server › Devices › **Rex Go QR…** shows the tailcat address in a `rexgo://` code, while remote access listens. It also opens the pairing window:

```
rexgo://tailcat?a=<tailcat address>&n=<machine name>
```

The address is a secret (README). The dialog shows the code and says so. GoRex never logs the code or copies it.

Rex Go takes the code from the scanner of Add Host, from a picture of it that the user picks in the scanner, or from the camera app through the `rexgo` scheme that Rex Go already registers. Then Rex Go does these steps:

1. It makes its client key if it has none (Keychain, this device only).
2. It asks to pair with its device name (`rexgo-xxxx`, IOS-001-tailnet.md).
3. It shows the fingerprint. The desktop shows the request with the same fingerprint. The user approves on the desktop, as for a desktop.
4. It adds the host. The host connects as soon as the machine allows the key.

A machine with Reachable at on also pairs by its IP address (NET-002-host-links.md, Pairing by address). The user goes to Add Host › Set Up Manually, enters an IP address or IP:PORT, and selects Pair with GoRex. Both sides show a code. The user says that the codes match, and the machine approves. The phone then adds the host at the tailcat address that it got, as from a code.

Revoking is the Remove of the allowlist. The phone connections close, and the machine refuses its key. Nothing else changes. Removing the host on the phone does not revoke it.

### Rex Go: the engine

`ios/tailnet` gains a tailcat client beside the tailnet node, in the same framework. tailcat is built on the same `tailscale.com` packages, so it adds little. One SOCKS5 proxy on loopback serves both (IOS-001-tailnet.md, Transport):

- A name under `.tailcat.invalid` dials the tailcat address of that host on port 7297 with `tailcat.Client.DialTCPPort`.
- Tailnet names dial the tailnet as today.
- The proxy refuses anything else.

The proxy is one for each process, not one for each run of the tailnet node. So it serves tailcat hosts with the node stopped. iOS can reclaim the listening socket of a suspended app (TN2277). The next `Proxy()` replaces a proxy whose listener died. The new proxy has a new port and a new password. The next session of the hosts goes through it. The app keeps the address it read, though, and a suspension it has not resumed from leaves a dead one in place, so a dial through it fails at once. A failed dial has the engine read it again (`TailnetEngine.reproxy`), and `resume()` reads it before the socket reset's wait, so the next session goes through a live listener.

| Call | Does |
| --- | --- |
| `SetTailcatKey(key)` / `NewTailcatKey() string` | the client key from the Keychain. A new one for it |
| `SetTailcatHosts(json)` | the paired hosts: id, address |
| `TailcatPair(address, name) string` | the pairing request. It returns the fingerprint |
| `TailcatKey() string` | the public key of this phone, for Settings |

The engine has one `tailcat.Client` for each address, not for each host id. Two clients of one key take the packets of each other (#212). A machine that the user added twice would cause the same problem. As the pool of `internal/rex` does, the engine counts the open connections of a client. It closes the client after 3 minutes with none. An idle phone then holds no DERP connection. Pairing a machine that is not listed yet uses a client of its own, for that request only.

Go never writes the client key. The app passes the key in from the Keychain at start.

A tailcat dial has its own bounds, as the dial of `internal/rex` has. The bounds are 15 s in all, and 3 s for a try of a client that connected before. The SOCKS server has a limit of 5 s. That limit would cut the first dial of a client, which connects to DERP and shakes hands. The SOCKS server never cancels a dial when the app gives up the connection. It does not read the side of the app while it dials. So a dial ends at its 15 s, at a cancel by its caller, when its host leaves the list, or when the key changes.

The `createEngine` function of tailcat turns `netns` off for the whole process. After a tailcat client starts, the tailnet node no longer binds its sockets to the default interface. The routes of the system pick the interface. This is harmless without a VPN. With a system VPN on (the Tailscale app, for example), the traffic of the node could enter the VPN. This is unverified. An upstream ask could make it optional.

`URLSession` reaches a tailcat host at `http://<id>.tailcat.invalid`. This is plain HTTP inside WireGuard, like the own streams of the gateway. Rex Go already allows arbitrary loads.

### Rex Go: hosts

`Transport` gets `.tailcat`. `HostConfig` gets an optional `tailcat` (address, machine name). It is optional for the same decoding reason as the transport. `HostConfig.reach` stays the one place that picks the path. A host with a tailcat address uses it, whether or not built-in Tailscale is on.

### Lifecycle

The tailcat client is `magicsock`. So the network of the phone and the suspension hit it as they hit the tailnet node (IOS-001-tailnet.md, Lifecycle). The news of the path monitor goes to it too. The socket reset after a suspension binds it again. The reset carries over with no hook upstream. The `lb.sys` of a tailcat client is the same `tsd.System` as the `Server.Sys()` of tsnet. The engine reaches it the same way, through unexported fields. The tests of these fields fail when they move. If they move, the smallest upstream ask is `(*Client).Sys()`. Then `Host.resume()` runs, which keeps the sessions on the server.

A rebound client is cold: its DERP connection is dead and a new path costs seconds, so its next dial gets the whole 15 s, as a fresh client's, not the 3 s a try gets — a try tells a restarted server, and a suspension is not one. A dial that kept the try here paid it, plus the start of the client the try retired, and a connect after a suspension took about nine seconds. So the reset also starts the client back to a path ahead of that dial (`warm`): a meow ping opens the relay connection anew and has the server add the client as a peer again, which a server restarted in the suspension needs too, and a disco ping finds a path and shakes hands. The dial then only sends.

A machine whose server restarted, as on an upgrade, forgot the client. The client still trusts its old direct path and its WireGuard session. If the engine leaves the client alone, it waits for the rekey of WireGuard, 15 s and more. So the engine retires a client that connected before and fails its try. It retires the client when the try ran out of time, or when no other connection uses the client. The pool of `internal/rex` on the desktop uses the same rule (`retire`, #751). Before, the desktop replaced a client only when no other connection used it, so a connection that stayed open across the restart kept the host unreachable. The client leaves the pool and closes, and a new client dials, within the 15 s. The other connections of the old client go with it, because their server forgot them too. Two clients of one key take the packets of each other.

The dials that failed their try on the old client at the same time take the one new client. Every dial after that takes it too. None waits for the others. None waits for an open connection that the proxy keeps until it reads a FIN that never comes. A try that a port refused, while other connections use the client, keeps the client. The socket resets of a suspension run side by side, one for each client and one for the node. So a stuck reset holds up no other reset.

## Size

tailcat links `magicsock`, WireGuard and `netstack`, which the engine already has. The size was measured on the Release device binary of Rex Go, stripped, with and without tailcat: +0.26 MB installed, +0.11 MB compressed (gzip -9).

## Tests

- `internal/rex`: port 7297 accepts allowed keys only. The socket of the gateway gets a stream with the right key. Remove and remote off close the gateway stream of a phone. The relay setting puts the region in the address.
- `internal/rexapi`: the `identify` of a tailcat stream. A refused key gets nothing.
- End to end on a Mac mini: Rex Go in the simulator scans the code and pairs. It opens a terminal, goes to the background and the foreground, and is removed. Run this once through the relays of tailcat, and once through a `derper` in a container.
- On a phone, with cellular only and no Tailscale anywhere, switch Wi-Fi to cellular and back with a terminal open.

## Phases

1. **Spike** (done, #396): a tailcat client in the engine, and a host reached through the proxy. The host test used Go tests against a local DERP, and not yet the simulator. The spike also covered the socket reset after a suspension, and the size.
2. **Server** (done): port 7297 on the tailcat address, the socket of the gateway for its streams, and the relay setting (#398).
3. **Rex Go** (done): the `.tailcat` transport, the `rexgo://tailcat` code, pairing, the client key and the lifecycle (#400, and #507 for pairing by IP address). Findings:
   - The button sits in Devices, beside Pair New Device…, as Rex QR… already names the code of the tailnet and of the local network. Its dialog shows the image only. It shows no address and has no Copy.
   - Rex Go registered no URL scheme before. It now registers `rexgo`. The `ASWebAuthenticationSession` of the Tailscale login keeps its own callback, which its page never calls.
   - The URL of a host is `http://<id>.tailcat.invalid:7297`, and the id is its UUID. The port is the port of Rex's API, because the proxy dials the port that the caller asks for.
   - The app adds the paired host at once. The host retries every 3 s until the machine allows the key, for 5 minutes, which is the pairing window. Cancel takes the host back.
   - With built-in Tailscale off, the path monitor runs while a host is paired over tailcat. The socket reset of the foreground runs too.
   - The app keeps the address in UserDefaults with the host. The address lets in nothing that the machine did not approve. The secret that the phone holds, its client key, is in the Keychain.
   - End to end in the simulator on a Mac mini, through the relays of tailcat: pair, approve, sessions listed, background and foreground, revoke. Not yet tested: opening a terminal, a `derper` of one's own, a phone.
4. **Docs** (done): `docs/REF-005-rex-api.md` and the README "Remote access" describe Rex Go over tailcat and a `derper` of one's own. The side of the server and the relay were written with phase 2. The steps of Rex Go followed phase 3.
