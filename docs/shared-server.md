# Shared server on Linux

GoRex can run its session server on a Linux account that several paired
clients use. The server runs as one OS account; each remote client is a
principal by its tailcat key, and the sessions it creates belong to it.

This is not a login server and not a multi-user privilege boundary. Every
session runs as the same OS user. Do not use it to separate untrusted
users; a PAM/login design is a separate project.

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
gorex remote share on         # optional: every client may use every session
gorex remote layout on        # optional: share the layout read-only
```

A client runs GoRex, adds a remote host with the address, and asks to pair.
Compare the fingerprint shown by both sides before approving. `gorex
remote allow NAME KEY` adds an already-known key without the pairing
window; `gorex remote revoke KEY` drops it and its connections.

## Ownership

`gorex ls` shows the owner of each session: `local` for one the server
started itself, or the paired client name. Without `share on`, a client
sees only sessions it created; with `share on`, it sees all. Per-client
sharing is available in Settings › Remote Hosts, or with the remote admin
API. Layout sharing is separate and is filtered to sessions the client
may use.

## Limits

- The server app is the only layout writer; layout commands require the
  app window to be open.
- Tailscale identity/ACL is not part of this mode: pairing is the
  identity, with tailcat keys.
- The session server and its programs share one OS account, one
  filesystem permission set and one set of environment secrets.
