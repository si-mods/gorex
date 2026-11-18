# Changes that cannot be undone

Some changes reach beyond the diff: a build already installed, a paired
device, a running shell. Git reverses the file, not the effect. This file
lists those changes. When a task needs one, an agent stops and asks,
instead of choosing.

The rule: stop before the change, write the options, recommend one, wait.

## The list

| Class | Examples | Why the file back does not undo it |
|---|---|---|
| A saved format another build reads | `settings.json`, `hosts.json`, `layout.jsonl` in the GoRex dir (`internal/rex/journal.go`); the iOS app's `hosts` in `UserDefaults`, a pane gallery's `index.json` | An older build reads what a newer one wrote, and an upgraded app reads what the old one left. A rename or a new required field strands one side. |
| A session id | the ids in `layout.jsonl`, a pane's `SID` | Running shells are found by id. A changed or reused id leaves a shell nobody can attach to, or two panes on one session. |
| The wire protocol | `internal/rexproto`, `internal/rexapi`, the events in `internal/rex/events.go` | A server upgraded in place talks to the old app; the iOS app talks to a server it did not ship with. A field the other side sends is a compatibility promise. |
| Identity and access | tailnet node keys, a pairing code or token (`internal/pairaddr`), a host's stored address | A rotated key or a changed pairing rule disconnects devices that are already paired, and someone has to pair them again by hand. |
| A release number or a published file | `MARKETING_VERSION` in `ios/project.yml`, App Store build numbers, the web build on R2, a release tag | A tag or a build number that is out cannot be reused or withdrawn. |
| A destructive command on a real installation | killing a running server, deleting a journal or a GoRex dir, `git push --force`, removing another branch's worktree | The server's shells die with it, and a journal that is gone restores nothing. `CLAUDE.md` forbids the kill outright. |
| Another person's data | a real `GOREX_DIR`, a window someone is typing in, a host they work on | The state is not in the repo, so no test and no revert reaches it. |

## Stopping

Write three things, then wait:

1. the two or three options, numbered, each with what it breaks;
2. one recommendation, first, marked as such;
3. how to check afterwards that the choice held, and what to do if it did
   not.

A change that only looks risky is not on this list: a refactor, a test, a
doc, a new setting with a default, or a format change that carries its
migration and its test with it. Those go ahead in the pull request, with
the migration named in its body.

## Before asking, do these

- Work on a copy: an isolated `GOREX_DIR` (`/tmp/...`), a test device, a
  branch. The size of the blast radius is often the answer.
- Read the reader: the code that parses the format, the other side of the
  protocol. A field with a default and a test that reads an old file is
  not irreversible.
- Name the builds that would break, with versions, in the question.
