// The Rex API's two connections, as the iOS app speaks them
// (ios/GoRex/Rex/ControlConnection.swift, DataStream.swift): the control
// connection's JSON requests, responses and events, and a data stream
// carrying a terminal's bytes.

/** A failed call, as the response's error names it. */
export class RexError extends Error {
  constructor(public code: string, message: string) {
    super(message);
  }
}

/** An event; a session's event is unwrapped, with its session. */
export interface RexEvent {
  seq: number;
  type: string;
  sessionID?: string;
  payload: any;
}

/** The API revision this client speaks. */
const revision = 10;
const callWait = 30_000;

/**
 * The host the connections go to, as http(s)://host[:port]. The page is
 * hosted on its own, so no URL is the page's: every one is built from it.
 */
let base = "";

/** Points every new connection at a host's gateway. */
export function setHost(url: string) {
  base = url;
}

/** The URL of path on the host's gateway. */
export function apiURL(path: string): string {
  if (!base) throw new RexError("offline", "no host is chosen");
  return new URL(path, base).href;
}

/** The WebSocket URL of path on the host's gateway. */
function wsURL(path: string): string {
  const u = new URL(apiURL(path));
  u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
  return u.href;
}

/** The control connection: one WebSocket at /api/control. */
export class Control {
  onEvent: (e: RexEvent) => void = () => {};
  /** Called once when the connection ends, by either side. */
  onClose: (reason: string) => void = () => {};
  clientID = "";
  reconnectToken?: string;
  features = new Set<string>();

  private nextID = 0;
  private pending = new Map<string, { resolve: (v: { seq: number; payload: any }) => void; reject: (e: Error) => void }>();
  private closed = false;

  private constructor(private ws: WebSocket) {}

  /** Opens the connection and says hello. */
  static open(token?: string): Promise<Control> {
    return new Promise((resolve, reject) => {
      // The constructor throws for a URL the page may not open (mixed content).
      const ws = new WebSocket(wsURL("/api/control"), "rex.control.v1");
      const c = new Control(ws);
      let welcomed = false;
      ws.onopen = () => {
        const hello: any = {
          type: "hello",
          api_revision: revision,
          client: { kind: "app", platform: "web", name: "GoRex Web", version: "0.1", label: navigator.platform || "browser" },
        };
        if (token) hello.reconnect_token = token;
        ws.send(JSON.stringify(hello));
      };
      ws.onmessage = (m) => {
        if (typeof m.data !== "string") return;
        const msg = JSON.parse(m.data);
        if (!welcomed) {
          if (msg.type !== "welcome" || msg.protocol !== "rex.control.v1") {
            ws.close();
            reject(new RexError("protocol", "no welcome"));
            return;
          }
          welcomed = true;
          c.clientID = msg.client?.id ?? "";
          c.reconnectToken = msg.client?.reconnect_token;
          c.features = new Set(msg.features ?? []);
          resolve(c);
          return;
        }
        c.receive(msg);
      };
      ws.onclose = (e) => {
        if (!welcomed) reject(new RexError("closed", `the server closed the connection (${e.code})`));
        c.finish(`closed (${e.code}${e.reason ? ", " + e.reason : ""})`);
      };
    });
  }

  /** Calls a method and returns its result with the response's seq. */
  call(method: string, payload?: any): Promise<{ seq: number; payload: any }> {
    if (this.closed) return Promise.reject(new RexError("offline", "disconnected"));
    const id = String(++this.nextID);
    const req: any = { type: "request", id, method };
    if (payload !== undefined) req.payload = payload;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        if (this.pending.delete(id)) reject(new RexError("timeout", "no response"));
      }, callWait);
      this.pending.set(id, {
        resolve: (v) => (clearTimeout(timer), resolve(v)),
        reject: (e) => (clearTimeout(timer), reject(e)),
      });
      this.ws.send(JSON.stringify(req));
    });
  }

  /** Calls a terminal block's method, such as write or resize. */
  async callBlock(method: string, session: string, block: string, args: any = {}): Promise<any> {
    const r = await this.call("com.superlogical.terminal." + method, { session_id: session, block_id: block, args });
    return r.payload;
  }

  close() {
    this.ws.close(1000);
    this.finish("closed");
  }

  private receive(m: any) {
    if (m.type === "response") {
      const p = this.pending.get(m.id);
      if (!p) return;
      this.pending.delete(m.id);
      if (m.error) p.reject(new RexError(m.error.code ?? "", m.error.message ?? ""));
      else p.resolve({ seq: m.seq ?? 0, payload: m.payload ?? {} });
    } else if (m.type === "event" && m.payload?.type) {
      let type: string = m.payload.type;
      let payload = m.payload.payload ?? {};
      let sessionID: string | undefined;
      if (type === "session_event" && payload.event) {
        sessionID = payload.session_id;
        type = payload.event.type ?? "";
        payload = payload.event.payload ?? {};
      }
      this.onEvent({ seq: m.seq, type, sessionID, payload });
    }
  }

  private finish(reason: string) {
    if (this.closed) return;
    this.closed = true;
    for (const p of this.pending.values()) p.reject(new RexError("closed", "disconnected"));
    this.pending.clear();
    this.onClose(reason);
  }
}

/** HELLO: 0x01 and the ticket's 16 bytes, from its base64url form. */
function helloFrame(ticket: string): Uint8Array {
  let s = ticket.replace(/-/g, "+").replace(/_/g, "/");
  s += "=".repeat((4 - (s.length % 4)) % 4);
  const t = Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
  if (t.length !== 16) throw new RexError("protocol", "bad ticket");
  const out = new Uint8Array(17);
  out[0] = 0x01;
  out.set(t, 1);
  return out;
}

/** One data connection carrying a terminal's stream. */
export class DataStream {
  /** Stream bytes, as they arrive. */
  onBytes: (b: Uint8Array) => void = () => {};
  /** The connection ended, with its close code. */
  onDrop: (code: number) => void = () => {};
  nonce = 0n;
  private ws?: WebSocket;

  /** Opens the stream with a ticket: HELLO, then the server's WELCOME. */
  open(ticket: string): Promise<void> {
    const hello = helloFrame(ticket);
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(wsURL("/api/data"), "rex.data.terminal.v1");
      ws.binaryType = "arraybuffer";
      this.ws = ws;
      let welcomed = false;
      ws.onopen = () => ws.send(hello);
      ws.onmessage = (m) => {
        if (!(m.data instanceof ArrayBuffer)) return;
        const b = new Uint8Array(m.data);
        if (welcomed) {
          this.onBytes(b);
          return;
        }
        if (b.length === 32 && b[0] === 0x81) {
          // WELCOME: the reconnect token (1..17), keep-alive (20..24)
          // and the stream's nonce (24..32), little-endian.
          this.nonce = new DataView(m.data).getBigUint64(24, true);
          welcomed = true;
          resolve();
        } else if (b.length === 2 && b[0] === 0x82) {
          reject(new RexError("rejected", `data connection refused (0x${b[1].toString(16).padStart(2, "0")})`));
        } else {
          reject(new RexError("protocol", "bad data handshake"));
        }
      };
      ws.onclose = (e) => {
        if (!welcomed) reject(new RexError("closed", `data connection closed (${e.code})`));
        else if (this.ws === ws) this.onDrop(e.code);
      };
    });
  }

  close() {
    const ws = this.ws;
    this.ws = undefined;
    ws?.close(1000);
  }
}

export type StreamItem =
  | { kind: "snapshot"; data: Uint8Array }
  | { kind: "output"; data: Uint8Array }
  | { kind: "message"; message: any };

const marker = new TextEncoder().encode("\x1b_rex1;");
const maxMessage = 1 << 20;

/** The first index of pat in b[from, end), or -1. */
function indexOf(b: Uint8Array, pat: Uint8Array, from: number, end: number): number {
  outer: for (let i = from; i <= end - pat.length; i++) {
    for (let j = 0; j < pat.length; j++) if (b[i + j] !== pat[j]) continue outer;
    return i;
  }
  return -1;
}

/**
 * Splits a terminal stream into the Ghostty snapshot it starts with, the
 * program's output, and Rex's own messages, `ESC _ rex1;<nonce>;<json> ESC \`,
 * in order. Bytes may arrive split anywhere. The bytes wait in a buffer
 * that doubles as it fills, and the parse goes on where it stopped, so
 * that each byte is read a bounded number of times, a large snapshot too.
 */
export class TerminalStream {
  /** The bytes not yet taken are buf[head, len). */
  private buf: Uint8Array = new Uint8Array(0);
  private head = 0;
  private len = 0;
  private snapshotDone = false;
  /** The snapshot's next record, from head: the envelope is 10 bytes. */
  private snapAt = 10;
  /** While a message waits for its end, where to look for it next, from head; -1 for none. */
  private endAt = -1;

  constructor(private nonce: bigint) {}

  private append(data: Uint8Array) {
    const live = this.len - this.head;
    if (this.len + data.length > this.buf.length) {
      if (live + data.length <= this.buf.length) {
        this.buf.copyWithin(0, this.head, this.len);
      } else {
        const grown = new Uint8Array(Math.max(2 * this.buf.length, live + data.length, 4096));
        grown.set(this.buf.subarray(this.head, this.len));
        this.buf = grown;
      }
      this.head = 0;
      this.len = live;
    }
    this.buf.set(data, this.len);
    this.len += data.length;
  }

  feed(data: Uint8Array): StreamItem[] {
    this.append(data);
    const items: StreamItem[] = [];
    if (!this.snapshotDone) {
      const n = this.snapshotLength();
      if (n === undefined) return items;
      items.push({ kind: "snapshot", data: this.buf.slice(this.head, this.head + n) });
      this.head += n;
      this.snapshotDone = true;
    }
    const b = this.buf, end = this.len;
    while (this.head < end) {
      let start: number, from: number;
      if (this.endAt >= 0) {
        // A message's start waits at head: look on for its end.
        start = this.head;
        from = this.head + this.endAt;
        this.endAt = -1;
      } else {
        start = indexOf(b, marker, this.head, end);
        if (start < 0) {
          // Keep a tail that could begin the marker.
          const keep = partialMarker(b, this.head, end);
          if (end - this.head > keep) items.push({ kind: "output", data: b.slice(this.head, end - keep) });
          this.head = end - keep;
          break;
        }
        if (start > this.head) items.push({ kind: "output", data: b.slice(this.head, start) });
        from = start + marker.length;
      }
      const body = start + marker.length;
      let stop = -1;
      for (let i = from; i + 1 < end; i++) {
        if (b[i] === 0x1b && b[i + 1] === 0x5c) {
          stop = i;
          break;
        }
      }
      if (stop < 0) {
        if (end - start > maxMessage) {
          items.push({ kind: "output", data: b.slice(start, end) });
          this.head = end;
        } else {
          this.head = start;
          // The last byte may be the end's ESC: look again from it.
          this.endAt = Math.max(body, end - 1) - start;
        }
        break;
      }
      const m = this.message(b.subarray(body, stop));
      if (m) items.push({ kind: "message", message: m });
      this.head = stop + 2;
    }
    if (this.head === this.len) this.head = this.len = 0;
    return items;
  }

  /** The JSON of a message with the stream's nonce; undefined for one a program printed. */
  private message(body: Uint8Array): any {
    const text = new TextDecoder().decode(body);
    const semi = text.indexOf(";");
    if (semi < 0 || text.slice(0, semi) !== this.nonce.toString()) return undefined;
    try {
      return JSON.parse(text.slice(semi + 1));
    } catch {
      return undefined;
    }
  }

  /** snapshotLength of the bytes waiting, going on from the record it stopped at. */
  private snapshotLength(): number | undefined {
    const n = this.len - this.head;
    if (n < 10) return undefined;
    const v = new DataView(this.buf.buffer, this.buf.byteOffset + this.head, n);
    let at = this.snapAt;
    while (n - at >= 10) {
      const tag = v.getUint16(at, true);
      const next = at + 10 + v.getUint32(at + 2, true);
      if (next > n) break;
      if (tag === 6) return next; // FINISH
      at = next;
    }
    this.snapAt = at;
    return undefined;
  }
}

/** The length of the longest tail of b[from, end) that begins the marker. */
function partialMarker(b: Uint8Array, from: number, end: number): number {
  for (let n = Math.min(marker.length - 1, end - from); n > 0; n--) {
    let ok = true;
    for (let i = 0; i < n; i++) if (b[end - n + i] !== marker[i]) ok = false;
    if (ok) return n;
  }
  return 0;
}

/**
 * The length of the snapshot at the start of b, through its FINISH record,
 * or undefined until all of it has arrived. A snapshot is a 10-byte
 * envelope ("GHOSTSNP", version) and records of tag (u16), payload length
 * (u32), CRC (u32) and payload.
 */
export function snapshotLength(b: Uint8Array): number | undefined {
  if (b.length < 10) return undefined;
  const v = new DataView(b.buffer, b.byteOffset, b.byteLength);
  let at = 10;
  while (b.length - at >= 10) {
    const tag = v.getUint16(at, true);
    const len = v.getUint32(at + 2, true);
    const next = at + 10 + len;
    if (next > b.length) return undefined;
    if (tag === 6) return next; // FINISH
    at = next;
  }
  return undefined;
}
