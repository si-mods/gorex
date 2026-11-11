#!/usr/bin/env node
// Builds GoRex's web client into internal/webui/dist, which the gateway
// embeds and serves at /: libghostty-vt as WebAssembly, from Ghostty's
// sources at the commit GoRex's terminal binding uses, with the Zig that
// Ghostty asks for (fetched into a cache when ZIG is not set), and the
// TypeScript client, bundled by esbuild.
//
// Usage: node web/build.mjs [--ghostty-source <path>] [--rebuild-wasm]
//
// The cache is $GOREX_WEB_CACHE, else $XDG_CACHE_HOME/gorex-web, else
// ~/.cache/gorex-web. See docs/web-client.md.
import { execFileSync, execSync } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

// The commit of si-mods/mygo's plugins/terminal/internal/libbuild, as
// ios/scripts/build-ghostty-vt.sh pins it.
const COMMIT = "dc8f59943ce94b75ffadc45cc4f36eca6a7ed2ae";
const ZIG_VERSION = "0.17.0";

const web = dirname(fileURLToPath(import.meta.url));
const root = dirname(web);
const dist = join(root, "internal", "webui", "dist");
const cache = process.env.GOREX_WEB_CACHE || join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "gorex-web");
const args = process.argv.slice(2);
const windows = process.platform === "win32";

function run(cmd, argv, opts = {}) {
  console.log(`> ${cmd} ${argv.join(" ")}`);
  execFileSync(cmd, argv, { stdio: "inherit", ...opts });
}

/** The Zig to build with: $ZIG, else Zig ZIG_VERSION in the cache, fetched once. */
async function zig() {
  if (process.env.ZIG) return process.env.ZIG;
  const dir = join(cache, `zig-${ZIG_VERSION}`);
  const exe = join(dir, windows ? "zig.exe" : "zig");
  if (existsSync(exe)) return exe;
  const arch = { x64: "x86_64", arm64: "aarch64" }[process.arch];
  const os = { win32: "windows", linux: "linux", darwin: "macos" }[process.platform];
  if (!arch || !os) throw new Error(`no Zig download for ${process.platform}/${process.arch}; set ZIG`);
  const ext = windows ? "zip" : "tar.xz";
  const url = `https://ziglang.org/download/${ZIG_VERSION}/zig-${arch}-${os}-${ZIG_VERSION}.${ext}`;
  console.log(`fetching ${url}`);
  const res = await fetch(url);
  if (!res.ok) throw new Error(`${url}: ${res.status}`);
  mkdirSync(dir, { recursive: true });
  const archive = join(cache, `zig-${ZIG_VERSION}.${ext}`);
  writeFileSync(archive, Buffer.from(await res.arrayBuffer()));
  // Windows' own tar (bsdtar) reads zip files; Git Bash's GNU tar does not.
  const tar = windows ? join(process.env.SystemRoot || "C:\\Windows", "System32", "tar.exe") : "tar";
  run(tar, ["-xf", archive, "--strip-components=1", "-C", dir]);
  rmSync(archive);
  return exe;
}

/** A checkout of Ghostty at COMMIT. */
function ghostty() {
  const i = args.indexOf("--ghostty-source");
  const src = i >= 0 ? args[i + 1] : join(cache, "ghostty");
  if (!existsSync(join(src, ".git"))) {
    run("git", ["clone", "--filter=blob:none", "https://github.com/ghostty-org/ghostty", src]);
  }
  try {
    execFileSync("git", ["-C", src, "fetch", "-q", "origin", COMMIT], { stdio: "ignore" });
  } catch {
    // Already there, or offline with the commit at hand.
  }
  run("git", ["-C", src, "checkout", "-q", "--detach", COMMIT]);
  return src;
}

/** ghostty-vt.wasm at COMMIT, built once into the cache. */
async function wasm() {
  const out = join(cache, `ghostty-vt-${COMMIT.slice(0, 12)}.wasm`);
  if (existsSync(out) && !args.includes("--rebuild-wasm")) return out;
  const z = await zig();
  const src = ghostty();
  const prefix = mkdtempSync(join(tmpdir(), "ghostty-vt-wasm-"));
  try {
    run(z, ["build", "-Demit-lib-vt", "-Dtarget=wasm32-freestanding", "-Doptimize=ReleaseSmall", "--prefix", prefix], { cwd: src });
    copyFileSync(join(prefix, "bin", "ghostty-vt.wasm"), out);
  } finally {
    rmSync(prefix, { recursive: true, force: true });
  }
  return out;
}

const wasmFile = await wasm();
if (!existsSync(join(web, "node_modules"))) {
  console.log("> npm ci");
  execSync("npm ci --no-audit --no-fund", { cwd: web, stdio: "inherit" });
}
run(process.execPath, [join(web, "node_modules", "typescript", "bin", "tsc"), "--noEmit", "-p", web]);

// Everything in dist but its README is the build's.
for (const f of readdirSync(dist)) if (f !== "README") rmSync(join(dist, f), { recursive: true, force: true });
const { build } = await import(pathToFileURL(join(web, "node_modules", "esbuild", "lib", "main.js")).href);
await build({
  entryPoints: [join(web, "src", "main.ts")],
  bundle: true,
  format: "esm",
  target: "es2022",
  minify: true,
  outfile: join(dist, "app.js"),
  logLevel: "info",
});
copyFileSync(join(web, "index.html"), join(dist, "index.html"));
copyFileSync(wasmFile, join(dist, "ghostty-vt.wasm"));
console.log(`built ${dist}`);
