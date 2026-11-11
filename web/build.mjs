#!/usr/bin/env node
// Builds GoRex's web client, a standalone page that GoRex does not ship,
// into web/dist: libghostty-vt as WebAssembly, from Ghostty's sources at
// the commit GoRex's terminal binding uses, with the Zig that Ghostty
// asks for (fetched into a cache when ZIG is not set), and the
// TypeScript client, bundled by esbuild. web/dist is what Cloudflare
// serves (wrangler.jsonc) and what the dev server serves (dev.mjs).
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
export const dist = join(web, "dist");
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

/** The client's commit, short, with "-dirty" for uncommitted changes; "" outside a checkout. */
function commit() {
  try {
    const git = (...a) => execFileSync("git", ["-C", root, ...a], { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] }).trim();
    return git("rev-parse", "--short", "HEAD") + (git("status", "--porcelain", "--untracked-files=no") ? "-dirty" : "");
  } catch {
    return "";
  }
}

/** esbuild's options for the client's bundle, which dev.mjs rebuilds too. The About section shows the defines. */
export const appOptions = {
  entryPoints: [join(web, "src", "main.ts")],
  bundle: true,
  format: "esm",
  target: "es2022",
  minify: true,
  outfile: join(dist, "app.js"),
  logLevel: "info",
  define: {
    __GOREX_COMMIT__: JSON.stringify(commit()),
    __GOREX_BUILT__: JSON.stringify(new Date().toISOString().slice(0, 10)),
    __GHOSTTY_COMMIT__: JSON.stringify(COMMIT),
  },
};

/** esbuild, from web/node_modules. */
export async function esbuild() {
  return import(pathToFileURL(join(web, "node_modules", "esbuild", "lib", "main.js")).href);
}

async function main() {
  const wasmFile = await wasm();
  if (!existsSync(join(web, "node_modules"))) {
    console.log("> npm ci");
    execSync("npm ci --no-audit --no-fund", { cwd: web, stdio: "inherit" });
  }
  run(process.execPath, [join(web, "node_modules", "typescript", "bin", "tsc"), "--noEmit", "-p", web]);

  rmSync(dist, { recursive: true, force: true });
  mkdirSync(dist);
  const { build } = await esbuild();
  await build(appOptions);
  copyFileSync(join(web, "index.html"), join(dist, "index.html"));
  // Cloudflare's headers for the files (cache, CSP); see docs/web-client.md.
  copyFileSync(join(web, "_headers"), join(dist, "_headers"));
  copyFileSync(wasmFile, join(dist, "ghostty-vt.wasm"));
  // The Ghostty themes the iOS app ships, not copied into the repo twice.
  copyFileSync(join(root, "ios", "GoRex", "ghostty-themes.json"), join(dist, "ghostty-themes.json"));
  // The desktop's terminal font, which GoRex embeds too, with its licence.
  mkdirSync(join(dist, "fonts"));
  for (const f of readdirSync(join(root, "assets", "fonts"))) copyFileSync(join(root, "assets", "fonts", f), join(dist, "fonts", f));
  // The glyphs of the program tiles: the repo's lucide icons and brand marks, which the desktop embeds too.
  for (const d of ["icons", "brands"]) {
    mkdirSync(join(dist, d));
    for (const f of readdirSync(join(root, "assets", d))) copyFileSync(join(root, "assets", d, f), join(dist, d, f));
  }
  console.log(`built ${dist}`);
}

// Run as a script, not when dev.mjs imports the options.
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
