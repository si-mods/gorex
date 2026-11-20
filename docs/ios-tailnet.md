# Rex Go: built-in Tailscale

Status: built (#369, #371, #375, #381, #382).

This doc answers part of [#330](https://github.com/si-mods/gorex/issues/330). It lets Rex Go join a tailnet inside the app, with no Tailscale app and no system VPN. The work shipped in phases.

## Problem

Away from the local network, Rex's own iPhone app reaches a server only through the Tailscale app. Without that app installed, signed in and with its VPN on, the app reaches nothing. This dependency is the pain that this design removes. Rex Go inherited the dependency, because Rex Go speaks Rex's API.

Rex Go reaches a GoRex machine in one of two ways today:

- **The tailnet.** The QR code holds `https://<machine>-gorex.<tailnet>.ts.net`. The phone gets there through the system VPN of the Tailscale app. The VPN stays on for every app on the phone. Many users do not want this. The VPN fights other VPNs, and it changes how the rest of the phone reaches the network.
- **The local network.** The phone uses `http://<address>:7297` over plain HTTP, open to the networks that the user allows. It needs no VPN. It is not encrypted and has no credential. It works only on that network.

The desktop side already solved this problem. The gateway of the GoRex server joins the tailnet as a device of its own (`internal/rexapi/tailnet.go`), with no Tailscale installed. The phone has no equivalent.

## Goal

Rex Go joins the tailnet itself, inside the app, as the "rootshell Only" mode of rootshell does. Tailscale runs in the process of the app with a userspace network stack. Only the own connections of the app go through it. The app uses no system VPN and no Tailscale app.

The phone and the gateway share one implementation of built-in Tailscale. They use one `tailscale.com`, one wrapper of `tsnet` and one check of a control URL. The behavior of the gateway does not change. Its code moves into a package that both use (see Shared package, below).

This covers the first need of #330, **no VPN on the phone**. It works on Wi-Fi or cellular, for a GoRex machine that has built-in Tailscale on. The machine needs no public address. Peers connect directly when NAT allows. They connect through the DERP relays of Tailscale when NAT does not allow.

## Non-goals

- **A login of GoRex's own.** The identity of the phone is its tailnet identity. The gateway lets in the Tailscale user who signed in the device of the machine. As an alternative, it lets in the members of the tailnet when `--allow-tailnet-members` is set. An administrator revokes a device in the Tailscale admin console or with `headscale nodes delete`. #330 also asks for pairing and revocation that need no tailnet. That is separate work on the local network listener (HTTPS plus credentials for each device). It gets its own document.
- **A direct connection with no control server.** The data path between two public addresses is direct. The keys still go through a control server. The control server is the server of Tailscale or the Headscale of the user.
- **Changing what the gateway does.** The gateway already has everything that the phone needs (see Server, below).
- **Headscale, for now.** Rex Go takes a control URL, as the server does. The phone can then join a Headscale tailnet. But the gateway serves only HTTPS on the tailnet, and Headscale issues no certificates. A GoRex machine on Headscale has nothing for the phone to reach. The self-hosted case of #330 does not need a tailnet at all. Rex Go reaches GoRex over tailcat ([ios-tailcat.md](ios-tailcat.md)).
- **Running in the background.** iOS suspends the app, and the engine stops with it. Rex Go is a foreground terminal. Its sessions live on the server and attach again.
- **Replacing the system VPN path.** Hosts that Rex Go reaches through the Tailscale app, or on the local network, keep working as they do now.

## Design

### Engine

The engine is a Go package, `ios/tailnet`. It is in the module of GoRex and has no `go.mod` of its own. It uses the `tailscale.com` of the server and may import `internal/tailnode`. It adds only what the phone needs on top. It exports a narrow API for `gomobile bind`:

| Call | Does |
| --- | --- |
| `Start(configJSON, stateDir, callback)` | starts the node: hostname, control URL, optional one-time auth key |
| `Login()` / `Logout()` | interactive login. The auth URL arrives through the callback |
| `Status() string` | JSON: backend state, auth URL, the node's name and addresses, peers and how each is reached |
| `SetHostname(name)` | renames the running node, as the `SetHostname` of the gateway does |
| `Proxy() string` | the SOCKS5 proxy (Transport): address and password, one for each process |
| `DefaultInterfaceChanged(name)` | the news of the path monitor about a network change |
| `ResetSockets()` | binds again the UDP sockets that iOS reclaimed while the app was suspended |
| `Stop()` | stops the node |
| `ValidControlURL(url) bool` | the check of `internal/tailnode`, for Settings |
| `ValidHostname(name) bool` | the device name check of `internal/tailnode`, for Settings |

The reference for the iOS-specific parts is `iosbridge/tailnet*.go` of rootshell (kitknox/trzsz-ssh-rootshell, MIT license). The iOS-specific parts are the interface monitor that `tsnet` lacks on iOS, the socket reset after a suspension and its watchdog.

Two ready-made engines were weighed and rejected:

- The `TrzszSSH.xcframework` of rootshell carries all of trzsz-ssh (a 60 MB download). It also pins a Tailscale version that GoRex does not choose.
- TailscaleKit of Tailscale (`tailscale/libtailscale`, Swift over C over Go) has no hook for either iOS-specific part. Its C API is new/start/up/close, listen, dial, loopback and status. To add the hooks, GoRex must fork three layers. TailscaleKit has no releases. GoRex must build it from source with Go all the same, and it pins its own `tailscale.com`. Its `URLSession` extension is the model for the extension of GoRex (Transport).

### Shared package

`internal/tailnode` takes from `internal/rexapi/tailnet.go` the parts that are not specific to the gateway:

- the node: `tsnet.Server` with its local client (start, status, watch, login, logout, set hostname, close), behind the interface that the tests of the gateway fake.
- `ValidControlURL` and the device name check.

The gateway keeps its listener, `identify`, its settings and its methods. It imports the rest. The move changes no behavior. The tests of the moved code move with it.

The state of the node lives in the Application Support directory of the app. The directory is excluded from backups. It has `NSFileProtectionCompleteUntilFirstUnlock`. The name of the node defaults to `rexgo-` and four random letters, such as `rexgo-kqzt`. The user can change the name in Settings (the engine applies it as the `SetHostname` of the gateway does). The engine does not use the own name of the phone. Since iOS 16, `UIDevice.name` returns only the model ("iPhone") without an entitlement that Apple grants case by case. Two phones would show the same name, and a user could revoke one phone for the other.

### Transport

Rex Go talks to a host only through `URLSession` (control and data WebSockets, `/api/spaces`, uploads). A host on the built-in tailnet gets a `URLSession` of its own. The configuration of this `URLSession` points at the proxy of the engine:

```swift
let c = URLSessionConfiguration.default
var proxy = ProxyConfiguration(socksv5Proxy: .hostPort(host: "127.0.0.1", port: port))
proxy.applyCredential(username: "tsnet", password: password)
c.proxyConfigurations = [proxy]
```

This is the `URLSessionConfiguration.proxyVia` of TailscaleKit. The proxy is the own proxy of the engine (`ios/tailnet/proxy.go`). It is `tailscale.com/net/socks5` on loopback. It has the user `tsnet` and a random password for each process, so another app on the phone cannot use it. It dials only the devices of the tailnet. It takes the MagicDNS name of the target (full or short) or the address of the target. It looks the target up among the current peers of the node. It dials the Tailscale address of that peer through the node.

The proxy keeps the status that it looks in between dials. A watch of the IPN bus (`Node.WatchPeers`) runs during this time. It forgets the status at each change of state, at each change of self, and when a peer is added, replaced or removed. When the status lacks a name, the proxy looks the name up in a new status. The proxy refuses everything else: an unknown name, an address off the tailnet, and any address before the network map arrives. TLS is end to end. `URLSession` still checks the `*.ts.net` certificate of the gateway.

The engine does not use the own proxy of `tsnet`, `Server.Loopback()`. That proxy dials through the network of the phone any address that is not a known peer. In the second or so after a start, before the network map arrives, every tailnet address is not a known peer. The spike saw exactly that. The first connections went to the gateway over the home network, which happened to route the tailnet, and not through Tailscale. The port of that proxy also serves the LocalAPI. The proxy of the engine exposes no LocalAPI on any port.

The proxy keeps its port and password for the process. Its tailnet connections end when the run of the engine ends. When the engine starts again, every `tailnet` host drops its session, makes a new session and reconnects. iOS can also reclaim the listening socket of a suspended app (TN2277). The next `Proxy()` replaces a proxy whose listener died. The new proxy has a new port and a new password. `resume()` reads them after the socket reset. The next session of the hosts goes through the new proxy.

The protocol code (`ControlConnection`, `DataStream`, `Spaces`, `Upload`) changes only in one way. It takes the session of the host instead of `URLSession.shared`.

Phase 2 tested two assumptions in the simulator. Both assumptions hold:

- `URLSessionWebSocketTask` honors `proxyConfigurations` with SOCKS5.
- `URLSession` hands the proxy the name of the host (SOCKS5 `DOMAINNAME`). It does not resolve the name first through the DNS of the phone. That DNS knows no MagicDNS name without the Tailscale VPN. The assumption holds for plain requests and for WebSockets.

For the second assumption, a request and a WebSocket went to a MagicDNS short name. The DNS of the Mac mini could not resolve this name. Both reached the gateway through the proxy and met its certificate. If either assumption had failed, the fallback was the `DialTCP` of the engine. It hands Swift a socket file descriptor. The WebSocket then runs on `NWConnection`.

### Hosts

`HostConfig` gets `transport: Transport?`. The value `nil` or `.system` is the way of today. The value `.tailnet` is the way of the engine. The field is optional because the synthesized `Decodable` of `HostConfig` ignores default values. A new required key would fail to decode every saved list. The `try?` of `HostStore` would turn that failure into no hosts at all. A test decodes a list that the current version saved.

A host uses `tailnet` when built-in Tailscale is on and one of two conditions is true. Condition one: its name is under the MagicDNS suffix of the tailnet (from the status of the engine). Condition two: its address is in the ranges of the tailnet. This applies to a scanned code and to a host that the user adds by hand. The details of the host show the switch. A user who also runs the Tailscale app can choose either way. `HostStore.update` reconnects a host whose transport changed, as it does for a host whose address changed.

### Settings

Settings › Tailscale follows the VPN settings of rootshell. A status card is on top (the Tailscale tile with a state dot, a title and a subtitle) with the switch:

- **Built-in Tailscale** on or off.
- **Control server**: empty for the server of Tailscale, or a URL such as `https://headscale.example.com`. `ValidControlURL` of `internal/tailnode` checks it. This is the own rule of the gateway: `https`, or `http` only for a loopback, private or link-local address.
- **Device name**: `rexgo-xxxx` by default, as above.
- **Sign in**: opens the auth URL in `ASWebAuthenticationSession`. As an alternative, the user enters an auth key. The app uses the key once and never saves it.
- **Status**: who is signed in, the name and address of this device, and whether Rex Go reaches the GoRex hosts directly or through DERP.
- **Sign out**, which removes the state of the node.

### Lifecycle

- When the app starts, with built-in Tailscale on, the engine starts before the hosts connect. A host on `tailnet` waits for the node to run.
- While the engine runs, and before each start, `NWPathMonitor` reports each change of the network of the phone to the engine. It reports a change of the interface, so the engine binds again when the phone moves between Wi-Fi and cellular. It also reports a change of the gateways, as from one Wi-Fi to another. The engine hands the name of the interface to `netmon`, as the own app of Tailscale does, and has it check the network again.
- The app comes back to the foreground after more than 5 s away, with the engine running. The app calls `ResetSockets` first. Then it reconnects every `tailnet` host with `resume()`, not `reconnectIfNeeded()`. The WebSocket to the loopback proxy can look open while its tailnet side died in the suspension. Unlike `reconnect()`, `resume()` keeps the reconnect token, so the sessions on the server go on. An engine that is signing in has nothing to reset. An engine that failed to start starts again.
- A reset that fails or does not finish (10 s for each step) restarts the engine. The connections of the old proxy end with it. The hosts make new sessions for the new proxy (Transport) when it runs, as at app start. A reset that did not finish holds the lock of WireGuard. Closing the old device would wait for that lock. So the old device closes in the background, as far as it gets. Its control client stops before its WireGuard device does. The new device, on the same state, is then the only device that control sees. If the close stops earlier, both devices talk to control under one key until the app quits. Control serves the newer device.
- The reset closes the bind of WireGuard, has `magicsock` open new sockets and opens the bind on them. This starts the receive loops that ended on the reclaimed sockets. A `magicsock` rebind alone leaves the loops ended, and every peer stays on DERP. `tsnet` exports none of this. The engine reaches its subsystems through `Server.Sys`, which is not a stable API. It reaches the device of WireGuard and the sockets of `magicsock` through unexported fields, as rootshell does. The tests of the engine fail when an update of `tailscale.com` moves these fields.

### Server

The behavior of the server does not change. The gateway already serves HTTPS on the tailnet. It checks each caller with `WhoIs`, and it takes a control URL.

The `identify` function of the gateway decides who gets in. Without `--allow-tailnet-members`, the gateway lets in only an untagged device of the user who signed in the device of the gateway. It does this only when the device of the gateway is untagged itself. So Rex Go signs in as that user, not with a tagged auth key. A tagged setup needs `--allow-tailnet-members` and the access rules of the tailnet. Settings says so when the gateway refuses the phone.

### Build

`gomobile bind -target ios,iossimulator` builds `Tailnet.xcframework` from `ios/tailnet`. A script in `ios/scripts/` runs it. The TestFlight workflow sets up Go and runs the script before `xcodegen`. The workflow caches the result by the hash of `go.mod`, `go.sum`, `ios/tailnet`, `internal/tailnode` and the script. The `paths` of the workflow add `go.mod`, `go.sum` and `internal/tailnode/**`. A `tailscale.com` bump or a fix there then reaches TestFlight like a change in `ios/`. `gomobile` is pinned as a Go tool in `go.mod`. The repository does not hold the framework.

The size was measured with `tsnet` at the version that `go.mod` pins. The build was linked into an arm64 iOS executable with `-s -w` and stripped:

| Build | Installed | Compressed (gzip -9, near the download) |
| --- | --- | --- |
| all features | 18.5 MB | 7.1 MB |
| `ts_omit_*` for every feature `tsnet` can do without | 15.8 MB | 6.0 MB |
| the engine as built (`ios/scripts/build-tailnet`), in Rex Go's Release binary | 17.8 MB | 6.8 MB |

The last row is measured on the Release device binary of Rex Go, stripped, with and without the engine linked. It includes the runtime of `gomobile` and the three features below.

The build keeps these `ts_omit_*` tags off: `netstack`, `serve`, `acme`, `dns`, `logtail`, `tailscaledhttps`, `cachenetmap` and `ssh`. `tsnet` does not build without them. The engine also keeps three features that it could do without:

- `ipnbus`: the status that the app watches (`Node.Watch`) comes over it.
- `portmapper`: UPnP, NAT-PMP and PCP open a port on the router. A phone then reaches more machines directly instead of through DERP.
- `tailnetlock`: a tailnet with Tailnet Lock needs the device to check its signatures.

The simulator slice never reaches the App Store.

The app cannot download the engine later instead. iOS runs only code that is signed into the bundle of the app, and a downloaded library fails to load. App Review Guideline 2.5.2 forbids the download of executable code. On-Demand Resources cannot hold executable code.

## Tests

- The tests of `internal/rexapi` pass after the move.
- Go unit tests check the API of the engine with a fake node.
- The engine runs in the simulator, with no network extension involved. So the end-to-end test runs on a Mac mini. A simulator with no Tailscale reaches the `*-gorex` device of a GoRex machine, opens a terminal, and keeps it through a background and foreground.
- The host list of Rex Go that the current version saved decodes after the update. A transport change reconnects the host.
- On a phone, with the Tailscale app off and cellular only, switch Wi-Fi to cellular and back with a terminal open.

## Phases

1. **Shared package**: `internal/tailnode` out of the gateway, with no change in behavior.
2. **Engine and transport spike**: `ios/tailnet`, the build script, and one host connected through the proxy in the simulator. This phase settles both transport risks.
3. **Settings and hosts**: the Tailscale settings, sign-in, the device name, the `transport` of the host, the QR flow.
4. **Lifecycle**: path monitor, socket reset, forced reconnect, engine restart.
5. **CI**: the framework built in the TestFlight workflow, and its `paths`.
