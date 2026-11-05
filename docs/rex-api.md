# Rex's apps

Rex's own apps, the iPhone one among them, connect to GoRex as they
connect to a Rex server. `GoRex rexapi` serves Rex's API, revision 10, from
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
  leaves.
- **Layout changes** (new window, split, close, rename, focus, zoom, swap,
  move, resize) go to the GoRex window, which makes them as its own
  buttons do, as for rootshell.

The GoRex server runs the gateway itself while the local network
listener or built-in Tailscale is on, and again after it restarts or
upgrades: nothing else needs to run. `GoRex rexapi` runs one of its own,
for development; the two never run on the same data directory at once.

The gateway listens on four places:

- its local socket, `rexapi.sock` in GoRex's data directory, for this
  user only. The Rex CLI reaches it with `rex -S unix://PATH`, and the
  methods that administer the gateway (`server.tailscale.*`) are accepted
  only there, as on a Rex server;
- the local network, over plain HTTP, while that listener is on: only
  the networks it allows get in (below);
- the tailnet, over HTTPS, while built-in Tailscale is on;
- with `GoRex rexapi --listen http://127.0.0.1:PORT`, a TCP port that lets
  everyone in unauthenticated: for tests on this machine only.

## Local network

When the phone reaches this machine by its address on the local network,
directly or through a VPN to it, Rex's app connects without Tailscale.
Settings › Remote › Rex apps on the local network turns the listener on.
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
When the listener is bound to the active system Tailscale IP, another
GoRex device connected with system Tailscale can open **Settings →
Remote Hosts → Rex QR…** to show the address as a code for Rex to scan.

A headless server can set `showDesktopSessions` to `true` in
`rexapi/lan.json`. Rex then shows live sessions created by desktop
Tailscale peers under **Desktop terminals** if they are absent from the
server's desktop layout. Opening a terminal there attaches to the
existing process.

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
   `GoRex rexapi tailscale on --control-url https://headscale.example.com`.
   Turn it off first to switch servers; an explicit empty `control_url`
   (`GoRex rexapi call server.tailscale.enable '{"allow_tailnet_members":false,"control_url":""}'`)
   returns the device to Tailscale's own.
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
| `GoRex rexapi [--listen URL] [--socket PATH] [--tailnet-dir DIR]` | a gateway of its own, for development; the server runs one while the local network or built-in Tailscale is on |
| `GoRex rexapi lan on \| off \| status` | the listener on the local network |
| `GoRex rexapi lan listen HOST:PORT` | the address it listens on |
| `GoRex rexapi lan allow NETWORK` / `deny NETWORK` | the networks let in: a CIDR, or an address alone |
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
  a minimized layer, and `session.set_layer_state` minimizes it and
  brings it back, with the focus when asked.
- There are no detached blocks, Lua scripts, configuration file or host
  list: `client.set_script`, `client.perform`, `hosts.put` and the like
  fail with `not_supported`, and `client.keymap` is empty.
- Every ID lasts: a window is named by its tab's own ID, which GoRex
  keeps in the layout, so windows, sessions and blocks keep their IDs
  across restarts of the gateway and of GoRex.
- Closing a session's last window leaves its space empty, the GoRex
  window open on it, as a Rex session goes on with no windows: make the
  next window from the client. On the desktop the window closes with its
  last tab instead.
- Changing the layout needs the GoRex window open, as for rootshell:
  without it, the call fails with `conflict`.
- Tabs and panes of remote hosts are left out.
- Revision 8's `data_tickets` on `session.bootstrap` is served: a client
  that asks for the terminal blocks gets a ticket for each one, and opens
  its terminals without a `data.connect` round trip each. Only terminals
  have tickets, since every GoRex block is a terminal.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `GoRex is not running on this machine` | Open GoRex first: the gateway starts no server. |
| `tailscale: needs_login` | Open the `sign in` link of `GoRex rexapi tailscale status`, or run `GoRex rexapi tailscale login` for a new one. |
| `https: pending` stays, or Tailscale turns off with an error | Turn on MagicDNS and HTTPS in the tailnet's admin console, then `GoRex rexapi tailscale on` again. |
| The phone gets 403 | It is signed in to Tailscale as another user, or is a tagged device: sign it in as you, or turn on `--allow-tailnet-members`. |
| `no Rex API gateway on …` | The gateway is not running, or listens on another socket (`--socket`, `GOREX_REXAPI_SOCKET`). |
| A split, close or rename from the phone fails with `conflict` | Open the GoRex window. |
