# Host links: one identity, any path, one switch

Status: proposal. Not implemented. It sets the frame for
[desktop-tailnet.md](desktop-tailnet.md), which becomes one of its
phases.

## Problem

Two GoRex machines can reach each other in two ways today, and each way
is a whole stack of its own:

| | Tailcat | Tailscale discovery |
| --- | --- | --- |
| Who gets in | the keys of an allowlist, added by pairing | devices of the same Tailscale user |
| Identity of a client | its client key | `tailscale:<scope>:<node>` |
| Encryption and path | WireGuard, direct or a DERP relay | the tailnet's WireGuard, direct or DERP |
| Finding a host | an address, typed or scanned | found on the tailnet |
| Code | `remote.go` | `system_tailnet.go`, a second listener, links and relay |

The user pays for it:

- Settings names transports (Tailcat, Tailscale, relay) before it says
  what they are for, and each has its own switch and state.
- One machine reached both ways is two hosts with two identities, and
  the sessions it opened belong to one or the other.
- A new way to connect, such as a relay on a public server, would be a
  third stack.

## First principles

[ios-tailcat.md](ios-tailcat.md) lists what reaching a terminal needs,
each exactly once: **trust** (each side knows the other's key and can
forget it), **encryption** between the two keys, **a path** (direct or
relayed), and **rendezvous** (where the two meet).

Tailcat gives all four with no server that knows anything: keys in an
allowlist, WireGuard, `magicsock`, and a DERP relay named in the
address.

What Tailscale adds is none of the four. It adds an **introduction**:
it tells two machines that never met that they belong to one user, so
no one has to carry a key between them. A QR code is another
introduction; so is the pairing window.

So the stacks differ in one step only, how the keys get exchanged. The
rest can be one.

## The three layers

### 1. Identity: the device key

A device is a key. That is the whole identity, for every client, by
every way in.

- Each machine has its client key (`remote-client.json`) and its
  allowlist (`remote.json`), as today.
- The server admits a connection by its key and nothing else. The
  principal of a session is that key.
- Removing a device from the list closes its connections, as today.

**Introductions** put a key on the list. They differ in who approves:

| Introduction | Approval | Today |
| --- | --- | --- |
| Pairing window | the user compares a fingerprint | exists |
| QR code | the scan | exists, for Rex Go |
| Tailscale | none: the device is the same Tailscale user's, checked with `WhoIs` | new, replaces the tailnet's own data path |

An introduction is a short exchange, then it is over: the client hands
its key and name, the server adds it to the list and hands back its
address. Each entry keeps how it came (`paired`, `tailscale`), for the
list to show.

An entry that came by Tailscale also keeps the device's node ID, and
lasts as long as the rule holds for it: when a fresh snapshot of the
tailnet no longer has that device as a same-user peer (removed, expired,
tagged, another user's), the entry is removed and its connections close,
as the tailnet mode drops such a peer today. Turning the rule off
removes these entries too. A snapshot that cannot be read removes
nothing: a failed read says nothing of the peers. A paired entry is
never removed this way.

### 2. Path: whatever carries bytes

A path gets bytes from one machine to the other. It decides nothing
about trust: every path ends in the same thing, a connection whose two
device keys are proven and whose bytes are encrypted between them. The
server then serves it as it does today (`Server.handle`, with the
client's key).

So a path may be anything, and a network nobody trusts is as good as
one's own. There are two kinds, by whether the machine can be dialed:

| | Direct address | Tailcat |
| --- | --- | --- |
| For | a machine one can dial: `192.168.8.20`, a name, a public address | a machine one cannot dial |
| Reaches it over | the local network, a VPN of one's own (a WireGuard tunnel between routers), a port forward, any TCP relay | `magicsock`: direct through NAT, or the DERP relay the address names |
| Needs a third party | no | a relay to meet at |
| Keys proven by | a handshake of the device keys on the connection | WireGuard, in the tunnel |
| Today | missing for desktops | exists |

**Direct address** is new. The server listens on TCP `RemotePort` of an
address the user picks, for the networks the user allows, as the local
network listener for Rex's API does today (`gorex rexapi lan`). A client
dials `host:port`; a Noise handshake of the two device keys follows,
and what comes after is the protocol of today, inside it. A key not on
the list gets what it gets over tailcat: nothing, or the pairing window
while it is open.

With it, a user whose machines already reach each other needs nothing
else: no relay, no account, no way out to the internet.

**Tailcat** stays as it is, for machines behind NAT with nothing between
them. Its relay is tailcat's own by default, or the user's `derper`
(`gorex remote relay HOST`).

**A public relay**, later, is then a choice of two existing shapes, and
no third transport:

- a `derper`, named as tailcat's relay; or
- any server that forwards a TCP port to the machine, dialed as a
  direct address. It sees ciphertext, and the handshake refuses whoever
  is not on the list.

One host has one identity and may have several paths: its direct
addresses and its tailcat address. A client tries the direct ones
first, then tailcat, and stays one host in the list. Once connected by
any path, the server tells the client its other addresses, so the next
connection finds the best one without the user typing it.

The tailnet no longer carries sessions. The node (the system client's,
or the built-in one of desktop-tailnet.md) is used to see the user's
other devices and to carry the introduction, on port 7464 as now.

In code the two kinds sit behind one small contract: a listener and a
dialer that hand back a connection with its peer's key. That is the
whole interface.

### 3. Surface: devices

Settings › Server, in the user's words:

- **Let other devices connect**: one switch. It turns the listener on.
- **Devices**: the list of who may connect, each with its name, how it
  came and when it was last seen, and Remove.
- **Add a device**: the pairing window, and the QR code for a phone.
- **Devices on my Tailscale account connect without pairing**: the one
  rule. It needs the node signed in (**Sign in**, desktop-tailnet.md).
- **Addresses**: where this machine listens for direct connections,
  and for which networks. Off by default.
- **Relay**: default, or a host.

Remote Hosts lists machines. A machine found on the tailnet and not yet
connected shows as available; **Connect** makes the introduction and
saves it as a host like any other. **Add Remote Host** takes either
kind of address: `192.168.8.20`, or the long one a machine shows. A
host's details say how it is reached now: at which address, or through
the relay.

The words Tailcat and DERP stay in the documentation.

## What goes away

- The tailnet's listener for sessions, its links, slots and relay
  (`relayTailnet`, `connectTailnet`, `remote.tailnetDial`).
- `tailscale:` targets as a host's address and as a session's owner, and
  the cases for them in the app (`hosts.go`, `attention.go`, `state.go`,
  `internal/rexapi/model.go`).
- The second switch with its own state.

What stays of `system_tailnet.go`: the snapshot of same-user peers, the
probe, and the `WhoIs` checks, now in front of a key exchange.

## Outside this frame

- **Rex's API for Rex's own apps**: the gateway's HTTPS on the tailnet
  and its local network listener. Rex's app knows no tailcat, so these
  stay as they are, as compatibility listeners. Rex Go already pairs
  over tailcat and fits the frame.
- **Accounts of GoRex's own**, and any server that keeps who belongs to
  whom. The coordinator was removed for its complexity; a public server
  here is a relay and nothing more.

## Security

- **Admission** is the allowlist alone. The Tailscale rule adds to it
  only after `WhoIs` says: same user, untagged on both ends, not
  expired, not shared in, as `tailnetSnapshot.verifies` checks today.
- **The address is a secret** (the server's key and the pre-shared
  key). The introduction sends it over the tailnet, to a device that
  passed the check.
- **A relay sees keys and ciphertext.** That holds for a public one
  too; it is why it needs no trust.
- **A direct address is a port open to its networks.** Anyone there can
  start a handshake; only a key on the list finishes it. The allowed
  networks are a second fence, not the lock. The handshake is limited
  in time and number, as pairing requests are.
- **The first connection to a direct address** has no key to check the
  server against. Pairing shows a fingerprint of both keys on both
  screens, as today; the client keeps the server's key from then on,
  and refuses another at that address.
- **Leaving the tailnet removes a device** that the rule added
  (Identity), within a refresh of the snapshot, as today.
- **Remove and the rule.** A removed device that is still on the
  tailnet would come back with the next introduction. Remove keeps its
  key as refused until the user allows it again.

## Alternatives

| Option | Why not |
| --- | --- |
| Sessions over the tailnet as a third path | a second identity, or a second handshake, for a network tailcat and a direct address already cover |
| Tailcat as the only path | a machine one can already dial would still need a relay to meet at, and a way out to it |
| Sessions over the tailnet only | needs a Tailscale account for everyone; no pairing by hand |
| A GoRex account server that introduces devices | the coordinator again |

## Migration

- A host saved by its `tailscale:` target introduces itself the next
  time it connects, and is saved by its address.
- A session a tailnet peer opened is owned by its target. After the
  update that owner is gone; the session stays and is shared as the
  server shares sessions.
- A machine with Tailscale discovery on and remote access off gets the
  listener on with the update, as the rule needs it. Settings says so.

## Phases

1. **Introduction over the tailnet**: the key exchange on 7464, entries
   with their source, refused keys. Both ways in still work.
2. **Direct address**: the listener, the handshake, pairing over it,
   a host with several addresses.
3. **The built-in node** carries the introduction on Windows and macOS
   (desktop-tailnet.md, without its session transport).
4. **Sessions leave the tailnet**: remove the relay, the links and the
   targets; migrate saved hosts.
5. **Settings and Remote Hosts** in the words above.
6. **A public relay**: its own document, when there is a server to name.

## Open questions

- **The handshake.** Noise IK with the device keys is the candidate:
  `tailscale.com/control/controlbase` has it, for keys of the same
  curve, and it is already a dependency. Whether it takes the device
  keys as they are, and how the client learns the server's key before
  pairing, is not checked.
- **Rex Go and Rex's API on a direct address.** The local network
  listener for Rex's API is plain HTTP with no credential. The gateway's
  port could ride the direct address as it rides tailcat's (7297), with
  the device key. Not in these phases.
- **Changing the relay changes the address**, and paired clients need
  the new one. With more relays to choose from, the server should tell
  its connected clients the new address before it moves.
- **Direct or relayed.** Whether tailcat tells which route a connection
  takes is not checked.
- **Linux**: system client or built-in node, as in desktop-tailnet.md.
  Here the node only introduces, so the choice costs less either way.
