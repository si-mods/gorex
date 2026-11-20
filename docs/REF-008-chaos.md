---
name: REF-008
description: "Faults that GoRex must survive, how to cause each one, and the evidence it leaves. Use when you add an invariant or test failure recovery."
metadata:
  created: "2026-11-18"
  updated: "2026-11-20"
---

# REF-008: Chaos scenarios

Status: reference. It describes the code in main.

Faults GoRex must survive, and the evidence each one leaves. The list
comes from `docs/invariants.md`; this file says how to break the system
on purpose and what proves it held.

A scenario names three things: the fault, the invariant it attacks, and
the evidence a person or an agent looks at. Evidence is a test result,
and for what only eyes can judge, a PNG in a scratch directory. Nothing
here writes into the repo.

## Scenarios

| Fault | Invariants | Automated check | Evidence |
|---|---|---|---|
| The server is killed (`SIGKILL`) | 2 | `TestCrashRestoresSessions` (`internal/rex/journal_test.go`) | Test log: sessions back with their ids and directories |
| The server is upgraded in place | 1, 2 | `TestUpgradeServerKeepsSessions` (`internal/cli/update_unix_test.go`), `TestUpgradeKeepsAppliedLayout` (`internal/rex/upgrade_test.go`) | Test log: the same sessions, the same layout |
| The connection drops | 1, 3 | `TestReconnectUpdatesWindows` (`workspace_test.go`), `TestChaosConnectionLoss` (`chaos_test.go`) | Test log: the same session, its terminal still holds the screen |
| A client stalls or its address is blackholed | 5 | `TestStalledClientIsDropped`, `TestBlackholedAttachesAreDropped` (`internal/rex/stale_test.go`) | Test log: dropped, queue bounded |
| A pane is killed while a layout applies | 3, 6 | `TestKillDuringApply`, `TestKilledPaneGoesAtOnce` (`internal/rex/layout_life_test.go`) | Test log: the pane leaves at once, no ghost |
| The window is resized without pause | 4 | `TestChaosResizeStorm` (`chaos_test.go`) | `chaos-resize-storm-dark.png`: one pane, no tear, the last size |
| A program floods the pane | 3, 5 | `TestChaosBurstOutput` (`chaos_test.go`) | `chaos-burst-output-dark.png`: the last line shows |
| iOS: the system kills the app | 1, 7 | Not automated: simulator procedure below | Screenshots before and after the relaunch |

A test path or name in this table is checked against `origin/main` when a
scenario is added. A rename that breaks the table is a defect of the
pull request that renames it.

## Running them

```sh
.agents/skills/gorex-ui-testing/scripts/chaos.sh /tmp/gorex-chaos
```

The script runs the `chaos_test.go` scenarios with `GOREX_SHOTS` set to
the directory, then prints the files it wrote. Compare two runs by
looking at the PNGs of the same name: the pane count, the pane's border
and the last line must match. A scenario whose state a PNG cannot show
leaves a test log line instead, as the connection-loss row says. A screenshot from a scenario is not a screenshot
for a pull request: those go on a `shots/pr-<n>` branch, as `CLAUDE.md`
says.

The server-side scenarios stay in their packages and run in CI:

```sh
go test ./internal/rex -run 'TestCrashRestoresSessions|TestUpgrade|TestStalledClientIsDropped|TestBlackholedAttachesAreDropped|TestKillDuringApply'
go test ./internal/cli -run TestUpgradeServerKeepsSessions
```

`TestUpgradeServerKeepsSessions` needs Unix and does not run on Windows.

## iOS: the system kills the app

No test drives a real kill on a device. On the simulator:

```sh
xcrun simctl list devices booted
xcrun simctl terminate booted dev.gorex.ios
xcrun simctl launch booted dev.gorex.ios
xcrun simctl io booted screenshot after-kill.png
```

Then check, against a screenshot taken before the kill:

- the app opens on the same space and the same tab;
- the pane shows the session's screen, not an empty one;
- an edit made just before the kill is in the pane, and a program still
  running is still running.

A kill during a large paste is the case to try: the edit the pane showed
must be there after the relaunch, or the run is a failure.

## Adding a scenario

1. Write the fault as an action another person can repeat: a signal, a
   dropped socket, a resize loop, a flood.
2. Name the invariant it attacks, by number, from `docs/invariants.md`.
   A fault that attacks no invariant is not a scenario yet.
3. Make the check fail when the invariant breaks. Break the code on
   purpose once and watch the test fail.
4. Name the evidence. A PNG only when a person must judge what it shows;
   otherwise the test log.
5. If it cannot run in CI, say where it runs, as the iOS row does.
