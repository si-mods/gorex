#!/usr/bin/env node
// Builds GoRex's web client, a standalone page that GoRex does not ship,
// into web/dist: libghostty-vt as WebAssembly, from Ghostty's sources at
// the commit GoRex's terminal binding uses, with the Zig that Ghostty
// asks for (fetched into a cache when ZIG is not set), and the
// TypeScript client, bundled by esbuild. web/dist is what Cloudflare
// serves (wrangler.jsonc) and what the dev server serves (dev.mjs).
// It also builds the tailnet engine (web/tailnet, docs/web-tailscale.md)
// with Go, when Go is installed: a page built without Go works, with no
// built-in Tailscale.
//
// The large files go in dist/assets under names that carry a hash of
// their content (app-<hash>.js, ghostty-vt-<hash>.wasm,
// ghostty-themes-<hash>.json, tailnet-<hash>.*), which _headers keeps a
// year: a reload takes them from the browser's cache. index.html names the
// script, and the script names the rest (assetDefines), among them the
// asset manifest, which lists what the page loads on demand by name
// (src/loader.ts): the terminal's CJK font, Maple Mono NF CN, cut into
// chunks by web/fonts/maple.mjs, in dist/assets/lazy.
//
// Before tsc it checks the catalogs of web/locales and writes
// src/locales.gen.ts from them (i18n.mjs; docs/web-client.md, Languages):
// a catalog error fails the build, and so does a missing translation in CI
// ($CI set, or --ci).
//
// It also writes the page's app files (docs/web-client.md, App): the
// manifest, its icons (web/pwa, drawn by go run ./tools/mkicon -web) and
// sw.js, the Service Worker, with this build's version and files.
//
// Usage: node web/build.mjs [--ghostty-source <path>] [--rebuild-wasm] [--ci]
//
// $GOREX_WEB_DIST builds into another directory than web/dist, as a check
// beside a dev server that serves web/dist. The cache is $GOREX_WEB_CACHE, else $XDG_CACHE_HOME/gorex-web, else
// ~/.cache/gorex-web. See docs/web-client.md.
import { execFile, execFileSync, execSync } from "node:child_process";
import { createHash } from "node:crypto";
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { gzipSync } from "node:zlib";
import { faces as mapleFaces, family as mapleFamily, mapleChunks } from "./fonts/maple.mjs";
import { generateOrFail } from "./i18n.mjs";

// The commit of si-mods/mygo's plugins/terminal/internal/libbuild, as
// ios/scripts/build-ghostty-vt.sh pins it.
const COMMIT = "dc8f59943ce94b75ffadc45cc4f36eca6a7ed2ae";
const ZIG_VERSION = "0.17.0";

const web = dirname(fileURLToPath(import.meta.url));
const root = dirname(web);
export const dist = process.env.GOREX_WEB_DIST ? resolve(process.env.GOREX_WEB_DIST) : join(web, "dist");
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

/**
 * The tailnet engine's build tags: tsconnect's own list, read from the
 * pinned tailscale.com so a bump keeps it current, and five features the
 * web node does not need (docs/web-tailscale.md, Build and size).
 */
const extraTags = ["ts_omit_c2n", "ts_omit_dns", "ts_omit_health", "ts_omit_ipnbus", "ts_omit_logtail"];

/** The largest file Cloudflare Workers serves as a static asset. */
const assetLimit = 25 * 1024 * 1024;

/**
 * Builds the tailnet engine into dir: assets/tailnet-<hash>.wasm.gz (the
 * module, gzipped; only the compressed file is shipped, under Cloudflare's
 * 25 MiB a file, and the page unpacks it with DecompressionStream),
 * assets/tailnet-<hash>.js (Go's wasm_exec.js of the same toolchain) and
 * tailnet.json, which names them for the page and is fetched only when the
 * user turns Tailscale on. Old engine files go. Without Go it writes none
 * of them and returns false; a build that fails throws.
 */
export async function buildTailnet(dir) {
  const run = promisify(execFile);
  const go = process.env.GO || "go";
  let goroot, version;
  try {
    goroot = (await run(go, ["env", "GOROOT"], { cwd: root })).stdout.trim();
    version = (await run(go, ["env", "GOVERSION"], { cwd: root })).stdout.trim();
  } catch {
    console.warn("tailnet engine: skipped, Go is not installed (the page builds without built-in Tailscale; see docs/web-tailscale.md)");
    return false;
  }
  const started = Date.now();
  const tags = (await run(go, ["run", "./web/tailnet/tags/tags.go"], { cwd: root })).stdout.trim();
  const tmp = mkdtempSync(join(tmpdir(), "gorex-tailnet-"));
  try {
    const out = join(tmp, "tailnet.wasm");
    const env = { ...process.env, GOOS: "js", GOARCH: "wasm", CGO_ENABLED: "0", GOEXPERIMENT: "nojsonv2" };
    console.log(`> GOOS=js GOARCH=wasm GOEXPERIMENT=nojsonv2 go build -trimpath -ldflags "-s -w" -tags <tsconnect's,${extraTags.join(",")}> ./web/tailnet`);
    await run(go, ["build", "-trimpath", "-ldflags", "-s -w", "-tags", [tags, ...extraTags].join(","), "-o", out, "./web/tailnet"], {
      cwd: root, env, maxBuffer: 16 << 20,
    });
    const raw = readFileSync(out);
    const gz = gzipSync(raw, { level: 9 });
    if (gz.length > assetLimit) throw new Error(`the gzipped tailnet engine is ${gz.length} bytes, over Cloudflare's ${assetLimit}`);
    const hash = createHash("sha256").update(gz).digest("hex").slice(0, 16);
    const exec = readFileSync(join(goroot, "lib", "wasm", "wasm_exec.js"));
    const execHash = createHash("sha256").update(exec).digest("hex").slice(0, 16);
    mkdirSync(join(dir, "assets"), { recursive: true });
    for (const f of readdirSync(join(dir, "assets"))) if (/^tailnet-[0-9a-f]+\.(wasm\.gz|js)$/.test(f)) rmSync(join(dir, "assets", f));
    const manifest = { engine: `assets/tailnet-${hash}.wasm.gz`, exec: `assets/tailnet-${execHash}.js`, bytes: gz.length, raw: raw.length, go: version };
    writeFileSync(join(dir, manifest.engine), gz);
    writeFileSync(join(dir, manifest.exec), exec);
    writeFileSync(join(dir, "tailnet.json"), JSON.stringify(manifest) + "\n");
    const mb = (n) => (n / 1e6).toFixed(2) + " MB";
    console.log(`tailnet engine: ${manifest.engine}, ${mb(gz.length)} gzipped (${mb(raw.length)} raw), ${((Date.now() - started) / 1000).toFixed(1)} s`);
    return true;
  } catch (e) {
    throw new Error(`tailnet engine: ${e.stderr || e.message}`);
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
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
    // Whether the page registers sw.js: the build's, not the dev server's (dev.mjs).
    __GOREX_SW__: "false",
  },
};

/** A name in dist/assets for content: base-<hash>.ext. */
function hashedName(content, base, ext) {
  return `assets/${base}-${createHash("sha256").update(content).digest("hex").slice(0, 16)}${ext}`;
}

/**
 * The names of the hashed files in dist/assets that the script loads, as
 * esbuild defines: the terminal's wasm (with its size, the progress bar's
 * total), the themes, and the asset manifest (writeManifest), which names
 * what the page loads on demand ("" for a dist built before it).
 */
export function assetDefines(dir) {
  const files = existsSync(join(dir, "assets")) ? readdirSync(join(dir, "assets")) : [];
  const find = (re) => files.find((f) => re.test(f));
  const wasmName = find(/^ghostty-vt-[0-9a-f]+\.wasm$/), themes = find(/^ghostty-themes-[0-9a-f]+\.json$/);
  if (!wasmName || !themes) throw new Error(`${dir} has no hashed ghostty-vt.wasm or themes: run node build.mjs`);
  const manifest = find(/^manifest-[0-9a-f]+\.json$/);
  return {
    __GHOSTTY_WASM__: JSON.stringify({ url: `assets/${wasmName}`, size: statSync(join(dir, "assets", wasmName)).size }),
    __GHOSTTY_THEMES__: JSON.stringify(`assets/${themes}`),
    __ASSET_MANIFEST__: JSON.stringify(manifest ? `assets/${manifest}` : ""),
  };
}

/**
 * The asset manifest: the files of dist/assets the page loads by name
 * through its loader (src/loader.ts; docs/web-client.md, Loading on
 * demand), each with its URL, size, the hash its name carries and its
 * kind. A lazy one, in assets/lazy/, is loaded only when the page needs
 * it: the Service Worker does not keep it, the loader's cache does once it
 * is loaded.
 */
class Manifest {
  assets = {};
  /** Writes content to dist/assets under a hashed name, and lists it as name. */
  add(dir, name, content, base, ext, kind, lazy = false) {
    // The lazy ones in assets/lazy/, which the Service Worker leaves to the loader.
    const file = hashedName(content, base, ext).replace(/^assets\//, lazy ? "assets/lazy/" : "assets/");
    mkdirSync(join(dir, dirname(file)), { recursive: true });
    writeFileSync(join(dir, file), content);
    this.assets[name] = { url: file, bytes: content.length, hash: file.slice(file.lastIndexOf("-") + 1, -ext.length), kind, ...(lazy ? { lazy } : {}) };
    return file;
  }
  /** Writes the manifest itself, by its hash, which the script names (assetDefines). */
  write(dir) {
    const json = Buffer.from(JSON.stringify({ version: 1, assets: this.assets }));
    writeFileSync(join(dir, hashedName(json, "manifest", ".json")), json);
  }
}

/**
 * Maple Mono NF CN, the terminal's CJK font (web/fonts/maple.mjs), as
 * lazy chunks in the manifest: maple/<face>/<n> for each face and chunk,
 * and maple/index, which lists the faces and each chunk's unicode-range.
 * Its licence goes to fonts/. Without the faces, as offline with nothing
 * cached, it warns, and the page draws no CJK text (docs/web-client.md,
 * Fonts).
 */
async function addMaple(dir, manifest) {
  let m;
  try {
    m = await mapleChunks(cache);
  } catch (e) {
    console.warn(`\nWARNING: built WITHOUT Maple Mono NF CN (${e.message}).\nThe page draws no Chinese, Japanese or other CJK text. Build again with network access.\n`);
    return;
  }
  let bytes = 0;
  for (const f of mapleFaces) {
    for (let n = 0; n < m.ranges.length; n++) {
      const chunk = m.chunk(f.key, n);
      bytes += chunk.length;
      manifest.add(dir, `maple/${f.key}/${n}`, chunk, `maple-${f.key}${n}`, ".woff2", "font", true);
    }
  }
  const index = { family: mapleFamily, version: m.version, faces: mapleFaces.map(({ key, weight, style }) => ({ key, weight, style })), ranges: m.ranges };
  manifest.add(dir, "maple/index", Buffer.from(JSON.stringify(index)), "maple-index", ".json", "json");
  writeFileSync(join(dir, "fonts", "MapleMono-OFL.txt"), m.license);
  console.log(`maple: ${mapleFaces.length} faces, ${m.ranges.length} chunks each, ${(bytes / 1e6).toFixed(2)} MB in all`);
}

/** esbuild, from web/node_modules. */
export async function esbuild() {
  return import(pathToFileURL(join(web, "node_modules", "esbuild", "lib", "main.js")).href);
}

/** dir's files, by their paths from dir with forward slashes. */
function filesOf(dir, at = dir) {
  return readdirSync(at, { withFileTypes: true }).flatMap((e) =>
    e.isDirectory() ? filesOf(dir, join(at, e.name)) : [relative(dir, join(at, e.name)).split("\\").join("/")],
  );
}

/**
 * Writes dir/sw.js from web/sw.js with this build's files, which the
 * worker keeps for the page offline: all of dir but the page (which it
 * keeps as the scope's root), _headers, sw.js itself, and built-in
 * Tailscale's engine and tailnet.json, which only a page that turns
 * Tailscale on fetches, and assets/lazy/, which the page loads on demand
 * (Manifest). Its version is a hash of web/sw.js, the page and
 * those files, so that a build that changes any of them is a new worker,
 * and one that changes none is the same.
 */
export function writeWorker(dir) {
  const files = filesOf(dir)
    .filter((f) => !["index.html", "_headers", "sw.js", "tailnet.json"].includes(f) && !f.startsWith("assets/tailnet-") && !f.startsWith("assets/lazy/"))
    .sort();
  let sw = readFileSync(join(web, "sw.js"), "utf8");
  const h = createHash("sha256").update(sw);
  for (const f of ["index.html", ...files]) h.update(f + "\0").update(readFileSync(join(dir, f))).update("\0");
  const version = h.digest("hex").slice(0, 16);
  const lines = ['const VERSION = "development";', "const FILES = [];"];
  if (!lines.every((l) => sw.includes(l))) throw new Error(`web/sw.js has no ${lines.join(" or ")}`);
  sw = sw.replace(lines[0], `const VERSION = ${JSON.stringify(version)};`).replace(lines[1], `const FILES = ${JSON.stringify(files)};`);
  writeFileSync(join(dir, "sw.js"), sw);
  console.log(`sw.js: version ${version}, ${files.length} files`);
}

async function main() {
  const wasmFile = await wasm();
  if (!existsSync(join(web, "node_modules"))) {
    console.log("> npm ci");
    execSync("npm ci --no-audit --no-fund", { cwd: web, stdio: "inherit" });
  }
  await generateOrFail({ ci: args.includes("--ci") || !!process.env.CI });
  run(process.execPath, [join(web, "node_modules", "typescript", "bin", "tsc"), "--noEmit", "-p", web]);

  rmSync(dist, { recursive: true, force: true });
  mkdirSync(join(dist, "assets"), { recursive: true });
  // The wasm and the Ghostty themes the iOS app ships (not copied into the
  // repo twice), by their content's hash, which the script names.
  const manifest = new Manifest();
  manifest.add(dist, "ghostty-vt", readFileSync(wasmFile), "ghostty-vt", ".wasm", "wasm");
  manifest.add(dist, "ghostty-themes", readFileSync(join(root, "ios", "GoRex", "ghostty-themes.json")), "ghostty-themes", ".json", "json");
  mkdirSync(join(dist, "fonts"));
  await addMaple(dist, manifest);
  manifest.write(dist);
  const { build } = await esbuild();
  await build({ ...appOptions, define: { ...appOptions.define, ...assetDefines(dist), __GOREX_SW__: "true" } });
  // The script by its hash too, which index.html names.
  const app = readFileSync(join(dist, "app.js"));
  const appName = hashedName(app, "app", ".js");
  writeFileSync(join(dist, appName), app);
  rmSync(join(dist, "app.js"));
  const page = readFileSync(join(web, "index.html"), "utf8");
  if (!page.includes('src="app.js"')) throw new Error('index.html has no <script src="app.js">');
  writeFileSync(join(dist, "index.html"), page.replace('src="app.js"', `src="${appName}"`));
  // Cloudflare's headers for the files (cache, CSP); see docs/web-client.md.
  copyFileSync(join(web, "_headers"), join(dist, "_headers"));
  // The desktop's terminal font, which GoRex embeds too, with its licence.
  for (const f of readdirSync(join(root, "assets", "fonts"))) copyFileSync(join(root, "assets", "fonts", f), join(dist, "fonts", f));
  // The glyphs of the program tiles: the repo's lucide icons and brand marks, which the desktop embeds too.
  for (const d of ["icons", "brands"]) {
    mkdirSync(join(dist, d));
    for (const f of readdirSync(join(root, "assets", d))) copyFileSync(join(root, "assets", d, f), join(dist, d, f));
  }
  // The app's manifest and icons.
  copyFileSync(join(web, "manifest.webmanifest"), join(dist, "manifest.webmanifest"));
  mkdirSync(join(dist, "pwa"));
  for (const f of readdirSync(join(web, "pwa"))) copyFileSync(join(web, "pwa", f), join(dist, "pwa", f));
  // Built-in Tailscale's engine, which the page loads only when the user turns it on.
  await buildTailnet(dist);
  writeWorker(dist);
  console.log(`built ${dist}`);
}

// Run as a script, not when dev.mjs imports the options.
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
