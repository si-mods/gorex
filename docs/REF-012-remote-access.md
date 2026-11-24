---
name: REF-012
description: "Remote access to the session server: Tailscale discovery, tailcat pairing and keys, remote hosts and teleport. Use when you change pairing, `hosts.json`, the remote protocol or remote settings."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-012: Remote access

Status: reference. It describes the code in main.

Remote access is off by default. This doc describes the two ways in (Tailscale discovery and tailcat), the hosts this machine connects to, and teleport, which moves a pane's work to a host. The model behind it is [NET-002](NET-002-host-links.md); the web client is a remote client too ([REF-007](REF-007-web-client.md)).

## Tailscale discovery (desktop)

On Linux, GoRex uses an existing, running system Tailscale client to
discover GoRex hosts and connect to their terminals. On macOS and
Windows, it uses the gateway's built-in Tailscale node instead (no
Tailscale app), which you sign in once in a browser (click **Sign in** in Settings ›
Server). Devices with the
same Tailscale user in the same tailnet appear in Remote Hosts once they
have a compatible GoRex server and Tailscale policy allows TCP port
7464. Click Connect on a host to open or reattach to a session. The
server only listens on its Tailscale interface, or in the built-in
node's own network stack, and checks each connection's device identity
with Tailscale.

Turn **Tailscale discovery and connections** on in Settings › Server, or
use `gorex remote tailscale on`; on macOS and Windows this also turns
built-in Tailscale on, which serves Rex's API on port 443 to your own
devices. An upgrade never turns built-in Tailscale on: with the mode on
from an earlier version, it stays off until you click **Turn on** under
the switch or run `gorex remote tailscale on` (or `gorex rexapi tailscale
on`). Built-in Tailscale that you turn off later stays off over
restarts; **Turn on** starts it again. The mode is off by default and waits when Tailscale is
unavailable. `gorex remote status --json` reports the local mode, the
side that carries it (`system` or `built-in`), the built-in node's state
and the hosts it has discovered.

## Tailcat

The session server can also listen on a
[tailcat](https://github.com/tailscale/tailcat) address: Tailscale's data
plane without its control plane, so WireGuard end to end, through a DERP
relay or directly, with no Tailscale account. Remote access is off by
default. Each machine has a client key (`remote-client.json`), and the
server serves the keys of its allowlist only: it reads the key of every
connection, and closes those of other keys at once, saying first that
they are not allowed (so that a client tells a refusal from a network
that dropped). With an empty
allowlist nobody gets in. The server keeps its keys (`remote.json`, mode
0600), so its address stays the same; regenerating them makes a new one,
and the old address stops working.

Pairing adds a client without copying its key by hand:

1. On the server, turn remote access on and Start pairing: a window
   opens for five minutes.
2. On the client, ask to pair with the server's address and a name
   (`remotectl pair ADDRESS alice-laptop`). It prints a fingerprint, as
   `01234567-cdef`.
3. The request shows on the server with its name and fingerprint.
   Approve it only if the fingerprint is the one the client printed:
   anyone with the address can ask too.

While the window is open, a client not allowed may ask to pair and
nothing else; at most 8 requests wait, one per key, and they are
forgotten when the window closes. They are never saved.

What to know before turning it on:

- The address is a secret: it holds the server's key and the WireGuard
  pre-shared key. Do not paste it where others read it. GoRex never logs
  it, nor any key.
- The allowlist is what keeps others out. A leaked address alone gets
  nobody in, but it lets anyone ask to pair while a window is open:
  compare fingerprints, and regenerate a leaked address anyway.
- An allowed key means running commands as this user: a client may
  start any program with any environment. From a shell there it can
  reach the local socket, and so change the layout and remote access
  itself. Trust it as you would an SSH key.
- Every allowed client sees and uses every session: this machine's
  window's and other clients'. The server remembers which key created a
  session, in memory, and keeps it across an in-place upgrade. A session a client opens
  here gets a tab in this window too, last among this machine's tabs and
  without taking the focus, rather than running unseen.
- Over the connection itself, a remote client may list, create, kill,
  resize and attach to sessions only. It may not shut the server down,
  read or set the window layout, or see or change remote access
  (`remote.*`, pairing included): those are for the local socket only.
- Every connection is limited: a line of requests of at most 1 MiB, a
  terminal of 1 to 1000 columns and rows, and 8 kills at once.

The same address also serves Rex's API on its port, 7297, for Rex Go
(see [IOS-002](IOS-002-tailcat.md)). Rex's API commands
the layout, as a client may over 7464 too: an allowed key gets it; any
other gets nothing there, and pairing stays on 7464. The session server hands each such connection to the Rex API
gateway, which it runs while remote access is on, on the gateway's
socket (`rexapi-tailcat.sock`, this user's only); with no gateway there,
the connection closes and the client dials again. Remove, or remote
access turned off, closes these connections.

The address names the DERP relay where clients meet the server when
they cannot connect directly: by default one of tailcat's free relays,
picked once. A relay of one's own takes its place: **Relay** in Settings
› Remote, or `gorex remote relay derp.example.com` (`gorex remote relay
default` goes back); `gorex remote relay gorex` names the relay the team
runs. Give a host name, with `:PORT` when not 443, and not
an IP address: `derper` serves a certificate for its host name. The
relay is Tailscale's `derper`, on any machine clients can reach:

```sh
derper --hostname=derp.example.com
```

with 443/TCP and 3478/UDP open. It sees
keys and ciphertext only. Changing the relay changes the address (the
keys stay), so clients need the new one; it is kept in `remote.json`.

## Remote hosts

Remote clients pair by name and keep their key and created/last-seen
times; a client's hello names its principal, `gorex clients` lists the
live attachments with their principal and age, and `gorex ls` shows the
principal that owns each session. Settings › Server can share this
window's layout read-only, and give the allowed clients all sessions or
each only the sessions it opened; the shared layout is filtered to
sessions that client may use; panes it may not use are shown as unavailable placeholders, and
panes of other hosts are left out; `gorex -S HOST layout`
reads it and `events` follows changes.

A session attached by several clients takes the size of the one the
user's focus is on, if that machine had input in the last minute; see
[REF-003](REF-003-session-size.md).

For a Linux service shared by several paired clients, see
[REF-004](REF-004-shared-server.md). Per-user isolation and
PAM login are a separate proposal: [NET-001](NET-001-login-server-rfc.md).

Settings › Remote Hosts connects this machine to other machines' session
servers, whose sessions then open in tabs of the window beside its own.
Add Remote Host… asks for the host's address (the one its Settings ›
Remote shows; the field hides it), a name for it, and the name this
machine pairs as (the computer's name, at first):

1. On the host, turn remote access on and Start pairing.
2. On this machine, Add Remote Host… with its address. A host that allows
   this machine already is saved at once; otherwise GoRex asks to pair
   and shows this machine's fingerprint.
3. On the host, approve the request that shows the same fingerprint. The
   sheet tries again every three seconds and saves the host once it is
   let in.

The hosts are kept in `hosts.json` beside the settings, readable by the
user only, and not in `settings.json`: their addresses are secrets, which
GoRex never logs nor shows. Each host's row shows its state (Connecting,
Connected, Reconnecting (attempt N), or why it is not connected) with
Retry, which tries at once, and Remove. Removing a host closes its tabs
in the window and leaves its sessions running on the remote server.

GoRex keeps a connection to each host and connects again when it drops,
waiting longer after each failure, up to 30 seconds. New Tab opens on the
active tab's host, and a split on its pane's; New Tab on a host (the
command palette, or the + of a host's name in the sidebar) picks one.
Closing a tab or a pane ends its session on its host, once connected
again if it is not now. With hosts saved, the sidebar lists
the tabs under their host's name, this machine's first, and tabs above
show a host's name on its tabs. A pane whose host is away keeps its
screen under "Reconnecting to …", drops what is typed meanwhile, and
attaches again by itself; one whose session ended there closes. The
window's layout keeps the host of each pane, so a host's tabs come back
when GoRex opens again, attached once the host answers. Sessions the host
lists that no tab shows (opened in the host's own window, or by another
client) open last among the host's:
one tab for each session, or, while the host shares its layout, one tab
for each tab that layout shows, with its splits, so two panes side by
side there are not two tabs here; a tab closed here ends its session as
any other and does not come back.
`go run ./tools/remotectl pair ADDRESS NAME` asks to pair from a machine
without the app; with the IP address of a host's Reachable at in place of
ADDRESS, it pairs by address: it prints a code to compare with the
host's, and the host's addresses once approved.

## Teleport

Teleport to… (the pane's menu, or Teleport to a host in the command
palette) moves a pane's work to a connected host: the Git work tree's
commits, changes and untracked files, Claude Code's or omp's conversation, and the
scrollback. The host makes a work tree of its clone of the repo,
`.gorex/teleport/<id>` on a branch `teleport/<id>`, or, for work coming
back to a directory it left untouched, takes it there again; and starts
`claude --resume` or `omp --resume` there, or a shell; the pane then shows that session, in
a tab of the host's, and its own session ends. Only a shell at its prompt
or Claude Code or omp waiting for the user can go, from this machine;
`gorex teleport PANE HOST` does the same from the command line. Dragging
a tab of one pane onto another host's tabs, or its name in the sidebar,
asks to teleport it there. See [DSK-003](DSK-003-teleport.md).
