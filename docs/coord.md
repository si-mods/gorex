# GoRex network coordinator (coord)

GoRex's remote access today is pairwise: every server keeps its own
allowlist, and adding a machine means a pairing window and a fingerprint
check, machine by machine. The coordinator turns that into a network the
way Tailscale does: a machine joins **once** with a network key, and
every member trusts every other member and finds it without anyone
copying addresses by hand.

The coordinator is a mini control plane only: it holds the member
directory and the network key. Terminals, sessions and all data stay on
the tailcat data plane (WireGuard end to end, through DERP or directly);
the coordinator never sees terminal traffic, and it stops being reachable
without breaking machines that already know each other.

## Deployment

One coordinator per network, on an always-on machine (a small LXC, a
VPS), run as `gcoord serve`. Default port 443, HTTPS's own. State lives in one
directory (default `/var/lib/gcoord`): `network.json` (members, the
hashed network key; mode 0600) and `tls.crt`/`tls.key`, a self-signed
certificate whose fingerprint pins the server.

`gcoord init` prints the network key **once**, and a join URL of the
form

```
https://host/<fingerprint>
```

where `<fingerprint>` is the first 16 hex digits of the SHA-256 of the
certificate's DER. A join URL is a secret like a tailcat address: it
points at the coordinator and pins its certificate, so no certificate
authority is needed. Losing the network key means regenerating it and
rejoining every machine; rotating is `gcoord init` again.

## Trust model

Whoever holds the network key is a member of the network, and a member
may run commands as this user on every machine in it (see README,
Remote access: an allowed key is an SSH key). That is the same trust
as a Tailscale tailnet: joining once replaces pairwise approval.
Leaking the network key hands over the network; the answer is to
regenerate it (`gcoord init`) — members then rejoin with the new key –
and to kick the member that should not be there (`gcoord kick`, or
`POST /v1/kick`). The coordinator knows every member's tailcat address,
so its state directory and its logs hold secrets: addresses are never
logged, and the state file is 0600.

## Wire protocol

HTTPS only, JSON bodies, no certificate authority: the client verifies
the server against the fingerprint in the join URL. Every response
carries `Cache-Control: no-store`. Two header names: `X-Gorex-Key`
(the network key, compared in constant time) and, on 401 answers,
`Retry-After` while an address is rate-limited.

Members are `{name, nodeKey, address, joined, lastSeen}`:

- `name`: printable, at most 40 characters, unique per member; joining
  with an existing `nodeKey` renames and refreshes that member (a
  rejoin), a name that exists under another key is rejected.
- `nodeKey`: canonical `nodekey:` + 64 hex digits, as
  `rex.NormalizePublicKey` returns.
- `address`: a tailcat address, as `rex.ValidRemoteAddress` checks.

### Endpoints

`POST /v1/join` — `{"name","nodeKey","address"}` and the network key.
Registers the sender (or refreshes it), and answers the whole directory
`{"revision", "members":[…]}` so a new member needs no second call.
Wrong key: 401, and five failures from one address block it for ten
minutes. Bad fields: 400 with a reason.

`GET /v1/members?since=R` — the network key. The directory as
`{"revision","members":[…]}`, or 204 when `since` equals the current
revision (members poll this, about every 30 s, with jitter). A member's
own poll updates its `lastSeen`; members silent for 7 days are shown as
stale in `gcoord members`, never dropped.

`POST /v1/leave` — `{"nodeKey"}` and the key: removes the sender.

`POST /v1/kick` — `{"nodeKey"}` or `{"name"}` and the key: removes
another member. Every machine that polls learns within its next poll
and closes that key's connections (the existing peer-key refusal).

`GET /v1/health` — no key: `{"revision", "members":N}` only, for
monitoring; no names, no keys, no addresses.

The revision is a counter that grows on every directory change.

## Client (GoRex side, later PRs)

`internal/coord` also builds the client: a `Client` with the join URL
and the network key that speaks the protocol above over the pinned TLS
connection. GoRex keeps the network key beside the rest of the remote
state (`remote.json`, 0600, never logged), joins once, then polls
`/v1/members` in the background and mirrors the directory into two
places: the allowlist (`remote.json`) and the remote hosts
(`hosts.json`). The pairing window, `remote.pair` and the fingerprint
flow go away for machines in a network.
