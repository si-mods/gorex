---
name: IOS-024
description: "Rex Go reaches a host over SSH through a TCP endpoint (direct-tcpip), for Windows hosts and TCP Rex API listeners where direct-streamlocal cannot go. Use when you change SSH hosts, the engine's SSH client, or the rexapi LAN gateway's admission."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
---

# IOS-024: Rex Go reaches a host over SSH through TCP

Status: built (phases 1 and 2).

## Problem

IOS-023's `ssh://` host speaks `direct-streamlocal` to the host's Unix socket. On Windows that reaches nothing: upstream Rex listens on a named pipe, and GoRex's `server.sock`, though a real AF_UNIX socket, is refused by Win32-OpenSSH's streamlocal connect (`OpenSSH_for_Windows_10.0p2`: channel accepted, every path form answered "open failed"). Port forwarding itself works on Windows; only the Unix-socket end does not.

A host can also run its Rex API on TCP — GoRex's rexapi LAN gateway, an upstream `--extra-listen http` or a `--allow-anonymous` TCP listener — with no Unix socket at all, on any OS.

## Goal

An `ssh://` host whose endpoint is TCP reaches it over a `direct-tcpip` channel through the same SSH client: SSH to the machine, forward to the listener, speak the Rex API as before. The LAN pairing token rides the connection, as it does for a LAN host.

## How the pieces fit

- `direct-tcpip` (RFC 4254 §7.2) is plain port forwarding; every OpenSSH server supports it, Win32-OpenSSH included. The same `ssh.Client` carries it beside streamlocal channels.
- GoRex's rexapi gateway already listens on TCP (`gorex rexapi lan`, `LANPort` 7297) and admits a caller that is in the networks allowed *and* shows `Authorization: Bearer <token>` (`internal/rexapi/lan.go`). A forwarded channel arrives from 127.0.0.1, so a listener bound to loopback with `allow 127.0.0.1/32` answers only the host's own processes and SSH forwards — the LAN is never exposed.
- The token still earns its keep on loopback: TCP cannot check a peer's uid, so any local process could drive the gateway without it. The app keeps the token in the Keychain, beside the LAN tokens, and sends it as it does today (`HostConfig.auth`, `LANTokens`).
- The user's own SSH session may read the token — it may read `lan.json` too — so the engine learns endpoint and token by exec, the way IOS-023 discovers a socket.

## Design

### The address

`ssh://user@host[:port][/endpoint]`, as IOS-023 spells it, where endpoint is now also `tcp://host:port`:

- `ssh://alice@pc024` — discovery, below.
- `ssh://alice@pc024/tcp://127.0.0.1:7297` — forward to that address.
- A bare Unix path means streamlocal, as before. `tcp://` is refused where the host part is not a valid `host:port`.
- The pairing token comes from the pairing URL's `#k=`, as a LAN host's does — `ssh://alice@pc024/tcp://127.0.0.1:7297#k=<token>` — or from discovery. The fragment never reaches the wire; the token goes to the Keychain.

### What the user does

1. On the host: `gorex rexapi lan listen 127.0.0.1:7297`, `gorex rexapi lan allow 127.0.0.1/32`. (A LAN-bound listener works too — the forward can aim at its address — but loopback is the recommended shape.)
2. Add Host takes the address, optionally with `#k=` from `gorex rexapi lan`'s pairing URL.
3. First connect, host-key card and trust as IOS-023. With no token named, the engine execs `gorex rexapi lan token show` (or `rex` for an upstream gateway) for the token — see Changes below; a host that cannot answer asks the user to paste the token, as the refusal hint already does.

### The transport

- `dialSSH` picks the channel by the record's endpoint kind: `direct-streamlocal` for a Unix path, `direct-tcpip` for `tcp://`, with `127.0.0.1:0` as the originator address. Both land in the same `net.Conn`, so nothing past the channel changes.
- The TCP gateway's DNS-rebinding check (`internal/rexapi/host.go`) answers 421 to a Host it does not know, and the proxy's `<id>.ssh.invalid` is no name of its. Requests over an ssh channel therefore send `Host: localhost`, which the check always admits; a Unix endpoint skips the check, so the rewrite is safe there too.
- The discovery and token execs run on the host's login shell — `gorex … || rex …`, which cmd and the unix shells both read, with no `sh -c` (a Windows sshd has no sh). They work only when `gorex` or `rex` is on the remote PATH; a host where it is not needs the `#k=` token pasted.
- Discovery runs the same `server status --json` exec. The answer's `listeners[]` already carries `transport`: a `unix`/`named_pipe` entry feeds streamlocal; a `tcp` entry whose address is loopback feeds direct-tcpip. A named-pipe-only answer then falls back: `gorex rexapi lan status` names the gateway's address — bound to the LAN one, which a loopback probe would miss — else the loopback TCP probe; a Unix answer keeps today's behavior.
- For a tcp endpoint whose token the address did not name, a second exec reads it: `gorex rexapi lan token show` (upstream `rex` next), a command the CLIs already have — the user's own session may read `lan.json`, so nothing new is exposed. A host that answers neither is asked for the token as the refusal hint says.
- An `--allow-anonymous` or legacy-open endpoint gets no `Authorization` header; the store keeps no token.

### Changes, compact

- `HostConfig.SSH` gains `tcp` (`host`, `port`) beside `socket`; `auth` returns the lanToken for ssh-tcp hosts too.
- `ios/tailnet/ssh.go`: `sshHost.endpoint` becomes `unix | tcp`; `dialSSH` opens the matching channel; the status-exec parser learns `transport`, `endpoint` and the rexapi-lan token exec.
- `URLRequest.authorized` sends `Host: localhost` for `<id>.ssh.invalid` URLs, beside the bearer.
- Tests: fake sshd gains a `direct-tcpip` backend (trivial — it is a TCP listener), covering grammar, discovery fallback npipe→tcp, token fetch and the bearer header.

## Security

- The token crosses only the host's loopback inside the SSH channel; it is never logged and lives in the Keychain.
- A loopback-bound listener is the recommended deployment; a LAN-bound one works but exposes the gateway to the network as `rexapi lan` already documents.
- Refusing `tcp://` endpoints that are not on the host's own addresses is not enforced — `direct-tcpip` can name any address the sshd reaches, which is port forwarding's documented contract, and the same trust bar as the shell access SSH already grants.

## Non-goals

- Named pipes. Win32-OpenSSH cannot carry them; nothing here changes that.
- Installing or starting the gateway over SSH (`rex server install` style). The host must run `gorex rexapi lan` first.
- WebTransport/UDP listeners.
- TLS on the forwarded channel — SSH is the transport security, as it is for the Unix socket.

## Migration

`HostConfig.SSH` grows an optional `tcp` member; saved lists decode as before. A `tcp` endpoint in a list read by a build without this doc silently fails its dials, as an unknown socket path would.

## Phases

1. The address grammar, `direct-tcpip` dial and token handling against an existing `rexapi lan` loopback listener.
2. Discovery: `server status` TCP listeners and the rexapi-lan token exec.
