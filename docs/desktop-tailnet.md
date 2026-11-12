# Desktop: Tailscale discovery over built-in Tailscale

Status: proposal. Not implemented. [host-links.md](host-links.md) sets
the frame: if it is accepted, the node of this document carries only
the introduction, and Transport's listen and dial for sessions, Names
and Mixed tailnets are revised to match.

## Problem

**Tailscale discovery and connections** (Settings › Server, or
`gorex remote tailscale on`) lets the GoRex desktops of one Tailscale
user find each other and open terminals, with no pairing. It runs on the
system's Tailscale client (`internal/rex/system_tailnet.go`): it reads
the client's status, checks every connection with its `WhoIs`, and binds
its sockets to the Tailscale interface.

That leaves two gaps:

- **Windows has no such mode.** `systemTailscaleClient()` returns nil
  there (`system_tailnet_other.go`): a socket bound to an address alone
  does not keep other interfaces out, and the receive side's isolation
  on Windows is not verified. The switch still shows, and its hint says
  "System Tailscale is unavailable. GoRex will try again automatically",
  which never comes true.
- **The mode needs the Tailscale app**, installed, signed in and
  running. Rex Go and the web client no longer do: each joins the
  tailnet itself ([ios-tailnet.md](ios-tailnet.md),
  [web-tailscale.md](web-tailscale.md)). The desktop already has the
  same engine, the gateway's built-in Tailscale (`internal/tailnode`,
  `internal/rexapi/tailnet.go`), but it serves only Rex's API on
  port 443.

## Goal

On Windows and macOS the mode runs over the gateway's built-in Tailscale
node: the same switch, the same Remote Hosts entries, the same identity
rule, no Tailscale app. The user signs the node in once, in a browser.

Windows has no other way. macOS leaves the system client by choice: the
mode there starts the Tailscale app's CLI as a process for every status
read and every `WhoIs` (`system_tailnet_darwin.go`), and needs the app
installed and its VPN on. Linux keeps the system client for now
(Open questions).

## Non-goals

- **System Tailscale on Windows.** It needs the app the user wants to do
  without, and the interface isolation work.
- **A second node.** The desktop mode uses the gateway's node, so one
  machine is one extra device on the tailnet, signed in once, for Rex
  Go, the web client and the desktops.
- **A new protocol.** The probe and relay greetings, port 7464, the
  `tailscale:<scope>:<node>` targets and the server's `handle` stay.
- **Changing who gets in.** Same user, untagged, not expired, not
  shared: `tailnetSnapshot.verifies` as it is.
  `--allow-tailnet-members` is the gateway's, for port 443; it does not
  widen port 7464.
- **Tailcat.** It stays the way with no Tailscale account at all.

## Design

### Transport

The broker (`systemTailnet`) uses the system client for four things. They
move behind one interface in `internal/rex`:

| Need | System client (today) | Built-in node |
| --- | --- | --- |
| Status with peers | `tailscale status --json` or the local client | `Node.StatusWithPeers` |
| Who a peer is | the client's `WhoIs` | `Node.WhoIs` |
| Listen on 7464 | `tcp4` on the Tailscale address, bound to its interface | `tsnet.Server.Listen("tcp", ":7464")`, new in `tailnode` |
| Dial a peer | a socket bound to the interface | `Node.Dial` |

The snapshot, the discovery passes, the admission checks, the slots, the
links and their revocation are the broker's and do not change. Two rules
of the snapshot are the system transport's only and move with it:
`st.TUN` (false for `tsnet`) and the lookup of the interface that holds
the address.

Why the built-in node needs no interface isolation: its listener is in
`tsnet`'s userspace network stack. Nothing on the machine's own
interfaces reaches it, so only a packet that came through the tailnet
can open a connection. A test checks this (Test plan).

`Node.Dial` dials an address that is not a known peer's through the
system's network (`tailnode.go`). The broker dials only an address its
snapshot holds, after a `WhoIs` of it, and checks the connection's peer
again before a relay, as today; the built-in transport refuses any other
address itself.

### Reaching the node

`internal/rex` cannot import `internal/rexapi`. The gateway runs in the
server's process (`rex.OnServe = rexapi.Supervise`), so a hook is enough,
set in `main.go` beside the others:

```go
// TailnetNode returns the gateway's running built-in Tailscale node, or
// nil while it is off.
rex.TailnetNode = rexapi.DesktopNode
```

The broker asks for it on every refresh. No node, a node that is not
signed in, or a tagged node, is "unavailable" with a reason. A node
other than the last one (off and on, signed out and in) drops the
listener and every link, as a changed binding does today.

A gateway of its own (`gorex rexapi`, for development) is another
process: the mode is unavailable beside it.

### Which transport

One at a time, chosen when the mode starts:

1. Windows and macOS: the built-in node, always. A system Tailscale
   client on the machine is not used, installed or not.
2. Linux: the system client, today's behavior, unchanged.

`system_tailnet_darwin.go` and its CLI calls go away.

### Turning it on

The switch keeps its place and its setting (`remote.json`'s `tailnet`).
On Windows and macOS:

- Turning it on with built-in Tailscale off turns that on too
  (`tailscale.json`'s `enabled`, as `gorex rexapi tailscale on`), which
  starts the gateway (`Wanted`).
- Under the switch, a status line follows the node:
  - **needs sign-in**: a **Sign in** button opens the node's auth URL in
    the browser. The app reads it over the gateway's local socket, the
    only place it is shown (#289).
  - **needs approval**: says the tailnet's admin has to approve the
    device.
  - **running**: "Signed in as NAME", and the device's name.
  - **tagged** or another reason: the broker's error, as today.
- The hint "System Tailscale is unavailable…" is no longer shown
  there.

Turning built-in Tailscale on also serves Rex's API on port 443 to the
same user's devices, as it does today. The switch's note says so
(Open questions).

### Migration on macOS

A Mac with the mode on moves from the system client to the built-in
node with the update:

- The mode waits for a sign-in of the node, unless built-in Tailscale
  was signed in already. Settings shows **Sign in**; nothing is served
  on 7464 until then.
- The Mac is another device now (`<machine>-gorex`), so its target
  changes: the scope holds the node's ID. Peers list it as a new host,
  and the entry for the old device stops answering. `TailnetStatus.Owns`
  already tells a target of another scope apart.
- The Tailscale app stays as the user has it; GoRex no longer starts
  its CLI.

`gorex remote status` adds the transport (`system` or `built-in`) and,
for the built-in one, the node's state.

### Names

A built-in node's host name is `<machine>-gorex`. Remote Hosts shows it
without the suffix, as Rex Go does (`Host.name`).

### Mixed tailnets

By design, a desktop on the built-in node and one on the system client
(Linux, or a Mac of a version before this) find each other: each is an
untagged device of the same user to the other, port 7464 answers on
both, and `WhoIs` confirms both ends. This is not verified until two
machines run it (Test plan).

A Mac with the Tailscale app has two devices on the tailnet, the app's
and the node's. Only the node answers on 7464, so a peer lists the Mac
once.

## Security

- **Admission** is unchanged: same user, untagged on both ends, not
  expired, not a shared-in device, checked at accept, at dial and again
  on the connected socket.
- **Reach**: the listener exists only in the node's network stack. The
  tailnet's access rules must allow TCP 7464 between the user's devices,
  as for the system mode.
- **The sign-in link** binds the device to whoever opens it first. It
  stays on the local socket; the button opens it only from the app.
- **The node's state** (`rexapi/tsnet`) holds its key. On Windows the
  directory's mode bits do nothing: the profile's ACL is the boundary,
  as for the gateway's socket today.
- **Revocation**: removing the device in Tailscale's admin console ends
  the node's session; the broker drops its links after three failed
  reads, as today. Turning the switch off closes them at once.

## Alternatives

| Option | Why not |
| --- | --- |
| System Tailscale on Windows | needs the Tailscale app; isolation on the receive side unverified |
| A node of the desktop mode's own | two devices and two sign-ins per machine |
| Desktops talk Rex's API on 443 | the desktop is not a Rex API client for hosts; a second protocol for the same job |
| Tailcat only | works today with no account, but each pair of machines is paired by hand, with no discovery |

## Test plan

- The broker's tests run against both transports with a fake node:
  discovery, admission, relay, revocation, a node replaced.
- `tailnode`: a listener on the node takes no connection from the
  machine's own interfaces.
- Existing `system_tailnet` tests pass unchanged on Linux.
- Windows with macOS, neither with the Tailscale app: sign in, each
  lists the other, open and reattach a terminal.
- A built-in node with Linux (system), both ways.
- A Mac with the mode on before the update: asks for a sign-in after
  it, and its peers list the new device.
- Refusals: another user's device, a tagged device, a LAN address on
  port 7464.
- No Windows Firewall prompt when the mode starts.

## Phases

1. **Transport interface**: the broker behind it, the system transport
   as the only one. No change in behavior.
2. **Built-in transport**: `tailnode`'s listener, the hook, the choice
   of transport, `gorex remote status`.
3. **Settings**: the status line, **Sign in**, the notes and their
   translations, names without `-gorex`.
4. **Docs**: README's Remote access, `docs/rex-api.md`.

Until phase 3, a one-line fix can hide the switch on Windows, so the
hint that never comes true is gone at once.

## Open questions

- **Linux.** The built-in transport would work there too. Keep the
  system client, offer the node when no client is installed, or move
  Linux as well? Moving it removes the system transport, and with it
  the transport interface and the interface binding.
- **Port 443 with the switch.** Should the desktop mode be able to run
  the node without serving Rex's API on it? That splits "the node is on"
  from "the gateway serves on it" in `tailscale.json`.
- **Hostinfo.** `web-tailscale.md` proposes `hostinfo.SetApp("gorex")`
  for the gateway. With it, discovery could probe those peers first
  instead of eight at a time.
