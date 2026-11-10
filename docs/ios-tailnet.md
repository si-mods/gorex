# Rex Go: built-in Tailscale

Status: implemented, in phases #369, #371, #375, #381 and #382. Answers part of
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

The phone and the gateway share one implementation of built-in
Tailscale: one `tailscale.com`, one wrapper of `tsnet`, one check of a
control URL. The gateway's behavior stays as it is; its code only moves
into a package both use (Shared package, below).

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
- **Changing what the gateway does.** Everything the phone needs from
  it is there already (Server, below).
- **Headscale, for now.** Rex Go takes a control URL, as the server
  does, so the phone can join a Headscale tailnet. But the gateway
  serves only HTTPS on the tailnet, and Headscale issues no
  certificates, so a GoRex machine on Headscale has nothing for the
  phone to reach. #330's self-hosted case doesn't need a tailnet at
  all: Rex Go reaches GoRex over tailcat ([ios-tailcat.md](ios-tailcat.md)).
- **Running in the background.** iOS suspends the app, and the engine
  with it. Rex Go is a foreground terminal; its sessions live on the
  server and reattach.
- **Replacing the system VPN path.** Hosts reached through the Tailscale
  app, or on the local network, keep working as they do.

## Design

### Engine

A Go package, `ios/tailnet`, in GoRex's own module (no `go.mod` of its
own), so it uses the server's `tailscale.com` and may import
`internal/tailnode`. It adds only what the phone needs on top, and
exports a narrow API for `gomobile bind`:

| Call | Does |
| --- | --- |
| `Start(configJSON, stateDir, callback)` | starts the node: hostname, control URL, optional one-time auth key |
| `Login()` / `Logout()` | interactive login; the auth URL arrives through the callback |
| `Status() string` | JSON: backend state, auth URL, the node's name and addresses, peers and how each is reached |
| `SetHostname(name)` | renames the running node, as the gateway's `SetHostname` |
| `Proxy() string` | the SOCKS5 proxy (Transport): address and password, one per process |
| `DefaultInterfaceChanged(name)` | the path monitor's news of a network change |
| `ResetSockets()` | rebinds the UDP sockets iOS reclaimed while suspended |
| `Stop()` | stops the node |
| `ValidControlURL(url) bool` | `internal/tailnode`'s check, for Settings |
| `ValidHostname(name) bool` | `internal/tailnode`'s device name check, for Settings |

rootshell's `iosbridge/tailnet*.go` (kitknox/trzsz-ssh-rootshell, MIT)
is the reference for the iOS-specific parts: the interface monitor
`tsnet` lacks on iOS, the socket reset after a suspension and its
watchdog.

Two ready-made engines were weighed and set aside:

- rootshell's `TrzszSSH.xcframework` carries all of trzsz-ssh (a 60 MB
  download) and pins a Tailscale we don't choose.
- Tailscale's own TailscaleKit (`tailscale/libtailscale`, Swift over C
  over Go) has no hook for either iOS-specific part: its C API is
  new/start/up/close, listen, dial, loopback and status. Adding them
  means forking three layers. It has no releases either, so it is built
  from source with Go all the same, and it pins its own `tailscale.com`.
  Its `URLSession` extension is the model for ours (Transport).

### Shared package

`internal/tailnode` takes from `internal/rexapi/tailnet.go` what isn't
the gateway's own:

- the node: `tsnet.Server` with its local client (start, status, watch,
  login, logout, set hostname, close), behind the interface the gateway's
  tests fake;
- `ValidControlURL` and the device name check.

The gateway keeps its listener, `identify`, its settings and its
methods, and imports the rest. The move changes no behavior; the tests
of what moved move with it.

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
proxy.applyCredential(username: "tsnet", password: password)
c.proxyConfigurations = [proxy]
```

This is TailscaleKit's `URLSessionConfiguration.proxyVia`. The proxy is
the engine's own (`ios/tailnet/proxy.go`): `tailscale.com/net/socks5` on
loopback, user `tsnet` and a random password per process, so another app on
the phone can't use it. It dials only the tailnet's devices: it takes
the target's MagicDNS name (full or short) or address, looks it up among
the node's current peers, and dials that peer's Tailscale address
through the node. Anything else is refused: an unknown name, an address
off the tailnet, or any address before the network map arrives. TLS is
end to end: `URLSession` still verifies the gateway's `*.ts.net`
certificate.

`tsnet`'s own proxy, `Server.Loopback()`, is not used. It dials any
address that isn't a known peer through the phone's network, as every
tailnet address is in the second or so after a start before the network
map arrives. The spike saw exactly that: the first connections went to
the gateway over the home network, which happened to route the tailnet,
not through Tailscale. Its port also serves the LocalAPI; the engine's
proxy exposes no LocalAPI on any port.

The proxy keeps its port and password for the process, but its tailnet
connections end with the engine's run: when the engine starts again,
every `tailnet` host drops its session, makes a new one and reconnects.
iOS can also reclaim a suspended app's listening socket (TN2277): a proxy
whose listener died is replaced by the next `Proxy()`, of a new port and
password, which `resume()` reads after the socket reset; the hosts'
next session then goes through it.

The protocol code (`ControlConnection`, `DataStream`, `Spaces`,
`Upload`) changes only to take the host's session instead of
`URLSession.shared`.

Two risks, which phase 2 settled in the simulator; both hold:

- that `URLSessionWebSocketTask` honours `proxyConfigurations` with
  SOCKS5;
- that `URLSession` hands the proxy the host's name (SOCKS5 `DOMAINNAME`)
  rather than resolving it first through the phone's DNS, which knows no
  MagicDNS name without the Tailscale VPN. Plain requests and WebSockets
  both.

For the second, a request and a WebSocket to a MagicDNS short name, which
the Mac mini's DNS could not resolve, reached the gateway through the
proxy and met its certificate. Had either risk failed, the fallback was
the engine's `DialTCP`, handing Swift a socket file descriptor, with the
WebSocket on `NWConnection`.

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

Settings › Tailscale, after rootshell's VPN settings: a status card on top (the Tailscale tile with a state dot, a title and a subtitle) with the switch:

- **Built-in Tailscale** on or off.
- **Control server**: empty for Tailscale's own, or a URL such as
  `https://headscale.example.com`. Checked by `internal/tailnode`'s
  `ValidControlURL`, the gateway's own rule (`https`, or `http` only for
  a loopback, private or link-local address).
- **Device name**: `rexgo-xxxx` by default, as above.
- **Sign in**: opens the auth URL in `ASWebAuthenticationSession`; or an
  auth key, used once and never saved.
- **Status**: signed in as whom, this device's name and address, and
  whether the GoRex hosts are reached directly or through DERP.
- **Sign out**, which removes the node's state.

### Lifecycle

- At launch, with built-in Tailscale on, the engine starts before the
  hosts connect; a host on `tailnet` waits for the node to run.
- While the engine runs, and before each start, `NWPathMonitor` reports
  each change of the phone's network to the engine: of the interface,
  so it rebinds when the phone moves between Wi-Fi and cellular, and of
  the gateways, as from one Wi-Fi to another. The engine hands the
  interface's name to `netmon`, as Tailscale's own app does, and has it
  check the network again.
- Back in the foreground after more than 5 s away, with the engine
  running, the app calls `ResetSockets` first, then reconnects every
  `tailnet` host with `resume()`, not `reconnectIfNeeded()`: the
  WebSocket to the loopback proxy can look open while its tailnet side
  died in the suspension. Unlike `reconnect()`, `resume()` keeps the
  reconnect token, so the sessions on the server go on. An engine that
  is signing in has nothing to reset, and one that failed to start
  starts again.
- A reset that fails or doesn't finish (10 s a step) restarts the
  engine. The old proxy's connections end with it, and the hosts make
  new sessions for the new proxy (Transport) once it runs, as at launch.
  A reset that didn't finish holds WireGuard's lock, and closing the old
  device would wait for it, so the old device closes in the background,
  as far as it gets. Its control client stops before its WireGuard device
  does, so the new device, on the same state, is the only one that
  control sees. Should the close stop earlier, both talk to control
  under one key until the app quits; control serves the newer.
- The reset closes WireGuard's bind, has `magicsock` open new sockets
  and opens the bind on them, which starts the receive loops that ended
  on the reclaimed sockets; a `magicsock` rebind alone leaves them ended,
  and every peer on DERP. `tsnet` exports none of this: the engine reaches
  its subsystems through `Server.Sys`, not a stable API, and WireGuard's
  device and `magicsock`'s sockets through unexported fields, as
  rootshell does. Its tests fail when a `tailscale.com` update moves them.

### Server

No change in behavior. The gateway already serves HTTPS on the
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
by the hash of `go.mod`, `go.sum`, `ios/tailnet`, `internal/tailnode`
and the script. Its `paths` add `go.mod`, `go.sum` and
`internal/tailnode/**`, so a `tailscale.com` bump or a fix there reaches
TestFlight like an `ios/` change. `gomobile` is pinned as a Go tool in
`go.mod`. The framework is not checked in.

Size, measured with `tsnet` at the version `go.mod` pins, linked into an
arm64 iOS executable with `-s -w` and stripped:

| Build | Installed | Compressed (gzip -9, near the download) |
| --- | --- | --- |
| all features | 18.5 MB | 7.1 MB |
| `ts_omit_*` for every feature `tsnet` can do without | 15.8 MB | 6.0 MB |
| the engine as built (`ios/scripts/build-tailnet`), in Rex Go's Release binary | 17.8 MB | 6.8 MB |

The last row is measured: Rex Go's Release device binary, stripped, with
and without the engine linked; it includes `gomobile`'s runtime and the
three features below.

The `ts_omit_*` tags kept off are `netstack`, `serve`, `acme`, `dns`,
`logtail`, `tailscaledhttps`, `cachenetmap` and `ssh`; `tsnet` doesn't
build without them. The engine also keeps three it could do without:

- `ipnbus`: the status the app watches (`Node.Watch`) comes over it.
- `portmapper`: UPnP, NAT-PMP and PCP open a port on the router, so a
  phone reaches more machines directly instead of through DERP.
- `tailnetlock`: a tailnet with Tailnet Lock needs the device to verify
  its signatures.

The simulator slice never reaches the App Store.

The engine can't be downloaded later instead. iOS runs only code
signed into the app's bundle (a downloaded library fails to load), and
App Review Guideline 2.5.2 forbids downloading executable code;
On-Demand Resources can't hold any.

## Test plan

- `internal/rexapi`'s tests pass after the move.
- Go unit tests for the engine's API with a fake node.
- The engine runs in the simulator (no network extension involved), so
  the end-to-end test runs on the Mac mini: a simulator with no
  Tailscale reaches a GoRex machine's `*-gorex` device, opens a terminal,
  and keeps it through a background and foreground.
- Rex Go's host list saved by the current version decodes after the
  update, and a transport change reconnects the host.
- On a phone: Tailscale app off, cellular only; switch Wi-Fi ↔ cellular
  with a terminal open.

## Phases

1. **Shared package**: `internal/tailnode` out of the gateway, no
   change in behavior.
2. **Engine and transport spike**: `ios/tailnet`, the build script,
   and one host connected through the proxy in the simulator, which
   settles both transport risks.
3. **Settings and hosts**: the Tailscale settings, sign-in, the device
   name, the host's `transport`, the QR flow.
4. **Lifecycle**: path monitor, socket reset, forced reconnect, engine
   restart.
5. **CI**: the framework built in the TestFlight workflow, and its
   `paths`.
