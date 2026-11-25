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
3. First connect, host-key card and trust as IOS-023. With no token named, the engine reads the token over SSH without making one — see The transport below; a host that cannot answer asks the user to paste the token, as the refusal hint already does.

### The transport

- `dialSSH` picks the channel by the record's endpoint kind: `direct-streamlocal` for a Unix path, `direct-tcpip` for `tcp://`, with `127.0.0.1:0` as the originator address. Both land in the same `net.Conn`, so nothing past the channel changes.
- The TCP gateway's DNS-rebinding check (`internal/rexapi/host.go`) answers 421 to a Host it does not know, and the proxy's `<id>.ssh.invalid` is no name of its. Requests over an ssh channel therefore send `Host: localhost`, which the check always admits; a Unix endpoint skips the check, so the rewrite is safe there too.
- The discovery and token execs run on the host's login shell, one exec for each CLI name: `gorex …` first, then `rex …` when it fails. No `a || b` and no `sh -c`: Windows PowerShell 5.1 (a common sshd `DefaultShell`) and nushell cannot read `||`, and a Windows sshd has no sh. An exec counts only when it exits 0: a non-zero exit status, a signal or a missing status is no answer, whatever it printed. The execs work only when `gorex` or `rex` is on the remote PATH; a host where it is not needs the `#k=` token pasted.
- Discovery runs the same `server status --json` exec, and takes the first of:
  1. a `unix` listener of the answer, the primary one first;
  2. the first usual socket path that exists (IOS-023), so a Unix host keeps its socket even when status also lists a tcp listener;
  3. a `tcp` listener, a loopback one first, at the address it is bound to (`addresses`), which shows the port that a listen on port 0 got, else at its `endpoint`;
  4. the gateway `gorex rexapi lan status` names — bound to the LAN address, which a loopback probe would miss — or the `listen:` address of a gateway that is on but has not reported one (`lan: on, waiting …`); a gateway that is off or in error names none;
  5. for a server whose answer lists a `named_pipe` listener, the loopback TCP probe (`127.0.0.1:7297`).

  A host where none of these answers fails the dial with "no rex socket on <host>", as IOS-023's does, and runs no token exec.
- For a tcp endpoint whose token the address did not name, the engine reads it without making one. `token show` alone would make and save a token in `lan.json` on a host that has none, so the engine first runs `rexapi lan status`, and runs `token show` (with the same CLI name) only when the status says `pairing token: set`. Both lines came in the same GoRex release (#684, v0.36.0), so every GoRex host that has `token show` answers the status this way, and an older host needs no new command. What upstream `rex` prints there is not checked; a host whose status lacks the line gets no token read, and needs the `#k=` token pasted. The user's own session may read `lan.json`, so nothing new is exposed. Only an exec that exits 0 is kept, "" included for a host whose gateway has no token; a failed one keeps nothing, and a dial 30 seconds later tries again. A host that answers neither is asked for the token as the refusal hint says.
- The engine reads the token for every tcp endpoint, as status cannot tell which listener needs it: the LAN gateway's listener reports `admission: anonymous` (no peer credentials) though it asks for the token. A listener that lets callers in without a token, such as `--allow-anonymous` or an address on the legacy list, does not read the `Authorization` header, so the header the app sends costs nothing there. A host whose gateway has no token gives none, and the store keeps none.
- A gateway that refuses a connect — with the token the app kept, after `gorex rexapi lan token rotate` or with a token of another gateway, or with none, as the engine kept "" from a read before the gateway had a token — makes the app tell the engine to forget its token (`SSHForgetHostToken`) and dial once more, which reads the token again. A connect that still refuses waits for the user.

### Changes, compact

- `HostConfig.SSH` gains `tcp` (`host`, `port`) beside `socket`; `auth` returns the lanToken for an ssh host that names no socket (a tcp:// endpoint or a discovered one), and the store reads it from the Keychain for the same hosts (`sendsToken`).
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
