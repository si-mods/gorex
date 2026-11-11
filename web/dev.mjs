#!/usr/bin/env node
// The web client's dev server: builds web/dist once (build.mjs, with its
// type check), then serves it on http://127.0.0.1:5173, bundling src/
// again on each change and copying index.html again when it changes. A
// page served from loopback over plain http may open ws:// and wss://
// connections, so it reaches both a local network gateway and a tailnet
// one (docs/web-client.md). The host must trust its origin:
// gorex rexapi web allow http://127.0.0.1:5173
//
// Usage: npm run dev [-- --port N] [-- --host ADDR]
import { execFileSync } from "node:child_process";
import { copyFileSync, watch } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { appOptions, dist, esbuild } from "./build.mjs";

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

const { context } = await esbuild();
const ctx = await context({ ...appOptions, minify: false, sourcemap: "linked" });
await ctx.watch();
watch(join(web, "index.html"), () => {
  try {
    copyFileSync(join(web, "index.html"), join(dist, "index.html"));
  } catch {}
});
const served = await ctx.serve({ servedir: dist, host, port });
const origin = `http://${served.hosts?.[0] ?? host}:${served.port}`;
console.log(`\nGoRex web client: ${origin}/`);
console.log(`Trust it on a host with: gorex rexapi web allow ${origin}\n`);
