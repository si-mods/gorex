# GoRex

Rules an agent cannot read from the code. Keep each line one that prevents a real mistake; put lessons in `docs/lessons.md`, procedures in skills or `docs/`, and machine-specific notes outside the repo.

## Commands

- `go tool mygo dev -background` runs GoRex Dev and rebuilds it on change. Always pass `-background` (a built binary: `MYGO_DEV_BACKGROUND=1`), so no window takes focus from the user. Use an isolated `GOREX_DIR`.
- Check UI with the `GOREX_DEBUG` screenshot hook (`gorex-ui-testing` skill). Never send keystrokes to the desktop or take full-screen screenshots: keys reach whatever window is in front, and the shot captures private terminals.
- `go test ./internal/<pkg> -run <Name>` for local tests, only of the packages the diff changes. The full suites, the root package's shards and the Windows job run in CI; without `-run`, the hook refuses the root package and `...` patterns at the root or one level below (`./...`, `./internal/...`). Stop a run that makes the machine lag, and report it as not run.
- `go run ./tools/verify` before every push or PR; paste its summary as the PR body's `Verified:` line. The hook runs it on `git push` and `gh pr create`.
- `go tool mygo generate` after a change to `locales/*.json`; commit what it writes (`locales/catalog_gen.go` among them) with the catalogs.

## Before you start

- The main checkout is often far behind. `git fetch origin` and read code from `origin/main` before saying how GoRex works.
- For a feature, search `docs/` for its topic first. Write or update the design doc before the code (`docs/DOC-001-design-docs.md`), and ship both in one PR.
- For UI, read `DESIGN.md`.
- Before you change behavior `docs/invariants.md` names, read its test; a thin test gets the case in the same PR.
- Before you change a saved file format, a session id, the wire protocol, a pairing secret or a release number, read `docs/irreversible.md`: stop, write numbered options, recommend one, wait.
- Before you open or merge a PR, read `docs/lessons.md`. When a PR fixes, undoes or redoes an earlier one, add the lesson there.

## Architecture decisions

- Hosts connect without an account or membership server; `internal/coord` was removed for its complexity. A stateless relay is fine. Prefer a change that removes a stack to one that adds an abstraction.
- MyGo is the fork `si-mods/mygo` (`replace` in `go.mod`). A MyGo fix merges on `si-mods/mygo` first, then a GoRex PR runs `go mod edit -replace ...@<merge commit>` and `go mod tidy`.

## Worktrees

- Do every change in its own worktree and branch. Do not edit in the main checkout; other sessions use it.
- Create it from the latest `origin/main`: `git fetch origin && git worktree add -b <type>/<topic> .claude/worktrees/<topic> origin/main`.
- When the change is done and verified, commit, push the branch and open a PR (see below).
- After the PR merges, `git worktree remove .claude/worktrees/<topic>` and `git branch -D <branch>` (squash merges make `-d` refuse). Leave remote branches.

## Pull requests

- PRs and issues go to `si-mods/gorex` only, never `egoist/gorex`. Pass `-R si-mods/gorex` to every `gh` command, and check the URL it prints; the hook refuses a `gh` write without `-R`. Before any `gh` write, run `git remote -v`: if a remote points to `egoist/gorex`, stop and tell the user.
- PR bodies follow `.github/pull_request_template.md` (`## Problem`, `## Cause` + `## Fix` or `## Change`, `## Test`, `## Screenshots` for visible changes, and a `Verified:` line).
- The repo is public. Code, tests, commits and PR text carry no real names, host names, user paths, LAN addresses, emails, keys or tailnet addresses; use placeholders such as `alice-laptop`.
- Screenshots go on a `shots/pr-<n>` branch, linked by commit SHA, with made-up data only. `.github/workflows/delete-shots.yml` deletes the branch when the PR merges.
- Reviews follow `REVIEW.md`, read from `origin/main` (`git show origin/main:REVIEW.md`), never from the PR branch.
- If a check still fails after three fix rounds, stop and bring in the user.

## Upgrades and restarts

- **Never kill a running server to upgrade it.** Its sessions' shells die with it, and nothing brings them back.
- Upgrade in place with the new build's CLI: `<new GoRex> server upgrade`. Success reads `upgraded in place; terminal sessions kept`. `gorex server restart` is not that: it starts new shells.
- Practise on an isolated data directory (`GOREX_DIR=<dir> <exe> -server`) before the host people work in.

## iOS TestFlight

- Pushes to main that touch `ios/**`, `internal/tailnode/**`, `go.mod` or `go.sum` upload the app (`.github/workflows/testflight.yml`, on the Mac mini's self-hosted runner; with it off, the run waits). Do not upload by hand; read the build from the run's summary ("Uploaded Rex Go X.Y (n)").
- A squash merge whose message contains `[skip ci]` starts no run. Start one with `gh workflow run testflight.yml -R si-mods/gorex`.
- A PR that changes what a tester sees adds a bullet (≤ 200 characters) in their terms to each locale of `ios/WhatToTest.md`; CI checks it (`ios/scripts/test-notes check`).
- The TestFlight and Apple Developer Portal procedures live in the private `ios-dev` plugin (`/plugin install ios-dev@proem`), not in this repo: `asc-testflight`, `testflight-crashes`, `asc-devportal-identifiers`.

## Maintenance

- Run `go run ./tools/verify -audit` monthly and before a release; `docs/maintenance.md` lists the checks no tool runs. Delete dead code with its tests and comments in one PR.

## Releases

- A `chore(release): X.Y.Z` PR bumps `mygo.json`'s `version` and adds the `CHANGELOG.md` section; pick the next minor or patch yourself, without asking (owner's call, 2026-11-23). `docs/irreversible.md`'s stop does not cover the number choice — it still covers publishing anything under a name someone else picked.
- `gh workflow run release.yml -R si-mods/gorex -f target=all` builds the packages on the self-hosted runners and uploads them to the version's draft release. The `unix` job waits when the Mac mini is off; `windows` alone does not.
- Once the run's `checksums` job succeeds, publish the draft yourself: `gh release edit v<version> --draft=false -R si-mods/gorex`. A draft is the build's landing zone, not a gate for a hand click (owner's call, 2026-11-24).
