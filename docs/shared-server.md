# Shared server on Linux

GoRex can run its session server on a Linux account that several paired
clients use. The server runs as one OS account; each remote client is a
principal by its tailcat key, and the sessions it creates belong to it.

This is not a login server: there is no PAM, no per-user authentication
and no password. Each client is a principal by its tailcat key, not by a
system account.

By default every session runs as the same OS user as the server, and that
user reaches the server's socket, its layout and its remote
configuration. On a machine where the sessions, or the agents in them, are
not to have the server's own data, start the server as root and name the
user the sessions run as — see [Sessions as another
user](#sessions-as-another-user).

## Install

1. Create the account and data directory:

   ```sh
   sudo useradd --system --home /var/lib/gorex --create-home gorex
   sudo install -m 0755 gorex /usr/local/bin/gorex
   ```

2. Install `packaging/gorex-server.service` as
   `/etc/systemd/system/gorex-server.service`, then:

   ```sh
   sudo systemctl daemon-reload
   sudo systemctl enable --now gorex-server
   ```

The service keeps `GOREX_DIR=/var/lib/gorex`, where the server socket,
layout and remote configuration live.

## Remote access

From the server account (or with `sudo -u gorex`):

```sh
gorex remote enable
gorex remote address          # the tailcat address clients pair with
gorex remote pair start       # opens the five-minute pairing window
gorex remote pending          # the requests, as JSON
gorex remote approve KEY      # approve one fingerprint
gorex remote layout on        # optional: share the layout read-only
```

Every paired client may see and use every session, as suits machines
that are all yours: pair only devices you trust as you would an SSH key.

A client runs GoRex, adds a remote host with the address, and asks to pair.
Compare the fingerprint shown by both sides before approving. `gorex
remote allow NAME KEY` adds an already-known key without the pairing
window; `gorex remote revoke KEY` drops it and its connections.

## Ownership

`gorex ls` shows the owner of each session: `local` for one the server
started itself, or the paired client name. Every client sees all of
them. Layout sharing is separate, and filtered to the sessions here.

## Sessions as another user

A server started as root can run its sessions as another user, while its
socket, layout and remote configuration stay root's:

```ini
# /etc/systemd/system/gorex-server.service.d/sessions.conf
[Service]
User=root
Environment=GOREX_SESSION_USER=me
```

Every session — its login shell, the commands run in it, the agents and
programs in it — runs as `me`, with `me`'s home and login shell, and
without the server's capabilities: the ambient set survives a change of
user, so the server clears it in each session before its program starts.
GoRex's data directory stays root's, `0700`, so a session cannot reach the
socket, the layout journal or the tailcat keys in `remote.json`, and
`gorex ls` inside it does not reach the server.

Two things follow:

- `gorex` as `me` does not reach the server either: the socket is root's.
  Work on the server's sessions from a client — the app, `gorex -S host`
  from another machine, or the web client.
- The server reads, as root, the files it resolves for a session: agent
  sessions under the user's `~/.claude`, omp's, teleported panes, and
  uploads. They are the session user's, and the session can write them.
- A client's reads of any path go through the session user: `file.get`
  (the Rex API's `download_begin`) and `dir.list` (`gorex.dir.list`)
  run in a helper the server starts as that user from its own executable
  (`gorex -session-read`), which clears the capabilities first. The helper
  opens the file and hands the server its descriptor, or lists the
  directory, so a client gets only what the session user may read: the
  system checks the user's groups and ACLs. A file or directory only root
  may read answers `permission denied` or `denied`, as for any user.

Unset, sessions run as the server does, as before. The setting is
Linux's: on macOS and Windows the app and the server are the user's, and
the server refuses the variable there. A server started as root without
it runs its sessions as root, and says so at start.

## Limits

- `action`, `float` and `config reload` need the app window to be open;
  the server makes the other layout changes itself.
- Tailscale identity/ACL is not part of this mode: pairing is the
  identity, with tailcat keys.
- Without `GOREX_SESSION_USER`, the session server and its programs share
  one OS account, one filesystem permission set and one set of
  environment secrets.
