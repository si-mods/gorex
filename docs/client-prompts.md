# Asking the person a question

Status: proposal.

This doc settles GoRex's support for Rex API revision 17. At revision 17, an action asks the person a question and every app can show the question. The doc extends [rex-api.md](rex-api.md) and follows [design-docs.md](design-docs.md).

## Problem

Rex API revision 17 adds the events `client_prompt_requested` and `client_prompt_ended`. It also adds three answer shapes to `client.reply`. A config action calls `rex.ui`. The server then sends the question to every app that can show it.

GoRex has no producer. `methodClientAction` in `internal/rexapi/clients.go` runs block methods only. `client.set_script` refuses Lua. The gateway reads no configuration file. GoRex's Lua runs in the app, not in the server ([lua.md](lua.md)). No action calls `rex.ui`, so a broker alone is dead code.

## Goal

- A client at revision 17 receives a prompt, shows it and answers it.
- The first answer closes the prompt on every other client.
- A timeout or a stopped action closes the prompt.
- A client at revision 16 receives no prompt.
- A prompt exists only when an action asks for it.

## Non-goals

- A `gorex.ui` in GoRex's app-side Lua. That is a client feature.
- `client.perform` between GoRex clients. This doc does not change it.
- Prompts for actions on another server. Rex's `rex.server` calls are out of scope.

## Design

### Behavior

In this doc, an app is a client of kind `app`. An action is a `gorex.action` from the configuration file.

- A config action asks for a branch name. Example: the "New worktree" action.
- Every connected app that supports revision 17 shows the question. The Mac app shows a field with a title. The phone shows the same field.
- The person types a name on the phone. The action continues with the name. The prompt on the Mac closes.
- A device answers before the person on the Mac. That answer wins. The other devices close their prompts.
- The person closes the prompt. The action receives `cancelled`.
- No app answers within the timeout. The action receives `timeout`.
- A device cannot show the prompt. It answers `not_supported`. The other devices continue.
- An answer names an item that the prompt did not offer. The call fails with `bad_request`. The prompt stays open.

### Model

- A prompt holds a kind, a title, a placeholder, an initial value and a confirm label. It also holds a `multiple` flag, an `allow_other` flag and a list of items. The kind is `input` or `pick`.
- A prompt call holds a call id, the action name, a deadline and the connections that show it.
- A connection shows at most one prompt at a time.
- A connection is eligible when its client kind is `app`, its revision is 17 or higher, it is connected and it shows no prompt.
- The first answer, a cancel, a timeout or a stopped action ends the call. Every other connection receives `client_prompt_ended` with `answered`, `timeout` or `cancelled`.
- The action waits for the answer. The prompt time does not count in the action's 30-second limit.

### Code

- `internal/rexapi/prompts.go`: the registry, the fan-out and the answer checks.
- `internal/rexapi/clients.go`: `methodClientReply` accepts prompt results. `methodClientAction` runs config actions.
- `internal/rexapi/control.go`: sends the events to the eligible connections.
- `internal/config`: registers the `rex.ui` calls for input and pick. The action waits on the registry.
- `internal/rexapi/revisions.go`: an event gate at 17.
- `internal/rexapi/help.go`: the event schemas and the reply shapes.
- An error message longer than 4096 bytes is shortened with an ellipsis.

## Alternatives

- **Broker without a producer**: every path exists and no action calls it. Lost.
- **App-side prompts in GoRex only**: the phone cannot answer a question that the Mac asks, and a Rex app cannot show the prompt. Lost.
- **No revision 17 support**: the gateway stays at 16 and a Rex client at 17 sees an incomplete server. Lost.

## Security

- Rex sends a prompt to every connected app that a listener accepts. The set includes apps of another user and apps on another listener. The prompt shows the action name, the title and the items.
- Any eligible app can answer first. This is Rex's design. A listener with untrusted clients makes prompts unsafe. The method notes state this fact.
- The change adds no listener and no credential.

## Migration

- The change adds the events of revision 17. A client at 16 receives none.
- This change touches the wire protocol. The options from [irreversible.md](irreversible.md):
  1. **Recommended.** Ship the producer and the broker together. The gateway can serve revisions 18 and 19 before them. A server with no prompting action sends no prompt. A reply for a call that nothing waits for already receives `not_found`.
  2. Ship the broker first. Rejected: dead code.
  3. Ship app-side prompts only. Rejected: no compatibility with Rex.
- Check: a client at 16 never receives `client_prompt_requested`. A client at 17 shows a prompt and its answer reaches the action. A late answer receives `not_found`.

## Tests

- Two apps at revision 17: the first answer wins and the second app receives `client_prompt_ended` with `answered`.
- A test covers the timeout, the cancel, `not_supported`, an unoffered item, a wrong answer shape and a late reply.
- A client at 16 receives no prompt.
- An action without a prompt keeps its 30-second limit.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. Config actions in the gateway. This is the producer.
2. The prompt registry, `client.reply` and the tests.
3. The help entries. The revision number needs no change when 18 and 19 land first.

## Open questions

1. Does the gateway run `init.lua`, or does the app run it and ask the gateway to send the prompt? Option one gives Rex clients the GoRex actions. Option two keeps Lua in the app. The user decides, because the answer changes where Lua runs.
2. Which actions get prompts first? Candidates are the new-worktree action, a project picker and a destructive confirm. The author decides.
