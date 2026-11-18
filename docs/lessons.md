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

## Fixes

### Path shared by local panes, remote hosts and iOS
- **Trigger:** the diff changes a path that local panes, remote hosts and iOS all use (stream, attach, resume, size, reconnect).
- **Check:** every consumer is listed, and all are fixed in this PR with one policy.
- **Source:** the lost terminal stream took #641, #654 and #663 in two days because #641 fixed local panes only.

### Value reset after a round trip
- **Trigger:** the diff clears or resets a field (revision, counter, pending flag) after a call that reads or writes remote state.
- **Check:** the reset compares with the value read before the call and applies only if it did not change. What arrived during the call is kept.
- **Source:** the outbox (#230) cleared the revision a layout event told while its read of the layout was on its way, so another client's change was not shown until the next one (#692).

### Fix for an unexplained failure
- **Trigger:** a `fix` PR for a failure whose cause the PR does not prove.
- **Check:** the PR adds the log line that explains the failure next time.
- **Source:** #663 had to add the logs that #641 and #654 lacked.

### Any fix: search open PRs first
- **Trigger:** a PR fixes a bug, a build error or a security finding.
- **Check:** open PRs are searched for the same file, symbol or finding before work starts.
- **Source:** #587 and #588 are the same one-line fix from two sessions; #728 duplicated #723's Windows device-path fix and was closed.

## Style

### Style or size change
- **Trigger:** the diff changes sizes, spacing, colors or corner values in UI code.
- **Check:** the PR carries a screenshot, and the sizes come from a shared constant, not a literal.
- **Source:** #605, #607, #608, #611 and #614 reset the same sizes within hours.

## Tests and CI

### Async state in a test
- **Trigger:** a test reads async state once, or adds a sleep or retry; or a PR touches a flaky test.
- **Check:** CI does not retry failed tests. A flaky test is fixed by finding the race and waiting on state, or deleted with the reason written down. Never sleeps or retries. Run `-count 30` before merge, not after main goes red.
- **Source:** #630 found its race with `-count 30`; also #658.

### CI retry
- **Trigger:** the diff adds a retry or rerun of a failed test or shard to a workflow or script.
- **Check:** reject it. No CI job retries a failed test. Fix the test.
- **Source:** the root-shard retry hid `TestAdoptHostTabSplit*` and `TestPaneFollowsAnotherClientsSize` (fixed in #630) and `TestPaneReturnsToItsSize`; removed in #674 (see also #563).

### Server state read after a write
- **Trigger:** a test writes to a server over `net.Pipe` or a socket and then reads the server's state.
- **Check:** the test waits on the server's state before asserting. A returned `Write` only means the bytes were read, not processed.
- **Source:** `TestVoiceKeepsTheLast` read the voice buffer between two chunks and got 321000 bytes, not 320000 (#700).

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

### Lint before the PR
- **Trigger:** any Go change.
- **Check:** the author ran `golangci-lint` at the version `ci.yml` pins, for `GOOS` linux, windows and darwin, and `gofmt -l` on the changed files. `go vet` alone is not the lint job.
- **Source:** #684 failed CI three times, one push at a time: `bodyclose` (lint), gofmt, and `TestNoInternalInit` (a rule test that `go vet` does not run); #672 needed a gofmt fix after review.

### Shell pipeline with an early exit
- **Trigger:** a script with `set -o pipefail` pipes into `head`, `grep -q`, `awk '... exit'` or anything else that stops reading early.
- **Check:** the reader consumes all input, or the producer writes to a file first. Otherwise the producer gets SIGPIPE and the step fails.
- **Source:** `draft-release.sh` piped `tr` into an `awk` that exited early; it passed in earlier releases, then failed the 0.36.0 release on timing (#699).

### Release workflow change
- **Trigger:** the diff touches `release.yml` or `.github/scripts/*release*` / `checksums.sh`.
- **Check:** the PR shows a `--dry-run` or a run on a test tag, and the job's runner matches the release target (a Windows-only release must not wait for the Mac mini).
- **Source:** #694 merged without a release run; the first one (0.36.0) failed in `draft` (#699), and its new `checksums` job runs on the Mac mini for every target.

### New CI gate
- **Trigger:** the diff turns on `-race`, adds a shard, or adds a gate to `ci.yml`.
- **Check:** the gate was run with `-count 10` on every platform it gates. One clean run does not show a test is free of races.
- **Source:** #563.

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
- **Check:** UNC (`//host/…`, `\\host\…`) and device paths (`\\.\`, `\\?\`, `//./`) are refused, with a test for each form. On Windows a UNC stat contacts that host.
- **Source:** #721 passed `//host/share/x`; the device-path guard from #410 never matched because its prefixes lost a backslash.

### Public repo
- **Trigger:** every PR, and every screenshot or log added to one.
- **Check:** the repo is public. The diff has no real names, home paths, host names, IPs, Team or issuer IDs. Screenshots use made-up data.
- **Source:** standing rule.

## Size

### Growth of `state.go`
- **Trigger:** the diff adds lines to `state.go`.
- **Check:** `state.go` passed 4000 lines and is still growing. New behavior goes in a file of its own concern.
- **Source:** standing rule.

### Disabled instead of removed
- **Trigger:** a feature is turned off "for now" (`isEnabled = false`, a flag, an early return) and its code stays.
- **Check:** an issue is linked with what must happen before it comes back or is deleted.
- **Source:** #677 disabled the pane swipe from #665 and #667 within hours; it did this right by linking #676, which lists the open problems and the steps before re-enabling.

### "Same rule as X"
- **Trigger:** code or a comment says it applies the same rule as another place.
- **Check:** both places call one shared function, and both have a test.
- **Source:** #717's `dir.list` said it used the same device-path rule as `openToSend`, but kept its own copy; `openToSend`'s copy had lost a backslash since #410 (both now share one helper, #723). The Ghostty theme parser and the server's theme parser both read a hex color, but the server took only six digits, so a `#fff` theme worked in the app and vanished on a host.

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
