// The tailcat engine's page half (tailcat.ts, docs/WEB-006-tailcat.md): the
// code that runs only once a tailcat host needs it, a chunk of its own.
// It loads the engine through the loader (loader.ts): Go's wasm_exec.js
// (tailcat/exec), then the gzipped module (tailcat/engine), which the
// loader's cache keeps; and it carries HTTP and WebSockets to a host over
// the engine's tailcat client.
import { failure, t } from "./i18n";
import { loader } from "./loader";
import type { SocketLike, Transport } from "./rex";
import { EngineCat, NoCatEngine } from "./tailcat";

/** Go's wasm_exec.js, which tailcat/exec defines. */
declare class Go {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

/**
 * Downloads the engine, reporting the compressed bytes as they come,
 * unpacks it with DecompressionStream and starts Go's runtime, which sets
 * gorexTailcat. A module that arrives unpacked (a server that decoded it)
 * is taken as it is. A build without Go has no engine (NoCatEngine).
 */
export async function loadEngine(progress: (received: number, total: number) => void, starting: () => void): Promise<void> {
  await loader.init();
  const a = loader.asset("tailcat/engine");
  if (!a || !loader.asset("tailcat/exec")) throw new NoCatEngine();
  await loader.script("tailcat/exec");
  let packed: Uint8Array = await loader.load("tailcat/engine", { progress: (received, total) => progress(received, total ?? a.bytes) });
  loader.release("tailcat/engine");
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
  go.run(instance).then(() => console.warn("tailcat: the engine exited"));
  if (typeof (globalThis as any).gorexTailcat !== "function") throw failure(() => t("tsEngineNotStarted"));
}

/** A WebSocket over the engine, as SocketLike. Its events come after the engine's call returns. */
export class TailcatSocket implements SocketLike {
  binaryType: BinaryType = "blob";
  protocol = "";
  onopen: ((e: Event) => any) | null = null;
  onmessage: ((m: MessageEvent) => any) | null = null;
  onclose: ((e: CloseEvent) => any) | null = null;
  private h?: ReturnType<EngineCat["socket"]>;

  constructor(node: EngineCat | undefined, address: string, path: string, protocol: string) {
    if (!node) {
      queueMicrotask(() => this.onclose?.(new CloseEvent("close", { code: 1006, reason: t("tcIsOff") })));
      return;
    }
    this.h = node.socket(address, path, protocol, (kind, a, b) => queueMicrotask(() => this.event(kind, a, b)));
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

/** HTTP and WebSockets to a host's tailcat address through the engine. */
export function tailcatTransport(node: () => EngineCat | undefined, address: string): Transport {
  return {
    kind: "tailcat",
    socket: (path, protocol) => new TailcatSocket(node(), address, path, protocol),
    async fetch(path, init = {}) {
      const n = node();
      if (!n) throw new TypeError(t("tcIsOff"));
      const signal = init.signal;
      if (signal?.aborted) throw new DOMException("The request was aborted.", "AbortError");
      const headers: Record<string, string> = {};
      new Headers(init.headers).forEach((v, k) => (headers[k] = v));
      const body = init.body == null ? null : new Uint8Array(await new Response(init.body).arrayBuffer());
      const r = await n.request(address, init.method ?? "GET", path, JSON.stringify(headers), body, 30_000);
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
