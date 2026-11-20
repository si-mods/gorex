# Teleport: moving a pane's work to another host

Status: phase 1 built (#199, #217, #233). The items in "Not in phase 1" are not built.

Teleport moves the work of a pane to another machine. The work is the code that the pane works on, the conversation of the coding agent, and what the terminal showed. The process does not move, because no OS that GoRex runs on can migrate a live process with its PTY. GoRex packs the work on the source and unpacks it on the target. GoRex then starts the program again on the target. This is how `claude --teleport` brings a web session to a local checkout.

After a teleport the pane is the new session on the target host. It is in a tab of that host (see The app). The source session ends.

## Non-goals

Phase 1 does not do these things:

- Merging work that went on at both ends. A teleport back to a directory that changed since the work left makes a new work tree there. The user merges the branches.
- Cleaning the `.gorex/teleport/` work trees. The user runs `git worktree remove` by hand, or a later "Clean up teleports" command does it.
- Agents other than Claude Code and omp (Codex keeps its conversations elsewhere). GoRex refuses these agents while they run. A shell goes.

## Design

### Scope

Phase 1 teleports a pane from this machine to a host. The pane must meet these conditions:

- The foreground of the pane is a coding agent that GoRex knows (Claude Code or omp (oh-my-pi)), or a shell at its prompt.
- The pane is inside a Git work tree.
- The two hosts are hosts that GoRex already connects to (paired over tailcat, or system Tailscale).

GoRex refuses anything else with a reason:

- A pane of another host. Only the server of this machine packs.
- A directory that is not a Git work tree (nothing to rebuild the directory from). A repo with no commit, or with no remote URL (nothing to clone), is also refused.
- An agent that is working. Its conversation file is still being written. Its status says working, or it printed in the last two seconds, as its spinner does. The user waits for the agent to be idle, or stops it first.
- Another program in the foreground (a server, an editor). The state of the program is in its process.
- A session that started with a program and not with a shell (`gorex new -- ping`). The program is its own process, and the target would make a shell of it. This includes an agent that started this way (`gorex new -- omp`), because GoRex finds the conversation of an agent only under a shell.
- A target whose server is too old to have the `teleport` op.

Windows hosts are in. Either side can be macOS, Linux or Windows. The target handles what differs, with its own rules:

- The path of the work tree (`%USERPROFILE%\repos`…).
- The directory name of the conversation. The slug of Claude Code for `C:\Users\…` is `C--Users-…`.
- The resume command, quoted for the shell of the target pane (PowerShell, cmd, POSIX, fish). These are the quoters that `resume.go` had, now in `internal/rex/quote.go` so that the server can type the command.

Line endings are the business of Git. The patch is binary and Git applies it. Git honours the `core.autocrlf` of the target clone.

### What is packed

A package is one tar stream. Its manifest travels before it, in the line that asks for it (see Protocol). The manifest holds these items:

- The version, the name of the source host, and the program (`claude` / `shell`).
- The session id.
- The remote URLs of the repo (the URL of origin first), the branch, and HEAD.
- The root of the work tree on the source (for the `cwd` of the conversation).
- The directory of the pane, relative to that root.
- The root of the work tree, relative to the home of the source (when the root is under it).
- The terminal size.
- The link of the hop, and the links of earlier hops of the work tree (see Round trips).
- The length of the package.

| Part | Content |
|---|---|
| `repo.bundle` | `git bundle` of the commits reachable from HEAD that the remotes do not have. GoRex decides this by what `git ls-remote` answers now for the heads and tags of the remotes. A remote-tracking ref may be stale, for example a branch removed after a squash merge. When no remote answers, as offline, GoRex uses the remote-tracking refs. The bundle is empty when HEAD is pushed |
| `worktree.patch` | `git diff --binary HEAD`: staged and unstaged changes |
| `untracked.tar` | the regular files that `git ls-files -o --exclude-standard` lists. Ignored files and links stay behind |
| `claude/<uuid>.jsonl` | the conversation, and its `<uuid>/` directory (subagents, tool results) when there is one |
| `claude/file-history/<uuid>/<hex>@v<n>` | the backups that `/rewind` restores files from (`<config>/file-history/<uuid>/`). Only regular files of that name travel, and of those only the files outside the work tree |
| `omp/<time>_<id>.jsonl` | the conversation of omp, the directory of the same name beside it (its artifacts, as `109.bash.log`), and `omp/blobs/<sha256>[.ext]`. The blobs are the blobs of its data dir that the conversation quotes (`blob:sha256:<hex>`) |
| `scrollback.txt` | the text of the pane (`format` text), its last MiB. The new pane shows it above the program, dimmed and without control characters |

Limits: GoRex refuses a package over 256 MiB, and an `untracked.tar` over 64 MiB. So a stray build output does not cross the network. Secrets in untracked files (`.env`) travel. The data plane is WireGuard end to end, and the target is a host that the user already trusts with commands.

### Unpacking on the target

0. **Back where it came from.** A teleport can come back to a work tree that it left, untouched since. It lands there and not in a new work tree (see Round trips). GoRex then skips steps 1 and 2.
1. **Find the repo.** The target looks for a local clone whose remote URL matches one of the URLs in the manifest. The `https://`, `ssh://` and `git@host:` spellings of one repo match. The target looks in the directories where the user works on the target, as GoRex knows them:
   - The directories of the sessions of the server, live and recorded in its journal.
   - The `cwd` of each project of Claude Code in the config dir of the target. For each project, GoRex reads one conversation and takes the first line that has a `cwd`.

   GoRex resolves each directory to its Git work tree and repo by reading `.git`. The `.git` file of a linked work tree and its `commondir` lead to the repo. The main work tree of that repo is the clone. GoRex reads the remotes from the `config` file of the repo. It runs no `git` for each directory. It does not follow includes and `insteadOf`. GoRex searches no fixed directories. If it finds no clone, it clones the first URL to the place where the source has its work tree, relative to its home directory. For example, `~/work/x` on a Mac lands in `%USERPROFILE%\work\x` on Windows. If a clone of the repo is already there, GoRex takes it as it is. If that path is another directory, GoRex clones to `~/<repo name>`. GoRex does the same when the work tree of the source was outside its home. It adds `-2`, `-3`… when the name is taken. If there is no URL (a repo never pushed), GoRex refuses.
2. **Make a work tree.** GoRex never touches the own checkout of the clone. It fetches the bundle, then runs `git worktree add -b teleport/<short-id> <clone>/.gorex/teleport/<short-id> <HEAD>`. When HEAD is on the remote and the clone lacks it, GoRex fetches it first. GoRex adds `/.gorex/` to the `.git/info/exclude` of the clone, so the status of the clone does not list the work trees.
3. **Apply the changes.** GoRex runs `git apply --binary worktree.patch` in the work tree (staged changes arrive unstaged). Then it unpacks `untracked.tar`. It takes only regular files. Each file must be new, at a relative path reached through no link. A failing apply aborts and removes the work tree and its branch. Any failure after it does the same.
4. **Place the conversation.** GoRex rewrites the `cwd` field of every line of the jsonl from the source path to the target path. It writes the result to the Claude config dir of the target, under `projects/<slug(target cwd)>/`. The slug is the slug of Claude Code. Every character other than a letter or digit becomes `-`. GoRex does not follow the shortening that Claude Code does for very long paths. The config dir is the `CLAUDE_CONFIG_DIR` of the target server, else `~/.claude`.

   The conversation of omp goes the same way, under the data dir of omp. GoRex keeps the `<time>_<id>.jsonl` name of the file. It puts the file, and the directory of that name beside it, into `sessions/<omp's slug of the target cwd>/`. It rewrites the `cwd`. The slug of omp comes from its `session-paths.ts`:
   - It starts from the real path.
   - A path under the temporary directory becomes `-tmp-` and the path from it.
   - A path under home becomes `-` and the path from it.
   - Any other path becomes `--<path>--`.
   - Each `/`, `\` and `:` becomes `-`.

   Examples: `~/repos/gorex` is `-repos-gorex`, and `C:\x` is `--C--x--`. The data dir comes from `dirs.ts` of omp, applied to the environment of the target server:
   - `~/.omp/agent` is the default.
   - `PI_CONFIG_DIR` replaces `.omp`.
   - The directory of a profile (`OMP_PROFILE`, `PI_PROFILE`) is under `profiles/`.
   - `PI_CODING_AGENT_DIR` applies when there is no profile.
   - `$XDG_DATA_HOME/omp` applies when it is there.

   The blobs merge into the `blobs/` of omp, as the file history of Claude Code does (below). The `id` of a line is a message when the line is part of the conversation. The round trip rule below uses it, as it uses `uuid` for Claude Code.

   A copy of the same uuid can already be here, in any project. This is what a round trip leaves. The conversation went A → B, the user went on at B, and now the conversation comes back to A. A still has the copy that it sent. The source keeps its file. After a teleport the file is an older version, and nothing on the source changes it. GoRex compares the copies by their messages:
   - For Claude Code, the messages are the `uuid` of the `user` and `assistant` lines.
   - For omp, the messages are the `id` of the `message`, `custom_message`, `branch_summary` and `compaction` entries. omp makes these entries the context of the model.

   The other lines do not count. These are summaries, snapshots, and what an agent notes of itself. One example is the `session_exit` that omp appends to the copy on the source once its pane ends after the teleport. There are two cases:
   - Every message here is in the incoming copy (the same, or the conversation went on elsewhere). The copies here move to a backup in the config dir. This holds each `<uuid>.jsonl` and its `<uuid>/` directory. GoRex writes the incoming copy as above. The backup goes once the session starts. A failure before then moves the copies back, as they were.
   - A message here is not in the incoming copy. The conversation went on at both machines. GoRex refuses the teleport ("the conversation went on here too") and changes nothing.

   GoRex decides this once the package is in. The request carries no message ids.

   File history, for `/rewind`: GoRex changes the file history in the same pass as `cwd`.
   - In each `file-history-snapshot` line, a `trackedFileBackups` key under the work tree of the source becomes the same path under the work tree of the target. The path uses the separators of the target.
   - GoRex drops a key outside the work tree. A rewind on another machine must write nothing outside the work tree. The source does not send the backups that only such keys name.
   - A relative key stays.
   - GoRex writes anew only the lines that change.
   - The backups go to `<config>/file-history/<uuid>/`. GoRex adds those that are not there. A backup that is there already must be the same. If it is not, the history went on there too, and GoRex refuses the teleport.
   - A failure removes only what GoRex added.
5. **Start the session.** GoRex runs `create` with the target directory and the size of the pane. The session id is the link of the hop. By then the window of the source knows the link. The window waits for the session and does not open it as a tab of its own. The server of the target gives the session a tab there, as it does for a remote client. Then GoRex types `claude --resume <uuid>`, or `omp --resume <id>`, into the shell (`ClaudeResume`, `ompResume`). So the shell stays when the agent exits, as on the source. GoRex cannot quote for some shells (nushell). It refuses these shells before it sends the package. A shell pane receives only its directory.

   The target sends the answer once the session runs. A client can be gone by then, for example after a cancel. In that case nothing starts. The session ends and GoRex undoes what steps 0 to 4 made. The target closes the connection after it keeps what it made.

The target answers with the id of the new session. The source app re-points the pane to it, then kills the source session. Until the answer comes, nothing on the source changes. So a failed teleport leaves the pane as it was.

### Round trips

Each successful hop has a link, a random id. Each server keeps the hops that it saw in `teleports.json` in its directory (readable by the user only). Each record is `{link, dir, departed, at}`. `dir` is the root of a work tree. `departed` is the fingerprint of the work tree as the work left it. It is "" for a work tree that the work landed in. The fingerprint is a SHA-256 of HEAD, the `git diff --binary HEAD`, and the names and contents of the untracked files. This is what a package takes of the work tree.

- The source records `{L, its root, fingerprint}` as it packs, because nothing tells it of the end of the hop. The manifest carries `L` and the links that the source recorded for the same root (those of earlier hops).
- The target records `{L, the work tree it landed in, ""}` once the session starts.
- The target looks up the earlier links of the manifest in its own records. It looks for a directory that the work left by one of these links. The directory must still be a work tree of a clone of the repo. Its fingerprint must still be the fingerprint that it left with, untouched since. The most recent such directory, if any, takes the work:
  1. GoRex fetches the bundle or the remote into its repo, as step 2 does. Its HEAD must be an ancestor of the incoming HEAD. If not, a new work tree takes the work.
  2. GoRex keeps all that the directory has, ignored files aside, as a commit of a temporary index (`add -A`, `write-tree`, `commit-tree` on HEAD) under `refs/gorex/teleport-backup/<L>`, and its index file. GoRex never uses the stash.
  3. GoRex removes the untracked files of the directory (they are in the backup). Then it runs `git reset --hard` on its branch, or detached HEAD, to the incoming HEAD. This is a fast forward.
  4. GoRex applies the changes and the untracked files, as step 3 does.

  A failure after the move puts the directory back as it was:
  - `reset --hard` to its old HEAD.
  - The incoming untracked files are removed.
  - `read-tree -u --reset` of the backup.
  - Its index file comes back.
  - The backup ref is removed.

  On success GoRex removes the ref. The record that the work left by receives `departed` "". Nothing is left to go back to.
- GoRex never touches a directory that the user changed since the work left it. A new work tree takes the work, as on a first hop.

Example: `alice-laptop:~/repos/x` → `bob-desktop` (new work tree W) → `alice-laptop` continues in `~/repos/x`. Also `bob-desktop` → `alice-laptop` → `bob-desktop` again in W. Round trips leave no work trees behind. The conversation follows step 4, in the project of the directory where it lands.

### Protocol

There is one new server op, `teleport`. The server allows it to remote peers that may `create` (it runs a command, as `create` does), and only to them. It runs on a connection of its own, as a file transfer does (`file.put`). The server routes it by its first line. The server drops the connection of a remote peer that sends or takes nothing for 30 seconds while the server waits on the peer. The request carries the manifest and the length of the package. The server answers `{"ok":true}`, or refuses at once a manifest that it does not take. The package then follows as raw bytes. A framed attach sends its snapshot the same way after a header line (`SnapshotHeader`). The reply is a `Response` whose data is the new `SessionInfo`, or an error that names the step that failed. A server that has the op says `teleport` in the capabilities of its hello. For a server that is too old, the menu names the problem.

Packing happens on the own server of the source (op `teleport.pack`, local socket only, on a connection of its own). That server has the directory of the session, the process of the agent, and its config dir (`AgentSession`, `AgentConfigDir`).
- For Claude Code, the conversation is the one that its `sessions/<pid>.json` names.
- For omp, the conversation is the file that `terminal-sessions/<tty>` in its state dir records for the terminal of the pane. The tty name is the name past `/dev/`, as `ttys008`. The directory is the directory of the pane.
- If there is no such record, as on Windows, GoRex uses the conversation of the directory of the pane that omp wrote last. On Windows omp keys a terminal by the environment of its emulator, which the panes of GoRex would share.
- The dirs of omp follow the environment of the process and its `--profile` and `--session-dir`.

The server answers `{"ok":true}`, or the error, then a line of the manifest, then the package. The source app (or CLI) opens both connections as it opens the connection of a file transfer (`openFile`). It cancels by closing them. It pipes the package from its server to the server of the target. It holds no files.

### The app

- **Teleport to…** is in the menu of the pane and in the command palette. It lists the connected hosts. It is disabled, with the reason, when the pane cannot go.
- The user can drag a tab onto the tab of another host, or onto its header in the sidebar. The app outlines the target there and does not place the tab. When the user drops it, a dialog asks to teleport its pane there. The dialog has Cancel and Teleport. When the pane cannot go, the dialog shows the reason. The reason can be a tab of more than one pane, the pane, or the host.
- While the teleport runs, the pane shows a frosted overlay (`paintFrost`) with the step and the host. Cancel closes the connections. The target looks at its connection before `create` and before it answers, and it removes what it made. The client also kills the session by its id, in case the answer crossed the cancel. A failure shows in the error banner, as other errors do. It stays until the user dismisses it, or until the next teleport of the pane.
- On success the pane is the remote session, in a tab of the host. All panes of a tab are on one machine. So the session opens as a tab among the tabs of the host. It has the name of the tab of the pane, when the pane was its only pane. The pane closes and its session ends.
- `gorex teleport <pane> <host>` does the same from the CLI.
  - The host is named as `-S` names a host. Use its name as Remote Hosts shows it, or its address. The name is the name of a saved host, else the name of a system Tailscale host. GoRex refuses a name that two hosts have.
  - The CLI pipes the package. It tells the server of this machine about the session before the host starts it (`pane.teleporting`). The layout notes this, so the window opens no tab of its own for the session.
  - The CLI then asks the server to show the pane as the session of the host, with or without a window. This is `pane.teleported`, for a local client only. The tab of the host goes after the tab of the pane, active. The pane goes and its session ends.
  - The CLI names the host by the address that it dialed. The server matches this address against the hosts that the app keeps in `hosts.json` (the server only reads it, and a name may differ between them). The server refuses an address that none of the hosts has, or that two of them have.
  - The window can place a pane of the new session meanwhile, as a new session of a host. That pane moves to the new tab and does not show twice.
  - With no pane of the source in the layout, the CLI ends the source session itself. When the server cannot show the new session, both sessions stay. The CLI says so and exits 1.
