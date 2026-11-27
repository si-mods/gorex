---
name: IOS-032
description: "Rex Go removes the duplicate GhosttyVt.xcframework static library by exporting the libghostty-vt C API from GhosttyKitAppStore. Use when you change how Rex Go links Ghostty, decodes GHOSTSNP snapshots, or builds iOS frameworks in CI."
metadata:
  created: "2026-11-27"
  updated: "2026-11-27"
---

# IOS-032: Merge GhosttyVt into GhosttyKitAppStore

Status: proposal.

This doc decides how Rex Go removes the duplicate `GhosttyVt.xcframework` static library by consuming the `libghostty-vt` C API from `GhosttyKitAppStore` (issue #1229). It updates the iOS linking design in [`REF-006`](REF-006-session-server.md).

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

- Remove `ios/Frameworks/GhosttyVt.xcframework` from the `GoRex` and `GoRexTests` targets in `ios/project.yml`.
- Reclaim ~3.3 MB from the arm64 iOS binary by sharing one Ghostty runtime and terminal core.
- Delete `ios/scripts/build-ghostty-vt.sh` and remove the Zig build and cache steps from `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`.
- Keep all `VTerminal` snapshot restoration, re-encoding, formatting, color extraction, and viewport reflow behavior unchanged.
- Verify `GHOSTSNP` binary compatibility between GoRex's session server (`si-mods/mygo`) and `GhosttyKitAppStore` before removing `GhosttyVt.xcframework`.

## Non-goals

- Replacing `GhosttyKit` Metal rendering with a custom iOS renderer.
- Changing the `GHOSTSNP` wire protocol or replacing `GHOSTSNP` with plain VT replay on the server.
- Changing desktop or web `libghostty-vt` builds (`si-mods/mygo` and `web/`).

## Design

### Behavior

Users see no change in terminal rendering, reconnect restoration, theme colors, or narrow-viewport reflow. App download and install size drops by ~3.3 MB on arm64 devices.

Developers and CI runners no longer install Zig or run `ios/scripts/build-ghostty-vt.sh` to build or test Rex Go. `xcodegen generate` and Swift Package Manager resolve `GhosttyKitAppStore` as the single Ghostty dependency.

### Model

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

#### Upstream changes in `kitknox/ghostty-rootshell` and `kitknox/ghosttykit-rootshell`

1. Export the `libghostty-vt` C API from `src/terminal/c/main.zig` inside `src/main_c.zig` (or link `vt_c` into `GhosttyLib.initStatic`).
2. Confirm that no C symbol names collide between `src/main_c.zig` and `src/terminal/c/main.zig`.
3. Copy `include/ghostty/vt.h` and `include/ghostty/vt/*.h` into the xcframework `Headers/` directory in `src/build/GhosttyXCFramework.zig`.
4. Add `module GhosttyVt { header "ghostty/vt.h" export * }` beside `module GhosttyKit` in `include/module.modulemap`. Both blocks use `header` instead of `umbrella header`, so Clang emits no `-Wincomplete-umbrella` warnings.
5. Publish a new `kitknox/ghosttykit-rootshell` release tag and verify the exported symbols with `nm -g`.

#### GoRex repository changes

- `ios/project.yml`: bump `GhosttyKit` to the new `exactVersion` and remove `Frameworks/GhosttyVt.xcframework` from `GoRex` and `GoRexTests`. Add package product `GhosttyKitAppStore` to `GoRexTests` and add `-lc++` to `GoRexTests` `OTHER_LDFLAGS` if required.
- `ios/scripts/build-ghostty-vt.sh`: delete the script.
- `ios/scripts/upload`: remove the comment that references `scripts/build-ghostty-vt.sh`.
- `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`: remove the `ghostty-vt` cache restore, build, and cache save steps.
- `tools/verify/main.go`: remove the `ios/Frameworks/GhosttyVt.xcframework` check from `f.iosToolsPresent` and update its skip message.
- `ios/THIRD_PARTY_NOTICES.md`: update the Ghostty section to state that `libghostty-vt` comes from `GhosttyKitAppStore`.
- `docs/REF-006-session-server.md`: update the iOS section to state that `VTerminal` uses `libghostty-vt` from `GhosttyKitAppStore` and note `GHOSTSNP` compatibility.
- `docs/REF-007-web-client.md`, `web/build.mjs`, and `.agents/skills/gorex-ui-testing/SKILL.md`: update comments that reference `ios/scripts/build-ghostty-vt.sh`.

## Alternatives

- **Maintain a `si-mods` fork of `ghostty-rootshell` and `ghosttykit-rootshell`**: GoRex controls the Ghostty commit directly without upstream review. It loses because GoRex must rebase rootshell's iOS patches, run multi-platform xcframework builds, and host SPM artifacts.
- **Replace `GHOSTSNP` with VT replay on the server for iOS**: iOS skips `libghostty-vt` for initial restore. It loses because `TerminalFeed` uses `VTerminal` as a shadow emulator (`TerminalFeed.shadow` and `VTerminal.reflowedReplay`) to reflow wide desktop redraws.
- **Keep `GhosttyVt.xcframework` as a separate static library**: `si-mods/mygo` and iOS stay pinned to one Ghostty commit. It loses because Rex Go keeps ~3.3 MB of duplicate arm64 code and CI keeps the Zig build step.

## Migration

`GHOSTSNP` format version 1 carries no upstream binary-compatibility guarantee (`include/ghostty/vt/snapshot.h`). As [`REF-006`](REF-006-session-server.md) states, `ios/scripts/build-ghostty-vt.sh` pins `GhosttyVt.xcframework` to `si-mods/mygo`'s Ghostty commit (`108cacb17ed8990840cd14abfce43d8ed8503ca1`).

Before switching `ios/project.yml` to the merged `GhosttyKitAppStore`:

1. Confirm that `kitknox/ghostty-rootshell`'s `src/terminal/snapshot/` matches or supersedes `si-mods/mygo`'s `GHOSTSNP` v1 layout, including unknown-record skipping in `src/terminal/snapshot/record.zig`.
2. Run `GoRexTests/TerminalStreamTests` and `GoRexTests/TerminalFeedTests` against `internal/rexapi/testdata/rex-terminal-stream.bin` and a live server snapshot.
3. Add a CI compatibility check or fixture refresh rule so future `si-mods/mygo` or `GhosttyKit` bumps catch `GHOSTSNP` drift.

Installed iOS builds and running GoRex servers need no data migration because the `GHOSTSNP` wire envelope stays unchanged.

## Tests

- **Symbol and module verification**: run `nm -g` on `ios-arm64` and `ios-arm64-simulator` slices in `GhosttyKitAppStore.xcframework`. Confirm all 14 `libghostty-vt` C symbols appear and linking succeeds without duplicate symbols.
- **Unit tests**: run `.github/workflows/ios-test.yml` (`GoRexTests/TerminalStreamTests` and `GoRexTests/TerminalFeedTests`). Verify `GHOSTSNP` decoding, `retainContinuation`, re-encoding, formatting, color extraction, and `reflowedReplay`.
- **Archive and size check**: run `DESTINATION=export ios/scripts/upload` on the Mac mini. Compare the exported arm64 binary size before and after to confirm the ~3.3 MB reduction.
- **End-to-end check**: attach Rex Go on a simulator or device to a live GoRex server. Verify initial screen restore, theme colors, and narrow-viewport reflow during streaming output.
- **Invariants**: `TerminalStreamTests` and `TerminalFeedTests` check `docs/invariants.md` item 3 ("A pane's text is the session's screen").

## Phases

1. Align `GHOSTSNP` format compatibility and submit the `libghostty-vt` export and header changes to `kitknox/ghostty-rootshell` and `kitknox/ghosttykit-rootshell`.
2. Bump `GhosttyKit` in `ios/project.yml`, remove `GhosttyVt.xcframework`, delete `ios/scripts/build-ghostty-vt.sh`, update CI, `tools/verify`, and docs, and verify size and tests on the Mac mini.

## Open questions

1. **Upstream acceptance**: will `kitknox/ghostty-rootshell` accept a PR exporting `libghostty-vt` symbols and headers in `GhosttyKitAppStore`, or should GoRex maintain a `si-mods` fork? Owner decides after checking with upstream.
2. **`GHOSTSNP` version skew**: `kitknox/ghostty-rootshell`'s `src/terminal/snapshot/record.zig` currently lags behind `si-mods/mygo`'s Ghostty commit and rejects unknown tags instead of skipping them. Should Phase 1 wait for `kitknox/ghostty-rootshell` to rebase past `108cacb17ed8990840cd14abfce43d8ed8503ca1`, or pin both repos to a shared Ghostty revision? Owner decides.
3. **Link time for `GoRexTests`**: linking `GhosttyKitAppStore` pulls the full Ghostty static library into the unit-test bundle. Confirm during Phase 2 that `ios-test.yml` link time and test runtime remain acceptable on the Mac mini runner.
