# Rex Go: built-in Tailscale

Status: proposal. Not implemented. Answers part of
[#330](https://github.com/si-mods/gorex/issues/330).

## Problem

Away from the local network, Rex's own iPhone app reaches a server only
through the Tailscale app: without that app installed, signed in and its
VPN on, it reaches nothing. That dependency is the pain this design
removes. Rex Go inherited it, as it speaks Rex's API.

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

The scope is the iOS app alone: `ios/` and its build. The GoRex
server and its gateway stay as they are.

That covers #330's first need, **no VPN on the phone**, on Wi-Fi or
cellular, for a GoRex machine whose built-in Tailscale is on. The
machine needs no public address: peers connect directly when NAT
allows, and through Tailscale's DERP relays when it doesn't.

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
- **Server changes.** Everything the phone needs from the gateway is
  there already (Server, below).
- **Headscale, for now.** Rex Go takes a control URL, as the server
  does, so the phone can join a Headscale tailnet. But the gateway
  serves only HTTPS on the tailnet, and Headscale issues no
  certificates, so a GoRex machine on Headscale has nothing for the
  phone to reach. That needs the gateway to serve the tailnet without a
  certificate: server work for its own document. #330's self-hosted
  relay case waits for it.
- **Running in the background.** iOS suspends the app, and the engine
  with it. Rex Go is a foreground terminal; its sessions live on the
  server and reattach.
- **Replacing the system VPN path.** Hosts reached through the Tailscale
  app, or on the local network, keep working as they do.

## Design

### Engine

A small Go module of its own, `ios/tailnet`, so the whole change stays
under `ios/`. It pins `tailscale.com` itself, at the server's version
when it's made; the two needn't move together, as any Tailscale client
talks to any other. It wraps `tsnet.Server` and exports a narrow API for
`gomobile bind`:

| Call | Does |
| --- | --- |
| `Start(configJSON, stateDir, callback)` | starts the node: hostname, control URL, optional one-time auth key |
| `Login()` / `Logout()` | interactive login; the auth URL arrives through the callback |
| `Status() string` | JSON: backend state, auth URL, the node's name and addresses, peers |
| `ProxyStart() string` | starts the SOCKS5 proxy (below); returns port, user, password |
| `DefaultInterfaceChanged(name)` | the path monitor's news of a network change |
| `ResetSockets()` | rebinds the UDP sockets iOS reclaimed while suspended |
| `Stop()` | stops the proxy and the node |
| `ValidControlURL(url) bool` | the check of a control URL, for Settings |

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
it as the gateway's `SetHostname` does). The phone's own name is not
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
anything else is refused (not dialed directly). TLS is end to end:
`URLSession` still verifies the gateway's `*.ts.net` certificate.

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
under the tailnet's MagicDNS suffix (from the engine's status) or its
address is in the tailnet's ranges:
a scanned code, or one added by hand. The host's details show the
switch, so a user who also runs the Tailscale app can choose either
way. `HostStore.update` reconnects a host whose transport changed, as it
does one whose address changed.

### Settings

Settings › Tailscale, like the desktop's Tailscale card:

- **Built-in Tailscale** on or off.
- **Control server**: empty for Tailscale's own, or a URL such as
  `https://headscale.example.com`. The engine's `ValidControlURL`
  applies the server's rule (`https`, or `http` only for a loopback,
  private or link-local address). It is a copy, since `ios/tailnet` can't
  import the server's `internal/rexapi`; it runs the same test cases as
  the server's, so a drift shows.
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

No change, and none in scope. The gateway already serves HTTPS on the
tailnet, checks each caller with `WhoIs`, and takes a control URL.

Who gets in is the gateway's `identify`: without
`--allow-tailnet-members`, only an untagged device of the user who
signed the gateway's device in, and only when the gateway's device is
untagged itself. So Rex Go signs in as that user, not with a tagged auth
key; a tagged setup needs `--allow-tailnet-members` and the tailnet's
access rules. Settings says so when the gateway refuses the phone.

## Build

`gomobile bind -target ios,iossimulator` builds `Tailnet.xcframework`
from `ios/tailnet`, by a script in `ios/scripts/`. The TestFlight
workflow sets up Go and runs it before `xcodegen`; the result is cached
by the hash of `ios/tailnet` (its `go.mod`, `go.sum` and sources). Its
`paths`, `ios/**`, already take in an engine fix or a `tailscale.com`
bump. `gomobile` is pinned as a Go tool in `ios/tailnet/go.mod`. The framework is not checked in.

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
- On a phone: Tailscale app off, cellular only; switch Wi-Fi ↔ cellular
  with a terminal open.

## Phases

1. **Engine and transport spike**: `ios/tailnet`, the build script,
   and one host connected through the proxy in the simulator, which
   settles both transport risks.
2. **Settings and hosts**: the Tailscale settings, sign-in, the device
   name, the host's `transport`, the QR flow.
3. **Lifecycle**: path monitor, socket reset, forced reconnect, engine
   restart.
4. **CI**: the framework built in the TestFlight workflow.
