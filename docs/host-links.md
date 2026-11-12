# Host links: one identity, any path, one switch

Status: the model is accepted; revised after review. Not implemented.
It covers the three clients, the desktop, Rex Go (iOS) and the web
client, and sets the frame for
[desktop-tailnet.md](desktop-tailnet.md).

## Problem

A client reaches a GoRex machine in five ways today, and each way has
its own idea of who the client is:

| Way in | Who gets in | Used by |
| --- | --- | --- |
| Tailcat, port 7464 | a device key on the allowlist, added by pairing | desktop |
| Tailcat, port 7297 (gateway) | the same allowlist | Rex Go |
| Tailscale discovery, port 7464 | devices of the same Tailscale user; identity `tailscale:<scope>:<node>` | desktop (macOS, Linux) |
| Gateway HTTPS on the tailnet, port 443 | the Tailscale user who signed the gateway in | Rex Go, web, Rex's own app |
| Gateway HTTP on the local network, port 7297 | the networks allowed; no credential, no encryption | Rex Go, web, Rex's own app |

The user pays for it:

- Settings names transports (Tailcat, Tailscale, relay, local network)
  before it says what they are for, and each has its own switch.
- One machine reached two ways is two hosts with two identities.
- Machines that can already dial each other, as over a WireGuard tunnel
  between two routers, still need a relay to meet at, or the
  unencrypted listener.
- A new way to connect, such as a relay on a public server, would be a
  sixth row.

## First principles

[ios-tailcat.md](ios-tailcat.md) lists what reaching a terminal needs,
each exactly once: **trust** (each side knows the other's key and can
forget it), **encryption** between the two keys, **a path**, and
**rendezvous** (where the two meet).

Tailcat gives all four with no server that knows anything. What
Tailscale adds is none of the four: it adds an **introduction**. It
tells two machines that never met that they belong to one user, so
nobody carries a key between them. A QR code is another introduction;
so is the pairing window.

So the ways in differ in two steps only: how the keys get exchanged,
and where the two meet. The rest can be one.

## The three layers

### 1. Identity: the device key

A device is a key: its client key (`remote-client.json` on a desktop,
the Keychain in Rex Go). A machine that serves also has its server key
(`remote.json`'s `nodeKey`), which its address carries. The server
admits a connection by the client's key and nothing else, on every port.

**Introductions** put a client key on the allowlist:

| Introduction | Approval | Today |
| --- | --- | --- |
| Pairing window | the user compares the fingerprint of the client's key | exists (desktop, Rex Go) |
| Pairing by address | the user compares a code of both keys | new: type `192.168.8.20` |
| QR code | the scan, then the pairing window | exists (Rex Go) |
| Tailscale | none: the device is the same Tailscale user's | new, replaces the tailnet's own session path |

Every introduction gives the client the server's address, which holds
the server's key. After it, a client talks only to that key, at
whatever address.

**Pairing by address.** A user who can dial a machine types what they
know, `192.168.8.20` or a name, and nothing else. The client has no key
for it yet, so this one introduction is a first contact, and the code
is what makes it safe:

1. On the machine, **Reachable at** is on and the pairing window is
   open. Only then does its listener answer a pairing request.
2. The client connects to the address over TLS and has seen the
   listener's certificate. Three messages follow, the way Bluetooth
   pairs by comparing numbers:
   - the client sends a commitment: a hash of its key and a random
     number of its own;
   - the server answers with its key and a random number;
   - the client opens the commitment: its key, its name and its number.
3. Both screens show a **pairing code** of six digits, from a hash of
   the certificate, both keys and both numbers. Each side computes it
   from what it holds and what it saw.
4. The user compares the codes and approves on the machine, as today.
5. The machine answers on the same connection with its addresses. The
   client keeps the server's key from then on.

Somebody in the middle shows the client another certificate and has to
choose every value of its own before it sees the other side's number,
so it cannot pick them to match: its codes agree by chance once in a
million, and a mismatch is on both screens. That is why six digits are
enough, and why the commitment is there: without it a key could be
ground until a short code matched.

This replaces today's fingerprint for this introduction only: that one
covers the client's key alone, which is enough where the address
already carried the server's key, and not here.

What it costs, said plainly: while the window is open, whoever can
dial the address can ask to pair, where over tailcat they would need
the secret address first. A request shows the address it came from,
one may wait per source, and eight in all, as today.

**The Tailscale rule.** On the tailnet, port 7464, a client sends its
key and name; the server checks the connection with `WhoIs`, checks the
peer in its current snapshot again under the allowlist's lock (as
`admit` does today), then writes the entry and answers with its
address. What the rule means, plainly:

- **Any device of the user's Tailscale account gets a shell here.** It
  may enrol any key, its own or another's, and an allowed key runs
  commands as this user from anywhere (README). The account and its
  control server, Tailscale's or the user's Headscale, are the root of
  trust. Untagged devices made with an auth key, as a CI runner, count.
- **One key per node.** An entry keeps the node ID it came from; a new
  introduction from that node replaces the key.
- **The entry lives while the tailnet vouches for it.** It is admitted
  only while the broker holds a fresh snapshot that has the node as a
  same-user peer: untagged, not expired, not shared in. Three failed
  reads in a row, as today, a signed-out node, another tailnet, or the
  rule turned off, close its connections and refuse its key until a
  snapshot has it again. A device removed from the tailnet loses its
  entry.
- **Remove** refuses the node, not only the key, until the user allows
  it again in the list. While the rule is on, keeping a device out for
  good means removing it from the Tailscale account.

A paired entry is none of this: it stays until Removed.

### 2. Path: tailcat, and where it meets

Every session of a GoRex client runs over tailcat: WireGuard between
the two keys, one listener, the two ports it has today (7464 for
desktops, 7297 for the gateway). Trust never depends on the path.

What varies is the relay the address names, where the two meet before
`magicsock` finds the direct route:

| Relay | For | Today |
| --- | --- | --- |
| tailcat's own | machines with nothing between them | default |
| a `derper` of one's own | the same, self-hosted | `gorex remote relay HOST` |
| **the machine itself** | a machine one can dial: a local network, a VPN of one's own (WireGuard between routers), a port forward | new |
| a public server | later | one more `derper` to name |

**The machine as its own relay** is the direct address. The server runs
a DERP server in its process (`tailscale.com/derp/derpserver`, already
a dependency) on an address the user picks, with a certificate of its
own, and shows a second tailcat address whose region is that address,
the certificate pinned (`tailcfg.DERPNode.CertName`, `sha256-raw:`). A
client that can dial it meets the server there and goes direct; it
needs no third party and no way out to the internet. The keys, the
allowlist, pairing, Remove and both clients' tailcat code are the ones
of today. No handshake is designed and sessions get no second
listener. The relay's listener also answers pairing by address
(Identity) while the window is open, and nothing else of GoRex's.

It listens on a fixed port by default, so that an address is an IP
address or a name and no more.

A spike settled this (branch `spike/own-relay`, tests only), on one
machine:

- The address carries the region whole: the IP address, the port, the
  pin and no STUN. It is 316 characters, and parses back the same.
- A client with only that address connects through the relay in the
  server's process, and a wrong pin connects nothing. The desktop's
  client code and Rex Go's engine both do, unchanged.
- No DERP map is fetched, no STUN is asked, and no TCP connection left
  the machine. The route goes direct at the first ping, by the local
  interface addresses.
- The same listener serves a path of GoRex's own beside the relay's,
  and a client reads the certificate's hash from the TLS connection:
  what pairing by address needs. HTTP/2 must be off on it, as the relay
  upgrades an HTTP/1.1 connection.

And what it found to build around:

- **The relay passes packets for anybody** who can dial it, as
  `derpserver` does by default. It has to admit this machine's key and
  the keys on the allowlist only, and any key while the pairing window
  is open; `derpserver` checks a client through a URL, which can be the
  process's own.
- **The first connection after the server starts takes 3 s**: with no
  STUN the server waits that long before it tries its relay. Later ones
  take some 40 ms.
- **`gorex remote relay` refuses an IP address** and sets no pin: the
  region of this address is built apart from it.
- The relay's client follows the environment's HTTP proxy; a dial to
  one's own address must not.

Not yet seen: two machines, a NAT between them, a phone, and what a
firewall says of the listening port.

A machine may show several addresses, one per relay, with the same
keys. A host is one entry by its server key, with the addresses the
client knows; the client tries them in order. An introduction and a
hello give the client the others, all for the key it already holds.

**The tailnet carries no desktop sessions.** The node, the system
client's or the built-in one, sees the user's devices and carries the
introduction.

### 3. Surface: devices

Settings › Server, in the user's words. The first three are all a new
user needs:

- **Let other devices connect**: one switch. It turns the listener on.
- **Devices**: who may connect, each with its name, how it came
  (paired, Tailscale), when it was last seen, and Remove. Refused
  devices show at the end, with Allow.
- **Add a device**: the pairing window, and the QR code for a phone.

Under Show Details:

- **Devices on my Tailscale account connect without pairing**, with
  **Sign in** (desktop-tailnet.md). Its note says what it means: every
  device of the account gets in.
- **Reachable at**: off, or an address of this machine. On, the
  machine is its own relay there, and Devices shows a second address
  and QR code for clients that can dial it.
- **Relay**: default, or a host.

Remote Hosts lists machines. **Add Remote Host** takes what the user
has: an IP address or a name of a machine they can dial, or the long
address a machine shows. A machine found on the tailnet shows as
available;
**Connect** makes the introduction and saves it as a host like any
other. A host's details say how it is reached now.

The first connection, in order: on the serving machine, turn **Let
other devices connect** on and open **Add a device**; on the other,
type its IP address (with **Reachable at** on), or paste or scan its
address; compare the code; Approve.

The words Tailcat and DERP stay in the documentation. The CLI keeps its
commands (`gorex remote tailscale on|off`, `gorex remote relay`).

## The three clients

| | Desktop | Rex Go (iOS) | Web |
| --- | --- | --- | --- |
| Speaks | the session protocol, 7464 | Rex's API, 7297 | Rex's API |
| Device key | `remote-client.json` | Keychain | none today |
| Paired by | pairing window; by address, new | QR code, then the window; by address, new | — |
| Tailcat | yes | yes (ios-tailcat.md) | not yet |
| The machine as relay | new, no client change | new, no client change | no: a browser cannot pin a certificate |
| Tailscale introduction | new | later: its node asks, as a desktop's | — |
| Other ways it keeps | none | the gateway on the tailnet (443), the local network | the gateway on the tailnet (443) through the page's node, the local network |

**Desktop.** Everything above. What goes away is below.

**Rex Go.** It already has the device key, pairing and tailcat, on the
gateway's port. A machine that is its own relay is one more address in
a `rexgo://tailcat` code; the engine dials it as any tailcat address.
Add Host by hand takes an IP address too: the engine makes the request
and the app shows the code.
Its built-in Tailscale stays for Rex servers, which have no tailcat,
and for a GoRex machine reached on 443. Later, its node can ask for an
introduction as a desktop's does, so a phone on the account needs no
QR code.

**Web.** A browser has no sockets of its own and holds no device key
today, so the page stays outside the identity layer for now, and says
so: it reaches a gateway on the tailnet through its own node (who gets
in: the Tailscale user) or on the local network (who gets in: the
networks), and a host must trust the page's origin either way
(web-client.md). The way in for it is tailcat compiled to WebAssembly,
which upstream has as an experiment, relayed only, with a key kept by
the browser and the same pairing. It needs a relay with a certificate a
browser accepts, so not the machine as its own relay. Not in these
phases.

**Rex's own app** knows neither tailcat nor device keys. The gateway's
HTTPS on the tailnet and its local network listener stay for it, as
they are, as compatibility listeners outside this frame. Their
settings move under Show Details.

## What goes away

On the desktop, once sessions leave the tailnet:

- In `internal/rex`: the `remote.tailnetDial` op (`server.go`),
  `relayTailnet`, `connectTailnet`, the tailnet's links and slots, the
  `IsTailnetTarget` cases of `remoteClientFor` and of the dial
  (`remote.go`), `Request.TailnetTarget` (`proto.go`).
- In the app: the `tailscale:` cases of `hosts.go` (`TailnetOpened`,
  `tailnetOnline`, `reconcileTailnet`), `hostsview.go`, `attention.go`,
  `state.go`, and of the CLI (`internal/cli/commands.go`).
- `tailscale:` targets as a session's owner.
- The second switch with its own state.

What stays of `system_tailnet.go`: the snapshot of same-user peers, the
probe, and the `WhoIs` checks, now in front of a key exchange.

## Security

- **Admission** is the allowlist alone, on every address. The direct
  address's relay admits nobody: it only passes packets between keys,
  as any DERP server does.
- **The address is a secret** (the server's key and the pre-shared
  key). The introduction sends it over the tailnet to a device that
  passed the check.
- **A relay sees keys, ciphertext, and the timing and size of the
  traffic.** That holds for a public one too.
- **The Tailscale rule widens trust** from devices the user approved
  one by one to every device of an account (Identity). It is off by
  default and never turned on by an update.
- **A machine that is its own relay opens a port** to whoever can dial
  the address. It serves the relay's protocol to the keys on the
  allowlist, and to any key only while the pairing window is open, so
  it relays for nobody else; and pairing by address while that window
  is open. Reaching the session server still needs an allowed key and
  the address's secret. The listener is limited in connections, per
  source and in all.
- **Pairing by address rests on the comparison.** A user who approves
  without comparing the code lets in whoever asked. The dialog shows
  the code large, with the name and the address the request came from,
  and Approve is not the default button.
- **Revocation** closes a key's connections on every address at once,
  as today for tailcat's two ports.

## Alternatives

| Option | Why not |
| --- | --- |
| A TCP listener with a Noise handshake of the device keys | not needed: the spike held. It would need a handshake of GoRex's own (`controlbase` fixes its prologue to Tailscale's), an address that carries the server's key, a second listener and limits before authentication |
| Pairing by address with today's fingerprint | it covers the client's key only, so somebody in the middle passes it; the code of both keys is what closes that |
| A longer code and no commitment | 12 characters from a plain hash of the keys need no exchange, but are too long to compare by eye |
| Sessions over the tailnet as a second path | a second identity for a network tailcat already covers |
| A GoRex account server that introduces devices | the coordinator again |

## Migration

- Nothing is turned on by an update. A machine with Tailscale discovery
  on and remote access off is asked once, in Settings, before the
  listener starts: the rule needs it, and the keys it adds then work
  from outside the tailnet too.
- A host saved by its `tailscale:` target introduces itself the next
  time the user connects it, and is saved by the address.
- A session a tailnet peer opened is owned by its target. The owner is
  a label, not a permission; after the update the label is gone and the
  session stays.
- A machine of a version before the introduction speaks the tailnet's
  relay only. Until both sides are updated, two machines connect over
  the tailnet only when both still have the relay.

## Phases

Each ships something a user can use.

1. **Spike** (done): the machine as its own relay, with tailcat as it
   is. It holds; see Path for what it found.
2. **Reachable at**: the setting, the second address and its QR code,
   and pairing by address. Desktops and Rex Go connect over a local
   network or a VPN of one's own with no relay, from an IP address
   typed in. Docs.
3. **Settings in the new words**: one switch, Devices, Add a device,
   Show Details. No change in behavior.
4. **Tailscale introduction** on the system client (Linux): the
   exchange, entries with their node, the snapshot rule, refused
   nodes. The tailnet's relay still works beside it.
5. **The built-in node** carries it on Windows and macOS
   (desktop-tailnet.md).
6. **Sessions leave the tailnet**: What goes away, and Migration.
7. Later, each with its own document: Rex Go's introduction, the web
   client's key, a public relay.

## Open questions

- **Changing the relay changes the address.** With several addresses a
  server can tell a connected client the new one before the old one
  goes.
- **Which route a connection takes** (direct, or through which relay):
  whether tailcat tells is not checked.
- **The embedded relay's protocol on a port forward**: DERP rides
  HTTPS, so a forward of one TCP port should do, and the direct route
  needs UDP as well. Not checked.
- **The 3 s of a cold start.** A STUN answer from the same process may
  remove the wait; not tried.
- **The firewall.** Unlike the built-in Tailscale node, this is a
  listening port on the machine's own stack: Windows asks the user the
  first time, and other firewalls may block it silently.
- **The fixed port.** `RemotePort` (7464) on the machine's own stack is
  the candidate. The system Tailscale mode on Linux listens on that
  port of the Tailscale address, so the two meet only if **Reachable
  at** is that address or every address.
- **Linux**: system client or built-in node, as in desktop-tailnet.md.
