# GoRex

## Lessons

- Before you open or merge a PR, read `docs/lessons.md`. When a PR fixes, undoes or redoes an earlier one, add the lesson there.

## Design

- Before adding or changing UI, read `DESIGN.md`. New overlays, pages and inactive states use its shaders (`paintFrost`, `paintBackground`, `dimmed`) and follow its rules.

## Worktrees

- Do every change in its own worktree and branch. Do not edit in the main checkout; other sessions use it.
- Create it from the latest `origin/main`: `git fetch origin && git worktree add -b <type>/<topic> .claude/worktrees/<topic> origin/main`.
- When the fix is done and verified, commit, push the branch and open a PR (see below).
- After the PR merges, remove the worktree with `git worktree remove .claude/worktrees/<topic>`.

## Pull requests

- PRs go to `si-mods/gorex` only. Never open, comment on or push to `egoist/gorex`.
- Pass `-R si-mods/gorex` to every `gh pr` command, and check the URL it prints.
- Before any `gh` write, run `git remote -v`. If a remote points to `egoist/gorex`, stop and tell the user; do not add one back.
- Screenshots for a PR go on a `shots/pr-<n>` branch, linked by commit SHA, with made-up data only (the repo is public). `.github/workflows/delete-shots.yml` deletes the branch when the PR merges.

## Upgrades and restarts

- **Never kill a running server to upgrade it.** A session's terminal belongs to the server process: `Stop-Process`/`pkill` (or an installer over it) loses every shell and its screen for good, and no resume brings them back.
- Upgrade in place with the newer build's own CLI: `<new GoRex> server upgrade`, with the host's data directory. Outside macOS the server executable is the CLI's own binary, so it must be the new build. Success reads `upgraded in place; terminal sessions kept`; the server logs `upgrading to …` then `resumed N of N sessions`, and `gorex ls` keeps the session IDs (`internal/rex/upgrade.go`, `upgrade_windows.go`). Linux user installs get the same handoff from `gorex update`.
- `gorex server restart` is not that: the layout comes back with new shells, as its own output says.
- Practise on an isolated data directory (`GOREX_DIR=<dir> <exe> -server`) before the host that people are working in.

## iOS TestFlight

- GitHub Actions uploads the iOS app (`.github/workflows/testflight.yml`) on every push to main that touches `ios/**`, `internal/tailnode/**`, `go.mod` or `go.sum`. The job runs on the Mac mini's self-hosted runner (`~/actions-runner-gorex`, label `gorex-testflight`); with the Mac mini off, it waits. Do not upload by hand after a merge; read the build from the run's summary ("Uploaded Rex Go 1.1 (n)").
- To upload without a merge: `gh workflow run testflight.yml -R si-mods/gorex` (main only).
- Versions read `X.Y (n)`. `MARKETING_VERSION` in `ios/project.yml` is X.Y; `ios/scripts/upload` takes the version's highest build on App Store Connect plus one. A new X.Y starts again at 1. Local builds are `(0)`.
- A squash merge whose message contains `[skip ci]` does not start the run; start it by hand.
- The run sets the build's What to Test per locale (`en-US`, `zh-Hans`) to the bullets `ios/WhatToTest.md` gained in that locale's section since the last run that uploaded (`ios/scripts/test-notes`). A PR that changes what a tester sees adds a bullet to each section, in their terms; the `notes` job of `ci.yml` fails a PR whose section is still empty or whose bullet reads like a commit subject. To set an older build's: `ios/scripts/test-notes 1.1 <n> <from> <to>`.
