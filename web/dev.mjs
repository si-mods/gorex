#!/usr/bin/env node
// The web client's dev server: builds web/dist once (build.mjs, with its
// type check), then serves it on http://127.0.0.1:5173, bundling src/
// again on each change and copying index.html again when it changes. A
// page served from loopback over plain http may open ws:// and wss://
// connections, so it reaches both a local network gateway and a tailnet
// one (docs/web-client.md). The host must trust its origin:
// gorex rexapi web allow http://127.0.0.1:5173
//
// It builds the tailnet engine again when a Go file of web/tailnet changes
// (a few seconds with Go's build cache); reload the page to load it.
//
// The page registers no Service Worker here (__GOREX_SW__ is false), and
// sw.js is one that removes itself and the page's caches: a worker that a
// build served on the same origin installed then goes at the next load,
// rather than serving that build's page in place of the dev server's.
//
// The script keeps its plain name here, app.js, which index.html names as
// it is; the wasm and the themes keep the hashed names the build gave them.
//
// Usage: npm run dev [-- --port N] [-- --host ADDR]
import { execFileSync } from "node:child_process";
import { copyFileSync, watch, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { appOptions, assetDefines, buildTailnet, dist, esbuild } from "./build.mjs";

const web = dirname(fileURLToPath(import.meta.url));
const args = process.argv.slice(2);
const flag = (name, def) => {
  const i = args.indexOf(name);
  return i >= 0 && args[i + 1] ? args[i + 1] : def;
};
const host = flag("--host", "127.0.0.1");
const port = Number(flag("--port", process.env.PORT || "5173"));

// build.mjs takes the rest (--ghostty-source, --rebuild-wasm).
const rest = args.filter((a, i) => !["--host", "--port"].includes(a) && !["--host", "--port"].includes(args[i - 1]));
execFileSync(process.execPath, [join(web, "build.mjs"), ...rest], { stdio: "inherit" });
writeFileSync(
  join(dist, "sw.js"),
  `// The dev server's: it removes itself and the page's caches (dev.mjs).
self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil((async () => {
  for (const k of await caches.keys()) if (k.startsWith("gorex-")) await caches.delete(k);
  await self.registration.unregister();
})()));
`,
);

const { context } = await esbuild();
const ctx = await context({ ...appOptions, define: { ...appOptions.define, ...assetDefines(dist) }, minify: false, sourcemap: "linked" });
await ctx.watch();
copyFileSync(join(web, "index.html"), join(dist, "index.html"));
watch(join(web, "index.html"), () => {
  try {
    copyFileSync(join(web, "index.html"), join(dist, "index.html"));
  } catch {}
});
// The engine, again after its Go changes: one build at a time, and one
// more if a change came during it.
let building = false, again = false, timer;
async function rebuildTailnet() {
  if (building) return void (again = true);
  building = true;
  try {
    await buildTailnet(dist);
  } catch (e) {
    console.error(e.message);
  }
  building = false;
  if (again) {
    again = false;
    rebuildTailnet();
  }
}
watch(join(web, "tailnet"), { recursive: true }, (_, file) => {
  if (!file?.endsWith(".go")) return;
  clearTimeout(timer);
  timer = setTimeout(rebuildTailnet, 300);
});
const served = await ctx.serve({ servedir: dist, host, port });
const origin = `http://${served.hosts?.[0] ?? host}:${served.port}`;
console.log(`\nGoRex web client: ${origin}/`);
console.log(`Trust it on a host with: gorex rexapi web allow ${origin}\n`);
