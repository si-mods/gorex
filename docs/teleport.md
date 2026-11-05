# Teleport

Teleport moves a pane's work to another machine: the code it works on,
the coding agent's conversation, and what the terminal showed. The
process does not move — no OS GoRex runs on can migrate a live process
with its PTY. The work is packed on the source, unpacked on the target,
and the program started again there, the way `claude --teleport` brings
a web session to a local checkout.

After a teleport the pane is the new session on the target host, in a
tab of that host's (see The app). The source session ends.

## Scope

Phase 1 teleports a pane whose foreground is Claude Code, or a shell at
its prompt, inside a Git work tree, between two hosts GoRex already
connects to (Remote hosts: coord network or system Tailscale), from this
machine to a host. Anything else is refused with a reason:

- a pane of another host: only this machine's server packs;
- not a Git work tree (nothing to rebuild the directory from), a repo
  with no commit, or one with no remote URL (nothing to clone);
- Claude Code working (its conversation file is still being written:
  its status says working, or it printed in the last two seconds, as its
  spinner does); the user waits for it to be idle, or stops it first;
- another program in the foreground (a server, an editor): its state is
  in its process;
- the target's server too old to have the `teleport` op.

Windows hosts are in: either side may be macOS, Linux or Windows. What
differs there is handled at the target, from the target's own rules:
the work tree path (`%USERPROFILE%\repos`…), the conversation's
directory name (Claude Code's slug of `C:\Users\…` is `C--Users-…`), and
the resume command, quoted for the target pane's shell (PowerShell,
cmd, POSIX, fish; the quoters of `resume.go`, now in
`internal/rex/quote.go` for the server to type it). Line endings
are Git's business: the patch is binary and applied by Git, which
honours the target clone's `core.autocrlf`.

## What is packed

A package is one tar stream. Its manifest travels before it, in the line
that asks for it (below): version, source host name, program (`claude` /
`shell`), the session id, the repo's remote URLs (origin's first),
branch, HEAD, the work tree's root on the source (for the conversation's
`cwd`), the pane's directory relative to it, terminal size, and the
package's length.

| Part | Content |
|---|---|
| `repo.bundle` | `git bundle` of the commits reachable from HEAD that no remote-tracking ref has; empty when HEAD is pushed |
| `worktree.patch` | `git diff --binary HEAD`: staged and unstaged changes |
| `untracked.tar` | the regular files `git ls-files -o --exclude-standard` lists; ignored files and links stay behind |
| `claude/<uuid>.jsonl` | the conversation, and its `<uuid>/` directory (subagents, tool results) when there is one |
| `scrollback.txt` | the pane's text (`format` text), its last MiB, for the new pane to show, dimmed and without control characters, above the program |

Limits: the package is refused past 256 MiB, and `untracked.tar` past
64 MiB, so a stray build output does not cross the network. Secrets in
untracked files (`.env`) travel; the data plane is WireGuard end to end
and the target is a host the user trusts with commands already (see
[coord.md](coord.md), Trust model).

## Unpacking on the target

1. **Find the repo.** A local clone whose remote URL matches one of the
   manifest's (`https://`, `ssh://` and `git@host:` spellings of one repo
   match), one or two levels under `~/repos`, `~/src`, `~/code` and
   `~/Projects`, under the home directory on every OS. None: clone the
   first URL into `~/repos`. No URL (a repo never pushed): refuse. A
   setting for other roots is for later.
2. **Make a work tree.** Never touch the clone's own checkout: fetch the
   bundle, then `git worktree add -b teleport/<short-id> <clone>/.gorex/teleport/<short-id> <HEAD>`.
   When HEAD is on the remote and the clone lacks it, fetch it first. The
   clone's `.git/info/exclude` gets `/.gorex/`, so its own status does not
   list the work trees.
3. **Apply the changes.** `git apply --binary worktree.patch` to the
   work tree (staged changes arrive unstaged), then unpack
   `untracked.tar`: only regular files, each new, at a relative path
   reached through no link. A failing apply aborts and removes the work
   tree and its branch, as any failure after it does.
4. **Place the conversation.** Rewrite the `cwd` field of every line of
   the jsonl from the source path to the target path, and write it to
   the target's Claude config dir under `projects/<slug(target cwd)>/`,
   the slug being Claude Code's own (every character other than a
   letter or digit becomes `-`; Claude Code's shortening of very long
   paths is not followed). The config dir is the target server's
   `CLAUDE_CONFIG_DIR`, else `~/.claude`.

   A copy of the same uuid already here, in any project, is what a
   round trip leaves: the conversation went A → B, the user went on at
   B, and now comes back to A, which still has the copy it sent (the
   source keeps its file: after a teleport it is an older version, and
   nothing on the source changes it). The copies are compared by their
   messages, the `uuid` field of each line (lines without one, as
   summaries, do not count):

   - every message here is in the incoming one (the same, or it went on
     elsewhere): the copies here, each `<uuid>.jsonl` and its `<uuid>/`
     directory, move to a backup in the config dir, the incoming one is
     written as above, and the backup goes once the session starts; a
     failure before then moves them back, as they were;
   - a message here is not in the incoming one: the conversation went on
     on both machines, and the teleport is refused ("the conversation
     went on here too"), nothing changed.

   This is decided once the package is in: the request carries no
   message ids.
5. **Start the session.** `create` with the target directory and the
   pane's size, then type `claude --resume <uuid>` into the shell
   (reusing `claudeResume`), so the shell stays when Claude Code exits,
   as on the source. A shell pane only gets its directory.

The target answers with the new session's id; the source app re-points
the pane to it, then kills the source session. Until the answer comes,
nothing on the source changes, so a failed teleport leaves the pane as
it was.

## Protocol

One new server op, `teleport`, allowed to remote peers that may
`create` (it runs a command, as `create` does) and only to them, on a
connection of its own. The request carries the manifest and the
package's length; the server answers `{"ok":true}`, or refuses at once
a manifest it does not take. The package then follows as raw bytes, the
way a framed attach sends its snapshot after a header line
(`SnapshotHeader`). The reply is a `Response` whose data is the new
`SessionInfo`, or an error naming the step that failed. A server that
has it says `teleport` in its hello's capabilities; one too old has the
menu name it.

Packing happens on the source's own server (op `teleport.pack`, local
socket only, on a connection of its own), which has the session's
directory, Claude Code's process and its config dir (`AgentSession`,
`AgentConfigDir`). It answers a `Response` line whose data is the
manifest, then the package. The source app (or CLI) pipes the package
from its server to the target's; it holds no files.

## The app

- **Teleport to…** in the pane's menu and the command palette, listing
  the connected hosts; disabled with the reason when the pane cannot
  go.
- While it runs, the pane shows a frosted overlay (`paintFrost`) with
  the step and the host; Cancel closes the connections, and the target,
  which looks at its connection just before `create`, removes what it
  made.
- On success the pane is the remote session, in a tab of the host's: a
  tab's panes are all on one machine, so the session opens as a tab among
  the host's, named as the pane's tab was when the pane was its only one,
  and the pane closes, ending its session.
- `gorex teleport <pane> <host>` does the same from the CLI: it pipes the
  package, then asks the window (layout command `pane.teleported`, local
  clients only) to show the pane as the host's session; with no window
  showing the pane, it ends the source session itself.

## Not in phase 1

- Teleporting back as a merge: going back is another teleport, which
  makes a new work tree on the original machine (the conversation takes
  the place of the older copy there, step 4); the user merges the
  branch.
- Cleaning `.gorex/teleport/` work trees: `git worktree remove` by hand,
  or a later "Clean up teleports" command.
- Agents other than Claude Code (Codex keeps its conversations
  elsewhere); their panes go as shells.
