#!/usr/bin/env node
// Maple Mono NF CN for the web client (docs/web-client.md, Fonts): its four
// faces, from the pinned release, cut into woff2 chunks by unicode-range,
// as Google Fonts cuts its CJK families, so that a page downloads only the
// chunks of the characters it draws.
//
// - The faces come from the release's zip by HTTP ranges (as the iOS app's
//   Maple.swift takes them), each checked against its CRC and against the
//   SHA-256 pinned below, into <cache>/maple-<version>/. A build after the
//   first reads them there, offline.
// - The chunks are cut by harfbuzz's subsetter (harfbuzzjs) and compressed
//   by woff2 (wawoff2), both pinned in package.json, into
//   <cache>/maple-<version>/chunks-<key>/, where key is a hash of this
//   script, the faces and the order file: a build after the first copies
//   them. Nothing of it is committed.
// - Chunk 0 is Latin, Greek and Cyrillic (U+0000 to U+1FFF), the chunks
//   after it the rest of the font by blocks: punctuation and symbols, then
//   CJK, its most frequent characters first (cjk-order.txt), in chunks of
//   chunkCommon characters, then the rest of CJK by code point, then the
//   Nerd Font icons and what remains, in chunks of chunkRest.
//
// Usage: node web/fonts/maple.mjs [<out dir>]   (cuts the chunks and lists them)
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { isMainThread, Worker, workerData } from "node:worker_threads";
import { crc32, inflateRawSync } from "node:zlib";

export const family = "Maple Mono NF CN";
const version = "v7.9";
const release = `https://github.com/subframe7536/maple-font/releases/download/${version}/MapleMono-NF-CN.zip`;
/** The zip's length: GitHub serves ranges, not a range of the last bytes. */
const zipLength = 159_498_447;
/** The faces, by their file in the zip, with the CSS weight and style each is, and the SHA-256 of each. */
export const faces = [
  { key: "r", file: "MapleMono-NF-CN-Regular.ttf", weight: 400, style: "normal", sha256: "bb8e8e8c263896f42555107202f1847f7a42c340a3e532df9c4d585c9794411c" },
  { key: "b", file: "MapleMono-NF-CN-Bold.ttf", weight: 700, style: "normal", sha256: "9e0c22a032c255b2da2c073d6cca6f8cf6fd6f214ae5407eb9f1aa523713729b" },
  { key: "i", file: "MapleMono-NF-CN-Italic.ttf", weight: 400, style: "italic", sha256: "39e6c6e611e65e6d0780f6279561f954f9db51221987d0924c45f6121eaf9054" },
  { key: "bi", file: "MapleMono-NF-CN-BoldItalic.ttf", weight: 700, style: "italic", sha256: "9d6e76fbb5767406efd1f5ae2d32f5439de3b4d4063b128e4186ad5619267120" },
];
const licenseFile = "LICENSE.txt";

const here = dirname(fileURLToPath(import.meta.url));
const web = dirname(here);

/** The bytes of url's range [from, to), following the redirect to the signed URL once. */
async function range(url, from, to) {
  for (let attempt = 0; ; attempt++) {
    const res = await fetch(url, { headers: { Range: `bytes=${from}-${to - 1}` } });
    const body = Buffer.from(await res.arrayBuffer());
    if (res.status === 206 && body.length === to - from) return body;
    if (attempt >= 2) throw new Error(`${url}: ${res.status}, ${body.length} bytes for ${to - from}`);
  }
}

/** The zip's central directory: its entries by name. */
function entries(tail, base) {
  let end = -1;
  for (let i = tail.length - 22; i >= 0; i--) if (tail.readUInt32LE(i) === 0x06054b50) { end = i; break; }
  if (end < 0) throw new Error("maple: no end of central directory");
  let p = tail.readUInt32LE(end + 16) - base;
  const out = new Map();
  for (let n = tail.readUInt16LE(end + 10); n > 0; n--) {
    if (p < 0 || tail.readUInt32LE(p) !== 0x02014b50) throw new Error("maple: bad central directory");
    const nameLen = tail.readUInt16LE(p + 28);
    const name = tail.subarray(p + 46, p + 46 + nameLen).toString("utf8");
    out.set(name, {
      method: tail.readUInt16LE(p + 10), crc: tail.readUInt32LE(p + 16), compressed: tail.readUInt32LE(p + 20),
      size: tail.readUInt32LE(p + 24), offset: tail.readUInt32LE(p + 42),
    });
    p += 46 + nameLen + tail.readUInt16LE(p + 30) + tail.readUInt16LE(p + 32);
  }
  return out;
}

/** An entry's bytes, inflated and checked against its CRC. */
async function extract(e) {
  const head = await range(release, e.offset, e.offset + 30);
  if (head.readUInt32LE(0) !== 0x04034b50) throw new Error("maple: bad local header");
  const start = e.offset + 30 + head.readUInt16LE(26) + head.readUInt16LE(28);
  const raw = await range(release, start, start + e.compressed);
  const data = e.method === 8 ? inflateRawSync(raw) : raw;
  if (data.length !== e.size || crc32(data) !== e.crc) throw new Error("maple: an entry's CRC does not match");
  return data;
}

const sha = (b) => createHash("sha256").update(b).digest("hex");

/**
 * The faces' files and the licence in dir, fetched once: the ones there
 * already, checked, are kept. Throws when one is missing and cannot be
 * fetched, or does not match its pinned hash.
 */
export async function fetchFaces(dir) {
  const want = [...faces.map((f) => f.file), licenseFile];
  const missing = want.filter((f) => !existsSync(join(dir, f)));
  if (missing.length) {
    mkdirSync(dir, { recursive: true });
    console.log(`maple: fetching ${missing.join(", ")} from ${release}`);
    const tail = await range(release, zipLength - 65536, zipLength);
    const dirEntries = entries(tail, zipLength - 65536);
    for (const f of missing) {
      const e = dirEntries.get(f);
      if (!e) throw new Error(`maple: the release has no ${f}; it has ${[...dirEntries.keys()].join(", ")}`);
      const data = await extract(e);
      writeFileSync(join(dir, f + ".part"), data);
      renameSync(join(dir, f + ".part"), join(dir, f));
    }
  }
  for (const f of faces) {
    const got = sha(readFileSync(join(dir, f.file)));
    if (f.sha256 && got !== f.sha256) {
      rmSync(join(dir, f.file));
      throw new Error(`maple: ${f.file} has SHA-256 ${got}, not the pinned ${f.sha256}`);
    }
    if (!f.sha256) console.log(`maple: ${f.file} sha256 ${got}`);
  }
}

/** The characters of a chunk in the most frequent CJK groups, and in every other chunk. */
const chunkCommon = 200;
const chunkRest = 400;

const isCJK = (c) =>
  (c >= 0x2e80 && c < 0xa000) || (c >= 0xf900 && c < 0xfb00) || (c >= 0xfe10 && c < 0xfe70) || (c >= 0xff00 && c < 0xfff0) || (c >= 0x20000 && c < 0x40000);

/** Cuts cps, sorted, into chunks of at most n, the last ones evened out. */
function cut(cps, n) {
  const k = Math.ceil(cps.length / n);
  const out = [];
  for (let i = 0; i < k; i++) out.push(cps.slice(Math.floor((i * cps.length) / k), Math.floor(((i + 1) * cps.length) / k)));
  return out.filter((c) => c.length);
}

/**
 * The chunks of the font's characters (all, the union of the faces'), in
 * order: each a list of code points. See the top of this file.
 */
export function partition(all, order) {
  const left = new Set(all);
  const take = (pred) => {
    const out = [...left].filter(pred).sort((a, b) => a - b);
    for (const c of out) left.delete(c);
    return out;
  };
  const chunks = [take((c) => c < 0x2000)];
  chunks.push(...cut(take((c) => c < 0x2e80), chunkRest));
  // CJK punctuation, kana, Bopomofo, fullwidth forms: with the most frequent characters.
  const common = take((c) => isCJK(c) && (c < 0x3400 || c >= 0xfe10) && c < 0x20000);
  const freq = [];
  for (const line of order) for (const ch of line) {
    const c = ch.codePointAt(0);
    if (left.delete(c)) freq.push(c);
  }
  chunks.push(common);
  for (let i = 0; i < freq.length; i += chunkCommon) chunks.push(freq.slice(i, i + chunkCommon).sort((a, b) => a - b));
  chunks.push(...cut(take(isCJK), chunkRest));
  chunks.push(...cut(take(() => true), chunkRest));
  return chunks.filter((c) => c.length);
}

/** A list of code points as a CSS unicode-range: "U+4E00-4E05, U+4E07". */
export function unicodeRange(cps) {
  const out = [];
  const hex = (c) => c.toString(16).toUpperCase();
  for (let i = 0; i < cps.length; ) {
    let j = i;
    while (j + 1 < cps.length && cps[j + 1] === cps[j] + 1) j++;
    out.push(i === j ? `U+${hex(cps[i])}` : `U+${hex(cps[i])}-${hex(cps[j])}`);
    i = j + 1;
  }
  return out.join(", ");
}

/** harfbuzz's subsetter on one face: subset(cps) is the face with only those characters, as a TrueType font. */
async function subsetter(font) {
  const wasm = readFileSync(join(web, "node_modules", "harfbuzzjs", "dist", "harfbuzz-subset.wasm"));
  const x = (await WebAssembly.instantiate(wasm)).instance.exports;
  const ptr = x.malloc(font.length);
  new Uint8Array(x.memory.buffer).set(font, ptr);
  const blob = x.hb_blob_create(ptr, font.length, 2 /* HB_MEMORY_MODE_WRITABLE */, 0, 0);
  const face = x.hb_face_create(blob, 0);
  x.hb_blob_destroy(blob);
  return (cps) => {
    const input = x.hb_subset_input_create_or_fail();
    const set = x.hb_subset_input_unicode_set(input);
    for (const c of cps) x.hb_set_add(set, c);
    // No hinting: the browsers draw these faces without it, at half the size.
    x.hb_subset_input_set_flags(input, 1 /* HB_SUBSET_FLAGS_NO_HINTING */);
    const sub = x.hb_subset_or_fail(face, input);
    x.hb_subset_input_destroy(input);
    if (!sub) throw new Error("maple: harfbuzz could not subset");
    const out = x.hb_face_reference_blob(sub);
    const at = x.hb_blob_get_data(out, 0);
    const bytes = new Uint8Array(x.memory.buffer).slice(at, at + x.hb_blob_get_length(out));
    x.hb_blob_destroy(out);
    x.hb_face_destroy(sub);
    return bytes;
  };
}

/** The code points a face maps, by harfbuzz. */
async function unicodesOf(font) {
  const hb = await import(pathToFileURL(join(web, "node_modules", "harfbuzzjs", "dist", "index.mjs")).href);
  return [...new hb.Face(new hb.Blob(font)).collectUnicodes()];
}

/** The pinned tools' versions, which change the chunks. */
function toolVersions() {
  const v = (p) => JSON.parse(readFileSync(join(web, "node_modules", p, "package.json"), "utf8")).version;
  return `harfbuzzjs ${v("harfbuzzjs")}, wawoff2 ${v("wawoff2")}`;
}

/**
 * The chunks, cut once into the cache: <dir>/chunks-<key>/ holds them as
 * <face>-<n>.woff2, with index.json, which lists the chunks' ranges. key
 * changes with this script, the order file, the faces and the tools.
 */
async function chunksIn(dir) {
  const order = readFileSync(join(here, "cjk-order.txt"), "utf8").split("\n").filter((l) => l && !l.startsWith("#"));
  const key = createHash("sha256")
    .update(readFileSync(fileURLToPath(import.meta.url)))
    .update(order.join("\n"))
    .update(faces.map((f) => f.sha256).join())
    .update(toolVersions())
    .digest("hex")
    .slice(0, 12);
  const out = join(dir, `chunks-${key}`);
  if (existsSync(join(out, "index.json"))) return out;
  const started = Date.now();
  const all = new Set();
  for (const f of faces) for (const c of await unicodesOf(readFileSync(join(dir, f.file)))) all.add(c);
  const chunks = partition([...all], order);
  const tmp = out + ".part";
  rmSync(tmp, { recursive: true, force: true });
  mkdirSync(tmp, { recursive: true });
  // A worker a face, as the subsetter and brotli take a while (a minute a face).
  await Promise.all(
    faces.map(
      (f) =>
        new Promise((done, fail) => {
          const w = new Worker(new URL(import.meta.url), { workerData: { font: join(dir, f.file), chunks, out: tmp, key: f.key } });
          w.on("error", fail);
          w.on("exit", (code) => (code === 0 ? done() : fail(new Error(`maple: cutting ${f.file} failed`))));
        }),
    ),
  );
  console.log(`maple: cut the ${faces.length} faces into ${chunks.length} chunks each`);
  writeFileSync(join(tmp, "index.json"), JSON.stringify({ ranges: chunks.map(unicodeRange) }));
  // Older cuts go.
  for (const d of readdirSync(dir)) if (/^chunks-[0-9a-f]+$/.test(d)) rmSync(join(dir, d), { recursive: true, force: true });
  renameSync(tmp, out);
  console.log(`maple: ${chunks.length} chunks a face, ${((Date.now() - started) / 1000).toFixed(1)} s`);
  return out;
}

/**
 * Maple's chunks for the page, from the cache, fetched and cut once.
 * Returns the chunks' bytes, the ranges of each chunk (the same for every
 * face) and the licence; throws when the faces can be neither found nor
 * fetched.
 */
export async function mapleChunks(cache) {
  const dir = join(cache, `maple-${version}`);
  await fetchFaces(dir);
  const out = await chunksIn(dir);
  const { ranges } = JSON.parse(readFileSync(join(out, "index.json"), "utf8"));
  return {
    version,
    ranges,
    license: readFileSync(join(dir, licenseFile)),
    chunk: (face, n) => readFileSync(join(out, `${face}-${n}.woff2`)),
  };
}

/** A worker of chunksIn: cuts one face's chunks into out. */
async function cutFace({ font, chunks, out, key }) {
  const { compress } = (await import(pathToFileURL(join(web, "node_modules", "wawoff2", "index.js")).href)).default;
  const subset = await subsetter(readFileSync(font));
  for (const [n, cps] of chunks.entries()) writeFileSync(join(out, `${key}-${n}.woff2`), await compress(subset(cps)));
}

if (!isMainThread) await cutFace(workerData);
// Run as a script: cut the chunks and list their sizes.
else if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  const cache = process.env.GOREX_WEB_CACHE || join(process.env.XDG_CACHE_HOME || join(homedir(), ".cache"), "gorex-web");
  const m = await mapleChunks(cache);
  for (const f of faces) {
    const sizes = m.ranges.map((_, n) => m.chunk(f.key, n).length);
    console.log(`${f.key}: ${sizes.length} chunks, ${(sizes.reduce((a, b) => a + b, 0) / 1e6).toFixed(2)} MB; ${sizes.map((s) => (s / 1e3).toFixed(0)).join(" ")} KB`);
  }
}
