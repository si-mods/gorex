---
name: MOD-009
description: "An action on the desktop asks a question, and every GoRex client, the phone among them, can show it and answer it. Use when you change gorex.ui, the server's prompt broker, or the prompt sheet on a client."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# MOD-009: Questions across GoRex's clients

Status: proposal.

This doc adds a prompt channel to GoRex's own protocol, and the phone's prompt sheet. It is the GoRex-protocol counterpart of [MOD-005](MOD-005-client-prompts.md), which serves Rex's clients. It follows [DOC-001](DOC-001-design-docs.md).

## Problem

`gorex.ui` exists in `internal/config`, but the app sets no host for it (`SetUI` is called only by the gateway). An action that asks a question in the app fails with `gorex.ui: no prompt host`.

The gateway's producer and broker, [MOD-008](MOD-008-gateway-actions.md) and MOD-005, serve Rex's clients. Rex Go is not a Rex client. It speaks GoRex's protocol, so it receives none of those questions.

The user pays for it: an action such as "New worktree" runs on the desktop and cannot ask the person on the phone.

## Goal

- An action that runs in the app can ask a question, and it receives the answer.
- Every connected GoRex client that supports prompts shows the question, the phone among them. The first answer ends it everywhere.
- A timeout, a cancel or a closed client ends the question for everyone.
- The question time does not count toward the action's time limit.
- A server that does not support prompts answers the action with a clear error.

## Non-goals

- Rex's own clients and the Rex API broker. MOD-005 and MOD-008 keep their own.
- The web client. It reaches GoRex through the Rex API gateway, so a later doc can bridge the two brokers.
- Running config actions on the phone. This doc only answers their questions.
- Actions that the gateway runs. Their prompts stay with the Rex broker.
- A shared record of past answers.

## Design

### Behavior

- The person runs an action on the desktop, from a key, the palette or `gorex action run`. The action calls `gorex.ui.input` or `gorex.ui.pick`.
- Every GoRex client that can show the question does so. That is each window of the desktop app, each connected phone, and any later client that supports prompts.
- The person answers on the phone. The other clients close the question, and the desktop action continues with the answer.
- The desktop app shows its own question too, so one device is enough.
- A person closes a question. The other devices close it, and the action receives a cancel.
- No device answers within the timeout. Every device closes it, and the action receives a timeout error.
- A device cannot show the question. It answers `not_supported`, and the other devices keep it.
- No connected client can show the question. The action fails at once with `no client can show the question`.

### Model

- The server keeps one question, with its options, its deadline and the places that show it. A place is a client connection.
- The server gives each connection its own call ID. The first answer wins, and every other connection receives `prompt_ended` with `answered`.
- A connection shows one question at a time. A busy connection is skipped.
- A connection is eligible when its hello lists the prompt capability, it is connected and it shows no other question.
- The deadline is the one the action asked for, from five minutes by default to one hour at most.
- The action's own deadline stops while its question is open, then continues with the time it has left.
- The answer is `text` for an input or an `allow_other` pick, `ids` for a pick, or a cancel. The server checks each against the question, as MOD-005 does.

### Code

- `internal/rex/prompts.go`: the registry, the eligibility, the deadline, the first answer and the ended events.
- `internal/rex/proto.go`: the hello capability, the event kinds and the two requests.
- The protocol messages are additive:
  - A client's `prompt.open` request carries the question and its timeout. Its response carries the answer.
  - The server sends `prompt_requested` to each eligible connection, with its call ID.
  - A client's `prompt.answer` request carries its call ID and the answer.
  - The server sends `prompt_ended` to the connections whose question ended.
- `luaconfig.go`, `commands.go`: the app sets the `gorex.ui` host to a call on the app's server connection. It runs the action with a clock that stops while a question is open. `internal/config` already has `UIOptions`, `UIAnswer`, `SetUI` and `InvokeWithin`.
- `ios/GoRex/Rex/Host.swift`: the prompt events and the `prompt.answer` request.
- `ios/GoRex/Views/PromptSheet.swift`: an input sheet or a pick sheet, with the items, the confirm label and the cancel.
- A client that cannot show a prompt answers `not_supported` once, and the server removes it from the question.

## Alternatives

- **Run config actions in the server, as the gateway does**: one producer and one broker for every client. The desktop's keys need the action to touch its windows, though. A later change can unify them. Lost for this doc.
- **Let the phone connect to the gateway as a Rex client**: a second protocol and a second identity on the phone. Lost.
- **Show the question only on the desktop**: the phone cannot answer, which is the point. Lost.
- **A question per device, with no first answer**: two devices can answer, and the action cannot choose. Lost.

## Security

- Every eligible client of the host shows the question, and any of them can answer it. These are the user's own connections: the local user, paired devices and the tailnet.
- A question can ask for a secret. It appears on every connected device until someone answers or the timeout passes. The sheet says which action asks.
- The channel adds no credential. It uses the host connection that already exists.
- A client that answers `not_supported` is removed, and the question stays with the others.

## Migration

- The protocol change is additive. A client that does not list the capability receives no question. A server that does not know `prompt.open` answers `unsupported`, and the action fails with a message that names the server.
- There is no saved format change. The app's action clock and the server's registry live in memory.
- The gateway's Rex prompts and this channel stay separate until a doc bridges them.

## Tests

- A server test with two clients checks that both receive the question, that the first answer wins, and that the second receives `prompt_ended` with `answered`.
- Tests for a timeout, a cancel, `not_supported` with one and with two clients, a wrong item and a busy client.
- An app test with a fake `gorex.ui` host checks that the action's deadline stops during a question and continues after it.
- An iOS unit test checks the event decoding and the answer's shape. A UI check on the simulator and the Mac mini shows an input and a pick.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The server's registry and protocol, the app's producer and the desktop sheet.
2. The phone's sheet, with input and pick.
3. A bridge to the web client through the gateway, in a later doc.

## Open questions

1. Does the phone ever start an action? Options: no, or a palette on the phone that calls `gorex action run` on a host. The user decides.
2. Does a question survive a client's reconnect? Options: no, the client is removed, or yes, with a new call ID. The author decides.
3. Is the default timeout five minutes? Options: two, five or ten. The user decides.
