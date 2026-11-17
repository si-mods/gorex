---
name: verify
description: Run GoRex's pre-push smoke checks (go run ./tools/verify) — gofmt, vet and cross-builds for linux/windows/darwin, golangci-lint, tests of the changed packages, web typecheck and tests, actionlint — chosen from the diff against origin/main, and get the Verified line for the PR body. Use before every commit, push or PR, before saying work is done, when a push is blocked by the tools/verify hook, or when asked to "verify", "run the checks", "检查一下", "跑一下验证".
---

# Verify before you push

`tools/verify` runs the cheap checks that the current diff needs, cheapest first, and stops at the first failure. Run it before every commit, push or PR, and before you say a change is done.

```sh
go run ./tools/verify          # the checks for this diff
go run ./tools/verify -full    # also the root package's UI tests (slow; CI runs them)
```

It reads the files changed since the merge base with `origin/main`, committed or not (run `git fetch origin` first if main moved). It needs no shell, so it runs the same on Windows and macOS.

## What each check proves

| Diff touches | Check | Proves |
|---|---|---|
| `*.go` | `gofmt -l` | CI's format gate passes for the changed files |
| `*.go`, `go.mod` | `go vet` with GOOS linux, windows, darwin (CGO off) | the changed packages compile and vet on every system, build tags included |
| `*.go`, `go.mod` | `go build ./...` for CI's four targets | nothing that imports the change broke |
| `*.go`, `go.mod` | golangci-lint v2.15.0 (CI's version, as linux) on the changed packages | CI's lint gate |
| `*.go` | `go test -count=1` of the changed non-root packages | their unit tests pass on this machine |
| root `*.go` | `go test .` only with `-full` | the UI tests; otherwise listed as skipped |
| `web/**` | `npm run typecheck`, `npm test` in `web/` | CI's web job (needs `npm ci` once) |
| `.github/**` | actionlint (pinned) | workflow syntax and expressions |
| `ios/**` | `xcodegen generate` + `xcodebuild` simulator build, on a Mac only | the app builds; on Windows it prints NOT VERIFIED |

Pinned tools install once into the user cache dir (`gorex-verify/`).

These checks prove the code builds and its unit tests pass. They do not prove the app works. For UI, session-server and iOS changes, go on to the gorex-ui-testing skill (headless view tests, debug-hook screenshots, the iOS simulator). An iOS change is not verified until `xcodebuild` has built it on the Mac; `swiftc -parse` is not verification.

## Rules

- Paste the final summary line into the PR body as its `Verified:` line, unchanged. Add what you checked by hand (screenshots, a real run) after it. Never write a check as passed that the tool skipped.
- If a check still fails after three rounds of fixes, stop and bring in the user with the failing output and what you tried.
- Flaky tests: never add sleeps, retries or longer timeouts to make a test pass. Find the race and fix it; if the test cannot be made deterministic, delete it and say why in the PR.
- Failures that are already on `origin/main` are not yours to hide: report them separately, with the commit that broke them if you can find it.

## The push hook

`.claude/settings.json` runs `go run ./tools/verify -hook` before every Bash or PowerShell call. It does nothing unless the command contains `git push` (not `--delete`) or `gh pr create` in a GoRex checkout. Then it runs the same checks (without `-full`) in the directory the command runs in (the session's cwd, a leading `cd DIR &&`, or `git -C DIR`), and blocks the call with the failure if one fails (a `deny` decision whose reason is the failing output).

- After two blocks in one session the third push goes through with a loud UNVERIFIED warning. Say so in the PR body.
- `GOREX_VERIFY=off` turns the hook off (for example, to push a branch you know is broken to share it).
