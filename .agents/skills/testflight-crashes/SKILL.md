---
name: testflight-crashes
description: Read Rex Go's (dev.gorex.ios) TestFlight crash reports from App Store Connect, find the cause in the code, tell whether the latest build has the fix, and fix it if not. Use when the user pastes an appstoreconnect.apple.com …/testflight/crashes/<id> link, or says "TestFlight 崩溃", "崩溃报告", "crash log", "看看这个崩溃", "最新版本还有没有这个崩溃", "testers say it crashed".
---

# TestFlight crashes

Use the App Store Connect API through `asc`. Do not use the web page: a
`/m/` link opens the App Store page of the ASC app, and the browser is not
signed in to App Store Connect. The API key is the one `ios/scripts/upload`
uses (`~/.appstoreconnect/private_keys/AuthKey_6GG554C43Y.p8`); `asc` already
has it.

## 1. List the crashes

```bash
.claude/skills/testflight-crashes/scripts/crashes        # newest 20
.claude/skills/testflight-crashes/scripts/crashes 100
```

Each line: `SUBMISSION_ID  DATE(UTC)  X.Y (n)  DEVICE  iOS  COMMENT`. The
submission ID is the last part of the ASC link. `X.Y (n)` is the version as
TestFlight shows it. The old builds have date numbers, such as
`1.0.1 (202611070229)`.

## 2. Read the log

```bash
.claude/skills/testflight-crashes/scripts/crashes log SUBMISSION_ID > "$TMPDIR/crash.txt"
```

Exit 2 with "no crash log … yet" means that Apple has not attached the log.
This is not an incorrect ID. Apple attaches it some time later. Until then,
use only the submission's attributes (`asc testflight crashes view
--submission-id ID`): device, OS, `appUptimeInMilliseconds`, connection
type, the tester's comment. Say that you do not have the stack. Do not guess
the cause from the time or the comment.

## 3. Find the cause

- Read `Exception Type`, `Termination Reason` and `Triggered by Thread: N`.
  Read **thread N**. Thread 0 is the main thread. It only shows what was on
  screen at that time, unless N is 0.
- Find the first frame in `GoRex`. It gives `File.swift:line`.
- The log is for the build's commit, not for main. To find the commit, see
  the About page of that build, or match the upload time to
  `git log origin/main -- ios`. Read the code at that commit.
- Know these Swift traps:
  - `EXC_BREAKPOINT` + `_assertionFailure` + `CheckedContinuation.resume`:
    the continuation was resumed twice.
  - `swift_unexpectedError` / `fatalError`: a forced `try!`, `!` or index.
  - `EXC_BAD_ACCESS`: memory. Read the C frames (libghostty, Go/mygo).

## 4. Is it fixed in the latest build?

```bash
git fetch -q origin
git log --format='%ci %h %s' origin/main -- <file from the stack>
asc builds list --app 6831210589 --sort -uploadedDate --limit 10 --output json 2>/dev/null | head -1 |
  python3 -I -c 'import sys,json
for b in json.loads(sys.stdin.readline())["data"]: print(b["attributes"]["version"], b["attributes"]["uploadedDate"])'
```

- Read the crashing lines on `origin/main`. Is the defect still there?
- A build contains a fix if its upload time is after the merge time of the
  fix. CI uploads after each `ios/**` merge (see CLAUDE.md "iOS TestFlight").
  A build uploaded a few minutes before the merge does not contain it.
- Also look for newer crashes with the same top `GoRex` frame
  (`crashes 100`). A crash on a build that contains the fix means that the
  fix is not complete.

## 5. Fix

If the defect is still on main, fix it the usual way: a worktree from
`origin/main`, the smallest change, a PR to `si-mods/gorex`
(CLAUDE.md). In the PR, name the submission ID and the build that crashed.
Do not upload by hand: the merge starts the TestFlight run. Give the user
the build from that run's summary as the first build with the fix.

## Report

Give: the build that crashed, the cause (thread, frame, `file:line`, the
reason in one sentence), and the status: fixed in `<commit> (#PR)` from
build `X.Y (n)`, or not fixed and the PR. If you had no log, say so.
