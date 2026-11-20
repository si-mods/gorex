// The Rex API's two connections, as the iOS app speaks them
// (ios/GoRex/Rex/ControlConnection.swift, DataStream.swift): the control
// connection's JSON requests, responses and events, and a data stream
// carrying a terminal's bytes.

import { Msg, PlainKey, t } from "./i18n";
import { RexProtocol } from "./rex-protocol.gen";
import { joinHostURL, withToken } from "./share";

/**
 * A failed call, as the response's error names it: the host's code and its
 * words (English), or the page's own, said in the page's language as they
 * are read (a function), so that a status line drawn again after a change
 * of language says them in the new one.
 */
export class RexError extends Error {
  /** The project an `exists` error names (gorex.project.create). */
  projectID?: string;
  constructor(public code: string, message: Msg) {
    super(typeof message === "string" ? message : "");
    if (typeof message === "function") Object.defineProperty(this, "message", { get: message });
  }
}

/** The gateway's error codes (internal/rexapi/protocol.go), in words. */
const codeKeys: Record<string, PlainKey> = {
  bad_request: "errorBadRequest",
  not_found: "errorNotFound",
  method_not_found: "errorMethodNotFound",
  session_not_found: "errorSessionNotFound",
  conflict: "errorConflict",
  not_supported: "errorNotSupported",
  too_large: "errorTooLarge",
  internal: "errorInternal",
  client_unavailable: "errorClientUnavailable",
};

/**
 * An error in words for the status line: a host's error by its code, in
 * the page's language, with the host's own words after it; the page's own
 * errors as they say themselves. codes names other words for some codes,
 * as opening a file has (files.ts).
 */
export function describe(e: unknown, codes: Record<string, PlainKey> = {}): string {
  if (e instanceof RexError && !(e.code in pageCodes)) {
    const key = codes[e.code] ?? codeKeys[e.code];
    if (!key) return e.message || e.code;
    const error = t(key);
    return e.message && !codes[e.code] ? t("errorWithDetail", { error, detail: e.message }) : error;
  }
  return e instanceof Error ? e.message : String(e);
}

/** The codes of the page's own errors, whose words are already the page's. */
const pageCodes = { offline: 1, closed: 1, timeout: 1, protocol: 1, rejected: 1 };

/** An event; a session's event is unwrapped, with its session. */
export interface RexEvent {
  seq: number;
  type: string;
  sessionID?: string;
  payload: any;
}

/** The API revision this client speaks, and the oldest it accepts. */
const revision = RexProtocol.apiRevision;
const minRevision = RexProtocol.apiMinRevision;

/**
 * How long a control connection may take to open and be welcomed: an
 * address that drops packets, a host asleep or a proxy that never answers
 * would otherwise hold its attempt, and its place among the opens
 * (hostconn.ts), for good.
 */
export const openWait = 10000;
const callWait = 30_000;

/**
 * The part of a WebSocket that Control and DataStream use. The browser's
 * is one; the tailnet engine's (tailnet.ts) is another.
 */
export interface SocketLike {
  binaryType: BinaryType;
  readonly protocol: string;
  send(data: string | ArrayBufferLike | ArrayBufferView): void;
  close(code?: number, reason?: string): void;
  onopen: ((e: Event) => any) | null;
  onmessage: ((m: MessageEvent) => any) | null;
  onclose: ((e: CloseEvent) => any) | null;
}

/**
 * How the page reaches a host's gateway (docs/WEB-002-tailscale.md, Hosts and
 * transport): directly, with the browser's WebSocket and fetch, or
 * through the tailnet engine. Paths are the gateway's, such as
 * /api/control.
 */
export interface Transport {
  kind: "direct" | "tailnet";
  socket(path: string, protocol: string): SocketLike;
  fetch(path: string, init?: RequestInit): Promise<Response>;
  /** The URL of path the browser can open; only a direct host has one. */
  url?(path: string): string;
}

/** The browser's own connections to a gateway at url: today's, unchanged. */
export function directTransport(url: string, token?: string): Transport {
  return {
    kind: "direct",
    socket(path, protocol) {
      const u = new URL(withToken(joinHostURL(url, path), token));
      u.protocol = u.protocol === "https:" ? "wss:" : "ws:";
      return new WebSocket(u.href, protocol) as SocketLike;
    },
    fetch: (path, init) => fetch(withToken(joinHostURL(url, path), token), init),
    url: (path) => withToken(joinHostURL(url, path), token),
  };
}

/** The control connection: one WebSocket at /api/control. */
export class Control {
  onEvent: (e: RexEvent) => void = () => {};
  /** Called once when the connection ends, by either side. */
  onClose: (reason: Msg) => void = () => {};
  clientID = "";
  reconnectToken?: string;
  apiRevision = revision;
  features = new Set<string>();
  /** The welcome as the host wrote it, for the versions of its features. */
  welcome: any = {};

  private nextID = 0;
  private pending = new Map<string, { resolve: (v: { seq: number; payload: any }) => void; reject: (e: Error) => void }>();
  private closed = false;

  private constructor(
    /** The transport this connection was opened on: its tickets work only there. */
    readonly transport: Transport,
    private ws: SocketLike,
  ) {}

  /**
   * Opens the connection on transport and says hello. It fails after
   * openWait without a welcome, and at once when signal aborts: the
   * socket closes either way.
   */
  static open(transport: Transport, token?: string, signal?: AbortSignal): Promise<Control> {
    return new Promise((resolve, reject) => {
      if (signal?.aborted) return reject(new RexError("closed", () => t("errorOffline")));
      // The constructor throws for a URL the page may not open (mixed content).
      const ws = transport.socket("/api/control", "rex.control.v1");
      const c = new Control(transport, ws);
      let welcomed = false;
      const give = (e: RexError) => {
        if (welcomed) return;
        clearTimeout(timer);
        signal?.removeEventListener("abort", aborted);
        ws.onclose = () => c.finish(() => t("errorOffline"));
        ws.close();
        reject(e);
      };
      const timer = setTimeout(() => give(new RexError("timeout", () => t("errorTimeout"))), openWait);
      const aborted = () => give(new RexError("closed", () => t("errorOffline")));
      signal?.addEventListener("abort", aborted, { once: true });
      ws.onopen = () => {
        const hello: any = {
          type: "hello",
          api_revision: revision,
          api_min_revision: minRevision,
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
            reject(new RexError("protocol", () => t("errorProtocol")));
            return;
          }
          welcomed = true;
          clearTimeout(timer);
          signal?.removeEventListener("abort", aborted);
          c.clientID = msg.client?.id ?? "";
          c.reconnectToken = msg.client?.reconnect_token;
          if (typeof msg.api_revision === "number") c.apiRevision = msg.api_revision;
          c.welcome = msg;
          c.features = new Set((msg.features ?? []).map((f: any) => (typeof f === "string" ? f : (f?.name ?? f?.id ?? f?.feature))).filter((f: unknown) => typeof f === "string"));
          resolve(c);
          return;
        }
        c.receive(msg);
      };
      ws.onclose = (e) => {
        if (!welcomed) {
          clearTimeout(timer);
          signal?.removeEventListener("abort", aborted);
          // The reason a transport gave (`WebTransport` says "Opening handshake failed.") is the browser's own words; keep them.
          reject(new RexError("closed", e.reason || (() => t("errorServerClosed", { code: String(e.code) }))));
        }
        c.finish(() => (e.reason ? t("statusClosedReason", { code: String(e.code), reason: e.reason }) : t("statusClosed", { code: String(e.code) })));
      };
    });
  }

  /** Calls a method and returns its result with the response's seq. */
  call(method: string, payload?: any): Promise<{ seq: number; payload: any }> {
    if (this.closed) return Promise.reject(new RexError("offline", () => t("errorOffline")));
    const id = String(++this.nextID);
    const req: any = { type: "request", id, method };
    if (payload !== undefined) req.payload = payload;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        if (this.pending.delete(id)) reject(new RexError("timeout", () => t("errorTimeout")));
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
    this.finish(() => t("errorDisconnected"));
  }

  private receive(m: any) {
    if (m.type === "response") {
      const p = this.pending.get(m.id);
      if (!p) return;
      this.pending.delete(m.id);
      if (m.error) {
        const e = new RexError(m.error.code ?? "", m.error.message ?? "");
        if (typeof m.error.project_id === "string") e.projectID = m.error.project_id;
        p.reject(e);
      }
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

  private finish(reason: Msg) {
    if (this.closed) return;
    this.closed = true;
    for (const p of this.pending.values()) p.reject(new RexError("closed", () => t("errorDisconnected")));
    this.pending.clear();
    this.onClose(reason);
  }
}

const protocolError = () => t("errorProtocol");
const helloWait = openWait;

interface DataWelcome {
  ws: SocketLike;
  token: Uint8Array;
  keepMillis: number;
  nonce: bigint;
}

/** HELLO: 0x01 and the ticket's 16 bytes, from its base64url form. */
function helloFrame(ticket: string): Uint8Array {
  let s = ticket.replace(/-/g, "+").replace(/_/g, "/");
  s += "=".repeat((4 - (s.length % 4)) % 4);
  let t: Uint8Array;
  try {
    t = Uint8Array.from(atob(s), (c) => c.charCodeAt(0));
  } catch {
    throw new RexError("protocol", protocolError);
  }
  if (t.length !== 16) throw new RexError("protocol", protocolError);
  const out = new Uint8Array(17);
  out[0] = 0x01;
  out.set(t, 1);
  return out;
}

/** RESUME: 0x02, the latest 16-byte reconnect token, and received byte count (LE u64). */
function resumeFrame(token: Uint8Array, received: bigint): Uint8Array {
  if (token.length !== 16) throw new RexError("protocol", protocolError);
  const out = new Uint8Array(25);
  out[0] = 0x02;
  out.set(token, 1);
  new DataView(out.buffer).setBigUint64(17, received, true);
  return out;
}

/** Opens a Rex data WebSocket, sends the initial handshake frame, and awaits WELCOME. */
function openDataSocket(
  transport: Transport,
  subprotocol: string,
  frame: Uint8Array,
  onAttach: (ws: SocketLike) => void,
  onData: (b: Uint8Array) => void,
  onEnded: (ws: SocketLike, code: number, reason: string) => void,
): Promise<DataWelcome> {
  return new Promise((resolve, reject) => {
    const ws = transport.socket("/api/data", subprotocol);
    ws.binaryType = "arraybuffer";
    onAttach(ws);
    let welcomed = false;
    let settled = false;
    const fail = (err: RexError, closeSocket = true) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      if (closeSocket) {
        try {
          ws.close(1000);
        } catch {
          // ignore close errors on failed handshake socket
        }
      }
      reject(err);
    };
    const timer = setTimeout(() => {
      fail(new RexError("timeout", () => t("errorTimeout")));
    }, helloWait);

    ws.onopen = () => ws.send(frame);
    ws.onmessage = (m) => {
      if (!(m.data instanceof ArrayBuffer)) return;
      const b = new Uint8Array(m.data);
      if (welcomed) {
        onData(b);
        return;
      }
      if (b.length === 32 && b[0] === RexProtocol.dataWelcomeByte) {
        const view = new DataView(m.data);
        const token = b.slice(1, 17);
        const keepMillis = view.getUint32(20, true);
        const nonce = view.getBigUint64(24, true);
        welcomed = true;
        settled = true;
        clearTimeout(timer);
        resolve({ ws, token, keepMillis, nonce });
      } else if (b.length === 2 && b[0] === RexProtocol.dataRejectByte) {
        fail(new RexError("rejected", () => t("errorDataRefused", { code: `0x${b[1].toString(16).padStart(2, "0")}` })));
      } else {
        fail(new RexError("protocol", protocolError));
      }
    };
    ws.onclose = (e) => {
      if (!welcomed) fail(new RexError("closed", () => t("errorDataClosed", { code: String(e.code) })), false);
      else onEnded(ws, e.code, e.reason ?? "");
    };
  });
}

/** One data connection carrying a terminal's stream. */
export class DataStream {
  /** Stream bytes, as they arrive. */
  onBytes: (b: Uint8Array) => void = () => {};
  /** The connection ended, with its close code. */
  onDrop: (code: number) => void = () => {};
  nonce = 0n;
  token?: Uint8Array;
  private ws?: SocketLike;

  /** A stream on transport: the one of the Control that gives its ticket. */
  constructor(private transport: Transport) {}

  /** Opens the stream with a ticket: HELLO, then the server's WELCOME. */
  async open(ticket: string): Promise<void> {
    const welcome = await openDataSocket(
      this.transport,
      RexProtocol.terminalProtocol,
      helloFrame(ticket),
      (ws) => { this.ws = ws; },
      (b) => this.onBytes(b),
      (ws, code) => { if (this.ws === ws) this.onDrop(code); },
    );
    this.token = welcome.token;
    this.nonce = welcome.nonce;
  }

  /** Resumes the stream after a drop using the latest reconnect token and received byte count. */
  async resume(received: bigint): Promise<void> {
    if (!this.token) throw new RexError("protocol", protocolError);
    const welcome = await openDataSocket(
      this.transport,
      RexProtocol.terminalProtocol,
      resumeFrame(this.token, received),
      (ws) => { this.ws = ws; },
      (b) => this.onBytes(b),
      (ws, code) => { if (this.ws === ws) this.onDrop(code); },
    );
    this.token = welcome.token;
    this.nonce = welcome.nonce;
  }

  close() {
    const ws = this.ws;
    this.ws = undefined;
    ws?.close(1000);
  }
}

/** One rex.data.bytes.v1 data stream for file uploads, downloads, or tunnels. */
export class BytesStream {
  onBytes: (b: Uint8Array) => void = () => {};
  onClose: (code: number, reason: string) => void = () => {};
  private ws?: SocketLike;

  constructor(private transport: Transport) {}

  async open(ticket: string): Promise<void> {
    await openDataSocket(
      this.transport,
      RexProtocol.bytesProtocol,
      helloFrame(ticket),
      (ws) => { this.ws = ws; },
      (b) => this.onBytes(b),
      (ws, code, reason) => { if (this.ws === ws) this.onClose(code, reason); },
    );
  }

  send(b: Uint8Array) {
    this.ws?.send(b);
  }

  halfClose() {
    this.ws?.send(new Uint8Array(0));
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
