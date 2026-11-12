// Built-in Tailscale's engine (tailnet.ts, docs/web-tailscale.md): the
// code that runs only once the user turns Tailscale on, a chunk of its own
// (docs/web-client.md, Loading on demand). It loads the engine through the
// loader (loader.ts): Go's wasm_exec.js (tailnet/exec), then the gzipped
// module (tailnet/engine), which the loader's cache keeps; and it carries
// HTTPS and WebSockets to a host over the engine's node.
import { failure, t } from "./i18n";
import { loader } from "./loader";
import type { SocketLike, Transport } from "./rex";
import { EngineNode, NoEngine } from "./tailnet";

/** Go's wasm_exec.js, which tailnet/exec defines. */
declare class Go {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}

/**
 * Downloads the engine, reporting the compressed bytes as they come,
 * unpacks it with DecompressionStream and starts Go's runtime, which sets
 * gorexTailnet. A module that arrives unpacked (a server that decoded it)
 * is taken as it is. A build without Go has no engine (NoEngine).
 */
export async function loadEngine(progress: (received: number, total: number) => void, starting: () => void): Promise<void> {
  await loader.init();
  const a = loader.asset("tailnet/engine");
  if (!a || !loader.asset("tailnet/exec")) throw new NoEngine();
  await loader.script("tailnet/exec");
  // Only the gzipped file is shipped (25 MiB is Cloudflare's limit for one
  // file); it is not application/wasm, so it is read whole, then unpacked.
  let packed: Uint8Array = await loader.load("tailnet/engine", { progress: (received, total) => progress(received, total ?? a.bytes) });
  // The cache keeps it; memory needs only the module.
  loader.release("tailnet/engine");
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
  // instantiateStreaming would need application/wasm; this is a buffer.
  const { instance } = await WebAssembly.instantiate(module, go.importObject);
  go.run(instance).then(() => console.warn("tailnet: the engine exited"));
  if (typeof (globalThis as any).gorexTailnet !== "function") throw failure(() => t("tsEngineNotStarted"));
}

/** A WebSocket over the engine, as SocketLike. Its events come after the engine's call returns. */
export class TailnetSocket implements SocketLike {
  binaryType: BinaryType = "blob";
  protocol = "";
  onopen: ((e: Event) => any) | null = null;
  onmessage: ((m: MessageEvent) => any) | null = null;
  onclose: ((e: CloseEvent) => any) | null = null;
  private h?: ReturnType<EngineNode["socket"]>;

  constructor(node: EngineNode | undefined, host: string, path: string, protocol: string) {
    if (!node) {
      queueMicrotask(() => this.onclose?.(new CloseEvent("close", { code: 1006, reason: t("tsIsOff") })));
      return;
    }
    this.h = node.socket(host, path, protocol, location.origin, (kind, a, b) => queueMicrotask(() => this.event(kind, a, b)));
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

/** HTTPS and WebSockets to a host's MagicDNS name through the engine, with this page's Origin. */
export function tailnetTransport(node: () => EngineNode | undefined, host: string): Transport {
  return {
    kind: "tailnet",
    socket: (path, protocol) => new TailnetSocket(node(), host, path, protocol),
    async fetch(path, init = {}) {
      const n = node();
      if (!n) throw new TypeError(t("tsIsOff"));
      const signal = init.signal;
      if (signal?.aborted) throw new DOMException("The request was aborted.", "AbortError");
      const headers: Record<string, string> = {};
      new Headers(init.headers).forEach((v, k) => (headers[k] = v));
      const body = init.body == null ? null : new Uint8Array(await new Response(init.body).arrayBuffer());
      const r = await n.request(host, init.method ?? "GET", path, JSON.stringify(headers), body, location.origin, 30_000);
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
