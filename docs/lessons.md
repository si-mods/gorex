# Lessons

Mistakes that reached main, and the rule that would have stopped each one. Read this before you open or merge a PR. Add a lesson when a PR has to fix, undo or redo an earlier one; cite both PRs.

## Merge gates

- **A fix names the test that failed before it.** If no test can be written, the PR says why and what was checked instead. Good examples: #630, #641, #654, #658. Bad: #590, #593, #595, #597, #600, #616 merged with no test.
- **`swiftc -parse` is not verification.** An iOS PR builds with `xcodebuild` against current main before merge. #587, #588 and #627 repaired Swift 6 build errors on main; the one #627 fixed also stopped TestFlight uploads.
- **iOS layout, animation and gesture PRs carry a simulator screenshot or recording** in the PR, and say what was not tested. "In progress" at merge time (#616) means not ready. The key bar and bottom edge were redone five times (#616, #631, #637, #643, #648); animations went back and forth (#590→#595, #593→#597, #594→#603).
- **A style change carries a screenshot**, and sizes come from a shared constant. #605, #607, #608, #611 and #614 reset the same sizes within hours.
- **A failing test found during a PR gets fixed or tracked, not just noted.** #663 reported that `TestLostStreamWaitsForItsInput` already fails on main on Windows, and nothing followed.

## Fixes

- **List every consumer before fixing one.** A bug in a shared path (local pane, remote host, iOS) is fixed for all of them in one PR, with one policy. The lost terminal stream took #641, #654 and #663 in two days because #641 fixed local panes only.
- **A fix for an unexplained failure adds the log line that explains it.** #663 had to add the logs that #641 and #654 lacked.
- **Before a build fix, search open PRs for the same symbol.** #587 and #588 are the same one-line fix from two sessions.

## Tests and CI

- **Do not hide flakes with retries.** Find the race and wait on state; never read async state once. #630 found its race with `-count 30`; do that before merge, not after main goes red (#630, #658).
- **Before turning on a new gate (`-race`, a new shard), run it with `-count 10` on every platform it gates.** One clean run (#563) does not show a test is free of races.
- The root shards in `ci.yml` still rerun a failed shard once. Treat its `::warning::` as a failure to fix, not a pass.

## Design before code

- **A gesture or interaction feature needs its design doc settled first.** Pane switching took #639, #653 and #665 in three days; #653 dropped what #639 had just added.
- **Pick one layout model** (inset or overlay) and change it in one PR. #637 and #648 reversed each other.
- **When a feature ships, update its design doc's status line** in the same PR.

## Security

- **Do not approve a fork PR's workflow run if the PR changes anything under `.github/`.** `test-windows` in `ci.yml` runs PR code on a self-hosted runner that also builds releases and keeps its Go caches. A fork PR can rewrite the workflow, so the `if:` on the head repo is not a boundary; the approval for outside contributors is the only one. Read the whole diff of a fork PR before approving its run.
- **Pin a checksum for every downloaded tool before running it.** Reject `curl | tar` of a toolchain.
- **A new network listener needs a reviewed auth model before merge.** An IP-range allow list with no token is not auth on a shared Wi-Fi.
- **Project-level agent config (`.omp/`, `.claude/`, `.agents/`) must not point at third-party endpoints** without a note saying why.
- **The repo is public.** Scan the diff for real names, home paths, host names, IPs, Team or issuer IDs before pushing. Screenshots use made-up data.

## Size

- `state.go` passed 4000 lines and is still growing. Put new behavior in a file of its own concern instead of appending to it.
- Delete code when its feature goes. Run `staticcheck -checks U1000` for `GOOS=windows`, `linux` and `darwin` before a cleanup PR; code unused on one OS may be used on another.
