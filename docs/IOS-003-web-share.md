---
name: IOS-003
description: "Rex Go serves the web client to a laptop over Personal Hotspot and forwards its requests to the hosts. Use when you change hotspot sharing."
metadata:
  created: "2026-11-13"
  updated: "2026-11-20"
---

# IOS-003: Rex Go: the web client over Personal Hotspot

Status: phase 1 built (#556, #866). Phases 2 and 3 not built.

This design lets Rex Go share the web client with a laptop over Personal Hotspot. The phone serves the page and forwards the requests to the hosts of the phone.

## Problem

A laptop away from home reaches GoRex hosts only if it has a way in of its own. The ways are the Tailscale app, built-in Tailscale in the web page (WEB-002-tailscale.md), or the local network of the host. On a borrowed or work laptop, the first way is often not allowed. The second way needs a Tailscale sign-in on a machine that is not the machine of the user.

The phone already has a way in to every host that it shows. The ways are built-in Tailscale, tailcat or the local network (IOS-001-tailnet.md, IOS-002-tailcat.md). Also, a laptop beside the phone often joins its Personal Hotspot anyway.

## Goal

The user turns on **Share on Hotspot** in Rex Go. The laptop joins the Personal Hotspot of the phone and opens `http://172.20.10.1:7300` in any browser. The user taps **Allow** on the phone. The laptop then gets the web client (docs/REF-007-web-client.md) with the hosts of the phone in its host list: sessions, windows, panes, typing, files. The user installs nothing and signs in to nothing on the laptop. There is no Cloudflare in the path.

## Non-goals

- **Terminals on the phone.** iOS starts no processes and opens no PTYs. Every terminal stays on a GoRex host. The phone only serves the page and forwards.
- **Changes to the gateway.** The gateway sees the phone as it sees it today. The web client changes only where it must (see The web client).
- **Sharing on Wi-Fi or other networks.** The phone serves only Personal Hotspot clients, USB or Bluetooth tethering included.
- **A long-lived server in the background as a supported feature.** iOS suspends an app in the background and reclaims its listeners (Apple TN2277). Apple's DTS says a background HTTP server is "not supported" (forums thread 798468). Phase 2 is an experiment, off by default (see Background).
- **Turning the hotspot on.** No API does that. The app asks the user.

## Design

### What the phone serves

A Go package of its own, `ios/share`, is in the Tailnet framework beside `ios/tailnet`. It dials hosts exactly as the SOCKS proxy of the app does (`dial` in `ios/tailnet/proxy.go`). It dials tailcat names over tailcat, tailnet names over the tailnet node, and local network addresses directly.

It listens on one port, 7300, of the address of the hotspot, and serves these paths:

| Path | Serves |
| --- | --- |
| `/` and the page's files | the web client (`WebClient/` cached on the phone) |
| `/share/hosts` | the phone's hosts, as JSON: id, name, icon |
| `/share/state` | the poll of the admission page (see Admission) |
| `/h/<id>/…` | forwarded to the gateway of host `<id>`, without the prefix |

The id is the id of the host in the app (`HostConfig.id`), so a host keeps its path across shares. The host list comes from Swift as JSON, as `SetTailcatHosts` does today, and changes with it. Each host's transport in it is the way the app reaches the host (`reach` in `ios/GoRex/Rex/Host.swift`: its tailcat client, the built-in Tailscale while it is on, else the system's way) — not the stored `transport`, which is nil on a tailcat host and stays "tailnet" while the engine is off. The demo host is left out: it answers nothing away from the app.

### Forwarding

`/h/<id>/…` is a `httputil.ReverseProxy` to the URL of the host. It includes WebSocket upgrades. Its transport has the `dial` of the app as `DialContext`, and TLS for `https` hosts (the `*.ts.net` names of the tailnet). The proxy does these things:

- **It drops `Origin`**, so the gateway serves the request as a request of a native client: the phone. The own check of the gateway (`checkOrigin` in `internal/rexapi/web.go`) stays as it is, and no host needs `gorex rexapi web allow`. The phone does that check itself. It forwards a request with an `Origin` only when that `Origin` is its own, `http://<hotspot address>:7300`. Page and API share one origin, so there is no CORS.
- It serves only admitted devices (see Admission).

The access rule of the gateway then judges the phone. The rule uses the tailnet user of the phone, its tailcat key, or its local network address. A laptop can do on a host exactly what the phone can do, no more.

### The web client

The page already connects several hosts at once (`docs/WEB-003-multihost.md`). So every host that the phone shares can connect together and sit in the sidebar side by side. The page needs two small changes:

- **The address of a host may carry a path.** The host of a phone is `http://172.20.10.1:7300/h/<id>`. `directTransport` (`socket`, `fetch`, and `url` in `web/src/rex.ts`) and `diagnose` (`web/src/hosts.ts`) now join the path of the API to the address of the host (`base + "/api/control"`). Before, they resolved it (`new URL("/api/control", base)`), which drops the path. Downloads by hidden frame (`transport.url` in `web/src/files.ts`) and every other call then work as they are. A typed address still may not have a path (`hostURL`).
- **An iPhone group in the host list and sidebar.** When `/share/hosts` on the own origin of the page answers, the page adds those hosts. It does not save them to `localStorage`, and the user cannot edit them in the sidebar or host list. The page connects them alongside any saved hosts (`HostConn`, staggered four at a time). It groups them under the name of the phone in the host list. If no window is saved in `gorex.place`, the page shows the first window of the first host. A page that a phone does not serve gets 404 from `/share/hosts` and shows no group.

### Admission

Anyone on the hotspot could otherwise open a shell on every host. So the phone admits each device:

1. A browser from an address that the phone has not admitted gets a page of its own at `/`: "Allow this computer on your iPhone". The page polls `/share/state`. On `/share/hosts` and `/h/…` the browser gets 403.
2. The phone shows an alert: **Allow 172.20.10.3?** with the user agent of the browser, in plain words ("Chrome on macOS"). The user chooses Allow or Deny.
3. When the user allows, the address is admitted until sharing stops. The waiting page loads the web client.

Admission is by the address of the client, not by a cookie. The page requests some downloads with `credentials: "omit"` (`web/src/files.ts`), which sends no cookie even to its own origin. An address is good enough here. Only a device that knows the hotspot password is on that network, and the phone admits it by hand. Turning sharing off forgets every address.

The share screen lists the admitted devices. The user can remove each device, which closes its connections, the proxied WebSockets included. Deny and stopping the share close them too.

### Addresses

The server listens on the address of the hotspot only. This is the interface that holds `172.20.10.1` (iOS uses `172.20.10.0/28` for hotspot clients). The server reads it when sharing starts, and again on network changes. With no such interface, the share screen says "Turn on Personal Hotspot" and waits for it. The share screen shows the address of the page, large enough to type.

Whether iOS asks for local network permission on an incoming connection is checked in phase 1. The app already declares `NSLocalNetworkUsageDescription`.

### The page in a non-secure context

`http://172.20.10.1` is not a secure context. The page already works on a LAN dev server:

- There is no service worker and no Cache Storage, so there is no offline copy and no install. The HTTP cache of the browser keeps the files (`loader.ts`).
- The loader skips its hash check without `crypto.subtle` (`loader.ts`).
- Copy falls back to `execCommand` (`pointer.ts`). The paste button needs `navigator.clipboard.readText`, so it hides. ⌘V still pastes.
- The page does not use built-in Tailscale.

### The web client files

The iOS app bundle does **not** embed the web client (`web/dist`). Instead, `web/build.mjs` writes a `files.json` index alongside `web/dist`. The index lists the path, size and SHA-256 of each static file. It excludes the built-in Tailscale engine, which the phone does not use. The built files are hosted on Cloudflare R2.

When the user turns on **Share on Hotspot** (or taps **Update Web Client**), Rex Go does these steps:

1. It reads `files.json` from the configured Cloudflare R2 URL.
2. It downloads any missing or changed files into `Application Support/WebClient/`.
3. It verifies their SHA-256.
4. It swaps them in atomically.

Once the files are cached, sharing works offline too. This keeps the iOS `.ipa` small and avoids building `web/dist` inside Xcode.

### Foreground (phase 1)

Sharing runs while Rex Go is in the foreground:

- The share screen turns the idle timer off (`UIApplication.isIdleTimerDisabled`). It dims the screen to its lowest level after 30 s without a touch. A touch brings the brightness back, and so does stopping.
- When the app goes to the background, it starts `beginBackgroundTask`. This gives about 30 s. That is enough to answer a message and come back without the laptop losing anything.
- After a suspension the listeners may be gone. In the foreground the server checks them, as `Proxy()` checks the SOCKS proxy, and listens again on the same port. The page reconnects by itself every few seconds (REF-007-web-client.md, Hosts), and the sessions keep running on the hosts.

The share screen says this plainly: "Keep Rex Go open while sharing."

### Background (phase 2, experimental)

A setting under the share screen, off by default: **Keep Sharing in the Background**. When it is on, the app plays a silent loop with an `AVAudioSession` of category `.playback` and `.mixWithOthers` while sharing. The app also declares `audio` in `UIBackgroundModes`. iOS then keeps the app running when the screen locks, and the listeners stay up.

Other local-server apps on iOS do this (WorldWideWeb, PocketServer). It is not what the audio mode is for (App Review Guideline 2.5.4). So these points apply:

- It may fail the beta review of TestFlight for external testers, or App Review. The setting can then be built only into internal builds.
- The setting says that it uses more battery.
- Dictation (`Dictation.swift`) changes the category of the audio session to record. The share loop must survive that and take the session back when dictation stops. An interruption (a call) stops the loop. The app starts it again when the interruption ends.

### Live Activity (phase 3)

With phase 2 on, the app runs while locked, so a Live Activity has something to show. The Live Activity shows "Sharing · 1 computer · 172.20.10.1:7300" on the Lock Screen and in the Dynamic Island, and a **Stop** button. The button is a `LiveActivityIntent` (iOS 17+, and the app targets iOS 26). The Live Activity needs a widget extension target in `ios/project.yml`.

The app updates it locally. A Live Activity alone gives the app no background time (ActivityKit runs no app code). So without phase 2 the Live Activity is not built. Developers report that local updates can stop while locked even in audio mode (forums thread 822999). Phase 3 checks this on a phone before it ships.

`BGContinuedProcessingTask` (iOS 26) also shows a Live Activity and runs in the background. But it is for work that ends and reports progress, and reports say it fails after minutes locked. This design does not use it.

## Alternatives

- **The hosted page with built-in Tailscale in the page.** It works on any network, but it signs the laptop in to Tailscale and needs Cloudflare. This design keeps the laptop out of the tailnet.
- **One port per host.** It keeps the page as it is. But it needs a port, a listener and an origin for each host. Also, the saved list of the page goes stale when a port changes. One port with a path for each host keeps one origin, so there is no CORS either.
- **A cookie or token for admission.** Downloads omit credentials (see Admission).
- **A Live Activity to keep the app alive.** It does not keep the app alive (see Live Activity).

## Tests

- `ios/share`:
  - The static files and their types.
  - Forwarding under `/h/<id>`, WebSocket upgrades included, to a test upstream (`httptest`, in `ios/share/share_test.go`), with `Origin` dropped.
  - Another origin is refused.
  - Admission: 403 before, served after, forgotten on stop.
  - A live proxied WebSocket is closed, at both ends, on revoke, deny and stop.
  - Listening again after the listener closes.
- `web/`: a host address with a path, for sockets, requests, downloads and the diagnosis. The iPhone group shows only when `/share/hosts` answers.
- In the simulator on a Mac: the share screen, the admission alert, the dimming.
- On a phone:
  - A laptop on the hotspot opens the page and is admitted.
  - It opens a terminal on a host over tailcat and one over the tailnet, types, and downloads a file.
  - Background and foreground within 30 s, and after 30 s.
  - A second laptop is denied.
  - Phase 2: locked for an hour, then a call.

## Phases

1. **Foreground**: `ios/share`, the two changes of the page, `files.json` in `web/build.mjs`, on-demand download and cache of `WebClient/`, the share screen, admission, dimming, and listening again after a suspension. Phase 1 also checks the local network permission. Built (#556, #866).
2. **Background**: the experimental setting, and audio session handling with dictation and interruptions.
3. **Live Activity**: the widget extension, status and Stop.
4. **Docs**: `docs/REF-005-rex-api.md` and the README.
