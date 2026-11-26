---
name: MOD-014
description: "Terminal data streams compress program output and graphics with WebSocket permessage-deflate to save bytes and time on slow links. Use when you change the data connection's WebSocket options, the stream resume, or a client's terminal feed."
metadata:
  created: "2026-11-26"
  updated: "2026-11-26"
---

# MOD-014: Compressed terminal output

Status: accepted. Not implemented. The user picked option 1 (WebSocket permessage-deflate) on 2026-11-26.

## Problem

A terminal data stream sends every output byte as it is. A scrollback replay, a `cat` of a large file, or a Kitty graphics image moves the full bytes over the connection. On a slow link, such as a cellular tailnet or an `ssh://` host over WAN, the stream costs more data and arrives later.

The wire path is `internal/rexapi/data.go`. `(*stream).feedOutput` appends program output to `s.buf`, and `(*stream).write` sends the buffer to the WebSocket in `maxChunk` messages. Nothing compresses. Rex 0.1.1 compresses terminal output and images on its built-in Tailscale and `ssh://` connections. The changelog names the same two paths GoRex clients use.

## Background: how Rex does it

A live capture against the Rex 0.1.1 server (snapshot 2146) shows the wire mechanism is plain WebSocket permessage-deflate, not a stream-level format:

- A data WebSocket that offers `Sec-WebSocket-Extensions: permessage-deflate` gets it, with `server_no_context_takeover` and `client_no_context_takeover` in the answer. The subprotocol stays `rex.data.terminal.v1`; HELLO, WELCOME and the stream contents are unchanged.
- Output then arrives as binary WebSocket messages with the RSV1 bit set. Roughly 1.4 MB of `jot` output moved in about 700 KB of compressed payloads — modest because each message deflates alone, without history.
- Small frames and handshake messages go out uncompressed. Stream byte counts are logical bytes: the client counts what `receive` delivers, so RESUME needs no compression state.
- The binary's `parzlib` segmented encoder (`blocks/terminal/internal/parzlib`, preset dictionaries, per-segment Adler-32, `WithScrollbackCompression`, `compressed_pages`) compresses scrollback pages for memory and suspend/restore inside the mux. It does not appear in the stream: a backlogged, uncompressed capture shows no in-band compressed segments.
- `zstd` appears only inside Tailscale's own control client. It is not the wire codec.

Our own code names the one hazard: `internal/rexapi/control.go` disables compression because "Rex's iPhone app offers it, but drops the connection on a large compressed message as this library writes it (seen with block_creator.list, ~50 KB)". Apple's WebSocket client negotiates permessage-deflate on its own; the failure was on the server→client write path of a large compressed message.

## Goal

- A data WebSocket between ends that both speak permessage-deflate compresses terminal output and Kitty graphics.
- No negotiation of ours: the WebSocket extension does it. A client or server without it sees a raw stream, with no flag, subprotocol or retry.
- `RESUME`, `collapse`, `mark` and the reconnect-token path keep their meaning untouched: they count logical stream bytes.
- The known large-compressed-message drop is measured again under the shipped settings, and the design avoids it.

## Non-goals

- The control connection stays uncompressed. Its messages are mostly small, and the ~50 KB drop was seen exactly there. Enabling it on data does not imply enabling it on control.
- `bytes` streams for uploads, downloads and tunnels stay as they are.
- Client-to-server input stays small and uncompressed.
- No server-internal scrollback compression. Rex's `parzlib` role is out of scope.
- No user-facing setting in this phase.

## Design

### Behavior

The user sees nothing new. On a slow link, output needs less data and shows up sooner.

### Negotiation

1. `internal/rexapi/data.go` accepts `permessage-deflate` on `/api/data` with `CompressionNoContextTakeover`, matching what the Rex server negotiates. `CompressionThreshold` keeps the handshake messages and small frames raw.
2. The iOS client changes nothing on the wire: `URLSessionWebSocketTask` offers the extension itself and inflates transparently. It records whether the upgrade answer carried the extension, for diagnostics.
3. Go clients (`internal/rexapi/cli.go` `dataHello`, `internal/rexpeer`, the desktop) opt in with the same no-context-takeover mode in their `websocket.DialOptions`.
4. Old mixes need no table: a server that ignores the extension answers a raw stream, and a client that does not offer it gets one.

### The large-message drop

The reported failure was a ~50 KB compressed message written by `coder/websocket` read by Apple's client. Two changes should cover it, both verified on a device before this ships:

- `CompressionNoContextTakeover` emits each message as a fresh deflate stream, the form the Rex server writes. Whether Apple's client accepts that form past ~50 KB is untested — the device test decides.
- `(*stream).write` already chunks `s.buf` into `maxChunk` (64 KiB) messages. Compressed writes cap at a lower `compressedChunk` (32 KiB wire bytes), so no single compressed message approaches the size that dropped. Splitting is free: each message is an independent deflate stream under no-context-takeover.

If a device still drops on these settings, the fallback is option 2 below (content frames), not a per-client transport exception that the upgrade response cannot express.

### Server

`internal/rexapi/data.go` and `clients.go` (whichever accepts `/api/data`) set `CompressionMode: websocket.CompressionNoContextTakeover` on the data `Accept`, and `CompressionThreshold` at a few hundred bytes. `(*stream).write` splits at `compressedChunk` when the negotiated compression is on (`ws.CompressionMode()` reports it on the conn), else at `maxChunk`. Everything else — `s.buf`, `appendBytes`, `dropUnsent`, `collapse`, `feedResumed`, `applyResync` — is unchanged, because the WebSocket layer compresses under the byte stream.

No type or field on the ticket, HELLO, WELCOME or subprotocol changes.

### iOS client

`TerminalStream.feed` and `DataHandshake` are untouched: delivered bytes are already inflated. `TerminalStream` reads `Sec-WebSocket-Extensions` from the upgrade `HTTPURLResponse`, stores it, and logs it in the ConnectionLog WELCOME line; a `read` drop on a compressed connection notes that a large compressed message may have dropped the socket, mirroring the control.go comment. That diagnostic piece ships on its own first, since it is useful whether or not the server compresses.

### Other clients

`internal/cli`, the desktop app and `rexpeer` opt in per `websocket.DialOptions`. The web client gets compression wherever its browser `WebSocket` enables it, which is automatic and needs no code.

## Expected gain

Every compressed message deflates alone (no context takeover), so there is no cross-message history to borrow. The win is real but bounded:

| Load | Expected ratio | Basis |
| --- | --- | --- |
| Text floods: logs, `cat` of a large file | ~2–4x | Measured on the Rex server: ~1.4 MB of `jot` output took ~700 KB on the wire (2x); ANSI text at message granularity runs a bit better on varied content |
| Ghostty snapshot and scrollback replay | ~2–5x | Structured cells plus repeated text |
| Kitty graphics (base64) | ~1.3x | Mostly earns back the base64 tax |
| Already-compressed bytes (a binary `cat`) | ~1x | Deflate degenerates but does not hurt |
| Interactive typing | none | Below `CompressionThreshold`, messages go raw with identical latency |

So on a slow link a flood arrives in about half to a third of the time, and a burst reaches the `collapse` backlog about twice as late. A rolling-window codec would beat this (~7–12x on text), which is what a future context-takeover or dictionary design could revisit — at the price of the state this design avoids.

CPU cost is a few hundred microseconds per 64 KiB message and invisible next to the link.

## Options for the irreversible choice

`docs/irreversible.md` applies: the choice fixes what shipped builds do on the wire. Pick one.

1. **Recommended and picked: WebSocket permessage-deflate, no context takeover.** What Rex does. No GoRex protocol element at all — negotiation, framing and fallback are the standard WebSocket extension, and stream byte counts stay logical, so RESUME is free. The cost is the known Apple-side large-message drop, handled above by message capping and a device test, and the limit that compression exists only where the transport is a WebSocket.
2. **Counted `rexz` content frames under a `gorex.data.terminal.v1` subprotocol**, kept as [MOD-015](MOD-015-terminal-rexz-frames.md). Independent of the transport and of WebSocket bugs, but it is a new private protocol: subprotocol, feature flag, frame parser, inflate and order logic on iOS, and a resume-mid-frame rule.
3. **Hybrid: permessage-deflate where it negotiates, `rexz` only where it cannot.** Two formats to keep. Do this only if the device test shows WebSocket compression unreachable on iOS.

## Migration

| Mix | Result |
| --- | --- |
| New app, new server | Extension negotiated, compressed messages. |
| New app, old server | Server ignores the extension, raw stream. |
| Old app, new server | App may or may not offer the extension; either way the stream parses as before. |
| Rex (superlogical) client, GoRex server | Rex's client offers permessage-deflate and gets compressed messages — the form it already reads. |
| Web, desktop, CLI, `rexpeer` | Each opts in when convenient; nothing breaks before. |

Nothing is written to disk. A server upgraded in place keeps open streams as they are, and new data connections negotiate per connection.

## Security

- Compression under TLS or WireGuard can widen a chosen-plaintext leak, as any compression can. The stream carries terminal output the client already sees; no new secret crosses it.
- A malicious server can send inflated bombs at the WebSocket layer; `URLSessionWebSocketTask.maximumMessageSize` (1 MiB) already caps delivered messages, and Go clients keep `SetReadLimit`.
- `no_context_takeover` avoids compressor state that could bleed data between streams that share a socket pool.

## Tests

- `internal/rexapi`: accept a data connection with a deflating client and assert the same stream bytes arrive; assert `CompressionNoContextTakeover` is what the server negotiates; assert messages split at `compressedChunk`; RESUME mid-stream replays identical bytes with and without compression.
- iOS device check before ship: a data stream that carries several compressed messages well past 50 KiB — a big `GHOSTSNP` scrollback replay — must not drop. This is the gate for option 1 versus option 2.
- Manual: on `ssh macmini`, build and run on a device over a slow link (Network Link Conditioner). Compare bytes for `cat` of a large file and a Kitty image. Check `docs/invariants.md` items on the data stream, if any, and read `docs/lessons.md` before the PR.

## Phases

1. iOS diagnostic logging for the negotiated extension and the compressed-drop hint (independent, ships first).
2. Server: accept permessage-deflate on `/api/data`, chunk compressed writes, contract tests.
3. Optional: opt-in on the Go clients (`internal/cli`, desktop, `rexpeer`).
4. Optional: control connection compression, once the data path proves the drop is gone.

## Open questions

1. The irreversible choice is made: option 1, WebSocket permessage-deflate with no context takeover.
2. Exact `compressedChunk` value: 32 KiB is a guess below the reported 50 KB; the device test may push it lower.
3. Whether to enable the same on `bytes` streams (uploads/downloads) later; `gorex.upload.deflate-raw` already covers upload bodies.
