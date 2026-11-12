// Loading on demand (docs/web-client.md, Loading on demand): the files of
// the build's asset manifest (build.mjs, Manifest), by name. The page
// loads a file when it needs it, not before: the CJK font's chunks as the
// terminal draws their characters, for one.
//
// - init reads the manifest, which build.mjs names (__ASSET_MANIFEST__).
// - load(name) fetches a file once: callers that ask while it loads share
//   the one request; a network error or a 5xx answer is tried again, after
//   0.5, 1 and 2 s; each caller may abort its wait (the request stops when
//   every caller has); progress hears the bytes (progress.ts's readBody).
//   A file loaded stays in memory until release(name).
// - The files' names carry their content's hash, so a file is the same as
//   long as its name is: they are kept in Cache Storage (cacheName), read
//   from there first, and checked against their hash when the browser can
//   (crypto.subtle, a secure context). init removes the files no longer in
//   the manifest. The Service Worker (sw.js) leaves this cache and the lazy
//   files alone: it keeps the page and the files every load needs, this
//   cache the ones loaded on demand. Without Cache Storage (plain http on
//   another host than loopback), the browser's HTTP cache keeps them.
// - stats() says what loaded, how many bytes, and from where; devtools and
//   the tests reach it as globalThis.gorexLoader.
import { readBody } from "./progress";

declare const __ASSET_MANIFEST__: string;

export type AssetKind = "wasm" | "json" | "font" | "script" | "data";

/** A file of the manifest. */
export interface Asset {
  /** Its path from the page, under assets/. */
  url: string;
  bytes: number;
  /** The hash its name carries: the first 16 hex digits of its SHA-256. */
  hash: string;
  kind: AssetKind;
  /** Loaded only on demand: the Service Worker does not keep it. */
  lazy?: boolean;
}

export interface LoadOptions {
  /** Stops this caller's wait: its promise rejects with the signal's reason. */
  signal?: AbortSignal;
  /** Hears the bytes as they arrive; none when the file comes from memory or the cache. */
  progress?: (received: number, total?: number) => void;
}

/** Where a load's bytes came from. */
export type Source = "memory" | "cache" | "network";

export interface LoadStat {
  name: string;
  bytes: number;
  source: Source;
  /** How long the load took, in milliseconds. */
  ms: number;
  /** Tries it took over the network: 1, or more after errors. */
  tries: number;
}

/** The Cache Storage cache of the files loaded on demand; a new number drops the old cache. */
export const cacheName = "gorex-assets-v1";
const retryDelays = [500, 1000, 2000];

class HttpError extends Error {
  constructor(readonly url: string, readonly status: number) {
    super(`${url}: ${status}`);
  }
}

/** Waits ms, or until signal aborts. */
function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((done, fail) => {
    const t = setTimeout(done, ms);
    signal.addEventListener("abort", () => (clearTimeout(t), fail(signal.reason)), { once: true });
  });
}

/** Whether bytes have the hash a file's name carries; true when the browser cannot tell. */
async function matches(bytes: Uint8Array, hash: string): Promise<boolean> {
  if (!globalThis.crypto?.subtle) return true;
  const d = new Uint8Array(await crypto.subtle.digest("SHA-256", bytes as Uint8Array<ArrayBuffer>));
  return [...d.slice(0, 8)].map((b) => b.toString(16).padStart(2, "0")).join("") === hash;
}

/** A load in flight: its callers, and the request they share. */
interface Flight {
  promise: Promise<Uint8Array>;
  abort: AbortController;
  waiting: number;
  progress: Set<(received: number, total?: number) => void>;
}

export class Loader {
  private assets: Record<string, Asset> = {};
  private loaded = new Map<string, Uint8Array>();
  private flights = new Map<string, Flight>();
  private log: LoadStat[] = [];
  private ready?: Promise<void>;

  /** Reads the manifest at url, once; a page built without one has no assets. */
  init(url = typeof __ASSET_MANIFEST__ === "string" ? __ASSET_MANIFEST__ : ""): Promise<void> {
    return (this.ready ??= (async () => {
      if (!url) return;
      const r = await this.fetchRetrying(url, new AbortController().signal, () => {});
      this.assets = JSON.parse(new TextDecoder().decode(r.bytes)).assets ?? {};
      void this.prune();
    })());
  }

  /** The manifest's entry for name. */
  asset(name: string): Asset | undefined {
    return this.assets[name];
  }

  /** The manifest's names, those that start with prefix. */
  names(prefix = ""): string[] {
    return Object.keys(this.assets).filter((n) => n.startsWith(prefix));
  }

  /** Whether name is in memory. */
  has(name: string): boolean {
    return this.loaded.has(name);
  }

  /** name's bytes: from memory, else the cache, else the network. */
  load(name: string, opts: LoadOptions = {}): Promise<Uint8Array> {
    const a = this.assets[name];
    if (!a) return Promise.reject(new Error(`no asset ${name}`));
    const kept = this.loaded.get(name);
    if (kept) {
      this.log.push({ name, bytes: kept.length, source: "memory", ms: 0, tries: 0 });
      return Promise.resolve(kept);
    }
    let f = this.flights.get(name);
    if (!f) {
      const abort = new AbortController();
      const progress = new Set<(received: number, total?: number) => void>();
      const flight: Flight = { abort, waiting: 0, progress, promise: undefined! };
      flight.promise = this.fetchAsset(name, a, abort.signal, (r, t) => progress.forEach((p) => p(r, t))).finally(() => this.flights.delete(name));
      this.flights.set(name, (f = flight));
    }
    const flight = f;
    flight.waiting++;
    if (opts.progress) flight.progress.add(opts.progress);
    const leave = () => {
      if (opts.progress) flight.progress.delete(opts.progress);
      if (--flight.waiting === 0) flight.abort.abort(new DOMException("aborted", "AbortError"));
    };
    const signal = opts.signal;
    if (!signal) return flight.promise.finally(() => opts.progress && flight.progress.delete(opts.progress));
    if (signal.aborted) {
      leave();
      return Promise.reject(signal.reason);
    }
    return new Promise((done, fail) => {
      const onAbort = () => {
        leave();
        fail(signal.reason);
      };
      signal.addEventListener("abort", onAbort, { once: true });
      flight.promise.then(
        (b) => (signal.removeEventListener("abort", onAbort), done(b)),
        (e) => (signal.removeEventListener("abort", onAbort), fail(e)),
      );
    });
  }

  /** name's bytes as JSON. */
  async json<T>(name: string, opts?: LoadOptions): Promise<T> {
    return JSON.parse(new TextDecoder().decode(await this.load(name, opts))) as T;
  }

  /** Lets name's bytes go from memory; the cache keeps them. */
  release(name: string) {
    this.loaded.delete(name);
  }

  /** What loaded: each load, and the bytes by source, and in memory now. */
  stats() {
    const by = (s: Source) => this.log.filter((l) => l.source === s).reduce((n, l) => n + l.bytes, 0);
    return {
      loads: [...this.log],
      network: by("network"),
      cache: by("cache"),
      memory: [...this.loaded.values()].reduce((n, b) => n + b.length, 0),
      loading: [...this.flights.keys()],
    };
  }

  private async fetchAsset(name: string, a: Asset, signal: AbortSignal, progress: (r: number, t?: number) => void): Promise<Uint8Array> {
    const started = performance.now();
    const store = await this.cache();
    const key = new URL(a.url, document.baseURI).href;
    const hit = await store?.match(key).catch(() => undefined);
    if (hit) {
      const bytes = new Uint8Array(await hit.arrayBuffer());
      if (bytes.length === a.bytes && (await matches(bytes, a.hash))) return this.keep(name, bytes, "cache", started, 0);
      await store!.delete(key);
    }
    const r = await this.fetchRetrying(a.url, signal, progress, a.bytes);
    if (!(await matches(r.bytes, a.hash))) throw new Error(`${a.url}: its content does not match its hash`);
    if (store) {
      store.put(key, new Response(r.bytes as Uint8Array<ArrayBuffer>, { headers: { "Content-Type": r.type } })).catch(() => {});
    }
    return this.keep(name, r.bytes, "network", started, r.tries);
  }

  private keep(name: string, bytes: Uint8Array, source: Source, started: number, tries: number): Uint8Array {
    this.loaded.set(name, bytes);
    this.log.push({ name, bytes: bytes.length, source, ms: Math.round(performance.now() - started), tries });
    if (this.log.length > 2000) this.log.splice(0, 1000);
    return bytes;
  }

  /** url's bytes over the network, tried again after network errors and 5xx answers. */
  private async fetchRetrying(url: string, signal: AbortSignal, progress: (r: number, t?: number) => void, size = 0) {
    for (let tries = 1; ; tries++) {
      try {
        const res = await fetch(url, { signal });
        if (!res.ok || !res.body) throw new HttpError(url, res.status);
        const bytes = await readBody(res, size, progress);
        return { bytes, tries, type: res.headers.get("content-type") ?? "application/octet-stream" };
      } catch (e) {
        const again = !signal.aborted && tries <= retryDelays.length && (!(e instanceof HttpError) || e.status >= 500);
        if (!again) throw e;
        await sleep(retryDelays[tries - 1], signal);
      }
    }
  }

  private cacheOpen?: Promise<Cache | undefined>;
  /** The Cache Storage cache, when the browser has one here. */
  private cache(): Promise<Cache | undefined> {
    return (this.cacheOpen ??= globalThis.caches ? caches.open(cacheName).catch(() => undefined) : Promise.resolve(undefined));
  }

  /** Removes from the cache the files the manifest no longer lists, and the caches of older numbers. */
  private async prune() {
    try {
      const store = await this.cache();
      if (!store) return;
      for (const k of await caches.keys()) if (k.startsWith("gorex-assets-") && k !== cacheName) await caches.delete(k);
      const want = new Set(Object.values(this.assets).map((a) => new URL(a.url, document.baseURI).href));
      for (const req of await store.keys()) if (!want.has(req.url)) await store.delete(req);
    } catch {
      // A cache that cannot be read is pruned next time.
    }
  }
}

/** The page's loader. */
export const loader = new Loader();
(globalThis as { gorexLoader?: Loader }).gorexLoader = loader;
