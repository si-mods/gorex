# Rex Go: built-in Tailscale

Status: proposal. Not implemented. Answers part of
[#330](https://github.com/si-mods/gorex/issues/330).

## Problem

Rex Go reaches a GoRex machine in one of two ways today:

- **The tailnet.** The QR code holds `https://<machine>-gorex.<tailnet>.ts.net`,
  and the phone gets there through the Tailscale app's system VPN. The
  VPN stays on for every app on the phone, and many users don't want
  that: it fights other VPNs, and it changes how the rest of the phone
  reaches the network.
- **The local network.** `http://<address>:7297` over plain HTTP, open to
  the networks allowed. It needs no VPN, but it isn't encrypted, has no
  credential, and works only on that network.

The desktop side already solved this: the GoRex server's gateway joins
the tailnet as a device of its own (`internal/rexapi/tailnet.go`), with
no Tailscale installed. The phone has no equivalent.

## Goal

Rex Go joins the tailnet itself, inside the app, as rootshell's
"rootshell Only" mode does: Tailscale runs in the app's process with a
userspace network stack, and only the app's own connections go through
it. No system VPN, no Tailscale app.

That covers two of #330's cases:

- **No VPN on the phone**, on Wi-Fi or cellular.
- **A machine with no public address**, through a relay the user runs:
  a Headscale control server and a DERP relay. Peers connect directly
  when NAT allows and through DERP when it doesn't. The GoRex server
  already accepts a control URL (`gorex rexapi tailscale on
  --control-url`), and Rex Go gets the same field. Headscale issues no
  HTTPS certificates, so the gateway needs a change for it (Server,
  below).

## Non-goals

- **A login of GoRex's own.** The phone's identity is its tailnet
  identity: the gateway lets in the Tailscale user who signed the
  machine's device in, or the tailnet's members with
  `--allow-tailnet-members`. A device is revoked in the Tailscale admin
  console or with `headscale nodes delete`. #330 also asks for pairing
  and revocation that need no tailnet. That is separate work on the
  local network listener (HTTPS plus per-device credentials) and gets its
  own document.
- **A direct connection with no control server.** The data path between
  two public addresses is direct, but keys still go through a control
  server: Tailscale's or the user's Headscale.
- **Running in the background.** iOS suspends the app, and the engine
  with it. Rex Go is a foreground terminal; its sessions live on the
  server and reattach.
- **Replacing the system VPN path.** Hosts reached through the Tailscale
  app, or on the local network, keep working as they do.

## Design

### Engine

A small Go package, `mobile/tailnet`, in GoRex's module, so it pins the
same `tailscale.com` as the server. It wraps `tsnet.Server` and exports a
narrow API for `gomobile bind`:

| Call | Does |
| --- | --- |
| `Start(configJSON, stateDir, callback)` | starts the node: hostname, control URL, optional one-time auth key |
| `Login()` / `Logout()` | interactive login; the auth URL arrives through the callback |
| `Status() string` | JSON: backend state, auth URL, the node's name and addresses, peers |
| `ProxyStart() string` | starts the SOCKS5 proxy (below); returns port, user, password |
| `DefaultInterfaceChanged(name)` | the path monitor's news of a network change |
| `ResetSockets()` | rebinds the UDP sockets iOS reclaimed while suspended |
| `Stop()` | stops the proxy and the node |
| `ValidControlURL(url) bool` | the server's own check of a control URL, for Settings |

rootshell's `iosbridge/tailnet*.go` (kitknox/trzsz-ssh-rootshell, MIT)
is the reference for the iOS-specific parts: the interface monitor
`tsnet` lacks on iOS, the socket reset after a suspension and its
watchdog. We write our own rather than depend on rootshell's
`TrzszSSH.xcframework`: that one carries all of trzsz-ssh (a 60 MB
download) and pins a Tailscale we don't choose.

The node's state lives in the app's Application Support directory,
excluded from backups, with `NSFileProtectionCompleteUntilFirstUnlock`.
Its name defaults to `rexgo-` and four random letters, such as
`rexgo-kqzt`, and the user can change it in Settings (the engine applies
it as the server's `SetHostname` does). The phone's own name is not
used: since iOS 16 `UIDevice.name` returns only the model ("iPhone")
without an entitlement Apple grants case by case, so two phones would
show the same name, and one could be revoked for the other.

### Transport

Rex Go talks to a host only through `URLSession` (control and data
WebSockets, `/api/spaces`, uploads). A host on the built-in tailnet gets
a `URLSession` of its own whose configuration points at the engine's
proxy:

```swift
let c = URLSessionConfiguration.default
var proxy = ProxyConfiguration(socksv5Proxy: .hostPort(host: "127.0.0.1", port: port))
proxy.applyCredential(username: user, password: pass)
c.proxyConfigurations = [proxy]
```

The proxy listens on loopback only, with a random user and password per
start, so another app on the phone can't use it. It resolves the name
itself: MagicDNS names and tailnet addresses go through the tailnet, and
anything else is refused (not dialed directly). Over HTTPS,
`URLSession` still verifies the gateway's `*.ts.net` certificate. Over a
Headscale tailnet the URL is `http://`: WireGuard already encrypts it end
to end, and the gateway knows the caller by its tailnet address
(Server, below).

Each `ProxyStart` makes a new port, user and password, and a
`URLSession`'s configuration is fixed when it's made. So the proxy's
address belongs to the engine's current run: when the engine starts
again, every `tailnet` host drops its session, makes a new one and
reconnects.

The protocol code (`ControlConnection`, `DataStream`, `Spaces`,
`Upload`) changes only to take the host's session instead of
`URLSession.shared`.

Risks, which phase 1 proves first:

- that `URLSessionWebSocketTask` honours `proxyConfigurations` with
  SOCKS5;
- that `URLSession` hands the proxy the host's name (SOCKS5 `DOMAINNAME`)
  rather than resolving it first through the phone's DNS, which knows no
  MagicDNS name without the Tailscale VPN. Plain requests and WebSockets
  both.
 If it doesn't, the fallback is the
engine's `DialTCP`, handing Swift a socket file descriptor, with the
WebSocket on `NWConnection`. That one touches the protocol code more.

### Hosts

`HostConfig` gets `transport: Transport?`: `nil` or `.system` is
today's way, `.tailnet` the engine's. It is optional because
`HostConfig`'s synthesized `Decodable` ignores default values: a new
required key would fail to decode every saved list, and `HostStore`'s
`try?` would turn that into no hosts at all. A test decodes a list saved
by the current version.

A host uses `tailnet` when built-in Tailscale is on and its name is
under the tailnet's MagicDNS suffix (from the engine's status: `ts.net`
or Headscale's `base_domain`) or its address is in the tailnet's ranges:
a scanned code, or one added by hand. The host's details show the
switch, so a user who also runs the Tailscale app can choose either
way. `HostStore.update` reconnects a host whose transport changed, as it
does one whose address changed.

### Settings

Settings › Tailscale, like the desktop's Tailscale card:

- **Built-in Tailscale** on or off.
- **Control server**: empty for Tailscale's own, or a URL such as
  `https://headscale.example.com`. Checked by the engine's
  `ValidControlURL`, the server's own rule (`https`, or `http` only for a
  loopback, private or link-local address), not a Swift copy of it. The
  check moves out of `internal/rexapi` into a small package both import.
- **Device name**: `rexgo-xxxx` by default, as above.
- **Sign in**: opens the auth URL in `ASWebAuthenticationSession`; or an
  auth key, used once and never saved.
- **Status**: signed in as whom, this device's name and address, and
  whether the GoRex hosts are reached directly or through DERP.
- **Sign out**, which removes the node's state.

### Lifecycle

- At launch, with built-in Tailscale on, the engine starts before the
  hosts connect; a host on `tailnet` waits for the node to run.
- `NWPathMonitor` reports each change of the default interface to the
  engine, so it rebinds when the phone moves between Wi-Fi and cellular.
- Back in the foreground after more than 5 s away, the app calls
  `ResetSockets` first, then reconnects every `tailnet` host with
  `reconnect()`, not `reconnectIfNeeded()`: the WebSocket to the
  loopback proxy can look open while its tailnet side died in the
  suspension. A reset that doesn't finish restarts the engine, and the
  hosts make new sessions for the new proxy (Transport). They reconnect
  with their reconnect token, so the sessions on the server go on.

### Server

The gateway already checks each caller with `WhoIs` and takes a
control URL. One change:

- **Plain HTTP on the tailnet when there is no certificate.** Today
  `listen` takes only `ListenTLS` on :443, and its failure turns built-in
  Tailscale off. Headscale issues no certificates, so it always fails
  there. The gateway also listens on :80 of the tailnet, and keeps
  running when the certificate fails; the identity check is the same
  `WhoIs`, which needs no TLS. Status, the QR code and `rexapi pair` give
  `http://<name>` when there is no HTTPS. :80 serves only tailnet peers
  and is safe for the same reason: WireGuard encrypts and authenticates
  each packet.

Who gets in stays as it is (`identify`). Without
`--allow-tailnet-members`, the gateway lets in only an untagged device of
the user who signed the gateway's device in, and only when the gateway's
device is untagged itself. So Rex Go signs in as that user, not with a
tagged auth key; a tagged setup needs `--allow-tailnet-members` and the
tailnet's access rules.

## Build

`gomobile bind -target ios,iossimulator` builds `Tailnet.xcframework`
from `mobile/tailnet`, by a script in `ios/scripts/`. The TestFlight
workflow sets up Go and runs it before `xcodegen`; the result is cached
by the hash of `go.mod`, `go.sum` and the package. The workflow's
`paths` add `mobile/**`, `go.mod` and `go.sum`, so an engine fix or a
`tailscale.com` bump reaches TestFlight like an `ios/` change.
`gomobile` is pinned as a Go tool in `go.mod`. The framework is not checked in.

Size, measured with `tsnet` at the version `go.mod` pins, linked into an
arm64 iOS executable with `-s -w` and stripped:

| Build | Installed | Compressed (gzip -9, near the download) |
| --- | --- | --- |
| all features | 18.5 MB | 7.1 MB |
| `ts_omit_*` for every feature `tsnet` can do without | 15.8 MB | 6.0 MB |

The `ts_omit_*` tags kept off are `netstack`, `serve`, `acme`, `dns`,
`logtail`, `tailscaledhttps`, `cachenetmap` and `ssh`; `tsnet` doesn't
build without them. The simulator slice never reaches the App Store.

The engine can't be downloaded later instead. iOS runs only code
signed into the app's bundle (a downloaded library fails to load), and
App Review Guideline 2.5.2 forbids downloading executable code;
On-Demand Resources can't hold any.

## Test plan

- Go unit tests for the proxy: the credentials, refusing names off the
  tailnet, and closing both sides.
- The engine runs in the simulator (no network extension involved), so
  the end-to-end test runs on the Mac mini: a simulator with no
  Tailscale reaches a GoRex machine's `*-gorex` device, opens a terminal,
  and keeps it through a background and foreground.
- Rex Go's host list saved by the current version decodes after the
  update, and a transport change reconnects the host.
- The gateway over Headscale: `ListenTLS` fails, :80 serves, and a
  tailnet caller of another user is refused.
- On a phone: Tailscale app off, cellular only; switch Wi-Fi ↔ cellular
  with a terminal open; then the same against Headscale with a DERP
  relay of its own.

## Phases

1. **Engine and transport spike**: `mobile/tailnet`, the build script,
   and one host connected through the proxy in the simulator, which
   settles both transport risks.
2. **Server**: the gateway's plain HTTP on the tailnet without a
   certificate, and the control URL check in its own package. It helps
   any Headscale client, so it can ship before the app.
3. **Settings and hosts**: the Tailscale settings, sign-in, the device
   name, the host's `transport`, the QR flow.
4. **Lifecycle**: path monitor, socket reset, forced reconnect, engine
   restart.
5. **CI**: the framework built in the TestFlight workflow, and its
   `paths`.
