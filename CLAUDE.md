# GoRex

## Worktrees

- Do every change in its own worktree and branch. Do not edit in the main checkout; other sessions use it.
- Create it from the latest `origin/main`: `git fetch origin && git worktree add -b <type>/<topic> .claude/worktrees/<topic> origin/main`.
- When the fix is done and verified, commit, push the branch and open a PR (see below).
- After the PR merges, remove the worktree with `git worktree remove .claude/worktrees/<topic>`.

## Pull requests

- PRs go to `si-mods/gorex` only. Never open, comment on or push to `egoist/gorex`.
- Pass `-R si-mods/gorex` to every `gh pr` command, and check the URL it prints.
- Before any `gh` write, run `git remote -v`. If a remote points to `egoist/gorex`, stop and tell the user; do not add one back.
