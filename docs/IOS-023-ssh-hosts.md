---
name: IOS-023
description: "Rex Go reaches a Rex server over SSH, as upstream Rex's clients do. Use when you change SSH hosts, the engine's SSH client, or the Add Host form's address rules."
metadata:
  created: "2026-11-25"
  updated: "2026-11-25"
---

# IOS-023: Rex Go reaches a host over SSH

Status: phases 1 (#1124) and 2 built. Phase 3 not built.

## Problem

Rex Go accepts three kinds of host: an `http(s)://` address of a gateway, a tailcat address from a Rex Go code, and a device on the tailnet. An `ssh://` address is refused (`HostConfig.refusal`, #504). A machine whose only way in is `sshd` cannot be a host. That is the case for a server on a network the phone cannot dial, where the user's way in is already SSH.

Upstream Rex's clients accept `ssh://`. Its CLI takes `ssh://[USER@]HOST[:PORT]` wherever `--server` takes an endpoint, and its Mac app takes `ssh://user@host` in the same kind of form Rex Go has. This doc ports the feature, not the mechanism, which the next section describes.

## How upstream Rex does it

The picture below comes from the upstream Mac app (0.1.0) and its `rex` CLI.

The `rex` CLI spawns the system `ssh`:

- One shared connection (`ControlMaster`) carries the host. Later commands ride it. A full shared connection starts another.
- `ssh -L` forwards the remote Unix socket to a local one (`StreamLocalBindUnlink=yes`, `StreamLocalBindMask=0177`, `ExitOnForwardFailure=yes`). Clients then use the local socket.
- An internal `SSH_ASKPASS` server (`SSH_ASKPASS_REQUIRE=force`) turns the questions of `ssh` into prompts. A terminal asks on the spot. The app shows cards. A run with no terminal fails with "ssh cannot ask questions".
- `ssh -G` reads the effective config of the host. A managed `Include` line adds a `SetEnv TERM` guard for the hosts Rex knows.
- Socket discovery runs `rex server status --json` on the host. The command autostarts a personal server that is not running, unless the environment turns autostart off. `rex server install ssh://user@host` installs Rex on a Linux host, enables `rex.socket` with lingering, connects and adds the host.

The upstream Mac app speaks SSH itself, through SwiftNIO SSH (`RexSSH`):

- The keys come from an SSH agent. A setting chooses the agent socket: the macOS agent, a custom path, or the known paths of Secretive and 1Password.
- The app asks for a password or a key passphrase in a card (`SSHPromptCenter`). A password in the address is refused.
- The app keeps its own host-key trust. A first connect shows the fingerprint for approval. A changed key warns.
- `SSHWebSocketConnector` carries the Rex WebSocket over an SSH channel, and `SSHTunnel` keeps the SSH connection for the app and the CLI.

## How rootshell does it

rootshell (kitknox/rootshell, MIT) is a mature iOS terminal with native SSH. It is also the project whose `iosbridge` the engine's iOS-specific parts already follow (`ios/tailnet/lifecycle.go`). Its shape for this feature:

- The SSH client is Citadel (SwiftNIO SSH), in process. The Go side crosses gomobile's `Iosbridge*` protocols (`TSSHCallGate`), the same bridge this doc's engine calls use.
- `SOCKS5ProxyServer` routes local SOCKS requests to `direct-tcpip` channels. This is the same shape as the engine's proxy, from the other direction.
- `CitadelStreamLocalPipe` and `streamlocal-forward@openssh.com` forward a Unix socket to the app (a GPG agent, a remote askpass). The OpenSSH extension family is proven on iOS there.
- The host-key check is a request of two states (`HostKeyValidationRequest`): a new host, or a changed key that shows the trusted and the new fingerprint.
- Keyboard-interactive prompts are classified (`KeyboardInteractiveSecretKind`): a one-time-code prompt gets the OTP AutoFill, and the username is a hidden anchor for the OS password manager.
- `SSHCopyID` installs keys as `ssh-copy-id` does: it makes `~/.ssh` with its permissions, appends to `authorized_keys`, and verifies.
- Passwords can save to the Keychain (`SSHPasswordManager`). Keys live in the Keychain, or in the Secure Enclave as unexportable P-256.
- Its OpenSSH scope is wider: `known_hosts`, SSHFP DNS records, host certificates, `ProxyJump` and `~/.ssh/config`, agent forwarding, MPTCP and post-quantum key exchange.

## Goal

- The Add Host form accepts `ssh://user@host[:port][/socket]`. The host connects and lists its sessions as any other host.
- The feature works against any machine whose OpenSSH `sshd` the phone can log in to and that runs a Rex server. The server needs no new listener, port, certificate or pairing.
- The phone has a key of its own. The user installs it on the host from the app, or logs in with a password.
- The phone checks the host key on first connect and refuses a changed one.

## Non-goals

- `rex server install ssh://`, upstream's bootstrap of a host that has no Rex. The app reports that case.
- `~/.ssh/config`, `ProxyJump`, `known_hosts`, SSHFP records, host certificates, agents and agent forwarding. The phone has no agent, and rootshell's wider scope stays out.
- Saving the password in the Keychain, as rootshell's `SSHPasswordManager` offers. A later phase can add a per-host option.
- A device key in the Secure Enclave, as rootshell's P-256 keys. It cannot leave the device, but every signature would cross the delegate to Swift. The Keychain Ed25519 key covers the same ground at no cost.
- `ssh://` on the desktop: `gorex rex-import --rex ssh://...`, and `ssh://` as a remote-host address. A desktop doc can add them, through the system `ssh` as upstream's CLI does.
- Tailscale SSH. The built-in Tailscale's dialer can reach a peer's port 22, and a host that runs `tailscaled --ssh` needs no keys. A later phase can add it.
- A Rex server on Windows. It listens on a named pipe, which `direct-streamlocal` cannot open. GoRex's own `server.sock` there is a real AF_UNIX socket, but Win32-OpenSSH's `direct-streamlocal` connect still refuses it (measured on `OpenSSH_for_Windows_10.0p2`: channel accepted, every path form answered "open failed"). [IOS-024](IOS-024-ssh-tcp.md) reaches a Windows host through `direct-tcpip` to a TCP listener instead.

## Design

### What the user does

1. Add Host takes `ssh://alice@alice-laptop` or `ssh://alice@alice-laptop:2222/run/rex/server.sock`, or its fields directly: user, host, port, and an optional endpoint. Add saves the host.
2. On first connect a card shows the host's key fingerprint and type, as rootshell's request does. Trust & Save keeps it; Connect Once trusts it for the run, written nowhere; Cancel refuses. A different key on a later connect refuses, warns of a man-in-the-middle, and shows the trusted and the new fingerprint.
3. If the device key is not authorized, a card asks the host's password. After a login with a secret the user typed, the card offers to install the device's key on the host. A reconnect on the run's cached password does not offer again, so Not Now holds until the user types a password again.
4. The host's detail shows the trusted fingerprint and the device's public key, with copy. Forget host key trusts anew on next connect.

### The address

An SSH address is `ssh://user@host[:port][/socket]`, as upstream's Mac app spells it:

- `user` is required. The phone has no login name to default to, and upstream's app asks for one the same way.
- `port` defaults to 22.
- `socket` is the remote socket path. Without it, discovery finds the socket.
- A password, a query, a fragment, spaces and control characters are refused, as upstream refuses them.
- `HostConfig.refusal` names each fault in the field's footer, as it names `ssh` today.
- The form's SSH fields (user, host, port, and an optional endpoint) make the same record the address makes: they are read by the address's grammar, so a field takes no value the address refuses (a port, a space or a `/` in the host). The user and the endpoint go in percent-encoded, as an address spells them, so a Windows login such as `alice@example.com` and a socket path with a space are taken. Typing in fields beats spelling a URL: the user names the login once, not inside an address.

`HostConfig` gains an `ssh` member (`HostConfig.SSH`: user, host, port, socket). The host's `url` is `http://<id>.ssh.invalid`, made as `tailcatURL` makes the tailcat one. `reach(_:)` returns a new `Transport.ssh` when `ssh` is present. Every request of the host then goes through the engine's loopback proxy, as a tailcat host's do.

### The transport

The engine (`ios/tailnet`) speaks SSH through `golang.org/x/crypto/ssh`. The module is in the module graph already, as a dependency of Tailscale.

- `SetSSHHosts`, as `SetTailcatHosts`, gives the engine the SSH hosts by id.
- The proxy's `dial` maps `<id>.ssh.invalid` to the host's SSH client. One `ssh.Client` per host carries every channel, as upstream's shared connection does, without its files.
- Each connection is a `direct-streamlocal@openssh.com` channel to the host's socket (OpenSSH 6.7 and later). A server without it fails the dial, and the error says it needs OpenSSH.
- A dead client drops its channels. The next dial makes a new client, so a suspension or a network change costs a reconnect and nothing more. The engine's own resume path reopens the proxy listener.

### Socket discovery

A host whose address has no socket path learns it on its first connect:

1. An exec channel runs `rex server status --json`, which autostarts a personal server as upstream's commands do. The primary Unix listener of the answer is the socket.
2. With no `rex` on the host's non-interactive `PATH`, the error says Rex is not installed there, as upstream's message does: "no rex socket on <host>". GoRex's `gorex` runs first and `rex` next, each in an exec of its own; only an exec that exits 0 is an answer. A unix listener that is not the primary one is taken when no primary one is listed.
3. With a `rex` too old to report listeners, or an answer that lists no unix listener, the dial tries the defaults in order, before any tcp listener of the answer ([IOS-024](IOS-024-ssh-tcp.md)): `$XDG_RUNTIME_DIR/rex/server.sock`, `~/.local/share/rex/server.sock`, `/run/rex/server.sock`, `/var/run/rex/server.sock`, `~/Library/Application Support/rex/server.sock`. The last two also reach a shared server when the user is in its group.

### Trust

- The device key is an Ed25519 key the engine makes (`NewSSHKey`, `SSHPublicKey`, `SetSSHKey`, as the tailcat key's calls). The app keeps the private key in the Keychain, where the tailcat client key lives. The key stays on the device, as the tailcat key does. [`IOS-026`](IOS-026-ssh-keys.md) extends this to a multi-key store and Settings UI (`SetSSHKeys`, `ParseSSHKey`).
- The host key is trusted on first use. The card shows its SHA256 fingerprint and its type, as rootshell's request does, and offers Trust & Save, Connect Once and Cancel. Save writes it to the host's record; Once marks it trusted for the run only (`sshTrusted`), written nowhere. A changed key refuses, warns of a man-in-the-middle, shows both fingerprints, and only Forget clears it.
- A question outlives its dial. Two dials of one host at the same fingerprint share one card and one answer; a retry after a timed-out dial reuses the pending card instead of asking again, as rootshell's serialised queue does. A second fingerprint while a card is up queues behind it. A question also outlives its record: Trust & Save changes the record while a password card can still show, and the new record's dial shares that card. A card's dismiss refuses the question that card showed, never the one that a button's answer made current.
- A changed key is one that differs from the record's fingerprint, or, with none saved, from the one Connect Once trusted for the run. Either way the card warns of a man-in-the-middle and shows both. With none saved, the card offers Connect Once again beside Replace & Connect, so the user's choice not to save holds.
- Authentication tries the device key, then `password`, then `keyboard-interactive`. Each of the last two asks in a card. The card classifies the prompt, as rootshell does: a one-time-code prompt (any case) gets the OTP AutoFill, and the host's user is the anchor for the OS password manager. The password lives in memory for the run and is never written; a keyboard-interactive prompt that asks a password takes it too, as PAM's sshd (the macOS default) asks there. A keyboard-interactive password is kept only once the host takes it, as PAM refuses a wrong one without asking again, and a cached password the host refuses is dropped, so the next connect asks. A keyboard-interactive card says "try again" only for a prompt that an earlier round asked, not for the second question of one round. An empty answer is an answer (Return at a notice, an empty password); only Cancel refuses.
- Install key runs an exec on the host after a password login, as `ssh-copy-id` does: it makes `~/.ssh` with its permissions and appends the public key to `authorized_keys` under a `rexgo` comment. A second exec then checks that `authorized_keys` holds the key. A command that exits non-zero (a host whose shell is cmd, a read-only `~/.ssh`) or a key the check does not find shows the error in the card, which stays down while the install runs and comes back with it.
- The engine asks the app through a delegate, as `ShareDelegate` works for hotspot share and rootshell's `Iosbridge*` callbacks do: a question (host key, password) carries an id, and the app's `SSHAnswer` replies. A run that cannot ask fails the dial, as upstream's does.

## Alternatives

- **Spawn `ssh -L`, as upstream's CLI.** iOS has no `ssh` binary and an app spawns no processes. Not possible.
- **SwiftNIO SSH (Citadel), as upstream's app and rootshell.** Both mature apps chose it, and it is proven on iOS. It adds a large Swift dependency and puts the transport in Swift, while the engine already owns the proxy every host uses. A Swift-side channel cannot return a `net.Conn` to the Go proxy, so the app would need a second transport stack. `x/crypto/ssh` covers the same protocols in the place the dial happens.
- **`direct-tcpip` to the gateway's TCP port.** It needs a TCP listener the server does not open by default, and a token or anonymous access on it. The Unix socket needs nothing.
- **An exec channel running `nc -U` or `socat`.** It works on servers without streamlocal, but needs the tool installed. Streamlocal needs only OpenSSH.

## Security

- SSH is the admission. The socket admits its own user by peer credentials, so whoever logs in as the user controls the server. This is upstream's model too: the key the user installs is the pairing.
- The host key check is the only check of the server's side. It is stored, and a changed key stops the connection.
- The private key lives in the Keychain. The password is never stored. The client speaks no agent protocol and forwards none.
- A streamlocal channel opens only the socket discovery found or the address named.

## Migration

`hosts` in UserDefaults decodes as one list (`docs/irreversible.md`). A `Transport` case an older build does not know makes its decode fail, and the whole list is lost on a downgrade. So an SSH host writes `ssh` and leaves `transport` unset; `reach(_:)` derives `Transport.ssh` from `ssh != nil`, and `Transport.ssh` is never written. An older build reads the host as a system host whose `http://<id>.ssh.invalid` address answers nothing. The list stays whole.

## Tests

- `internal`-side Go tests in `ios/tailnet`: the `ssh://` grammar, `SetSSHHosts`, a fake `sshd` (`x/crypto/ssh` server) that serves a streamlocal channel to a Rex API socket, a proxy dial of `<id>.ssh.invalid` end to end, discovery parsing of `server.status` JSON, and reconnect after a dead client.
- Swift tests: `HostConfig`'s `ssh` parse and each refusal, a saved-list round trip, and a fixture without `ssh` that decodes as a system host.
- iOS UI tests (on the Mac mini): the host-key card, the password card, and the install-key flow.

## Phases

1. Engine transport and discovery, device key, host-key trust, Add Host grammar, `Transport.ssh`. The user installs the key by hand at first. (#1124)
2. Password and keyboard-interactive auth, the install-key flow, the host detail's fingerprints and Forget, Connect Once, shared answers and pending questions, and the SSH fields of Add Host.
3. Later docs as needed: Tailscale SSH, `ssh://` on the desktop.
