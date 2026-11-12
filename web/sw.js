// GoRex web client's Service Worker (docs/web-client.md, App). build.mjs
// writes this build's version and files into the two lines below; the dev
// server serves a worker that only removes itself (dev.mjs).
//
// What it does:
// - Keeps this build's page and files in a cache of its own version, so
//   the page opens offline, and a new build waits until the user reloads
//   (main.ts shows "A new version of GoRex is ready").
// - The page (a navigation to the scope's root) and the files of /assets,
//   whose names carry their content's hash, come from that cache first.
//   Other files of the page's own origin (fonts, icons, tailnet.json) come
//   from the network, and from the cache when the network fails.
// - It leaves alone everything else: every request to another origin (the
//   GoRex hosts, Tailscale's control server and relays), every request
//   that is not a GET, and /api/. WebSockets never pass through a worker.
// - It streams a tailnet host's download to the browser's own download
//   (files.ts, streamSave): the page posts the file's chunks over a
//   MessagePort, and a hidden frame opens save/<id>, which this worker
//   answers as an attachment.
const VERSION = "development";
const FILES = [];

const CACHE = `gorex-${VERSION}`;
const scope = new URL(self.registration.scope);
const local = (url) => url.origin === scope.origin && url.pathname.startsWith(scope.pathname);

self.addEventListener("install", (e) => {
  e.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      // The page first, checked to be this build's: it names this build's script.
      const page = await fetch(new Request(scope.href, { cache: "no-cache" }));
      const script = FILES.find((f) => /^assets\/app-[0-9a-f]+\.js$/.test(f));
      if (!page.ok || page.redirected || (script && !(await page.clone().text()).includes(script))) {
        throw new Error("the server's page is not this version's");
      }
      await cache.put(scope.href, page);
      await Promise.all(
        FILES.map(async (f) => {
          const r = await fetch(new Request(new URL(f, scope).href, { cache: "no-cache" }));
          if (!r.ok) throw new Error(`${f}: ${r.status}`);
          await cache.put(new URL(f, scope).href, r);
        }),
      );
    })(),
  );
});

self.addEventListener("activate", (e) => {
  e.waitUntil(
    (async () => {
      for (const k of await caches.keys()) if (k.startsWith("gorex-") && k !== CACHE) await caches.delete(k);
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("message", (e) => {
  const d = e.data;
  // The user clicked Reload: this worker, waiting, takes over.
  if (d?.type === "skip-waiting") self.skipWaiting();
  else if (d?.type === "save" && e.ports[0]) prepareSave(String(d.id), String(d.name), e.ports[0]);
  // A page saving a file also sends "ping" every few seconds: each message
  // is an event that keeps this worker awake while the download lasts, as
  // a browser stops an idle worker after about 30 s (and one event held
  // open for minutes too). There is nothing more to do for it.
});

self.addEventListener("fetch", (e) => {
  const req = e.request;
  if (req.method !== "GET") return;
  const url = new URL(req.url);
  if (!local(url)) return;
  const path = url.pathname.slice(scope.pathname.length);
  if (path.startsWith("api/") || path === "sw.js") return;
  if (path.startsWith("save/")) return void serveSave(e, path.slice(5));
  if (req.mode === "navigate") {
    if (path === "" || path === "index.html") e.respondWith(fromCache(scope.href, req));
    return;
  }
  if (path.startsWith("assets/")) e.respondWith(fromCache(url.href, req));
  else e.respondWith(fromNetwork(req));
});

/** The cache's answer for key, else the network's, which an /assets file then joins. */
async function fromCache(key, req) {
  const cache = await caches.open(CACHE);
  const hit = await cache.match(key);
  if (hit) return hit;
  const r = await fetch(req);
  if (r.ok && r.type === "basic" && new URL(key).pathname.startsWith(scope.pathname + "assets/")) {
    await cache.put(key, r.clone());
  }
  return r;
}

/** The network's answer, else the cache's when the network fails. */
async function fromNetwork(req) {
  try {
    return await fetch(req);
  } catch (err) {
    const hit = await (await caches.open(CACHE)).match(req, { ignoreSearch: true });
    if (hit) return hit;
    throw err;
  }
}

/** The downloads the page set up and their frames have not opened yet, by id. */
const saves = new Map();

/**
 * A download the page streams: a stream that asks the page for each chunk
 * (pull), so that no more than one is held here, until save/<id> answers
 * with it.
 */
function prepareSave(id, name, port) {
  let pulled;
  const stream = new ReadableStream(
    {
      start(c) {
        port.onmessage = ({ data }) => {
          if (data.chunk) {
            c.enqueue(new Uint8Array(data.chunk));
            pulled?.();
          } else if (data.end) {
            c.close();
            pulled?.();
          } else if (data.error) {
            c.error(new Error(data.error));
            pulled?.();
          }
        };
      },
      pull() {
        port.postMessage({ pull: true });
        return new Promise((r) => (pulled = r));
      },
      cancel() {
        port.postMessage({ cancel: true });
      },
    },
    { highWaterMark: 1 },
  );
  saves.set(id, { stream, name });
  // A frame that never comes lets it go.
  setTimeout(() => {
    if (saves.delete(id)) port.postMessage({ cancel: true });
  }, 60_000);
  port.postMessage({ ready: true });
}

/** Answers save/<id> once with its stream, as an attachment; a second time, or an unknown id, is 404. */
function serveSave(e, id) {
  const s = saves.get(id);
  saves.delete(id);
  if (!s) return void e.respondWith(new Response("Not found", { status: 404 }));
  const ascii = s.name.replace(/[^\x20-\x7e]|["\\]/g, "_");
  e.respondWith(
    new Response(s.stream, {
      headers: {
        "Content-Type": "application/octet-stream",
        "Content-Disposition": `attachment; filename="${ascii}"; filename*=UTF-8''${encodeURIComponent(s.name)}`,
        "Cache-Control": "no-store",
        "X-Content-Type-Options": "nosniff",
        "Content-Security-Policy": "default-src 'none'; sandbox",
      },
    }),
  );
}
