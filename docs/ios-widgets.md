# Rex Go: widgets

Status: phases 1 and 3 built (#892, #900, #901). Phase 2 not built.

Phase 1 is built. It holds the `GoRexWidgets` target, the snapshot and its writer, the Inbox widget, and `rexgo://open` and `inbox`. The export signs it with the App Group (see Build and release). Phase 3 is built too. The Live Activity of [ios-live-activities.md](ios-live-activities.md) runs in `GoRexWidgets` (`TerminalActivityView.swift`). Phase 2 is not built. It holds the Terminal widget and the Control Center controls.

## Problem

To see whether an agent waits for the user, or to get back to a terminal, the user opens Rex Go. The user waits for it to connect and finds the window in the list. The Home Screen, the Lock Screen, StandBy and Control Center can show that state, or open that terminal, without the list in between. Rex Go puts nothing there.

### Evidence labels

Each statement has a label. **[code]** means that the statement was read in this repository. **[doc]** means that it comes from the documentation of Apple or from a WWDC session, which is linked. **[assumption]** means that nobody checked it. Settle it before the phase that needs it, or in that phase. **[checked]** means that someone tried it on the Mac mini and saw that it holds.

### What there is to show

This is the state before phase 1. Phase 1 changed what is under Design.

- `Host.inbox` (`ios/GoRex/Rex/Inbox.swift`) lists each window whose program needs the user or works. It lists the most urgent OSC 7501 record of each window. `Inbox.groups` orders the windows blocked, error, done, working. Done and error records that the user saw drop out (`mostUrgent`, `seen`). The list is empty while a host is not online. **[code]**
- `InboxItem.heading` and `detail` build the two lines of a row. `Inbox.ago` builds the time. **[code]**
- `Program` (`ios/GoRex/Views/ProgramTile.swift`) maps a program id to its name, glyph and tile colors. The brand marks are in `Assets.xcassets/Brands`. **[code]**
- The hosts are `HostConfig`s, saved as `hosts` in `UserDefaults`. The Keychain holds the LAN tokens (`KeychainLANTokens`). The state of the built-in Tailscale is under Application Support. **[code]**
- A notification tap sets `Notifier.shared.opening` to a `TerminalTarget` (host, session, block, title, `explicit: true`). `HostListView` opens it. The URL scheme of the app is `rexgo`. The app uses it for the tailcat pairing code and for the callback of the Tailscale login. `onOpenURL` handles only the pairing code. **[code]**
- Rex Go has no App Group and no background mode. Its only entitlement is the iCloud key-value store (`GoRex.entitlements`). **[code]**

### WidgetKit constraints

- A widget runs in its own extension process. It renders a timeline of entries that its provider returns. It does not run while it is shown. **[doc]**
- Reloads have a budget. Reloads that the app asks for while it is in the foreground do not count against the daily limit of the widget. **[doc]** The source is the `TimelineProvider` documentation, quoted in [this forum thread](https://developer.apple.com/forums/thread/652946).
  - Developers report that WidgetKit drops reloads that the app asks for in quick succession, or from the background. **[assumption]** until someone tests it on a release build.
- From iOS 26, a server can refresh widgets by push. A `WidgetPushHandler` hands over a push token. The request has `apns-push-type: widgets`, the topic `<bundle id>.push-type.widgets`, and `content-changed: true` in `aps`. A push carries no data. It only makes the provider run again. Pushes have a budget and the system delivers them opportunistically. **[doc]** See [What's new in widgets, WWDC25](https://developer.apple.com/videos/play/wwdc2025/278/) and [WidgetPushHandler](https://developer.apple.com/documentation/widgetkit/widgetpushhandler).
- An extension reads the data of the app only through a shared container. An App Group (`com.apple.security.application-groups`) holds files and `UserDefaults(suiteName:)`. A Keychain access group holds secrets. **[doc]**
- A widget extension has a small memory limit, far below what the built-in Tailscale engine needs. **[assumption]** Apple does not publish the exact limit.
- The system redacts text marked `.privacySensitive()` on the Lock Screen and in StandBy while the device is locked. **[doc]**
- Configurable widgets use `AppIntentConfiguration` with an `AppEntity` and its `EntityQuery`. Control Center controls are `ControlWidget`s (iOS 18). The deployment target of Rex Go is iOS 26, so all of them are available. **[doc]** **[code]** for the target.

## Goal

- An **Inbox** widget shows what the status inbox shows (blocked, error, done, working). It shows the most urgent first, on the Home Screen and the Lock Screen.
- A **Terminal** widget shows one window that the user picks, with the tile and the state of its program. A tap opens that terminal.
- A tap on any row opens its terminal, as a notification tap does.
- A widget never shows state older than it says. Each widget shows when its data was taken. It turns plainly stale after a while.
- The extension is the one that the Live Activity uses ([ios-live-activities.md](ios-live-activities.md)). The target, the signing and the shared code are done once.

## Non-goals

- **Live state while Rex Go is suspended.** A widget shows what the app last saw. Live state on the Lock Screen is the job of the Live Activity. Its phase 2 (the host pushes) makes it live. See Alternatives.
- **Acting from a widget.** No buttons answer an agent, approve a permission or type into a terminal. Each button would need the extension to reach the host. Do this later, if at all.
- **The extension connecting to hosts.** See Alternatives, b.
- **macOS and watchOS widgets.** Rex Go is an iPhone and iPad app.
- **Changing what the inbox ranks or counts.** The widget shows `Host.inbox` as it is.

## Design

### Target

- A widget extension target `GoRexWidgets` is in `ios/project.yml` (`app-extension`, `com.apple.widgetkit-extension`). Its bundle id is `dev.gorex.ios.widgets`. The app embeds it. It links SwiftUI, WidgetKit and, from phase 2, AppIntents only. It does not link GhosttyKit, Tailnet, FluidAudio or SherpaOnnx.
- One `WidgetBundle` holds the Inbox widget in phase 1 and the Terminal widget in phase 2. The Live Activity joined the bundle in phase 3.
- An App Group `group.dev.gorex.ios` is on both the app and the extension.
- Shared sources are compiled into both targets:
  - `WidgetSnapshot.swift` holds the snapshot type, its reader and its writer.
  - The `Program` table and `Color.attention` and `.done` moved out of `ProgramTile.swift` into `ios/Shared/Program.swift`, without the views of the app. The tile itself and the progress ring moved into `ios/Shared/Tile.swift`.
  - The brand marks moved out of the catalog of the app into `ios/Shared/Brands.xcassets`. They are in the resources of both targets.
  - `Inbox.ago` is not shared. A string made when the widget draws stays as it is until the next reload. The times of the widget are the date references of SwiftUI (`Text(.currentDate, format: .reference(to:))`, "2 minutes ago"). They move by themselves.

### Snapshot

One JSON file, `<App Group>/widgets/snapshot.json`, holds the snapshot. The app writes it atomically.

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
      ]
    }
  ]
}
```

- `items` is the inbox of the host, in the order of `Inbox.groups`. It has at most 20 items for each host. `heading` and `detail` are cut to 200 characters.
- `windows` comes with phase 2, for the picker of the Terminal widget. It lists every window, at most 100 for each host. It is an optional field. Phase 1 does not write it. Window titles change often, and each change would be a write and a reload.
- A host that connects (`Host.Status.connecting`: at launch, and back in the foreground) is written as it was last written, from the file at launch. It likely still shows what it showed. A quick leave would otherwise write its inbox away. A host that failed (offline, retrying) is written with `online: false` and no items. A host that connects with nothing written of it gets the same entry. Its inbox is empty then.
- **Format.** This is a saved format that another build reads ([irreversible.md](irreversible.md), class A). After an update, the new extension may read a file that the old app wrote. These rules apply:
  - `v` is the format.
  - A reader that meets another `v`, or a file that it cannot decode, shows "Open Rex Go" and nothing else.
  - Fields are added as optional only.
  - The file is a cache. If the app loses it, nothing is lost that the next foreground does not rebuild.
- **Writer.** A `WidgetPublisher` in the app watches the inbox of the hosts. It coalesces changes to one write for each second. It skips a write whose content (without `written`) is unchanged. After each write it calls `WidgetCenter.shared.reloadAllTimelines()`. When the app moves to the background, it writes once more, inside a short `beginBackgroundTask`. Then `written` is the moment the user left.
- **Demo Mode.** The app writes the host of Demo Mode as any other host. The widgets show its made-up data for screenshots.
- **Protection.** The file has the default data protection of the app (complete until first unlock). The extension can read it on a locked phone. It holds titles and messages, as the inbox does. It holds no tokens or addresses.

### Freshness

- Each entry of the Home Screen families shows `written` as relative time ("Updated 2 minutes ago"). The time moves without reloads. The Lock Screen families have no room for it. They show only the "Open Rex Go to update" of the stale entry (rectangular, inline) or they dim (circular).
- The provider returns two entries. The first is for now. The second is for `written` + 10 minutes. It shows the content dimmed with "Open Rex Go to update".
- The time of an item counts from its `updated` as a date reference, so "5 minutes ago" keeps moving. Whether that is right after the snapshot goes stale is the reason for the dimmed look.
- The reload policy is `.never`. Only the app asks for reloads.

### Inbox widget

The kind is `Inbox`. The configuration is static and covers all hosts. Its name in the widget gallery is "Activity" ("动态"). This is the name of the button of the app that shows the inbox ("Show Activity").

- **Small** shows the program tile of the top item with the dot of its state. It shows the name of the state in the color of that dot, then the heading. The counts are below ("1 waiting · 3 working"). With nothing in the inbox, it shows "Nothing needs you" with the time.
- **Medium** shows up to 3 rows, as the status inbox draws them (tile, heading, host, state, time), then "+N more".
- **Large** shows up to 7 rows, grouped by state as `Inbox.groups` does. On a smaller phone, or with long text, it shows as many rows as fit (`ViewThatFits`), then "+N more".
- **accessoryCircular** (Lock Screen) shows the needs-input count over the attention glyph. It shows the working count when nothing waits.
- **accessoryRectangular** shows the heading and the state of the top item.
- **accessoryInline** shows the counts.
- Rows are `Link`s to their terminals. The background of the widget opens the inbox.
- Heading, detail and host names are `.privacySensitive()`.
- Hosts that were not online at `written` show in one line under the rows. The line names the first host, then how many more ("bob-desktop · Offline +1").

### Terminal widget (phase 2, not built)

The kind is `Terminal`. It uses `AppIntentConfiguration` with a `WindowEntity` (host, session, block, title, program). The `EntityQuery` of the entity reads the `windows` of the snapshot.

- **Small** and **accessoryCircular** show the tile of the program, the title and host of the window, and its state from `items` when it has one.
- **accessoryRectangular** shows the title, the host and the state.
- A window that is gone from the snapshot shows "Window closed". A tap opens its host.
- A tap opens the terminal.

### Opening from a widget

- `rexgo://open?host=<uuid>&session=<id>&block=<id>` opens a terminal. `rexgo://inbox` opens the status inbox.
- `onOpenURL` in `HostListView` routes `open` to `Notifier.shared.opening` with `explicit: true`, as a notification tap does. It routes `inbox` to the inbox. The pairing code keeps its route.
- A host that no longer exists opens the list. A window that no longer exists opens as the tap of a notification for it does. The target of the URL goes to `Notifier.shared.opening` unchecked.
- Any app can open a `rexgo://` URL. These two URLs only open what the user could open from the list. They do not type, create or close anything.

### Control Center (phase 2, not built)

- A `ControlWidgetButton` "Rex Go Inbox" opens the inbox.
- A configurable "Open terminal" control uses the same `WindowEntity`.

### Build and release

- The App Group `group.dev.gorex.ios` is on the developer portal, on the App IDs `dev.gorex.ios` (with iCloud) and `dev.gorex.ios.widgets`. **[checked]** `asc bundle-ids capabilities list` shows `APP_GROUPS` on both and `ICLOUD` on the App ID of the app. The API does not list the groups of a capability. The profiles that the export received name `group.dev.gorex.ios` for both.
- TestFlight CI (`ios/scripts/upload`) archives unsigned (`CODE_SIGNING_ALLOWED=NO`), because the Mac holds no signing identity. It signs at export, cloud-managed, with `-allowProvisioningUpdates` and the App Store Connect API key. **[code]**
- **An unsigned archive carries no entitlements, and the export adds none.** **[checked]** The CI exported its archive of build 1.1 (158) again to a local folder. The result held only `application-identifier`, `team-identifier`, `get-task-allow` and `beta-reports-active`. It did not hold the iCloud key-value store that `GoRex.entitlements` names. So builds up to 158 lack it, and would have lacked the App Group.
- So `upload` signs each bundle of the archive ad hoc with its entitlements (`$(TeamIdentifierPrefix)` and `$(CFBundleIdentifier)` filled in) before the export. The export signs them again for the distribution and keeps them. **[checked]** `DESTINATION=export BUILD=1 scripts/upload` on this branch exported an IPA, and nothing was uploaded. The cloud-managed Apple Distribution certificate signed it. Its `GoRex.app` holds `com.apple.security.application-groups` `[group.dev.gorex.ios]` and `com.apple.developer.ubiquity-kvstore-identifier`. Its `GoRexWidgets.appex` holds the App Group. `codesign --verify --deep --strict` passes. The same step carries the `aps-environment` of [ios-live-activities.md](ios-live-activities.md).
- `ios/WhatToTest.md` gets a bullet for each locale in the PR that ships each widget.
- The design has no Push Notifications capability, because it sends no pushes.

## Alternatives

These are the options for where the data of a widget comes from.

| | What the user sees | Work | Outcome |
| --- | --- | --- | --- |
| a. The app writes a snapshot | State as of the last moment the app was in front. The widget is marked stale after that | A shared file and a writer | chosen |
| b. The extension asks the hosts | Live for LAN hosts, at most a few times an hour. Nothing for hosts over the built-in Tailscale | A second client in the extension, shared tokens | rejected |
| c. The host pushes a refresh | As b, sooner | b, plus the host sender of the Live Activity's phase 2 | later, only with b |

**a. Snapshot.** The app already holds the inbox of every host. It writes a small file to the App Group and asks WidgetKit to reload. In the foreground the reload is free. Once the app is suspended, the widget keeps the last snapshot and says how old it is. The snapshot is what the user saw in the app a moment before. So it adds the glance and the shortcut, not freshness.

**b. The extension connects.** The extension would need its own Rex API client, the LAN tokens in a shared Keychain group, and a way to reach the host. Hosts paired through the built-in Tailscale are reachable only through the engine inside the app. That engine does not fit in an extension. Reload budgets would make even LAN hosts update a few times an hour. Two clients would disagree with each other. Rejected.

**c. Widget push.** A push only wakes the provider. The provider still needs b to get data. It also needs the host sender and the APNs key of phase 2 of [ios-live-activities.md](ios-live-activities.md). For live state on the Lock Screen, the Live Activity is the better place. Do c later, if someone ever does b.

## Tests

- Unit tests (`GoRexTests`) check these items:
  - the snapshot from hosts (order, caps, offline hosts, cut text, Demo Mode).
  - the coalescing of the writer, and its skip of unchanged content.
  - reading a file of another `v`, and reading a broken file.
  - URL parsing for `open`, `inbox` and the pairing code.
- On the simulator on the Mac mini, check each family in light and dark with a demo snapshot. Check the stale entry. Check that taps open the right terminal.
- On a phone, with a release build from TestFlight:
  - Leave the app with an agent working, and see that the widget shows it.
  - Come back after 10 minutes, and see that the widget is stale.
  - Lock the phone, and see that the Lock Screen redacts headings.
  - Reload from the foreground many times in a minute, and see whether the widget lags.

## Phases

| Phase | Scope | Effort |
| --- | --- | --- |
| 1 | Target, App Group, snapshot and writer, Inbox widget, `rexgo://open` and `inbox` | 3–4 days |
| 2 | Terminal widget, Control Center controls | 2 days |
| 3 | The Live Activity's phase 1 in the same extension | see its document |

Efforts are rough estimates. **[assumption]**

1. **Inbox.** Built (#892). The prerequisite is that CI signs the extension with the App Group. Without it nothing ships, so check that first with an empty widget.
2. **Terminal and controls.** Not built.
3. **Live Activity.** Phase 1 of [ios-live-activities.md](ios-live-activities.md), in `GoRexWidgets`. Built (#900, #901).

## Open questions

1. Decided: phase 1 ships to TestFlight when merged, before any Live Activity.
2. Decided: the Lock Screen shows headings, `.privacySensitive()`, with no setting of its own.
3. Settled: the App IDs and the App Group are on the portal. The upload signs the archive ad hoc with its entitlements before the export (Build and release).
4. Decided: a tap from a widget does not mark done or error records seen. It only opens the terminal, as the tap of a notification does. The terminal marks the records of its block seen as it shows them, as it always does (`TerminalScreen`, `Host.see`).
