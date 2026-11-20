---
name: DSK-005
description: "The desktop joins a tailnet with built-in Tailscale, and Tailscale only introduces devices. Use when you change the desktop tailnet node or introductions."
metadata:
  created: "2026-11-12"
  updated: "2026-11-20"
---

# DSK-005: Desktop: Tailscale introductions over built-in Tailscale

Status: phases 1 to 4 built (#573, #716). The introduction exchange is not built.

This is phase 5 of [NET-002-host-links.md](NET-002-host-links.md) and follows its frame. A device is its key, sessions run over tailcat, and Tailscale only introduces. This design builds on phase 4 of that document, the introduction itself. It was planned to ship after that phase. The built-in node shipped first, with one deviation. The probe and relay exchanges stay. Sessions still run over relay through the node. The introduction of NET-002-host-links.md will replace relay later. The text of Design describes the introduction as the plan, and relay as the code of today.

## Problem

**Tailscale discovery and connections** (Settings › Server, or `gorex remote tailscale on`) lets the GoRex desktops of one Tailscale user find each other and open terminals, with no pairing. Before #573 it ran on the system Tailscale client only (`internal/rex/system_tailnet.go`). It reads the status of the client. It checks every connection with the `WhoIs` of the client. It binds its sockets to the Tailscale interface.

That left two gaps:

- **Windows had no such mode.** `systemTailscaleClient()` returned nil there (`system_tailnet_other.go`). A socket bound to an address alone does not keep other interfaces out. The isolation of the receive side on Windows is not verified. The switch still showed, and its hint said "System Tailscale is unavailable. GoRex will try again automatically". This never came true.
- **The mode needed the Tailscale app**, installed, signed in and running. Rex Go and the web client no longer need it, because each joins the tailnet itself ([IOS-001-tailnet.md](IOS-001-tailnet.md), [WEB-002-tailscale.md](WEB-002-tailscale.md)). The desktop already has the same engine, the built-in Tailscale of the gateway (`internal/tailnode`, `internal/rexapi/tailnet.go`). But that engine serves only the API of Rex on port 443.

## Goal

On Windows and macOS, GoRex sees the other devices of the user. It makes the introduction of NET-002-host-links.md through the built-in Tailscale node of the gateway, with no Tailscale app. The user signs the node in once, in a browser.

Windows has no other way. On macOS, GoRex leaves the system client by choice. The old mode there started the CLI of the Tailscale app as a process for every status read and every `WhoIs` (`system_tailnet_darwin.go`, removed in #573). It needed the app installed and its VPN on. Linux keeps the system client for now (see Open questions).

## Non-goals

- **System Tailscale on Windows.** It needs the app that the user wants to do without. It also needs the interface isolation work.
- **A second node.** The introductions use the node of the gateway. So one machine is one extra device on the tailnet, signed in once, for Rex Go, the web client and the desktops.
- **Sessions over the tailnet.** The node carries the introduction and nothing after it (NET-002-host-links.md, Path).
- **Changing who is introduced.** The rule stays: same user, untagged, not expired, not shared. This is `tailnetSnapshot.verifies` as it is. `--allow-tailnet-members` belongs to the gateway, for port 443. It does not widen port 7464.

## Design

### What the node does

The broker (`systemTailnet`) keeps its snapshot of same-user peers, its discovery passes and its checks. On port 7464 it speaks two exchanges:

- **probe**, as today: is this peer a GoRex machine.
- **introduce**, new, in place of **relay**. This exchange is not built, and relay stays in the code today. The client sends its device key and name. The server calls `WhoIs` for the connection and checks its current snapshot. Then the server puts the key on its list with the node ID of the peer, and answers with its addresses. The connection closes. The exchange and its rules belong to NET-002-host-links.md (Identity).

The rule needs the listener. With remote access off there is no address to answer with, and the introduction says so. Settings asks before it turns the listener on (NET-002-host-links.md, Migration).

The broker needs four things from Tailscale. They move behind one interface in `internal/rex`:

| Need | System client (Linux) | Built-in node (Windows, macOS) |
| --- | --- | --- |
| Status with peers | the local client | `Node.StatusWithPeers` |
| Who a peer is | the client's `WhoIs` | `Node.WhoIs` |
| Listen on 7464 | `tcp4` on the Tailscale address, bound to its interface | `tsnet.Server.Listen("tcp", ":7464")`, new in `tailnode` |
| Dial a peer | a socket bound to the interface | `Node.DialPeer` |

Two rules of the snapshot belong to the system client only, and move with it. These are `st.TUN` (false for `tsnet`) and the lookup of the interface that holds the address.

Why the built-in node needs no interface isolation: `Listen` of `tsnet` "announces only on the Tailscale network". Its listener is in the userspace network stack of the node. Nothing on the own interfaces of the machine reaches it. A test checks this (see Tests).

`Node.Dial` dials an address with no route on the tailnet through the network of the system (`tailnode.go`, `tsdial`). A `WhoIs` of the address says nothing of the way the bytes took. So `tailnode` has a dial of its own for this, `DialPeer`. It fails unless the address belongs to a peer in the network map of the node. The client gets back an address for a server key. The session that follows proves that key over tailcat, so a wrong answer connects nothing.

### Reaching the node

`internal/rex` cannot import `internal/rexapi`. The gateway runs in the process of the server (`rex.OnServe = rexapi.Supervise`), so a hook is enough. `main.go` sets it beside the others:

```go
// TailnetNode returns the gateway's running built-in Tailscale node, or
// nil while it is off.
rex.TailnetNode = rexapi.DesktopNode
```

The broker asks for the node on every refresh. These cases are "unavailable" with a reason: no node, a node that is not signed in, or a tagged node. A node other than the last one (off and on, signed out and in) drops the listener.

A gateway of its own (`gorex rexapi`, for development) is another process. Introductions are unavailable beside it.

### Which side

1. Windows and macOS: the built-in node, always. GoRex does not use a system Tailscale client on the machine, installed or not.
2. Linux: the system client, the behavior of today, unchanged.

#573 removed `system_tailnet_darwin.go` and its CLI calls.

### Turning it on

The rule of NET-002-host-links.md, **Devices on my Tailscale account connect without pairing**, keeps the setting of the switch of today (`tailnet` in `remote.json`). On Windows and macOS:

- Turning the rule on with built-in Tailscale off turns built-in Tailscale on too (`enabled` in `tailscale.json`, as `gorex rexapi tailscale on` does). This starts the gateway (`Wanted`). It never sets `allow_tailnet_members`. A node that the rule turned on lets in the user who signed it in, and nobody else, on port 443.
- Under the rule, a status line follows the node:
  - **needs sign-in**: a **Sign in** button opens the auth URL of the node in the browser. The app reads the URL over the local socket of the gateway, the only place that shows it (#289).
  - **needs approval**: says that the admin of the tailnet must approve the device.
  - **running**: "Signed in as NAME", and the name of the device.
  - **tagged** or another reason: the error of the broker, as today.
- The hint "System Tailscale is unavailable…" no longer shows there.

Turning built-in Tailscale on also serves the API of Rex on port 443 to the devices of the same user, as it does today. Settings says so when the user turns the rule on, not only in a note (see Open questions).

The CLI keeps its command, `gorex remote tailscale on|off`.

`gorex remote status` tells which side carries the introductions (`system` or `built-in`). For the built-in side it also tells the state of the node.

### Names

The host name of a built-in node is `<machine>-gorex`. A machine that GoRex finds and does not yet connect shows without the suffix, as Rex Go does (`Host.name`). Once introduced, it is a saved host with the own name of the machine.

### Other versions and sides

- A built-in node and a system client introduce each other. Each is an untagged device of the same user to the other. Port 7464 answers on both, and `WhoIs` checks both ends. This is not verified until two machines run it (see Tests).
- A machine of a version before the introduction knows probe and relay only. A built-in node has no relay. So a Windows or macOS machine and a machine of an older version do not connect over the tailnet until the older one is updated. Tailcat pairing works between them meanwhile.
- A Mac with the Tailscale app has two devices on the tailnet, the one of the app and the one of the node. Only the node answers on 7464, so a peer finds the Mac once.

## Alternatives

| Option | Why not |
| --- | --- |
| System Tailscale on Windows | needs the Tailscale app, and the isolation on the receive side is unverified |
| A node of the desktop's own | two devices and two sign-ins per machine |
| Desktops talk the API of Rex on 443 | the desktop is not a client of the Rex API for hosts, and this adds a second protocol for the same job |

## Security

- **Introduction** works only for a peer that the snapshot has (same user, untagged, not expired, not shared in). `WhoIs` must also check the peer (same user, untagged, not shared in, that node). This holds at accept and at dial. What the introduction adds to the list, and when that entry goes, belong to NET-002-host-links.md.
- **Reach**: the listener exists only in the network stack of the node. The access rules of the tailnet must allow TCP 7464 between the devices of the user, as today.
- **The sign-in link** binds the device to the first person who opens it. The gateway shows it on the local socket only, and the button opens it only from the app. `tsnet` also writes it to its user log. That log ends in the log of the server, a file of this user only. This is the same boundary.
- **The state of the node** (`rexapi/tsnet`) holds its key. On Windows the mode bits of the directory do nothing. The ACL of the profile is the boundary, as it is today for the socket of the gateway.

## Migration

A Mac with the mode on moves from the system client to the built-in node with the update:

- The update never turns built-in Tailscale on, because it serves the API of Rex on port 443 over the tailnet. The user opts in. A node that the user turned on before stays on. A node that the user never turned on stays off. Settings says so under the switch, with **Turn on** and what the node serves. Nothing answers on 7464 or 443 until then. The same holds on Windows.
- Once the user turns the node on, the rule waits for a sign-in of the node, unless built-in Tailscale was signed in already. Settings shows **Sign in**.
- The Mac is another device now (`<machine>-gorex`). A peer that saved it by its `tailscale:` target finds it again as an available machine. One **Connect** introduces it (NET-002-host-links.md, Migration).
- The Tailscale app stays as the user has it. GoRex no longer starts its CLI.

## Tests

- The tests of the broker run against both sides with a fake node: discovery, introduction, refusals, a node replaced.
- `tailnode`: a listener on the node takes no connection from the own interfaces of the machine.
- The existing `system_tailnet` tests pass unchanged on Linux.
- Windows with macOS, neither with the Tailscale app: sign in, each finds the other, Connect, open and reattach a terminal.
- A built-in node with Linux (system), both ways.
- A Mac with the mode on before the update: built-in Tailscale stays off after it. After **Turn on** it asks for a sign-in, and its peers find the new device.
- Refusals: a device of another user, a tagged device, a LAN address on port 7464.
- No Windows Firewall prompt when the node starts.

## Phases

1. **Interface**: the four needs of the broker behind it, the system client as the only side. No change in behavior. Built (#573).
2. **Built-in node**: the listener of `tailnode`, the hook, the choice of side, `gorex remote status`. Built (#573).
3. **Settings**: the status line, **Sign in**, the notes and their translations, names without `-gorex`. Built (#573, #716).
4. **Docs**: README's Remote access, `docs/REF-005-rex-api.md`. Built (#573, #716).

Before these phases shipped, a one-line fix could hide the switch on Windows, so the hint that never comes true was gone at once.

## Open questions

- **Linux.** The built-in node would work there too. Options: keep the system client, offer the node when no client is installed, or move Linux as well. Moving Linux removes the system side, the interface above and the interface binding.
- **Port 443 with the rule.** Should the node be able to run without serving the API of Rex on it? That splits "the node is on" from "the gateway serves on it" in `tailscale.json`.
- **Hostinfo.** `WEB-002-tailscale.md` proposes `hostinfo.SetApp("gorex")` for the gateway. With it, discovery could probe those peers first instead of eight at a time.
