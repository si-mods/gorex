---
name: DSK-016
description: "Agent-driven pane highlight: server actor tracking, a blue pulsing border on panes driven by the CLI or peer delegation, a small agent badge on tab titles, immediate human takeover on typing, and a settings opt-out. Use when you change agent actor bookkeeping, pane borders, tab badges, or CLI agent flags."
metadata:
  created: "2026-11-28"
  updated: "2026-11-28"
---

# DSK-016: Agent-driven pane highlight

Status: built.

This doc decides how GoRex marks panes that an agent or script created or is driving through the command line or peer delegation.

## Problem

Agents and automation scripts drive GoRex panes with `gorex new`, `gorex split`, `gorex send`, `gorex paste`, and `gorex delegate`. When a tab holds several splits, a pane opened by a script looks identical to one opened by hand, and a user cannot tell at a glance which pane an agent owns, whether it is still sending input, or whether typing into it will collide with an active run. Scripts inspecting `gorex ls --json` also have no record of which sessions were created by the CLI or when the CLI last wrote to them.

## Goal

- Record per-session actor state on the session server: `createdBy` (`"human"`, `"cli"`, or `"delegate"`), `lastCliInputAt`, `agent` label, and whether the agent mark is still active.
- Stamp `lastCliInputAt` whenever the CLI creates a session (`gorex new`), runs an initial command on a split (`gorex split -- ...`), sends keys (`gorex send`), or pastes text (`gorex paste`), and when peer delegation delivers a prompt.
- Draw a calm blue pulsing border (2.4 s cycle) around an agent-active pane and a small agent badge on its tab in the title bar and sidebar while the pane was created by the CLI or `now - lastCliInputAt < 60s`.
- Clear both the border and the badge immediately as soon as a person types or pastes in that pane in the UI, and fade the mark out after 60 seconds without CLI input.
- Expose `agentActor` on every session in `gorex ls --json` so scripts can audit what they created and touched.
- Accept an optional `--agent <name>` flag on `gorex new`, `gorex split`, `gorex send`, and `gorex paste` (default `"cli"`) to label the tab badge (`agy`, `omp`, `claude`, and so on).
- Provide `gorex config set agent-highlight off|on` (`"agentHighlight"` in `settings.json`) for users who want no highlight.

## Non-goals

- Locking the terminal against human input while an agent writes. Typing in the UI always goes through and hands ownership back to the person at once.
- Persisting ephemeral pulse state across full machine reboots. In-place `gorex server upgrade` preserves live session actor metadata in `handoff.json`, while a cold restart starts fresh shells as human sessions.
- Changing terminal foreground or background colors inside the PTY. Only the pane card's border and the tab strip or sidebar row show the mark.

## Design

### Server actor bookkeeping

Each `session` on the server tracks an `AgentActor` value exposed in `SessionInfo` as `agentActor`:

| Field | Type | Meaning |
| --- | --- | --- |
| `createdBy` | `string` | `"human"` for window-created and restored panes, `"cli"` for `gorex new` and `gorex split`, `"delegate"` for `gorex delegate`. |
| `lastCliInputAt` | `time.Time` | Time of the latest CLI or delegated input (`gorex new`, `gorex split` with a command, `gorex send`, `gorex paste`, `gorex delegate` prompt delivery); omitted when zero. |
| `agent` | `string` | Short badge label from `--agent <name>` (default `"cli"` for CLI operations, or the delegated agent ID for `delegate`). |
| `active` | `bool` | True while an agent created or wrote to the session and no human UI input has taken over since. |

Human input arrives over an attached terminal stream (`session.admit`, after stripping terminal query replies and focus reports) or through explicit UI key and paste paths (`doKey`, ConPTY `pasteText`, file drop, and mobile UI input). Any such input sets `active` to `false` while keeping `createdBy` and `lastCliInputAt` intact so `gorex ls --json` retains an accurate audit trail. A later `gorex send` or `gorex paste` stamps `lastCliInputAt`, updates `agent`, and sets `active` back to `true`.

### Visual mark and motion

A pane shows the agent mark when all of the following hold:

1. `agentHighlight` in `settings.json` is not `false`.
2. `info.AgentActor.Active` is true and the pane has not exited or been taken over locally since that activity.
3. Elapsed time since `max(lastCliInputAt, created)` (for CLI or delegated panes) or `lastCliInputAt` (for human-created panes later driven by `send` or `paste`) is under 60 seconds.

While active:

- **Pane border**: `paneCard` draws a rounded stroke over the card edge in the palette's `agent` blue (`#2e6fd0` in light windows, `#6cb2ff` in dark windows). With motion enabled, a cosine wave of period 2.4 s breathes the stroke alpha gently between `0.35` and `0.85` with a soft outer halo, never strobing; over the final 3 seconds (`57s..60s`) the alpha fades linearly to zero. When `animation` is `"off"` or the OS asks for Reduce Motion, the border draws at steady alpha without continuous frames and schedules a single wakeup timer for expiry.
- **Tab badge**: `tabItem` in the horizontal tab strip and `sidebarTab` / `spaceTab` in the vertical sidebar render a compact pill (`fontCaption`, weight `600`, `radiusS`) tinted with `k.agent` showing the `agent` label (`cli`, `agy`, `omp`, and so on). When a tab has multiple panes and more than one is agent-driven, the focused active pane's label wins, falling back to the most recently active pane in the tab.
- **Immediate human takeover**: `OnKey` (for keys delivered to the terminal, excluding `Super` app shortcuts and bare modifiers) and `OnPaste` mark the pane as taken over on the client immediately so the border and badge disappear on the next frame without waiting for the 500 ms `ListSince` poll, while the attached PTY stream clears `active` on the server for all other windows.

### CLI flags and configuration

- `gorex new`, `gorex split`, `gorex send`, and `gorex paste` accept `--agent <name>` (default `"cli"`). Names are trimmed and capped to 24 visible characters so a script cannot blow out the tab strip.
- `gorex ls --json` includes `"agentActor": {"createdBy": "...", "lastCliInputAt": "..."}` (plus `"agent"` and `"active"` when present) on every session object.
- `gorex config set agent-highlight on|off` updates `"agentHighlight"` in `settings.json` atomically without disturbing unknown keys, and `gorex config get agent-highlight` prints `on` or `off`. Because open windows watch `settings.json` ([DSK-011](DSK-011-live-settings.md)), toggling the setting updates running windows immediately without a restart.
