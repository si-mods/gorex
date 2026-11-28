// Shared Go-WASM engine loader, WebSocket bridge, and HTTP/WS Transport used
// by both the built-in Tailscale engine (tailengine.ts) and the tailcat engine
// (catengine.ts). Keeping the mechanics here ensures both engines stay in sync
// while preserving separate on-demand chunks for tailnet and tailcat.
import { failure, t } from "./i18n";
import { loader } from "./loader";
import type { SocketLike, Transport } from "./rex";

/** Go's wasm_exec.js runtime class. */
declare class Go {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

export interface WasmBodyReader {
  read(): Promise<Uint8Array | null>;
  cancel(): void;
}

export interface WasmResponse {
  status: number;
  statusText: string;
  headers: string;
  body: WasmBodyReader;
}

export interface WasmSocketHandle {
  send(data: string | Uint8Array): void;
  close(code: number, reason: string): void;
}

export interface WasmEngineSpec {
  engineAsset: string;
  execAsset: string;
  globalFactory: string;
  logName: string;
  missingError: () => Error;
}

/**
 * Downloads a Go-WASM engine via loader, decompresses gzip if needed, starts
 * Go's runtime, and verifies that globalFactory was registered on globalThis.
 */
export async function loadWasmEngine(
  spec: WasmEngineSpec,
  progress: (received: number, total: number) => void,
  starting: () => void,
): Promise<void> {
  await loader.init();
  const a = loader.asset(spec.engineAsset);
  if (!a || !loader.asset(spec.execAsset)) throw spec.missingError();
  await loader.script(spec.execAsset);
  let packed: Uint8Array = await loader.load(spec.engineAsset, {
    progress: (received, total) => progress(received, total ?? a.bytes),
  });
  loader.release(spec.engineAsset);
  starting();
  let module: ArrayBuffer;
  if (packed[0] === 0x1f && packed[1] === 0x8b) {
    const stream = new Blob([packed as BlobPart]).stream().pipeThrough(new DecompressionStream("gzip"));
    module = await new Response(stream).arrayBuffer();
  } else if (packed[0] === 0 && packed[1] === 0x61 && packed[2] === 0x73 && packed[3] === 0x6d) {
    module = packed.buffer as ArrayBuffer;
  } else {
    throw failure(() => t("tsNotEngine"));
  }
  packed = new Uint8Array(0);
  const go = new Go();
  const { instance } = await WebAssembly.instantiate(module, go.importObject);
  go.run(instance).then(() => console.warn(`${spec.logName}: the engine exited`));
  if (typeof (globalThis as any)[spec.globalFactory] !== "function") throw failure(() => t("tsEngineNotStarted"));
}

/** A WebSocket over a Go-WASM engine, implementing SocketLike. */
export class WasmSocket implements SocketLike {
  binaryType: BinaryType = "blob";
  protocol = "";
  onopen: ((e: Event) => any) | null = null;
  onmessage: ((m: MessageEvent) => any) | null = null;
  onclose: ((e: CloseEvent) => any) | null = null;
  private h?: WasmSocketHandle;

  constructor(
    open: ((onEvent: (kind: string, a: any, b: any) => void) => WasmSocketHandle) | undefined,
    offReason: () => string,
  ) {
    if (!open) {
      queueMicrotask(() => this.onclose?.(new CloseEvent("close", { code: 1006, reason: offReason() })));
      return;
    }
    this.h = open((kind, a, b) => queueMicrotask(() => this.event(kind, a, b)));
  }

  private event(kind: string, a: any, b: any) {
    if (kind === "open") {
      this.protocol = a ?? "";
      this.onopen?.(new Event("open"));
    } else if (kind === "message") {
      let data: any = a;
      if (typeof a !== "string") {
        const u = a as Uint8Array;
        data = this.binaryType === "arraybuffer" ? u.buffer.slice(u.byteOffset, u.byteOffset + u.byteLength) : new Blob([u as BlobPart]);
      }
      this.onmessage?.(new MessageEvent("message", { data }));
    } else if (kind === "close") {
      this.onclose?.(new CloseEvent("close", { code: a, reason: b ?? "", wasClean: a !== 1006 }));
    }
  }

  send(d: string | ArrayBufferLike | ArrayBufferView) {
    if (typeof d === "string") this.h?.send(d);
    else if (ArrayBuffer.isView(d)) this.h?.send(new Uint8Array(d.buffer, d.byteOffset, d.byteLength));
    else this.h?.send(new Uint8Array(d));
  }

  close(code = 1000, reason = "") {
    this.h?.close(code, reason);
  }
}

/** Statuses whose Response has no body. */
const nullBody = new Set([101, 103, 204, 205, 304]);

export interface WasmTransportOptions<N> {
  kind: Transport["kind"];
  node: () => N | undefined;
  offReason: () => string;
  openSocket: (n: N, path: string, protocol: string, onEvent: (kind: string, a: any, b: any) => void) => WasmSocketHandle;
  request: (n: N, method: string, path: string, headersJSON: string, body: Uint8Array | null, timeoutMs: number) => Promise<WasmResponse>;
}

/** HTTP and WebSockets over a Go-WASM engine node. */
export function wasmTransport<N>(opts: WasmTransportOptions<N>): Transport {
  return {
    kind: opts.kind,
    socket: (path, protocol) => {
      const n = opts.node();
      return new WasmSocket(n ? (onEvent) => opts.openSocket(n, path, protocol, onEvent) : undefined, opts.offReason);
    },
    async fetch(path, init = {}) {
      const n = opts.node();
      if (!n) throw new TypeError(opts.offReason());
      const signal = init.signal;
      if (signal?.aborted) throw new DOMException("The request was aborted.", "AbortError");
      const headers: Record<string, string> = {};
      new Headers(init.headers).forEach((v, k) => (headers[k] = v));
      const body = init.body == null ? null : new Uint8Array(await new Response(init.body).arrayBuffer());
      const r = await opts.request(n, init.method ?? "GET", path, JSON.stringify(headers), body, 30_000);
      const reader = r.body;
      signal?.addEventListener("abort", () => reader.cancel(), { once: true });
      const stream = new ReadableStream<Uint8Array>({
        async pull(c) {
          for (;;) {
            const v = await reader.read();
            if (v === null) return c.close();
            if (v.length) return c.enqueue(v);
          }
        },
        cancel: () => reader.cancel(),
      });
      const h = new Headers();
      try {
        for (const [k, v] of JSON.parse(r.headers) ?? []) h.append(k, v);
      } catch {}
      const status = r.status >= 200 && r.status <= 599 ? r.status : 502;
      if (nullBody.has(status)) reader.cancel();
      try {
        return new Response(nullBody.has(status) ? null : stream, { status, statusText: r.statusText, headers: h });
      } catch {
        return new Response(nullBody.has(status) ? null : stream, { status, headers: h });
      }
    },
  };
}
