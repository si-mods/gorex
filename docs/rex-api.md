# Rex's apps

Rex's own apps, the iPhone one among them, connect to GoRex as they
connect to a Rex server. `GoRex rexapi` serves Rex's API, revision 7, from
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

The gateway listens on three places:

- its local socket, `rexapi.sock` in GoRex's data directory, for this
  user only. The Rex CLI reaches it with `rex -S unix://PATH`, and the
  methods that administer the gateway (`server.tailscale.*`) are accepted
  only there, as on a Rex server;
- the tailnet, over HTTPS, while built-in Tailscale is on;
- with `--listen http://127.0.0.1:PORT`, a TCP port that lets everyone in
  unauthenticated: for tests on this machine only.

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
2. Start the gateway, and keep it running:

   ```sh
   /Applications/GoRex.app/Contents/MacOS/GoRex rexapi
   ```

3. In another terminal, turn built-in Tailscale on:

   ```sh
   GoRex rexapi tailscale on
   ```

   The first time, it prints a sign-in link: open it and sign in. Or sign
   in without a browser with an auth key:
   `GoRex rexapi tailscale on --auth-key-file key.txt` (the key is used
   once, and never saved or logged).
4. Wait for `https: available` in `GoRex rexapi tailscale status`.
5. Show the address as a QR code, and scan it with Rex on the phone:

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
| `GoRex rexapi [--listen URL] [--socket PATH] [--tailnet-dir DIR]` | the gateway |
| `GoRex rexapi tailscale on [--hostname NAME] [--allow-tailnet-members] [--auth-key-file FILE]` | turn built-in Tailscale on |
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

- Floating layers are shown, but not made, moved or closed from a Rex
  client: the GoRex window takes no such commands yet. A hidden floating
  pane is in no layer.
- There are no detached blocks, Lua scripts, configuration file or host
  list: `client.set_script`, `client.perform`, `hosts.put` and the like
  fail with `not_supported`, and `client.keymap` is empty.
- Window IDs last as long as the gateway: GoRex's tabs have no IDs of
  their own, so a restarted gateway gives windows new ones. Session and
  block IDs stay.
- Changing the layout needs the GoRex window open, as for rootshell:
  without it, the call fails with `conflict`.
- Tabs and panes of remote hosts are left out.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| `GoRex is not running on this machine` | Open GoRex first: the gateway starts no server. |
| `tailscale: needs_login` | Open the `sign in` link of `GoRex rexapi tailscale status`, or run `GoRex rexapi tailscale login` for a new one. |
| `https: pending` stays, or Tailscale turns off with an error | Turn on MagicDNS and HTTPS in the tailnet's admin console, then `GoRex rexapi tailscale on` again. |
| The phone gets 403 | It is signed in to Tailscale as another user, or is a tagged device: sign it in as you, or turn on `--allow-tailnet-members`. |
| `no Rex API gateway on …` | The gateway is not running, or listens on another socket (`--socket`, `GOREX_REXAPI_SOCKET`). |
| A split, close or rename from the phone fails with `conflict` | Open the GoRex window. |
