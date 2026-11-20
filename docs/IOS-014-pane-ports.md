---
name: IOS-014
description: "Rex Go shows a pane's listening ports and opens one in the phone's browser, directly or through a temporary relay on the host. Use when you change the ports view, the ports request in the app, or the relay that opens a loopback port."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-014: A pane's listening ports on Rex Go

Status: accepted. Not implemented.

This doc adds a ports view to Rex Go for the open pane. It also adds a way to open a port in the phone's browser. It uses the server data that [MOD-004](MOD-004-terminal-ports.md) built. It extends [IOS-005](IOS-005-pane-switching.md) and follows [DOC-001](DOC-001-design-docs.md).

## Problem

A program in a pane can start a web server, such as `npm run dev` on port 5173. The server knows the port and the process that holds it (MOD-004). No GoRex client shows it, and Rex Go is no exception. The phone shows the terminal text. The person must read the port from the output and type an address.

Rex Go cannot open the port in its browser either. A server that listens on 127.0.0.1 is reachable only on the host, and the phone is not the host.

## Goal

- Rex Go shows a port chip on a pane that listens, and lists each port with the process that holds it.
- The list shows when the answer is partial, and when the platform cannot list ports.
- A port that the phone can reach opens in Safari with one tap.
- A port that only the host can reach opens through a temporary relay on the host, when the phone shares the host's network.
- The list refreshes while it is open.

## Non-goals

- The whole process tree of the pane. MOD-004 reports the processes that hold a port. A later doc can add the tree.
- A public address, Tailscale Funnel, or an internet-facing tunnel.
- Rex's tunnel blocks and `rex forward`. They serve Rex clients, not Rex Go.
- Opening a port when the phone has no route to the host's address, such as a phone away from the LAN with no system Tailscale. A later doc can carry the page over Rex Go's own connection.
- Changes to the Rex API.

## Design

### Behavior

- A small chip appears above the keyboard of a pane that listens, such as `5173` or `2 ports`. It is absent when nothing listens. A tap opens the ports sheet.
- The session sheet gains a **Ports** row. It shows the count, such as `2 ports`, or `Ports` when none are open.
- The row opens a sheet for the pane that is open. Each line shows the port, the address, the process name and the process ID, such as `5173 · 127.0.0.1 · node (81301)`.
- A port on an address the phone can reach opens Safari with one tap. The line shows the address it will open.
- A port on 127.0.0.1 or ::1 shows `Only on the host`. A button, **Open on the phone**, creates the relay and opens the address it returns.
- The chip fetches when the pane appears and every five seconds while the terminal shows. The sheet fetches every two seconds while it is open.
- The list shows `No listening ports` when it is empty. It shows `Some processes could not be checked` when the status is `partial`. It shows `This host cannot list ports` when it is `unsupported`.
- A pull refreshes the list. The sheet fetches again every two seconds while it is open, and stops when it closes.
- Closing the sheet closes a relay that the sheet opened, unless the person chose **Keep open**.

### Model

- The app asks the host for the pane's ports with the server's existing `ports` request. The answer holds `status`, `ports` and `processes`, as MOD-004 describes. The app matches each port to its process by `pid`.
- Reachability uses the port's address:
  - `0.0.0.0` or `::` means every address. The app uses the host address that its own connection uses, such as the LAN address or the tailnet name it dialed.
  - A loopback address, `127.0.0.1` or `::1`, is not reachable. The app offers the relay.
  - Any other address is used as it is. A link-local IPv6 address is not offered.
- The URL is `http://ADDRESS:PORT/`. The app opens it with `UIApplication.open`. The app shows the URL before it opens it, so the person can read it.
- The relay is a raw TCP listener on the host's address on the phone's network, which forwards to `127.0.0.1:PORT`. It handles any protocol, including a WebSocket that a development server uses.
  - The listener binds the host address the phone reached, not every interface.
  - The port is chosen by the system, so it is random.
  - The relay lives for 10 minutes, or 2 minutes after its last connection, whichever comes first.
  - The person can close it at once from the sheet. The host closes it when the app's host connection ends.
- A second tap on the same port reuses the open relay while it lives.

### Code

- `ios/GoRex/Rex/Host.swift`: a `ports(of:)` request and its decoded `Ports` and `Process` values.
- `ios/GoRex/Views/PortsView.swift`: the sheet, the rows, the status text, the refresh and the open action.
- `ios/GoRex/Views/SessionSheet.swift`: the **Ports** row, which names the open pane's session and block.
- `ios/GoRex/Views/TerminalScreen.swift`: the port chip, its count and its tap.
- `ios/GoRex/Rex/Host.swift`: `openPort`, which asks the host for the relay address and closes it.
- Phase 1 needs no server change: the `ports` request is in `internal/rex/server_ops.go`, and the client method is `Client.Ports`.
- Phase 2 adds a GoRex request, `session.open_port`, to `internal/rex/server_ops.go`. It takes the session, the block and the port, and answers `{url, expires_at}`. A second operation, `session.close_port`, ends it. The listener forwards bytes with `io.Copy` in both directions.
- The app never connects to the relay itself. It only shows the URL, and Safari fetches it.

## Alternatives

- **Live updates from `ports_changed`**: the server does not send ports events to remote subscribers, and the app is one. Polling for two seconds while one sheet is open costs less than a protocol change. A later change can send the events.
- **An HTTP reverse proxy on the host**: it adds paths, headers and a WebSocket upgrade to write and test. A raw TCP relay is smaller and handles every protocol.
- **Opening the page inside Rex Go**: the app would need a stream for the page over its own connection and a web view. It is the only way when the phone has no route, and it is a doc of its own.
- **Only a copy button**: it leaves the person to paste an address that a loopback port does not have. Lost.

## Security

- The relay is reachable by every device on the host's LAN. The random port is not a secret that survives a scan. The sheet says so before it starts one.
- It binds one host address, not every interface, and it ends after 10 minutes or 2 idle minutes.
- It carries the protocol as it is. A development server that lets anyone run code is as open as its own settings make it.
- The relay needs no new credential. The app's request travels on the existing host connection.
- A later remote relay must add a credential, because the phone and the host no longer share a network.

## Migration

- There is no saved data and no Rex API change. The app's request and the relay are GoRex's own.
- An older server answers the relay request with an error, and the sheet shows `Update the host to open this port`. The list works on an older server, because the `ports` request is from revision 15.
- A server older than revision 15 shows `Update the host to list ports`.

## Tests

- A unit test builds the URL from each kind of address: `0.0.0.0`, `::`, a LAN address, 127.0.0.1 and a link-local IPv6 address.
- A host test with a session that listens on 127.0.0.1 checks the list, the process name and the ID.
- A relay test opens a relay to a test listener and checks that bytes flow both ways, and that the relay closes at its limit.
- A UI check on the simulator, and then on the Mac mini, shows the sheet, the empty state, the partial state and the open action.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The ports request in the app, the chip, the sheet and the refresh. No server change.
2. Opening a reachable port in Safari, and the copy action.
3. The loopback relay on the host, with its close and its limit.
4. A later doc: opening a page when the phone has no route to the host.
