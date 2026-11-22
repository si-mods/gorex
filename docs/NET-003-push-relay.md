---
name: NET-003
description: "A relay that holds the APNs key of Rex Go, so that any host can update the Live Activity of a paired phone. Use when you change push registration, the relay API or the sender."
metadata:
  created: "2026-11-22"
  updated: "2026-11-22"
---

# NET-003: Push relay: Live Activity pushes for every host

Status: built. The host's sender (`internal/push`) is in #995, the relay is the private `si-mods/gorex-relay` repository, and this is the phone's half. The team decided these points on 2026-11-22.

This doc designs the relay that [IOS-006](IOS-006-live-activities.md) left open ("Later: a relay"). It replaces the direct last hop of that doc: no host ever holds an APNs key. Phase 2 of IOS-006 uses this relay as its only last hop.

## Problem

An update of a Live Activity through APNs needs a `.p8` key of the team of the app. Only the developer can hold it. A host that a user runs cannot. Without a push, the Live Activity freezes about 25 seconds after the app leaves the foreground. It shows as paused until the app comes back.

## Goal

- The Live Activity of a terminal updates while Rex Go is suspended, for every host the phone paired with.
- The host never sees the push token of the activity.
- A host can push only to a device that bound it.
- The phone reaches the relay over the public network. The path needs no tailnet, so a token that changes while the app sleeps can be bound in the time iOS gives.
- The relay stores bindings and nothing else. It logs no content.
- A phone with an old server, and a server with an old phone, keep the behavior of phase 1.

## Non-goals

- **End-to-end encryption of the content.** The widget extension renders the push directly. No extension can decrypt it. The relay and Apple see the content state in plain text.
- **A direct last hop.** No host holds an APNs key. The earlier direct design of [IOS-006](IOS-006-live-activities.md) is dropped.
- **Alert pushes in place of the local blocked notification.** The alert field is in the API. The swap of the local notification is a later change of IOS-006.
- **Push-to-start.** The app starts the activity in the foreground, as today.

## Design

### Parties and keys

- **The device key** is a P-256 key pair in the Keychain of Rex Go, made at first use. Its public part is the identity of the phone.
- **The push key of the host** is a P-256 key pair in the data dir of the host, mode 0600, made at first use. The host gives its public part to the phone in the result of `gorex.push.register`. The phone trusts it because the control connection is authenticated already.
- **The relay key** is the `.p8` of the app, on the relay only. It is topic-scoped to `dev.gorex.ios`, one key for each environment. It never enters a repository.

The signatures are ECDSA P-256 over SHA-256. The signature goes in the header `X-Gorex-Sig`, base64, over the raw request body. The verifier needs no canonical form, because it checks the bytes as they came.

### Register and bind

1. On each control connection, the phone calls `gorex.push.register` with its device key, its target (session and block of the terminal on screen, or none) and whether Live Activities are on. The result carries the push key of the host and the URL of the relay.
2. The phone calls it again when the target changes, and `gorex.push.unregister` when the terminal closes or pairing ends.
3. The app requests the activity with `pushType: .token`. On `pushTokenUpdates`, the phone binds the token at the relay: device key, host key, token, environment. This works in the short wake iOS gives for a new token, because the relay is on the public network.
4. The app gives the host no token. The host addresses the device key.

### Push

1. The host computes the content state of the target of the device, on each `program_status_changed` of the block. It coalesces the updates to one for each two seconds.
2. It posts the update to the relay, addressed to the device key, signed with its push key.
3. The relay checks the signature against the binding, then sends the push to APNs for each token bound to the device.
4. Each push carries a `stale-date` a few minutes ahead. A host or a relay that stops lets the activity show as paused.
5. When the control connection of a device closes, the host sends a last update: `connected: false`, `stale-date` of now. The activity shows the paused face, and the host goes on pushing new states to the token. The activity lives without the app, so it keeps updating while the app is suspended.

The host never learns whether the phone still listens. A dead token is removed when APNs rejects it.

### Content state

The host builds the `TerminalActivity.ContentState` of the target:

- `record`, `program`, `connected`: as the app computes them today.
- `title`: the title of the window, from the session view of the server. A rename that synced to the server shows.
- `line`: the `msg` (or `last_msg` for done and error) of the most urgent record. The host has no screen, so the screen-line fallback of phase 1 does not apply. A program that reports no message gets no line.

### Relay API

`https://relay.gorex.uk`, JSON over HTTPS:

- `POST /v1/bind`: `{token, device, host, env}`; `X-Gorex-Sig` by the device key. `env` is `sandbox` or `production`. Replaces the binding of the token.
- `POST /v1/unbind`: `{token}`; `X-Gorex-Sig` by the device key.
- `POST /v1/push`: `{device, event, content_state, stale_date, dismissal_date, alert, priority}`; `X-Gorex-Sig` by the host key. `event` is `update` or `end`. `alert` is optional. The relay forwards to every token bound to the device.
- `GET /v1/health`: liveness only.
- Answers are JSON: `{"sent": 1}` or `{"error": "..."}`. Bodies are limited to 4 KB. Each token accepts a limited rate.

The relay sends to `api.push.apple.com` or `api.sandbox.push.apple.com` by the bound `env`: `apns-push-type: liveactivity`, `apns-topic: dev.gorex.ios.push-type.liveactivity`, the priority of the request, `authorization: bearer` of an ES256 JWT it renews every 50 minutes. `BadDeviceToken`, `ExpiredToken` and `Unregistered` drop the binding.

### Rex API

`gorex.push.register` and `gorex.push.unregister` are additive, feature-gated (`gorex.push`, version 1). An old server answers `not_supported`; the phone then skips the relay path and keeps phase 1. An old phone calls nothing; the host does nothing.

### Security

- The token goes from the phone to the relay only. Its value is not guessable. A host that learns a token of another party cannot use it: the binding pins the host key.
- A stranger can bind a token to a device key and a host key of his own. To push he still needs the signature of the bound host. To learn a token he must run a host the phone pairs with. That is the trust of pairing itself.
- The relay is an open endpoint, by design. It accepts bindings at a limited rate and drops tokens APNs rejects.
- The content state crosses the relay in plain text. The relay keeps bindings (token, device key, host key, env, time) and no content.

### Failure modes

- The relay is down: no updates; `staleDate` turns the activity paused, as in phase 1.
- The phone cannot bind: same.
- The host cannot push: same; it retries briefly, then waits for the next register.
- The relay loses its table: bindings heal at the next token rotation or the next foreground of the app.

### Build and release

- The relay is a small Go service in its own repository. It deploys as a container behind TLS at `relay.gorex.uk`.
- The app adds `aps-environment` and `NSSupportsLiveActivitiesFrequentUpdates`. The Push Notifications capability goes on the App ID `dev.gorex.ios` in the portal. The key creation and the capability are manual steps of the developer.
- The `.p8`, its key id and the team id `57QL6YCAH5` are configuration of the relay deployment, one key for each environment.

## Tests

- Unit tests: signature checks, binding rules, payload build, error mapping, rate limit, token drop on APNs errors.
- Relay against a fake APNs server: HTTP/2, error answers, unregistered tokens.
- On a phone: pair, leave the app, watch the island update; kill the app, watch it still update; stop the host, watch it pause; token rotation with the app suspended.

## Open questions

1. Which Apple topic list shows for a topic-specific key, and whether `dev.gorex.ios.push-type.liveactivity` needs its own entry or comes with the app.
2. Whether an unsigned archive keeps `aps-environment` at export (as [IOS-006](IOS-006-live-activities.md) asks).
3. The alert push and the swap of the local blocked notification.

## References

- **bark-server** (`finb/bark-server`, Go): a public APNs relay. It keeps a table of device keys to device tokens and sends with `sideshow/apns2`, an HTTP/2 client with `.p8` token auth. Its register API is open, as ours is. It differs: no signatures, no Live Activities, one app.
- **alma-relay** (`yetone/alma-relay`, Node and Cloudflare Workers): an agent tunnel that also forwards pushes. Its agent socket carries a `push` frame of notifications, `pushType: 'liveactivity'` among them, to `<topic>.push-type.liveactivity`, with an answer of one result per token. It differs: its agent holds a long socket and addresses by a token hash; our host posts per update and addresses by the device key.
- **rootshell**: see [IOS-006](IOS-006-live-activities.md), Reference. Its alert pushes are end-to-end encrypted through a notification service extension. A Live Activity cannot use that, because no extension can decrypt its pushes.
