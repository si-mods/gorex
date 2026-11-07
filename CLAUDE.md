# GoRex

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

## iOS TestFlight

- GitHub Actions uploads the iOS app (`.github/workflows/testflight.yml`) on every push to main that touches `ios/**`. Do not upload by hand after a merge; read the build from the run's summary ("Uploaded Rex Go 1.1 (n)").
- To upload without a merge: `gh workflow run testflight.yml -R si-mods/gorex` (main only).
- Versions read `X.Y (n)`. `MARKETING_VERSION` in `ios/project.yml` is X.Y; `ios/scripts/upload` takes the version's highest build on App Store Connect plus one. A new X.Y starts again at 1. Local builds are `(0)`.
- A squash merge whose message contains `[skip ci]` does not start the run; start it by hand.
