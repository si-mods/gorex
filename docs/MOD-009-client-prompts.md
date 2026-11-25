---
name: MOD-009
description: "An action on the desktop asks a question, and every GoRex client that can show it does; the phone answers the gateway's actions through the Rex API broker. Use when you change gorex.ui, the server's prompt broker, or the prompt sheet on a client."
metadata:
  created: "2026-11-21"
  updated: "2026-11-25"
---

# MOD-009: Questions across GoRex's clients

Status: built (#959). The radio and check box rows and the reason for a disabled Send follow #1098.

This doc adds a prompt channel to GoRex's own protocol, and the desktop app's prompt sheet. It is the GoRex-protocol counterpart of [MOD-005](MOD-005-client-prompts.md), which serves Rex's clients, the phone among them ([IOS-017](IOS-017-prompt-sheet.md)). It follows [DOC-001](DOC-001-design-docs.md).

## Problem

`gorex.ui` exists in `internal/config`, but the app sets no host for it (`SetUI` is called only by the gateway). An action that asks a question in the app fails with `gorex.ui: no prompt host`.

The gateway's producer and broker, [MOD-008](MOD-008-gateway-actions.md) and MOD-005, serve the gateway's actions and Rex's clients. The app's actions run in the app, on Lua ([REF-002](REF-002-lua.md)), and their questions have no channel to the app's other clients. Rex Go is a Rex client of the gateway, so this doc's channel serves the desktop app's windows; the phone's questions come through MOD-005.

The user pays for it: an action such as "New worktree" runs on the desktop and cannot ask the person on the phone.

## Goal

- An action that runs in the app can ask a question, and it receives the answer.
- Every connected GoRex client that supports prompts shows the question, which is each window of the desktop app. The first answer ends it everywhere.
- A timeout, a cancel or a closed client ends the question for everyone.
- The question time does not count toward the action's time limit.
- A server that does not support prompts answers the action with a clear error.

## Non-goals

- Rex's own clients, the Rex API broker and the phone's sheet. MOD-005, MOD-008 and [IOS-017](IOS-017-prompt-sheet.md) keep their own.
- The web client. It reaches GoRex through the Rex API gateway, so a later doc can bridge the two brokers.
- An app-side action asking the phone. Its question needs the bridge below.
- Running config actions on the phone. This doc only answers their questions.
- Actions that the gateway runs. Their prompts stay with the Rex broker.
- A shared record of past answers.

## Design

### Behavior

- The person runs an action on the desktop, from a key, the palette or `gorex action run`. The action calls `gorex.ui.input` or `gorex.ui.pick`.
- Every GoRex client that can show the question does so. That is each window of the desktop app, and any later client that lists the capability.
- The person answers in one window. The other windows close the question, and the action continues with the answer.
- The window that asked shows its own question too, so one window is enough.
- A pick shows how many answers it takes, by the mark of its rows. A question that takes one answer (`multiple` is false) shows radio rows: one radio group, a round mark, role radio, "1 of 3" for assistive technology. The arrow keys choose the previous or next row. A question that takes several shows check box rows: a square mark with a check, role check box; the arrow keys move the focus and Space checks the row.
- While no row is chosen and the Other field is empty, Send is disabled and the sheet says why, in text beside it: "Pick an answer". An input has no such state.
- A person closes a question. The other devices close it, and the action receives a cancel.
- No device answers within the timeout. Every device closes it, and the action receives a timeout error.
- A device cannot show the question. It answers `not_supported`, and the other devices keep it.
- No connected client can show the question. The action fails at once with `no client can show the question`.

### Model

- The server keeps one question, with its options, its deadline and the places that show it. A place is a client connection.
- The server gives each connection its own call ID. The first answer wins, and every other connection receives `prompt_ended` with `answered`.
- A connection shows one question at a time. A busy connection is skipped. A reconnecting client is removed from a question that is open.
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
- `commands.go`, `layoutcmd.go`, `keymap.go`: the app runs a custom action off the main thread, so that the window draws while its question waits, and answers the command that started it without waiting for the answer.
- `commands.go`: the app sets the `gorex.ui` host to `askUI`, which opens the question on the app's server connection, and runs the action with `actionClock`.
- `actionclock.go`: the action's time, which pauses while a question is open.
- `promptsheet.go`: the question's panel over the window, an input or a pick, with Cancel and the confirm. A pick's rows are `ui.RadioBase` in a `ui.RadioGroup`, or `ui.CheckboxBase` in a vertical focus group, drawn with `choiceMark`. Return answers while the confirm is enabled.
- `internal/rex/client.go`: `DeclareCapabilities`, `PromptOpen` and `PromptReply`; `workspace.go` declares the capability before each hello, the upgrade's connection included, and shows the events.
- A server without the capability answers `prompt.open` with "unknown op", and the action fails with a message that names the server.
- A client that cannot show a prompt answers `unsupported` once, and the server removes it from the question.

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

- `TestPromptFirstAnswerWins`, with two clients: both receive the question, the first answer wins and reaches the asker, and the second client receives `ended` with `answered`; a late answer is refused.
- `TestPromptTimeout`, `TestPromptCancel`, `TestPromptUnsupported`, `TestPromptUnsupportedByAll`, `TestPromptPickChecks`, `TestPromptBusyClient` and `TestPromptSpecChecks` cover the rest of the model.
- `TestActionClockPauses`: a question's time does not count toward the action's, and the time it had left continues after the answer.
- `TestPromptPickOneShowsRadios`, `TestPromptPickSeveralShowsCheckboxes` and `TestPromptSendSaysWhyItIsOff` check the roles, "1 of 3", the arrow keys, Space and the reason beside a disabled Send.
- A run of the real app on the Mac mini, with a config action that asks, showed an input and a pick, and the action's Lua received the typed text, the chosen ID and `nil` for a cancel.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. The server's registry and protocol, the app's producer and the desktop sheet (#959).
2. The phone's sheet, with input and pick: [MOD-005](MOD-005-client-prompts.md) and [IOS-017](IOS-017-prompt-sheet.md).
3. A bridge from the app's channel to the gateway's Rex broker, so that an app-side action asks the phone, in a later doc.
