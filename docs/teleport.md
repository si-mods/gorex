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

Phase 1 teleports a pane whose foreground is a coding agent GoRex knows,
Claude Code or omp (oh-my-pi), or a shell at its prompt, inside a Git work tree, between two hosts GoRex already
connects to (Remote hosts: coord network or system Tailscale), from this
machine to a host. Anything else is refused with a reason:

- a pane of another host: only this machine's server packs;
- not a Git work tree (nothing to rebuild the directory from), a repo
  with no commit, or one with no remote URL (nothing to clone);
- the agent working (its conversation file is still being written:
  its status says working, or it printed in the last two seconds, as its
  spinner does); the user waits for it to be idle, or stops it first;
- another program in the foreground (a server, an editor): its state is
  in its process; so is a session started with a program rather than a
  shell (`gorex new -- ping`), the program its own process, which the
  target would make a shell of: an agent started so (`gorex new -- omp`)
  too, as GoRex finds an agent's conversation only under a shell;
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
`cwd`), the pane's directory relative to it, the work tree's root relative to
the source's home (when under it), terminal size, the hop's
link and the links of earlier hops of the work tree (Round trips), and
the package's length.

| Part | Content |
|---|---|
| `repo.bundle` | `git bundle` of the commits reachable from HEAD that the remotes do not have, by what `git ls-remote` answers of their heads and tags now (a remote-tracking ref may be stale: a branch deleted after a squash merge); by the remote-tracking refs when no remote answers, as offline; empty when HEAD is pushed |
| `worktree.patch` | `git diff --binary HEAD`: staged and unstaged changes |
| `untracked.tar` | the regular files `git ls-files -o --exclude-standard` lists; ignored files and links stay behind |
| `claude/<uuid>.jsonl` | the conversation, and its `<uuid>/` directory (subagents, tool results) when there is one |
| `claude/file-history/<uuid>/<hex>@v<n>` | the backups `/rewind` restores files from (`<config>/file-history/<uuid>/`), regular files of that name only, but those only files outside the work tree have |
| `omp/<time>_<id>.jsonl` | omp's conversation, the directory of the same name beside it (its artifacts, as `109.bash.log`), and `omp/blobs/<sha256>[.ext]`, the blobs of its data dir that the conversation quotes (`blob:sha256:<hex>`) |
| `scrollback.txt` | the pane's text (`format` text), its last MiB, for the new pane to show, dimmed and without control characters, above the program |

Limits: the package is refused past 256 MiB, and `untracked.tar` past
64 MiB, so a stray build output does not cross the network. Secrets in
untracked files (`.env`) travel; the data plane is WireGuard end to end
and the target is a host the user trusts with commands already (see
[coord.md](coord.md), Trust model).

## Unpacking on the target

0. **Back where it came from.** A teleport that comes back to a work tree
   it left, untouched since, lands there instead of in a new work tree
   (Round trips, below); steps 1 and 2 are skipped then.
1. **Find the repo.** A local clone whose remote URL matches one of the
   manifest's (`https://`, `ssh://` and `git@host:` spellings of one repo
   match), among the directories the user works in on the target, as
   GoRex knows them: the directories of the server's sessions, live and
   those its journal recorded, and the `cwd` of each of Claude Code's
   projects in the target's config dir (the first line that has one, of
   one conversation per project). Each is resolved to its Git work tree
   and repo by reading `.git` (a linked work tree's `.git` file and its
   `commondir` lead to the repo, whose main work tree is the clone), and
   the remotes are read from the repo's `config` file, with no `git` run
   per directory (includes and `insteadOf` are not followed). GoRex
   searches no fixed directories. None found: clone the first URL where
   the source has its work tree relative to its home directory
   (`~/work/x` on a Mac lands in `%USERPROFILE%\work\x` on Windows); a
   clone of the repo there is taken as it is. When that path is another
   directory, or the source's work tree was outside its home, clone to
   `~/<repo name>`, with `-2`, `-3`… when taken. No URL (a repo never
   pushed): refuse.
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

   omp's conversation goes the same way, under omp's data dir: its
   file, keeping its `<time>_<id>.jsonl` name, and the directory of that
   name beside it, into `sessions/<omp's slug of the target cwd>/`, its
   `cwd` rewritten. omp's slug, as its `session-paths.ts` makes it: of
   the real path, under the temporary directory `-tmp-` and the path from
   it, under home `-` and the path from it, else `--<path>--`, each `/`,
   `\` and `:` a `-` (`~/repos/gorex` is `-repos-gorex`, `C:\x` is
   `--C--x--`). The data dir, as omp's `dirs.ts` has it, of the target
   server's environment: `~/.omp/agent`, `PI_CONFIG_DIR` in place of
   `.omp`, a profile's (`OMP_PROFILE`, `PI_PROFILE`) under `profiles/`,
   `PI_CODING_AGENT_DIR` without one, `$XDG_DATA_HOME/omp` when it is
   there. The blobs merge into its `blobs/` as Claude Code's file history
   does (below). A message is the `id` of a line that is the
   conversation, for the round trip's rule below, as `uuid` is Claude
   Code's.

   A copy of the same uuid already here, in any project, is what a
   round trip leaves: the conversation went A → B, the user went on at
   B, and now comes back to A, which still has the copy it sent (the
   source keeps its file: after a teleport it is an older version, and
   nothing on the source changes it). The copies are compared by their
   messages: the `uuid` of Claude Code's `user` and `assistant` lines,
   the `id` of omp's `message`, `custom_message`, `branch_summary` and
   `compaction` entries (those omp makes the model's context of). The
   other lines do not count: summaries, snapshots, and what an agent
   notes of itself, as the `session_exit` omp appends to the source's
   copy once its pane ends after the teleport:

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

   File history, for `/rewind`: in the same pass as `cwd`, each
   `file-history-snapshot` line's `trackedFileBackups` key under the
   source's work tree becomes the same path under the target's (the
   target's separators); a key outside it is dropped, as a rewind on
   another machine must write nothing outside the work tree, and the
   source does not send the backups only such keys name; a relative key
   stays. Only the lines that change are written anew. The backups go to
   `<config>/file-history/<uuid>/`: those not there are added; one there
   already must be the same, else the history went on there too and the
   teleport is refused. A failure removes only what it added.
5. **Start the session.** `create` with the target directory and the
   pane's size, the session's id the hop's link (which the source's window
   knows by then, and waits for, rather than open it as a tab of its own;
   the target's server gives it a tab there, as a remote client's),
   then type `claude --resume <uuid>`, or `omp --resume <id>`, into the
   shell (`ClaudeResume`, `ompResume`), so the shell stays when the agent
   exits, as on the source. A shell GoRex
   cannot quote for (nushell) is refused before the package is sent. A
   shell pane only gets its directory.

   The answer goes once the session runs; a client gone by then, as a
   cancel, gets nothing started: the session ends and what step 0–4 made
   is undone. The target closes the connection once it kept what it made.

The target answers with the new session's id; the source app re-points
the pane to it, then kills the source session. Until the answer comes,
nothing on the source changes, so a failed teleport leaves the pane as
it was.

## Round trips

Each successful hop has a link, a random id. Each server keeps the hops
it saw in `teleports.json` in its directory (readable by the user only):
`{link, dir, departed, at}`, where `dir` is a work tree's root and
`departed` its fingerprint as the work left it, "" for one the work
landed in. The fingerprint is a SHA-256 of HEAD, the
`git diff --binary HEAD` and the untracked files' names and contents:
what a package takes of the work tree.

- The source records `{L, its root, fingerprint}` as it packs, since
  nothing tells it of the hop's end; the manifest carries `L` and the
  links it has recorded of the same root, those of earlier hops.
- The target records `{L, the work tree it landed in, ""}` once the
  session starts.
- The target looks the manifest's earlier links up in its own records:
  a directory the work left by one of them, still a work tree of a clone
  of the repo, whose fingerprint is still the one it left with, untouched
  since. The most recent of those, if any, takes the work:
  1. fetch the bundle or the remote into its repo, as step 2 does; its
     HEAD must be an ancestor of the incoming one, else a new work tree
     takes the work;
  2. keep all it has, ignored files aside, as a commit of a temporary
     index (`add -A`, `write-tree`, `commit-tree` on HEAD) under
     `refs/gorex/teleport-backup/<L>`, and its index file; never the
     stash;
  3. remove its untracked files (they are in the backup), and
     `git reset --hard` its branch, or detached HEAD, to the incoming
     HEAD: a fast forward;
  4. apply the changes and the untracked files, as step 3 does.

  A failure after the move puts it back as it was: `reset --hard` to its
  old HEAD, the incoming untracked files removed, `read-tree -u --reset`
  of the backup, its index file back, and the backup ref deleted. On
  success the ref is deleted, and the record it left by gets `departed`
  "": nothing is left to go back to.
- A directory the user changed since it left is never touched: a new
  work tree takes the work, as on a first hop.

So MacBook:`~/repos/x` → mini (new work tree W) → MacBook continues in
`~/repos/x`, and mini → MacBook → mini again in W: round trips leave no
work trees behind. The conversation follows step 4, in the project of
the directory it lands in.

## Protocol

One new server op, `teleport`, allowed to remote peers that may
`create` (it runs a command, as `create` does) and only to them, on a
connection of its own, as a file transfer's (`file.put`): the server
routes it by its first line, and drops one of a remote peer that sends
or takes nothing for 30 seconds while it waits on the peer. The request carries the manifest and the
package's length; the server answers `{"ok":true}`, or refuses at once
a manifest it does not take. The package then follows as raw bytes, the
way a framed attach sends its snapshot after a header line
(`SnapshotHeader`). The reply is a `Response` whose data is the new
`SessionInfo`, or an error naming the step that failed. A server that
has it says `teleport` in its hello's capabilities; one too old has the
menu name it.

Packing happens on the source's own server (op `teleport.pack`, local
socket only, on a connection of its own), which has the session's
directory, the agent's process and its config dir (`AgentSession`,
`AgentConfigDir`). Claude Code's conversation is the one its
`sessions/<pid>.json` names; omp's the file its state dir's
`terminal-sessions/<tty>` records for the pane's terminal (the tty's
name past `/dev/`, as `ttys008`), its directory the pane's; with no such
record, as on Windows, where omp keys a terminal by its emulator's
environment, which GoRex's panes would share, the conversation of the
pane's directory written last. Its dirs follow the process's
environment and its `--profile` and `--session-dir`. It answers
`{"ok":true}`, or the error, then a line of the manifest, then the
package. The source app (or CLI) opens both connections as it opens a
file transfer's (`openFile`), cancels by closing them, and pipes the
package from its server to the target's; it holds no files.

## The app

- **Teleport to…** in the pane's menu and the command palette, listing
  the connected hosts; disabled with the reason when the pane cannot
  go.
- A tab dragged onto another host's tab, or its header in the sidebar,
  is outlined there rather than placed, and dropped asks to teleport its
  pane there: a dialog with Cancel and Teleport, or the reason it cannot
  go (a tab of more than one pane, the pane's, the host's).
- While it runs, the pane shows a frosted overlay (`paintFrost`) with
  the step and the host; Cancel closes the connections, and the target,
  which looks at its connection before `create` and before it answers,
  removes what it made; the client kills the session by its id too, in
  case the answer crossed the cancel. A failure shows in the error banner,
  as other errors do: until dismissed, or the pane's next teleport goes.
- On success the pane is the remote session, in a tab of the host's: a
  tab's panes are all on one machine, so the session opens as a tab among
  the host's, named as the pane's tab was when the pane was its only one,
  and the pane closes, ending its session.
- `gorex teleport <pane> <host>` does the same from the CLI. The host is
  named as `-S` names one: by its name as Remote Hosts shows it (a saved
  host's, else a system Tailscale host's), or by its address; a name two
  hosts have is refused. The CLI pipes the package, telling this
  machine's server of the session before the host starts it
  (`pane.teleporting`, which the layout notes so that the window opens no
  tab of its own for it), then asks the server to show the pane as the
  host's session
  (`pane.teleported`, a local client's only), with or without a window:
  the host's tab goes after the pane's, active, and the pane goes, its
  session ending. It names the host by the address it dialed, which the
  server matches against the hosts the app keeps in `hosts.json` (it
  only reads it; a name may differ between them), and the server refuses
  an address none of them, or two, have. A pane of the new session the
  window placed meanwhile, as a host's new session, moves there rather
  than show twice. With no pane of the source in the layout, the CLI ends
  the source session itself; when the server cannot show the new
  session, both stay, and it says so and exits 1.

## Not in phase 1

- Merging work that went on at both ends: a teleport back to a
  directory changed since it left makes a new work tree there, and the
  user merges the branches.
- Cleaning `.gorex/teleport/` work trees: `git worktree remove` by hand,
  or a later "Clean up teleports" command.
- Agents other than Claude Code and omp (Codex keeps its conversations
  elsewhere): refused while they run; a shell goes.
