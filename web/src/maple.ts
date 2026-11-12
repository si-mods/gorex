// Maple Mono NF CN, the terminal's CJK font (docs/web-client.md, Fonts):
// whatever the chosen font, the terminals' stack is that font, then Maple,
// and the system's CJK font never draws.
//
// The build cuts Maple's four faces into chunks by unicode-range
// (web/fonts/maple.mjs), which the loader (loader.ts) fetches on demand.
// A chunk becomes a FontFace of the family, with its range, added to
// document.fonts once it has loaded, never before: so a character whose
// chunk has not loaded yet has no Maple face at all, and the browser would
// draw it in a system font. The terminal therefore draws a character only
// when ready() says its chunk is in (in its face: bold text takes the bold
// face's chunk), and asks for the chunk otherwise; listeners hear each
// chunk that arrives, and the panes draw again. A page built without Maple
// draws no CJK character at all.
import { loader } from "./loader";

export const mapleFamily = "Maple Mono NF CN";

interface Index {
  family: string;
  version: string;
  faces: { key: string; weight: number; style: string }[];
  ranges: string[];
}

/** The chunks' ranges, flat and sorted: start, end, chunk, start, end, chunk... */
let spans: Int32Array | undefined;
let faces: Index["faces"] = [];
/** Each face's chunks by variant (1 bold, 2 italic) and chunk: true once loaded, false after failing, a promise while loading. */
const state = new Map<number, boolean | Promise<void>>();
const listeners = new Set<() => void>();
let notifying = 0;

/**
 * Whether CJK without Maple draws nothing: in a build with an asset
 * manifest. A dist built before the manifest, as a dev server's that has
 * not built again, draws it as it did, in the system's font.
 */
let strict = false;

/** Whether this build has Maple, once init has run. */
export const hasMaple = () => spans !== undefined;

/** CJK: what the system's CJK fonts would draw. Without Maple these draw nothing. */
export const isCJK = (c: number) =>
  (c >= 0x2e80 && c < 0xa000) || (c >= 0xac00 && c < 0xd7b0) || (c >= 0xf900 && c < 0xfb00) || (c >= 0xfe10 && c < 0xfe70) ||
  (c >= 0xff00 && c < 0xfff0) || (c >= 0x20000 && c < 0x40000);

/** Whether this page draws no CJK: a build without Maple. */
export const noMaple = () => strict && !hasMaple();

/** Whether text has CJK that this page cannot draw. */
export const blockedCJK = (text: string) => noMaple() && [...text].some((ch) => isCJK(ch.codePointAt(0)!));

/** Reads Maple's index; a build without it leaves the page without Maple. */
export async function initMaple() {
  await loader.init().catch((e) => console.warn("asset manifest", e));
  strict = loader.names().length > 0;
  if (!loader.asset("maple/index")) return;
  try {
    const idx = await loader.json<Index>("maple/index");
    const flat: number[][] = [];
    idx.ranges.forEach((r, n) => {
      for (const part of r.split(",")) {
        const [a, b] = part.trim().slice(2).split("-");
        flat.push([parseInt(a, 16), parseInt(b ?? a, 16), n]);
      }
    });
    flat.sort((x, y) => x[0] - y[0]);
    spans = Int32Array.from(flat.flat());
    faces = idx.faces;
  } catch (e) {
    console.warn("maple index", e);
  }
}

/** The chunk of code point cp, -1 when Maple has no glyph for it. */
export function chunkOf(cp: number): number {
  const s = spans;
  if (!s) return -1;
  let lo = 0, hi = s.length / 3 - 1;
  while (lo <= hi) {
    const m = (lo + hi) >> 1;
    if (cp < s[m * 3]) hi = m - 1;
    else if (cp > s[m * 3 + 1]) lo = m + 1;
    else return s[m * 3 + 2];
  }
  return -1;
}

const faceKey = (variant: number) => ["r", "b", "i", "bi"][variant & 3];
const stateKey = (variant: number, chunk: number) => (variant & 3) * 4096 + chunk;

/**
 * Whether code point cp may draw now in variant (1 bold, 2 italic):
 * ASCII always (the chosen font has it), a character Maple has once its
 * chunk is in, CJK without Maple never, anything else always (the system
 * draws what neither font has, as emoji). Asks for the chunk when it is
 * not in.
 */
export function ready(cp: number, variant: number): boolean {
  if (cp < 0x80) return true;
  const n = chunkOf(cp);
  if (n < 0) return hasMaple() || !strict || !isCJK(cp);
  return want(variant, n) === true;
}

/** Whether every character of text may draw now in variant. */
export function textReady(text: string, variant: number): boolean {
  let ok = true;
  for (const ch of text) if (!ready(ch.codePointAt(0)!, variant)) ok = false;
  return ok;
}

/** Hears every chunk that arrives. */
export function onChunk(f: () => void): () => void {
  listeners.add(f);
  return () => listeners.delete(f);
}

/** A chunk's state, asking for it when nobody has. */
function want(variant: number, n: number): boolean | Promise<void> {
  const k = stateKey(variant, n);
  let s = state.get(k);
  if (s === undefined) state.set(k, (s = loadChunk(variant, n)));
  return s;
}

/** Tells the listeners, once a frame, that chunks arrived (or failed, so that nothing waits on them). */
function notify() {
  notifying ||= requestAnimationFrame(() => {
    notifying = 0;
    listeners.forEach((f) => f());
  });
}

/** Loads one chunk into document.fonts. */
async function loadChunk(variant: number, n: number): Promise<void> {
  const key = stateKey(variant, n);
  const f = faces[variant & 3];
  const name = `maple/${faceKey(variant)}/${n}`;
  try {
    const bytes = await loader.load(name);
    const face = new FontFace(mapleFamily, bytes as Uint8Array<ArrayBuffer>, {
      weight: String(f.weight), style: f.style, unicodeRange: rangeOf(n), display: "block",
    });
    await face.load();
    document.fonts.add(face);
    loader.release(name);
    state.set(key, true);
  } catch (e) {
    console.warn(`could not load ${name}`, e);
    state.set(key, false);
  }
  notify();
}

/** Chunk n's unicode-range, from the spans. */
function rangeOf(n: number): string {
  const s = spans!;
  const out: string[] = [];
  for (let i = 0; i < s.length; i += 3) {
    if (s[i + 2] === n) out.push(s[i] === s[i + 1] ? `U+${s[i].toString(16)}` : `U+${s[i].toString(16)}-${s[i + 1].toString(16)}`);
  }
  return out.join(",");
}

/**
 * Loads the chunks text needs in each variant, but ASCII's, and resolves
 * when they are in (or failed): for text the page shows outside the
 * terminal, as the Settings' preview, which shows once it resolves.
 * Without Maple it resolves at once.
 */
export async function ensure(text: string, variants = [0]): Promise<void> {
  const waits = new Set<Promise<void>>();
  for (const v of variants) {
    for (const ch of text) {
      const cp = ch.codePointAt(0)!;
      const n = cp < 0x80 ? -1 : chunkOf(cp);
      const s = n < 0 ? true : want(v, n);
      if (s instanceof Promise) waits.add(s);
    }
  }
  await Promise.all(waits);
}

/** Loads chunk 0 (Latin) of every face: the terminal measures its cells with it when Maple is the chosen font. */
export async function ensureLatin(): Promise<void> {
  if (hasMaple()) await Promise.all([0, 1, 2, 3].map((v) => want(v, 0)));
}
