---
name: MOD-008
description: "How the GoRex gateway runs the actions of the Lua configuration file for Rex clients, and how an action asks the person a question. Use when you change client.action, client.keymap, server.config_reload or the gateway's prompt producer."
metadata:
  created: "2026-11-21"
  updated: "2026-11-21"
---

# MOD-008: Running config actions in the gateway

Status: built (#924, #931).

This doc decides that the GoRex gateway loads GoRex's Lua configuration and runs its actions for Rex clients. It extends [REF-005](REF-005-rex-api.md), and it builds the producer that [MOD-005](MOD-005-client-prompts.md) waits for. It follows [DOC-001](DOC-001-design-docs.md).

## Problem

`methodClientAction` in `internal/rexapi/clients.go` runs block methods only. A call for an action name without a dot fails with `not_found`. `methodClientKeymap` lists the terminal's block actions, not the file's. `methodServerConfigReload` counts a generation and reads no file.

The Lua file runs in the app (`luaconfig.go`, `internal/config`). No Rex client can list or run a GoRex action. No action calls `rex.ui`, so the prompts of revision 17 have no producer, as [MOD-005](MOD-005-client-prompts.md) says.

The user pays for it. An action such as "New worktree" works on the desktop and nowhere else. It asks for a branch name and opens a pane.

## Goal

- A Rex client can list the file's actions with `client.keymap` and run one with `client.action`.
- The gateway performs the built-in actions that it can, and names the ones that it cannot.
- `server.config_reload` reads the file again. A bad file keeps the last good configuration and returns diagnostics.
- An action can ask a question through `gorex.ui`, and Rex's apps show it (MOD-005).
- The app's own Lua behavior does not change.

## Non-goals

- `gorex.on` events in the gateway. It registers the handlers and emits none. A later doc can wire block events to them.
- `client.set_script` Lua. It stays unsupported.
- Actions on another server, and `client.perform`. They stay unsupported.
- Moving the app's own action execution to the gateway. The app keeps its runtime for its keys.
- Rex Go (iOS) prompts. Rex Go speaks GoRex's protocol, not the Rex API. A later IOS doc adds them, as agreed.
- A mapping of every app-only built-in. The gateway performs a named set, below.

## Design

### Behavior

- The person writes an action in `init.lua`, such as `worktree`, which asks for a branch name and opens a pane there.
- The Rex app's palette lists `worktree` from `client.keymap`. Its title and its args come from the file.
- The person runs it. `client.action` runs the action in the gateway.
- The action calls `gorex.ui.input`. The prompt reaches every eligible app through MOD-005. The person answers on the phone, and the action continues.
- The action calls `gorex.invoke("tab.new")`. The gateway opens a window with one shell block.
- An action calls `gorex.invoke("palette.open")`. The gateway answers `not supported by the gateway: palette.open`, and the client shows the message.
- `server.config_reload` finds a syntax error. The old actions stay usable. The result lists the error as a diagnostic.
- The person fixes the file and reloads. The new generation and hash go to every client in `keymap_changed`.

### Model

- The gateway holds one configuration: the loaded file, its generation, its SHA-256 hash, its diagnostics and its actions.
- The gateway loads the file with the sandboxed loader. The sandbox has no `os`, `io`, `require`, `dofile` or `loadfile`. A file that needs them at load time gets a diagnostic, and the old configuration stays.
- The gateway runs one action at a time, because the Lua state is not safe for two. Each action gets 30 seconds, as Rex's actions do.
- The action receives `ctx.origin` (`key`, `palette` or `api`) and its args. The gateway checks the args against the action's declared types before it runs the action.
- The gateway performs these built-in IDs in phase 2. Any other ID fails with the message above.

| Built-in ID | Gateway operation |
| --- | --- |
| `tab.new` | `session.new_window` with one shell block |
| `tab.close` | `session.close_window` |
| `tab.rename` | `session.set_window_label` |
| `pane.split.right`, `pane.split.down` | `session.new_split` |
| `pane.close` | `block.close` |
| `pane.focus.left`, `pane.focus.right`, `pane.focus.up`, `pane.focus.down` | `session.focus_direction` |
| `pane.zoom` | `session.zoom_block` |
| `pane.swap` | `session.swap_blocks` |

- `gorex.ui.input` and `gorex.ui.pick` use the prompt broker of MOD-005. The answer is the function's result, and a cancel is `nil`. A client at revision 16 or lower cannot answer, and the broker removes it from the set.
- `client.keymap` adds one layer named `init.lua`. Its bindings come from the file, with their args and modes. Its actions have kind `config`, the title, the description and the args schema. The existing terminal actions stay.
- `server.config_reload` counts the load, even a failed one, and reports the generation of the active configuration. `keymap_changed` carries the generation and hash of the active configuration.

### Code

- `internal/rexapi/actions.go`: the loaded configuration, the executor, the built-in mapping and the keymap layer.
- `internal/config`: expose the action metadata for `client.keymap` (already exported) and add `InvokeWithin(ctx, name, ctx, args)`. It keeps the 5-second `Invoke` for the app.
- `internal/rexapi/clients.go`: `methodClientAction` runs a dotless action from the configuration. `methodServerConfigReload` reloads the file through `actions.go`.
- `internal/rexapi/keymap.go`: `methodClientKeymap` adds the `init.lua` layer and its actions.
- `internal/rexapi/prompts.go`: the broker of MOD-005, as the `gorex.ui` host.
- The loading, execution and reload share one mutex, so that a reload waits for a running action.

### Limits

- The sandbox differs from the app, which loads the same file with `os` and `io`. An action that reads an environment variable or a file at load time works in the app and fails in the gateway. The diagnostic names the line.
- The file is loaded twice, once in the app and once in the gateway. The sandbox keeps the gateway's load free of side effects. Top-level code still runs twice, so the doc tells authors to keep the file to definitions.
- The gateway runs a built-in on the caller's connection, so the caller must be attached to the session that the action names. A Rex client that shows the session is attached.
- The gateway maps eight built-in IDs. An action that needs an app-only action fails. The list can grow without a protocol change. `session.float_block` is not implemented by GoRex, so `pane.float` is not in the set.

## Alternatives

- **Run a broker with no producer**: the revision 17 code exists and nothing calls it. Lost.
- **Show prompts in GoRex's app only**: a Rex client cannot ask, and the phone cannot answer. Lost.
- **Run the file with full libraries in the gateway**: user code gets the server's file and process access at load time. Lost.
- **Perform every built-in through a bridge to the app**: the app may be closed, and the mapping doubles the app's command layer. Lost.
- **Move all action execution to the gateway**: the app's keys need a runtime that works with its windows. A later change can unify them. Lost for this doc.

## Security

- Any client that a listener admits can run the file's actions, as on a Rex server. The actions create and close sessions, which the same clients can already do. The gateway performs no shell command except through a built-in that starts a shell.
- The sandbox stops the gateway's load and run of the file from touching files or processes. The app still loads the same file without the sandbox, as the same user.
- A prompt reaches every eligible app, including apps of another user or on another listener (MOD-005). An answer is checked against the offered items.
- A listener that admits untrusted clients makes both actions and prompts unsafe. The method notes state this fact.

## Migration

- No saved format changes. The gateway reads the file that the app already reads.
- The protocol shapes of `client.action`, `client.keymap` and `server.config_reload` do not change. Their behavior does: an action that returned `not_found` can now run. The revision stays 19.
- The prompt events stay at revision 17, gated as MOD-005 says.
- This changes how an existing method behaves, so [irreversible.md](irreversible.md) applies. The options are:
  1. **Recommended.** Run the actions for every revision. An old client sees a success where it saw `not_found`, which is the point of the change.
  2. Gate the actions behind a new API revision. Old clients keep `not_found`, and every client pays a bump.
  3. Gate the actions behind a setting that is off by default. Two behaviors to test, and Rex clients see an empty action list by default.
- Check after the change: a Rex client lists the action, runs it, and a mapped built-in takes effect. A client at 16 cannot answer a prompt. A bad file leaves the old generation active.

## Tests

- A loader test reads a file, reports its actions, bindings and modes, and rejects a syntax error while the last good configuration stays.
- A test runs `client.keymap` and finds the action with its title and args, and a binding with its keys and mode.
- An action that invokes `tab.new` opens a window. An action that invokes `palette.open` fails with the message that names the ID.
- Args outside the declared types fail with `bad_request`.
- An action that runs longer than 30 seconds fails with `timeout`. Two actions from two connections run one after the other.
- `gorex.ui.input` returns the answer from a fake broker, and `nil` after a cancel. The prompt is sent only to clients at revision 17 or higher.
- The app's own configuration tests do not change.
- This change touches no item in [invariants.md](invariants.md).

## Phases

1. Load the file, list it in `client.keymap`, reload it in `server.config_reload`, and report diagnostics (#924).
2. Run the actions, check the args, enforce the deadline and map the eight built-in IDs (#924).
3. Give the actions `gorex.ui` through the MOD-005 broker (#931).
