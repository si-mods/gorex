# Rex Go: the web client over Personal Hotspot

Status: proposed.

## Problem

A laptop away from home reaches GoRex hosts only if it has a way in of
its own: the Tailscale app, built-in Tailscale in the web page
(web-tailscale.md), or the host's local network. On a borrowed or work
laptop the first is often not allowed, and the second needs a Tailscale
sign-in on a machine that isn't yours.

The phone already has a way in to every host it shows: built-in
Tailscale, tailcat or the local network (ios-tailnet.md,
ios-tailcat.md). And a laptop beside the phone often joins its Personal
Hotspot anyway.

## Goal

The user turns on **Share on Hotspot** in Rex Go. The laptop joins the
phone's Personal Hotspot and opens `http://172.20.10.1:7300` in any
browser. The user taps **Allow** on the phone, and the laptop gets the
web client (docs/web-client.md), unchanged, with the phone's hosts:
sessions, windows, panes, typing, files. Nothing to install or sign in
to on the laptop, and no Cloudflare.

## Non-goals

- **Terminals on the phone.** iOS starts no processes and opens no PTYs.
  Every terminal stays on a GoRex host; the phone only serves the page
  and forwards.
- **Changes to the web client or the gateway.** The page is the
  hosted page's build; the gateway sees the phone as it sees it today.
- **Sharing on Wi-Fi or other networks.** Only Personal Hotspot
  clients, USB or Bluetooth tethering included, are served.
- **A long-lived server in the background as a supported feature.** iOS
  suspends an app in the background and reclaims its listeners (Apple
  TN2277), and Apple's DTS says a background HTTP server is "not
  supported" (forums thread 798468). Phase 2 is an experiment, off by
  default (Background).
- **Turning the hotspot on.** No API does that; the app asks the user.

## Design

### What the phone serves

A Go package of its own, `ios/share`, in the Tailnet framework beside
`ios/tailnet`, so that it dials hosts exactly as the app's SOCKS proxy
does (`dial` in `ios/tailnet/proxy.go`): tailcat names over tailcat,
tailnet names over the tailnet node, local network addresses directly.

It listens on several ports of the hotspot's address:

| Port | Serves |
| --- | --- |
| 7300 | the web client's files (`web/dist`, in the app bundle), the admission page, `/share/state` |
| 7301 and up | one host each: everything is forwarded to that host's gateway |

A host's port is kept with the host (`HostConfig.sharePort`), assigned
once from the first free port, so the page's saved host list
(`gorex.hosts` in the laptop's `localStorage`, per origin) stays valid
across shares.

One port per host, rather than one port with a prefix per host, is what
lets the page stay as it is: the page takes a host as an origin only
(`hostURL` in `web/src/hosts.ts` refuses a path) and asks it for absolute
paths (`/api/control`, `/api/data`, `/api/help`, downloads).

### Forwarding

Each host port is a `httputil.ReverseProxy` to the host's URL, WebSocket
upgrades included, with a transport whose `DialContext` is the app's
`dial`, and TLS for `https` hosts (the tailnet's `*.ts.net`). It:

- **drops `Origin`**, so the gateway serves the request as a native
  client's: the phone's. The gateway's own check (`checkOrigin` in
  `internal/rexapi/web.go`) stays as it is, and no host needs
  `gorex rexapi web allow`. The phone does that check itself: it serves
  only the origin `http://<hotspot address>:7300`, and answers its CORS,
  preflights included, as the gateway does for a trusted page.
- serves only admitted devices (Admission).

The gateway's access rule then judges the phone: its tailnet user, its
tailcat key, or its local network address. A laptop can do on a host
exactly what the phone can, no more.

The host list comes from Swift as JSON (id, name, URL, port), as
`SetTailcatHosts` does today, and changes with it.

### Admission

Anyone on the hotspot could otherwise open a shell on every host, so
each device is admitted on the phone:

1. A browser from an address the phone has not admitted gets, on 7300, a
   page of its own: "Allow this computer on your iPhone", and polls
   `/share/state`. On the host ports it gets 403.
2. The phone shows an alert: **Allow 172.20.10.3?** with the browser's
   user agent, in plain words ("Chrome on macOS"). Allow or Deny.
3. Allowed, the address is admitted on every port until sharing stops.
   The waiting page lists the hosts, one link each,
   `/?host=http://172.20.10.1:7301`, which the page's quick link adds
   (`addLink` in `web/src/hosts.ts`).

Admission is by the client's address, not a cookie: the page fetches
downloads with `credentials: "omit"` (`web/src/files.ts`), and a
cross-port `fetch` sends no cookie by default, so a cookie would need
changes to the page. An address is good enough here: only a device that
knows the hotspot password is on that network, and the phone admits it
by hand. Turning sharing off forgets every address.

The share screen lists the admitted devices; each can be removed, which
closes its connections.

### Addresses

The server listens on the hotspot's address only: the interface that
holds `172.20.10.1` (iOS uses `172.20.10.0/28` for hotspot clients),
read when sharing starts and again on network changes. With no such
interface, the share screen says "Turn on Personal Hotspot" and waits
for it. The page's address is shown on the share screen, large enough
to type.

Whether iOS asks for local network permission on an incoming connection
is checked in phase 1; the app already declares
`NSLocalNetworkUsageDescription`.

### The page in a non-secure context

`http://172.20.10.1` is not a secure context. The page already works on
a LAN dev server:

- no service worker and no Cache Storage, so no offline copy and no
  install; the browser's HTTP cache keeps the files (`loader.ts`);
- the loader skips its hash check without `crypto.subtle` (`loader.ts`);
- copy falls back to `execCommand` (`pointer.ts`); the paste button,
  which needs `navigator.clipboard.readText`, hides, and ⌘V still
  pastes;
- built-in Tailscale in the page is not used.

### The app bundle

`ios/scripts` builds `web/dist` (`npm run build` in `web/`) and copies
it into the bundle as a folder reference, `WebClient`. The page then
matches the app's version. Its size is measured in phase 1 (most of it
is the libghostty-vt wasm and the fonts).

### Foreground (phase 1)

Sharing runs while Rex Go is in the foreground:

- the share screen turns the idle timer off
  (`UIApplication.isIdleTimerDisabled`) and dims the screen to its
  lowest after 30 s without a touch; a touch brings the brightness back,
  and so does stopping;
- going to the background starts `beginBackgroundTask`, which gives
  about 30 s: enough to answer a message and come back without the
  laptop losing anything;
- after a suspension the listeners may be gone. In the foreground the
  server checks them, as `Proxy()` checks the SOCKS proxy, and listens
  again on the same ports. The page reconnects by itself every few
  seconds (web-client.md, Hosts), and the sessions keep running on the
  hosts.

The share screen says this plainly: "Keep Rex Go open while sharing."

### Background (phase 2, experimental)

A setting under the share screen, off by default: **Keep Sharing in the
Background**. On, the app plays a silent loop with an `AVAudioSession`
of category `.playback` and `.mixWithOthers` while sharing, and declares
`audio` in `UIBackgroundModes`. iOS then keeps the app running when the
screen locks, and the listeners stay up.

This is what other local-server apps on iOS do (WorldWideWeb,
PocketServer). It is not what the audio mode is for (App Review
Guideline 2.5.4), so:

- it may fail TestFlight's beta review for external testers, or App
  Review; the setting can then be built only into internal builds;
- the setting says it uses more battery;
- dictation (`Dictation.swift`) changes the audio session's category to
  record; the share loop must survive that and take the session back
  when dictation stops. An interruption (a call) stops the loop; the app
  starts it again when the interruption ends.

### Live Activity (phase 3)

With phase 2 on, the app runs while locked, so a Live Activity has
something to show: "Sharing · 1 computer · 172.20.10.1:7300" on the Lock
Screen and in the Dynamic Island, and a **Stop** button (a
`LiveActivityIntent`, iOS 17+; the app targets iOS 26). It needs a
widget extension target in `ios/project.yml`.

The app updates it locally. A Live Activity alone gives the app no
background time (ActivityKit runs no app code), so without phase 2 it
is not built. Developers report that local updates can stop while
locked even in audio mode (forums thread 822999); phase 3 checks this on
a phone before it ships.

`BGContinuedProcessingTask` (iOS 26) also shows a Live Activity and
runs in the background, but it is for work that ends and reports
progress, and reports say it fails after minutes locked. It is not used.

## Alternatives

- **The hosted page with built-in Tailscale in the page.** Works on any
  network, but signs the laptop in to Tailscale and needs Cloudflare.
  This design keeps the laptop out of the tailnet.
- **One port with a path prefix per host.** Needs the page to take a
  base path for every request. Possible later if ports become a
  problem.
- **A cookie or token for admission.** Needs changes to the page's
  requests (above).
- **A Live Activity to keep the app alive.** It doesn't (above).

## Test plan

- `ios/share`: the static files and their types; forwarding, WebSocket
  upgrades included, to a test gateway (`internal/testrex`), with
  `Origin` dropped; CORS for the page's origin only, others refused;
  admission: 403 before, served after, forgotten on stop; listening
  again after the listener closes.
- In the simulator on the Mac mini: the share screen, the admission
  alert, the dimming.
- On a phone: a MacBook on the hotspot opens the page, is admitted, opens
  a terminal on a host over tailcat and one over the tailnet, types,
  downloads a file. Background and foreground within 30 s and after. A
  second laptop is denied. Phase 2: locked for an hour, then a call.

## Phases

1. **Foreground**: `ios/share`, the page in the bundle, the share screen,
   admission, dimming, listening again after a suspension. Measures the
   bundle size and the local network permission.
2. **Background**: the experimental setting, audio session handling with
   dictation and interruptions.
3. **Live Activity**: the widget extension, status and Stop.
4. **Docs**: `docs/rex-api.md` and the README.
