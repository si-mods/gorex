---
name: DSK-016
description: "Agent-driven pane highlight and human takeover events: server actor tracking, a blue pulsing border, tab badges, immediate human takeover on typing, and takeover events on gorex events. Use when you change agent actor bookkeeping, takeover events, pane borders, tab badges, or CLI agent flags."
metadata:
  created: "2026-11-28"
  updated: "2026-11-28"
---

# DSK-016: Agent-driven pane highlight

Status: built.

This doc decides how GoRex marks panes that an agent or script drives through the command line or peer delegation, and how GoRex notifies the parent agent on `gorex events` when a person takes over. It extends [`REF-001`](REF-001-cli.md), [`REF-006`](REF-006-session-server.md), and [`MOD-016`](MOD-016-peer-delegation.md).

## Problem

Agents and scripts drive GoRex panes with `gorex new`, `gorex split`, `gorex send`, `gorex paste`, and `gorex delegate`. Two gaps exist when a person and an agent share a window:

1. **The person cannot see which pane an agent drives.** In a tab with several splits, a pane opened by a script looks the same as a pane opened by hand. The person cannot tell which pane an agent owns, whether the agent still sends input, or whether typing in the pane will collide with an active script. Scripts that read `gorex ls --json` also have no record of who created a session or when the CLI last wrote to it.
2. **The parent agent does not hear when the person takes over.** When a person types in an agent-driven pane in `session.admit` or `session.Key`, the server clears `agentActive`, but the server pushes no event on `gorex events`. Unless the parent agent polls `gorex ls --json` in a loop, it keeps sending commands into the shell while the person types.

## Goal

- Record actor state on each session on the server: `createdBy` (`"human"`, `"cli"`, or `"delegate"`), `createdAt`, `lastCliInputAt`, `agent`, `active`, `parentSession`, and `tookOverAt`.
- Stamp `lastCliInputAt` when the CLI creates a session (`gorex new`), starts a split with a command (`gorex split -- ...`), sends input (`gorex send`), or pastes text (`gorex paste`), and when peer delegation sends a prompt.
- Keep a **persistent** calm blue pulsing border (2.4 s period) and a top-right circular `sparkles` corner mark on the tab/sidebar program tile (with no text label) on every agent-created pane (`createdBy` is `"cli"` or `"delegate"`) as long as `tookOverAt` is empty and the session has not exited—never decaying over time across working or idle states.
- Clear the highlight permanently on genuine human UI input (keyboard typing or paste, which sets `tookOverAt` and clears `active`) or when the session exits/closes; reserve the 60-second CLI idle window (`57s..60s` 3 s fade-out) exclusively for human-created panes (`createdBy == "human"`) that a script wrote to once.
- Push one `"takeover"` event line on `gorex events` at the exact moment human UI input flips `active` from `true` to `false`, carrying the child session ID, the displaced `agent` label, and the `parentSession` ID.
- Expose `agentActor` on every session in `gorex ls --json` so scripts can audit ownership and catch takeovers that happened before they subscribed.
- Accept an optional `--agent <name>` flag on `gorex new`, `gorex split`, `gorex send`, and `gorex paste` (default `"cli"`) to label the tab badge.
- Provide `gorex config set agent-highlight on|off` (`"agentHighlight"` in `settings.json`) to turn the visual highlight off.

## Non-goals

- Locking the terminal against human input while an agent writes. Human input in the UI always reaches the PTY at once.
- Writing takeover notices into the parent agent's PTY stdin. Injecting text into a running TUI corrupts its prompt buffer. Parent agents and supervisor scripts read `gorex events` or `gorex ls --json` instead.
- Persisting ephemeral pulse state across cold machine reboots. In-place `gorex server upgrade` keeps live actor state in `handoff.json`, while a cold restart starts fresh shells as human sessions.
- Changing terminal foreground or background colors inside the PTY. Only the pane border and the tab badge show the mark.

## Design

### Behavior

#### Highlight lifetime and human takeover

1. **Agent-created panes stay highlighted (`createdBy` in `{"cli", "delegate"}` and `tookOverAt` empty)**: Any pane created by `gorex new`, `gorex split`, or `gorex delegate` keeps its blue border (`#2e6fd0` in light windows, `#6cb2ff` in dark windows, 2.4 s cosine wave; static with zero wakeup timers when Reduce Motion or `animation: "off"` is set) and a circular `sparkles` icon mark on the top-right corner of its tab/sidebar program tile (`drawAgentTileMark`, with no text pill) **persistently** for the life of the session. It does **not** decay after 60 seconds without CLI writes, whether the program inside is actively working or sitting idle at its prompt: `gorex delegate` delivers its prompt once at startup and the agent TUI then owns the pane autonomously until a person steps in.
2. **Only genuine human input sets `tookOverAt` and permanently clears the highlight**: Typing a key or pasting in the UI sets `tookOverAt`, flips `active` to `false`, and clears the border and badge on the next frame without waiting for the 500 ms session poll. Attach, focus/blur (`CSI I`/`CSI O`), mouse hover/motion/wheel/click/release (`SGR`, `URXVT`, `X10`, `doMouse`), terminal query replies (`DA`, `CPR`, `DSR`, `DECRPM`, `XTWINOPS`, Kitty flags, `OSC`/`DCS`/`APC`), clipboard read policy denials (`OSC 5522` `EPERM`/`DONE`), scrollback shortcuts, Kitty `:3` key-release or modifier-only events, and ordinary CLI writes never set `tookOverAt`.
3. **Session exit or close clears the highlight**: Once the child process exits (`Exited == true`) or the pane closes, the border and badge disappear immediately.
4. **The 60-second CLI idle window applies only to human-created panes (`createdBy == "human"`) touched by a script**: When a person opens their own shell and a helper script writes to it once via `gorex send` or `gorex paste` (`lastCliInputAt` non-zero, `tookOverAt` empty), the pane remains the person's terminal rather than a dedicated agent worker. Keeping a permanent blue border on a human shell after a one-off script write would be visual noise, so human-created panes fade out across the final 3 seconds (`57s..60s`) of the 60-second CLI idle window.

#### Takeover notification on `gorex events`

When a parent agent runs inside a GoRex pane, its shell already has `GOREX_SESSION` set to its own session ID ([`REF-001`](REF-001-cli.md)). When that parent agent runs `gorex new`, `gorex split`, `gorex send`, `gorex paste`, or `gorex delegate` against another session on the same server, the CLI sends `$GOREX_SESSION` as `parentSession`.

While the parent agent supervises one or more child panes, it (or its background watcher) runs `gorex events` (or `gorex -S HOST events` for a remote host). When a person types or pastes in an active agent-driven pane in any UI client, `gorex events` prints one JSON line:

```json
{"event":"takeover","takeover":{"sid":"627b1e4f7177","agent":"agy","createdBy":"cli","parentSession":"d03c44d2f696","via":"pty","lastCliInputAt":"2026-11-28T07:38:12.401Z","at":"2026-11-28T07:38:19.105Z"}}
```

To avoid missing a takeover that occurred between spawning the pane and opening the event stream, a supervisor follows the standard subscribe-then-check sequence:

1. Start `gorex events` (subscribe on the control connection).
2. Read `gorex ls --json` once and check `agentActor.active` and `agentActor.tookOverAt` for the target session.
3. Read lines from `gorex events` and stop driving `sid` when an event has `event == "takeover"` and matching `sid` (or `parentSession == $GOREX_SESSION`).

As a CLI convenience built on the same stream, `gorex wait -s <sid> --takeover [--timeout D] [--json]` blocks until the session is taken over by a person (or already has `!agentActor.active && !agentActor.tookOverAt.IsZero()`) and exits `0` with the takeover record.

### Model

#### `AgentActor` state

Each `session` keeps an `AgentActor` value exposed in `SessionInfo` as `agentActor`:

| Field | Type | Phase | Meaning |
| --- | --- | --- | --- |
| `createdBy` | `string` | 1 | `"human"` for window-created and restored panes, `"cli"` for `gorex new` and `gorex split`, `"delegate"` for `gorex delegate`. |
| `createdAt` | `time.Time` | 1 | Creation time of the session, used as the fallback highlight reference when a delegated or CLI shell has not stamped `lastCliInputAt` yet. Omitted when zero. |
| `lastCliInputAt` | `time.Time` | 1 | Time of the latest CLI or delegated input (`gorex new`, `gorex split` with a command, `gorex send`, `gorex paste`, `gorex delegate` prompt delivery). Omitted when zero. |
| `agent` | `string` | 1 | Short badge label from `--agent <name>` (default `"cli"`, or the delegated agent ID), trimmed to 24 runes. |
| `active` | `bool` | 1 | True while an agent created or wrote to the session and no human UI input has taken over since. |
| `parentSession` | `string` | 2 | Session ID of the caller's `$GOREX_SESSION` that last created or drove this session (when distinct from `sid`). Omitted when empty. |
| `tookOverAt` | `time.Time` | 2 | Time of the human input that last cleared `active`. Cleared back to zero when new CLI input re-arms `active`. Omitted when zero. |

#### Edge-trigger rules for `"takeover"` events

A `"takeover"` event is strictly edge-triggered on the server transition `active: true -> false` caused by human input:

| Action on the session | Sets `active` | Updates `tookOverAt` | Emits `"takeover"` event |
| --- | --- | --- | --- |
| Human types or pastes on attached PTY stream (`session.admit`) while `active == true` | `false` | `now` | Yes (`via: "pty"`) |
| Human sends key via `doKey` (`KeyInput` press/repeat) while `active == true` | `false` | `now` | Yes (`via: "key"`) |
| Human pastes via `PasteHuman` / `InputHuman` (ConPTY paste, file drop, mobile paste) while `active == true` | `false` | `now` | Yes (`via: "paste"`) |
| Further human keystrokes while `active == false` | stays `false` | unchanged | No (no per-keystroke spam) |
| Terminal query replies (`DA1`, `DA2`, `CPR`, `DECRPM`, `OSC`), clipboard read denials (`OSC 5522` `EPERM`/`DONE`), or focus reports (`CSI I`, `CSI O`) | unchanged | unchanged | No |
| Pointer motion, wheel, click/release (`SGR`, `URXVT`, `X10`, `doMouse`), Kitty key-release (`:3`) or modifier-only events, or `FocusReport` (`doFocusReport`) | unchanged | unchanged | No |
| 60-second CLI idle window elapses on a `createdBy == "human"` pane with no human input | stays `true` | unchanged | No (visual mark fades on human pane; nobody took over) |
| Time elapses on a `createdBy` in `{"cli", "delegate"}` pane with no human input | stays `true` | unchanged | No (visual mark stays resident on agent-created pane) |
| Process exits (`Exited == true`) | `false` in `info()` | unchanged | No (`"sessions"` event fires instead) |
| Later `gorex send`, `gorex paste`, or `delegate.send` | `true` | zeroed | No (re-arms for next takeover) |

Note on the 60-second rule: even if a person types 90 seconds after the last `gorex send` (after the visual border has already faded out), `active` was still `true` on the server if no human had typed yet, so `active` transitions `true -> false` and emits `"takeover"`. A background supervisor waiting on `gorex events` still learns that a person stepped into the pane.

### Code

- **Wire types (`internal/rex/proto.go`)**:
  - Advertise `CapabilityAgentTakeover = "agent.takeover"` in `Hello.Capabilities` (`ProtocolVersion = 3`, additive).
  - Add `ParentSession string` (`json:"parentSession,omitempty"`) to `CreateOptions`, `LayoutCommand`, and `Request`.
  - Add `ParentSession string` (`json:"parentSession,omitempty"`) and `TookOverAt time.Time` (`json:"tookOverAt,omitzero"`) to `AgentActor`.
  - Add `Takeover *TakeoverEvent` (`json:"takeover,omitempty"`) to `Event`:

```go
type TakeoverEvent struct {
	SID            string    `json:"sid"`
	Agent          string    `json:"agent,omitempty"`
	CreatedBy      string    `json:"createdBy"`
	ParentSession  string    `json:"parentSession,omitempty"`
	Via            string    `json:"via"`
	LastCLIInputAt time.Time `json:"lastCliInputAt,omitzero"`
	At             time.Time `json:"at"`
}
```

- **Session transition (`internal/rex/session.go`, `internal/rex/input.go`)**:
  - Add `onTakeover func(TakeoverEvent)` on `session` (wired to `Server.agentTookOver` in `Server.register`), mirroring `changed func()`.
  - Extract a single helper `session.takeOverLocked(via string, now time.Time) (TakeoverEvent, bool)` called from `session.admit`, `session.inputProgramWithActor` (when `actor == ActorHuman`), and `session.Key` (when `k.Action != "release"`). When `!s.agentActive || s.exited`, it returns `false`. Otherwise it sets `s.agentActive = false` and `s.tookOverAt = now`, builds `TakeoverEvent`, and the caller invokes `s.onTakeover(ev)` after releasing `s.mu` so subscriber locks never nest inside `s.mu`.
- **Event fan-out (`internal/rex/events.go`)**:
  - Give `subscriber` a `takeovers []TakeoverEvent` slice under `s.subMu`, coalesced by `SID` in the same way `subscriber.transfers` coalesces `Transfer` updates.
  - `Server.agentTookOver(ev TakeoverEvent)` never blocks PTY readers: under `s.subMu`, it appends or replaces `ev` in each subscriber's `takeovers` slice and performs a non-blocking wake on `sub.wake`.
  - In `subscribe`'s writer loop, flush `takeovers` as `Event{Kind: "takeover", Takeover: &t}` lines. Unlike `"sessions"`, `"theme"`, and `"ports"`, `"takeover"` is also delivered to paired remote subscribers (`sub.remote == true`) so `gorex -S HOST events` works across hosts.
- **CLI (`internal/cli/commands.go`, `internal/cli/run_cmd.go`)**:
  - Populate `ParentSession` from `os.Getenv("GOREX_SESSION")` on local `new`, `split`, `send`, and `paste` when `GOREX_SESSION` differs from the target session. For `gorex delegate`, populate `ParentSession` from `opt.From` or `GOREX_SESSION`.
  - `gorex events` already marshals each `rex.Event` as one JSON line, so `"takeover"` prints with zero extra formatting code.
  - Optionally add `--takeover` to `gorex wait` (`internal/cli/run_cmd.go`), subscribing to `c.Events()`, checking `c.List()` once for an existing `tookOverAt`, and waiting for a matching `"takeover"` event.
- **Desktop client (`agenthighlight.go`, `paneattach.go`, `view.go`, `tabs.go`, `sidebar.go`)**:
  - Built in Phase 1 (#1281). Desktop windows already clear the border and badge locally on the next frame via `clearAgentLocal()`, and could optionally listen to `"takeover"` in `workspace.go` if instant cross-window clear (< 500 ms) is ever needed.

## Alternatives

Adding an event to `internal/rex/events.go` changes the wire protocol listed in [`docs/irreversible.md`](irreversible.md). Three protocol options were evaluated:

1. **(Recommended) Option 1: Dedicated `"takeover"` event on `Event` (`{"event":"takeover","takeover":{...}}`, `CapabilityAgentTakeover`)**
   - *Pros*: Fires once per human takeover edge (`active: true -> false`) with `via`, `agent`, and `parentSession`. Adds zero overhead on normal output or subsequent human keystrokes. Works out of the box in `gorex events` and `gorex -S HOST events`. Does not disturb tests that assert no `"sessions"` event fires on quiet sessions (`TestStatusSeenOp`).
   - *What it breaks*: Nothing in existing GoRex builds (`0.15` through current `main`). Every existing `c.Events()` reader was checked: `workspace.go` switches on `"layout"` and `"prompt"`, `hosts.go` checks `ev.Kind == "layout"`, `internal/cli/delegate.go` wakes on any event to poll `DelegateResult`, and `internal/rexapi/gateway.go` ignores unknown kinds beyond scheduling a harmless debounced reread. Old `json.Unmarshal` callers ignore the unknown `"takeover"` field.
2. **Option 2: Piggyback `AgentActor` onto `"sessions"` events (`EventSession.AgentActor`)**
   - *Reason rejected*: `"sessions"` events fire on every bell, title change, foreground change, and OSC 7501 status update, carrying all sessions at once. Every consumer would have to cache prior state to detect a takeover edge, `gorex -S HOST events` would never see it because remote subscribers skip `"sessions"` events in `internal/rex/events.go`, and emitting `"sessions"` on PTY input risks breaking tests that expect no `"sessions"` event when status records are unchanged.
3. **Option 3: Opt-in `Request.Takeovers` flag on `"subscribe"` (mirroring `Request.Transfers`)**
   - *Reason rejected*: File transfers stream frequent byte-progress updates that most subscribers do not want, whereas a human takeover is a single rare edge event (< 250 bytes). Requiring an opt-in flag would hide takeover events from plain `gorex events` unless callers knew to pass a new flag.

## Security

Local subscribers on `server.sock` already run as the session owner. Remote subscribers reach `subscribe` only after passing peer key allowlisting in `internal/rex/remote.go` (`remoteAllowed`), which already permits `list`, `capture`, `input`, and `paste` on the host's sessions. A `"takeover"` event exposes only session IDs, short agent labels, and timestamps—never terminal text, paths, or environment variables.

## Migration

- **New server, old desktop / iOS / CLI (`0.15+`)**: Old clients ignore `CapabilityAgentTakeover` in `Hello`, ignore `"takeover"` fields when unmarshaling `Response`, and skip `ev.Kind == "takeover"` in their event loops.
- **Old server, new CLI / desktop**: A server without `CapabilityAgentTakeover` emits no `"takeover"` events. `gorex wait --takeover` checks `Hello.Can(CapabilityAgentTakeover)` and exits `1` with a clear message if connected to an older remote server before upgrade.
- **In-place `gorex server upgrade`**: `sessionState` in `internal/rex/upgrade.go` carries `AgentActor` (including `parentSession` and `tookOverAt`), all optional with `omitempty` / `omitzero`, so handoffs from older builds decode cleanly with defaults.

## Tests

- **Wire compatibility (`internal/rex/event_wire_test.go`)**: Verify `Event{Kind: "takeover", Takeover: ...}` marshals to the exact one-line JSON shape and unmarshals into `oldResponse` (`0.15–0.21`) without error.
- **Takeover event edge triggers (`internal/rex/actor_test.go`)**:
  - `TestAgentActorOnlyHumanTypingSetsTookOverAt`: verify attach, focus/blur (`CSI I`/`CSI O`), pointer motion/wheel/click/release (`SGR`, `URXVT`, `X10`, `SendMouse`), query replies (`DA`, `CPR`, `DSR`, `DECRPM`, `XTWINOPS`, Kitty flags, `OSC`), clipboard denials (`OSC 5522` `EPERM`/`DONE`), Kitty `:3` key-release and modifier-only events, and CLI writes never set `tookOverAt` or clear `active` on either `att.Write` or `InputHuman`; only real human keyboard/paste input sets `tookOverAt` and emits `"takeover"`.
  - `TestAgentTakeoverEvents`: write a real human byte (`"a"`) on the attached stream and assert **exactly one** `"takeover"` event arrives with `SID`, `Agent: "agy"`, `ParentSession: "p1"`, and `Via: "pty"`, no duplicate fires on `"bcd"`, `InputWithAgent` re-arms for `SendKey` (`Via: "key"`) and `PasteHuman` (`Via: "paste"`), and paired remote subscribers receive `"takeover"`.
- **CLI & UI (`internal/cli/actor_test.go`, `agenthighlight_test.go`)**:
  - Verify `GOREX_SESSION` populates `parentSession` on `new`, `split`, `send`, and `paste`, and `gorex events` (plus `gorex wait --takeover`) surfaces the takeover line.
  - `TestAgentCreatedHighlightPersistsUntilHumanTakeoverOrExit`, `TestAgentHighlightVisualAndTakeover`, and `TestAgentHighlightWindowExpiryAndOptOut`: verify `createdBy` in `{"cli", "delegate"}` stays highlighted indefinitely across idle and working states until `tookOverAt` or session exit, immediate next-frame clear on `tt.Key`, 60 s expiry restricted to `createdBy == "human"` scripted panes, and `agentHighlight=false` opt-out.
- **Post-deploy check (`docs/irreversible.md`)**: Run `gorex events` in one pane, split a sibling pane with `gorex split --agent agy`, type in the new pane, confirm one `"takeover"` JSON line prints and open windows show zero errors. Rollback if needed is reverting the `"takeover"` emit in `events.go` while keeping `AgentActor` fields backward-compatible.

## Phases

1. **Phase 1**: Server `AgentActor` tracking (`createdBy`, `lastCliInputAt`, `agent`, `active`), desktop blue pulsing pane border and tab/sidebar badge, immediate UI takeover clear, `--agent <name>` on `new`/`split`/`send`/`paste`, `ls --json` `agentActor`, and `gorex config get/set agent-highlight`.
2. **Phase 2**: `TakeoverEvent` (`"event": "takeover"`) on `gorex events`, `parentSession` and `tookOverAt` on `AgentActor`, remote subscriber delivery, and `gorex wait --takeover`.
