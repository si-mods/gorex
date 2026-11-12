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
// their content (app-<hash>.js and its code chunks, ghostty-vt-<hash>.wasm,
// manifest-<hash>.json; in assets/lazy, ghostty-themes-<hash>.json,
// tailnet-<hash>.*, maple-*), which _headers keeps a year: a reload takes
// them from the browser's cache. index.html names the script, and the
// script names the asset manifest (assetDefines), which lists every file
// the page loads by name (src/loader.ts; docs/web-client.md, Loading on
// demand): the code chunks esbuild cuts at each import(), the terminal's
// wasm, the themes, the Tailscale engine, and the terminal's CJK font,
// Maple Mono NF CN, cut into chunks by web/fonts/maple.mjs.
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
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { basename, dirname, join, relative, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { gzipSync } from "node:zlib";
import { faces as mapleFaces, family as mapleFamily, mapleChunks } from "./fonts/maple.mjs";
import { generateOrFail } from "./i18n.mjs";

// The commit of si-mods/mygo's plugins/terminal/internal/libbuild, as
// ios/scripts/build-ghostty-vt.sh pins it.
const COMMIT = "108cacb17ed8990840cd14abfce43d8ed8503ca1";
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
 * Builds the tailnet engine: the module, gzipped (only the compressed
 * file is shipped, under Cloudflare's 25 MiB a file, and the page unpacks
 * it with DecompressionStream), and Go's wasm_exec.js of the same
 * toolchain, which addTailnet lists in the manifest. Without Go it
 * returns undefined; a build that fails throws.
 */
export async function buildTailnet() {
  const run = promisify(execFile);
  const go = process.env.GO || "go";
  let goroot, version;
  try {
    goroot = (await run(go, ["env", "GOROOT"], { cwd: root })).stdout.trim();
    version = (await run(go, ["env", "GOVERSION"], { cwd: root })).stdout.trim();
  } catch {
    console.warn("tailnet engine: skipped, Go is not installed (the page builds without built-in Tailscale; see docs/web-tailscale.md)");
    return undefined;
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
    const exec = readFileSync(join(goroot, "lib", "wasm", "wasm_exec.js"));
    const mb = (n) => (n / 1e6).toFixed(2) + " MB";
    console.log(`tailnet engine: ${version}, ${mb(gz.length)} gzipped (${mb(raw.length)} raw), ${((Date.now() - started) / 1000).toFixed(1)} s`);
    return { engine: gz, exec };
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

/**
 * Lists the tailnet engine (buildTailnet) in the manifest, lazy: the page
 * loads it only when the user turns Tailscale on (tailnet/engine, the
 * gzipped module; tailnet/exec, wasm_exec.js). The engine files the
 * manifest listed before go.
 */
export function addTailnet(dir, manifest, tn) {
  for (const n of ["tailnet/engine", "tailnet/exec"]) {
    const had = manifest.assets[n];
    if (had) rmSync(join(dir, had.url), { force: true });
  }
  manifest.add(dir, "tailnet/engine", tn.engine, "tailnet", ".wasm.gz", "data", true);
  manifest.add(dir, "tailnet/exec", tn.exec, "tailnet", ".js", "script", true);
}

/**
 * esbuild's options for the client's script, which dev.mjs rebuilds too.
 * ES modules, split (splitting): each dynamic import() is a chunk of its
 * own, and the code that chunks share with the script or with each other
 * is in chunks too, all in dist/assets with esbuild's hash in their
 * names. The script is assets/app-<hash>.js here, app.js in the dev
 * server. The About section shows the defines.
 */
export const appOptions = {
  entryPoints: { app: join(web, "src", "main.ts") },
  bundle: true,
  format: "esm",
  splitting: true,
  target: "es2022",
  minify: true,
  outdir: dist,
  entryNames: "assets/[name]-[hash]",
  chunkNames: "assets/[name]-[hash]",
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

/** The theme until one is chosen (src/prefs.ts, standardTheme). */
const standardTheme = "Gruvbox Dark";

/** The manifest's entries the page loads as it starts, which the script carries (assetDefines). */
const bootAssets = ["ghostty-vt", "maple/index"];

/**
 * The esbuild defines that name what the script loads: the asset manifest
 * of dir, or the one at url, and its entries the page needs as it starts
 * (bootAssets), which the loader takes before it has read the manifest;
 * and the standard theme's colours, so that a first visit starts with
 * them without the theme list (src/themes.ts).
 */
export function assetDefines(dir, url) {
  if (!url) {
    const files = existsSync(join(dir, "assets")) ? readdirSync(join(dir, "assets")) : [];
    const name = files.find((f) => /^manifest-[0-9a-f]+\.json$/.test(f));
    if (!name) throw new Error(`${dir} has no asset manifest: run node build.mjs`);
    url = `assets/${name}`;
  }
  return definesOf(JSON.parse(readFileSync(join(dir, url), "utf8")).assets, url);
}

/** assetDefines from the manifest's assets and its url. */
function definesOf(assets, url) {
  const themes = JSON.parse(readFileSync(join(root, "ios", "GoRex", "ghostty-themes.json"), "utf8"));
  return {
    __ASSET_MANIFEST__: JSON.stringify(url),
    __BOOT_ASSETS__: JSON.stringify(Object.fromEntries(bootAssets.filter((n) => assets[n]).map((n) => [n, assets[n]]))),
    __STANDARD_THEME__: JSON.stringify(themes.find((t) => t.name === standardTheme)),
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
export class Manifest {
  assets = {};
  /** The manifest at url in dir, to change in place (the dev server's engine; dev.mjs). */
  static read(dir, url) {
    const m = new Manifest();
    m.assets = JSON.parse(readFileSync(join(dir, url), "utf8")).assets;
    return m;
  }
  /** Writes it again at url, under the name it had: only the dev server, whose script names it. */
  rewrite(dir, url) {
    writeFileSync(join(dir, url), JSON.stringify({ version: 1, assets: this.assets }));
  }
  /**
   * Lists the script's code chunks (esbuild's outputs but the script):
   * js/<module> for a dynamic import()'s, js/chunk-<hash> for shared code.
   * Their names carry esbuild's hash; hash is their content's SHA-256's.
   */
  addChunks(dir, chunks) {
    for (const c of chunks) {
      const url = relative(dir, c.path).split("\\").join("/");
      this.assets[`js/${c.module ?? basename(url, ".js")}`] = {
        url, bytes: c.contents.length, hash: createHash("sha256").update(c.contents).digest("hex").slice(0, 16), kind: "script",
      };
    }
  }
  /** Writes content to dist/assets under a hashed name, and lists it as name. */
  add(dir, name, content, base, ext, kind, lazy = false) {
    // The lazy ones in assets/lazy/, which the Service Worker leaves to the loader.
    const file = hashedName(content, base, ext).replace(/^assets\//, lazy ? "assets/lazy/" : "assets/");
    mkdirSync(join(dir, dirname(file)), { recursive: true });
    writeFileSync(join(dir, file), content);
    this.assets[name] = { url: file, bytes: content.length, hash: file.slice(file.lastIndexOf("-") + 1, -ext.length), kind, ...(lazy ? { lazy } : {}) };
    return file;
  }
  /** Writes the manifest itself, by its hash, which the script names (assetDefines); returns its path. */
  write(dir) {
    const json = Buffer.from(JSON.stringify({ version: 1, assets: this.assets }));
    const name = hashedName(json, "manifest", ".json");
    writeFileSync(join(dir, name), json);
    return name;
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
 * keeps as the scope's root), _headers, sw.js itself, and assets/lazy/,
 * which the page loads on demand and its loader keeps (Manifest): the
 * themes, built-in Tailscale's engine and Maple's chunks. The code chunks,
 * small, are all kept, so that every part of the page opens offline. Its
 * version is a hash of web/sw.js, the page and those files, so that a
 * build that changes any of them is a new worker, and one that changes
 * none is the same.
 */
export function writeWorker(dir) {
  const files = filesOf(dir)
    .filter((f) => !["index.html", "_headers", "sw.js"].includes(f) && !f.startsWith("assets/lazy/"))
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

/**
 * Bundles the client into dist/assets, lists its code chunks in the
 * manifest and writes the manifest; returns the script's path. The script
 * names the manifest, which lists the chunks, so esbuild runs twice: once
 * to learn the chunks, once with the manifest's name. Only src/main.ts
 * reads that name, and only the script has main.ts, so the chunks are the
 * same both times (checked).
 */
async function buildApp(manifest) {
  const { build } = await esbuild();
  const opts = { ...appOptions, absWorkingDir: web, metafile: true, logLevel: "warning" };
  const define = { ...appOptions.define, __GOREX_SW__: "true" };
  const first = await build({ ...opts, write: false, define: { ...define, ...definesOf(manifest.assets, "") } });
  const key = (path) => relative(web, path).split("\\").join("/");
  const entryOf = (r) => Object.keys(r.metafile.outputs).find((k) => r.metafile.outputs[k].entryPoint === "src/main.ts");
  const entry1 = entryOf(first);
  const chunks = first.outputFiles.filter((f) => key(f.path) !== entry1).map((f) => {
    const out = first.metafile.outputs[key(f.path)];
    return { path: f.path, contents: f.contents, module: out.entryPoint ? basename(out.entryPoint, ".ts") : undefined };
  });
  manifest.addChunks(dist, chunks);
  const url = manifest.write(dist);
  const second = await build({ ...opts, define: { ...define, ...assetDefines(dist, url) } });
  for (const c of chunks) {
    if (!existsSync(c.path) || Buffer.compare(Buffer.from(c.contents), readFileSync(c.path)) !== 0) {
      throw new Error(`${c.path} changed with the manifest's name: only src/main.ts may read __ASSET_MANIFEST__`);
    }
  }
  const entry = entryOf(second);
  const out = second.metafile.outputs[entry];
  const kb = (n) => (n / 1e3).toFixed(1) + " KB";
  const boot = out.imports.filter((i) => i.kind === "import-statement").map((i) => i.path);
  const bootBytes = [entry, ...boot].reduce((n, f) => n + second.metafile.outputs[f].bytes, 0);
  console.log(`script: ${relative(dist, resolve(web, entry)).split("\\").join("/")}, ${kb(out.bytes)}; at boot with ${boot.length} chunks, ${kb(bootBytes)}; ${chunks.length - boot.length} chunks on demand`);
  return relative(dist, resolve(web, entry)).split("\\").join("/");
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
  // The themes, 190 KB, load when Settings shows them (src/themes.ts).
  manifest.add(dist, "ghostty-themes", readFileSync(join(root, "ios", "GoRex", "ghostty-themes.json")), "ghostty-themes", ".json", "json", true);
  mkdirSync(join(dist, "fonts"));
  await addMaple(dist, manifest);
  // Built-in Tailscale's engine, which the page loads only when the user turns it on.
  const tn = await buildTailnet();
  if (tn) addTailnet(dist, manifest, tn);
  const appName = await buildApp(manifest);
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
  writeWorker(dist);
  console.log(`built ${dist}`);
}

// Run as a script, not when dev.mjs imports the options.
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) await main();
