# Rex Go: widgets

Status: phase 1 built (the `GoRexWidgets` target, the snapshot and its
writer, the Inbox widget, `rexgo://open` and `inbox`); TestFlight does
not sign it with the App Group yet (see Build and release). Phases 2
and 3 are not built.

## Problem

To see whether an agent waits for the user, or to get back to a
terminal, the user opens Rex Go, waits for it to connect, and finds the
window in the list. The Home Screen, the Lock Screen, StandBy and
Control Center can show that state, or open that terminal, without the
list in between. Rex Go puts nothing there.

## Goal

- An **Inbox** widget: what the status inbox shows (blocked, error,
  done, working), the most urgent first, on the Home Screen and the
  Lock Screen.
- A **Terminal** widget: one window the user picks, with its program's
  tile and state; a tap opens that terminal.
- A tap on any row opens its terminal, as a notification tap does.
- A widget never shows state older than it says: each one shows when
  its data was taken, and turns plainly stale after a while.
- The extension is the one the Live Activity will use
  ([ios-live-activities.md](ios-live-activities.md)), so the target,
  signing and shared code are done once.

## Non-goals

- **Live state while Rex Go is suspended.** A widget shows what the app
  last saw. Live state on the Lock Screen is the Live Activity's job,
  and its phase 2 (the host pushes) is what makes it live. See Options.
- **Acting from a widget.** No buttons that answer an agent, approve a
  permission or type into a terminal: each would need the extension to
  reach the host. Later, if at all.
- **The extension connecting to hosts.** See Options, b.
- **macOS and watchOS widgets.** Rex Go is an iPhone and iPad app.
- **Changing what the inbox ranks or counts.** The widget shows
  `Host.inbox` as it is.

## Verified and assumed

Statements are marked **[code]** (read in this repository), **[doc]**
(Apple's documentation or WWDC session, linked) or **[assumption]** (not
checked; to settle before or in the phase that needs it).
**[checked]** marks what was tried on the Mac mini and seen to hold.

## What there is to show

- `Host.inbox` (`ios/GoRex/Rex/Inbox.swift`) lists each window whose
  program needs the user or works, by its most urgent OSC 7501 record;
  `Inbox.groups` orders them blocked, error, done, working. Done and
  error records the user saw drop out (`mostUrgent`, `seen`). It is
  empty while a host is not online. **[code]**
- `InboxItem.heading` and `detail` build the two lines a row shows;
  `Inbox.ago` the time. **[code]**
- `Program` (`ios/GoRex/Views/ProgramTile.swift`) maps a program id to
  its name, glyph and tile colors; the brand marks are in
  `Assets.xcassets/Brands`. **[code]**
- The hosts are `HostConfig`s saved as `hosts` in `UserDefaults`; LAN
  tokens are in the Keychain (`KeychainLANTokens`); the built-in
  Tailscale's state is under Application Support. **[code]**
- A notification tap sets `Notifier.shared.opening` to a
  `TerminalTarget` (host, session, block, title, `explicit: true`), and
  `HostListView` opens it. The app's URL scheme is `rexgo`, used for
  the tailcat pairing code and the Tailscale login's callback;
  `onOpenURL` handles only the pairing code. **[code]**
- Rex Go has no App Group and no background mode. Its only entitlement
  is the iCloud key-value store (`GoRex.entitlements`). **[code]**

## WidgetKit constraints

- A widget runs in its own extension process. It renders a timeline of
  entries its provider returns; it does not run while shown. **[doc]**
- Reloads are budgeted. Reloads that the app asks for while it is in
  the foreground do not count against the widget's daily limit.
  **[doc]**, quoted from the `TimelineProvider` documentation in
  [this forum thread](https://developer.apple.com/forums/thread/652946).
  Developers report that reloads asked for in quick succession, or from
  the background, are dropped. **[assumption]** until tested on a
  release build.
- From iOS 26, a server can refresh widgets by push: a
  `WidgetPushHandler` hands over a push token; the request has
  `apns-push-type: widgets`, topic `<bundle id>.push-type.widgets`, and
  `content-changed: true` in `aps`. A push carries no data: it only makes
  the provider run again. Pushes are budgeted and delivered
  opportunistically. **[doc]**
  [What's new in widgets, WWDC25](https://developer.apple.com/videos/play/wwdc2025/278/),
  [WidgetPushHandler](https://developer.apple.com/documentation/widgetkit/widgetpushhandler)
- An extension reads the app's data only through a shared container:
  an App Group (`com.apple.security.application-groups`) for files and
  `UserDefaults(suiteName:)`, a Keychain access group for secrets.
  **[doc]**
- A widget extension has a small memory limit, far below what the
  built-in Tailscale engine needs. **[assumption]** The exact limit is
  not published.
- Text marked `.privacySensitive()` is redacted on the Lock Screen and
  in StandBy while the device is locked. **[doc]**
- Configurable widgets use `AppIntentConfiguration` with an
  `AppEntity` and its `EntityQuery`; Control Center controls are
  `ControlWidget`s (iOS 18). Rex Go's deployment target is iOS 26, so
  all are available. **[doc]** **[code]** for the target.

## Options for where a widget's data comes from

| | What the user sees | Work | Outcome |
| --- | --- | --- | --- |
| a. The app writes a snapshot | State as of the app's last moment in front; marked stale after | A shared file and a writer | chosen |
| b. The extension asks the hosts | Live for LAN hosts, at most a few times an hour; nothing for hosts over the built-in Tailscale | A second client in the extension, shared tokens | rejected |
| c. The host pushes a refresh | As b, sooner | b, plus the host sender of the Live Activity's phase 2 | later, only with b |

**a. Snapshot.** The app already holds every host's inbox. It writes a
small file to the App Group and asks WidgetKit to reload. In the
foreground the reload is free. Once the app is suspended the widget
keeps the last snapshot and says how old it is. This is what the user
sees in the app a moment before, so it adds the glance and the shortcut,
not freshness.

**b. The extension connects.** The extension would need its own Rex
API client, the LAN tokens in a shared Keychain group, and a way to
reach the host. Hosts paired through the built-in Tailscale are
reachable only through the engine inside the app, which does not fit in
an extension. Reload budgets would make even LAN hosts update a few
times an hour. Two clients would disagree with each other. Rejected.

**c. Widget push.** A push only wakes the provider; the provider still
needs b to get data. It also needs the host sender and the APNs key of
[ios-live-activities.md](ios-live-activities.md) phase 2. For live state
on the Lock Screen, the Live Activity is the better place. Later, if b
is ever done.

## Design

### Target

- A widget extension target `GoRexWidgets` in `ios/project.yml`
  (`app-extension`, `com.apple.widgetkit-extension`), bundle id
  `dev.gorex.ios.widgets`, embedded in the app. It links SwiftUI,
  WidgetKit and AppIntents only: not GhosttyKit, Tailnet, FluidAudio or
  SherpaOnnx.
- One `WidgetBundle` with the Inbox and Terminal widgets; the Live
  Activity joins it later.
- An App Group `group.dev.gorex.ios` on both the app and the extension.
- Shared sources, compiled into both targets:
  - `WidgetSnapshot.swift`: the snapshot type, its reader and writer.
  - The `Program` table and `Color.attention` and `.done`, moved out of
    `ProgramTile.swift` into a file without the app's views.
  - The brand marks: `ios/Shared/Brands.xcassets`, moved out of the
    app's catalog, in both targets' resources.
  - Not `Inbox.ago`: a string made when the widget draws stays as it is
    till the next reload. The widget's times are SwiftUI's date
    references (`Text(.currentDate, format: .reference(to:))`, "2
    minutes ago"), which move by themselves.

### Snapshot

One JSON file, `<App Group>/widgets/snapshot.json`, written atomically.

```json
{
  "v": 1,
  "written": "2026-11-20T09:30:00Z",
  "hosts": [
    {
      "id": "UUID",
      "name": "alice-laptop",
      "icon": "laptopcomputer",
      "online": true,
      "items": [
        {
          "session": "s1", "block": "b3", "window": "api",
          "app": "claude", "state": "blocked", "kind": "permission",
          "progress": null, "heading": "Edit main.go?",
          "detail": "Claude wants to edit 2 files",
          "updated": "2026-11-20T09:29:41Z"
        }
      ],
      "windows": [
        { "session": "s1", "block": "b3", "title": "api", "app": "claude" }
      ]
    }
  ]
}
```

- `items` is the host's inbox as `Inbox.groups` orders it, at most 20
  per host. `windows` is every window, for the Terminal widget's picker,
  at most 100 per host. `heading` and `detail` are cut to 200
  characters.
- A host that is not online is written with `online: false`, its last
  `items` dropped (the inbox is empty then) and its `windows` kept.
- **Format.** This is a saved format another build reads
  ([irreversible.md](irreversible.md), class A): after an update, the
  new extension may read a file the old app wrote. Rules: `v` is the
  format; a reader that meets another `v`, or a file it can't decode,
  shows "Open Rex Go" and nothing else; fields are added as optional
  only. The file is a cache: losing it loses nothing the next foreground
  doesn't rebuild.
- **Writer.** A `WidgetPublisher` in the app watches the hosts' inbox
  and windows, coalesces changes to one write per second, and skips a
  write whose content (without `written`) is unchanged. After each write
  it calls `WidgetCenter.shared.reloadAllTimelines()`. On the move to
  the background it writes once more, inside a short
  `beginBackgroundTask`, so `written` is the moment the user left.
- **Demo Mode.** Demo Mode's host is written as any other, so the
  widgets show its made-up data for screenshots.
- **Protection.** The file has the app's default data protection
  (complete until first unlock), so the extension can read it on a
  locked phone. It holds titles and messages, as the inbox does; it
  holds no tokens or addresses.

### Freshness

- Each entry shows `written` as relative time ("2 min ago"), which
  moves without reloads.
- The provider returns two entries: now, and `written` + 10 minutes,
  which shows the content dimmed with "Open Rex Go to update".
- An item's time counts from its `updated` as a date reference, so
  "5 minutes ago" keeps moving.
  Whether that is right after the snapshot goes stale is the reason for
  the dimmed look.
- Reload policy `.never`: only the app asks for reloads.

### Inbox widget

Kind `Inbox`. Static configuration; all hosts. Its name in the widget
gallery is "Activity" ("动态"), as the app's button that shows the
inbox ("Show Activity").

- **Small**: the top item's program tile, tinted by state, its heading,
  and the counts below ("1 waiting · 3 working"). Nothing in the
  inbox: "Nothing needs you" with the time.
- **Medium**: up to 3 rows, as the status inbox draws them (tile,
  heading, host, state, time), then "+N more".
- **Large**: up to 7 rows, grouped by state as `Inbox.groups` does.
- **accessoryCircular** (Lock Screen): the needs-input count over the
  attention glyph; the working count when nothing waits.
- **accessoryRectangular**: the top item's heading and state.
- **accessoryInline**: the counts.
- Rows are `Link`s to their terminals; the widget's background opens
  the inbox.
- Heading and detail are `.privacySensitive()`.
- Hosts not online at `written` show as one line, "alice-laptop
  offline", under the rows.

### Terminal widget

Kind `Terminal`. `AppIntentConfiguration` with a `WindowEntity` (host,
session, block, title, program), whose `EntityQuery` reads the
snapshot's `windows`.

- **Small** and **accessoryCircular**: the program's tile, the window's
  title and host, and its state from `items` when it has one.
- **accessoryRectangular**: title, host and state.
- A window gone from the snapshot shows "Window closed"; a tap opens
  its host.
- A tap opens the terminal.

### Opening from a widget

- URLs: `rexgo://open?host=<uuid>&session=<id>&block=<id>` opens a
  terminal; `rexgo://inbox` opens the status inbox.
- `onOpenURL` in `HostListView` routes `open` to
  `Notifier.shared.opening` with `explicit: true`, as a notification
  tap does, and `inbox` to the inbox. The pairing code keeps its route.
- A host that no longer exists opens the list. A window that no longer
  exists opens its host.
- Any app can open a `rexgo://` URL. These two only open what the user
  could open from the list; they do not type, create or close anything.

### Control Center (phase 2)

- A `ControlWidgetButton` "Rex Go Inbox", which opens the inbox.
- A configurable "Open terminal" control with the same `WindowEntity`.

## Build and release

- The extension's App ID and the App Group must exist on the developer
  portal. TestFlight CI (`ios/scripts/upload`) signs at export,
  cloud-managed, with `-allowProvisioningUpdates` and the App Store
  Connect API key. **[code]** On 2026-11-20 the portal had the App ID
  `dev.gorex.ios` with one capability (In-App Purchase), no
  `dev.gorex.ios.widgets`, and so no App Group on either.
  **[checked]** with `asc bundle-ids capabilities list`. Whether the
  key may register them on its own depends on its role.
  **[assumption]**
- **An archive built with `CODE_SIGNING_ALLOWED=NO` carries no
  entitlements, and the export adds none.** **[checked]** The CI's
  archive of build 1.1 (158) is unsigned; exported again to a local
  folder with the CI's options (`destination` `export`), its app holds
  only `application-identifier`, `team-identifier`, `get-task-allow`
  and `beta-reports-active`: not the iCloud key-value store that
  `GoRex.entitlements` names. So, as the upload is now, the App Group
  is missing from the app and the extension at export: the app finds
  no container and writes nothing, and the widget shows "Open Rex Go".
  An archive of this branch built the same way embeds
  `GoRexWidgets.appex`, unsigned. Before widgets reach testers, the
  upload must sign the archive with its entitlements (or re-sign it
  with them before the export), and the App IDs must have the App
  Group; checked on an exported build, not assumed. The same holds for
  [ios-live-activities.md](ios-live-activities.md)'s `aps-environment`.
- `ios/WhatToTest.md` gets a bullet per locale in the PR that ships each
  widget.
- No Push Notifications capability: this design sends no pushes.

## Phases

| Phase | Scope | Effort |
| --- | --- | --- |
| 1 | Target, App Group, snapshot and writer, Inbox widget, `rexgo://open` and `inbox` | 3–4 days |
| 2 | Terminal widget, Control Center controls | 2 days |
| 3 | The Live Activity's phase 1 in the same extension | see its document |

Efforts are rough estimates. **[assumption]**

1. **Inbox.** Prerequisite: CI signs the extension with the App Group.
   Without it nothing ships, so check that first with an empty widget.
2. **Terminal and controls.**
3. **Live Activity.** [ios-live-activities.md](ios-live-activities.md)
   phase 1, in `GoRexWidgets`.

## Test plan

- Unit tests (`GoRexTests`): the snapshot from hosts (order, caps,
  offline hosts, cut text, Demo Mode); the writer's coalescing and
  unchanged-content skip; reading a file of another `v` or a broken
  file; URL parsing for `open`, `inbox` and the pairing code.
- Simulator on the Mac mini: each family in light and dark, with a
  demo snapshot; the stale entry; taps open the right terminal.
- On a phone, release build from TestFlight: leave the app with an
  agent working, see the widget show it; come back after 10 minutes and
  see it stale; lock the phone and see the Lock Screen redact headings;
  reload from the foreground many times in a minute and see whether the
  widget lags.

## Open questions

1. Decided: phase 1 ships to TestFlight when merged, before any Live
   Activity.
2. Decided: the Lock Screen shows headings, `.privacySensitive()`, with
   no setting of its own.
3. Does the CI's App Store Connect key have the role to register the
   extension's App ID and the App Group? An unsigned archive does not
   keep entitlements at export (Build and release): how should the
   upload sign the archive?
4. Decided: a tap from a widget does not mark done or error records
   seen. It only opens the terminal, as a notification's tap does; the
   terminal, as it shows, marks its block's records seen as it always
   does (`TerminalScreen`, `Host.see`).
