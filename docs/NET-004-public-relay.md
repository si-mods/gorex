---
name: NET-004
description: "A DERP relay that si-mods operates for the team at derp.gorex.uk, for hosts that should not depend on the tailcat fleet. Use when you change the private relay, the remote.relay setting or the path a host's address names."
metadata:
  created: "2026-11-22"
  updated: "2026-11-23"
---

# NET-004: A private relay for host links

Status: built and running at derp.gorex.uk (si-mods/gorex-relay#1, #1005). A host on the `gorex` alias passed the allowlist live.

This doc designs the public relay that phase 7 of [NET-002](NET-002-host-links.md) names, sized down to a private one: it serves the team by an allowlist of node keys. It runs next to the push relay of [NET-003](NET-003-push-relay.md) on the same machine, and shares nothing with it.

## Problem

Every remote session already rides a relay that GoRex does not operate. A host homes on a region of `https://tailcat.dev/derpmap.json`, the public fleet of the tailcat project, unless the user names a relay of their own (`remote.relay`, `internal/rex/remote.go`). A host behind NAT or CGNAT already meets its clients there, because both ends dial out.

So the fleet is a silent dependency of the remote path: its capacity, its terms for traffic that is not tailcat's, and its continued existence are not ours to decide. A user who wants off it must run a `derper` and type its host name. Almost nobody does. The team wants its own machines off it, and `relay.gorex.uk` already exists.

## Goal

- si-mods operates a DERP relay for the team, on the machine that serves `relay.gorex.uk`, at a second name such as `derp.gorex.uk`.
- A host homes on it by name: `gorex remote relay gorex`, or the same choice in Settings. The user types no host name.
- The relay admits the node keys on an operator's list and no others.
- The host needs no public address, no port forward and no tailnet. It dials out only.
- The relay holds no session state and reads no content.
- The same service is what a user self-hosts: run it, name it in `remote.relay`.

## Non-goals

- **Public use.** The relay serves the team. Opening it is a new decision, with the capacity questions that brings.
- **A change of the default region.** `tailcat.dev` stays the default. A private relay can be nobody's default.
- **Session traffic on the push API.** A POST API is not a stream, and NET-003 keeps that service to bindings only. The two services share a machine and nothing else.
- **A new relay protocol.** DERP is the protocol tailcat speaks. A second tunnel is a second stack.
- **Pairing by address through the relay.** First contact asks the machine's own listener, which exists only while Reachable at runs. A host behind NAT pairs another way: the pairing window, over the address the QR code or a paste carries.
- **A self-service register.** A signed enrollment would need an identity the project does not have. The operator edits the list.

## Design

### Behavior

1. The operator keeps `derp-allowed.json` on the relay machine: the node public keys of the team's hosts and of each client device, with a name for each.
2. On the serving machine, `gorex remote relay gorex` sets the DERP name in `remote.relay`. In Settings, the relay row accepts `gorex` beside a host name, and its hint names the choice.
3. Each side then shows the key the operator's list needs. The host's key shows beside its address, selectable and copyable, as **My key** shows the client's (`remoteview.go`). `gorex remote key` is the command-line form.
4. The machine's main address then names the relay. The address shows in Settings and in `gorex remote address`, and pairing hands it to clients as today.
5. A client that has the address dials the relay and presents its node key. The relay admits it when the key is on the list, and the client meets the host's key there. A refusal shows in the relay row's error place, and the hint says to send the key to the operator.
6. The pairing window, the approval and the sessions do not change. Reachable at keeps its own address and its pairing endpoint. Clearing the relay homes the address on the tailcat fleet again.

### The relay

In `si-mods/gorex-relay`, next to the push API but not behind the tunnel:

- The push API stays on `relay.gorex.uk` through the Cloudflare Tunnel. DERP needs a direct path: its long Upgrade connection does not belong behind a proxy that speaks HTTP only, and STUN is UDP, which the tunnel cannot carry. A grey-cloud name such as `derp.gorex.uk` resolves to the machine's address.
- A `derpserver.Server` (`tailscale.com/derp/derpserver`, the module that `internal/rex/ownrelay.go` already links) listens on its own port, with a certificate for the name from a public authority. `relayRegion` sets no `CertName`, so clients verify the chain — the check the web client of NET-002 needs.
- A STUN server on UDP 3478 on the same machine, as `derper` serves, so that netcheck completes and two machines that could dial each other can still find a direct path.
- Admission is `SetVerifyClientURL`, the hook `ownrelay.go` uses: the verifier reads `derp-allowed.json`, which the operator edits without a restart. A `DERPAdmitClientRequest` that names a listed key is allowed.
- The connection limits of `ownrelay.go` (`ownRelayPerSource`, `ownRelayTotal`, raised for a shared service) stay as a second layer, against a leaked or misused key.
- The relay holds the list and nothing else. It drops a connection when the key at its end disconnects, and nothing survives a restart.

### Self-hosting

The same service is the self-host artifact, published as the Dockerfile of `si-mods/gorex-relay` already is:

- A user runs the DERP half on a machine with a public address — a VPS, since the point of a rendezvous is that both ends can dial it. A home machine behind NAT cannot be its own public relay.
- The name needs a certificate the clients trust. `derper`'s autocert covers it when the name resolves; `remote.relay` takes `host:port` as today.
- `derp-allowed.json` is optional for the self-hoster: an empty or absent list runs open, on the user's own cost. Most will want their keys on it, for the same reason the team does.
- `gorex remote relay HOST` stays the plain way in, with no alias. The Settings row accepts the host name as today.

### The host

- `remote.relaySet` (`internal/rex/remote_ops.go`), `NormalizeRelay` and `relayRegion` (`internal/rex/remote.go`) do the work already. The new code is the alias `gorex`, which resolves to the relay's name in the CLI and in the Settings row.
- `RemoteStatus` gains `publicKey`, the host's node public key, so that `gorex remote status` prints it and Settings shows it beside the address. `gorex remote key` prints the client key of the machine it runs on (`ClientPublicKey` exists).
- The relay row (`remoteRelay` in `remoteview.go`) accepts `gorex`, names it in its hint, and shows a refusal as its error. Its strings are new locales.
- `remote.json` keeps storing a host name in `relay`. No saved value changes.

### The clients

- The desktop and Rex Go need no change to connect. `tailcat.ConnInfo` carries the region in the address, so a client fetches no map and verifies the TLS chain.
- For the operator's list, both ends show the client key already: the desktop's **My key** row copies it (`remoteview.go`), and Rex Go shows the phone's key in Remote Hosts (`RemoteHostsView.swift`). The CLI's `gorex remote key` is the third form of the same thing.
- The web client needs a relay whose certificate a browser accepts, which this one has. Its tailcat path keeps its own document.

## Alternatives

| Option | Why not |
| --- | --- |
| Stay on the tailcat fleet only | it works today and stays the default, but the team's remote paths then depend on a service GoRex does not operate |
| Open admission, with limits | the team runs the relay for itself. An open relay pays strangers' bandwidth and needs the capacity work this doc skips |
| Signed enrollment | it needs a registry of identities, which the project removed with `internal/coord`. A hand-edited list matches the size of the team |
| Extend the push API to carry sessions, as alma-relay multiplexes its agent socket | DERP is already this shape: two outbound connections that meet at the relay. A tunnel protocol next to it adds a stack |
| DERP through the Cloudflare Tunnel | STUN is UDP, which the tunnel cannot carry, and the long Upgrade connection does not belong behind an HTTP proxy |

## Security

- Admission to sessions does not change. The host's allowlist and the preshared key in the address decide, on every path.
- Admission to the relay is the operator's list. A stranger's key is refused at connect, so the relay's bandwidth serves only the team.
- The relay sees node public keys, ciphertext, and the timing and size of the traffic, as NET-002 sets out for a public relay. It cannot join a tunnel it relays: the preshared key is in the address, never at the relay.
- A leaked list entry lets its holder relay ciphertext through us. The connection limits bound it, and the operator removes the entry.
- The direct listener and STUN port open the machine's firewall, which today is closed to all inbound. The DEPLOY.md of `si-mods/gorex-relay` records the change; the machine's other listeners are unaffected.
- The relay logs no content, as the push relay does.

## Migration

- An update changes nothing: no saved `relay` value, no saved address, no region a host homes on.
- A host that sets or clears the relay keeps its keys. Its main address changes, as `remote.relay` always made it change. The Reachable-at address does not change.

## Tests

- Unit: the alias resolves and validates; a key on the list is admitted and one off it is refused; the list reloads without a restart.
- UI (`ui.NewTester`): the relay row accepts `gorex`, a refusal shows its error, and the host's key copies. Rex Go's key display needs no new test; it exists.
- End to end: a host on the alias pairs over the relay and serves a session; a revoke drops the client; a client whose key is not on the list cannot reach the host.
- Ops: the DERP probe and STUN answer at the relay's name, and the push API is not affected.
- No item of `docs/invariants.md` names this path.

## Phases

1. The relay service in `si-mods/gorex-relay`: DERP, STUN, the allowlist and the deployment note, including the self-host README.
2. The `gorex` alias in the CLI and in Settings, `RemoteStatus.publicKey`, `gorex remote key`, with docs.
3. Later, with its own document: the web client's tailcat path, if a team member's browser needs it.

## Open questions

1. **The name and the port.** `derp.gorex.uk` on 443, or another name and port, and whether the machine's firewall admits them: `sing-box` already binds `*:8443` on it, so there is a precedent to check. Answer in the deployment, before phase 1 merges.
2. **Who is on the list.** The team's machines today. Whether a contributor's or a user's machine may join is an operator's call, not a code change.
