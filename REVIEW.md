# Review

Instructions for the one reviewer of a GoRex PR (Claude `/code-review` or a review subagent).

## Read the rules from main

- Read this file and the checklist from `origin/main`, never from the PR branch. A change must not edit the rules that judge it.
  - `git fetch origin && git show origin/main:REVIEW.md`
  - `git show origin/main:docs/lessons.md`
  - On Windows Git Bash set `export MSYS_NO_PATHCONV=1` first.
- If the PR edits any of these, flag it to the human explicitly, at the top of the review: `REVIEW.md`, `docs/lessons.md`, `.github/workflows/`, `ios/scripts/test-notes`, `tools/verify`.
- Review the latest commit only. A review of a superseded commit means nothing; re-check the head SHA before you post.

## Steps

1. Read the diff.
2. From `docs/lessons.md`, select the items whose **Trigger** matches the diff (usually a few). Check those, plus correctness.
3. Read the PR body. It needs a `Verified:` line, and the evidence must match the change:
   - iOS change: `xcodebuild` plus a simulator screenshot.
   - `fix`: a test that failed before the fix.
   - UI or style change: a screenshot with made-up data.
   - If the evidence is missing or does not match, that is an Important finding.
4. Later rounds: read the earlier rounds' findings first and check whether each was fixed. Do not start from scratch.

## Severity

- **Important:** a bug, a failed lessons check, missing or mismatched evidence. Blocks merge.
- **Nit:** style or preference. Does not block.
- **Pre-existing:** a bug that was already on main. Report it; it does not block the author.

Each finding says where it is, what is wrong, and why it needs a human (for example: a judgment call, a design choice, a risk the tests cannot show). Do not post a finding with no such reason.

## GoRex focus

- **UI:** new overlays, pages and inactive states use the `DESIGN.md` shaders (`paintFrost`, `paintBackground`, `dimmed`) and follow its rules. Read `DESIGN.md` for any UI diff.
- **Icons:** Lucide icons only. No Unicode glyphs as icons in any UI.
- **Windows panes:** rounded pane corners on Windows stay unchanged (cardR 8); corner changes are macOS-only.
- **Public repo:** the diff, PR body and screenshots contain no real names, home paths, host names, IPs or Team or issuer IDs.
- **Session server:** changes to the session server (`internal/`, sockets, attach, upgrade, listeners) keep running sessions alive, add no unauthenticated listener, and cover local panes, remote hosts and iOS together.
- **Flaky tests:** CI does not retry. A sleep or retry in a test is Important.
- **Worktree and PR rules (`CLAUDE.md`):** the PR targets `si-mods/gorex`, never `egoist/gorex`. Screenshots live on a `shots/pr-<n>` branch with made-up data.
- **Checksums:** any downloaded tool has a pinned checksum.
