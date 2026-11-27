---
name: IOS-032
description: "Rex Go removes the duplicate GhosttyVt.xcframework static library by exporting the libghostty-vt C API from a si-mods fork of GhosttyKitAppStore. Use when you change how Rex Go links Ghostty, decodes GHOSTSNP snapshots, or builds iOS frameworks in CI."
metadata:
  created: "2026-11-27"
  updated: "2026-11-27"
---

# IOS-032: Merge GhosttyVt into GhosttyKitAppStore

Status: built (#1235, `si-mods/ghosttykit-rootshell` v0.3.0).

This doc decides how Rex Go removes the duplicate `GhosttyVt.xcframework` static library by consuming the `libghostty-vt` C API from a `si-mods`-maintained fork of `GhosttyKitAppStore` (issue #1229). It updates the iOS linking design in [`REF-006`](REF-006-session-server.md).

## Problem

Rex Go links two separate Ghostty static libraries side by side in `ios/project.yml`:

- `GhosttyKitAppStore` from `kitknox/ghosttykit-rootshell` provides the Metal renderer, input encoding, and full Ghostty runtime.
- `ios/scripts/build-ghostty-vt.sh` builds `GhosttyVt.xcframework` (~3.3 MB arm64 static library) from upstream Ghostty sources.

Both libraries compile the same Ghostty terminal core, SIMD helpers, and Zig runtime. To avoid duplicate symbol errors, `ios/scripts/build-ghostty-vt.sh` runs `ld -r -all_load -exported_symbols_list` on each slice. That step hides every symbol except the `libghostty-vt` C API. The script also moves `module.modulemap` into a `GhosttyVt/` folder so Xcode avoids a header map collision with `GhosttyKit`.

This split creates three costs:

- The iOS binary carries ~3.3 MB of duplicate arm64 code from `GhosttyVt.xcframework`.
- Local builds, `.github/workflows/ios-test.yml`, and `.github/workflows/testflight.yml` download Zig, clone Ghostty, compile `libghostty-vt`, and cache `ios/Frameworks/GhosttyVt.xcframework`.
- `kitknox/ghostty-rootshell` already contains `src/terminal/c/main.zig`, `src/terminal/c/snapshot.zig`, and `include/ghostty/vt.h`. However, `src/main_c.zig` does not export those C symbols, and `src/build/GhosttyXCFramework.zig` omits `include/ghostty/` from the xcframework headers.

## Goal

- Maintain `si-mods/ghostty-rootshell` and `si-mods/ghosttykit-rootshell` as `si-mods`-owned forks that publish `GhosttyKitAppStore.xcframework` with `libghostty-vt` exported.
- Remove `ios/Frameworks/GhosttyVt.xcframework` from the `GoRex` and `GoRexTests` targets in `ios/project.yml`.
- Reclaim ~3.3 MB from the arm64 iOS binary by sharing one Ghostty runtime and terminal core.
- Delete `ios/scripts/build-ghostty-vt.sh` and remove the Zig build and cache steps from `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`.
- Keep all `VTerminal` snapshot restoration, re-encoding, formatting, color extraction, and viewport reflow behavior unchanged.
- Keep `GHOSTSNP` binary compatibility aligned between `si-mods/mygo` and `si-mods/ghostty-rootshell`.

## Non-goals

- Opening issues or pull requests on upstream `kitknox/ghostty-rootshell` or `kitknox/ghosttykit-rootshell`.
- Replacing `GhosttyKit` Metal rendering with a custom iOS renderer.
- Changing the `GHOSTSNP` wire protocol or replacing `GHOSTSNP` with plain VT replay on the server.
- Changing desktop or web `libghostty-vt` builds (`si-mods/mygo` and `web/`).

## Design

### Behavior

Users see no change in terminal rendering, reconnect restoration, theme colors, or narrow-viewport reflow. App download and install size drops by ~3.3 MB on arm64 devices.

Developers and CI runners no longer install Zig or run `ios/scripts/build-ghostty-vt.sh` to build or test Rex Go. `xcodegen generate` and Swift Package Manager resolve `si-mods/ghosttykit-rootshell` as the single Ghostty dependency.

### Model

`si-mods` maintains two forks:

- `si-mods/ghostty-rootshell`: forked from `kitknox/ghostty-rootshell`, aligned with `si-mods/mygo`'s Ghostty revision for `GHOSTSNP` v1, and patched to export `libghostty-vt` symbols and headers.
- `si-mods/ghosttykit-rootshell`: forked from `kitknox/ghosttykit-rootshell`, publishing signed-checksum `GhosttyKitAppStore.xcframework` binary releases for Swift Package Manager.

All changes, issues, and pull requests stay inside `si-mods`. GoRex submits no issues or pull requests to upstream `kitknox` repositories.

`GhosttyKitAppStore.xcframework` exposes two Clang modules from one static library:

| Module | Header | Purpose |
| --- | --- | --- |
| `GhosttyKit` | `ghostty.h` | Metal renderer, surfaces, app lifecycle, and key encoding |
| `GhosttyVt` | `ghostty/vt.h` | Standalone VT terminal, `GHOSTSNP` snapshot codec, and VT formatter |

Keeping `GhosttyVt` as a separate top-level module in `module.modulemap` prevents C type or enum collisions between `ghostty.h` and `ghostty/vt.h`. Existing Swift files keep `import GhosttyKit` or `import GhosttyVt` without source changes.

The single static library in `GhosttyKitAppStore` exports the 14 `libghostty-vt` C symbols that `VTerminal` calls:

| Group | Exported C symbols |
| --- | --- |
| Snapshot codec | `ghostty_snapshot_decoder_new_buf`, `ghostty_snapshot_decoder_free`, `ghostty_snapshot_decoder_set`, `ghostty_snapshot_decoder_decode`, `ghostty_snapshot_encode_alloc` |
| Terminal lifecycle | `ghostty_terminal_free`, `ghostty_terminal_vt_write`, `ghostty_terminal_get`, `ghostty_terminal_resize` |
| Formatter | `ghostty_formatter_terminal_new`, `ghostty_formatter_free`, `ghostty_formatter_set`, `ghostty_formatter_format_alloc` |
| Memory | `ghostty_free` |

`VTerminal` in `ios/GoRex/Terminal/VTerminal.swift` stays `nonisolated` and runs on `TerminalFeed`'s background queue. It operates on independent `GhosttyTerminal` handles and does not touch `Ghostty.app` or Metal surfaces.

Because `GoRexTests` is a standalone unit-test bundle that compiles `VTerminal.swift` without hosting `GoRex.app`, `GoRexTests` switches from `GhosttyVt.xcframework` to `GhosttyKitAppStore`.

### Code

#### Fork changes in `si-mods/ghostty-rootshell` and `si-mods/ghosttykit-rootshell`

1. Fork `kitknox/ghostty-rootshell` to `si-mods/ghostty-rootshell` and `kitknox/ghosttykit-rootshell` to `si-mods/ghosttykit-rootshell`.
2. Backport upstream Ghostty commit `781c58e966` (skip snapshot records with unknown tags) into `si-mods/ghostty-rootshell` so `src/terminal/snapshot/` decodes `si-mods/mygo`'s `GHOSTSNP` v1 streams while keeping `kitknox/ghostty-rootshell`'s Zig 0.16 base.
3. Enable `c_abi = true` in `src/build/Config.zig` and export the `libghostty-vt` C API from `src/terminal/c/main.zig` inside `src/main_c.zig`.
4. Confirm that no C symbol names collide between `src/main_c.zig` and `src/terminal/c/main.zig`.
5. Copy `include/ghostty/vt.h` and `include/ghostty/vt/*.h` into the xcframework `Headers/` directory in `src/build/GhosttyXCFramework.zig`.
6. Add `module GhosttyVt { header "ghostty/vt.h" export * }` beside `module GhosttyKit` in `include/module.modulemap`. Both blocks use `header` instead of `umbrella header`, so Clang emits no `-Wincomplete-umbrella` warnings.
7. Update `scripts/release.sh` and `Package.swift` in `si-mods/ghosttykit-rootshell` to target `si-mods/ghosttykit-rootshell` releases, publish `v0.3.0`, and verify the exported symbols with `nm -g`.

#### GoRex repository changes

- `ios/project.yml`: switch `GhosttyKit` URL to `https://github.com/si-mods/ghosttykit-rootshell.git` at `exactVersion: 0.3.0` and remove `Frameworks/GhosttyVt.xcframework` from `GoRex` and `GoRexTests`. Add package product `GhosttyKitAppStore` to `GoRexTests` and add `-lc++` to `GoRexTests` `OTHER_LDFLAGS`.
- `ios/GoRex/Terminal/VTerminal.swift`: pass `GhosttyFormatterTerminalOptions` to `ghostty_formatter_terminal_new` to match `si-mods/ghostty-rootshell`'s `formatter.h`.
- `ios/scripts/build-ghostty-vt.sh`: delete the script.
- `ios/scripts/upload`: remove the comment that references `scripts/build-ghostty-vt.sh`.
- `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`: remove the `ghostty-vt` cache restore, build, and cache save steps.
- `tools/verify/main.go`: remove the `ios/Frameworks/GhosttyVt.xcframework` check from `f.iosToolsPresent` and update its skip message.
- `ios/THIRD_PARTY_NOTICES.md`: update the Ghostty section to reference `si-mods/ghosttykit-rootshell` and `si-mods/ghostty-rootshell`, keep the upstream MIT notices, and state that `libghostty-vt` comes from `GhosttyKitAppStore`.
- `docs/REF-006-session-server.md`: update the iOS section to state that `VTerminal` uses `libghostty-vt` from `si-mods/ghosttykit-rootshell` and note `GHOSTSNP` compatibility.
- `docs/REF-007-web-client.md`, `web/build.mjs`, and `.agents/skills/gorex-ui-testing/SKILL.md`: update comments that reference `ios/scripts/build-ghostty-vt.sh`.

## Alternatives

- **Submit an upstream PR to `kitknox/ghostty-rootshell` and `kitknox/ghosttykit-rootshell`**: GoRex would avoid maintaining forks and release pipelines. It loses because GoRex does not submit issues or pull requests upstream, and `kitknox/ghostty-rootshell` lags behind `si-mods/mygo`'s Ghostty commit for `GHOSTSNP` v1.
- **Replace `GHOSTSNP` with VT replay on the server for iOS**: iOS skips `libghostty-vt` for initial restore. It loses because `TerminalFeed` uses `VTerminal` as a shadow emulator (`TerminalFeed.shadow` and `VTerminal.reflowedReplay`) to reflow wide desktop redraws.
- **Keep `GhosttyVt.xcframework` as a separate static library**: `si-mods/mygo` and iOS stay pinned to one Ghostty commit. It loses because Rex Go keeps ~3.3 MB of duplicate arm64 code and CI keeps the Zig build step.

## Migration

`GHOSTSNP` format version 1 carries no upstream binary-compatibility guarantee (`include/ghostty/vt/snapshot.h`). Previously, `ios/scripts/build-ghostty-vt.sh` pinned `GhosttyVt.xcframework` to `si-mods/mygo`'s Ghostty commit (`108cacb17ed8990840cd14abfce43d8ed8503ca1`). Now `si-mods/ghostty-rootshell` keeps `src/terminal/snapshot/` wire-compatible by skipping unknown optional record tags.

Before switching `ios/project.yml` to `si-mods/ghosttykit-rootshell`:

1. Confirm that `si-mods/ghostty-rootshell`'s `src/terminal/snapshot/` matches or supersedes `si-mods/mygo`'s `GHOSTSNP` v1 layout, including unknown-record skipping in `src/terminal/snapshot/record.zig`.
2. Run `GoRexTests/TerminalStreamTests` and `GoRexTests/TerminalFeedTests` against `internal/rexapi/testdata/rex-terminal-stream.bin` and a live server snapshot.
3. Add a CI compatibility check or fixture refresh rule so future `si-mods/mygo` or `si-mods/ghosttykit-rootshell` bumps catch `GHOSTSNP` drift.

Installed iOS builds and running GoRex servers need no data migration because the `GHOSTSNP` wire envelope stays unchanged.

## Tests

- **Symbol and module verification**: run `nm -g` on `ios-arm64` and `ios-arm64-simulator` slices in `GhosttyKitAppStore.xcframework`. Confirm all 14 `libghostty-vt` C symbols appear and linking succeeds without duplicate symbols.
- **Unit tests and link time**: run `.github/workflows/ios-test.yml` (`GoRexTests/TerminalStreamTests` and `GoRexTests/TerminalFeedTests`). Verify `GHOSTSNP` decoding, `retainContinuation`, re-encoding, formatting, color extraction, `reflowedReplay`, and acceptable `GoRexTests` link time on the Mac mini runner.
- **Archive and size check**: run `DESTINATION=export ios/scripts/upload` on the Mac mini. Compare the exported arm64 binary size before and after to confirm the ~3.3 MB reduction.
- **End-to-end check**: attach Rex Go on a simulator or device to a live GoRex server. Verify initial screen restore, theme colors, and narrow-viewport reflow during streaming output.
- **Invariants**: `TerminalStreamTests` and `TerminalFeedTests` check `docs/invariants.md` item 3 ("A pane's text is the session's screen").

## Phases

1. Create `si-mods/ghostty-rootshell` and `si-mods/ghosttykit-rootshell` forks, align `src/terminal/snapshot/` with `si-mods/mygo`, export `libghostty-vt` symbols and headers, and publish the first `si-mods/ghosttykit-rootshell` release.
2. Switch `ios/project.yml` to `si-mods/ghosttykit-rootshell`, remove `GhosttyVt.xcframework`, delete `ios/scripts/build-ghostty-vt.sh`, update CI, `tools/verify`, and docs, and verify size and tests on the Mac mini.
