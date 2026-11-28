# Lessons

Mistakes that reached main, and the check that would have stopped each one. The author reads this before opening a PR. The reviewer reads it from `origin/main` and applies the items whose trigger matches the diff (see `REVIEW.md`).

Add an item when a PR has to fix, undo or redo an earlier one, and cite both PRs. An item must be recognizable from a diff: its trigger names paths, symbols or a kind of change, not a feeling.

Pruning: remove an item when its trigger no longer exists in the code, or when it has not fired for a long time. Merge items that duplicate each other.

Each item has a **Trigger** (what the diff shows), a **Check** (what the author and reviewer confirm) and a **Source** (the PRs).

## Merge gates

### Fix PRs
- **Trigger:** the PR title starts with `fix`.
- **Check:** the PR names the test that failed before the fix. If no test can be written, the PR says why and what was checked instead.
- **Source:** good examples #630, #641, #654, #658. Bad: #590, #593, #595, #597, #600, #616 merged with no test.

### Failing test found during a PR
- **Trigger:** the PR body or a comment reports a test that already fails on main.
- **Check:** the failure is fixed in the PR or tracked in an issue, not just noted.
- **Source:** #663 reported that `TestLostStreamWaitsForItsInput` already fails on main on Windows, and nothing followed.

### Regression test thrown away
- **Trigger:** a `fix` PR says its failing test was written but "not committed", or only run by hand.
- **Check:** the test is committed. If it cannot run in CI, the PR says why and where it runs.
- **Source:** #693 wrote an XCUITest that failed before the fix and dropped it.

### Review rounds that only find edge cases
- **Trigger:** a PR whose main behavior is already verified (tests and an end-to-end run pass) gets another fix-and-review round for findings that are low severity or not confirmed by running.
- **Check:** only confirmed findings of medium severity or higher block the merge. Low and unconfirmed ones go to a follow-up issue after the merge. Work that depends on the PR starts on its branch once its interfaces stop changing, instead of waiting for the last round. More than two fix-and-review rounds on one PR is a sign to stop and merge.
- **Source:** #722 (projects phase 2) went through five review rounds on app-side waiting edge cases after round one had verified the model, rules and end to end; phases 3–5 of #519 waited the whole time.

### Rule changes merged together
- **Trigger:** PRs that change `REVIEW.md`, `docs/lessons.md` or a workflow's gates.
- **Check:** each is reviewed against the `main` before it, and they are not merged in one batch with the PRs they would judge.
- **Source:** #678, #683, #687 and #694 merged within minutes of each other; #678 edited the rules that reviewed it.

### Two PRs that take the next design doc number
- **Trigger:** the diff adds a `docs/<PREFIX>-<NNN>-*.md` with a new number.
- **Check:** right before the merge, `git fetch origin` and list `docs/` on `origin/main`: no other doc has the number. Two PRs opened at the same time both take the next free number, and each passes its own checks until the second one merges.
- **Source:** #1079 (DSK-010 theme ambience) and #1071 (DSK-010 live settings) merged 50 minutes apart; `tools/verify` then failed on main, and this PR renumbered the later one to DSK-011.

## iOS

### Any change under `ios/**`
- **Trigger:** the diff touches `ios/**` Swift files.
- **Check:** the PR builds with `xcodebuild` against current main. `swiftc -parse` is not verification.
- **Source:** #587, #588 and #627 repaired Swift 6 build errors on main; the one #627 fixed also stopped TestFlight uploads.

### Two iOS PRs on one file
- **Trigger:** two open iOS PRs change the same Swift file.
- **Check:** after the first merges, the second is rebased and builds with `xcodebuild` before it merges.
- **Source:** #733 and #734 both changed `TerminalScreen.swift` and merged 38 seconds apart, with #734's compile check still unchecked.

### Destructive action in a menu
- **Trigger:** the diff adds an action that closes, kills or deletes (a pane, session, host, file).
- **Check:** the PR says whether it asks to confirm and whether it reaches a remote host's session; a one-tap action that ends a running program asks.
- **Source:** #734's "Close Pane" menu item ends the pane's program, also on remote hosts, with one tap and no question.

### iOS layout, animation or gesture
- **Trigger:** the diff changes iOS views, layout, animation or gesture code.
- **Check:** the PR carries a simulator screenshot or recording, and says what was not tested. "In progress" at merge time means not ready.
- **Source:** #616 merged in progress. The key bar and bottom edge were redone five times (#616, #631, #637, #643, #648); animations went back and forth (#590→#595, #593→#597, #594→#603).

### Zoom transition between different shapes
- **Trigger:** the diff adds a zoom transition (`navigationTransition(.zoom)`, `matchedTransitionSource`).
- **Check:** the source and the destination have a similar shape, such as a thumbnail and its image. A list row that opens a full screen uses the standard push.
- **Source:** #916 zoomed a terminal out of its 54-point row. A user found both the open and the close awkward, and #930 restored the standard push.

### A Live Activity or widget face
- **Trigger:** the diff changes a Live Activity's or widget's view, or the regions it is placed in.
- **Check:** the PR shows it as the system draws it: an activity on the simulator's Dynamic Island (expanded, compact, minimal) or a widget on its Home Screen. An `ImageRenderer` picture shows the view, not where the system puts it.
- **Source:** #900 checked its faces with `ImageRenderer` pictures only; on a device the expanded island cut the tile in its leading region's corner, apart from the heading (#901).

### An entitlement in a TestFlight build
- **Trigger:** a PR adds an entitlement or capability to the app or an extension.
- **Check:** the exported IPA's `codesign -d --entitlements :-` shows it (`DESTINATION=export ios/scripts/upload`), and the App ID has the capability on the portal.
- **Source:** the archive was built unsigned and signed only at export, which drops entitlements: #798's iCloud key-value store never reached TestFlight until #892 signed them in before the export.

### Removing an iOS entitlement or capability
- **Trigger:** a PR removes an iOS entitlement or capability.
- **Check:** search `docs/` for features or designs that may need it again, and say in the PR that a later PR re-adding it must also re-enable it on the App ID.
- **Source:** #581 added the iCloud key-value store entitlement, #778 removed it with space sync, and #798 adds it back for settings sync.

### A second chain that resolves the same title
- **Trigger:** the diff resolves a title, name or label that another function already resolves for the same thing (the terminal header's `windowTitle` next to `Host.title`).
- **Check:** the new chain shares the existing one's order — a window's `name` before its pane's title — or calls it, and a test pins an input where the two orders differ.
- **Source:** #639's `windowTitle` checked the pane's title before the window's name, so a renamed tab's header kept the pane's title while its row showed the name; the fix restored `w.name` first.

### A resource iOS can take back is refreshed on failure too
- **Trigger:** the diff caches or keeps a resource iOS can reclaim while suspended — a proxy listener, a socket — and refreshes it only on a lifecycle event.
- **Check:** the consumer drops or re-reads it on the failure it causes too; a resume that races the failure leaves a window in which every use fails at once.
- **Source:** #884 re-read the tailcat proxy only in `TailnetEngine.resume()`, after the socket reset's wait; a suspension's dials then reached the dead listener and failed at once, so every tailcat host showed "can't reach the server" for a stretch after each unlock.

### Keychain lookup that treats every error as missing
- **Trigger:** the diff reads iOS Keychain items (`SecItemCopyMatching`) and maps any non-`errSecSuccess` status to `nil`, or prunes saved metadata and dials before keys are synced.
- **Check:** distinguish `errSecItemNotFound` from transient/locked errors (`errSecInteractionNotAllowed`); never delete metadata or push an empty key set on a transient error, and gate dials on the key store being synced with a test of a locked Keychain cold start.
- **Source:** #1157 replaced `useSSHKey` with `SSHKeyStore`, which treated any Keychain error as missing, wiped `sshKeys` metadata, and let `tailcatProxyNow` dial SSH hosts with zero keys on cold start (fixed in this PR).

### A Core ML model loaded per call, or on `.all`
- **Trigger:** the diff calls `MLModel(contentsOf:)` inside a per-phrase path (`recognise`, a prediction), or sets `computeUnits` other than `.cpuOnly` for a dictation model.
- **Check:** the PR gives per-phrase latency for the compute units chosen, first and later phrase, measured natively on an Apple Silicon Mac (the simulator runs the CPU only), and the finish time of `DictationTests.testListeningEndsSoonAfterTheMic`. On `.all`, Core ML compiles for the GPU at each loaded model's first prediction. A synchronous `prediction` that runs over and over on a task's thread does so inside an `autoreleasepool`.
- **Source:** #1244 loaded Fun-ASR-Nano's encoder and decoder for each phrase on `.all` to fix an out-of-memory crash; each phrase then took 35 to 43 s on a Mac, a 14.5 s dictation finished 320 s after the mic, and testers saw the spinner without end. #1248 keeps the models loaded on the CPU (IOS-031, Latency). #1238's Silero VAD had no pool: 14.5 s of audio waiting took the simulator 25 s to hear.

### A UI test that expects a fresh install
- **Trigger:** an XCUITest under `ios/GoRexUITests/` checks a value the app makes once and keeps (UserDefaults, the Keychain, the app's files), such as a default name.
- **Check:** the test sets that state at launch, or accepts any value an earlier build could have left. The Mac mini's simulator keeps the app's data between runs; `xcodebuild test` installs over it. The PR shows a green `ios-test` run of the test, not only a local run on a new simulator.
- **Source:** #1027's `testDeviceNameWhileOff` waited for a `-rex-` name, but the runner's simulator kept `rexgo-xpkz` from an earlier build, and `makeName` keeps a name it finds (#1039). #1094 makes the test accept any valid name. `ComposerUITests.testNoComposerByDefault` found Composer Mode on, as its Compose tap writes the mode to the defaults and an earlier run stopped before it closed it (#1264). A `-composerMode NO` argument would hide the test's own writes, so the test turns the mode off through the UI.

### A UI change whose XCUITests did not run
- **Trigger:** the diff changes what an XCUITest under `ios/GoRexUITests/` looks for (an accessibility identifier, a bar that shows or hides), or the layout it taps by coordinates.
- **Check:** the PR shows a green `ios-test` run of every test class that covers the view, on the runner's simulator (an iPhone Duo, 466 x 678 pt). A tap at a fixed share of the screen is placed from an element's frame instead.
- **Source:** #1219 hid the key bar in Composer Mode, but `testComposerModePinsBar` still waited for its Esc key; `testSpacePanelMorph` tapped at 90 % of the screen, on the Duo's bottom bar, which does not close the space panel (#1264).

### A UI test that writes to the Keychain
- **Trigger:** an XCUITest drives a screen that calls `SecItemAdd`, or a diff puts `CODE_SIGNING_ALLOWED=NO` back into `.github/workflows/ios-test.yml`.
- **Check:** the ios-test build is signed ad hoc, so the app has its entitlements. An unsigned app gets `errSecMissingEntitlement` (-34018) for every item, and the screen shows its error alert in place of the result. The PR shows a green `ios-test` run of the test.
- **Source:** #1157's `SSHKeysUITests` never passed in CI: Generate showed "A required entitlement is not present." (#1161). #1265 signs the test build ad hoc.

## Fixes

### A remote command read by its output alone
- **Trigger:** the diff runs a command on another machine or in another process (an SSH exec such as `sshExec`, `exec.Command`) and uses its output, or takes the command as done.
- **Check:** the exit status is read: a non-zero status, a signal or a missing status is a failure, not an empty answer, and nothing is cached from it. A test runs the command failing.
- **Source:** #1136 and #1144: `sshExec` ignored the exit status, so a failed token exec cached "" and kept the host refused, and Install Key reported success on a host where nothing was installed (#1154, #1155).

### A bound applied to remote connections only
- **Trigger:** a deadline, timeout or drop that guards a remote connection but not a local one.
- **Check:** a local peer stalls the same way — its reader wedged while its control connection stays alive — so a local write can wait forever too. Guard the peer draining, not the transport's distance.
- **Source:** the attach writer's write deadline guarded remote clients only; a local window whose reads stopped stayed attached and its pane froze until the app reopened (#1076, fix here).

### A filter on some paths to the same place
- **Trigger:** the diff filters or validates a value that reaches one destination by more than one path.
- **Check:** every path to the destination gets the filter, or one choke point sees them all; a test drives each path.
- **Source:** #956 filtered a record's message and title for the Live Activity but the line the screen reads (`readLine` → `TerminalLive.state(_:)`) reached it unfiltered; it also lets a rule line through, which the card draws as bare lines (#980). The check still passed a box's edge that carries a title (──── title ────), which Devin draws over its input; the fixture drew a plain rule where the real screen does not.

### A pattern checked on its minimal form
- **Trigger:** the diff adds or uses a recognizer (a predicate, a grammar, a delimiter) whose test input is the simplest case.
- **Check:** the recognizer's test input comes from a real capture, or the variants a real producer emits are listed. `TerminalActivity.rule` knew a line of box-drawing characters but not the titled edge TUI agents draw.
- **Source:** this PR.

### A fixture's character copied by eye
- **Trigger:** the diff writes a fixture whose non-ASCII characters are typed from what a capture looks like.
- **Check:** the characters come from the capture's bytes, not its look — `capture --raw`, then the codepoints. Two glyphs identical in a terminal font can be different scalars with different behavior elsewhere: Devin's status dot is U+23FA, not the U+25CF it looks like, and iOS draws the first as an emoji where Unicode asks for the text style.
- **Source:** this PR.

### Path shared by local panes, remote hosts and iOS
- **Trigger:** the diff changes a path that local panes, remote hosts and iOS all use (stream, attach, resume, size, reconnect).
- **Check:** every consumer is listed, and all are fixed in this PR with one policy.
- **Source:** the lost terminal stream took #641, #654 and #663 in two days because #641 fixed local panes only.

### One of the two tailcat pools
- **Trigger:** the diff changes the client pool of `ios/tailnet/tailcat.go` or of `internal/rex/remote.go` (`dialAddr`, `dialTailcat`, `try`, `retire`, `drop`, `release`).
- **Check:** the other pool gets the same rule in the same PR, with a test of the same case, or the PR says why it differs.
- **Source:** #713 changed both pools. #884 gave iOS `retire` only, so the desktop still kept a client that an open connection used. After a server restart, that connection made the host unreachable from the desktop (#751).

### A deliberate reconnect goes away, it does not close normally
- **Trigger:** the diff ends a connection it means to reopen at once (reconnect, resume, network change).
- **Check:** the end's close code is not normalClosure — on Apple, `cancel(with: .goingAway)` — since URLSessionWebSocketTask sends a close frame on every task end (a bare `cancel()` included; measured on the iOS simulator, where macOS's URLSession sends none). The server keeps the client, its streams and its tickets for the reconnect grace unless the close is normal; it is what a reconnect needs kept.
- **Source:** iOS `Host.resume()` sent a normal closure on every reconnect, so each return to the foreground raced every terminal's RESUME against `cleanupClient`; the loser got `data connection refused (0x01)` and started over with a ticket and a snapshot. The first fix tried `task.cancel()` for a bare drop, which the iOS simulator proved still closes — the test caught it.

### A resolution whose fallback makes the thing another resolution tests
- **Trigger:** the diff changes a path resolver whose order includes "when it exists" (`homedir.DataDir`, `homedir.ConfigPath`, `rex.Dir`), or a fallback of one resolver that names a directory another resolver tests for.
- **Check:** list every writer of the fallback path (an install, a `MkdirAll`): none of them may create the directory an existence check elsewhere reads. A test plants each layout (fresh, legacy only, moved) and asserts that resolving and installing make no new top-level directory.
- **Source:** #1090 fell back to `~/.gorex/init.lua` before the move; installing a theme made `~/.gorex/themes`, and every process then resolved `~/.gorex` and lost the legacy settings, layout and socket (fix here).

### Value reset after a round trip
- **Trigger:** the diff clears or resets a field (revision, counter, pending flag) after a call that reads or writes remote state.
- **Check:** the reset compares with the value read before the call and applies only if it did not change. What arrived during the call is kept.
- **Source:** the outbox (#230) cleared the revision a layout event told while its read of the layout was on its way, so another client's change was not shown until the next one (#692).

### A promise or stub stands in for async work
- **Trigger:** the diff hands out a value before async work fills it in: a lazily-built object, an ifAvailable acquisition, a stub that throws until ready.
- **Check:** concurrent callers share one in-flight promise, so a loser's side effects cannot write wrong state; and a stub handed out early resolves to the real thing per call, since a caller keeps the reference it was given.
- **Source:** #1053's `Tailcat.ensure()` let two callers race `TabCoordinator.acquire()`, and the loser marked the device in use by another tab while the winner built the node — the page hung at "asking to pair" with a live tunnel. Its `transport()` also returned a stub that threw until the engine loaded; `HostConn` kept that stub, so every retry failed on it even after the engine was up.

### Fix for an unexplained failure
- **Trigger:** a `fix` PR for a failure whose cause the PR does not prove.
- **Check:** the PR adds the log line that explains the failure next time.
- **Source:** #663 had to add the logs that #641 and #654 lacked.

### Any fix: search open PRs first
- **Trigger:** a PR fixes a bug, a build error or a security finding.
- **Check:** open PRs are searched for the same file, symbol or finding before work starts.
- **Source:** #587 and #588 are the same one-line fix from two sessions; #728 duplicated #723's Windows device-path fix and was closed.

### A command that carries the space of another layout
- **Trigger:** the diff sends a layout command to a host's server (`askOn`, `sendOn`, `followHost`) with `Space` taken from the app's own state (`a.ws.ID`, a workspace of this machine's layout).
- **Check:** the space is one of the layout the command goes to. A pane of another host keeps its tab in that host's space; this machine's space answers `pane_not_found` there, which reads as a client bug.
- **Source:** #799: `pane.split` of a remote host's pane went to that host with this machine's space, and the window said "no such pane in the GoRex layout".

### Layout invariant that only the migration enforces
- **Trigger:** the diff changes a load-time repair of the layout (`migrateLayout`, `repairHostPanes`, `unnameProjectTabs`), or a command that lets a client of another version break a rule (`tab.set` with another host's leaf).
- **Check:** the rule is enforced where the layout loads, the offending command is logged, and a test loads a store that breaks it (`applyFixture`, then `s.layout = …; s.migrateLayout()`). The invariant of the flows the tests drive (`noHostLeaves`) is not enough: a client can break it and every test still passes.
- **Source:** #799: this machine's layout kept ten panes of another host for 45 minutes; the split of one failed, and no test noticed.

### Two builds on one data directory
- **Trigger:** a bug is reported from `layout.json`, `app.log`, `layout.jsonl` or `hosts-layout.json`, and the checkout has a `build/` or an installer whose app can open the same `GOREX_DIR`.
- **Check:** `ps ax -o pid,command | grep GoRex` and `lsof -p <pid> | grep 'Application Support/GoRex'` name every process writing the store, and `git describe` the binary, before the store is read as evidence. A build of before the model in the report writes that model back; close it and delete it.
- **Source:** #799: an app of v0.34, relaunched from `launchd` twice against the same store, put a host's panes back tab by tab while the new app took the layout and split them.

### One early return for several outputs
- **Trigger:** a function returns more than one derived value (a tab's name and its icon, a label and its color), and one guard returns the fallback for all of them.
- **Check:** each condition in the guard is needed by every output it skips. A test covers the case where one output has its own source and another uses the fallback, for example a named tab with a live session.
- **Source:** #793 made `savedTabFace` skip the session for a tab with a name, so a named tab lost its program's icon; #810 split the name from the icon.

### User-facing text outside the catalogs
- **Trigger:** a diff adds a user-facing string on any surface.
- **Check:** the string is in that surface's catalog — `ios/GoRex/Localizable.xcstrings`, `locales/*.json` (then `go tool mygo generate`) or `web/locales/*.json` (then `node web/i18n.mjs`) — not a literal. iOS `String(localized:)` and `LocalizedStringKey` fall back to the key, so the app builds and looks right in English while every other locale shows English too.
- **Source:** #975 shipped the agent launchers' labels raw; a Chinese sidebar read "Quick Launchers" while everything around it was translated.

## Style

### Style or size change
- **Trigger:** the diff changes sizes, spacing, colors or corner values in UI code.
- **Check:** the PR carries a screenshot, and the sizes come from a shared constant, not a literal.
- **Source:** #605, #607, #608, #611 and #614 reset the same sizes within hours.

### Sidebar row marks
- **Trigger:** the diff changes the mark at the start of a sidebar row (`projectRow`, `foldRow`, `sidebarTab`, `spaceTab`): an icon, its size or its column.
- **Check:** the PR shows a project open with two tabs next to a closed one, light and dark, on the user's real sessions or a scene like #896's, and the user sees it in the app before the merge: a mark that reads well alone can read as the same kind of row beside a tab's tile.
- **Source:** #806 drew no folder, #832 drew one, #895 filled it, and #907 took it out for a chevron, then brought it back as a neutral tile of a tab's size with the chevron at the row's end, after the user compared rendered options. #832 and #775 drew the web client's rows apart from the desktop's; the web client took the same tile, chevron, face and No Project group in its own PR, from the same design (#896, #933).

### A group with a header of its own
- **Trigger:** the diff draws a sidebar group (a project, Older, tabs with no project) with its own row builder instead of projectGroup and projectRow.
- **Check:** reuse the project's row and group; a second builder drifts from it in the tile, chevron, face and motion, and the next change has to fix both.
- **Source:** #907 drew tabs with no project as a light label (`noProjectSection`) beside the project row; #938 draws it with `projectGroup`, so that it matches Rex Go's group (#933).

### A mark that reaches past its container
- **Trigger:** the diff draws something past its rows with a negative margin or a pad (`projectGroup`'s face, `groupPad`), inside a scroll or a clip.
- **Check:** the clip's box has room for the reach on both sides, with the sidebar on the left and on the right, and a test samples the outermost pixel on each side. `sideInsets` gives the panes' side less room than the window's.
- **Source:** #907's face reached 4 px past the rows where the list had 2 px on the panes' side, so the scroll cut its corners there (#923).

### Selected paints the accent
- **Trigger:** a row uses `.Selected(` and draws its own selected look, as a pill, a swatch or a check.
- **Check:** use `.AccessSelected(`: `Selected` also fills the element with the theme's accent, which follows the desktop's accent and covers the element's own look.
- **Source:** #836: on Windows the system accent showed purple over the active tab's pill.

### Faint ink on glass
- **Trigger:** text in something a `glassPanel`, `frostPanel`, `panelMaterial` or `menuStyleOf` call gives its panel — a menu, a picker, a dialog.
- **Check:** `k.textMuted` or stronger; `k.textFaint` does not read over what the glass shows. `TestGlassInk` follows the calls from each glass surface and rejects `textFaint` in what they draw; `go run ./tools/verify` skips the root package, so run `go test . -run TestGlassInk` before pushing a UI change.
- **Source:** #991's empty menu lines used `textFaint` and the Linux root shard rejected them while the local verify stayed green.

### Glass tuned for its ink only
- **Trigger:** the diff changes `density`, `materials` or the `--glass-*` edge tokens in `web/src/glassmath.ts` or `web/index.html`.
- **Check:** the glass is checked against the backdrop as well as against its ink: over a flat backdrop as bright as its own tint (white glass over a light terminal), its edge keeps 1.4:1 (`edgeContrast` in `web/test/glass.test.ts`). The PR shows the light scheme over Gruvbox Light, iTerm2 Solarized Light and Builtin Light.
- **Source:** #1048 made the density follow only the backdrop's closeness to the ink, so white glass over a light terminal stayed thin with a 6 % outline (1.14:1) and its edge did not show; the lift in the PR after it fixed that.

## Tests and CI

### Async state in a test
- **Trigger:** a test reads async state once, or adds a sleep or retry; or a PR touches a flaky test.
- **Check:** CI does not retry failed tests. A flaky test is fixed by finding the race and waiting on state, or deleted with the reason written down. Never sleeps or retries. Run `-count 30` before merge, not after main goes red.
- **Source:** #630 found its race with `-count 30`; also #658.

### A test helper that drops what it skips
- **Trigger:** a test helper reads a connection's messages until one matches, and drops the others (`readResponse`, `readServerEvent` in `internal/rexapi/prompts_test.go`); or a flaky-test fix names a cause it did not reproduce.
- **Check:** the helper sets aside the messages it skips for the next wait (`c.pending`). The responses to two requests on one connection arrive in either order. A fix for a timeout reproduces the failing line first, with a deliberate delay if `-count` does not show it.
- **Source:** #1130 explained `TestPromptErrorIsShortened`'s timeout (#1114) by a gateway registration race. The same test and three others also had a second race: `readResponse(c, "reply")` dropped the action's response when it came first, and `readResponse(c, "run")` timed out (#1133).

### A test that expects no event
- **Trigger:** a test asserts that a step sends no event, or drains `c.Events()` with `len` before it waits for one.
- **Check:** the server coalesces a subscriber's changes and computes an event when it writes it, so an event of an earlier change can still be on its way, and carry the new state. Nothing else in the test may change the session on its own (output, the sampler, a program that changes), and the test reads up to a fence of its own (`fence` in `internal/rex/status_unix_test.go`), not a timeout.
- **Source:** `TestStatusSeenOp` drained only the events already read; the event of the record's own notify came after "seeStatus" and carried the new seen, so the next one looked like news (#1160).

### A new job on instabox
- **Trigger:** the diff adds a job, a matrix entry or a workflow that runs on `instabox-*`, or widens a trigger (types, paths) of one.
- **Check:** each instabox job that runs is one sandbox start, and the plan allows 500 a day, 200 an hour and 30 a minute. Add a step to the existing job (CI's `linux`) instead. A new instabox job names its expected starts per day in the PR.
- **Source:** CI started up to 9 boxes per run and averaged about 900 starts a day before the allowance (Nov 2026); this PR folded CI into one `linux` job.

### A pull request's base.sha
- **Trigger:** a workflow or tool diffs a pull request against `github.event.pull_request.base.sha`.
- **Check:** diff against the first parent of the checked-out merge commit (`HEAD^1`). `base.sha` does not move when main moves, so the diff also holds main's newer commits.
- **Source:** #1267's CI diffed against a stale `base.sha` and ran the tests of 10 packages that #1266 had changed on main; the follow-up PR to #1256 makes `tools/ciplan` use `HEAD^1`.

### A test that reads a file outside its package
- **Trigger:** a Go test or generator reads a repo path outside its package's directory (`os.ReadFile`, `WalkDir`, `git ls-files`), or a new `//go:embed` reaches outside the package.
- **Check:** the path is in `readsElsewhere` in `tools/ciplan/plan.go`, or the root test is in its `ruleTests`. Otherwise CI skips that test when only that file changes.
- **Source:** the PR that added `tools/ciplan`: CI runs only the packages a change reaches.

### CI retry
- **Trigger:** the diff adds a retry or rerun of a failed test or shard to a workflow or script.
- **Check:** reject it. No CI job retries a failed test. Fix the test.
- **Source:** the root-shard retry hid `TestAdoptHostTabSplit*` and `TestPaneFollowsAnotherClientsSize` (fixed in #630) and `TestPaneReturnsToItsSize`; removed in #674 (see also #563).

### Server state read after a write
- **Trigger:** a test writes to a server over `net.Pipe` or a socket and then reads the server's state.
- **Check:** the test waits on the server's state before asserting. A returned `Write` only means the bytes were read, not processed.
- **Source:** `TestVoiceKeepsTheLast` read the voice buffer between two chunks and got 321000 bytes, not 320000 (#700).

### A terminal size read after `settle`
- **Trigger:** a root-package test reads a pane's `term.Size()` as a baseline, or the diff changes when a pane's terminal takes a size (`syncSize`, `sizeWait`, `sizeSettle`).
- **Check:** the test waits for the terminal to take the size its view asks for (`waitTermSize`) before it reads the size. `settle` and `tt.Advance` run only the test's clock. The session's resize settles by the wall clock. A change to when the terminal takes a size runs, by name, every test that reads `term.Size()`, not only the tests of the new behaviour.
- **Source:** #981 made the terminal wait 150 ms of wall time for its own size. `TestFocusFollowsWindow`, `TestFocusNeedsPresence` and `TestFloatTerminalHeldWhileGliding` read the size before that time and failed on main's root shards (#982). `TestFloatMorphs` and `TestPaneShowsTheSessionAfterADrag` failed locally for the same cause.

### Test that depends on the environment
- **Trigger:** a test reads `$SHELL`, env vars, home paths or the host's tools without setting them.
- **Check:** the test pins the shell, env and paths it relies on.
- **Source:** `TestPeerListsAndMirrors` failed in Git Bash because `$SHELL` picked bash (#679).

### Caches on a self-hosted runner
- **Trigger:** the diff adds `cache: true` to `setup-go` or `actions/cache` in a job that runs on a self-hosted runner.
- **Check:** do not also save Go's caches to the Actions cache. The Mac mini holds `~/Library/Caches/go-build` (27 GB) and `~/go/pkg/mod` (4.5 GB) between runs. Jobs on instabox start on a fresh box, so they do use the Actions cache, but only pushes to main save it.
- **Source:** `setup-go` with `cache: true` in `testflight.yml` (#371) saved a 7.1 GB copy into the repository's 10 GB Actions cache and evicted the Linux test caches; #688 set `cache: false`, as `release.yml` already had (#681).

### Test helper that starts things
- **Trigger:** a test helper (`newTestApp` and the like) starts a client, server, goroutine or registers something global.
- **Check:** `t.Cleanup` closes and unregisters all of it, and one-time global setup is idempotent. Per-run time stays flat at `-count 200`.
- **Source:** `newTestApp` never closed its client and re-registered the fonts on every call; one test went from 0.2 s to about 4 s per run within one process and timed out in CI (#707, fixed in #711).

### Concurrency change tested only on Windows
- **Trigger:** the diff adds goroutines, locks, a listener restart or a shared package variable, and the PR's `-race` runs are from Windows only.
- **Check:** the new tests also pass `go test -race -count 20 -cpu 1,4,8` on Linux or macOS before the PR opens.
- **Source:** #684 passed `-race -count 5` on Windows, then Linux CI found a race in the WebTransport restart and the Mac mini found one on the `tracing` variable.

### A field set under a lock, read after the unlock
- **Trigger:** code takes values out of a locked map, unlocks, then reads their fields; or a flaky test's `-race` report names a field that a goroutine writes under a lock.
- **Check:** the fields a goroutine still writes are read under the same lock, and only the values read are used after the unlock. A `-race` failure is a production bug until its two stacks say otherwise.
- **Source:** #1124's `SetSSHHosts` read a dropped `pooledSSH.c` after `sshMu.Unlock` while its connect still wrote it; #1159's `TestSSHPendingOutlivesRecord` held such a connect open and failed under `-race` (#1257).

### Lint before the PR
- **Trigger:** any Go change.
- **Check:** the author ran `golangci-lint` at the version `ci.yml` pins, for `GOOS` linux, windows and darwin, and `gofmt -l` on the changed files. `go vet` alone is not the lint job.
- **Source:** #684 failed CI three times, one push at a time: `bodyclose` (lint), gofmt, and `TestNoInternalInit` (a rule test that `go vet` does not run); #672 needed a gofmt fix after review.

### Full suite run while the machine is in use
- **Trigger:** an author or an agent runs the full suites locally (`go test ./...`, `tools/verify -full`, the root package's tests) while someone works on the machine.
- **Check:** run locally the tests of the packages the diff changes. The full suites, the root package's shards and the Windows job run in CI. A run that makes the machine lag is stopped and reported as not run locally, not left to finish.
- **Source:** the maintenance audit of 0.38.0 ran three suites at once and made the host lag until the user stopped it. CI then passed every shard, and the one local failure (`TestProjectSetHome`) passed alone in 0.63 s.

### Shell pipeline with an early exit
- **Trigger:** a script with `set -o pipefail` pipes into `head`, `grep -q`, `awk '... exit'` or anything else that stops reading early.
- **Check:** the reader consumes all input, or the producer writes to a file first. Otherwise the producer gets SIGPIPE and the step fails.
- **Source:** `draft-release.sh` piped `tr` into an `awk` that exited early; it passed in earlier releases, then failed the 0.36.0 release on timing (#699).

### Release workflow change
- **Trigger:** the diff touches `release.yml` or `.github/scripts/*release*` / `checksums.sh`.
- **Check:** the PR shows a `--dry-run` or a run on a test tag, and the job's runner matches the release target (a Windows-only release must not wait for the Mac mini).
- **Source:** #694 merged without a release run; the first one (0.36.0) failed in `draft` (#699), and its new `checksums` job runs on the Mac mini for every target.

### Release tag made at publish time
- **Trigger:** the diff touches the `gh release create` of `.github/scripts/draft-release.sh`, or a release is published while PRs merge.
- **Check:** the draft is created with `--target` the commit the run builds, and after publishing `git rev-parse v<version>^{commit}` equals the release run's `headSha`.
- **Source:** the draft had no target, so GitHub tagged main's head at publish: v0.52.0's tag holds #1017 and v0.53.0's holds #1018, neither in its apps. Those tags stay; the script now passes `--target`.

### Release run that cancels another
- **Trigger:** the diff touches `concurrency` or `permissions` in `release.yml` or `testflight.yml`, or the cancels and deletes of `.github/scripts/release-guard.sh` or `testflight-guard.sh`.
- **Check:** only a full release dispatch (`target: all`) cancels a run in flight; a partial one does not, though a newer dispatch can still replace it while it waits (one pending run per group). The group is per ref, so a branch dispatch cancels nothing of main's. Only the guard job holds `actions: write`. A cancel counts as the group's only by GitHub's "higher priority waiting request" annotation, or, for a run with no jobs, a cancel within 10 s of a newer run's start. A draft is deleted only if it is a draft, of another version, targets the cancelled run's commit, and that run has stopped. The PR shows a `DRY_RUN=1` run.
- **Source:** #1025 cancelled any run in flight on any dispatch, so a `web` or `windows` dispatch would cut the other platforms out of a full release; it left a superseded version's draft behind, named a run cancelled by hand as superseded, and failed a release whose commit main had left although #1023 had pinned its tag. #1031 fixes all four.

### New CI gate
- **Trigger:** the diff turns on `-race`, adds a shard, or adds a gate to `ci.yml`.
- **Check:** the gate was run with `-count 10` on every platform it gates. One clean run does not show a test is free of races.
- **Source:** #563.

### Process kill on a host that serves
- **Trigger:** the diff adds a `taskkill`, `Stop-Process`, `pkill` or `Process.Kill` to a script, a skill, a doc or a tool an agent may run on a host where a GoRex server also serves, or a test helper that stops an earlier instance.
- **Check:** the kill names only the pids the same run started and recorded at launch; it never enumerates by image name (`tasklist /FI "IMAGENAME eq"`, `Get-Process -Name`, `pgrep -f`) or by port owner. A stale instance on such a host is reported, not killed. Windows image-name filters ignore case, so `gorex.exe` also matches the installed `GoRex.exe`.
- **Source:** an agent recycled its UI-test build with `for p in $(tasklist ... IMAGENAME eq gorex.exe ...); do taskkill /PID $p /F; done`; the enumeration held the production GUI and server, which died with no stop event, took every session's shells with them, and left remote access down for an hour (2026-11-25).

## Design before code

### Gesture or interaction feature
- **Trigger:** the diff adds or changes a gesture or interaction (pane switching, drag, swipe) and no design doc settles it.
- **Check:** the design doc is settled first.
- **Source:** pane switching took #639, #653 and #665 in three days; #653 dropped what #639 had just added.

### Layout model
- **Trigger:** the diff moves a layout between inset and overlay.
- **Check:** one layout model is chosen, and changed in one PR.
- **Source:** #637 and #648 reversed each other.

### Feature ships
- **Trigger:** the PR completes a feature that has a design doc under `docs/`.
- **Check:** the design doc's status line is updated in the same PR.
- **Source:** standing rule.

### A theme feature with knobs
- **Trigger:** the diff adds a manifest key under `internal/theme` that GoRex draws with its own code: a layer kind, a shape, a motion or a placement that the theme only tunes with numbers, colors or enums.
- **Check:** the look comes from the theme pack (its assets and its own shader, DSK-012), not from GoRex. A fixed kind with knobs is hard-coding (`CLAUDE.md`, Architecture decisions; DSK-008 Non-goals). If the feature cannot be that open, it is not built.
- **Source:** #1083 and #1099 built glows, a watermark, swimming sprites and a brand row as fixed layer kinds with knobs (DSK-010); the user ruled that a look with knobs is hard-coding (#1117), and #1120 removed them.

### A bulk close with no undo
- **Trigger:** the diff adds a menu item or command that ends several tabs, panes or sessions at once.
- **Check:** the owner asked for it by name, and every client gets the same item. An ended session has no undo, so a confirm before it is the only guard.
- **Source:** #971 added Close Other Tabs on the desktop, the web and iOS. The owner had it removed from all three after a review of how GoRex confirms destructive actions (#1095).

## Security

### Fork PR touching `.github/`
- **Trigger:** the diff touches `.github/` and comes from a fork.
- **Check:** do not approve the workflow run until the whole diff is read. `test-windows` in `ci.yml` runs PR code on a self-hosted runner that also builds releases and keeps its Go caches; the `if:` on the head repo is not a boundary, and the approval for outside contributors is the only one.
- **Source:** standing rule.

### Downloaded tool
- **Trigger:** a script or workflow downloads a tool or toolchain.
- **Check:** a checksum is pinned and verified before the tool runs. Reject `curl | tar` of a toolchain.
- **Source:** standing rule.

### New network listener
- **Trigger:** the diff adds a `net.Listen` or other listener.
- **Check:** a reviewed auth model exists before merge. An IP-range allow list with no token is not auth on a shared Wi-Fi.
- **Source:** standing rule.

### Agent config
- **Trigger:** the diff touches `.omp/`, `.claude/` or `.agents/`.
- **Check:** config does not point at third-party endpoints without a note saying why.
- **Source:** standing rule.

### Agent hook runs repo code
- **Trigger:** the diff adds or changes hooks in `.claude/settings.json` or a tool they call.
- **Check:** the PR lists what the hook executes and from which checkout. A branch not from `si-mods` gets no tests or npm scripts run by the hook.
- **Source:** #687's push hook runs the pushed checkout's `go test`, npm scripts and lint config.

### Hook command written for a shell the agent does not use
- **Trigger:** the diff writes a hook `command` string or injected context that names a command (e.g. `hookCommand` and `sessionstart.go` in `internal/cli/`).
- **Check:** the commands run under the shell the agent actually spawns — on Windows some agents use `sh`, not `cmd`. A `cmd /c` command under `sh -c` fails silently: it parses but runs nothing. The same shell decides how the CLI name resolves: in `sh`, `gorex` does not match the `gorex.cmd` shim.
- **Source:** #824 wrote Devin's Windows hook as `cmd /c "gorex …"`, which Devin's `sh -c` runner never executed, and its injected context said `gorex rename`, which devin's shell cannot resolve; verified end-to-end, fixed in the follow-up PR.

### Upgrade turns on a listener
- **Trigger:** migration or upgrade code that enables a node, a listener or a network feature.
- **Check:** the user opts in, or the release notes say it.
- **Source:** #573 turns the built-in Tailscale node on once on upgrade, which serves the API on the tailnet.

### Link whose target differs from its text
- **Trigger:** a client opens or downloads something from a link in terminal output (OSC 8, a tapped path) that reads host data.
- **Check:** when the target differs from the visible text, the user sees the real target and confirms before anything is fetched.
- **Source:** #721 let an OSC 8 link labelled `chart.png` download `~/.ssh/id_ed25519` into Quick Look with one tap.

### Untrusted path to the host's filesystem
- **Trigger:** a path from terminal output, a link or a remote caller reaches `os.Stat`, `os.Open` or a directory read on the host.
- **Check:** device and pipe paths (`\\.\`, `\\?\`, `//./`, `\??\`, a redirector such as `\\;LanmanRedirector`) are refused, with a test for each form; opening one waits on it forever on Windows. A plain drive or share path is a place the user may name, and `plainPath` takes it. A path a client sends absolute goes through `rex.VolumePath` first: `list_dir`, `project.set_home` and `file.put` did not, so `\\.\pipe\x` from a paired device held a server goroutine.
- **Source:** #721 passed `//host/share/x`; the device-path guard from #410 never matched because its prefixes lost a backslash. The review of 0.38.0 found the three methods above still unchecked.

### Public repo
- **Trigger:** every PR, and every screenshot or log added to one.
- **Check:** the repo is public. The diff has no real names, home paths, host names, IPs, Team or issuer IDs. Screenshots use made-up data.
- **Source:** standing rule.

## Size

### Growth of `state.go`
- **Trigger:** the diff adds lines to `state.go`.
- **Check:** `state.go` passed 4000 lines, and the audit split it (#763): floats, the browser pane and `App` with its posted changes now have `float.go`, `browser.go` and `app.go`. New behavior goes in a file of its own concern.
- **Source:** standing rule.

### Disabled instead of removed
- **Trigger:** a feature is turned off "for now" (`isEnabled = false`, a flag, an early return) and its code stays.
- **Check:** an issue is linked with what must happen before it comes back or is deleted.
- **Source:** #677 disabled the pane swipe from #665 and #667 within hours; it did this right by linking #676, which lists the open problems and the steps before re-enabling.

### "Same rule as X"
- **Trigger:** code or a comment says it applies the same rule as another place.
- **Check:** both places call one shared function, and both have a test.
- **Source:** #717's `dir.list` said it used the same device-path rule as `openToSend`, but kept its own copy; `openToSend`'s copy had lost a backslash since #410 (both now share one helper, #723). The Ghostty theme parser and the server's theme parser both read a hex color, but the server took only six digits, so a `#fff` theme worked in the app and vanished on a host. #556's hotspot share copied "how the app reaches a host" as `config.transport ?? .system` while the app itself uses `reach`, so every tailcat host and every tailnet host with the engine off failed through the share with a 502 (both now share `reach` via `ShareHosts.json`).

### Comment names a file
- **Trigger:** a comment or doc points at a `.md` file or an issue.
- **Check:** the file exists in the same tree; a missing issue link says what it would have said. `rules_test.go` enforced rules "of `TECH_DEBT.md`" from #617, a file the repository never had, so the reason for the rule was unrecoverable.
- **Source:** #617.

### Unbounded work from a remote request
- **Trigger:** a remote request reads a directory, a file or a list whose size the caller controls.
- **Check:** the read is batched and capped before it is sorted or held in memory.
- **Source:** #717 reads a whole directory with `os.ReadDir` before keeping 2000 entries.

### Removing a feature
- **Trigger:** the PR deletes a feature or a cleanup PR removes unused code.
- **Check:** dead code goes with the feature. `go run ./tools/verify -audit` runs `deadcode` for linux, windows and darwin; code unused on one OS may be used on another.
- **Source:** standing rule.

### A rule with no gate
- **Trigger:** `docs/lessons.md`, `REVIEW.md` or a comment states a rule that no test, lint or workflow runs.
- **Check:** the rule names the command that runs it, and that command runs on every PR or on a schedule. A rule nobody runs is a wish: the dead wrappers `newTabAfter` and `newTabAfterRun` (#722) outlived their callers while this file told authors to run `staticcheck -checks U1000` (#668) by hand.
- **Source:** #668, #722.

### A check that cannot fail
- **Trigger:** a PR adds a gate, script or tool that decides by an exit code.
- **Check:** the PR shows it failing on a known-bad input before the merge. `deadcode` prints its findings and exits 0; a caller that trusted the exit code would pass every repository.
- **Source:** `tools/verify -audit` (deadcode's exit code).

### Dev-only switch in the release
- **Trigger:** code reads an environment variable or flag that no doc, help text or test names.
- **Check:** delete it, or document it and cover it with a test. `GOREX_REXAPI_PROXY` turned the gateway into a recording relay to any URL, with no doc and no test, compiled into every release.
- **Source:** `internal/rexapi/traceproxy.go` (removed).

### Terminal behavior on Windows
- **Trigger:** a feature depends on what a terminal does with an escape sequence, and the tests that exercise it run on unix.
- **Check:** ConPTY is not a byte pipe: its input turns text into input events and its output rewrites sequences, so a query's answer or a report may never cross it. Windows needs its own end-to-end test of the exchange, not a port of the unix assumption.
- **Source:** OSC 7501 reports worked on unix and macOS while the inbox ConPTY dropped the server's answer on Windows; shipping the packaged ConPTY fixed it (#827).

### A look on one platform
- **Trigger:** a PR adds a state or surface that shows on the desktop but not on iOS, or the reverse.
- **Check:** where the data is the same, the two platforms show the same thing; a divergence needs the design doc to name it as decided, or it drifts.
- **Source:** the sidebar's empty-projects card (#991) existed only on the desktop; #1018 drops it so an empty host shows just its header, as iOS does.

### Same-name asset upload race
- **Trigger:** two release jobs upload a same-named asset (`SHA256SUMS`) and one loses with HTTP 422.
- **Check:** uploads that dedupe by name still race when both jobs write at once; `gh release upload --clobber` does not cure it (it deletes, then creates, so two jobs still collide), and job `concurrency` would serialise the builds and make `windows` wait for the Mac mini; mygo's `-upload` retries a `SHA256SUMS` upload that fails with "already exists", after it merges the one the other job left.
- **Source:** the v0.63.0 release's `unix` job failed on `SHA256SUMS` already existing after `windows` won the race; a `--failed` rerun recovered it because the upload skips assets by name.

### Dictation that sends
- **Trigger:** a voice or dictation path in the composer (`VoiceCapsule`, `dictationInsert`) calls the send path.
- **Check:** dictation only fills the field; the user's own tap sends. Recognition can be wrong, and a sent message cannot be recalled.
- **Source:** #1166 added a one-tap Send to the inline voice view; this PR removes it for a Done button.
