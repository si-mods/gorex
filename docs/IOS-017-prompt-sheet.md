---
name: IOS-017
description: "Rex Go shows a question from one of the host's actions and answers it, as Rex API revision 17 defines. Use when you change the prompt sheet, the prompt events, or the reply the app sends."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# IOS-017: A host's question on the phone

Status: built (#957).

This doc adds the prompt sheet to Rex Go. The gateway already sends a question to every app that can show it, at Rex API revision 17 ([MOD-005](MOD-005-client-prompts.md)). The app ignored those events, so the phone could not answer. The sheet is the last part of the phone's client features: the ports sheet ([IOS-014](IOS-014-pane-ports.md)), the focus back ([IOS-015](IOS-015-focus-back.md)) and this sheet.

## Problem

A config action on the host calls `rex.ui.input` or `rex.ui.pick`. The gateway sends `client_prompt_requested` to every connected app at revision 17. Rex Go says kind `app` and revision 19, so it receives the event. Its `Host.apply` dropped the event with the other unknown types. `client.reply` has no caller. The person at the phone sees nothing while an action waits 300 seconds, and another device must answer.

## Goal

- A question from any host appears over the screen that is open, in the app's terms.
- An input shows its title, placeholder and first value. A pick shows its items with their subtitle, icon and detail.
- A pick answers several items when `multiple` is true, and text when `allow_other` is true.
- Cancel answers `cancelled`. The action receives it.
- The sheet closes when the host ends the question: another device answered, the time passed, or the action stopped.
- The answer reaches the action on the host.

## Non-goals

- Actions that the desktop asks its own protocol for ([MOD-009](MOD-009-client-prompts.md)). The phone reaches the host through the gateway's Rex API.
- Running actions on the phone. It answers their questions only.
- Prompt history or a notification for a question that arrived in the background.
- Promotion of the sheet over another sheet that is open. The question remains until its time passes.

## Design

### Behavior

- An action runs on the host and asks a question. The phone shows a sheet in about a second, over the host list or a terminal.
- The sheet names the action once, above the question.
- An input shows a text field with the placeholder and the value the action set. The confirm button carries the confirm label, or Send. The keyboard's return answers.
- A pick lists each item. A tap on one answers it alone, or adds it when `multiple` is true; a check marks it. Confirm answers the checked items in their order. With `allow_other`, an Other field answers its text instead, when it is not empty.
- Cancel, or the same question answered on another device, closes the sheet.
- The sheet for a host that disconnects closes.

### Model

- `Host.prompt` holds the one question the host asked, as `Prompt`: the call ID, the action, the deadline and the spec. A host shows no second question while one is open.
- The event's payload is the call ID, the action, `timeout_ms` and the spec: `kind` (`input` or `pick`), `title`, `placeholder`, `value`, `confirm`, `multiple`, `allow_other` and `items`, each an `id`, `title`, `subtitle`, `icon` (an SF Symbol), `detail` and `selected`.
- `client_prompt_ended` carries the call ID and a reason. The host clears the question when the ID matches.
- The answer is a `client.reply` on the connection that received the event: `{"text": …}` for an input or an Other answer, `{"ids": […]}` for a pick, `{"cancelled": true}` for a cancel.
- The app clears its question before it sends the answer. The server checks the answer and ends the call, then the other apps receive `client_prompt_ended` with `answered`.
- The presenter shows the first question of any host, one at a time. When a sheet closes, it shows the next.

### Code

- `ios/GoRex/Rex/Prompt.swift`: `Prompt`, its `Spec`, `Item` and `Answer`, and `Prompt.from`, which reads the event payload.
- `ios/GoRex/Rex/Host.swift`: `prompt`, the two events in `apply`, `answer`, which sends `client.reply`, and the clear on close.
- `ios/GoRex/Views/PromptSheet.swift`: the sheet and `promptSheet`, the modifier that shows the first question over any screen.
- `ios/GoRex/GoRexApp.swift`: the modifier on the app's list.
- No server change: MOD-005 built the events and `client.reply`.

## Alternatives

- **A full-screen cover**: it hides the terminal behind the question, which is not a screen the person chose. Lost.
- **Answer in the terminal's key bar**: a pick has items with subtitles and a long title, and the bar is full ([IOS-014](IOS-014-pane-ports.md)). Lost.
- **Wait for [MOD-009](MOD-009-client-prompts.md) and its own broker**: Rex Go reaches the host through the gateway's Rex API, which MOD-005 already serves. Lost.
- **Notify the phone in the background**: a question has a deadline of minutes, and a notification for a prompt that expires is worse than none. Lost.

## Security

- The gateway sends a question to every app that a listener admits. That is Rex's design ([MOD-005](MOD-005-client-prompts.md)).
- The sheet shows the action's name, the title and the items, as the action chooses them. It shows no path or secret of the host by itself.
- The answer travels on the host's connection, as every call does. The change adds no listener and no credential.

## Migration

- The events exist since revision 17, and the app already says revision 19. An older gateway sends no event, and no sheet appears.
- Nothing is saved. A question that was open when the app closed is lost with its connection.

## Tests

- `PromptTests` reads an input and a pick event, checks the fields, the default of five minutes, an unknown kind and a missing call ID, and the three result shapes.
- The simulator shows a sheet for a config action that calls `rex.ui.input`: the answer reaches the action, and a second app's sheet closes.
- `go run ./tools/verify` and the app's build for the simulator.

## Phases

1. The model, the sheet, the events and the reply (#957).
2. A later doc: a notification for a question that arrives while the app is away.
