---
name: REF-009
description: "How the session server's hot paths are measured: emit benchmarks, an attach-stall measurement and a differential test for scan changes. Use when you change `session.emit`, its output scans or the attach snapshot."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-009: Benchmarks for the session server's hot path

Status: phases 1 and 2 built (#1062, #1066). Phase 3 measured and declined.

The session server reads each PTY in a loop. Every chunk goes to a terminal emulator, to output trackers and to the attached clients (`session.emit` in `internal/rex/session.go`). This doc decides how that loop, and the attach that shares its lock, are measured. [REF-006](REF-006-session-server.md) describes the session and its data flow.

## Problem

Two costs on this path have no measurement.

- `emit` scans each chunk more than once. `graphics.filter`, `decModes.feed`, `programStatus.feed`, `programIcon.feed` and `batch` each walk the same bytes for an ESC. `vt.Feed` runs between them. No benchmark covers this loop.
- An attach that asks for escape sequences renders the whole scrollback under `session.mu` (`snapshotLocked`). A resync already renders outside the lock (`renderResync`). A benchmark that reports nanoseconds per operation cannot show what the held lock costs. The cost is the pause of every other client.

Without an instrument a change here cannot show its effect.

## Goal

- `BenchmarkEmit` reports `ns/op`, `B/op` and `allocs/op` for each load the loop sees.
- `BenchmarkAttachStall` reports the longest pause that an escape-sequence attach adds to `emit`.
- A differential test proves that a merged scanner gives the same results as the feeds it replaces.
- The benchmarks name the changes they measure. A change that does not move its number does not merge.

## Non-goals

- No optimization lands in this work. The off-lock attach and the merged scan are each their own PR that this instrument measures.
- No CI gate. Benchmarks run on the developer's machine. Results differ across machines.
- No pprof integration. The helpers `flood` and `styledSession` produce enough load.

## Design

### Loads

Each benchmark replays generated output in chunks of 4 KiB, as `flood` does. A chunk can cut a sequence anywhere.

| Load | Content | What it exercises |
| --- | --- | --- |
| `plain` | UTF-8 text with no ESC | The fast path of every scan. |
| `styles` | The colored lines of `flood` | CSI-dense output, the common case. |
| `osc` | Lines mixed with OSC 133, 7501 and 7502 | The trackers' body parsing. |
| `graphics` | Lines mixed with Kitty APC chunks | The scans over APC sequences. |

A generated stream keeps each load's share stable. Real scrollback is not reproducible.

### Benchmarks

`BenchmarkEmit` feeds the chunks of one load to `session.emit` with no client attached. It runs once per load as a sub-benchmark.

`BenchmarkEmitClients` does the same with attached clients whose writers drain the queue. It measures `attached.send` and the per-client queue.

`BenchmarkAttachStall` runs one goroutine that emits a small chunk in a loop and records the duration of each call. Another goroutine attaches with `Attach{Framed: true, History: resyncHistory, Mute: true}`, as `stream.attachAgain` does in `internal/rexapi/data.go`. The benchmark reports the largest recorded duration as the custom metric `ns/stall`. It must not read `ns/op`. An average hides the pause.

### Correctness instrument

The merged scan changes behavior only if it answers differently. `TestScanFeedsMatch` sends the same input, cut at every possible point, to the three feeds and to the merged scanner. The records, the mode state, the icon, the query counts and the carries must be equal.

### Baseline

The benchmark PR records the numbers of its first run in the PR body, with the machine and the date. This doc records them here after the merge. Numbers compare only to numbers from the same machine.

Apple M5, 2026-11-24, `-benchtime=500ms -count=6`, medians:

| Benchmark | Result | Throughput | B/op | allocs/op |
| --- | --- | --- | --- | --- |
| `BenchmarkEmit/plain` | 11.8 µs | 346 MB/s | 48 | 3 |
| `BenchmarkEmit/styles` | 19.6 µs | 208 MB/s | 498 | 3 |
| `BenchmarkEmit/osc` | 27.3 µs | 150 MB/s | 5117 | 58 |
| `BenchmarkEmit/graphics` | 28.2 µs | 145 MB/s | 947 | 3 |
| `BenchmarkEmitClients` | 22.1 µs | 186 MB/s | 639 | 6 |
| `BenchmarkAttachStall` | 14.5 ms stall | — | — | — |

The attach stall matches `BenchmarkSnapshotLocked`. Every client of the session sees it on each escape-sequence attach. After #1066 the stall is ~2.0 ms, what `GhosttySnapshot` costs under the lock.

## Alternatives

- **pprof under a synthetic run**: more faithful and more work. The test helpers already produce the loads. Revisit when the benchmarks disagree with a profile.
- **`ns/op` of the attach**: the render does not get faster. The time the lock is held is the result. An average hides it.
- **A CI gate on the numbers**: machine variance makes the gate flaky. The numbers inform reviews. They do not block merges.

## Tests

- `BenchmarkEmit*` runs and gives stable numbers across `-count=6`.
- `BenchmarkAttachStall` reports a pause near the `BenchmarkSnapshotLocked` time for the current attach.
- `TestScanFeedsMatch` compares both scanners on input cut at each byte. Phase 3 is declined, so it is not built.
- `TestAttachRendersOffLock` lands with phase 2. It compares the off-lock attach's snapshot and `snapshotLocked`'s byte for byte.
- No item of `docs/invariants.md` applies.

## Phases

1. The emit benchmarks and the attach-stall benchmark: #1062.
2. The off-lock attach snapshot, measured by `BenchmarkAttachStall`: #1066. The stall fell 14.5 ms to ~2.0 ms.
3. The merged scan: declined. Removing all three feeds saves at most 19% of `emit` on ESC-dense loads and nothing on plain ones. A merged scan keeps the record building and saves only the shared walks, less than that ceiling, and it couples three different carry rules into one pass.
