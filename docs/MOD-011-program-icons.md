---
name: MOD-011
description: "OSC 7502, the Program Icon Protocol: how a program exposes a custom icon to GoRex. Use when you change program icon parsing, transport or rendering."
metadata:
  created: "2026-11-23"
  updated: "2026-11-23"
---

# MOD-011: OSC 7502 (Program Icon Protocol)

Status: phase 1 built (#1040). Phase 2 not built.

This doc defines OSC 7502, the wire fields in `SessionInfo`, and desktop icon rendering. It extends [`MOD-001`](MOD-001-osc-7501.md) with a separate icon channel.

## Problem

GoRex maps each program name to a built-in icon table in `programs.go`, `ios/Shared/Program.swift` and `web/src/programs.ts`. Custom, internal or niche tools fall back to the generic terminal icon, even over SSH.

## Goal

- A program running in a local or remote session can send its own SVG or PNG icon through an OSC sequence.
- The session server keeps the icon across reconnects and in-place upgrades.
- Desktop tabs, pane headers and notifications show the custom icon instead of the fallback terminal glyph.

## Non-goals

- User-configured icon overrides in `init.lua` or `.config/rex` belong to a separate feature.
- iOS and Web client rendering will follow in separate pull requests using the same wire fields.

## Design

### Behavior

A program queries support by sending `ESC ] 7502 ; ? ST`. The session server answers `ESC ] 7502 ; ? ST` once, before `CSI c`.

A program sets its icon by sending `ESC ] 7502 ; format=<fmt>:data=<base64> ST`, with optional `mode=mono|color`, `bg=<rrggbb>` and `fg=<rrggbb>`. The server accepts `svg` and `png` formats.

A program clears its icon by sending `ESC ] 7502 ; clear ST`. A terminal reset (`ESC c`) or a shell prompt mark (`OSC 133 ; A`) also clears the icon.

When a session has a custom icon, desktop pane headers and tab tiles draw it. A `mono` SVG icon takes the foreground ink of the surface or tile `fg`. A `color` SVG or PNG icon keeps its own colors.

### Model

`ProgramIcon` holds `Format` (`svg` or `png`), `Mode` (`mono` or `color`), `Data` (standard base64), optional `Bg` and `Fg` (`#rrggbb`), and `Hash` (a short SHA-256 hex digest).

The parser in `internal/rex/icon.go` enforces `maxIconSeq = 49152` bytes for the raw OSC sequence and `maxIconBytes = 32768` bytes for the decoded icon. Oversized or invalid payloads are dropped without error.

Only changes that produce a different `Hash`, `Bg`, `Fg` or `Mode` mark the session dirty and trigger a session event.

### Code

- `internal/rex/icon.go`: `programIcon` parser, validator, base64 normalization and `maxIconSeq` / `maxIconBytes` guards.
- `internal/rex/proto.go`: `CapabilityProgramIcon` (`"program.icon"`), `ProgramIcon` struct, and `SessionInfo.ProgramIcon`.
- `internal/rex/events.go`: `EventSession.ProgramIcon` propagation and diff check.
- `internal/rex/session.go`: feeds `s.icon.feed(data)` in `emit`, answers `7502;?` queries, drops on `133;A`, and populates `SessionInfo.ProgramIcon`.
- `internal/rex/upgrade.go`: preserves `ProgramIcon` in `sessionState` across in-place upgrades.
- `internal/cli/icon.go`, `internal/cli/commands.go`: `gorex icon [--bg HEX] [--fg HEX] [--mode mono|color] [--format svg|png] FILE|clear` writes the OSC 7502 sequence to stdout for programs or wrappers that do not emit it natively.
- `program_icon.go`, `programs.go`, `state.go`, `tabs.go`, `view.go`: caches decoded SVG/PNG icons by `Hash` and draws them in pane headers and tab tiles.

## Alternatives

- **Reuse OSC 7501 (`icon=` field)**: Rejected because `OSC 7501` caps sequences at 4096 bytes and ties lifetime to task status reports.
- **Pass local file paths or icon names**: Rejected because remote SSH sessions cannot serve local files on the client host.
- **Fetch icon bytes via a separate RPC by hash**: Rejected to avoid extra round-trips and multi-client cache sync for icons capped at 32 KiB.

## Security

- Raw OSC sequences past 48 KiB and decoded payloads past 32 KiB are dropped before allocation or storage.
- PNG payloads must carry the PNG signature and decode dimensions between 1 and 512 pixels via `image/png.DecodeConfig`.
- SVG payloads must be valid UTF-8, contain `<svg`, and must not contain `<script`, `javascript:` or inline `on*=` event handlers.

## Migration

Older clients ignore the `programIcon` field in `SessionInfo` because it uses `json:"programIcon,omitempty"`. Older servers omit `CapabilityProgramIcon` and `programIcon`, and desktop clients fall back to `programOf`.

## Tests

- `internal/rex/icon_test.go`: query reply, SVG/PNG validation, oversized drop, `clear`, `RIS`, `133;A` drop, and base64 variants.
- `internal/rex/icon_windows_test.go`: Windows ConPTY end-to-end query/reply and `7502` delivery.
- `internal/rex/upgrade_test.go`: in-place upgrade preserves `ProgramIcon`.
- `program_icon_test.go`: desktop `program` resolution and tile key caching with `ProgramIcon`.

## Phases

1. Design doc, server `OSC 7502` parser, wire protocol, in-place upgrade, and desktop rendering (this PR).
2. iOS (`Rex Go`) and Web client rendering of `SessionInfo.ProgramIcon`.
