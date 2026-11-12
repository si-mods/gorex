# Rex Go: Live Activities for sessions

Status: proposed. Nothing built. Decided: the host sends the updates,
with the last hop replaceable; this document designs the direct last
hop (an APNs key on the host) and leaves a relay for later. Rex Go is
distributed to other people (public TestFlight, later the App Store),
who can't hold the app's APNs key.

## Problem

Rex Go shows what each session's program is doing (working, needs
input, done, failed) in its status inbox, and posts a local
notification when a program turns blocked. Both work only while the app
runs. Once the phone locks or the user switches apps, iOS suspends Rex
Go within seconds, the connections to the hosts close, and nothing
reaches the phone until the app comes back to the foreground. An agent
that waits for the user in that time waits unnoticed.

A Live Activity (ActivityKit) shows a task's state on the Lock Screen,
in the Dynamic Island, on a paired Apple Watch, in CarPlay and in a
paired Mac's menu bar. It is the place iOS gives to "an agent is working
and may need you".

## Goal

- A Live Activity that shows the session that most needs the user, and
  how many others work, wait or finished.
- It stays correct while Rex Go is suspended: a program that turns
  blocked lights up the Lock Screen within seconds.
- The "needs input" notification arrives while Rex Go is suspended too.
- It shows no state older than it admits: when updates stop, it says so.
- Users whose hosts have no APNs key (everyone but the developer, until
  a relay exists) get phase 1: the activity is live while Rex Go runs,
  and once the app is suspended it freezes and shows as paused.

## Non-goals

- **Acting from the Lock Screen.** Approving a permission or answering
  a question from the activity would need the app to reach the host from
  an intent. Later, if at all.
- **One activity per session by default.** See Presentation.
- **Keeping Rex Go running in the background.** See Options, b.
- **Programs that don't report.** The activity shows what OSC 7501
  reports; GoRex installs no hooks to report for other programs.
- **Direct APNs for other users.** Only the app's developer account can
  have an APNs key for Rex Go; direct sending is the developer's path,
  not a feature.
- **A push relay.** It is what would bring phase 2 to other users; its
  design is not decided here (Later).

## Verified and assumed

Statements below are marked **[code]** (read in this repository or
rootshell's), **[doc]** (Apple's documentation, linked) or
**[assumption]** (not checked; to settle before or during the phase
that needs it).

## What a session's state is

- A program reports its state with OSC 7501
  ([osc-7501.md](osc-7501.md)): `idle`, `working`, `blocked` (`kind`
  permission, question or auth), `done` or `error`, with optional
  progress, app, title and message. The docs name Claude Code and Pi as
  senders; omp appears in test fixtures. **[code]** Whether every
  version of those agents sends it was not checked. **[assumption]**
- The session server parses the reports from each session's output and
  keeps the records (`internal/rex/status.go`): at most 64 per session,
  title up to 192 bytes, message up to 2048. A prompt (OSC 133;A) or a
  reset drops all but `done` and `error`, which stay until seen.
  `MostUrgent` picks the record that needs the user most
  (`status.go`, `StatusRank`: blocked > error > done > working > idle).
  Records survive an in-place upgrade, not a restart. **[code]**
- The gateway emits `program_status_changed` and
  `program_status_removed` block events when a block's records change
  (`internal/rexapi/events_blocks.go`). **[code]**
- Rex Go keeps one control connection per host with `event_scope: all`
  (`ios/GoRex/Rex/Host.swift`, `bootstrap`), decodes records into
  `Host.ProgramStatus`, and calls `Host.blocked` when a record turns
  blocked; `Notifier` posts the local notification. `Host.inbox`
  (`ios/GoRex/Rex/Inbox.swift`) lists each window by its most urgent
  record; `seen` (which done and error records the user looked at) is
  kept on the phone, apart from the server's own. **[code]**
- Rex Go has no background modes, no entitlements, no push, and no
  background task (`ios/project.yml`; no `beginBackgroundTask` or
  `BGTask` in `ios/`). Its built-in Tailscale stops with it
  ([ios-tailnet.md](ios-tailnet.md), Non-goals). **[code]**

So in the foreground the state is current to within the gateway's
watch interval; in the background it is current for a few seconds, then
not at all. The host, though, always knows: its records don't depend on
the phone.

## ActivityKit constraints

From Apple's documentation, checked 2026-11. Rex Go's deployment target
is iOS 26, so every API below is available.

- **Start.** `Activity.request(attributes:content:pushType:)` starts an
  activity only while the app is in the foreground, or from a
  `LiveActivityIntent`. A running app may update and end it in the
  background. The activity itself runs in its own sandbox and **can't
  reach the network**. **[doc]**
  [Displaying live data](https://developer.apple.com/documentation/activitykit/displaying-live-data-with-live-activities),
  [request](https://developer.apple.com/documentation/activitykit/activity/request(attributes:content:pushtype:))
- **Lifetime.** Active for at most 8 hours; then the system ends it,
  and it stays on the Lock Screen for at most 4 more (12 in all). An
  ended activity's dismissal date can be any time within those 4 hours.
  **[doc]**
- **Size.** Static and dynamic data together at most 4 KB, for local
  updates and pushes alike. The Lock Screen presentation is truncated
  past 160 pt. Images larger than the presentation may keep the activity
  from starting. **[doc]**
- **Presentations.** All are required: Lock Screen, compact leading and
  trailing, minimal, expanded (regions center, leading, trailing,
  bottom). Compact and minimal may be narrowed
  (`isDynamicIslandLimitedInWidth`); fit them in a 28 pt circle then.
  `relevanceScore` picks which of an app's activities the Dynamic Island
  shows. `supplementalActivityFamilies([.small, .medium])` adapts to
  Apple Watch's Smart Stack, CarPlay (both compact) and StandBy. A
  paired Mac shows them in the menu bar. iOS 27 shows them in the
  Dynamic Island in landscape too. **[doc]**
  [Custom views](https://developer.apple.com/documentation/activitykit/creating-custom-views-for-live-activities),
  [WWDC26 223](https://developer.apple.com/videos/play/wwdc2026/223/)
- **Animation.** At most 2 seconds, none in Always On. No free-running
  animation; timer text (`Text(timerInterval:)`, `numericText`) and
  progress views move without updates. **[doc]**
  [Animating data updates](https://developer.apple.com/documentation/widgetkit/animating-data-updates-in-widgets-and-live-activities)
- **Stale date.** Past `staleDate`, `isStale` turns true and the view
  can say its data is old. **[doc]**
- **Alerts.** An update with an `AlertConfiguration` (or a push with
  `alert`) lights the screen and shows the expanded presentation, or a
  banner without a Dynamic Island; Apple Watch shows its title and body.
  **[doc]**
- **Pushes.** `apns-push-type: liveactivity`, topic
  `<bundle id>.push-type.liveactivity`, priority 5 or 10; `aps` has
  `timestamp`, `event` (`start`, `update`, `end`), `content-state`,
  `stale-date`, `dismissal-date`, `alert`, `relevance-score`. A push
  wakes the widget extension to render, not the app. Each activity has
  its own push token, which may change (`pushTokenUpdates`; the app gets
  background time to handle a new one). Push-to-start tokens
  (`pushToStartTokenUpdates`, iOS 17.2) start an activity remotely; the
  payload must carry an `alert` and `attributes-type`; with
  `input-push-token: 1` (iOS 18) the system wakes the app with the new
  activity's update token. Broadcast channels (iOS 18) update many
  activities with one push, can't start one, and are enabled only on
  the developer portal. **[doc]**
  [Push notifications](https://developer.apple.com/documentation/activitykit/starting-and-updating-live-activities-with-activitykit-push-notifications),
  [Broadcast](https://developer.apple.com/documentation/usernotifications/setting-up-broadcast-push-notifications)
- **Budget.** Priority 10 counts against an hourly budget and is
  throttled past it; priority 5 does not count.
  `NSSupportsLiveActivitiesFrequentUpdates` raises the budget; people
  can turn it off (`frequentPushesEnabled`). Apple publishes no numbers.
  **[doc]** How many activities one app may run at once is not
  documented either. **[assumption]**
- **APNs.** HTTP/2 only; token auth with a `.p8` key and an ES256 JWT
  renewed at least hourly. Keys can be scoped to an environment, or to
  chosen topics (bundle IDs) and an environment. **[doc]**
  [Token-based connection](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns),
  [Sending requests](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns),
  [APNs key options](https://developer.apple.com/news/?id=wy4tb0uo)
- **Target.** The UI lives in a widget extension; the app's Info.plist
  sets `NSSupportsLiveActivities`. **[doc]**

## Options for updates while Rex Go is suspended

| | What the user sees | New infrastructure | Privacy and security | App Review | Outcome |
| --- | --- | --- | --- | --- | --- |
| a. Foreground only | Live in the app; frozen seconds after it leaves; "paused" past the stale date | none | none | low | phase 1, the base |
| b. Keep the app alive | Live while iOS lets it be; drains the battery | none | none | high | rejected |
| c. Host sends to APNs directly | Live | an APNs key on the host | content goes host → Apple only; the key is the developer account's | low | phase 2, developer only |
| d. Host sends through a relay | Live | a relay holding the APNs key | state passes the relay and Apple | low | later, not designed here |
| e. Silent push, Background App Refresh, NetworkExtension | Nothing reliable (NetworkExtension: live, at a system VPN's cost) | silent push needs d anyway | NetworkExtension brings the system VPN back | medium to high | rejected |

**a. Foreground only.** The app starts and updates the activity while
it runs and, at the background edge, sets a short stale date. A
background task (`beginBackgroundTask`) buys about 30 seconds, which
covers a quick switch to another app; the built-in Tailscale engine is
suspended with the app anyway. While the app is in front, the inbox
shows the same thing, so on its own it adds little. It is what users
without a push path get.

**b. Keeping the app alive.** The background modes that would do it are
`audio` (silent audio), `location` and `voip`. None is what a terminal
app does; App Review Guideline 2.5.4 allows background modes only for
their intended purpose. rootshell declares `location`, but has a real
location feature (its Location Diary). Rejected.

**c and d: the host sends, the last hop varies.** The host is the one
party that knows the state while the phone sleeps, so it computes the
content state, builds the payloads and keeps the phone's tokens. Only
the last hop differs:

- **Direct**: the host signs the request with an APNs key configured on
  it and sends it to Apple. No cloud service, and nothing leaves the
  user's network but the push to Apple. But the key belongs to the
  app's team, and anyone holding it can push to any Rex Go device whose
  token they learn; a topic-specific key narrows that to Rex Go, not to
  one user. So only the developer can have one. Built first: it proves
  the whole chain (tokens, payloads, widget, entitlements, signing)
  before any cloud service exists, and it stays the developer's path.
- **Relay**: the host posts the same payloads to a relay, which holds
  the key and sends. The only way to give other users phase 2, since
  they can't hold the key. Later; see Later.

**e. Others.**

- Silent (background) pushes wake the app, but Apple asks for no more
  than two or three an hour and may hold them back; they need a relay
  anyway. **[doc]**
  [Background updates](https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app)
- Background App Refresh runs when iOS decides, often hours apart, for
  seconds: not enough to start the engine, reach a host and update.
- A Packet Tunnel extension runs in the background and could hold the
  host connections. It needs the system VPN, which
  [ios-tailnet.md](ios-tailnet.md) removed on purpose, and has a tight
  memory limit; whether it may call ActivityKit is unverified.
  **[assumption]** Rejected.

## Design

### Presentation

One activity per host, `HostActivity`, showing that host's most urgent
record as `Host.inbox` and `Inbox.groups` rank it, and counts of the
rest. Per host, because each host pushes for itself: a push replaces
the whole content state, so two hosts sharing one activity would
overwrite each other unless something merges them. Each
host's `relevance-score` follows its top state (blocked highest), so the
Dynamic Island shows the host that needs the user. Not per session,
because:

- The Dynamic Island shows one of an app's activities anyway.
- The 8-hour limit, the push budget and the push token are per activity;
  many short sessions would mean many starts, each with an alert.
- The number of concurrent activities is limited and undocumented.

In phase 1, with no pushes, the app could merge all hosts into one
activity; it uses the same per-host shape so phase 2 changes nothing
on screen.

Content state, all small fields, well under 4 KB:

- `state` (the top record's), `kind`, `app` (program id, for the tile),
  `progress`, `since` (when it began working, for the timer),
  `updated`;
- `heading` and `detail` (as `InboxItem` builds them); in pushes, Apple
  sees them (Open questions);
- `blocked`, `working`, `done`, `failed` counts;
- `target` (session and block of the top record).

The host's name and id are static attributes, set at start.

Presentations:

- **Compact leading**: the program's tile (`ProgramTile`'s artwork,
  sized to the presentation), tinted by state: attention for blocked
  and done, red for error.
- **Compact trailing**: blocked: the needs-input glyph and the blocked
  count; working with progress: a circular progress view; working
  without: `Text(timerInterval:)` since `since`; done or failed: the
  glyph. Narrowed: the glyph alone.
- **Minimal**: the tinted tile, or the blocked count when more than one.
- **Expanded**: leading: tile and host; center: heading (or the state's
  name when there is none); trailing: the timer or "5m ago"; bottom:
  detail, then the counts ("1 needs input · 3 working · 1 done").
- **Lock Screen**: the host, then the top record as an inbox row (tile,
  heading, state, time), then the counts; when `isStale`:
  "Paused — open Rex Go to update".
- Tapping opens the terminal of the top record, as a notification tap
  does (`Notifier.opening`).

### Lifecycle

- **Start**: the app, in the foreground, when a host has a working or
  blocked record and no activity; or, from phase 2, the host by
  push-to-start when it has one and the phone has none for it.
- **Update**: from the app on every inbox change, coalesced to one per
  second, while it runs; from the host by push, from phase 2. Both carry
  the record's time; the host's `timestamp` keeps an old push from
  replacing newer state. **[assumption: how a local update and a push
  order on the device]**
- **End**: when no record is working or blocked and the done and error
  ones were seen; dismissed after a few minutes. At 8 hours iOS ends it;
  the next foreground or push-to-start begins a new one.
- **Stale**: without a push path, 30 seconds after the app leaves the
  foreground; with one, a few minutes ahead with each push, so a host
  that goes away shows as stale.

### Notifications

- Phase 1 keeps the local blocked notification as it is.
- From phase 2, a blocked record updates the host's activity with an
  alert at priority 10, which replaces the local blocked notification
  (no double alert). When Live Activities are off, the host sends an
  ordinary alert push instead, so "needs input" arrives while the app is
  suspended either way. Taps open the terminal, as now.

### Host sender

One component on the host, in the process that runs the gateway, which
already sees every block's records (`events_blocks.go`). **[code]** It:

- keeps, per registered device: the environment (sandbox for Xcode
  builds, production for TestFlight and the App Store), the
  push-to-start token, the device token for alerts, the current
  activity's update token, and the last state sent;
- computes the host's content state with `rex.MostUrgent` over every
  session, honoring the seen state the phone reports;
- sends working and progress changes at priority 5, coalesced to one per
  few seconds, and blocked, done and error at 10, once per change;
- starts an activity by push-to-start (with `input-push-token: 1`) when
  the phone has none for this host, ends it when nothing works or waits;
- drops a token APNs rejects as unregistered or expired;
- hands each request to a **last hop**. The last hop takes a built
  request (kind, token, environment, priority, payload) and returns
  APNs's answer; nothing else depends on which one runs. This document
  builds one, `direct`; a relay would be another (Later).

#### Rex API: `push.register`

A GoRex-only Rex API method. The phone calls it over the connection it
already has, so the gateway's existing auth decides who may register.
**[code]** for the gateway's auth, not for the method, which is new.

- Params: a device id (random, per install), environment, push-to-start
  token, device token, the update token of this host's activity when
  one runs, and whether Live Activities are on.
- The phone calls it on every connect and whenever a token changes.
- `push.seen` (or a field of the same call) reports which done and
  error records the user saw, so the host stops alerting for them.
  Today `seen` lives only on the phone.
- `push.unregister` on sign-out or when the user turns it off.

Known gap: an activity the host starts by push-to-start gets its update
token on the phone, which iOS wakes briefly. The phone must hand it to
the host in that time. On the local network or through a system VPN it
may; through the built-in Tailscale, the engine has to start first.
**[assumption]** Until the host has the token, it can't update that
activity, only send alerts; the next foreground fixes it.

#### Direct last hop

- Go's `net/http` speaks HTTP/2 over TLS by default; the JWT is ES256
  over the `.p8` key (`crypto/ecdsa`), cached and renewed every
  50 minutes. **[assumption: no third-party APNs library needed]**
- The key: a topic-specific APNs key scoped to Rex Go's bundle id and
  one environment. Whether such a key also covers the
  `.push-type.liveactivity` topic is unverified. **[assumption]**
- Configured with a command, for instance
  `gorex push key <file.p8> --key-id … --team-id … --env production`,
  which copies the key into GoRex's data dir (`rex.Dir()`) with mode
  0600 (on Windows, an ACL for the user only). Never in the repository,
  never in `hosts.json` or `remote.json`, never in logs.
- Revocation: revoke the key on the developer portal, then
  `gorex push key --remove`. A leaked key works until revoked.
- Where a key is configured, the host sends directly; elsewhere it
  doesn't send at all, and its phones get phase 1. Content goes from
  the host to Apple only.

## Build and release

- A widget extension target in `ios/project.yml` (xcodegen
  `app-extension`, `com.apple.widgetkit-extension`), bundle id under
  `dev.gorex.ios`, embedded in the app; the attributes type compiled
  into both. It links SwiftUI and WidgetKit only, not GhosttyKit,
  Tailnet or FluidAudio. **[code]** for what the app links.
- `NSSupportsLiveActivities` in the app's Info.plist; phase 2 also
  `NSSupportsLiveActivitiesFrequentUpdates`.
- Phase 1 needs no entitlement. From phase 2 the app needs the Push
  Notifications capability (`aps-environment`), which also lets it
  register for the alert pushes' device token.
- TestFlight CI (`ios/scripts/upload`) archives with
  `CODE_SIGNING_ALLOWED=NO` and signs at export, cloud-managed, with
  `-allowProvisioningUpdates` and the App Store Connect API key. **[code]**
  Whether that key may register the extension's App ID and enable push
  without portal work depends on its role. **[assumption]** An archive
  built unsigned may carry no entitlements, so the export could sign
  the app without `aps-environment`; phase 2 may have to sign at
  archive. Check the exported app's entitlements before relying on it.
  **[assumption]**
- TestFlight and App Store builds use production APNs; Xcode builds use
  sandbox. The phone reports which in `push.register`, and a host's key
  serves one environment.
- Broadcast channels would need the portal by hand. This design doesn't
  use them.

## Phases

| Phase | Scope | Effort | Who gets live updates once suspended |
| --- | --- | --- | --- |
| 1 | Local-only activity | 3–4 days | nobody |
| 2 | Host sender, `push.register`, direct last hop, alert pushes | 5–7 days | phones of hosts with a key: the developer's |
| 3 | Follow one session (optional) | 2–3 days | as phase 2 |

Efforts are rough estimates. **[assumption]**

1. **Local activity**: the extension, the attributes, the
   presentations; start, update and end from the inbox; the stale mark
   at the background edge; the 8-hour restart. Prerequisite: CI signs
   the extension. Unsolved: everything after the app is suspended.
2. **Host sender, direct**: the sender and its last-hop interface,
   `push.register` and seen, the direct last hop with the key command,
   push-to-start, update tokens, the ordinary "needs input" alert push
   while the app is suspended, the push entitlement and signing.
   Content goes from the host to Apple only. Users without a key get
   phase 1's behavior. Unsolved: the push-to-start token gap through the
   built-in Tailscale; programs that don't report; records lost on a
   server restart.
3. **Follow one session**: a per-session activity from a session row's
   menu; actions from the activity stay out.

## Test plan

- Unit tests: content state from records (ranking, counts, seen),
  coalescing, the 4 KB limit with the longest title and message; JWT
  signing and renewal; the direct last hop against a fake APNs server
  (HTTP/2, error answers, unregistered tokens).
- Simulator on the Mac mini: start, update, stale, end; pushes with
  `xcrun simctl push` or the Push Notifications Console.
- On a phone: lock it with an agent working on a host with a key, make
  it block, and see the alert; push-to-start with the app suspended;
  a host without a key turning paused; leave it past 8 hours.

## Open questions

1. Do phases 1 and 2 ship to TestFlight as they land, given that
   testers without a key get phase 1's behavior only?
2. Do direct pushes carry session titles and messages (Apple sees them
   in transit), or only states, counts and program ids?
3. How is the key configured on a host (the `gorex push key` command,
   or Settings in the app), and on which hosts: every machine of the
   developer, or one?
4. Does the CI's App Store Connect key have the role to register the
   extension's App ID and enable push without portal work, and does an
   unsigned archive keep `aps-environment` at export? To check in
   phase 1 and before phase 2.
5. Does a topic-specific key scoped to Rex Go's bundle id cover the
   `.push-type.liveactivity` topic, or is an environment-scoped key
   needed?

## Later: a relay

Other users can't hold the app's APNs key, so a relay holding it is
what would bring phase 2 to them. The sender's last hop is replaceable
so a relay can be added without redoing the sender. Its design
(operator, hosting, storage, auth between hosts and relay, what leaves
the user's network, merging hosts into one activity) is deliberately
not decided here.

## Reference: rootshell

From its repository, read for this design. **[code]**

- Its widget extension shows coding agents as counts (working, needs
  attention, idle) on the Lock Screen, in the Dynamic Island and on
  Apple Watch. Counts update on the device in the foreground only; at
  the background edge it publishes the last counts marked frozen, and
  the view says they are paused. It starts its activities with
  `pushType: nil`: no ActivityKit pushes.
- It declares the `location` and `remote-notification` background
  modes, and asks for about 30 seconds of background time on a switch.
- Its agent notifications are ordinary alert pushes through its own
  stateless relay: the APNs token is sealed in the sender credential by
  the relay's key, the content is end-to-end encrypted to a key on the
  device (X-Wing HPKE) and decrypted by a notification service
  extension. A command-line client on each computer sends them from
  agent hooks. That relay is the infrastructure rootshell depends on,
  and it does not use it for Live Activities, whose pushes no extension
  can decrypt.
