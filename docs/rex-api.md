# Rex's apps

Rex's own apps, the iPhone one among them, connect to GoRex as they
connect to a Rex server. `GoRex rexapi` serves Rex's API, revision 12, from
the session server the GoRex app runs: the same WebSocket endpoints, the
same messages, the same errors. Rex's apps need no change.

## How it works

`GoRex rexapi` is a gateway. It connects to the GoRex server of this
machine (it starts none), reads the layout the app keeps there, and shows
it to Rex clients in Rex's terms:

| Rex | GoRex |
| --- | --- |
| session | space |
| window | tab |
| block | pane with a terminal of this machine |
| floating layer | floating pane |
| owner of a block | the client that holds the session's size |

- **Terminals** stream as Ghostty snapshots (GHOSTSNP), then what the
  program prints, as on a Rex server. The GoRex server takes the snapshot
  from the emulator it keeps for each session, so a full-screen program
  comes back exactly.
- **Typing** goes through the server, which encodes each key, the pointer
  and the focus with the modes the program set (cursor keys, the Kitty
  keyboard protocol, bracketed paste, mouse reports).
- **Size.** A client that owns a block sizes its terminal, through the
  server's size lock: the desktop shows the pane at that size meanwhile.
  The lock is renewed while the client stays, and given back when it
  leaves. A desktop pane that types takes the size back: the block then
  has no owner (`block_owner_changed`, reason `taken`) until the client
  that took it types again (reason `input`).
- **Layout changes** (new window, split, close, rename, focus, zoom, swap,
  move, resize, and GoRex's own move of a window to another session) the
  GoRex server makes itself; an open window shows them at once.

The GoRex server runs the gateway itself while remote access, the local
network listener or built-in Tailscale is on, and again after it
restarts or upgrades: nothing else needs to run. `GoRex rexapi` runs one of its own,
for development; the two never run on the same data directory at once.

The gateway listens on five places:

- its local socket, `rexapi.sock` in GoRex's data directory, for this
  user only. The Rex CLI reaches it with `rex -S unix://PATH`, and the
  methods that administer the gateway (`server.tailscale.*`) are accepted
  only there, as on a Rex server;
- the local network, over plain HTTP, while that listener is on: only
  the networks it allows get in (below);
- the tailnet, over HTTPS, while built-in Tailscale is on;
- the session server's tailcat address, on Rex's port 7297, while remote
  access is on (below);
- with `GoRex rexapi --listen http://127.0.0.1:PORT`, a TCP port that lets
  everyone in unauthenticated: for tests on this machine only.

None serves a web page. A browser's request carries its page's origin,
and the gateway refuses it on every path, with 403 before the access
rule, unless the origin is one the host trusts (`gorex rexapi web allow
ORIGIN`, empty by default). This is how the standalone web client
(docs/web-client.md), hosted elsewhere, reaches a host. Native clients
send no origin and are not affected.

Before that, on the local network, the tailnet and `--listen`, the
gateway answers only a request whose `Host` is one of its own names, as
a defence against DNS rebinding (a page that points a name of its own at
the gateway's address sends no origin): `localhost`, an IP address (the
listener's own among them), built-in Tailscale's MagicDNS name and its
first label, or a name added with `gorex rexapi web host add NAME`, such
as one a reverse proxy in front of the gateway serves. Any other `Host`
gets `421 Misdirected Request` with a JSON error, on every path. The
local socket and tailcat are not checked: no browser reaches either.

## Local network

When the phone reaches this machine by its address on the local network,
directly or through a VPN to it, Rex's app connects without Tailscale.
Settings › Server › Local API › Serve Rex apps on the local network turns the listener on.
It listens on this machine's private address with Rex's port, such as
`192.168.88.119:7297`, and lets in that address's network, such as
`192.168.88.0/24`; change either there. In Rex's app, add the host
`http://192.168.88.119:7297`. The same from the command line:

```sh
gorex rexapi lan on                 # this machine's address and network
gorex rexapi lan allow 10.6.0.2     # the phone's VPN address, alone
gorex rexapi lan deny 192.168.88.0/24
gorex rexapi lan listen 0.0.0.0:7297
gorex rexapi lan status
```

A device in no network allowed gets 403. The refusal is in the server's
log with the device's address, which is how to find the one to allow.

Mind what this listener is:

- **Anyone in a network allowed gets a full shell as you.** Rex's API has
  no password on a plain TCP listener; the networks are the only check.
  Allow as little as you can: the phone's address alone (`/32`), or the
  VPN's network rather than the whole home network.
- **It is not encrypted.** What you type and what the terminals show
  cross the local network as plain HTTP. A VPN protects the way to the
  network, not the last hop in it. Built-in Tailscale encrypts end to
  end.

The settings live in `rexapi/lan.json` in GoRex's data directory; the
server's gateway follows a change within a second, without a restart.
**Settings › Server › Local API → Rex QR…** shows this gateway's pairing
address as a code for Rex to scan, as `gorex rexapi pair` does: the
built-in Tailscale address, `https://<machine>-gorex.<tailnet>.ts.net`.
The code holds the address only, not a credential, and the device that
scans it must be on the same tailnet. Until built-in Tailscale is on and
its HTTPS address is ready, the dialog shows the local network
listener's address instead, `http://<address>:7297`, for a device on the
same network: the listener below is unchanged, and Rex can also add it
by hand.

A session that a GoRex desktop of yours made on this machine (one of its
remote hosts' panes) and that no local tab shows is under **Desktop
terminals**. Opening a terminal there attaches to the existing process.
A desktop counts as yours when it reached this machine over system
Tailscale, which lets in only your own Tailscale user, or paired with it
over tailcat: every paired client sees every session.

## Built-in Tailscale

Like a Rex server, the gateway joins your tailnet as a device of its own,
with a copy of Tailscale inside it: the machine needs no Tailscale
installed, and the device does not clash with a Rex server's. Its name is
this machine's with `-gorex`, such as `studio-gorex`.

The tailnet needs MagicDNS and HTTPS certificates turned on (the admin
console's DNS page). By default the gateway lets in only the Tailscale
user who signed the device in; every other caller gets 403.
`--allow-tailnet-members` lets in everyone the tailnet's access rules
allow.

On Windows and macOS, the desktop's Tailscale discovery uses this same
device: turning **Tailscale discovery and connections** on in Settings
also turns built-in Tailscale on. Sign in with the button there.

## Tailcat

Rex Go reaches the machine over tailcat, at the address of GoRex's
remote access (README, Remote access): WireGuard end to end, directly or
through a DERP relay, with no Tailscale account and no VPN on the phone.
The gateway keeps no tailcat of its own. The session server listens on
port 7297 of its address beside 7464, and lets in only the clients that
may command its layout over 7464: the keys of its allowlist and its
network (an unknown key gets nothing; pairing stays on 7464). For each, it dials
the gateway's socket, `rexapi-tailcat.sock` in GoRex's data directory
(this user only), and writes a line with the client's key and name, then
the bytes. With no gateway there, the connection closes and the client
dials again.

A client over tailcat is identified as
`{"kind": "device", "transport": "tailcat", "display": "<client name>", "path": ["tailcat"]}`.
It gets Rex's API as the owner does over built-in Tailscale, layout
included, but not `server.tailscale.*`. Remove in Settings › Server › Devices,
its sessions no longer shared, or turning remote
access off closes its connections. `server.status` does not list this listener: its address is
a secret.

## Set up

1. Open GoRex.
2. Turn built-in Tailscale on; the GoRex server starts the gateway for it:

   ```sh
   GoRex rexapi tailscale on
   ```

   The first time, it prints a sign-in link: open it and sign in. Or sign
   in without a browser with an auth key:
   `GoRex rexapi tailscale on --auth-key-file key.txt` (the key is used
   once, and never saved or logged). A tailnet run by your own control
   server, such as Headscale, takes its URL:
   `GoRex rexapi tailscale on --control-url https://headscale.example.com`,
   or in Settings › Server, under the Tailscale switch. The URL must be `https`:
   the control traffic itself is encrypted, but the device fetches the
   server's key over the URL unauthenticated, so over `http` anyone on
   the path could swap the key. `http` is accepted only for `localhost`
   or a loopback, private (RFC 1918, `fc00::/7`) or link-local IP
   address, judged as written and not resolved; a name such as
   `http://headscale.lan` and Tailscale's own ranges (`100.64.0.0/10`,
   `fd7a:115c:a1e0::/48`) need `https`. A saved `http` URL that is
   refused keeps the device off, and `GoRex rexapi tailscale status` and
   Settings say why. Turn it off first to switch
   servers; an explicit empty `control_url`
   (`GoRex rexapi call server.tailscale.enable '{"allow_tailnet_members":false,"control_url":""}'`)
   or an empty field in Settings returns the device to Tailscale's own.
3. Wait for `https: available` in `GoRex rexapi tailscale status`.
4. Show the address as a QR code, and scan it with Rex on the phone:

   ```sh
   GoRex rexapi pair
   ```

   The code holds only the address, not a credential: the phone must be on
   the same tailnet, signed in as you.

Built-in Tailscale stays on across restarts of the gateway, with its
sign-in, until `GoRex rexapi tailscale off`. `tailscale logout` signs the
device out; `tailscale rename NAME` renames it.

## Commands

| Command | What it does |
| --- | --- |
| `GoRex rexapi [--listen URL] [--socket PATH] [--tailnet-dir DIR]` | a gateway of its own, for development; the server runs one while remote access, the local network or built-in Tailscale is on |
| `GoRex rexapi lan on \| off \| status` | the listener on the local network |
| `GoRex rexapi lan listen HOST:PORT` | the address it listens on |
| `GoRex rexapi lan allow NETWORK` / `deny NETWORK` | the networks let in: a CIDR, or an address alone |
| `GoRex rexapi web allow ORIGIN` / `deny ORIGIN` | the web pages trusted, by origin, such as `https://gorex-web.example.workers.dev`: `http(s)://host[:port]`, no path or wildcard; in `rexapi/web.json`, followed within a second (docs/web-client.md) |
| `GoRex rexapi web host add NAME` / `host remove NAME` | the host names the gateway answers besides `localhost`, IP addresses and built-in Tailscale's names: a DNS name, no port or wildcard; in `rexapi/web.json` |
| `GoRex rexapi web status` | the web pages trusted, and the host names added |
| `GoRex rexapi tailscale on [--hostname NAME] [--control-url URL] [--allow-tailnet-members] [--auth-key-file FILE]` | turn built-in Tailscale on |
| `GoRex rexapi tailscale off \| login \| logout \| rename NAME \| status` | the rest of `server.tailscale.*`, and its status |
| `GoRex rexapi pair [--json]` | the tailnet address as a QR code |
| `GoRex rexapi call METHOD [JSON]` | call any method, as `rex api call` does |

The gateway's state lives in `rexapi/` in GoRex's data directory.
`GOREX_REXAPI_SOCKET` moves the socket, for a data directory whose path is
too long for one.

## What differs from a Rex server

The wire is Rex's; where GoRex cannot do what Rex does, the method fails
with the error code Rex's documentation gives the closest meaning, and
says why. The full list is in `internal/rexapi/doc.go`. The ones a user
meets:

- Floating layers are shown, but not made or closed from a Rex client:
  the GoRex window takes no such commands yet. A hidden floating pane is
  a minimized layer: its header has a minimize button, a chip over the
  tab shows it again, and `session.set_layer_state` minimizes it and
  brings it back from a Rex client, with the focus when asked.
- There are no detached blocks, Lua scripts, configuration file or host
  list: `client.set_script`, `client.perform`, `hosts.put` and the like
  fail with `not_supported`, and `client.keymap` is empty.
- Every ID lasts: a window is named by its tab's own ID, which GoRex
  keeps in the layout, so windows, sessions and blocks keep their IDs
  across restarts of the gateway and of GoRex.
- Closing a session's last window leaves its space empty, as a Rex
  session goes on with no windows: make the next window from the client.
  A GoRex window that showed it shows another space, or closes. On the
  desktop the space goes with its last tab instead.
- A new window made with the focus in a session no GoRex window shows
  brings that session's space into view, in the GoRex window focused
  last, or in the next one to open within 30 seconds; one made without
  the focus leaves every GoRex window where it is.
- An app action still needs the GoRex window, and fails with `conflict`
  without it. Closing a window or session that holds a pane of another
  host drops that pane, and the GoRex app ends its session on that host,
  as it ends one its user closes: at once, or once the app runs and the
  host is connected again.
- Tabs and panes of remote hosts are left out of the views. GoRex's
  own `GET /api/spaces` tells which they are (below).
- Built-in Tailscale's sign-in link, `auth_url`, goes only to the local
  socket: whoever opens it first claims the device. Over the local
  network or the tailnet, `server.status`, `session.bootstrap`'s
  `server_status` and `tailscale_status_changed` leave it out, so such a
  client sees `needs_login` with no link, and a new link alone sends it
  no event. Get the link on this machine, from
  `GoRex rexapi tailscale status` or `GoRex rexapi tailscale login`.
- Revision 8's `data_tickets` on `session.bootstrap` is served: a client
  that asks for the terminal blocks gets a ticket for each one, and opens
  its terminals without a `data.connect` round trip each. Only terminals
  have tickets, since every GoRex block is a terminal.
- Revision 12's `listeners` on `server.status`, and on
  `session.bootstrap`'s `server_status`, lists the local socket first, as
  the primary endpoint (`peer_credentials`; on Windows its admission is
  left out), then `--listen`'s port and the local network's listener
  while it listens (`anonymous`), each with the address it is bound to.
  Built-in Tailscale is not in the list, as on a Rex server: `tailscale`
  describes it.
- A terminal's data stream can start over in the middle. The gateway
  reads the session's output all the time, so the GoRex server never
  drops it as a client too far behind. A connection slower than the
  program prints falls behind the gateway instead. Once the gateway holds
  2 MiB of output that came after the newest snapshot and that the
  connection did not take, it drops those bytes, and the output that
  follows. Once a connection has taken all the bytes kept, and at least a
  second has passed since the last resync, the gateway attaches to the
  session again. A stream with no connection first waits for a client to
  resume it. The gateway then sends on the same stream, in order:
  1. `ESC \` (which ends any string or sequence left open), RIS, and the
     sequences that clear the screen and scrollback;
  2. a `resize` message with the session's size;
  3. a `terminal_reset` message (`{"type":"terminal_reset"}`), GoRex's
     own;
  4. the screen, with up to 256 KiB of scrollback, as escape sequences;
  5. the `theme` message, if a client set one, and `child_exited`, if
     the program ended.

  Then the output follows. The byte counts of a resume stay those of the
  stream. RIS keeps the colours a client gave its terminal, but resets
  the cursor style: on `terminal_reset`, a client sets again what it set
  on the terminal itself (the web client sets its cursor again). The
  gateway does the same when the GoRex server ends its attach while the
  session runs. When it cannot attach again, the stream closes with
  `4000` if the session is gone, sends `child_exited` if the program
  ended, and else closes with `1011`. It also closes with `1011` when an
  attach ends with no output after its snapshot.

## Spaces across hosts

A GoRex space may hold panes of this machine and of remote hosts. Each
host's gateway shows its own terminals only, and Rex Go connects to each
host apart. To show the desktop's spaces as they are, the gateway tells
its own clients, at `GET /api/spaces`, which panes of other hosts each
space holds, named as those hosts' own gateways name them:

```json
{"machine_id": "9c63…", "sessions": [{"session_id": "session:…", "revision": 4,
  "remote_blocks": [{"machine": "f4b7…", "block_id": "block:…"}]}]}
```

- `machine_id` is a machine's lasting ID: a hash of its GoRex client key,
  the same whichever address reaches it, and no user-editable name. The
  desktop learns each host's from the host's hello, and keeps it in
  `hosts.json` as `machine`.
- A client shows a remote block only through its own connection to that
  machine's gateway, which it was let into there. The answer holds no
  address, key nor name of a host, and opens nothing: a block of a
  machine the client does not reach is of no use to it.
- The welcome lists the feature `gorex.spaces` when the gateway serves
  it. Rex's views are unchanged, but a change of a space's remote blocks
  is a new revision of its session, so a client reads them again on
  `session_view_changed`.

Rex Go groups windows by these spaces by default (Settings › Grouping):
a desktop's space lists its windows on other hosts too, which leave
their own host's list. Those of a host it has not added, or that is
offline, are left out.

## Moving a window to another session

Rex's API moves a window only within its session (`session.move_window`);
`session.move` orders sessions. GoRex's own method `gorex.move_window`
moves a window to another session, as the desktop's Move to Space moves a
tab to another space. The welcome lists the feature `gorex.move_window`
when the GoRex server can make the move (`CapabilityTabToSpace`, its
`tab.to_space` command); call it only then.

```
→ {"method": "gorex.move_window",
   "payload": {"session_id": "<session>", "window_id": "<window>",
               "to_session_id": "<other session>",
               "before_window_id": "<window of the other session>"}}
← {"revision": 42, "to_revision": 7}
```

- `before_window_id` is optional: without it the window goes to the end.
- The window keeps its ID, label, layout, floating layers, focus and
  zoom. Its blocks keep their IDs, and their programs go on.
- The other session's active window stays. The session the window left
  makes the next window active, else the one before it.
- The window that was a session's last destroys the session, as the
  desktop's space goes with its last tab: the result has
  `"session_destroyed": true` and no `revision`, and the connections that
  hear of every session get `session_destroyed`.
- The session left sends `window_closed`, `block_closed` for each block
  and `session_view_changed`; the session reached sends `window_created`,
  `block_created` for each block and `session_view_changed`.
- An owner of a block that is not attached to the session the block went
  to loses it: the next attached client in line gets it
  (`block_owner_changed`, reason `disconnect`).
- Errors: `conflict` (the connection is not attached to `session_id`),
  `session_not_found`, `not_found` (a window not in its session),
  `bad_request` (`to_session_id` is `session_id`, a missing or unknown
  argument), `not_supported` (the GoRex server is older).

`session.help`, `help.all` and `/llms.txt` describe it after Rex's
methods; `/api/help` is Rex's document as it is.

## Seen program status

A program's `done` and `error` records (OSC 7501) stay until the user
sees them. The GoRex server keeps when they were seen, per session, so
every client agrees: the desktop's panes and Rex Go read the same time,
and a record seen on one device is seen on all, after a restart too.
The welcome lists the feature `gorex.see_block` when the server keeps it
(`CapabilityStatusSeen`); call it only then.

```
→ {"method": "gorex.see_block",
   "payload": {"session_id": "<session>", "block_id": "<block>",
               "seen_at": "2026-11-13T09:30:00Z"}}
← {}
```

- `seen_at` is the `updated_at` of the latest done or error record the user saw. A time
  of a whole second, as `updated_at` has, covers that second.
- The seen time only moves forward, and never past the block's latest
  `done` or `error` record. Within one second, a second `done` or `error`
  record after the one seen reads as seen too, as `updated_at` has whole
  seconds.
- `com.superlogical.terminal.program_status` and the
  `program_status_changed` event carry `seen_at` beside `records` once a
  client saw some; a change of it sends `program_status_changed`. A
  `done` or `error` record whose `updated_at` is not after `seen_at` was
  seen, as the desktop's `MostUrgent` reads it.
- Errors: `conflict` (the connection is not attached to `session_id`),
  `not_found` (a block not in its session), `bad_request` (a missing,
  unknown or malformed argument), `not_supported` (the GoRex server is
  older).

## Files of a host

The web client opens a file path printed in a pane as the desktop does
(docs/web-open-files.md). The welcome lists the feature
`gorex.download` when the GoRex server sends files
(`CapabilityFileTransfer`). It takes two steps.

`com.superlogical.terminal.download_begin`, a block method like
`upload_begin`, opens the file with `file.get` for the block's session
and answers a ticket:

```
→ {"method": "com.superlogical.terminal.download_begin",
   "payload": {"session_id": "<session>", "block_id": "<block>",
               "args": {"path": "<path>"}}}
← {"ticket": "<base64url>", "name": "report.png", "size": 48213,
   "mtime": "2026-11-12T09:30:00Z", "type": "image/png"}
```

- `path` is absolute, from `~/`, or relative to the directory of the
  block's program in the foreground, else the session's.
- `type` is a guess from the name's extension; `mtime` is left out when
  the server sends none.
- A ticket is good for one download, and lapses after 60 s unused or
  when its control connection closes, which closes its `file.get`. A
  control connection holds at most 4.
- Errors: `not_found` (no block, or its session ended), `is_folder`,
  `not_regular` (a FIFO, a device, a socket), `no_file`, `denied`,
  `too_large` (over 1 GiB), `not_supported` (the server sends no files),
  `conflict` (4 tickets open already), `bad_request`, else `internal`
  with the server's text. Each refusal is logged with its code.

`GET /api/download?ticket=…` streams the ticket's file, once: `200` with
`Content-Type: application/octet-stream`, `Content-Length`,
`Last-Modified`, `Cache-Control: no-store`, `X-Content-Type-Options:
nosniff`, and `Content-Disposition: attachment` with an ASCII
`filename=` and, for any other name, `filename*=utf-8''…`. A trusted page
also gets `Access-Control-Expose-Headers: Content-Disposition,
Content-Length`. An unknown, used or lapsed ticket gets `404`. A file
that ends short of its size aborts the response. `Range` is not taken.
Each download is logged as `rexapi: download <client address> <block>
<path> <bytes>`.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `GoRex is not running on this machine` | Open GoRex first: the gateway starts no server. |
| `tailscale: needs_login` | Open the `sign in` link of `GoRex rexapi tailscale status`, or run `GoRex rexapi tailscale login` for a new one. |
| `https: pending` stays, or Tailscale turns off with an error | Turn on MagicDNS and HTTPS in the tailnet's admin console, then `GoRex rexapi tailscale on` again. |
| The phone gets 403 | It is signed in to Tailscale as another user, or is a tagged device: sign it in as you, or turn on `--allow-tailnet-members`. |
| A page or client gets 421 | It reached the gateway by a name the gateway doesn't know: use its IP address, `localhost` or the tailnet name, or add the name with `GoRex rexapi web host add NAME`. |
| `no Rex API gateway on …` | The gateway is not running, or listens on another socket (`--socket`, `GOREX_REXAPI_SOCKET`). |
