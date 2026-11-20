# An event too large to send

Status: proposal.

This doc settles GoRex's behavior when one event is larger than one control message, at Rex API revision 18. It extends [rex-api.md](rex-api.md) and follows [design-docs.md](design-docs.md).

## Problem

`sendChanges` in `internal/rexapi/control.go` builds each event for a connection. When the message is larger than `maxMessage`, the server closes the connection with status 1009. A phone on a slow link loses every session that it shows, and it reconnects.

Rex revision 18 replaces the event with `event_dropped`. The connection stays open. A client at revision 18 reads the state that the event described.

## Goal

- A connection at revision 18 receives `event_dropped` with the same `seq` as the replaced event.
- A connection at revision 17 stays open and receives no replacement.
- An oversized request still closes the connection with status 1009.
- A client can read the session or the server state again.

## Non-goals

- The 8 MiB event backlog limit. It keeps its 1008 close.
- Request fragmentation. Rex's clients fragment large requests. The server accepts a request up to `maxMessage`.
- GoRex's own protocol.

## Design

### Behavior

- A pane prints a title of one megabyte. The title event is too large.
- A client at revision 18 receives `event_dropped` with the type `block_event`, the block, the creator and the name `title_changed`. The client calls `session.view` or `session.describe_block` and reads the new state.
- A client at revision 17 receives nothing for that event. The stream continues.
- A status record with a large message behaves the same way.

### Model

- The server consumes the sequence number. The replacement event carries the same `seq`.
- The replacement names the dropped event. It holds `event_type`, `session_id`, `block_id`, `creator_name`, `block_event_name` and `size`.
- `size` counts the encoded event without the control message.
- The server does not queue the dropped event. The rest of the burst continues.

### Code

- `internal/rexapi/control.go`: `sendChanges` builds the replacement when the encoded event is too large.
- `internal/rexapi/events.go`: `change` gains optional block fields for the replacement.
- `internal/rexapi/events_blocks.go`: `emitBlockEvent` sets the fields.
- `internal/rexapi/revisions.go`: an event gate at 18.
- `internal/rexapi/help.go`: the event description.

## Alternatives

- **Keep the 1009 close**: a revision 18 client expects the connection to stay open. Lost.
- **Drop the event for every revision**: a revision 18 client cannot learn that it missed an event. Lost.
- **Split the event into parts**: the control protocol has no part message. Lost.

## Migration

- A client at 17 keeps the connection and misses the event. A client at 18 receives the replacement. The revision 17 shapes are server events that no action of GoRex produces, so revisions 18 and 19 can land before a prompt producer exists.
- This change touches the wire protocol. The options from [irreversible.md](irreversible.md):
  1. **Recommended.** Follow Rex. Replace the event at 18 and send nothing at 17.
  2. Keep the close for clients below 18. This option contradicts Rex's documentation and costs a reconnect. Lost.
- Check: at 17 the connection stays open and no replacement arrives. At 18 the replacement arrives with the same `seq`. An oversized request still closes with 1009.

## Tests

- A test makes an event larger than `maxMessage`. `internal/rexapi/control_test.go` already makes one for the 1009 close.
- At 17 the connection stays open and receives no replacement.
- At 18 the replacement holds every field.
- An oversized request still closes with 1009.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The replacement, the block fields and the tests.
2. The help entry and the revision change to 18.
