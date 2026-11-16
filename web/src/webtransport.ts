import { Problem, problem } from "./hosts";
import { t } from "./i18n";
import { joinHostURL } from "./share";
import type { SocketLike, Transport } from "./rex";

const streamSocket = 0x01;
const streamHTTP = 0x03;

const frameText = 0x01;
const frameBinary = 0x02;
const frameClose = 0x03;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

/** Decodes a `sha256-<base64url>` (or raw base64url) certificate digest to 32 raw bytes. */
export function parseCertHash(raw: string): ArrayBuffer {
  const clean = raw.trim().replace(/^sha256-/i, "");
  const pad = clean.length % 4 === 0 ? "" : "=".repeat(4 - (clean.length % 4));
  const b64 = clean.replace(/-/g, "+").replace(/_/g, "/") + pad;
  const bin = atob(b64);
  if (bin.length !== 32) {
    throw new Error(`Invalid SHA-256 certificate hash length: ${bin.length}`);
  }
  const bytes = new Uint8Array(32);
  for (let i = 0; i < 32; i++) bytes[i] = bin.charCodeAt(i);
  return bytes.buffer;
}

class StreamReader {
  private buf = new Uint8Array(0);
  private done = false;

  constructor(private readonly reader: ReadableStreamDefaultReader<Uint8Array>) {}

  async readExact(n: number): Promise<Uint8Array> {
    while (this.buf.length < n) {
      if (this.done) throw new Error("Unexpected end of WebTransport stream");
      const { value, done } = await this.reader.read();
      if (done) {
        this.done = true;
        if (this.buf.length < n) throw new Error("Unexpected end of WebTransport stream");
        break;
      }
      if (value && value.length > 0) {
        const next = new Uint8Array(this.buf.length + value.length);
        next.set(this.buf);
        next.set(value, this.buf.length);
        this.buf = next;
      }
    }
    const out = this.buf.slice(0, n);
    this.buf = this.buf.slice(n);
    return out;
  }

  async readU32(): Promise<number> {
    const b = await this.readExact(4);
    return new DataView(b.buffer, b.byteOffset, 4).getUint32(0, false);
  }
}

function encodeShortString(s: string): Uint8Array {
  const bytes = encoder.encode(s);
  const out = new Uint8Array(2 + bytes.length);
  new DataView(out.buffer).setUint16(0, bytes.length, false);
  out.set(bytes, 2);
  return out;
}

function encodeLenBytes(data: Uint8Array): Uint8Array {
  const out = new Uint8Array(4 + data.length);
  new DataView(out.buffer).setUint32(0, data.length, false);
  out.set(data, 4);
  return out;
}

function encodeFrame(kind: number, data: Uint8Array): Uint8Array {
  const out = new Uint8Array(5 + data.length);
  out[0] = kind;
  new DataView(out.buffer).setUint32(1, data.length, false);
  out.set(data, 5);
  return out;
}

/** Whether this browser opens WebTransport at all (Safari before 26.4 and old Chrome do not). */
export function webTransportAvailable(): boolean {
  return typeof WebTransport === "function";
}

/** This page's permission to reach the local network: Chrome's Local Network Access, and Safari's like it. */
export type LocalNetworkAccess = "granted" | "denied" | "prompt" | "unknown";

/** The permission's state, "unknown" where the browser has no such permission. */
export async function localNetworkAccess(): Promise<LocalNetworkAccess> {
  try {
    // "local-network-access" is Chrome 142+'s and Safari 26.4+'s name for it, newer than TypeScript's PermissionName.
    const state = await navigator.permissions.query({ name: "local-network-access" as PermissionName });
    return state.state;
  } catch {
    return "unknown";
  }
}

/**
 * Why a WebTransport host's control connection did not open. A browser says
 * only "Opening handshake failed." for a permission it did not give, a refused
 * certificate hash and an unreachable host alike, so the card asks the
 * permission which of those it is, and names what the user can act on — never
 * the gateway, which a pinned self-signed certificate would fail to answer.
 */
export async function webTransportProblem(url: string, err?: unknown): Promise<Problem> {
  if (!webTransportAvailable()) {
    return problem(() => t("problemWebTransportUnsupportedTitle"), () => t("problemWebTransportUnsupportedText"));
  }
  const why = err instanceof Error ? err.message : err === undefined ? "" : String(err);
  const access = await localNetworkAccess();
  const details = why ? () => t("problemWebTransportDetails", { why }) : undefined;
  if (access === "denied") {
    return problem(
      () => t("problemWebTransportDeniedTitle"),
      () => t("problemWebTransportDeniedText", { url }),
      undefined,
      details,
    );
  }
  if (access === "prompt") {
    return problem(
      () => t("problemWebTransportPromptTitle"),
      () => t("problemWebTransportPromptText", { url }),
      undefined,
      details,
    );
  }
  return problem(
    () => t("problemWebTransportTitle"),
    () => t("problemWebTransportText", { url }),
    undefined,
    details,
  );
}

/**
 * A Transport over WebTransport (`serverCertificateHashes`) for connecting from
 * an HTTPS page to a GoRex LAN host with a rotating self-signed ECDSA P-256 cert.
 */
export function webTransport(url: string, certHash: string): Transport {
  const hashBuf = parseCertHash(certHash);
  const wtURL = joinHostURL(url, "/api/wt");
  let sessionPromise: Promise<WebTransport> | undefined;

  const getSession = (): Promise<WebTransport> => {
    if (sessionPromise) return sessionPromise;
    if (typeof WebTransport !== "function") {
      return Promise.reject(new Error("WebTransport is not supported in this browser (requires Chrome, Firefox, or Safari 26.4+)"));
    }
    const p = (async () => {
      const wt = new WebTransport(wtURL, {
        serverCertificateHashes: [{ algorithm: "sha-256", value: hashBuf }],
      });
      await wt.ready;
      void wt.closed.catch(() => {}).finally(() => {
        if (sessionPromise === p) sessionPromise = undefined;
      });
      return wt;
    })();
    sessionPromise = p;
    p.catch(() => {
      if (sessionPromise === p) sessionPromise = undefined;
    });
    return p;
  };

  return {
    kind: "direct",
    socket(path: string, protocol: string): SocketLike {
      let binaryType: BinaryType = "arraybuffer";
      let closed = false;
      let writer: WritableStreamDefaultWriter<Uint8Array> | undefined;
      let writeChain = Promise.resolve();

      const sock: SocketLike = {
        get binaryType() {
          return binaryType;
        },
        set binaryType(v: BinaryType) {
          binaryType = v;
        },
        protocol,
        onopen: null,
        onmessage: null,
        onclose: null,
        send(data: string | ArrayBufferLike | ArrayBufferView) {
          if (closed || !writer) return;
          let frame: Uint8Array;
          if (typeof data === "string") {
            frame = encodeFrame(frameText, encoder.encode(data));
          } else if (ArrayBuffer.isView(data)) {
            frame = encodeFrame(frameBinary, new Uint8Array(data.buffer, data.byteOffset, data.byteLength));
          } else {
            frame = encodeFrame(frameBinary, new Uint8Array(data));
          }
          const w = writer;
          writeChain = writeChain.then(() => w.write(frame)).catch(() => {});
        },
        close(code = 1000, reason = "") {
          if (closed) return;
          closed = true;
          const w = writer;
          if (w) {
            const reasonBytes = encoder.encode(reason);
            const payload = new Uint8Array(2 + reasonBytes.length);
            new DataView(payload.buffer).setUint16(0, code, false);
            payload.set(reasonBytes, 2);
            const frame = encodeFrame(frameClose, payload);
            writeChain = writeChain.then(() => w.write(frame)).then(() => w.close()).catch(() => {});
          }
        },
      };

      void (async () => {
        try {
          const wt = await getSession();
          if (closed) return;
          const bidi = await wt.createBidirectionalStream();
          if (closed) {
            void bidi.writable.abort().catch(() => {});
            return;
          }
          writer = bidi.writable.getWriter();
          const reader = new StreamReader(bidi.readable.getReader());
          const pathBytes = encodeShortString(path);
          const protoBytes = encodeShortString(protocol);
          const init = new Uint8Array(1 + pathBytes.length + protoBytes.length);
          init[0] = streamSocket;
          init.set(pathBytes, 1);
          init.set(protoBytes, 1 + pathBytes.length);
          await writer.write(init);

          const ack = (await reader.readExact(1))[0];
          if (ack !== 0x00) {
            let code = 1006;
            let reason = "WebTransport socket rejected";
            if (ack === frameClose) {
              const len = await reader.readU32();
              const payload = await reader.readExact(len);
              if (payload.length >= 2) {
                code = new DataView(payload.buffer, payload.byteOffset, 2).getUint16(0, false);
                reason = decoder.decode(payload.slice(2));
              }
            }
            if (!closed) {
              closed = true;
              sock.onclose?.(new CloseEvent("close", { code, reason }));
            }
            return;
          }

          sock.onopen?.(new Event("open"));

          while (!closed) {
            const fType = (await reader.readExact(1))[0];
            const len = await reader.readU32();
            const payload = len > 0 ? await reader.readExact(len) : new Uint8Array(0);
            if (fType === frameText) {
              sock.onmessage?.(new MessageEvent("message", { data: decoder.decode(payload) }));
            } else if (fType === frameBinary) {
              const copy = new Uint8Array(payload);
              const data = (binaryType as BinaryType) === "blob" ? new Blob([copy]) : copy.buffer;
              sock.onmessage?.(new MessageEvent("message", { data }));
            } else if (fType === frameClose) {
              let code = 1000;
              let reason = "";
              if (payload.length >= 2) {
                code = new DataView(payload.buffer, payload.byteOffset, 2).getUint16(0, false);
                reason = decoder.decode(payload.slice(2));
              }
              if (!closed) {
                closed = true;
                sock.onclose?.(new CloseEvent("close", { code, reason }));
              }
              break;
            }
          }
        } catch (err) {
          if (!closed) {
            closed = true;
            const reason = err instanceof Error ? err.message : String(err);
            sock.onclose?.(new CloseEvent("close", { code: 1006, reason }));
          }
        }
      })();

      return sock;
    },
    async fetch(path: string, init?: RequestInit): Promise<Response> {
      const wt = await getSession();
      const bidi = await wt.createBidirectionalStream();
      const writer = bidi.writable.getWriter();
      const reader = new StreamReader(bidi.readable.getReader());

      const headers: Record<string, string> = {};
      if (init?.headers) {
        new Headers(init.headers).forEach((v, k) => {
          headers[k] = v;
        });
      }
      let bodyBytes = new Uint8Array(0);
      if (init?.body != null) {
        if (typeof init.body === "string") {
          bodyBytes = encoder.encode(init.body);
        } else if (init.body instanceof ArrayBuffer) {
          bodyBytes = new Uint8Array(init.body);
        } else if (ArrayBuffer.isView(init.body)) {
          bodyBytes = new Uint8Array(init.body.buffer, init.body.byteOffset, init.body.byteLength);
        } else if (init.body instanceof Blob) {
          bodyBytes = new Uint8Array(await init.body.arrayBuffer());
        }
      }
      const meta = encoder.encode(JSON.stringify({ method: init?.method ?? "GET", path, headers }));
      const kindByte = new Uint8Array([streamHTTP]);
      await writer.write(kindByte);
      await writer.write(encodeLenBytes(meta));
      await writer.write(encodeLenBytes(bodyBytes));
      await writer.close();

      const respMetaLen = await reader.readU32();
      const respMeta = JSON.parse(decoder.decode(await reader.readExact(respMetaLen))) as {
        status: number;
        headers?: Record<string, string>;
      };
      const respBodyLen = await reader.readU32();
      const respBody = respBodyLen > 0 ? new Uint8Array(await reader.readExact(respBodyLen)) : new Uint8Array(0);
      return new Response(respBody.buffer, { status: respMeta.status, headers: respMeta.headers });
    },
  };
}
