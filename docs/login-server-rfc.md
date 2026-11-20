# RFC: login server and PAM

Status: proposal. Not implemented.

Nothing exists yet: no login, no PAM and no per-principal owner uid. #609 built one piece of step 4, a fixed session user (`GOREX_SESSION_USER`) for all sessions on a Linux server. #795 and #851 later extended `GOREX_SESSION_USER`. This RFC records the design and the risks. It is not part of the current roadmap epic.

## Problem

The shared server runs every session as one OS account. Pairing identifies a client. Session ownership names the principal of a session. Neither is a privilege boundary. The programs of every client can read the same files, use the same environment secrets and signal the same processes.

A login server lets one GoRex server authenticate principals. The server then runs each session with the Unix identity of that principal.

## Non-goals

- Replacing SSH or claiming SSH equivalence before this exists.
- Tailscale ACLs. Those are a separate identity source. A team can layer them in front of this design.
- Windows service accounts. The first implementation targets Linux with PAM and NSS.
- Migrating existing sessions between OS users.

## Design

The first step is to use one server, authenticate with PAM, and start each session as the authenticated Unix user. The server process holds no long-lived per-user credentials.

### Proposed flow

1. A remote client connects over tailcat and sends its principal key.
2. The client requests a login with a username and an authentication method (password, or a future public-key or device method).
3. The server asks PAM in a helper process. It does not ask PAM in the session server itself. The daemon has no PAM modules. No credentials stay in its memory longer than the exchange.
4. On success, the server resolves the uid, the gid and the supplementary groups. Then it starts the PTY with `setuid`, `setgid` and `initgroups` and a clean environment. The PTY master stays with the server. The slave starts the shell of the user.
5. The session records both the Unix uid and the remote principal. ACLs compare both. A principal can use only the sessions of its uid, unless an administrator shares them.
6. Logout revokes the login token and drops the connections of the client. It ends the sessions as configured.

### Protocol sketch

```json
{"op":"login","login":{"user":"alice","method":"password","secret":"…"}}
```

The response carries an opaque login id and the uid and gid that the server accepted. It never carries the password. Later `create` and `attach` requests name the login id. The server checks the uid of the session against the login id.

## Alternatives

| Option | Isolation | Cost |
|---|---|---|
| Keep one OS account | none between principals | current shared mode |
| One server process per Unix user | process and file isolation | supervisor, per-user sockets, coordination |
| One server, demote each session to the authenticated uid | file and process isolation, shared daemon | PAM, setuid/setgid, supplementary groups, fd hygiene |
| External login service (systemd/logind, SSH) | delegated | another dependency, session lifetime coupling |

The design above uses the third option.

## Security

- Authenticate on a separate socket or helper. Use a rate limit and a constant-time failure path. Never log passwords or PAM messages.
- Do not pass the environment of the server to user sessions. Drop the `GOREX_*` server internals, the API keys and the remote keys.
- A `setuid` session must close inherited descriptors, clear capabilities and set a restrictive umask before `exec`.
- Session files, layout and logs stay per uid. The root server must not write into user homes, except through explicit operations.
- A remote principal can own sessions on only its authenticated uid. Admin sharing is explicit and audited.
- Treat PAM modules as privileged code. A crash or a timeout must fail closed. It must not kill the sessions of other users.

## Migration

- Existing remote pairing and the single-account shared mode stay.
- A server with login enabled rejects `create` from a paired client without a login id.
- The layout and the session format gain an optional owner uid. Old layouts without a uid stay owned by the server account.

## Tests

These checks apply to a future implementation:

- Two principals on one server cannot read or signal the sessions of each other.
- A failed or timed-out PAM exchange creates no session and does not reveal if an account exists.
- Session output, layout sharing and `gorex clients` show the principal and the uid consistently.
- The existing single-account mode does not change when login is off.

## Open questions

- Does the first version use only passwords, or device-bound public-key login from the start?
- How do root-owned sessions (`sudo`) interact with layout sharing?
- Should PAM sessions run under `systemd-logind` for cgroup and seat management?
- How does the server expose per-user sockets to local CLI clients without giving every user access to the server socket?
