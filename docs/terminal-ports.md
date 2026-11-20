# A terminal's listening ports

Status: built (#912).

This doc settles how GoRex reports the TCP ports that a terminal's programs listen on, at Rex API revisions 15 and 16. It extends [rex-api.md](rex-api.md) and follows [design-docs.md](design-docs.md).

## Problem

Rex's CLI and apps speak API revision 19. The GoRex gateway speaks revision 14. A Rex client that calls `com.superlogical.terminal.ports` receives `revision_too_low`. `rex forward open BLOCK` cannot list the ports of a pane.

The GoRex server knows the process facts of each terminal. `SessionInfo` in `internal/rex/proto.go` holds the process id, the foreground program, its directory and its status records. The server knows no listening socket. The gateway cannot answer the method and sends no `ports_changed` event.

## Goal

- A client at revision 15 receives the ports of a terminal, with the process that holds each port.
- A client at revision 16 receives `ports_changed` after a real change.
- A client at revision 14 receives no new method and no new event.
- A call does not wake an idle terminal.
- The server performs the scan and the gateway translates its answer.

## Non-goals

- UDP ports. Rex reports TCP ports only.
- Ports of programs that leave the terminal's process tree. GoRex has no cgroup or job object for each terminal today. A later doc can add one.
- A port display in GoRex's desktop and iOS apps. A later client change can use this method.
- `rex forward` and tunnel blocks. GoRex already has them.

## Design

### Behavior

In this doc, a pane is a terminal block of a session. A session is a space in GoRex.

- A user starts a development server in a pane. Example: `npm run dev` listens on 127.0.0.1:5173.
- The Rex app calls `ports` and shows the port. Or it follows `ports_changed` and shows the port when it opens.
- The Rex CLI runs `rex forward open BLOCK`. The CLI reads `ports`, then creates a tunnel for one port.
- A script waits for a port before the next step. It calls `ports` until the port appears, or it follows the event.
- A program stops. The next event omits its port.
- A program listens on 0.0.0.0. The client sees the address and can warn the user.

### Model

- A terminal has one port snapshot. An entry holds the protocol, the address, the port, the process id and `first_seen_at`. The snapshot holds the processes and a status.
- The status is `ok`, `partial` or `unsupported`. `partial` means that the server could not inspect some processes.
- The scan reads the processes in the terminal's process tree and the system socket table. It does not read the terminal emulator.
- The scan interval is 10 seconds after input or output. The interval grows to 5 minutes while the terminal is idle. A call, a new terminal or a change resets the interval to 10 seconds.
- An event carries the full snapshot. A client replaces its list. The server sends at most one event for each terminal each second. It also limits the events of all terminals together. A burst sends one event at the start and one event at the end.
- A scan that fails sends no event. An empty first snapshot sends no event.

### Code

- `internal/rex/server_ops.go`: a new `ports` request op. It answers from the last snapshot, or from a new scan when the snapshot is older than 250 ms.
- `internal/rex/ports.go`: the snapshot, the intervals and the event. `ports_darwin.go`, `ports_linux.go` and `ports_windows.go` hold the platform code.
- `internal/rex/events.go`: a new event kind `ports`.
- `internal/rexapi/gateway.go`: the pump accepts the `ports` kind.
- `internal/rexapi/blocks.go`: the `ports` block method. A large answer uses the blob store and `blob_read`.
- `internal/rexapi/events_blocks.go`: the `ports_changed` block event.
- `internal/rexapi/help.go`: the method entry and the event schema.
- `internal/rexapi/revisions.go`: `methodSince` 15 and an event gate at 16.
The process set is the descendants of `SessionInfo.PID`, found by parent process id.

| Platform | Process set | Listening sockets |
| --- | --- | --- |
| Linux | `/proc/<pid>/task/<tid>/children` | `/proc/net/tcp`, `/proc/net/tcp6` and the `socket:[inode]` links in `/proc/<pid>/fd` |
| macOS | `proc_listchildpids` | `proc_pidinfo(PROC_PIDLISTFDS)` and `proc_pidfdinfo(PROC_PIDFDSOCKETINFO)`, through libproc, which `internal/rex/proc_darwin.go` already loads |
| Windows | a Toolhelp snapshot | `GetExtendedTcpTable(TCP_TABLE_OWNER_PID_LISTENER)` |

## Alternatives

- **Scan in the gateway**: the gateway cannot see the process tree, and it repeats the server's work. Lost.
- **Add the ports to `SessionInfo`**: every session list read then carries socket data, also for clients that never ask. Lost.
- **Client polling only**: no `ports_changed`, and each client scans again. Lost.

## Security

- The method uses the block method rules. A client needs the session attachment and the access rule of its transport. The change adds no listener and no credential.
- The answer shows local addresses, process ids, process names, paths and directories. `com.superlogical.terminal.process` already shows names and directories. The new data is the socket address.
- A port on 0.0.0.0 is visible to every client that can call the method, also to a client on another host. The method notes state this fact.

## Migration

- The gateway serves revision 14. This work adds revisions 15 and 16. A client at 14 receives `method_not_found` for `ports` and no `ports_changed`.
- This change touches the wire protocol. The options from [irreversible.md](irreversible.md):
  1. **Recommended.** Land both revisions in one pull request, then set `api_revision` to 16. Old clients keep the shapes of revision 14.
  2. Land revision 15 first and revision 16 second. This option costs one more release and one more test run.
  3. Serve the method before the revision change. A client at 14 could call a method that its revision does not have. Lost.
- Check: a control connection at 14 receives `method_not_found`. A connection at 15 receives the answer. A connection at 16 receives the event. A build from `main` keeps its behavior.

## Tests

- Unit tests for each platform use recorded socket tables and process trees. They check `ok`, `partial`, IPv4-mapped IPv6 and link-local zones.
- An end-to-end test on macOS and Linux starts a program that listens on 127.0.0.1 in a session. It checks the port, the process id and `first_seen_at`. It stops the program and checks the next snapshot.
- A burst test checks the one-event-per-second rule and the final event.
- A revision test checks `method_not_found` at 14, the answer at 15 and the event at 16.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The server scan, the request op and the platform files (#912).
2. The gateway method, the event, the help entries and the revision change to 16 (#912).

## Open questions

1. Is a Windows scan part of phase 1, or does `status: unsupported` ship first? The reviewer decides after a Windows test.
2. Does `first_seen_at` name the scan or the socket start? Rex names the scan. Option: follow Rex. The author decides.
3. What is the cost on a host with many processes? Measure it before the merge. If the cost is high, increase the idle interval.
