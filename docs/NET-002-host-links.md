---
name: NET-002
description: "One device identity for all paths, tailcat for sessions, and Tailscale only as an introduction. Use when you change pairing, device keys or how hosts connect."
metadata:
  created: "2026-11-12"
  updated: "2026-11-23"
---

# NET-002: Host links: one identity, any path, one switch

Status: phases 1 to 3 built (#495, #498, #501, #507, #511, #541). Phase 4 not built. Phase 5 is built in part: the built-in node exists (#573), but it carries no introduction. Phases 6 and 7 not built.

The model is accepted, and it changed after review. It covers the three clients: the desktop, Rex Go (iOS) and the web client. It sets the frame for [DSK-005-tailnet.md](DSK-005-tailnet.md).

## Problem

Today a client reaches a GoRex machine in five ways. Each way has its own idea of who the client is:

| Way in | Who gets in | Used by |
| --- | --- | --- |
| Tailcat, port 7464 | a device key on the allowlist, added by pairing | desktop |
| Tailcat, port 7297 (gateway) | the same allowlist | Rex Go |
| Tailscale discovery, port 7464 | devices of the same Tailscale user, with the identity `tailscale:<scope>:<node>` | desktop (macOS, Linux) |
| Gateway HTTPS on the tailnet, port 443 | the Tailscale user who signed the gateway in | Rex Go, web, Rex's own app |
| Gateway HTTP on the local network, port 7297 | the networks allowed, with the pairing token (or an address on the legacy list). There is no encryption, so a sniffer on the network can take the token | Rex Go, web (WebTransport, 7298), Rex's own app (legacy list) |

The user pays for this:

- Settings names the transports (Tailcat, Tailscale, relay, local network) before it says what they are for. Each transport has its own switch.
- One machine that the user reaches in two ways is two hosts with two identities.
- Some machines can already dial each other, as over a WireGuard tunnel between two routers. They still need a relay to meet at, or the unencrypted listener.
- A new way to connect, such as a relay on a public server, would be a sixth row.

## Design

### First principles

[IOS-002-tailcat.md](IOS-002-tailcat.md) lists what the connection to a terminal needs. It lists each item exactly once:

- **trust**: each side knows the key of the other side and can forget it
- **encryption** between the two keys
- **a path**
- **rendezvous**: where the two meet

Tailcat gives all four, with no server that knows anything. Tailscale adds none of the four. It adds an **introduction**. It tells two machines that never met that they belong to one user, so nobody carries a key between them. A QR code is another introduction. The pairing window is also an introduction.

So the ways in differ in two steps only: how the keys get exchanged, and where the two meet. The rest can be one.

### The three layers

#### 1. Identity: the device key

A device is a key. This is its client key (`remote-client.json` on a desktop, the Keychain in Rex Go). A machine that serves also has its server key (`nodeKey` in `remote.json`), which its address carries. The server admits a connection by the key of the client and nothing else, on every port.

**Introductions** put a client key on the allowlist:

| Introduction | Approval | Today |
| --- | --- | --- |
| Pairing window | the user compares the fingerprint of the client's key | exists (desktop, Rex Go) |
| Pairing by address | the user compares a code of both keys | exists (desktop, Rex Go): type `192.0.2.20` |
| QR code | the scan, then the pairing window | exists (Rex Go) |
| Tailscale | none: the device is the same Tailscale user's | new, replaces the tailnet's own session path |

Every introduction gives the client the address of the server, which holds the key of the server. After it, a client talks only to that key, at whatever address.

**Pairing by address.** A user who can dial a machine types what they know, `192.0.2.20` or a name, and nothing else. The client has no key for the machine yet. So this introduction is a first contact, and the code makes it safe:

1. On the machine, **Reachable at** is on and the pairing window is open. Only then does its listener answer a pairing request. When the window is closed, the path is unknown (404).
2. The client connects to the address over TLS 1.3 and has seen the certificate of the listener. It checks no chain, because this is a first contact. It takes no HTTP proxy. Three messages follow, in the way that Bluetooth pairs by comparing numbers:
   - The client sends a commitment: a hash of its key, a random number of its own and its name.
   - The server answers with its key and a random number, and not its name.
   - The client opens the commitment: its key, its number and its name. If the opening does not match, the server closes the connection.
3. Both screens show a **pairing code** of six digits. It comes from a hash of the TLS connection, the certificate, both keys and both numbers. Each side computes it from what it holds and what it saw.
4. The user compares the codes and approves on the machine. On the client, the user says that the codes match too. The client keeps nothing before that.
5. The machine adds the key to the allowlist. It answers on the same connection with its addresses, the address of Reachable at first. The client keeps the addresses only when each address is of the server key that the client got in step 2. The first address pins the certificate that the client saw. If an address fails this check, the client says that the machine may have let in an unknown device, whose entry the user must remove.

Somebody in the middle shows the client another certificate. That person has to choose every value of their own before they see the number of the other side. So they cannot pick the values to make the codes match. Their codes agree by chance once in a million. They do learn the code of the machine before they open their own commitment. They could leave and ask again until one code matched. So they get one try. A request that got the number of the machine and ends without the approval closes the pairing window. It can end by leaving, by a rejection or by time. That is why six digits are enough, and why the commitment is there. Without the commitment, someone could grind a key until a short code matched.

This replaces today's fingerprint for this introduction only. That fingerprint covers the key of the client alone. This is enough where the address already carried the key of the server, and it is not enough here.

As built, in `internal/pairaddr` (which `internal/rex/pairaddr.go` serves), `remotectl pair IP NAME` and the engine of Rex Go (`ios/tailnet/pairaddr.go`):

- The path is `/gorex/pair` on the listener of the relay. It is an HTTP/1.1 upgrade to `gorex-pair-v1`. Then the messages are bytes:
  - the commitment (32 bytes)
  - the key and number of the server (64 bytes)
  - the opening: the key, the number, the length of the name in two bytes, and the name
  - at the end, a JSON answer after its length in two bytes
- `commit = SHA-256("gorex-pair-v1/commit\0" || K_C || N_C || u16be(len(name)) || name)`.
- `X` is the TLS exporter `EXPORTER-gorex-pair-v1`, 32 bytes, no context. `h = SHA-256("gorex-pair-v1/code\0" || X || SHA-256(cert) || K_C || K_S || N_C || N_S)`. The code is the first 8 bytes of `h`, as a number, modulo 1 000 000, of six digits. The numbers are 32 random bytes, new for every attempt.
- A request lives with its connection. It ends when the connection closes, after 2 minutes, or with the window. Each message before the end has 10 s.
- One connection may wait for each source (an IPv6 source counts by its /64), and eight may wait in all. The server refuses more. The server refuses a key that is on the allowlist or already waiting.
- The requests by address are a list of their own, beside the list of today. The list is by connection. It shows each request with its code, name and source (`gorex remote pending`).
  - Approve takes the code (`gorex remote approve CODE`), never the fingerprint. It works only while exactly one request waits, because each other request could be a guess. With two or more requests, reject them all and ask again.
- The confirmation of the client is its own. It may come before or after the approval. `PairByAddress` returns the code at once. `Confirm` waits for the addresses. `Mismatch` ends the pairing with the warning.
- The client half is `internal/pairaddr`. It is apart from the packages of the server, so that the engine of Rex Go asks with it too. It uses the tailcat key of the phone, with one pairing at a time (`PairByAddress`, `ConfirmAddressPairing`, `MismatchAddressPairing`, `CancelAddressPairing`). The phone keeps the first address, the address of Reachable at, as a host of a `rexgo://tailcat` code.

What it costs: while the window is open, whoever can dial the address can ask to pair. Over tailcat, they would need the secret address first. A request shows the address that it came from. One request may wait for each source, and eight in all.

**The Tailscale rule.** On the tailnet, at port 7464, a client sends its key and name. The server checks the connection with `WhoIs`. It checks the peer in its current snapshot again under the lock of the allowlist (as `admit` does today). Then it writes the entry and answers with its address. The rule means this:

- **Any device of the user's Tailscale account gets a shell here.** It may enrol any key, its own or another key. An allowed key runs commands as this user from anywhere (README). The account and its control server are the root of trust. The control server is the one of Tailscale or the Headscale of the user. Untagged devices made with an auth key, as a CI runner, count.
- **One key for each node.** An entry keeps the node ID that it came from. A new introduction from that node replaces the key.
- **The entry lives while the tailnet vouches for it.** The server admits it only while the broker holds a fresh snapshot that has the node as a same-user peer. The peer must be untagged, not expired and not shared in. In these cases, the server closes the connections of the entry and refuses its key until a snapshot has it again:
  - three failed reads in a row, as today
  - a signed-out node
  - another tailnet
  - the rule turned off

  A device that someone removes from the tailnet loses its entry.
- **Remove** refuses the node, not only the key, until the user allows it again in the list. While the rule is on, the user must remove the device from the Tailscale account to keep it out for good.

A paired entry is none of this. It stays until the user removes it.

#### 2. Path: tailcat, and where it meets

Every session of a GoRex client runs over tailcat. Tailcat is WireGuard between the two keys, with one listener and the two ports it has today (7464 for desktops, 7297 for the gateway). Trust never depends on the path.

The relay that the address names varies. This is where the two meet before `magicsock` finds the direct route:

| Relay | For | Today |
| --- | --- | --- |
| tailcat's own | machines with nothing between them | default |
| a `derper` of one's own | the same, self-hosted | `gorex remote relay HOST` |
| **the machine itself** | a machine one can dial: a local network, a VPN of one's own (WireGuard between routers), a port forward | new |
| a public server | later | one more `derper` to name |

**The machine as its own relay** is the direct address. The server runs a DERP server in its process (`tailscale.com/derp/derpserver`, already a dependency). The DERP server uses an address that the user picks, with a certificate of its own. The server shows a second tailcat address. The region of this address is the address that the user picked, and the certificate is pinned (`tailcfg.DERPNode.CertName`, `sha256-raw:`). A client that can dial it meets the server there and goes direct. It needs no third party and no way out to the internet. The keys, the allowlist, pairing, Remove and the tailcat code of both clients are the ones of today. No handshake is designed, and sessions get no second listener. The listener of the relay also answers pairing by address (Identity) while the window is open, and nothing else of GoRex.

It listens on a fixed port by default, so that an address is an IP address or a name and no more.

A spike settled this (branch `spike/own-relay`, tests only), on one machine:

- The address carries the whole region: the IP address, the port, the pin and no STUN. It is 316 characters, and it parses back the same.
- A client with only that address connects through the relay in the process of the server. A wrong pin connects nothing. The client code of the desktop and the engine of Rex Go both do this, unchanged.
- The spike loaded no DERP map and asked no STUN. No TCP connection left the machine. The route goes direct at the first ping, by the local interface addresses.
- The same listener serves a path of GoRex beside the path of the relay. A client reads the hash of the certificate from the TLS connection. This is what pairing by address needs. HTTP/2 must be off on the listener, because the relay upgrades an HTTP/1.1 connection.

The spike also found what to build around:

- **The relay passes packets for anybody** who can dial it, as `derpserver` does by default. It has to admit only the key of this machine and the keys on the allowlist. It also admits any key while the pairing window is open. `derpserver` checks a client through a URL, which can be the own URL of the process.
- **The first connection after the server starts takes 3 s.** With no STUN, the server waits that long before it tries its relay. Later connections take some 40 ms.
- **`gorex remote relay` refuses an IP address** and sets no pin. The region of this address is built apart from it.
- The client of the relay follows the HTTP proxy of the environment. A dial to one's own address must not.

The spike did not see these: two machines with a NAT between them, a phone, and what a firewall says of the listening port.

As built (`internal/rex/ownrelay.go`, `gorex remote reachable IP|off`):

- A tailcat node listens through one relay only. So the second address has a tailcat server of its own, with the same keys, beside the first. Both feed the same accept path: one allowlist, one pairing window, one revoke.
- The relay answers STUN on the same port, UDP, and the address names it. This removes the 3 s wait. Without the UDP port it still works.
- `derpserver` asks a URL whether to admit a key. The URL is on the loopback, with a random path, and the same process serves it. A failure refuses the key.
- The relay does not know which connection is the connection of which key. So a revoke and the end of the pairing window close all of its connections. The keys that the relay still admits connect again.
- `gorex remote address` prints the second address on a second line. `gorex remote status` does not, because an address is a secret.

A machine may show several addresses, one for each relay, with the same keys. A host is one entry by its server key, with the addresses that the client knows. The client tries them in order. An introduction and a hello give the client the other addresses, all for the key that it already holds.

**The tailnet carries no desktop sessions.** The node, the node of the system client or the built-in node, sees the devices of the user and carries the introduction.

#### 3. Surface: devices

The Server page of Settings (Settings › Server) uses the words of the user. The first three items are all that a new user needs:

- **Let other devices connect** is one switch. It turns the listener on.
- **Devices** shows who may connect. Each device shows its name, how it came (paired, Tailscale), when it was last seen, and Remove. Refused devices show at the end, with Allow.
- **Add a device** opens the pairing window and the QR code for a phone.

Under Show Details:

- **Devices on my Tailscale account connect without pairing**, with **Sign in** (DSK-005-tailnet.md). Its note says what it means: every device of the account gets in.
- **Reachable at** is off, or it is an address of this machine. When it is on, the machine is its own relay there. Devices shows a second address and QR code for clients that can dial it.
- **Windows Firewall** (`internal/rex/firewall.go`). While the relay runs, the server reads the firewall through its COM API (`HNetCfg.FwPolicy2`, in a hidden PowerShell, no administrator). It reads the firewall at start, when something asks for the status and the last answer is 5 minutes old, and on `remote.firewallCheck`.
  - An inbound TCP connection to GoRex on port 7464 passes in one of three cases. The firewall is off, or the default allows it, or an enabled allow rule of the active profile covers the program or the port. No block rule may apply.
  - The query leaves out a rule with an owner and no program (the package rule of a Store app).
  - With several profiles active, the connection is blocked only when each profile blocks it. A failed query says nothing.
  - When the connection is blocked, Settings says so under the row, with **Allow**. **Allow** asks Windows for administrator. It adds two inbound rules, "GoRex Reachable at", TCP and UDP, port 7464, every profile, any local address and program. The rules hold after GoRex updates itself to another path and after the address changes.
  - `gorex remote status` prints `reachableFirewall=blocked` and the `netsh` commands.
  - A rule that blocks GoRex (Windows makes one when the user cancels its prompt) wins over the two rules. Settings and the CLI say to remove it in the inbound rules of Windows Defender Firewall.
  - When the user turns Reachable at off, the rules stay. To remove them needs administrator.
- **Relay** is the default, or a host.

Remote Hosts lists the machines that the user added. **Add Remote Host** takes what the user has. This is an IP address or a name of a machine that they can dial, or the long address that a machine shows. The sheet also offers the GoRex hosts that the tailnet finds, each with **Add Host**. Discovery adds no host by itself. A host added in this way is a host like any other, which the user can rename and remove. The details of a host say how it is reached now. A connected host shows its path under its state: a direct path names its UDP endpoint, a relayed path names the relay, and each names the round trip. A host of the tailnet says only that it goes over Tailscale, because its path is the node's own.

As built, the local server measures the path. The op `remote.tailcatStatus` pings the address through the tailcat client of it (`tailcat.Client.DiscoPing`), which the pool of the relayed connections shares. The pong names the path. The ping also nudges direct path discovery, so the display settles on the route the sessions use. The client asks again when a measure is older than 30 seconds. A measure that fails shows nothing and is tried again at the next interval. The op is additive to the socket protocol: a server of before it answers "unknown op", and the row stays silent.

The first connection goes in this order:

1. On the serving machine, turn **Let other devices connect** on and open **Add a device**.
2. On the other machine, type the IP address of the serving machine (with **Reachable at** on). Or paste or scan its address.
3. Compare the code.
4. Approve.

The words Tailcat and DERP stay in the documentation. The CLI keeps its commands (`gorex remote tailscale on|off`, `gorex remote relay`).

### The three clients

| | Desktop | Rex Go (iOS) | Web |
| --- | --- | --- | --- |
| Speaks | the session protocol, 7464 | Rex's API, 7297 | Rex's API |
| Device key | `remote-client.json` | Keychain | none today |
| Paired by | pairing window, and by address | QR code, then the window, and by address | — |
| Tailcat | yes | yes (IOS-002-tailcat.md) | not yet |
| The machine as relay | new, no client change | new, no client change | no: a browser cannot pin a certificate |
| Tailscale introduction | new | later: its node asks, as a desktop's | — |
| Other ways it keeps | none | the gateway on the tailnet (443), the local network | the gateway on the tailnet (443) through the page's node, the local network |

**Desktop.** The desktop gets everything above. What goes away is below.

**Rex Go.** It already has the device key, pairing and tailcat, on the port of the gateway. A machine that is its own relay is one more address in a `rexgo://tailcat` code. The engine dials it as any tailcat address. Add Host by hand takes an IP address too. Pair with GoRex has the engine make the request. The app shows the code. The user says that the codes match, and the machine approves. Then the app adds the host at the address that it got, as it adds a scanned host. Its built-in Tailscale stays for Rex servers, which have no tailcat, and for a GoRex machine reached on 443. Later, its node can ask for an introduction as a desktop's node does. Then a phone on the account needs no QR code.

**Web.** A browser has no sockets of its own and holds no device key today. So the page stays outside the identity layer for now, and says so. It reaches a gateway on the tailnet through its own node (who gets in: the Tailscale user). Or it reaches a gateway on the local network (who gets in: the networks). In both cases, a host must trust the origin of the page (REF-007-web-client.md). The way in for the web client is tailcat compiled to WebAssembly, which upstream has as an experiment. It is relayed only, with a key that the browser keeps and the same pairing. It needs a relay with a certificate that a browser accepts, so it cannot use the machine as its own relay. Not in these phases.

**Rex's own app** knows neither tailcat nor device keys. The HTTPS of the gateway on the tailnet and its local network listener stay for it, as they are. They are compatibility listeners outside this frame. Their settings move under Show Details.

### What goes away

On the desktop, once sessions leave the tailnet:

- In `internal/rex`: the `remote.tailnetDial` op (`server.go`), `relayTailnet`, `connectTailnet`, the links and slots of the tailnet, the `IsTailnetTarget` cases of `remoteClientFor` and of the dial (`remote.go`), and `Request.TailnetTarget` (`proto.go`).
- In the app: the `tailscale:` cases of `hosts.go` (`TailnetOpened`, `reconcileTailnet`), `hostsview.go` and `state.go`, and of the CLI (`internal/cli/commands.go`). `tailnetOnline` is already gone (#761 removed it).
- `tailscale:` targets as the owner of a session.
- The second switch with its own state.

What stays of `system_tailnet.go`: the snapshot of same-user peers, the probe, and the `WhoIs` checks, now in front of a key exchange.

## Alternatives

| Option | Why not |
| --- | --- |
| A TCP listener with a Noise handshake of the device keys | not needed, because the spike held. It would need a handshake of GoRex's own (`controlbase` fixes its prologue to Tailscale's). It would also need an address that carries the server's key, a second listener and limits before authentication |
| Pairing by address with today's fingerprint | it covers the client's key only, so somebody in the middle passes it. The code of both keys closes that |
| A longer code and no commitment | 12 characters from a plain hash of the keys need no exchange, but are too long to compare by eye |
| Sessions over the tailnet as a second path | a second identity for a network tailcat already covers |
| A GoRex account server that introduces devices | the coordinator again |

## Security

- **Admission** is the allowlist alone, on every address. The relay of the direct address admits nobody. It only passes packets between keys, as any DERP server does.
- **The address is a secret** (the key of the server and the pre-shared key). The introduction sends it over the tailnet to a device that passed the check.
- **A relay sees keys, ciphertext, and the timing and size of the traffic.** This holds for a public relay too.
- **The Tailscale rule widens trust** from devices that the user approved one by one to every device of an account (Identity). It is off by default. An update never turns it on.
- **A machine that is its own relay opens a port** to whoever can dial the address.
  - It serves the protocol of the relay to the keys on the allowlist, and to any key only while the pairing window is open. So it relays for nobody else.
  - It serves pairing by address while that window is open.
  - Reaching the session server still needs an allowed key and the secret of the address.
  - The listener limits connections, for each source and in all.
- **Pairing by address rests on the comparison.** A user who approves without comparing the code lets in whoever asked.
  - The dialog shows the code large, with the name and the address that the request came from. Approve is not the default button.
  - Approve takes one request by address at a time, alone. The client keeps nothing before its own user says that the codes match.
  - A client that finds the codes or the answer wrong says to remove the newest device of the machine. The machine may have let in somebody in the middle.
- **Revocation** closes the connections of a key on every address at once, as today for the two ports of tailcat.

## Migration

- An update turns nothing on. A machine with Tailscale discovery on and remote access off is asked once, in Settings, before the listener starts. The rule needs the listener. The keys that it adds then work from outside the tailnet too.
- A host saved by its `tailscale:` target introduces itself the next time the user connects it. The app then saves it by the address.
- The target owns a session that a tailnet peer opened. The owner is a label, not a permission. After the update the label is gone and the session stays.
- A machine of a version before the introduction speaks the relay of the tailnet only. Until both sides are updated, two machines connect over the tailnet only when both still have the relay.

## Phases

Each phase ships something that a user can use.

1. **Spike** (done): the machine as its own relay, with tailcat as it is. It holds. See Path for what it found.
2. **Reachable at** (built: #495, #498, #501, #507, #511) covers these parts. They are the server and its CLI. They are pairing by address, the Settings and Add Remote Host of the desktop, and Add Host of Rex Go. It adds the setting, the second address and its QR code, and pairing by address. Desktops and Rex Go connect over a local network or a VPN of one's own with no relay. They connect from an IP address that the user types in. Docs.
3. **Settings in the new words** (built in the Settings of the desktop: #541): one switch, Devices, Add a device, Show Details. No change in behavior.
4. **Tailscale introduction** on the system client (Linux): the exchange, entries with their node, the snapshot rule, refused nodes. The relay of the tailnet still works beside it. Not built.
5. **The built-in node** carries it on Windows and macOS (DSK-005-tailnet.md). The built-in node is built (#573). It carries no introduction, because phase 4 is not built.
6. **Sessions leave the tailnet**: What goes away, and Migration. Not built.
7. Later, each with its own document: the introduction of Rex Go, the key of the web client, a public relay.

## Open questions

- **Changing the relay changes the address.** With several addresses, a server can tell a connected client the new address before the old address goes.
- **Which route a connection takes** is settled: `DiscoPing` reports the pong's path (Surface, "As built"). A ping's answer is the path of the moment, not a promise. A route can change after it.
- **The protocol of the embedded relay on a port forward**: DERP rides HTTPS, so a forward of one TCP port should do. The direct route needs UDP as well. Not checked.
- **Other firewalls** than Windows Firewall (the firewall of macOS, a third-party firewall, the firewall of the network) may block the port silently. Only Windows Firewall is checked (as built, below **Reachable at**).
- **The fixed port.** `RemotePort` (7464) on the own stack of the machine is the candidate. The system Tailscale mode on Linux listens on that port of the Tailscale address. So the two meet only if **Reachable at** is that address or every address.
- **Linux**: system client or built-in node, as in DSK-005-tailnet.md.
