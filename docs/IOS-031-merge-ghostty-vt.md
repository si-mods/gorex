---
name: IOS-031
description: "Rex Go removes the duplicate GhosttyVt.xcframework static library by exporting the libghostty-vt C API from GhosttyKitAppStore. Use when you change how Rex Go links Ghostty, decodes GHOSTSNP snapshots, or builds iOS frameworks in CI."
metadata:
  created: "2026-11-27"
  updated: "2026-11-27"
---

# IOS-031: Merge GhosttyVt into GhosttyKitAppStore

Status: proposal.

This doc decides how Rex Go removes the duplicate `GhosttyVt.xcframework` static library by consuming the `libghostty-vt` C API directly from `GhosttyKitAppStore` (issue #1229). It updates the iOS linking design described in [`REF-006`](REF-006-session-server.md).

## Problem

Rex Go currently links two separate Ghostty static libraries side by side in `ios/project.yml`:

- `GhosttyKitAppStore` from `kitknox/ghosttykit-rootshell` provides the Metal renderer, input encoding, and full Ghostty runtime.
- `GhosttyVt.xcframework` (~3.3 MB arm64 static library) is built separately from upstream Ghostty sources by `ios/scripts/build-ghostty-vt.sh`.

Both libraries compile the same Ghostty terminal core, SIMD helpers, and Zig runtime. To link them in one binary without duplicate symbol errors, `ios/scripts/build-ghostty-vt.sh` runs `ld -r -all_load -exported_symbols_list` on every slice to hide all symbols except the `libghostty-vt` C API. It also relocates `module.modulemap` into a `GhosttyVt/` subdirectory so Xcode does not collide with `GhosttyKit`'s header map.

This split creates three costs:

- The iOS app binary carries ~3.3 MB of duplicate arm64 code from `GhosttyVt.xcframework`.
- Local builds, `.github/workflows/ios-test.yml`, and `.github/workflows/testflight.yml` must download Zig, clone Ghostty, compile `libghostty-vt`, and cache `ios/Frameworks/GhosttyVt.xcframework`.
- `GhosttyKitAppStore` (`kitknox/ghostty-rootshell`) already contains `src/terminal/c/main.zig`, `src/terminal/c/snapshot.zig`, and `include/ghostty/vt.h` in its source tree, but `src/main_c.zig` does not export those C symbols and `src/build/GhosttyXCFramework.zig` omits `include/ghostty/` from the xcframework headers.

## Goal

- Remove `ios/Frameworks/GhosttyVt.xcframework` from the `GoRex` and `GoRexTests` targets in `ios/project.yml`.
- Reclaim ~3.3 MB from the arm64 iOS binary by sharing one Ghostty runtime and terminal core.
- Remove `ios/scripts/build-ghostty-vt.sh` and the Zig build and cache steps from `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`.
- Keep all `VTerminal` snapshot restoration, re-encoding, formatting, color extraction, and viewport reflow behavior unchanged.
- Guarantee `GHOSTSNP` binary compatibility between GoRex's session server (`si-mods/mygo`) and `GhosttyKitAppStore` before removing `GhosttyVt.xcframework`.

## Non-goals

- Replacing `GhosttyKit` Metal rendering with a custom iOS renderer.
- Changing the `GHOSTSNP` wire protocol or replacing `GHOSTSNP` with plain VT replay on the server.
- Changing desktop or web `libghostty-vt` builds (`si-mods/mygo` and `web/`).

## Design

### Behavior

Users see no change in terminal rendering, reconnect restoration, theme colors, or narrow-viewport reflow. App download and install size decreases by ~3.3 MB on arm64 devices.

Developers and CI runners no longer install Zig or run `ios/scripts/build-ghostty-vt.sh` to build or test Rex Go. `xcodegen generate` and Swift Package Manager resolve `GhosttyKitAppStore` as the single Ghostty dependency.

### Model

`GhosttyKitAppStore.xcframework` exposes two Clang modules from one static library:

| Module | Header | Purpose |
| --- | --- | --- |
| `GhosttyKit` | `ghostty.h` | Metal renderer, surfaces, app lifecycle, and key encoding |
| `GhosttyVt` | `ghostty/vt.h` | Standalone VT terminal, `GHOSTSNP` snapshot codec, and VT formatter |

Keeping `GhosttyVt` as a separate top-level module in `GhosttyKitAppStore.xcframework`'s `module.modulemap` prevents C type or enum collisions between `ghostty.h` and `ghostty/vt.h`. Existing Swift files keep `import GhosttyKit` or `import GhosttyVt` without source churn.

The single static library in `GhosttyKitAppStore` exports the `libghostty-vt` C ABI symbols that `VTerminal` calls:

| Group | Exported C symbols |
| --- | --- |
| Snapshot codec | `ghostty_snapshot_decoder_new_buf`, `ghostty_snapshot_decoder_free`, `ghostty_snapshot_decoder_set`, `ghostty_snapshot_decoder_decode`, `ghostty_snapshot_encode_alloc` |
| Terminal lifecycle | `ghostty_terminal_free`, `ghostty_terminal_vt_write`, `ghostty_terminal_get`, `ghostty_terminal_resize` |
| Formatter | `ghostty_formatter_terminal_new`, `ghostty_formatter_free`, `ghostty_formatter_set`, `ghostty_formatter_format_alloc` |
| Memory | `ghostty_free` |

`VTerminal` in `ios/GoRex/Terminal/VTerminal.swift` stays thread-isolated (`nonisolated`) and operates on independent `GhosttyTerminal` handles on `TerminalFeed`'s background queue. It does not touch `Ghostty.app` or any Metal surface state.

Because `GoRexTests` is a standalone unit-test bundle that compiles `VTerminal.swift` without hosting `GoRex.app`, `GoRexTests` switches its dependency from `GhosttyVt.xcframework` to `GhosttyKitAppStore`.

### Code

#### Upstream changes in `kitknox/ghostty-rootshell` and `kitknox/ghosttykit-rootshell`

1. In `src/main_c.zig`, export the `libghostty-vt` C API symbols from `src/terminal/c/main.zig` (or link the `vt_c` export set into `GhosttyLib.initStatic`) behind a build option or unconditionally for `GhosttyKitAppStore`.
2. Verify that no C symbol names collide between `src/main_c.zig` and `src/terminal/c/main.zig` (such as allocator or color helpers).
3. In `src/build/GhosttyXCFramework.zig`, copy `include/ghostty/vt.h` and `include/ghostty/vt/*.h` alongside `include/ghostty.h` into the xcframework `Headers/` directory.
4. In `include/module.modulemap`, add a second module block `module GhosttyVt { header "ghostty/vt.h" export * }` beside `module GhosttyKit`. Both blocks use `header` rather than `umbrella header`, so Clang emits no `-Wincomplete-umbrella` warnings.
5. Publish a new `kitknox/ghosttykit-rootshell` release tag that includes the merged `GhosttyKitAppStore.xcframework` and verify the exported symbols with `nm -g`.

#### GoRex repository changes

- `ios/project.yml`: bump `GhosttyKit` to the new `exactVersion`, remove `Frameworks/GhosttyVt.xcframework` from `GoRex` and `GoRexTests`, add package product `GhosttyKitAppStore` to `GoRexTests`, and add `-lc++` to `GoRexTests`'s `OTHER_LDFLAGS` if `GhosttyKitAppStore` requires the C++ runtime.
- `ios/scripts/build-ghostty-vt.sh`: delete the script.
- `ios/scripts/upload`: remove the comment referencing `scripts/build-ghostty-vt.sh`.
- `.github/workflows/ios-test.yml` and `.github/workflows/testflight.yml`: remove the `ghostty-vt` cache restore, build, and cache save steps.
- `ios/THIRD_PARTY_NOTICES.md`: update the Ghostty section to state that `libghostty-vt` comes from `GhosttyKitAppStore`.
- `docs/REF-006-session-server.md`: update the iOS section to document that `VTerminal` uses `libghostty-vt` exported from `GhosttyKitAppStore` and note the `GHOSTSNP` compatibility requirement.

## Alternatives

- **Maintain a `si-mods` fork of `ghostty-rootshell` and `ghosttykit-rootshell`**: this gives GoRex direct control over the pinned Ghostty commit and immediate delivery without upstream review. It loses because GoRex would have to rebase rootshell's iOS patches, run the multi-platform xcframework release pipeline, and host binary SPM artifacts.
- **Replace `GHOSTSNP` with VT replay on the server for iOS**: this avoids linking `libghostty-vt` on iOS for initial restore. It loses because `TerminalFeed` also uses `VTerminal` as a local shadow emulator (`TerminalFeed.shadow` and `VTerminal.reflowedReplay`) to reflow wide desktop PTY redraws onto narrow iPhone viewports.
- **Keep `GhosttyVt.xcframework` as a separate static library**: this keeps `si-mods/mygo` and iOS pinned to the exact same Ghostty commit. It loses because the app keeps ~3.3 MB of duplicate arm64 code and CI keeps the extra Zig build pipeline.

## Migration

`GHOSTSNP` format version 1 does not yet carry an upstream binary-compatibility guarantee (`include/ghostty/vt/snapshot.h`). As noted in [`REF-006`](REF-006-session-server.md), `GhosttyVt.xcframework` is currently pinned to the exact Ghostty commit used by `si-mods/mygo` (`108cacb17ed8990840cd14abfce43d8ed8503ca1`).

Before switching `ios/project.yml` to the merged `GhosttyKitAppStore`:

1. Confirm that `kitknox/ghostty-rootshell`'s `src/terminal/snapshot/` matches or supersedes the `GHOSTSNP` v1 record layout used by `si-mods/mygo`, including unknown-record skipping in `src/terminal/snapshot/record.zig`.
2. Run `GoRexTests/ProtocolTests` and `GoRexTests/TerminalFeedTests` against `internal/rexapi/testdata/rex-terminal-stream.bin` and a live snapshot from the current GoRex server.
3. Add a CI compatibility check or fixture refresh rule so future `si-mods/mygo` or `GhosttyKit` bumps catch `GHOSTSNP` drift before release.

Old installed iOS builds and existing GoRex servers require no data migration because the `GHOSTSNP` wire envelope does not change.

## Tests

- **Symbol and module verification**: run `nm -g` on `GhosttyKitAppStore.xcframework`'s `ios-arm64` and `ios-arm64-simulator` slices to confirm all 14 `libghostty-vt` C symbols are exported and no duplicate symbol errors occur at link time.
- **Unit tests**: run `.github/workflows/ios-test.yml` (`GoRexTests/ProtocolTests` and `GoRexTests/TerminalFeedTests`) to verify `GHOSTSNP` decoding, `retainContinuation`, re-encoding, formatting, color extraction, and `reflowedReplay`.
- **Archive and size check**: run `DESTINATION=export ios/scripts/upload` on the Mac mini and compare the exported arm64 binary size before and after to confirm the ~3.3 MB reduction.
- **End-to-end check**: attach Rex Go on a simulator or device to a live GoRex server, verify initial screen restore, theme colors, and narrow-viewport reflow during streaming output.
- **Invariants**: `docs/invariants.md` item 3 ("A pane's text is the session's screen") is exercised by `ProtocolTests` and `TerminalFeedTests`.

## Phases

1. Align `GHOSTSNP` snapshot format version and submit the `libghostty-vt` export and header changes to `kitknox/ghostty-rootshell` and `kitknox/ghosttykit-rootshell`.
2. Switch `ios/project.yml` to the new `GhosttyKitAppStore` release, remove `GhosttyVt.xcframework`, delete `ios/scripts/build-ghostty-vt.sh`, update CI workflows and docs, and verify size and tests on the Mac mini.

## Open questions

1. **Upstream acceptance**: will `kitknox/ghostty-rootshell` accept a PR exporting `libghostty-vt` symbols and headers in `GhosttyKitAppStore`, or should GoRex maintain a `si-mods` fork if rootshell declines? Owner decides after checking with upstream.
2. **`GHOSTSNP` version skew**: `kitknox/ghostty-rootshell` currently lags behind `si-mods/mygo`'s Ghostty commit (`src/terminal/snapshot/record.zig` in rootshell rejects unknown tags instead of skipping them). Should Phase 1 wait for `kitknox/ghostty-rootshell` to rebase past `108cacb17ed8990840cd14abfce43d8ed8503ca1`, or pin both `si-mods/mygo` and `GhosttyKitAppStore` to a shared Ghostty revision? Owner decides.
3. **Binary size impact on `GoRexTests`**: linking `GhosttyKitAppStore` into `GoRexTests` pulls the full Ghostty static library into the test bundle. Confirm during Phase 2 that `ios-test.yml` link time and test runtime remain acceptable on the Mac mini runner.
