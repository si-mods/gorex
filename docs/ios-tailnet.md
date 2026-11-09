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
  --control-url`), and Rex Go gets the same field.

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

rootshell's `iosbridge/tailnet*.go` (kitknox/trzsz-ssh-rootshell, MIT)
is the reference for the iOS-specific parts: the interface monitor
`tsnet` lacks on iOS, the socket reset after a suspension and its
watchdog. We write our own rather than depend on rootshell's
`TrzszSSH.xcframework`: that one carries all of trzsz-ssh (a 60 MB
download) and pins a Tailscale we don't choose.

The node's state lives in the app's Application Support directory,
excluded from backups, with `NSFileProtectionCompleteUntilFirstUnlock`.
Its name is the phone's with `-rexgo`, such as `iphone-rexgo`, so it
never clashes with the Tailscale app's device for the same phone.

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
`URLSession` still verifies the `*.ts.net` certificate the gateway
serves.

The protocol code (`ControlConnection`, `DataStream`, `Spaces`,
`Upload`) changes only to take the host's session instead of
`URLSession.shared`.

Risk: that `URLSessionWebSocketTask` honours `proxyConfigurations` with
SOCKS5. Phase 1 proves it first. If it doesn't, the fallback is the
engine's `DialTCP`, handing Swift a socket file descriptor, with the
WebSocket on `NWConnection`. That one touches the protocol code more.

### Hosts

`HostConfig` gets a `transport`: `system` (today's, the default) or
`tailnet`. A scanned `https://*.ts.net` code while built-in Tailscale is
on, and a host added by hand with that switch on, use `tailnet`. The
host's details show the switch, so a user who also runs the Tailscale
app can choose either way.

### Settings

Settings › Tailscale, like the desktop's Tailscale card:

- **Built-in Tailscale** on or off.
- **Control server**: empty for Tailscale's own, or a URL such as
  `https://headscale.example.com`. The same rule as the server's
  `ValidControlURL`: `https`, or `http` only for a loopback, private or
  link-local address.
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
  `ResetSockets` first, then reconnects the hosts. A reset that doesn't
  finish restarts the engine. The hosts reconnect as they do now, with
  their reconnect token, so the sessions on the server go on.

### Server

No change. The gateway already serves HTTPS on the tailnet, checks the
caller with `WhoIs`, and takes a control URL. The phone's node signed in
as the same Tailscale user is let in by default.

## Build

`gomobile bind -target ios,iossimulator` builds `Tailnet.xcframework`
from `mobile/tailnet`, by a script in `ios/scripts/`. The TestFlight
workflow sets up Go and runs it before `xcodegen`; the result is cached
by the hash of `go.sum` and the package. `gomobile` is pinned as a Go
tool in `go.mod`. The framework is not checked in.

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
- On a phone: Tailscale app off, cellular only; switch Wi-Fi ↔ cellular
  with a terminal open; then the same against Headscale with a DERP
  relay of its own.

## Phases

1. **Engine and transport spike**: `mobile/tailnet`, the build script,
   and one host connected through the proxy in the simulator, which
   settles the WebSocket-over-SOCKS question.
2. **Settings and hosts**: the Tailscale settings, sign-in, the host's
   `transport`, the QR flow.
3. **Lifecycle**: path monitor, socket reset, engine restart.
4. **CI**: the framework built in the TestFlight workflow.
