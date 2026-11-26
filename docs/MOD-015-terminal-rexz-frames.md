---
name: MOD-015
description: "Fallback to MOD-014: counted `rexz` deflate frames inside the terminal data stream, under a `gorex.data.terminal.v1` subprotocol, for when WebSocket permessage-deflate cannot ship. Use when you change the data stream wire format, the stream resume, or a client's terminal feed."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# MOD-015: `rexz` compressed stream frames (fallback)

Status: deferred — do not implement. This is the kept fallback for [MOD-014](MOD-014-terminal-stream-compression.md) option 1 (WebSocket permessage-deflate). Pick it up only if the MOD-014 device test shows Apple's WebSocket client still drops on large compressed messages after `CompressionNoContextTakeover` and the `compressedChunk` cap. The design below was the earlier MOD-014 draft and is preserved as the second wire-format candidate.

## Problem

Same as MOD-014: the terminal data stream moves every output byte raw, so floods and Kitty graphics cost full bytes on slow links. If the WebSocket extension cannot be used — the iOS drop, or a data transport that is not a WebSocket — compression must live in the stream content instead.

## How it differs from MOD-014

- Compression rides inside the stream as counted frames, so it works over any transport (WebSocket, an `ssh://` byte pipe, a future carrier) and never touches WebSocket bugs.
- It is a private GoRex protocol: a new subprotocol, a feature flag, a frame parser, inflate and order logic on the iOS client, and a rule for RESUME mid-frame.
- Each frame is an independent deflate stream, so the ratio matches no-context-takeover permessage-deflate (~2–4x on text floods), not a rolling window.

## Negotiation

1. The server lists `gorex.terminal.deflate-raw` in the control `welcome` `features`, next to `gorex.upload.deflate-raw`.
2. A client that sees the feature asks for the subprotocol `gorex.data.terminal.v1` in `data.connect` or in `session.bootstrap` `data_tickets`. A client that does not see it asks for `rex.data.terminal.v1` as before.
3. `methodDataConnect` stores the subprotocol on the ticket as it does today. `dataHello` accepts `gorex.data.terminal.v1` when the ticket names it, and marks the stream compressed.
4. The WebSocket handshake echoes the subprotocol. `dataResume` keeps the existing check that a resumed connection offers the same subprotocol.
5. A client that tries the new subprotocol on an old server gets WELCOME, then a close with `data provider unavailable`, because the old `dataHello` accepts only `rex.data.terminal.v1`. The client retries with `rex.data.terminal.v1`. The feature flag makes this rare.

## Wire format

A compressed run is one counted binary frame in the stream:

```
ESC _ "rexz" ; <nonce> ; <rawlen> ; <clen> ESC \ <clen payload bytes>
```

- `<nonce>` is the channel nonce from WELCOME, as in `rex1` messages. A frame with another nonce is program output and is consumed and ignored, like a `rex1` message with a wrong nonce.
- The header is ASCII digits and `;`. It ends at `ESC \`, so the parser does not scan the payload for a terminator.
- The payload is `<clen>` bytes of raw DEFLATE (RFC 1951). It inflates to exactly `<rawlen>` bytes of the stream. Each frame is a complete, independent deflate stream. No frame depends on earlier data.
- The parser takes the frame and the payload out of the stream, inflates it, and feeds the result as ordinary stream bytes. A compressed Kitty graphics transmission needs no special case.

GoRex uses plain raw DEFLATE, which matches `deflate-raw` on uploads. A shared dictionary is a later option.

## Server

`internal/rexapi/data.go` gains a small compressing buffer on a compressed stream:

- `feedOutput` feeds `s.vt` the raw bytes as today, then hands the bytes to the buffer instead of `appendBytes`.
- The buffer emits a `rexz` frame into `s.buf` when the pending raw run reaches `zMin` (4 KiB), or before any raw append must follow it. Pending bytes below `zMin` go out raw at a flush.
- Raw appends stay raw: `rex1` messages from `appendApc`, `resyncReset`, and WELCOME. `flushPending` and `applyResync` flush the compressor first, so raw messages keep their order after the output.
- The Ghostty snapshot and the resync snapshot go through the buffer like other program bytes. `mark` still points to the end of the newest snapshot, now a frame boundary.
- `appendBytes`, `write`, `dropUnsent`, `collapse` and the resume path do not change. They count `s.buf` bytes, which now hold a mix of raw bytes and frames.

Limits: pending raw data caps at `zMaxPending` (256 KiB), a frame payload caps at `maxFrame` (256 KiB compressed, 4 MiB inflated). Compression uses `compress/flate` at default speed.

## iOS client

`TerminalStream.feed` in `ios/GoRex/Rex/DataStream.swift` learns the `rexz` marker:

1. Find the marker, emit `.output` for the bytes before it, parse the header up to `ESC \`.
2. If the nonce differs, drop the header and the next `clen` bytes.
3. Wait for `clen` payload bytes across messages. `clen` above `maxFrame` ends the stream.
4. Inflate, check `rawlen`, and feed the result through `feed` again, so `.snapshot`, `.output` and `.message` items come out in order.
5. The `received` count for `DataHandshake.resume` still counts WebSocket bytes, before inflation.

`TerminalSession` and `DataStream` pick the subprotocol from the `welcome` features and pass it through `data.connect` and `session.bootstrap`. `RexProtocol.gen.swift` gets the new constant from `internal/rexproto/protocol.json` through `render.go`.

Decompression uses the `Compression` framework (`COMPRESSION_ZLIB` reads raw DEFLATE), with `zlib` `inflate` as the fallback if the framework disagrees.

## Rejected alternatives

- `rex.data.terminal.v2` as the subprotocol name: the `rex.*` namespace belongs to Rex; a GoRex server would honor a future Rex `v2` with GoRex meaning.
- Base64 deflate inside a `rex1` JSON message: ~33% more wire bytes per frame, hits the 1 MiB `rex1` cap sooner, and `.message` items must be turned back into output including for the snapshot.
- Whole-stream deflate under the existing subprotocol: a mid-deflate-stream position cannot resume without checkpoints, changing the resume semantics every client relies on.

## Migration

| Mix | Result |
| --- | --- |
| New app, new server | Feature seen, `gorex.data.terminal.v1`, compressed stream. |
| New app, old server | No feature in `welcome`, app requests `rex.data.terminal.v1`, raw stream. A guessed new subprotocol gets WELCOME then close, and the app retries `v1`. |
| Old app, new server | App requests `v1`, server sends raw. The server never sends `rexz` on `v1`. |
| Rex (superlogical) client, GoRex server | It offers its own subprotocols. `gorex.data.terminal.v1` is GoRex's name. |

Nothing is written to disk. A server upgraded in place keeps existing streams on `v1`, and new connections negotiate on their own.

## Security

- `clen` and `rawlen` are attacker-controlled fields for a malicious server or a MITM on a plain link. The client caps `clen` at `maxFrame` and `rawlen` at 4 MiB, and drops the stream on a mismatch after inflation.
- The channel nonce keeps a program's output from impersonating `rexz`, as `rex1` nonces do today.
- Compression can worsen a chosen-plaintext leak on encrypted transports, as any compression can. The streams carry terminal output the client already sees. No new secret crosses them.

## Tests

- `internal/rexapi`: contract test that a `gorex.data.terminal.v1` ticket gets frames and a `v1` ticket does not. Round-trip: feed output, inflate the frames, get the bytes back. Order test: `rex1` message between two runs lands correctly. Resume test: RESUME mid-frame replays the same bytes. Snapshot test on a compressed stream.
- `ios/GoRexTests`: `TerminalStream.feed` with frames split across deliveries, nonce mismatch, `clen` over the cap, and a compressed `GHOSTSNP`. `RexParityTests` and `FakeRex` get a `rexz` helper. `WhatToTest.md` bullet in each locale.
