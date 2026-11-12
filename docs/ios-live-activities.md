# Rex Go: Live Activities for sessions

Status: proposed. Nothing built. The open questions at the end need a
decision before phase 2.

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

- One Live Activity that shows the session that most needs the user, and
  how many others work, wait or finished, across hosts.
- It stays correct while Rex Go is suspended: a program that turns
  blocked lights up the Lock Screen within seconds.
- It shows no state older than it admits: when updates stop, it says so.

## Non-goals

- **Acting from the Lock Screen.** Approving a permission or answering
  a question from the activity would need the app to reach the host from
  an intent. Later, if at all.
- **One activity per session by default.** See Presentation.
- **Keeping Rex Go running in the background.** See Options, b.
- **Programs that don't report.** The activity shows what OSC 7501
  reports; GoRex installs no hooks to report for other programs.

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
not at all.

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
- **APNs keys.** Token auth (a `.p8` key, JWT signed hourly). Keys can
  now be scoped to an environment, or to chosen topics (bundle IDs) and
  an environment. **[doc]**
  [Token-based connection](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns),
  [APNs key options](https://developer.apple.com/news/?id=wy4tb0uo)
- **Target.** The UI lives in a widget extension; the app's Info.plist
  sets `NSSupportsLiveActivities`. **[doc]**

## Options for updates while Rex Go is suspended

| | What the user sees | Effort | New infrastructure | Privacy and security | App Review |
| --- | --- | --- | --- | --- | --- |
| a. Foreground only | Live in the app; frozen seconds after it leaves; "paused" past the stale date | 3–4 days | none | none | low |
| b. Keep the app alive | Live while iOS lets it be; drains the battery | 1–2 days, likely redone | none | none | high |
| c. Hosts push to APNs | Live | a + 3–4 days | an APNs key on every host | the key on every host; unusable for anyone but the key's owner | low |
| d. Push relay | Live; can start itself; also delivers ordinary pushes | a + 8–12 days | a relay with the APNs key, a small store | session state passes the relay and APNs | low |
| e. Silent push, Background App Refresh, NetworkExtension | Nothing reliable (NetworkExtension: live, at a system VPN's cost) | 1–2 days to weeks | silent push needs d anyway | NetworkExtension brings the system VPN back | medium to high |

Efforts are rough estimates. **[assumption]**

**a. Foreground only.** The app starts and updates the activity while
it runs and, at the background edge, sets a short stale date. A
background task (`beginBackgroundTask`) buys about 30 seconds, which
covers a quick switch to another app; the built-in Tailscale engine is
suspended with the app anyway. Value is small: while the app is in
front, the inbox shows the same thing. It is the base of every other
option, not a feature on its own.

**b. Keeping the app alive.** The background modes that would do it are
`audio` (silent audio), `location` and `voip`. None is what a terminal
app does; App Review Guideline 2.5.4 allows background modes only for
their intended purpose. rootshell declares `location`, but has a real
location feature (its Location Diary). Rejected.

**c. Hosts send pushes themselves.** Each host holds an APNs key for
Rex Go's topics and sends to the activity's token, which it learns over
the Rex API. Works for the developer's own machines. It can't ship: the
key belongs to the app's team, and anyone holding it can push to any
Rex Go device whose token they learn. A topic-specific key narrows the
damage to Rex Go, not to one user. Rejected for distribution; usable as
a private spike for phase 2.

**d. A push relay.** A small service holds the APNs key. The phone
registers its tokens with it; hosts post state with a credential the
phone gave them; the relay sends the pushes. This is the only option
that keeps the activity live for every user without their own APNs
key. Its costs are a service to run, and that whatever the activity
shows leaves the user's network (see Relay). Chosen.

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

One activity, `SessionsActivity`, for all hosts, showing the most urgent
record as `Host.inbox` and `Inbox.groups` rank it, and counts of the
rest. One rather than one per session, because:

- The Dynamic Island shows one of an app's activities anyway (the
  highest `relevanceScore`).
- The 8-hour limit, the push budget and the push token are per activity;
  many short sessions would mean many starts, each with an alert.
- The number of concurrent activities is limited and undocumented.

A per-session activity ("Follow on Lock Screen", from a session row's
menu) for one long task may come later (phase 3).

Content state, all small fields, well under 4 KB:

- `state` (the top record's), `kind`, `app` (program id, for the tile),
  `progress`, `since` (when it began working, for the timer),
  `updated`;
- `heading` and `detail` (as `InboxItem` builds them), and `host`
  (the host's name in Rex Go) — optional, see Open questions;
- `blocked`, `working`, `done`, `failed` counts;
- `deeplink` target (host id, session, block), opaque to the relay.

Presentations:

- **Compact leading**: the program's tile (`ProgramTile`'s artwork,
  sized to the presentation), tinted by state: attention for blocked
  and done, red for error.
- **Compact trailing**: blocked: the needs-input glyph and the blocked
  count; working with progress: a circular progress view; working
  without: `Text(timerInterval:)` since `since`; done or failed: the
  glyph. Narrowed: the glyph alone.
- **Minimal**: the tinted tile, or the blocked count when more than one.
- **Expanded**: leading: tile and host; center: heading; trailing: the
  timer or "5m ago"; bottom: detail, then the counts
  ("1 needs input · 3 working · 1 done").
- **Lock Screen**: up to three rows like the inbox's (tile, heading,
  host, state, time); when `isStale`: "Paused — open Rex Go to update".
- Tapping opens the terminal of the top record, as a notification tap
  does (`Notifier.opening`).

### Lifecycle

- **Start**: in phase 1, when a record turns working or blocked while
  the app is in the foreground and no activity runs. In phase 2, also
  the host, by push-to-start, when nothing runs.
- **Update**: on every inbox change, coalesced to one per second
  locally; pushes as in Host side, below.
- **End**: when no record is working or blocked and the done and error
  ones were seen; dismissed after a few minutes. At 8 hours iOS ends it;
  the next foreground or push-to-start begins a new one.
- **Stale**: phase 1 sets the stale date to 30 seconds after the app
  leaves the foreground; phase 2 sets it a few minutes ahead with each
  push, so a host that goes away shows as stale.

### Notifications

- Phase 1 keeps the local blocked notification as it is.
- Phase 2: a blocked record updates the activity with an alert at
  priority 10, which replaces the blocked notification (no double
  alert). OSC 9 and 777 notifications, and blocked ones when Live
  Activities are off, go as ordinary alert pushes through the relay,
  end-to-end encrypted and decrypted by a notification service
  extension, as rootshell's are.

### Host side (phase 2)

- A GoRex-only Rex API method, for instance `push.register`, takes the
  relay's sender credential from the phone over the connection it
  already has. No pairing code to copy: the gateway already decides who
  the phone is. The gateway keeps the credentials per device and drops
  one the relay rejects.
- The session server, where `program_status_changed` is emitted,
  computes the host's part of the content state with `rex.MostUrgent`
  and posts it to the relay: working and progress at priority 5,
  coalesced to one per few seconds; blocked, done and error at 10, once
  per change.
- "Seen" must come from the host. Today the phone keeps its own `seen`;
  the server's `MostUrgent` takes a seen time. The phone reports what it
  marks seen (as the desktop's pane does), so a done record the user
  read stops alerting everywhere.

### Relay (phase 2)

- **Stores**: per device, the push-to-start token and the current
  activity's update token, the device's public key for end-to-end
  pushes, and per host a revocable sender id. Tokens change; the app
  posts the new one straight to the relay, which the woken app can
  reach without a host.
- **Merges** each host's part into one content state per device, since
  every push replaces the whole state and hosts would overwrite each
  other. That makes the relay stateful (one small record per device).
  Without merging, the fallback is one activity per host.
- **Auth**: the phone registers with an app-attested request (App
  Attest) **[assumption: to evaluate]**, and mints sender credentials
  bound to its registration; hosts present them. A re-registration
  invalidates old ones.
- **What leaves the network**: by default only states, counts, program
  ids, times and opaque ids. Titles and messages only when the user
  turns them on (Open questions). Content state can't be decrypted on
  the phone the way an alert push is, since no service extension runs
  for Live Activity pushes; whether the widget could decrypt a field
  while rendering is unverified. **[assumption]** Alert text is always
  readable by the relay and APNs; keep it generic ("Needs input").
- **Abuse**: rate limits per credential and per device, priority 10
  only for blocked, done and error, a size check, and revocation from
  the phone (Settings lists hosts that send).
- **Who runs it, and cost**: the app's team, since it holds the key.
  APNs is free; the load fits a free or near-free tier of a serverless
  host. Whether Cloudflare Workers can reach APNs over HTTP/2 is
  unverified. **[assumption]** It is the only cloud service GoRex would
  depend on, which reverses the removal of the coordinator (#390).

## Build and release

- A widget extension target in `ios/project.yml` (xcodegen
  `app-extension`, `com.apple.widgetkit-extension`), bundle id under
  `dev.gorex.ios`, embedded in the app; the attributes type compiled
  into both. It links SwiftUI and WidgetKit only, not GhosttyKit,
  Tailnet or FluidAudio. **[code]** for what the app links.
- `NSSupportsLiveActivities` in the app's Info.plist; phase 2 also
  `NSSupportsLiveActivitiesFrequentUpdates`.
- Phase 1 needs no entitlement. Phase 2 needs the Push Notifications
  capability (`aps-environment`), a notification service extension, and
  an App Group if the extensions share keys.
- TestFlight CI (`ios/scripts/upload`) archives with
  `CODE_SIGNING_ALLOWED=NO` and signs at export, cloud-managed, with
  `-allowProvisioningUpdates` and the App Store Connect API key. **[code]**
  Whether that key may register the extension's App ID and enable push
  without portal work depends on its role. **[assumption]** Whether an
  archive built unsigned keeps the entitlements the export needs is
  unverified; phase 2 may have to sign at archive. **[assumption]**
- Broadcast channels would need the portal by hand. This design doesn't
  use them.

## Phases

1. **Local activity** (3–4 days): the extension, the attributes, the
   presentations, start, update and end from the inbox, the stale mark
   at the background edge, 8-hour restart. Prerequisite: CI signs the
   extension. Left unsolved: everything after the app is suspended. Ship
   to TestFlight only, as the base for phase 2.
2. **Relay and host sender** (8–12 days): the relay, `push.register`
   and the session server's sender, push-to-start with
   `input-push-token`, host-side seen, end-to-end alert pushes and the
   service extension. Prerequisites: the open questions below.
   Left unsolved: programs that don't report, records lost on a server
   restart.
3. **Follow one session** (optional): a per-session activity from a
   session row; actions from the activity stay out.

## Test plan

- Unit tests: content state from inbox items (ranking, counts, seen),
  coalescing, the 4 KB limit with the longest title and message.
- Simulator on the Mac mini: start, update, stale, end; pushes with
  `xcrun simctl push` or the Push Notifications Console.
- On a phone: lock it with an agent working, make it block, and see the
  alert; leave it past 8 hours.
- Relay: rejected credential, rate limit, token change, a host that goes
  away turning stale.

## Open questions

1. Is a cloud relay acceptable at all, given GoRex has none since #390?
   Without it, Live Activities are foreground-only and not worth
   shipping on their own.
2. Who runs the relay, where (Cloudflare Workers, another host, the
   user's own infrastructure), and under which account?
3. May session titles and messages leave the user's network, off by
   default, or only states and counts, ever?
4. One activity merged by the relay (stateful), or one per host
   (stateless relay, more activities)?
5. Should the relay also carry ordinary end-to-end encrypted pushes
   (blocked, OSC 9/777, command finished) in the same phase? It is the
   larger gain, and the same infrastructure.
6. Is phase 1 worth a TestFlight build before phase 2 is decided?

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
