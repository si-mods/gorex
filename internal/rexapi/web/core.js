export const VT_PROTOCOL = "gorex.data.terminal.vt.v1";
const encoder = new TextEncoder();
const decoder = new TextDecoder();

// Control events have an outer event envelope; session-scoped events add
// another envelope carrying the session ID and the actual change.
export function unwrapEvent(message) {
  if (message?.type === "event") message = message.payload;
  if (message?.type === "session_event") return message.payload?.event;
  return message;
}

export function hello(ticket) {
  if (!/^[A-Za-z0-9_-]{22}$/.test(ticket))
    throw new Error("Invalid data ticket");
  const raw = atob(ticket.replaceAll("-", "+").replaceAll("_", "/") + "==");
  return Uint8Array.from([1, ...Array.from(raw, (c) => c.charCodeAt(0))]);
}

export function resume(token, received) {
  const bytes = new Uint8Array(25);
  bytes[0] = 2;
  bytes.set(token, 1);
  new DataView(bytes.buffer).setBigUint64(17, BigInt(received), true);
  return bytes;
}

export function welcome(bytes) {
  if (bytes.length === 2 && bytes[0] === 0x82)
    throw new Error(`Stream rejected (${bytes[1]})`);
  if (bytes.length !== 32 || bytes[0] !== 0x81)
    throw new Error("Invalid data welcome");
  return {
    token: bytes.slice(1, 17),
    nonce: new DataView(bytes.buffer, bytes.byteOffset)
      .getBigUint64(24, true)
      .toString(),
  };
}

export function acknowledgement(received) {
  const bytes = new Uint8Array(8);
  new DataView(bytes.buffer).setBigUint64(0, BigInt(received), true);
  return bytes;
}

function indexOf(bytes, needle, from = 0) {
  outer: for (let i = from; i <= bytes.length - needle.length; i++) {
    for (let j = 0; j < needle.length; j++)
      if (bytes[i + j] !== needle[j]) continue outer;
    return i;
  }
  return -1;
}

// Metadata is delimited by a per-stream nonce. Every other byte, including
// UTF-8 split across frames and ordinary APC sequences, goes to the emulator.
export class VTParser {
  constructor(nonce) {
    this.marker = encoder.encode(`\x1b_rex1;${nonce};`);
    this.end = Uint8Array.of(27, 92);
    this.pending = new Uint8Array();
  }
  feed(chunk) {
    const data = new Uint8Array(this.pending.length + chunk.length);
    data.set(this.pending);
    data.set(chunk, this.pending.length);
    this.pending = new Uint8Array();
    const items = [];
    let at = 0;
    while (at < data.length) {
      const start = indexOf(data, this.marker, at);
      if (start < 0) {
        let keep = Math.min(this.marker.length - 1, data.length - at);
        while (
          keep > 0 &&
          !this.marker
            .slice(0, keep)
            .every((b, i) => b === data[data.length - keep + i])
        )
          keep--;
        if (data.length - keep > at)
          items.push({ bytes: data.slice(at, data.length - keep) });
        this.pending = data.slice(data.length - keep);
        break;
      }
      if (start > at) items.push({ bytes: data.slice(at, start) });
      const end = indexOf(data, this.end, start + this.marker.length);
      if (end < 0) {
        if (data.length - start > 65536)
          throw new Error("Oversized stream metadata");
        this.pending = data.slice(start);
        break;
      }
      items.push({
        message: JSON.parse(
          decoder.decode(data.slice(start + this.marker.length, end)),
        ),
      });
      at = end + 2;
    }
    return items;
  }
}

export function blocks(window) {
  return (window?.layers || [])
    .filter((l) => l.state !== "minimized")
    .flatMap((l) => l.blocks || []);
}

export function choose(views, preferred = {}) {
  const space = views.find((s) => s.session_id === preferred.space) || views[0];
  const window =
    space?.windows?.find((w) => w.window_id === preferred.window) ||
    space?.windows?.find((w) => w.window_id === space.active_window_id) ||
    space?.windows?.[0];
  const panes = blocks(window);
  const pane =
    panes.find((b) => b.block_id === preferred.block) ||
    panes.find((b) => b.block_id === window?.focused_block_id) ||
    panes[0];
  return {
    space: space?.session_id,
    window: window?.window_id,
    block: pane?.block_id,
  };
}

export function textChunk(text, limit = 16384) {
  let n = Math.min(limit, text.length);
  if (n < text.length && n > 0 && /[\uD800-\uDBFF]/.test(text[n - 1])) n--;
  return [text.slice(0, n), text.slice(n)];
}
