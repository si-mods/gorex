# Host links: one identity, one path, one switch

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

### 2. Path: tailcat

Every session between two GoRex machines runs over tailcat. There is
one listener (`RemotePort`, and the gateway's port beside it), one
client, one protocol.

Inside it, `magicsock` picks the route: direct when the network allows,
through the relay when it doesn't. The relay is the DERP server the
address names:

- tailcat's free relays, by default;
- the user's own `derper` (`gorex remote relay HOST`), today;
- **a public server**, later: one more relay to name. It sees keys and
  ciphertext only, and needs no account and no state.

That is the place where a public relay plugs in, and it exists. No new
transport interface is needed for it, and none is added.

The tailnet no longer carries sessions. The node (the system client's,
or the built-in one of desktop-tailnet.md) is used to see the user's
other devices and to carry the introduction, on port 7464 as now.

### 3. Surface: devices

Settings › Server, in the user's words:

- **Let other devices connect**: one switch. It turns the listener on.
- **Devices**: the list of who may connect, each with its name, how it
  came and when it was last seen, and Remove.
- **Add a device**: the pairing window, and the QR code for a phone.
- **Devices on my Tailscale account connect without pairing**: the one
  rule. It needs the node signed in (**Sign in**, desktop-tailnet.md).
- **Relay**: default, or a host.

Remote Hosts lists machines. A machine found on the tailnet and not yet
connected shows as available; **Connect** makes the introduction and
saves it as a host like any other. A host's details say how it is
reached now: directly, or through the relay.

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
- **Leaving the tailnet does not remove a device** that the rule
  already added: it is on the list until Removed. Today the tailnet mode
  drops such a peer by itself. See Open questions.
- **Remove and the rule.** A removed device that is still on the
  tailnet would come back with the next introduction. Remove keeps its
  key as refused until the user allows it again.

## Alternatives

| Option | Why not |
| --- | --- |
| Keep both paths behind a transport interface | two listeners and two identities remain, or a new signed handshake to make them one; a relay would be a third transport |
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
2. **The built-in node** carries it on Windows and macOS
   (desktop-tailnet.md, without its session transport).
3. **Sessions leave the tailnet**: remove the relay, the links and the
   targets; migrate saved hosts.
4. **Settings and Remote Hosts** in the words above.
5. **A public relay**: its own document, when there is a server to name.

## Open questions

- **Leaving the tailnet.** Should entries that came by the rule be
  dropped when their device is no longer a same-user peer? It keeps
  today's behavior, at the cost of a check on every refresh.
- **Changing the relay changes the address**, and paired clients need
  the new one. With more relays to choose from, the server should tell
  its connected clients the new address before it moves.
- **Direct or relayed.** Whether tailcat tells which route a connection
  takes is not checked.
- **Linux**: system client or built-in node, as in desktop-tailnet.md.
  Here the node only introduces, so the choice costs less either way.
