// Loading on demand (docs/web-client.md, Loading on demand): the files of
// the build's asset manifest (build.mjs, Manifest), by name, and the
// page's code chunks. The page loads a file when it needs it, not before:
// the CJK font's chunks as the terminal draws their characters, the themes
// when Settings shows them, the Tailscale engine when the user turns it on,
// the Settings dialog's code when it first opens.
//
// - configure names the manifest and the entries the page needs as it
//   starts (main.ts, from the build's defines), which load before the
//   manifest is read; init reads the manifest.
// - load(name) fetches a file once: callers that ask while it loads share
//   the one request; a network error or a 5xx answer is tried again, after
//   0.5, 1 and 2 s; each caller may abort its wait (the request stops when
//   every caller has); progress hears the bytes (progress.ts's readBody).
//   A file loaded stays in memory until release(name).
// - response(name) is a boot file's response, for WebAssembly's streaming
//   compile. module(name, importer) is a code chunk (a dynamic import()),
//   script(name) a classic script: each once, tried again as a file is.
// - The files' names carry their content's hash, so a file is the same as
//   long as its name is. The lazy ones (assets/lazy/) are kept in Cache
//   Storage (cacheName), read from there first, and checked against their
//   hash when the browser can (crypto.subtle, a secure context); init
//   removes the files the manifest no longer lists. The Service Worker
//   (sw.js) keeps the rest (the page, the boot files, the code chunks)
//   and leaves this cache and the lazy files alone. Without Cache Storage
//   (plain http on another host than loopback), the browser's HTTP cache
//   keeps them.
// - stats() says what loaded, how many bytes, and from where; devtools and
//   the tests reach it as globalThis.gorexLoader.
import { failure, t } from "./i18n";
import { countBody, readBody } from "./progress";

export type AssetKind = "wasm" | "json" | "font" | "script" | "data";

/** A file of the manifest. */
export interface Asset {
  /** Its path from the page, under assets/. */
  url: string;
  bytes: number;
  /**
   * The first 16 hex digits of its SHA-256, which its name carries; a code
   * chunk's name carries esbuild's hash instead.
   */
  hash: string;
  kind: AssetKind;
  /** Loaded only on demand, under assets/lazy/: the loader's cache keeps it, not the Service Worker. */
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
const never = new AbortController().signal;

class HttpError extends Error {
  constructor(readonly url: string, readonly status: number) {
    super();
  }
  get message() {
    return t("loadServerAnswered", { url: this.url, status: String(this.status) });
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

/** f's value, f run again after 0.5, 1 and 2 s on an error but an HTTP 4xx, until signal aborts. */
async function retrying<T>(signal: AbortSignal, f: (signal: AbortSignal) => Promise<T>): Promise<{ value: T; tries: number }> {
  for (let tries = 1; ; tries++) {
    try {
      return { value: await f(signal), tries };
    } catch (e) {
      const again = !signal.aborted && tries <= retryDelays.length && (!(e instanceof HttpError) || e.status >= 500);
      if (!again) throw e;
      await sleep(retryDelays[tries - 1], signal);
    }
  }
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
  private manifestURL = "";
  /** The code chunks and scripts, loading or in, and the ones in. */
  private modules = new Map<string, Promise<unknown>>();
  private modulesIn = new Set<string>();

  /**
   * Names the manifest, and the entries the page needs as it starts (the
   * terminal's wasm, Maple's index), which load at once, before the
   * manifest is read.
   */
  configure(url: string, boot: Record<string, Asset> = {}) {
    this.manifestURL = url;
    this.assets = { ...boot, ...this.assets };
  }

  /** Reads the manifest, once; a page built without one has no assets. After a failure, the next call tries again. */
  init(url = this.manifestURL): Promise<void> {
    return (this.ready ??= (async () => {
      if (!url) return;
      const r = await this.fetchRetrying(url, never, () => {});
      this.assets = JSON.parse(new TextDecoder().decode(r.bytes)).assets ?? {};
      void this.prune();
    })().catch((e) => {
      this.ready = undefined;
      throw e;
    }));
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

  /** name's entry: a boot entry at once, else once the manifest is read. */
  private async entry(name: string): Promise<Asset> {
    if (!this.assets[name]) await this.init();
    const a = this.assets[name];
    if (!a) throw new Error(`no asset ${name}`);
    return a;
  }

  /** name's bytes: from memory, else the cache, else the network. */
  load(name: string, opts: LoadOptions = {}): Promise<Uint8Array> {
    const a = this.assets[name];
    if (!a) return this.init().then(() => (this.assets[name] ? this.load(name, opts) : Promise.reject(new Error(`no asset ${name}`))));
    const kept = this.loaded.get(name);
    if (kept) {
      this.note({ name, bytes: kept.length, source: "memory", ms: 0, tries: 0 });
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

  /**
   * name's response, for a caller that reads it as a stream, as
   * WebAssembly.instantiateStreaming, which lets Chrome keep the compiled
   * code with a cached response. A boot file is fetched as the page
   * fetches any (the Service Worker's cache or the browser's answers it),
   * tried again on a network error or a 5xx answer; progress hears a
   * clone's bytes, and read resolves when they are all in. A lazy file
   * comes through load, from the loader's cache, as a response of its
   * kind's type.
   */
  async response(name: string, opts: LoadOptions = {}): Promise<{ response: Response; read: Promise<void> }> {
    const a = await this.entry(name);
    if (a.lazy) {
      const bytes = await this.load(name, opts);
      const type = a.kind === "wasm" ? "application/wasm" : "application/octet-stream";
      return { response: new Response(bytes as Uint8Array<ArrayBuffer>, { headers: { "Content-Type": type } }), read: Promise.resolve() };
    }
    const started = performance.now();
    const { value: response, tries } = await retrying(opts.signal ?? never, async (signal) => {
      const res = await fetch(a.url, { signal });
      if (!res.ok || !res.body) throw new HttpError(a.url, res.status);
      return res;
    });
    const read = countBody(response.clone(), a.bytes, opts.progress ?? (() => {})).then(() => {
      this.note({ name, bytes: a.bytes, source: this.sourceOf(a.url), ms: Math.round(performance.now() - started), tries });
    });
    return { response, read };
  }

  /**
   * A module of the page's code as importer (a dynamic import()) gives it,
   * once: callers that ask while it loads share the import; a failed one
   * is tried again after 0.5, 1 and 2 s, and a later call starts again.
   * name is its manifest entry (js/<module>), whose size the stats show.
   */
  module<T>(name: string, importer: () => Promise<T>): Promise<T> {
    const had = this.modules.get(name) as Promise<T> | undefined;
    if (had) {
      if (this.modulesIn.has(name)) this.note({ name, bytes: this.assets[name]?.bytes ?? 0, source: "memory", ms: 0, tries: 0 });
      return had;
    }
    const started = performance.now();
    const p = retrying(never, () => importer()).then(({ value, tries }) => {
      const a = this.assets[name];
      this.modulesIn.add(name);
      this.note({ name, bytes: a?.bytes ?? 0, source: a ? this.sourceOf(a.url) : "network", ms: Math.round(performance.now() - started), tries });
      return value;
    });
    p.catch(() => this.modules.delete(name));
    this.modules.set(name, p);
    return p;
  }

  /** Whether module (or script) name is in. */
  hasModule(name: string): boolean {
    return this.modulesIn.has(name);
  }

  /** Runs name, a classic script of the manifest, once, as module does. */
  script(name: string): Promise<void> {
    return this.module(name, async () => {
      const a = await this.entry(name);
      await new Promise<void>((done, fail) => {
        const s = document.createElement("script");
        s.src = a.url;
        s.onload = () => done();
        s.onerror = () => (s.remove(), fail(failure(() => t("loadScriptFailed", { file: a.url }))));
        document.head.append(s);
      });
    });
  }

  /** Lets name's bytes go from memory; the cache keeps them. */
  release(name: string) {
    this.loaded.delete(name);
  }

  /** What loaded: each load, and the bytes by source, in memory now, loading, and the modules in. */
  stats() {
    const by = (s: Source) => this.log.filter((l) => l.source === s).reduce((n, l) => n + l.bytes, 0);
    return {
      loads: [...this.log],
      network: by("network"),
      cache: by("cache"),
      memory: [...this.loaded.values()].reduce((n, b) => n + b.length, 0),
      loading: [...this.flights.keys()],
      modules: [...this.modulesIn],
    };
  }

  private async fetchAsset(name: string, a: Asset, signal: AbortSignal, progress: (r: number, t?: number) => void): Promise<Uint8Array> {
    const started = performance.now();
    // Only the lazy files: the Service Worker keeps the rest.
    const store = a.lazy ? await this.cache() : undefined;
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
    return this.keep(name, r.bytes, a.lazy ? "network" : this.sourceOf(a.url), started, r.tries);
  }

  private keep(name: string, bytes: Uint8Array, source: Source, started: number, tries: number): Uint8Array {
    this.loaded.set(name, bytes);
    this.note({ name, bytes: bytes.length, source, ms: Math.round(performance.now() - started), tries });
    return bytes;
  }

  private note(s: LoadStat) {
    this.log.push(s);
    if (this.log.length > 2000) this.log.splice(0, 1000);
  }

  /** Where the browser took url from, by its resource timing: a transfer of no bytes came from a cache. */
  private sourceOf(url: string): Source {
    const e = performance.getEntriesByName(new URL(url, document.baseURI).href, "resource").at(-1) as PerformanceResourceTiming | undefined;
    return e && e.transferSize === 0 && e.decodedBodySize > 0 ? "cache" : "network";
  }

  /** url's bytes over the network, tried again after network errors and 5xx answers. */
  private async fetchRetrying(url: string, signal: AbortSignal, progress: (r: number, t?: number) => void, size = 0) {
    const { value, tries } = await retrying(signal, async (signal) => {
      const res = await fetch(url, { signal });
      if (!res.ok || !res.body) throw new HttpError(url, res.status);
      return { bytes: await readBody(res, size, progress), type: res.headers.get("content-type") ?? "application/octet-stream" };
    });
    return { ...value, tries };
  }

  private cacheOpen?: Promise<Cache | undefined>;
  /** The Cache Storage cache, when the browser has one here. */
  private cache(): Promise<Cache | undefined> {
    return (this.cacheOpen ??= globalThis.caches ? caches.open(cacheName).catch(() => undefined) : Promise.resolve(undefined));
  }

  /** Removes from the cache the files the manifest no longer lists as lazy, and the caches of older numbers. */
  private async prune() {
    try {
      const store = await this.cache();
      if (!store) return;
      for (const k of await caches.keys()) if (k.startsWith("gorex-assets-") && k !== cacheName) await caches.delete(k);
      const want = new Set(Object.values(this.assets).filter((a) => a.lazy).map((a) => new URL(a.url, document.baseURI).href));
      for (const req of await store.keys()) if (!want.has(req.url)) await store.delete(req);
    } catch {
      // A cache that cannot be read is pruned next time.
    }
  }
}

/** The page's loader. */
export const loader = new Loader();
(globalThis as { gorexLoader?: Loader }).gorexLoader = loader;
