---
name: MOD-014
description: "Terminal data streams can compress program output and graphics to save bytes and time on slow links. Use when you change the data stream wire format, the stream resume, or a client's terminal feed."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# MOD-014: Compressed terminal output

Status: accepted. Not implemented. The user picked option 1 on 2026-11-26.

## Problem

A terminal data stream sends every output byte as it is. A scrollback replay, a `cat` of a large file, or a Kitty graphics image moves the full bytes over the connection. On a slow link, such as a cellular tailnet or an `ssh://` host over WAN, the stream costs more data and arrives later.

The wire path is `internal/rexapi/data.go`. `(*stream).feedOutput` appends program output to `s.buf`, and `(*stream).write` sends the buffer to the WebSocket in `maxChunk` messages. Nothing compresses. Rex 0.1.1 compresses terminal output and images on its built-in Tailscale and `ssh://` connections. The changelog names the same two paths GoRex clients use.

## Background: how Rex does it

The Rex 0.1.1 server binary shows the design:

- `blocks/terminal/internal/mux` queues output chunks per client. A timer runs `compressPass`, which rewrites queued output into compressed chunks before the writer sends them (`finishCompressedRewriteLocked`). `WithScrollbackCompression` compresses the scrollback replay too.
- `blocks/terminal/internal/parzlib` is a parallel zlib encoder. It splits input into segments, compresses them with preset dictionaries, and writes an Adler-32 checksum for each segment.
- `kittyapc.(*Transmission).Compressible` marks a Kitty graphics transmission that compression helps.
- The compression lives in the stream content, not in the transport. That is why Tailscale and `ssh://` both benefit.
- `zstd` appears in the binary only inside Tailscale's own control client. It is not the wire codec.

## Goal

- A terminal data stream between a new client and a new server compresses program output and Kitty graphics.
- An old client talks to a new server, and a new client talks to an old server, without change. The choice is per connection.
- `RESUME`, `collapse`, and `mark` keep their current meaning: stream positions count the bytes on the wire.
- The iOS client inflates frames in order and counts wire bytes for `RESUME`, not inflated bytes.

## Non-goals

- The control connection stays JSON. Its messages are small.
- `bytes` streams for uploads, downloads and tunnels stay as they are. `gorex.upload.deflate-raw` already covers upload bodies.
- Client-to-server input stays small and uncompressed.
- The web client, the desktop app, `internal/cli` and `internal/rexpeer` keep `rex.data.terminal.v1` in this phase.
- No user-facing setting in this phase.

## Design

### Behavior

The user sees nothing new. On a slow link, output needs less data and shows up sooner. A stream that floods the connection now reaches `collapse` later, because `collapseBytes` counts compressed bytes.

### Negotiation

1. The server lists `gorex.terminal.deflate-raw` in the control `welcome` `features`, next to `gorex.upload.deflate-raw`.
2. A client that sees the feature asks for the subprotocol `gorex.data.terminal.v1` in `data.connect` or in `session.bootstrap` `data_tickets`. A client that does not see it asks for `rex.data.terminal.v1` as before.
3. `methodDataConnect` stores the subprotocol on the ticket as it does today. `dataHello` accepts `gorex.data.terminal.v1` when the ticket names it, and marks the stream compressed.
4. The WebSocket handshake echoes the subprotocol. `dataResume` keeps the existing check that a resumed connection offers the same subprotocol.
5. A client that tries the new subprotocol on an old server gets WELCOME, then a close with `data provider unavailable`, because the old `dataHello` accepts only `rex.data.terminal.v1`. The client retries with `rex.data.terminal.v1`. The feature flag makes this rare.

### Wire format

A compressed run is one counted binary frame in the stream:

```
ESC _ "rexz" ; <nonce> ; <rawlen> ; <clen> ESC \ <clen payload bytes>
```

- `<nonce>` is the channel nonce from WELCOME, as in `rex1` messages. A frame with another nonce is program output and is consumed and ignored, like a `rex1` message with a wrong nonce.
- The header is ASCII digits and `;`. It ends at `ESC \`, so the parser does not scan the payload for a terminator.
- The payload is `<clen>` bytes of raw DEFLATE (RFC 1951). It inflates to exactly `<rawlen>` bytes of the stream. Each frame is a complete, independent deflate stream. No frame depends on earlier data.
- The parser takes the frame and the payload out of the stream, inflates it, and feeds the result as ordinary stream bytes. A compressed Kitty graphics transmission needs no special case.

Rex uses zlib streams with preset dictionaries and segmented Adler-32 checksums. GoRex uses plain raw DEFLATE first, which matches `deflate-raw` on uploads. A shared dictionary is a later option in "Open questions".

### Server

`internal/rexapi/data.go` gains a small compressing buffer on a compressed stream:

- `feedOutput` feeds `s.vt` the raw bytes as today, then hands the bytes to the buffer instead of `appendBytes`.
- The buffer emits a `rexz` frame into `s.buf` when the pending raw run reaches `zMin` (4 KiB), or before any raw append must follow it. Pending bytes below `zMin` go out raw at a flush.
- Raw appends stay raw: `rex1` messages from `appendApc`, `resyncReset`, and WELCOME. `flushPending` and `applyResync` flush the compressor first, so raw messages keep their order after the output.
- The Ghostty snapshot and the resync snapshot go through the buffer like other program bytes. `mark` still points to the end of the newest snapshot, now a frame boundary.
- `appendBytes`, `write`, `dropUnsent`, `collapse` and the resume path do not change. They count `s.buf` bytes, which now hold a mix of raw bytes and frames.

Limits: pending raw data caps at `zMaxPending` (256 KiB), a frame payload caps at `maxFrame` (256 KiB compressed, 4 MiB inflated). Compression uses `compress/flate` at default speed. `klauspost/compress` is a possible faster codec later, not a new dependency now.

### iOS client

`TerminalStream.feed` in `ios/GoRex/Rex/DataStream.swift` learns the `rexz` marker:

1. Find the marker, emit `.output` for the bytes before it, parse the header up to `ESC \`.
2. If the nonce differs, drop the header and the next `clen` bytes.
3. Wait for `clen` payload bytes across messages. `clen` above `maxFrame` ends the stream.
4. Inflate, check `rawlen`, and feed the result through `feed` again, so `.snapshot`, `.output` and `.message` items come out in order.
5. The `received` count for `DataHandshake.resume` still counts WebSocket bytes, before inflation.

`TerminalSession` and `DataStream` pick the subprotocol from the `welcome` features and pass it through `data.connect` and `session.bootstrap`. `RexProtocol.gen.swift` gets the new constant from `internal/rexproto/protocol.json` through `render.go`.

Decompression uses the `Compression` framework (`COMPRESSION_ZLIB` reads raw DEFLATE), with `zlib` `inflate` as the fallback if the framework disagrees.

## Options for the irreversible choice

`docs/irreversible.md` applies: this adds a wire-protocol element that shipped builds must agree on. Pick one.

1. **Recommended: `gorex.data.terminal.v1` subprotocol plus the counted `rexz` frame.** A GoRex-named subprotocol cannot collide with a future `rex.data.terminal.v2` that means something else. The counted frame keeps the full compression ratio and handles the snapshot path for free.
2. **`rex.data.terminal.v2` subprotocol plus the `rexz` frame.** The name follows the `v1` lineage, but the `rex.*` namespace belongs to Rex. If Rex ships a different `v2`, a GoRex server would honor it with GoRex meaning.
3. **`rexz` frame avoided: base64 deflate inside a `rex1` JSON message (`{"type":"z","d":"…"}`).** No new framing on the client. It costs about 33% more wire bytes per frame, hits the 1 MiB `rex1` cap sooner, and gives `.message` items that the consumer must turn back into output, including for the snapshot.
4. **Whole-stream deflate under the existing subprotocol.** Simplest writer, but `RESUME` replays stream bytes from a count, and a mid-deflate-stream position cannot resume without checkpoints. It would change the resume semantics every client relies on.

## Migration

| Mix | Result |
| --- | --- |
| New app, new server | Feature seen, `gorex.data.terminal.v1`, compressed stream. |
| New app, old server | No feature in `welcome`, app requests `rex.data.terminal.v1`, raw stream. A guessed new subprotocol gets WELCOME then close, and the app retries `v1`. |
| Old app, new server | App requests `v1`, server sends raw. The server never sends `rexz` on `v1`. |
| Rex (superlogical) client, GoRex server | It offers its own subprotocols. `gorex.data.terminal.v1` is GoRex's name. |
| Web, desktop, CLI, `rexpeer` | They request `v1` and see no change. |

Nothing is written to disk. A server upgraded in place keeps existing streams on `v1`, and new connections negotiate on their own.

## Security

- `clen` and `rawlen` are attacker-controlled fields for a malicious server or a MITM on a plain link. The client caps `clen` at `maxFrame` and `rawlen` at 4 MiB, and drops the stream on a mismatch after inflation.
- The channel nonce keeps a program's output from impersonating `rexz`, as `rex1` nonces do today.
- Compression can worsen a chosen-plaintext leak on encrypted transports, as any compression can. The streams carry terminal output the client already sees. No new secret crosses them.

## Tests

- `internal/rexapi`: contract test that a `gorex.data.terminal.v1` ticket gets frames and a `v1` ticket does not. Round-trip: feed output, inflate the frames, get the bytes back. Order test: `rex1` message between two runs lands correctly. Resume test: RESUME mid-frame replays the same bytes. Snapshot test on a compressed stream.
- `ios/GoRexTests`: `TerminalStream.feed` with frames split across deliveries, nonce mismatch, `clen` over the cap, and a compressed `GHOSTSNP`. `RexParityTests` and `FakeRex` get a `rexz` helper. `WhatToTest.md` bullet in each locale.
- Manual: on `ssh macmini`, build and run on a device over a slow link (Network Link Conditioner). Compare bytes for `cat` of a large file and a Kitty image. Check `docs/invariants.md` items on the data stream, if any, and read `docs/lessons.md` before the PR.

## Phases

1. Server: negotiation, `rexz` writer, contract tests.
2. iOS: `TerminalStream` frames, subprotocol choice, tests.
   The two ship in one PR if review stays easy.

## Open questions

1. The irreversible choice is made: option 1, `gorex.data.terminal.v1` plus the `rexz` frame.
2. A shared preset dictionary for small frames, as Rex's `parzlib` uses. It improves the ratio on small runs and needs the dictionary on both ends. Do it as a later revision, or skip it.
3. Whether `gorex.data.terminal.v1` should also appear on the web client's `web/src/pane.ts` and on `internal/cli` in a later phase.
