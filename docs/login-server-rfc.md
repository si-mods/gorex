# RFC: login server and PAM

Status: proposal. Not implemented.

## Problem

The shared server runs every session as one OS account. Pairing identifies
a client, and session ownership names its principal, but it is not a
privilege boundary: every client's programs can read the same files, use
the same environment secrets and signal the same processes.

A login server would let one GoRex server authenticate principals and run
each session with that principal's Unix identity. This RFC records the
design and risks; it is not part of the current roadmap epic.

## Non-goals

- Replacing SSH or claiming SSH equivalence before this exists.
- Tailscale ACLs. Those are a separate identity source and can be layered
  in front of this.
- Windows service accounts. The first implementation targets Linux with
  PAM/NSS.
- Migrating existing sessions between OS users.

## Options

| Option | Isolation | Cost |
|---|---|---|
| Keep one OS account | none between principals | current shared mode |
| One server process per Unix user | process and file isolation | supervisor, per-user sockets, coordination |
| One server, demote each session to the authenticated uid | file/process isolation, shared daemon | PAM, setuid/setgid, supplementary groups, fd hygiene |
| External login service (systemd/logind, SSH) | delegated | another dependency, session lifetime coupling |

Recommended first step: **one server, authenticate with PAM, launch each
session as the authenticated Unix user**, with the server process holding
no long-lived per-user credentials.

## Proposed flow

1. A remote client connects over tailcat and sends its principal key.
2. It requests login with a username and an authentication method
   (password, or a future public-key/device method).
3. The server asks PAM in a helper process, not in the session server
   itself: no PAM modules in the daemon, no credentials in its memory
   longer than the exchange.
4. On success the server resolves uid, gid and supplementary groups, then
   starts the PTY with `setuid`/`setgid`/`initgroups` and a clean
   environment. The PTY master stays with the server; the slave starts
   the user's shell.
5. The session records both the Unix uid and the remote principal. ACLs
   compare both: a principal may use only sessions of its uid, unless an
   administrator shares them.
6. Logout revokes the login token, drops the client's connections and
   ends sessions as configured.

## Security requirements

- Authenticate on a separate socket/helper with a rate limit and a
  constant-time failure path; never log passwords or PAM messages.
- Do not pass the server's environment to user sessions. Drop
  `GOREX_*` server internals, API keys and the remote keys.
- `setuid` sessions must close inherited descriptors, clear capabilities
  and set a restrictive umask before `exec`.
- Session files, layout and logs stay per uid; the root server must not
  write into user homes except through explicit operations.
- A remote principal can own sessions on only its authenticated uid;
  admin sharing is explicit and audited.
- Treat PAM modules as privileged code. A crash or timeout must fail
  closed and must not kill other users' sessions.

## Protocol sketch

```json
{"op":"login","login":{"user":"yibo","method":"password","secret":"…"}}
```

The response carries an opaque login id and the uid/gid the server
accepted, never the password. Subsequent `create`/`attach` requests name
the login id; the server checks the session's uid against it.

## Migration

- Existing remote pairing and single-account shared mode stay.
- A server with login enabled rejects `create` from a paired client
  without a login id.
- The layout and session format gain an optional owner uid; old layouts
  without one remain owned by the server account.

## Open questions

- Password-only first, or device-bound public-key login from the start?
- How do root-owned sessions (`sudo`) interact with layout sharing?
- Should PAM sessions run under `systemd-logind` for cgroup and seat
  management?
- How are per-user sockets exposed to local CLI clients without giving
  every user access to the server socket?

## Acceptance for a future implementation

- Two principals on one server cannot read or signal each other's
  sessions.
- A failed or timed-out PAM exchange creates no session and reveals no
  account existence.
- Session output, layout sharing and `gorex clients` show the principal
  and uid consistently.
- The existing single-account mode is unchanged when login is off.
