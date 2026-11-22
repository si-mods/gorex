---
name: IOS-006
description: "Rex Go shows a terminal in the Dynamic Island and on the Lock Screen through Live Activities. Use when you change Live Activities or their line."
metadata:
  created: "2026-11-17"
  updated: "2026-11-21"
---

# IOS-006: Rex Go: Live Activities for sessions

Status: phase 1 built (#892, #900, #901). Phases 2 and 3 not built.

Phase 1 is built as `TerminalActivity` and `TerminalLive`. Phases 2 and 3 are proposals. The team decided these points. Phase 1 is the terminal activity: the terminal that is open when Rex Go leaves the foreground, kept connected for a short grace. The host sends the later updates, and the last hop is replaceable. This doc designs the direct last hop (an APNs key on the host) and leaves a relay for later. Rex Go is distributed to other people (public TestFlight, later the App Store). They cannot hold the APNs key of the app. The names `HostActivity`, `push.register` and `push.seen` are phase 2 and 3 proposals. They are not in the code.

## Problem

Rex Go shows what the program of each session does (working, needs input, done, failed) in its status inbox. It posts a local notification when a program turns blocked. Both work only while the app runs. When the phone locks or the user switches apps, iOS suspends Rex Go within seconds. The connections to the hosts close. Nothing reaches the phone until the app comes back to the foreground. An agent that waits for the user in that time waits unnoticed.

A Live Activity (ActivityKit) shows the state of a task. It shows it on the Lock Screen, in the Dynamic Island and on a paired Apple Watch. It also shows it in CarPlay and in the menu bar of a paired Mac. It is the place that iOS gives to "an agent is working and may need you".

## Goal

- When the user leaves Rex Go with a terminal open, that terminal shows in the Dynamic Island and on the Lock Screen. The activity shows its program, its state and its latest line. Its stream stays connected for a short grace (about 25 seconds), so a quick switch to another app and back loses nothing. After the grace, the activity says that it is paused. When the user comes back, the stream resumes as today.
- A Live Activity shows the session that most needs the user, and how many other sessions work, wait or finished.
- It stays correct while Rex Go is suspended. A program that turns blocked lights up the Lock Screen within seconds.
- The "needs input" notification arrives while Rex Go is suspended too.
- It shows no state older than it admits. When updates stop, it says so.
- Users whose hosts have no APNs key get phase 1. This means everyone but the developer, until a relay exists. The activity is live while Rex Go runs. When the app is suspended, the activity freezes and shows as paused.

## Non-goals

- **Acting from the Lock Screen.** To approve a permission or answer a question from the activity, the app must reach the host from an intent. This is for later, if at all.
- **One activity per session by default.** See Presentation.
- **Keeping Rex Go running in the background** after the grace that a background task gives. See Alternatives, b. The user chose the grace (2026-11-20) over a background mode that keeps the connection open. That mode breaks App Review Guideline 2.5.4 and drains the battery.
- **Programs that do not report.** The activity shows what OSC 7501 reports. GoRex installs no hooks to report for other programs.
- **Direct APNs for other users.** Only the developer account of the app can have an APNs key for Rex Go. Direct sending is the path of the developer, not a feature.
- **A push relay.** A relay would bring phase 2 to other users. This doc does not decide its design (see Later).

## Design

### Verified and assumed

The statements below carry one of three marks:

- **[code]**: read in this repository, or in the repository of rootshell.
- **[doc]**: from the documentation of Apple, with a link.
- **[assumption]**: not checked. The team must settle it before or during the phase that needs it.

### What a session's state is

- A program reports its state with OSC 7501 ([MOD-001-osc-7501.md](MOD-001-osc-7501.md)). The states are `idle`, `working`, `blocked` (`kind` permission, question or auth), `done` and `error`. A report can have a progress, an app, a title and a message. The docs name Claude Code and Pi as senders. omp appears in test fixtures. **[code]** It is not checked if every version of those agents sends it. **[assumption]**
- The session server parses the reports from the output of each session and keeps the records (`internal/rex/status.go`). It keeps at most 64 records for each session. A title has up to 192 bytes. A message has up to 2048 bytes. A prompt (OSC 133;A) or a reset drops all records except `done` and `error`. These two stay until the user sees them. `MostUrgent` picks the record that needs the user most (`status.go`, `StatusRank`: blocked > error > done > working > idle). Records survive an in-place upgrade. They do not survive a restart. **[code]**
- The gateway emits the block events `program_status_changed` and `program_status_removed` when the records of a block change (`internal/rexapi/events_blocks.go`). **[code]**
- Rex Go keeps one control connection for each host with `event_scope: all` (`ios/GoRex/Rex/Host.swift`, `bootstrap`). It decodes the records into `Host.ProgramStatus`. It calls `Host.blocked` when a record turns blocked. `Notifier` posts the local notification. `Host.inbox` (`ios/GoRex/Rex/Inbox.swift`) lists each window by its most urgent record. `seen` (which done and error records the user looked at) stays on the phone, apart from the own record of the server. **[code]**
- Rex Go has no background modes, no entitlements for this, and no push (`ios/project.yml`). Before phase 1, it asked for background time (`beginBackgroundTask`) in only two cases. The first case is to finish a write as it left (`WidgetPublisher.flush`). The second case is to keep sharing over a hotspot for a while (`HotspotShare`). Its built-in Tailscale stops with it ([IOS-001-tailnet.md](IOS-001-tailnet.md), Non-goals). No code stops the engine on leaving. It therefore runs as long as the app does, a background task included. **[code]**

In the foreground, the state is current to within the watch interval of the gateway. In the background, the state is current for a few seconds, then not at all. The host always knows the state, because its records do not depend on the phone.

### ActivityKit constraints

These constraints come from the documentation of Apple, checked 2026-11. The deployment target of Rex Go is iOS 26, so every API below is available.

- **Start.** `Activity.request(attributes:content:pushType:)` starts an activity only while the app is in the foreground, or from a `LiveActivityIntent`. A running app can update and end it in the background. The activity itself runs in its own sandbox and **cannot reach the network**. **[doc]** [Displaying live data](https://developer.apple.com/documentation/activitykit/displaying-live-data-with-live-activities), [request](https://developer.apple.com/documentation/activitykit/activity/request(attributes:content:pushtype:))
- **Lifetime.** The activity is active for at most 8 hours. Then the system ends it, and it stays on the Lock Screen for at most 4 more hours (12 in all). The dismissal date of an ended activity can be any time within those 4 hours. **[doc]**
- **Size.** Static and dynamic data together are at most 4 KB, for local updates and pushes alike. The Lock Screen presentation is truncated past 160 pt. Images larger than the presentation may keep the activity from starting. **[doc]**
- **Presentations.** All of these are required: Lock Screen, compact leading and trailing, minimal, and expanded (regions center, leading, trailing, bottom). The system may narrow compact and minimal (`isDynamicIslandLimitedInWidth`). Then they must fit in a 28 pt circle. `relevanceScore` picks which of the activities of an app the Dynamic Island shows. `supplementalActivityFamilies([.small, .medium])` adapts to the Smart Stack of Apple Watch, CarPlay (both compact) and StandBy. A paired Mac shows them in the menu bar. iOS 27 shows them in the Dynamic Island in landscape too. **[doc]** [Custom views](https://developer.apple.com/documentation/activitykit/creating-custom-views-for-live-activities), [WWDC26 223](https://developer.apple.com/videos/play/wwdc2026/223/)
- **Animation.** An animation lasts at most 2 seconds, and none runs in Always On. No animation runs free. Timer text (`Text(timerInterval:)`, `numericText`) and progress views move without updates. **[doc]** [Animating data updates](https://developer.apple.com/documentation/widgetkit/animating-data-updates-in-widgets-and-live-activities)
- **Stale date.** After `staleDate`, `isStale` turns true and the view can say that its data is old. **[doc]**
- **Alerts.** An update with an `AlertConfiguration` (or a push with `alert`) lights the screen. It shows the expanded presentation, or a banner without a Dynamic Island. Apple Watch shows its title and body. **[doc]**
- **Pushes.** These are the facts for a push. **[doc]** [Push notifications](https://developer.apple.com/documentation/activitykit/starting-and-updating-live-activities-with-activitykit-push-notifications), [Broadcast](https://developer.apple.com/documentation/usernotifications/setting-up-broadcast-push-notifications)
  - The header is `apns-push-type: liveactivity`, the topic is `<bundle id>.push-type.liveactivity`, and the priority is 5 or 10.
  - `aps` has `timestamp`, `event` (`start`, `update`, `end`), `content-state`, `stale-date`, `dismissal-date`, `alert` and `relevance-score`.
  - A push wakes the widget extension to render. It does not wake the app.
  - Each activity has its own push token, which may change (`pushTokenUpdates`). The app gets background time to handle a new token.
  - Push-to-start tokens (`pushToStartTokenUpdates`, iOS 17.2) start an activity remotely. The payload must carry an `alert` and `attributes-type`. With `input-push-token: 1` (iOS 18), the system wakes the app with the update token of the new activity.
  - Broadcast channels (iOS 18) update many activities with one push. They cannot start one. They are enabled only on the developer portal.
- **Budget.** Priority 10 counts against an hourly budget and is throttled past it. Priority 5 does not count. `NSSupportsLiveActivitiesFrequentUpdates` raises the budget. People can turn it off (`frequentPushesEnabled`). Apple publishes no numbers. **[doc]** The documentation also does not say how many activities one app can run at once. **[assumption]**
- **APNs.** APNs uses HTTP/2 only. It uses token auth with a `.p8` key and an ES256 JWT that the sender renews at least hourly. Keys can have the scope of one environment, or of chosen topics (bundle IDs) and an environment. **[doc]** [Token-based connection](https://developer.apple.com/documentation/usernotifications/establishing-a-token-based-connection-to-apns), [Sending requests](https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns), [APNs key options](https://developer.apple.com/news/?id=wy4tb0uo)
- **Target.** The UI lives in a widget extension. The `Info.plist` of the app sets `NSSupportsLiveActivities`. **[doc]**

### Terminal activity (phase 1)

Today the app does this. On `.background`, `TerminalScreen` suspends the stream of every pane at once (`pane.session.suspend()`). It resumes them on `.active`. The shell on the host keeps running. **[code]**

`TerminalActivity` runs one at a time, for the terminal on screen:

- **Attributes** (static): host id and name, session, block.
- **Content state**: the window title, and the program id (for the tile). It also has the `state`, `kind`, `progress`, `title` and `updated` of the most urgent OSC 7501 record of the block. For a plain shell or an idle record, it has none of these. It has `line`: what the program says it does, or else the line of the screen that tells the latest state (see **Line** below). It has `connected` (whether the stream is open). Titles and the host name are cut to 200 characters and 400 UTF-8 bytes. The line is cut to 80 characters and 240 bytes. The cut keeps whole characters only. With the longest of each, attributes and state stay well under 4 KB (`TerminalLiveTests`).
- **Seen**: Done and error records that the user saw do not count, as in the inbox. The open screen marks the records of a block as seen when they come (`Host.shown`). It tells the server (`gorex.see_block`), and the server tells every client. In the background, the user does not see the screen. `Host.shown` is therefore false there (`Notifier.inFront`). A program that finishes during the grace shows as done, here and on every client. When the app is in front again, the screen sees its block again, as it does when it appears.
- **Start**: when a terminal screen appears, in the foreground, as `Activity.request` requires. While Rex Go is in front, iOS does not show the activity of the app in the Dynamic Island. **[assumption]** If Settings › Live Activities is off, or `areActivitiesEnabled` is false, no activity starts.
- **Update**: while the stream is open, on a record change or a change of `line`. The updates are coalesced to one for each two seconds. The output of the stream marks the line as changed. The next update reads the rows of the screen and the row of the cursor (`readRows` of `TerminalSession`). A new line comes in the update after that.
- **Line** (`TerminalLive.state`, `TerminalActivity.screenLine`): what the program says it does. Claude Code and Pi report it in their OSC 7501 records (`docs/MOD-001-osc-7501.md`). `msg` says what they do or wait for, in any state. A done record keeps the last message as `last_msg`. A screen tells this less well. Its last non-empty line is the status bar of a TUI agent (Claude Code, Codex, Devin, Pi), never its state. The app therefore picks the line in this order:
  1. The `msg` of the most urgent record of the block (`Host.ProgramStatus.mostUrgent`, which skips done and error records that the user saw). This applies in any state, idle too, when `msg` is not blank. It wins over the line that the screen read.
  2. Else, when that record is done or failed, its `last_msg`, when it is not blank.

  In both, line breaks become spaces. For a program that reports no message, the app uses a line of the screen:

  3. When the screen ends in the input of a TUI, the app uses the first line above the input that it does not skip. It skips blank lines and lines of the input. It skips rules: box-drawing or block characters (U+2500–U+259F) and spaces, or a box's edge that carries a title, a run of them on each side (──── title ────). It skips lines in a box, which start and end with a side of a box. The app removes the leading spaces of the line. The app finds the input from the bottom, wherever the cursor is. The input is rows between two rules, at most 10, with at most 5 lines under the lower rule. These rows hold a prompt (`>`, `›`, `❯`, `❭`, `▌`, `»`, `▶` after spaces and a side of a box) or the cursor. Without rules, the input is the lowest line that starts with a prompt that shells do not use (`›`, `❭`, `▌`). That line has a blank line or a rule above it and at most 5 lines under it.
  4. Else, when text is under the row of the cursor, a TUI has its cursor in its input. The app does as in 3. The input is the row of the cursor and the lines directly above it, up to the topmost line that starts with a prompt.
  5. Else, the app does as for a shell or a program that prints. It uses the last line that is not blank, at or above the row of the cursor. A shell prompt at the bottom, `❯` too, has no rules around it, so it stays a shell prompt.
  6. If no such line exists, the app uses the last non-empty line of the screen.

  The line, a record's message and title, the window title and a notification's text are shown as iOS can draw them (`String.shownText`, `ios/Shared/ShownText.swift`): without what a terminal's escape sequences leave (CSI, OSC, DCS and the like, a lone ESC), without C0 and C1 controls but tab and line breaks, bidi controls and zero-width characters, private-use glyphs (a Nerd Font icon, a picture's placeholder), noncharacters and U+FFFD. Emoji and CJK text stay, joiners and combining marks too; a character that can be an emoji but whose default style is text (⏺, ❤) gets U+FE0E behind it, as iOS draws the emoji style where Unicode asks for text and a terminal's ⏺ is a bullet, not a button. A notification's title, subtitle and body pass the same filter (`Notifier.post`), as a program sends them in OSC 9 or 777. Nor is a line of rules only a line — box-drawing or block characters and spaces, or a titled edge, whether a box's edge caught mid-draw or a separator a command printed, says nothing; the state falls back as for a blank line (`TerminalLive.state`).

  The app reads the rows one row at a time, because Ghostty joins soft-wrapped rows in one read. The row of the cursor comes from the IME point of Ghostty, as for the slide of the terminal (`TerminalView.drawnBottom`).

  `TerminalLiveTests` has idle screens captured in a 100×40 terminal:

  - Claude Code draws its `❯` input between two rules, with a status line and a mode line under it. The cursor is in the input and shows.
  - Devin draws from the top, not full-screen. Its `❭` input is between rules, with its status bar under it. The cursor is in the input and shows.
  - Pi draws an editor with no prompt between rules, with three footer lines under it. The cursor is in the editor and is hidden.
  - Codex asked the user to sign in, so the capture has no Codex input.

  Rule 3 finds a program that hides its cursor under its last line, as an Ink program may while it draws. The output of a command that ends in a prompt between rules, such as a quote in a table, reads as an input. This is a known miss.
- **Grace**: [`IOS-012`](IOS-012-background-and-network-changes.md) replaces this item. The grace becomes app-wide: it starts when any host is connected, and the terminal screen registers its work with it. Its end follows `backgroundTimeRemaining`. The text below describes how the terminal screen uses that grace. On `.background`, the screen no longer suspends the streams at once. The app asks for a background task (`beginBackgroundTask`, `BackgroundGrace`), and the screen registers its work with it. The streams and the built-in Tailscale engine keep running, and the activity keeps updating. The grace ends as [`IOS-012`](IOS-012-background-and-network-changes.md) says: 5 seconds before `backgroundTimeRemaining` runs out, or 25 seconds after the 1-second read of it (at most 26 seconds after the user leaves), on a memory warning, or by its expiration handler, whichever comes first. Then the screen suspends the streams as today. It updates the activity with `connected: false` and a stale date of now, and ends the task. iOS gives about 30 seconds. Apple does not promise a number. **[assumption]** Without background time, the streams suspend at once.
  - Updates during the grace carry a stale date at the end of the grace plus 5 seconds. An app that stops without notice therefore still shows as paused.
  - Each update asks for a moment of background time of its own, to reach ActivityKit before the app stops. That time ends when the update is made, or when iOS takes it back.
  - The paused update goes within the task of the grace. The task ends when ActivityKit has the update, or at once when iOS takes the time back.
  - Dictation and a pane switch that still waits stop at once, as before.
- **Coming back during the grace** cancels the grace. Nothing was suspended. The connections of the hosts are not reset, because the app ran (`BackgroundGrace.awake`, from which `HostStore.suspended` measures).
- **Return**: on `.active`, the streams resume as today. The activity shows `connected: true` again.
- **End**: when the terminal screen closes, or when another terminal opens (which starts its own activity). The system dismisses it at once. An activity that the user dismissed stays dismissed for that screen. A terminal that opens later starts one again. At 8 hours, iOS ends the activity. The next foreground starts a new one.
- **Presentations**:
  - Compact leading: the tile of the program.
  - Compact trailing: the state glyph, a progress ring, or a timer since `updated`.
  - Minimal: the tile.
  - Expanded: the tile and window title, the host, the state, and `line` in a monospaced font. The font is the font of the system, because the extension does not carry the font of the terminal. All of this is in the bottom region, under the camera. The leading region sits in the top corner of the island, which cut the tile (#901).
    - The second row puts the state first. Then it shows the title of the record and the host. A long host name therefore cuts no state.
  - Lock Screen: a card of its own (`TerminalActivityCard`), not the expanded face. It has three rows, each of a fixed height. The card is therefore equally tall in every state (`testCardHeight`):
    - A small top row, 16 pt: the tile, the name of the program, the host and the name of the state. The state word is the only cue for the state: attention color for blocked, the done blue (`Color.done`) for done, red for failed, grey for working. No glyph shows. While the program works, "· " and the timer since `updated` follow the state word, or "· 64%" when it reports progress. The host is cut first, then the name of the program; the state and the timer are not cut.
    - `line` in a 21 pt bold font of the system, not monospaced. Two lines are always kept for it, also for a short line. If `line` is empty, the name of the state shows (for example "Needs Input"). A shell with no record and no line shows the title of the window.
    - A small bottom row, 16 pt: the title of the window, in grey. It is empty when the large row shows the title. A paused card shows "Paused — open Rex Go" in this row, not in an extra row.
  - The card draws its own background, the same in light and dark: a gradient from the top left to the bottom right in two shades of the hue of the tile color. In OKLCH, the shades have lightness 0.40 and 0.20, and 0.85 and 0.55 of the chroma of the tile (at most 0.16). The gradient does not mix the color with black, so it does not turn grey. A neutral tile (chroma under 0.035, such as Codex, Devin and the shells) gets a graphite with a cool cast (chroma 0.012, hue 260). `OKLCH` (in `ios/Shared`) converts the colors; a color outside sRGB is clipped.
  - The glyph of the tile shows white and faint (12 %, 6 % paused) at the right, 190 pt, centered vertically, its right edge 56 pt past the right edge of the card. All three rows stop 84 pt before the right edge of the padding (100 pt before the edge of the card), so the side of the glyph stays clear. Nothing is at the top right. `activityBackgroundTint` is black and the system buttons are white. The card is 127 pt tall at the default text size, under the 160 pt limit of the Lock Screen.
  - A blocked program gives the card a 2 pt rim in the attention color, as `keylineTint` gives the island.
  - A plain shell shows no mark beside its tile, but it shows one in compact trailing.
  - A paused activity (`connected: false` or `isStale`) is dimmed and reads "Paused — open Rex Go". On the card, the top row and the large line dim to half.
  - `line`, the title of the window, the host and (on the island) the title of the record are `.privacySensitive()`. The Lock Screen card shows no record title; its text stops growing at Dynamic Type xLarge.
- **Tap**: opens the terminal, as a notification tap does.

The activity shares the widget extension `GoRexWidgets` ([IOS-010-widgets.md](IOS-010-widgets.md)). The per-host activity below comes in phase 2, with the pushes of the host. While both exist, the terminal activity wins in the Dynamic Island while it is connected (higher `relevanceScore`).

### Presentation (phase 2: per host)

`HostActivity` is one activity for each host. It shows the most urgent record of that host, as `Host.inbox` and `Inbox.groups` rank it, and the counts of the rest. It is per host because each host pushes for itself. A push replaces the whole content state. If two hosts shared one activity, they would overwrite each other, unless something merges them. The `relevance-score` of each host follows its top state (blocked highest). The Dynamic Island therefore shows the host that needs the user. It is not per session, for these reasons:

- The Dynamic Island shows one of the activities of an app anyway.
- The 8-hour limit, the push budget and the push token are per activity. Many short sessions would mean many starts, each with an alert.
- The number of concurrent activities is limited and not documented.

Per-host activities come with phase 2. Phase 1 has only the terminal activity.

The content state has small fields, well under 4 KB:

- `state` (of the top record), `kind`, `app` (program id, for the tile), `progress`, `since` (when it began to work, for the timer) and `updated`.
- `heading` and `detail` (as `InboxItem` builds them). In pushes, Apple sees them (see Open questions).
- The counts `blocked`, `working`, `done` and `failed`.
- `target` (session and block of the top record).

The host name and id are static attributes, set at start.

The presentations are:

- **Compact leading**: the tile of the program (the artwork of `ProgramTiles`, sized to the presentation), tinted by state. Attention is the tint for blocked and done, and red is the tint for error.
- **Compact trailing**: For blocked, it shows the needs-input glyph and the blocked count. For working with progress, it shows a circular progress view. For working without progress, it shows `Text(timerInterval:)` since `since`. For done or failed, it shows the glyph. When narrowed, it shows the glyph alone.
- **Minimal**: the tinted tile, or the blocked count when more than one record is blocked.
- **Expanded**: Leading shows the tile and host. Center shows the heading (or the name of the state when there is none). Trailing shows the timer or "5m ago". Bottom shows the detail, then the counts ("1 needs input · 3 working · 1 done").
- **Lock Screen**: the host, then the top record as an inbox row (tile, heading, state, time), then the counts. When `isStale`, it shows "Paused — open Rex Go to update".
- A tap opens the terminal of the top record, as a notification tap does (`Notifier.opening`).

### Lifecycle (phase 2: per host)

- **Start**: The app starts the activity, in the foreground, when a host has a working or blocked record and no activity. From phase 2, the host also starts it by push-to-start, when the host has such a record and the phone has no activity for it.
- **Update**: The app updates the activity on every inbox change while the app runs. The updates are coalesced to one for each second. From phase 2, the host updates it by push. Both carry the time of the record. The `timestamp` of the host keeps an old push from replacing a newer state. **[assumption: how a local update and a push order on the device]**
- **End**: The activity ends when no record is working or blocked and the user saw the done and error records. The system dismisses it after a few minutes. At 8 hours, iOS ends it. The next foreground or push-to-start starts a new one.
- **Stale**: Without a push path, the activity is stale 30 seconds after the app leaves the foreground. With a push path, each push sets the stale date a few minutes ahead. A host that goes away therefore shows as stale.

### Notifications

- Phase 1 keeps the local blocked notification as it is.
- From phase 2, a blocked record updates the activity of the host with an alert at priority 10. This replaces the local blocked notification, so there is no double alert. When Live Activities are off, the host sends an ordinary alert push instead. In both cases, "needs input" arrives while the app is suspended. Taps open the terminal, as now.

### Host sender

The sender is one component on the host. It runs in the process that runs the gateway, which already sees the records of every block (`events_blocks.go`). **[code]** The sender does these things:

- It keeps these items for each registered device:
  - the environment (sandbox for Xcode builds, production for TestFlight and the App Store)
  - the push-to-start token
  - the device token for alerts
  - the update token of the current activity
  - the last state sent
- It computes the content state of the host with `rex.MostUrgent` over every session. It uses the seen state that the phone reports.
- It sends working and progress changes at priority 5, coalesced to one for each few seconds. It sends blocked, done and error at priority 10, one time for each change.
- It starts an activity by push-to-start (with `input-push-token: 1`) when the phone has none for this host. It ends the activity when nothing works or waits.
- It drops a token that APNs rejects as unregistered or expired.
- It hands each request to a **last hop**. The last hop takes a built request (kind, token, environment, priority, payload) and returns the answer of APNs. Nothing else depends on which last hop runs. This doc builds one last hop, `direct`. A relay would be another (see Later).

#### Rex API: `push.register`

`push.register` is a GoRex-only Rex API method. The phone calls it over the connection that it already has. The existing auth of the gateway therefore decides who can register. **[code]** for the auth of the gateway, not for the method, which is new.

- Params:
  - a device id (random, per install)
  - the environment
  - the push-to-start token and the device token
  - the update token of the activity of this host, when one runs
  - whether Live Activities are on
- The phone calls it on every connect and when a token changes.
- `push.seen` (or a field of the same call) reports which done and error records the user saw. The host then stops alerting for them. Today `seen` lives only on the phone.
- `push.unregister` runs on sign-out or when the user turns it off.

Known gap: An activity that the host starts by push-to-start gets its update token on the phone, which iOS wakes briefly. The phone must hand the token to the host in that time. On the local network or through a system VPN, it may succeed. Through the built-in Tailscale, the engine must start first. **[assumption]** Until the host has the token, it cannot update that activity. It can only send alerts. The next foreground closes the gap.

#### Direct last hop

- Go `net/http` speaks HTTP/2 over TLS by default. The JWT is ES256 over the `.p8` key (`crypto/ecdsa`), cached and renewed every 50 minutes. **[assumption: no third-party APNs library needed]**
- The key is a topic-specific APNs key with the scope of the bundle id of Rex Go and one environment. It is not verified if such a key also covers the `.push-type.liveactivity` topic. **[assumption]**
- The user configures the key with a command, for instance `gorex push key <file.p8> --key-id … --team-id … --env production`. The command copies the key into the data dir of GoRex (`rex.Dir()`) with mode 0600 (on Windows, an ACL for the user only). The key never goes in the repository, never in `hosts.json` or `remote.json`, and never in logs.
- Revocation: revoke the key on the developer portal, then run `gorex push key --remove`. A leaked key works until someone revokes it.
- Where a key is configured, the host sends directly. Elsewhere, it does not send at all, and its phones get phase 1. Content goes from the host to Apple only.

### Build and release

- A widget extension target in `ios/project.yml` (xcodegen `app-extension`, `com.apple.widgetkit-extension`), with a bundle id under `dev.gorex.ios`, embedded in the app. The attributes type is compiled into both. It links SwiftUI and WidgetKit only. It does not link GhosttyKit, Tailnet or FluidAudio. **[code]** for what the app links. The Home Screen widgets ([IOS-010-widgets.md](IOS-010-widgets.md)) add this target first, as `GoRexWidgets`.
- `NSSupportsLiveActivities` goes in the `Info.plist` of the app. Phase 2 also adds `NSSupportsLiveActivitiesFrequentUpdates`.
- Phase 1 needs no entitlement. From phase 2, the app needs the Push Notifications capability (`aps-environment`). It also lets the app register for the device token of the alert pushes.
- TestFlight CI (`ios/scripts/upload`) archives with `CODE_SIGNING_ALLOWED=NO`. It signs at export, cloud-managed, with `-allowProvisioningUpdates` and the App Store Connect API key. **[code]**
  - Whether that key can register the App ID of the extension and enable push without portal work depends on its role. **[assumption]**
  - An archive that is built unsigned may carry no entitlements. The export could then sign the app without `aps-environment`. Phase 2 may have to sign at archive.
  - Check the entitlements of the exported app before relying on it. **[assumption]**
- TestFlight and App Store builds use production APNs. Xcode builds use sandbox. The phone reports which environment it uses in `push.register`. A key of a host serves one environment.
- Broadcast channels need the portal by hand. This design does not use them.

## Alternatives

These are the options for updates while Rex Go is suspended:

| | What the user sees | New infrastructure | Privacy and security | App Review | Outcome |
| --- | --- | --- | --- | --- | --- |
| a. Foreground only | Live in the app. Frozen seconds after it leaves. "paused" past the stale date | none | none | low | phase 1, the base |
| b. Keep the app alive | Live while iOS lets it be. Drains the battery | none | none | high | rejected |
| c. Host sends to APNs directly | Live | an APNs key on the host | content goes host → Apple only. The key is the developer account's | low | phase 2, developer only |
| d. Host sends through a relay | Live | a relay holding the APNs key | state passes the relay and Apple | low | later, not designed here |
| e. Silent push, Background App Refresh, NetworkExtension | Nothing reliable (NetworkExtension: live, at the cost of a system VPN) | silent push needs d anyway | NetworkExtension brings the system VPN back | medium to high | rejected |

**a. Foreground only.** The app starts and updates the activity while it runs. At the background edge, it sets a short stale date. A background task (`beginBackgroundTask`) buys about 30 seconds, which covers a quick switch to another app. The built-in Tailscale engine is suspended with the app anyway. While the app is in front, the inbox shows the same information. On its own, this option therefore adds little. It is what users without a push path get.

**b. Keeping the app alive.** The background modes that could do it are `audio` (silent audio), `location` and `voip`. None of them is what a terminal app does. App Review Guideline 2.5.4 allows background modes only for their intended purpose. rootshell declares `location`, but it has a real location feature (its Location Diary). Rejected.

**c and d: the host sends, the last hop varies.** The host is the one party that knows the state while the phone sleeps. It therefore computes the content state, builds the payloads and keeps the tokens of the phone. Only the last hop differs:

- **Direct**: The host signs the request with an APNs key that is configured on it and sends the request to Apple. There is no cloud service. Nothing leaves the network of the user except the push to Apple. But the key belongs to the team of the app. Anyone who holds it can push to any Rex Go device whose token they learn. A topic-specific key narrows that to Rex Go, not to one user. So only the developer can have one. This option is built first. It proves the whole chain (tokens, payloads, widget, entitlements, signing) before any cloud service exists. It stays the path of the developer.
- **Relay**: The host posts the same payloads to a relay, which holds the key and sends. It is the only way to give other users phase 2, because they cannot hold the key. It is for later (see Later).

**e. Others.**

- Silent (background) pushes wake the app. But Apple asks for no more than two or three each hour, and may hold them back. They need a relay anyway. **[doc]** [Background updates](https://developer.apple.com/documentation/usernotifications/pushing-background-updates-to-your-app)
- Background App Refresh runs when iOS decides, often hours apart, for seconds. This is not enough to start the engine, reach a host and update.
- A Packet Tunnel extension runs in the background and could hold the host connections. It needs the system VPN, which [IOS-001-tailnet.md](IOS-001-tailnet.md) removed on purpose. It has a tight memory limit. It is not verified if it can call ActivityKit. **[assumption]** Rejected.

## Tests

- Unit tests: the content state from records (ranking, counts, seen), coalescing, and the 4 KB limit with the longest title and message. Also JWT signing and renewal, and the direct last hop against a fake APNs server (HTTP/2, error answers, unregistered tokens).
- Simulator on the Mac mini: start, update, stale and end. Pushes with `xcrun simctl push` or the Push Notifications Console.
- On a phone: lock it with an agent that works on a host with a key, make it block, and see the alert. Push-to-start with the app suspended. A host without a key turns paused. Leave it past 8 hours.

## Phases

| Phase | Scope | Effort | Who gets live updates once suspended |
| --- | --- | --- | --- |
| 1 | Terminal activity, ~25 s grace | 3–4 days | nobody |
| 2 | Host sender, `push.register`, direct last hop, alert pushes | 5–7 days | phones of hosts with a key: the developer's |
| 3 | Follow a session not on screen (optional) | 2–3 days | as phase 2 |

The efforts are rough estimates. **[assumption]**

1. **Terminal activity** (built, #892, #900, #901): `TerminalActivity` in `GoRexWidgets` and its presentations. The activity starts and ends with the terminal screen. It also has the grace before the streams suspend, the paused mark and the 8-hour restart. Prerequisite: phase 1 of the widgets adds the extension, and CI signs it. Unsolved: everything after the grace.
2. **Host sender, direct** (not built): the sender and its last-hop interface, `push.register` and seen, and the direct last hop with the key command. It also has push-to-start, update tokens, the ordinary "needs input" alert push while the app is suspended, and the push entitlement and signing. Content goes from the host to Apple only. Users without a key get the behavior of phase 1. Unsolved: the push-to-start token gap through the built-in Tailscale, programs that do not report, and records lost on a server restart.
3. **Follow a session not on screen** (not built): a terminal activity that starts from the menu of a session row. Actions from the activity stay out.

## Open questions

1. Do phases 1 and 2 ship to TestFlight as they land? Testers without a key get the behavior of phase 1 only.
2. Do direct pushes carry session titles and messages (Apple sees them in transit), or only states, counts and program ids?
3. How does the user configure the key on a host (the `gorex push key` command, or Settings in the app)? On which hosts: every machine of the developer, or one?
4. Can the App Store Connect key of the CI register the App ID of the extension and enable push without portal work? Does an unsigned archive keep `aps-environment` at export? The team must check both points in phase 1 and before phase 2.
5. Does a topic-specific key with the scope of the bundle id of Rex Go cover the `.push-type.liveactivity` topic? Or does the team need an environment-scoped key?

## Later: a relay

Other users cannot hold the APNs key of the app. A relay that holds the key would bring phase 2 to them. The last hop of the sender is replaceable, so the team can add a relay without a rework of the sender. This doc deliberately does not decide the design of the relay. These points are undecided:

- the operator, the hosting and the storage
- the auth between hosts and relay
- what leaves the network of the user
- how to merge hosts into one activity

## Reference: rootshell

This part comes from the repository of rootshell, which the team read for this design. **[code]**

- Its widget extension shows coding agents as counts (working, needs attention, idle) on the Lock Screen, in the Dynamic Island and on Apple Watch. The counts update on the device in the foreground only. At the background edge, it publishes the last counts with a frozen mark, and the view says they are paused. It starts its activities with `pushType: nil`, so it has no ActivityKit pushes.
- It declares the `location` and `remote-notification` background modes. It asks for about 30 seconds of background time on a switch.
- Its agent notifications are ordinary alert pushes through its own stateless relay. The relay key seals the APNs token in the sender credential. The content is end-to-end encrypted to a key on the device (X-Wing HPKE) and a notification service extension decrypts it. A command-line client on each computer sends the pushes from agent hooks. That relay is the infrastructure that rootshell depends on. It does not use it for Live Activities, because no extension can decrypt their pushes.
