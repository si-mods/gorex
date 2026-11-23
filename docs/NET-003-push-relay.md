---
name: NET-003
description: "A relay that holds the APNs key of Rex Go, so that any host can update the Live Activity of a paired phone. Use when you change push registration, the relay API or the sender."
metadata:
  created: "2026-11-22"
  updated: "2026-11-22"
---

# NET-003: Push relay: Live Activity pushes for every host

Status: built, push-to-start included. The host's sender (`internal/push`) is in #995, the relay is the private `si-mods/gorex-relay` repository, and this is the phone's half; a blocked record's urgent push carries the alert that replaces the phone's local notification, `gorex.push.seen` reports the records the activity showed, and a registration with no target gets a start push that begins the activity remotely. The team decided these points on 2026-11-22.

This doc designs the relay that [IOS-006](IOS-006-live-activities.md) left open ("Later: a relay"). It replaces the direct last hop of that doc: no host ever holds an APNs key. Phase 2 of IOS-006 uses this relay as its only last hop.

## Problem

An update of a Live Activity through APNs needs a `.p8` key of the team of the app. Only the developer can hold it. A host that a user runs cannot. Without a push, the Live Activity freezes about 25 seconds after the app leaves the foreground. It shows as paused until the app comes back.

## Goal

- The Live Activity of a terminal updates while Rex Go is suspended, for every host the phone paired with.
- A host can start the activity while Rex Go is suspended and none runs, so a blocked record still reaches the phone.
- The host never sees the push token of the activity.
- A host can push only to a device that bound it.
- The phone reaches the relay over the public network. The path needs no tailnet, so a token that changes while the app sleeps can be bound in the time iOS gives.
- The relay stores bindings and nothing else. It logs no content.
- A phone with an old server, and a server with an old phone, keep the behavior of phase 1.

## Non-goals

- **End-to-end encryption of the content.** The widget extension renders the push directly. No extension can decrypt it. The relay and Apple see the content state in plain text.
- **A direct last hop.** No host holds an APNs key. The earlier direct design of [IOS-006](IOS-006-live-activities.md) is dropped.
- **Alert pushes in place of the local blocked notification.** The alert field is in the API. The swap itself is of the host's sender and the app (IOS-006), not of the relay.
- **Two activities per device.** The push-to-start begins the one terminal activity of the blocked record's target. A per-host activity (IOS-006, phase 2) is a proposal and out of scope.

## Design

### Parties and keys

- **The device key** is a P-256 key pair in the Keychain of Rex Go, made at first use. Its public part is the identity of the phone.
- **The push key of the host** is a P-256 key pair in the data dir of the host, mode 0600, made at first use. The host gives its public part to the phone in the result of `gorex.push.register`. The phone trusts it because the control connection is authenticated already.
- **The relay key** is the `.p8` of the app, on the relay only. It is topic-scoped to `dev.gorex.ios`, one key for each environment. It never enters a repository.

The signatures are ECDSA P-256 over SHA-256. The signature goes in the header `X-Gorex-Sig`, base64, over the raw request body. The verifier needs no canonical form, because it checks the bytes as they came.

### Register and bind

1. On each control connection, the phone calls `gorex.push.register` with its device key, its target (session and block of the terminal on screen, or none) and whether Live Activities are on. At feature version 2 it adds `host` and `host_name`: the id and the name the phone knows the host by, for the attributes of an activity a start push begins. The result carries the push key of the host and the URL of the relay.
2. The phone calls it again when the target changes, and `gorex.push.unregister` when the terminal closes or pairing ends.
3. The app requests the activity with `pushType: .token`. On `pushTokenUpdates`, the phone binds the token at the relay: device key, host key, token, environment. This works in the short wake iOS gives for a new token, because the relay is on the public network.
4. The app also listens to `pushToStartTokenUpdates`. The push-to-start token is of the attributes type, not of an activity, so the phone binds it with `kind: "start"` and the attributes type — once for each host it pairs with, as every host's binding of the one token is its own record at the relay.
5. The app gives the host no token. The host addresses the device key.

### Push

1. The host computes the content state of the target of the device, on each `program_status_changed` of the block. It coalesces the updates to one for each two seconds.
2. It posts the update to the relay, addressed to the device key, signed with its push key.
3. The relay checks the signature against the binding, then sends the push to APNs for each token bound to the device.
4. Each push carries a `stale-date` a few minutes ahead. A host or a relay that stops lets the activity show as paused.
5. When the control connection of a device closes, the host sends a last update: `connected: false`, `stale-date` of now. The activity shows the paused face, and the host goes on pushing new states to the token. The activity lives without the app, so it keeps updating while the app is suspended.

The host never learns whether the phone still listens. A dead token is removed when APNs rejects it.

### Push to start

An update push needs a running activity; a phone suspended with none cannot be reached that way. For it, iOS 17.2 hands the app a push-to-start token of each attributes type, and `event: "start"` begins an activity of that type remotely. The attributes the push carries are those the app would build when it shows the terminal: `host` and `hostName` as the phone knows them (which is why version 2 of the register sends them), the session and the block of the blocked record.

1. A registration with no target — the phone connected with no terminal on screen — is eligible for a start. One whose target is another terminal is not: its activity updates as today.
2. When the most urgent record of a block turns blocked, the host sends a start push to each eligible registration instead of an update: `attributes-type`, `attributes`, `content-state`, `alert` (Apple requires one on a start), `input-push-token: 1` and priority 10.
3. `input-push-token` wakes the app with the update token of the new activity (iOS 18). The existing `pushTokenUpdates` path binds it, and later changes reach the started activity as ordinary updates. The host also retargets the registration to the session and block it started, so the next change is an update, not another start.
4. The phone adopts the activity the push started: `activityUpdates` hands it to the driver, whose token watcher binds its update token, and a screen that shows the same terminal takes the activity over instead of ending it.

A start is rare and loud, so the sender limits it: never twice for one device, session and block while the record stays blocked — the dedupe key clears when the record is no longer blocked — and at most once for each device every five minutes. The maps are in memory; a restarted host may send a start it sent before. A registration without `host` — a phone that predates version 2 — gets no start, as its app binds no start token anyway; that is the phase-1 fallback.

### Content state

The host builds the `TerminalActivity.ContentState` of the target:

- `record`, `program`, `connected`: as the app computes them today. `record` also carries `id` — `""` for the root record — so the phone can report `gorex.push.seen` for what the activity showed.
- `title`: the title of the window, from the session view of the server. A rename that synced to the server shows.
- `line`: the `msg` (or `last_msg` for done and error) of the most urgent record. The host has no screen, so the screen-line fallback of phase 1 does not apply. A program that reports no message gets no line.

### Seen

A done or error record the user saw on the Live Activity is not news: when the app returns to the foreground it calls `gorex.push.seen` with the session, the block and the ids of the done and error records the activity's content state shows, and the host marks them seen as `gorex.see_block` does — the block's `seen_at` rises to the latest named record's `updated_at`, so every client stops counting them as new. The app reports the record the activity shows — the last state a local update or a push left — so a record that came and went in the background without reaching the activity is not marked. The report goes once for each record the activity shows, again when the record changes and when the activity ends; a host it could not reach is told once its `gorex.push.register` answers again.

The report names records by `id`, not at block granularity as `gorex.see_block` does with a time: the app knows exactly which records the activity showed, while a `seen_at` from it would have the app guess where its view left off — and two records can share an `updated_at` second. An `id` the host has no record of, as one a pushed update reported that has since gone, is skipped.

### Relay API

`https://relay.gorex.uk`, JSON over HTTPS:

- `POST /v1/bind`: `{token, device, host, env, kind, attributes_type}`; `X-Gorex-Sig` by the device key. `env` is `sandbox` or `production`. `kind` is `update` (also when left out) or `start`; a start binding needs `attributes_type`. An update binding replaces the binding of the token; a start binding is keyed by device, host and attributes type, so each host's bind of the one push-to-start token is its own record.
- `POST /v1/unbind`: `{token}`; `X-Gorex-Sig` by the device key. For a start binding: `{token, kind: "start", attributes_type, host}` — `host` narrows it to one pairing's record.
- `POST /v1/push`: `{device, event, content_state, stale_date, dismissal_date, alert, priority, attributes_type, attributes, input_push_token}`; `X-Gorex-Sig` by the host key. `event` is `update`, `end` or `start`. `alert` is optional but required on a start, which also needs `attributes_type`, `attributes` and `content_state`. Updates and ends go to the device's update bindings, a start to its start bindings of the attributes type named, and only to tokens whose binding names the host that signed.
- `GET /v1/health`: liveness only.
- Answers are JSON: `{"sent": 1}` or `{"error": "..."}`. Bodies are limited to 4 KB. Each token accepts a limited rate.

The relay sends to `api.push.apple.com` or `api.sandbox.push.apple.com` by the bound `env`: `apns-push-type: liveactivity`, `apns-topic: dev.gorex.ios.push-type.liveactivity`, the priority of the request, `authorization: bearer` of an ES256 JWT it renews every 50 minutes. `BadDeviceToken`, `ExpiredToken` and `Unregistered` drop the binding. The `aps` object maps the request's fields: `event`, `content-state`, `alert`, `stale-date`, `dismissal-date`, and on a start `attributes-type`, `attributes` and `input-push-token` as `1`.

### Rex API

`gorex.push.register`, `gorex.push.seen` and `gorex.push.unregister` are additive, feature-gated (`gorex.push`). Version 1 registers the device key, the target and `live_activities`. Version 2 adds `host` and `host_name` to the request, for the attributes of an activity a start push begins; the server advertises it in `feature_versions` and the phone sends the fields only at version 2. `gorex.push.seen` takes `session_id`, `block_id` and `record_ids` — the ids of the done and error records the activity showed, as `program_status` reports them — and needs the seen capability too; it answers an empty object, and skips an id the block has no record of. An old server answers `not_supported`; the phone then skips the relay path and keeps phase 1, and sends no `gorex.push.seen`. A version-1 server accepts the register of a new phone but gets no `host` fields — no start pushes. A server with `gorex.push` from before it answers `method_not_found`, which the phone sends the report without awaiting and ignores. An old phone calls nothing; the host does nothing.

### Security

- The token goes from the phone to the relay only. Its value is not guessable. A host that learns a token of another party cannot use it: the binding pins the host key.
- The push-to-start token is the app's, shared by every host the phone pairs with. A start binding names its host, so one host's start push reaches only the binding it signed for; an unbind carries the host for the same reason, or a removed host would drop another's record.
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
- Push to start: a start bind needs the attributes type, a start push needs its APNs fields, reaches the start bindings of that type alone and only where its host signed; a start unbind drops one pairing's record; update pushes ignore start bindings. The sender starts once per device, session and block while blocked, at most once per device each five minutes, and again after an unblock.
- Relay against a fake APNs server: HTTP/2, error answers, unregistered tokens.
- On a phone: pair, leave the app, watch the island update; kill the app, watch it still update; stop the host, watch it pause; token rotation with the app suspended. Leave the app with no terminal on screen, block an agent, watch the activity start and its first update arrive.

## Open questions

1. Which Apple topic list shows for a topic-specific key, and whether `dev.gorex.ios.push-type.liveactivity` needs its own entry or comes with the app.
2. Whether an unsigned archive keeps `aps-environment` at export (as [IOS-006](IOS-006-live-activities.md) asks).

## References

- **bark-server** (`finb/bark-server`, Go): a public APNs relay. It keeps a table of device keys to device tokens and sends with `sideshow/apns2`, an HTTP/2 client with `.p8` token auth. Its register API is open, as ours is. It differs: no signatures, no Live Activities, one app.
- **alma-relay** (`yetone/alma-relay`, Node and Cloudflare Workers): an agent tunnel that also forwards pushes. Its agent socket carries a `push` frame of notifications, `pushType: 'liveactivity'` among them, to `<topic>.push-type.liveactivity`, with an answer of one result per token. It differs: its agent holds a long socket and addresses by a token hash; our host posts per update and addresses by the device key.
- **rootshell**: see [IOS-006](IOS-006-live-activities.md), Reference. Its alert pushes are end-to-end encrypted through a notification service extension. A Live Activity cannot use that, because no extension can decrypt its pushes.
