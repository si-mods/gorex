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

## iOS

### Any change under `ios/**`
- **Trigger:** the diff touches `ios/**` Swift files.
- **Check:** the PR builds with `xcodebuild` against current main. `swiftc -parse` is not verification.
- **Source:** #587, #588 and #627 repaired Swift 6 build errors on main; the one #627 fixed also stopped TestFlight uploads.

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

### Build fix
- **Trigger:** the PR fixes a build error or a compile-time symbol.
- **Check:** open PRs are searched for the same symbol before the fix.
- **Source:** #587 and #588 are the same one-line fix from two sessions.

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

### Test that depends on the environment
- **Trigger:** a test reads `$SHELL`, env vars, home paths or the host's tools without setting them.
- **Check:** the test pins the shell, env and paths it relies on.
- **Source:** `TestPeerListsAndMirrors` failed in Git Bash because `$SHELL` picked bash (#679).

### Caches on a self-hosted runner
- **Trigger:** the diff adds `cache: true` to `setup-go` or `actions/cache` in a job that runs on a self-hosted runner.
- **Check:** do not also save Go's caches to the Actions cache. The Mac mini holds `~/Library/Caches/go-build` (27 GB) and `~/go/pkg/mod` (4.5 GB) between runs. Jobs on instabox start on a fresh box, so they do use the Actions cache, but only pushes to main save it.
- **Source:** `setup-go` with `cache: true` in `testflight.yml` (#371) saved a 7.1 GB copy into the repository's 10 GB Actions cache and evicted the Linux test caches; #688 set `cache: false`, as `release.yml` already had (#681).

### Concurrency change tested only on Windows
- **Trigger:** the diff adds goroutines, locks, a listener restart or a shared package variable, and the PR's `-race` runs are from Windows only.
- **Check:** the new tests also pass `go test -race -count 20 -cpu 1,4,8` on Linux or macOS before the PR opens.
- **Source:** #684 passed `-race -count 5` on Windows, then Linux CI found a race in the WebTransport restart and the Mac mini found one on the `tracing` variable.

### Lint before the PR
- **Trigger:** any Go change.
- **Check:** the author ran `golangci-lint` at the version `ci.yml` pins, for `GOOS` linux, windows and darwin, and `gofmt -l` on the changed files. `go vet` alone is not the lint job.
- **Source:** #684 failed CI three times, one push at a time: `bodyclose` (lint), gofmt, and `TestNoInternalInit` (a rule test that `go vet` does not run); #672 needed a gofmt fix after review.

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

### Public repo
- **Trigger:** every PR, and every screenshot or log added to one.
- **Check:** the repo is public. The diff has no real names, home paths, host names, IPs, Team or issuer IDs. Screenshots use made-up data.
- **Source:** standing rule.

## Size

### Growth of `state.go`
- **Trigger:** the diff adds lines to `state.go`.
- **Check:** `state.go` passed 4000 lines and is still growing. New behavior goes in a file of its own concern.
- **Source:** standing rule.

### Removing a feature
- **Trigger:** the PR deletes a feature or a cleanup PR removes unused code.
- **Check:** dead code goes with the feature. Run `staticcheck -checks U1000` for `GOOS=windows`, `linux` and `darwin`; code unused on one OS may be used on another.
- **Source:** standing rule.
