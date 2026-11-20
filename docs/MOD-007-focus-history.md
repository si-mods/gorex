---
name: MOD-007
description: "The focus history of each principal and the method focus_history.reverse, at Rex API revision 19. Use when you change focus history or a back walk over panes."
metadata:
  created: "2026-11-20"
  updated: "2026-11-20"
---

# MOD-007: Returning to the previous pane

Status: built (#917).

This doc settles the per-principal focus history and the method `focus_history.reverse` at Rex API revision 19. It extends [REF-005-rex-api.md](REF-005-rex-api.md) and follows [DOC-001-design-docs.md](DOC-001-design-docs.md).

## Problem

Rex revision 19 adds `focus_history.reverse`. GoRex keeps no history. A close chooses a neighbor: `focusSiblingPane` and `focusSiblingWindow` in `internal/rexapi/layout.go` take the next pane or window. A client cannot ask for the previous one.

The gateway knows the principal of each connection (`internal/rexapi/origin.go`). It keeps no record of the panes, tabs or sessions that a principal visited.

## Goal

- A client at revision 19 can step back through the panes, tabs and sessions that its principal visited.
- A filter limits a step to one session, one tab, another session or another tab.
- A close of the focused pane or tab returns focus to the previous place.
- A client at revision 18 receives `method_not_found` for the new method.
- A principal cannot walk the history of another principal.

## Non-goals

- The close and focus rules of GoRex's desktop app. A later change can use the same model in the server.
- A history that survives a gateway restart.
- A record of the pane that each device shows. A session has one focus.

## Design

### Behavior

In this doc, a pane is a terminal block of a session. A tab is a Rex API window. A session is a space in GoRex.

- A person works in pane A, moves to pane B and closes pane B. Focus returns to pane A.
- A person moves to another session to answer an agent. One key returns to the session and pane that they left.
- A key runs `focus_history.reverse` with the `window_id` of the current tab. Focus moves to the last pane in that tab. A second press moves to the pane before it.
- The same key with `not_window_id` moves to the last tab of the session. With `not_session_id` it moves to the last session.
- The person focuses a new pane. The walk starts at the new focus.

### Model

The history key follows the principal.

| Principal | Key |
| --- | --- |
| Unix peer | `peer:<uid>`, or the client id when the system gives no uid |
| Built-in Tailscale | `tailscale:<user_id>` |
| Tailcat device | `client:<client id>`, because the principal holds no stable device id today |
| Anonymous TCP | `client:<client id>` |

- An entry holds `seq`, `session`, `window` and `block`. The history is a most-recent-first list with 64 entries and one counter.
- The gateway records an entry after a call from that principal that leaves a focused block. The calls are `session.focus_block`, `session.focus_window`, `session.focus_direction`, `session.focus_next_pane`, `session.focus_previous_pane`, `session.focus_next_window`, `session.focus_previous_window`, `session.new_split`, `session.new_window`, `session.new_block` and `session.new_layer`. The close calls are `session.close_window`, `session.close_layer`, `session.detach_block` and `block.close`.
- A place that is already the newest is not added again.
- `focus_history.reverse` runs these steps:
  1. Check the filters. An unknown `session_id` is `session_not_found`. A `session_id` without attachment is `conflict`.
  2. Start at the current focus of the principal, or at the focus of the model when the principal has no entry.
  3. Walk from the newest entry to the oldest. Skip an entry that is gone, that equals the current focus, that fails a filter or that is not older than the cursor.
  4. Focus the first entry that remains. Answer `reversed: true` with the session, the tab and the pane. If no entry remains, answer `reversed: false`.
- A close from that principal checks whether the closed pane or tab held its focus. If it did, the gateway focuses the newest entry that still exists after the close. If no entry exists, the choice of the server stays. The behavior covers `session.close_window` and `block.close`; `session.close_layer` and `session.detach_block` are not implemented by GoRex yet, and their behavior lands with them.

### Code

- `internal/rexapi/focus.go`: the history, the principal keys and the reverse walk.
- `internal/rexapi/control.go`: each control connection finds its history by its principal.
- `internal/rexapi/layout.go`: the focus calls and the close calls record an entry.
- `internal/rexapi/protocol.go`: `focus_history.reverse` joins the ordered methods.
- `internal/rexapi/methods.go`: the method table entry.
- `internal/rexapi/help.go`: the method entry.
- `internal/rexapi/revisions.go`: `methodSince` 19.

## Alternatives

- **History in the GoRex server**: the desktop gets the same behavior, but the change touches the server protocol and the current close rule. It needs its own design. Lost for this revision.
- **History for each connection**: two devices of one person do not share it. Rex tracks the principal. Lost.
- **Persist the history**: the entries name panes that a restart can rename. A restart starts empty. Lost.

## Security

- The history shows where a principal worked. A different principal cannot walk it.
- The tailcat key uses the client id. Two connections of one device do not share a history. This is a limit, not a leak.
- The change adds no listener and no credential.

## Migration

- The change adds the method at revision 19. A client at 18 receives `method_not_found`.
- This change touches the wire protocol. The options from [irreversible.md](irreversible.md):
  1. **Recommended.** Land revisions 15, 16 and 18 first, then this change, and set `api_revision` to 19. Old clients keep their shapes.
  2. Land this change first. The gateway advertises 19 before the port and event shapes. Lost.
  3. Keep the revision at 18 and serve the method. A client at 18 could call a method that its revision does not have. Lost.
- Check: a client at 18 receives `method_not_found`. A client at 19 walks two entries. A close from a focused pane returns to the previous pane.

## Tests

- Two principals: each one walks only its own entries.
- A test covers each filter, the cursor walk, a closed entry and a missing block.
- A `session_id` without attachment is `conflict`.
- A close from a focused pane returns to the previous pane.
- The method runs in order with the other ordered methods.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The history and `focus_history.reverse` (#917).
2. The close-focus behavior (#917).
3. The help entry and the revision change to 19 (#917).

## Open questions

1. Do 64 entries fit a long working day? Options: 64, 128 or no cap. A test with a busy session decides.
2. Does a restart keep the history? Options: no (the current design) or a file. The user decides.
3. Does the desktop use the same walk later? Options: yes (server history) or no (client rule). The user decides.
4. Does the tailcat principal get a stable device id? Options: add the device key id, or keep the client id. The author decides.
