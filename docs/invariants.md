# Invariants

The behavior GoRex must not lose. Each item is a fact a user can see, the
test that proves it today, and where a fault that breaks it is exercised
(`docs/chaos.md`).

An agent works from this list. Before it changes state one of these items
names, it reads the test. When the test is thin, it adds the case in the
same pull request. A change that breaks an item is a regression: name it
in the pull request, and add the lesson to `docs/lessons.md` when a
merged pull request has to fix it.

The list is short on purpose. An item earns its place when a plausible
change could break it without a test failing.

## 1. A window comes back on its sessions

When the connection to the server drops, or the server is upgraded in
place, every window shows the same sessions, in the same tabs, on the
same panes.

- `TestReconnectUpdatesWindows` (`workspace_test.go`): a new connection
  keeps the sessions the window had.
- `TestUpgrade`, `TestUpgradeKeepsAppliedLayout`,
  `TestUpgradeKeepsWhatTheScreenTells` (`internal/rex/upgrade_test.go`):
  an upgrade keeps the sessions and the screen they show.
- `TestUpgradeServerKeepsSessions` (`internal/cli/update_unix_test.go`):
  the CLI's upgrade path keeps them too.

## 2. A crash loses no session

A server killed without a stop record starts its sessions again, with the
same ids, at the same directories, and the windows find them where they
were.

- `TestCrashRestoresSessions`, `TestRestartRestoresSessions`
  (`internal/rex/journal_test.go`).
- `TestJournalTornLine`: a journal torn by the kill still restores what
  it holds.

## 3. A pane's text is the session's screen

A pane draws what the session server renders, not a redraw of its own
input, also when another machine sets the size.

- `TestPaneFollowsAnotherClientsSize` (`size_test.go`) compares the
  pane's text with the server's capture, screen for screen.

## 4. A pane's size is its client's, and it comes back

With no other client attached, a pane follows its own size. A locked size
wins. When the client that held the size leaves, the size does not stay
stuck.

- `TestPaneKeepsItsSizeAsTheAuthority`, `TestPaneReturnsToItsSize`
  (`size_test.go`).
- `TestLaggingClientKeepsSize`, `TestSizeLockEndGivesSizeBack`
  (`internal/rex/session_size_test.go`).

## 5. Input is kept, in order, and the queue is bounded

Input the server accepted reaches the program in order, also while output
lags. A client that stops reading is dropped; its input does not grow
without end, and a lost stream waits for its input instead of skipping
it.

- `TestLostStreamWaitsForItsInput` (`hosts_test.go`).
- `TestQueueIsBounded`, `TestStalledClientIsDropped`,
  `TestBlackholedAttachesAreDropped` (`internal/rex/stale_test.go`).

## 6. A pane ends only when its session ends

A pane whose session still lives stays in its tab. A pane the server
killed goes at once. Closing a host's space kills the sessions it held.

- `TestPaneStopped` (`state_test.go`).
- `TestKilledPaneGoesAtOnce`, `TestKillDuringApply`
  (`internal/rex/layout_life_test.go`).
- `TestCloseKillsHostSessions` (`internal/rex/layout_apply_test.go`).

## 7. Resume offers the program that ran

A pane restored after a crash offers the command that ran there, and a
Claude Code session offers `--continue`.

- `TestRestoreOffersRun`, `TestRestoreOffersRunOfResumedSession`,
  `TestClaudeSessionRecorded` (`resume_test.go`).

## 8. Workspaces, tabs and layouts are saved

A window that quits and opens again finds its workspaces, tabs, splits and
active tab, and reads the changes the server made while it was closed.

- `TestWorkspacesSaved`, `TestReopenReadsServerChanges`,
  `TestReopenShowsSpace` (`workspace_test.go`).

## 9. Every visual state uses the DESIGN.md paints

A new overlay, page or inactive state paints the background, the frost
and the dim from `DESIGN.md`, not a variant of its own.

- `TestBackgroundShader`, `TestDimmed` (`shader_test.go`).
- `TestGlassDefaults`, `TestGlassPanelFlat`, `TestGlassWindow`
  (`glass_test.go`).
- `TestDesignTokens` (`tokens_test.go`): type sizes, radii and shadows
  come from `tokens.go`, and spacing is on the grid.

## What this list does not cover

Faults that need a real process, a window or a device run outside the
suite. `docs/chaos.md` lists them with the evidence each run must leave.
