// The liquid glass of what floats over the terminals (docs/WEB-005-liquid-glass.md):
// index.html draws the material from its --glass-* tokens; this module reads
// the terminal under each surface to choose its ink and its tint's density,
// keeps the Glass settings on the page, and gives the larger surfaces a rim
// lens (an SVG displacement filter) where the browser can show one. With the
// Glass setting off (until set) it runs nothing.
import { Backdrop, displacementMap, Ink, maxOffset, nextInk, readBackdrop, rimWidth, RGB, saturationKept, Scheme, density } from "./glassmath";
import { prefs } from "./prefs";

/** The surfaces of glass, as index.html lists them. */
const surfaces = ".update, .chunk-load, .pane-upload, .find-bar, .select-menu, .context-menu, .float-dock-btn";
/** The surfaces with a rim lens: the panels; not the menus (they open and close as their own layers) nor the dock's small buttons. */
const lensed = ".update, .chunk-load, .pane-upload, .find-bar";
/** How much more the red bends than the green, and the blue less: the rim's colour split. */
const split = 0.15;
/** The least time between two readings of the terminals while they draw. */
const resampleMs = 1000;
const inkKey = "gorex.glassInk";

interface Lens {
  svg: SVGSVGElement;
  image: SVGFEImageElement;
  maps: SVGFEDisplacementMapElement[];
  key: string;
  settle?: number;
}

interface Surface {
  ink?: Ink;
  lens?: Lens;
  watch: MutationObserver;
  resize?: ResizeObserver;
}

const tracked = new Map<HTMLElement, Surface>();
let lensOK = false;
let lastSample = 0;
let sampleTimer: number | undefined;
let nextId = 0;
let probe: CanvasRenderingContext2D | null | undefined;

const scheme = (): Scheme => (document.documentElement.dataset.scheme === "light" ? "light" : "dark");
const shown = (e: HTMLElement) => e.isConnected && e.getClientRects().length > 0;

/** The ink the light scheme showed last, so that a surface does not flash the other ink before its first reading. */
function rememberedInk(): Ink {
  try {
    return localStorage.getItem(inkKey) === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}

function remember(ink: Ink) {
  try {
    localStorage.setItem(inkKey, ink);
  } catch {
    // Without storage the next page starts from the theme's ink.
  }
}

/** The observers of the page while the glass runs: new surfaces, and the scheme. */
let watchers: MutationObserver[] = [];

/** Whether the glass runs: the Glass setting on, and applyGlass called since. */
export const glassRunning = () => watchers.length > 0;

/**
 * Puts the Glass settings on the page. On: data-glass="on" on <html> (index.html's glass rules), each
 * scheme's tint scale (index.html takes the shown scheme's, and glides to it), and the glass starts: it
 * finds its surfaces as they come and reads under them. Off: the attribute goes, and so do every observer,
 * timer, lens filter and value the glass put on a surface, so the surfaces are as they were without it.
 */
export function applyGlass(on: boolean) {
  const root = document.documentElement;
  root.style.setProperty("--glass-scale-light", String(prefs.glassLight ?? 1));
  root.style.setProperty("--glass-scale-dark", String(prefs.glassDark ?? 1));
  if (on === glassRunning()) return;
  if (!on) {
    for (const w of watchers) w.disconnect();
    watchers = [];
    clearTimeout(sampleTimer);
    sampleTimer = undefined;
    for (const e of [...tracked.keys()]) drop(e, true);
    delete root.dataset.glass;
    return;
  }
  root.dataset.glass = "on";
  const ua = navigator.userAgent;
  // An SVG filter in backdrop-filter shows in Chromium only: Firefox does not, and Safari parses it and drops the blur.
  lensOK =
    typeof CSS !== "undefined" && CSS.supports("backdrop-filter", "url(#a)") && !/firefox/i.test(ua) &&
    !(/AppleWebKit/.test(ua) && !/Chrome|Chromium|Edg\//.test(ua));
  const added = new MutationObserver((records) => {
    let removed = false;
    for (const r of records) {
      for (const n of r.addedNodes) if (n instanceof HTMLElement) find(n);
      if (r.removedNodes.length) removed = true;
    }
    if (removed) for (const e of tracked.keys()) if (!e.isConnected) drop(e);
  });
  added.observe(document.body, { childList: true, subtree: true });
  // The scheme changes the ink and the material: read again.
  const schemes = new MutationObserver(() => {
    for (const e of tracked.keys()) {
      const s = tracked.get(e)!;
      s.ink = undefined;
      e.dataset.glassInk = scheme() === "light" ? rememberedInk() : "light";
    }
    sampleSoon(true);
  });
  schemes.observe(root, { attributes: true, attributeFilter: ["data-scheme"] });
  watchers = [added, schemes];
  find(document.body);
}

/** The terminals drew: read under the shown surfaces again, at most once a second, when the page is idle. Nothing while the glass is off. */
export function glassRedrawn() {
  if (!tracked.size || sampleTimer !== undefined || !glassRunning()) return;
  sampleSoon(false);
}
function find(root: HTMLElement) {
  if (root.matches(surfaces)) track(root);
  for (const e of root.querySelectorAll<HTMLElement>(surfaces)) track(e);
}

function track(e: HTMLElement) {
  if (tracked.has(e)) return;
  // Shown, hidden or moved (the selection's menu): read again, and fit the lens. Its own style changes (the
  // density, the lens) move nothing, so the place it was read at is kept and compared.
  let at = "";
  const watch = new MutationObserver(() => {
    const r = e.getBoundingClientRect();
    const now = shown(e) ? [r.left, r.top, r.width, r.height].map(Math.round).join(",") : "";
    if (now === at) return;
    at = now;
    if (now) {
      sampleOne(e);
      fitLens(e);
    }
  });
  watch.observe(e, { attributes: true, attributeFilter: ["hidden", "style"] });
  const s: Surface = { watch };
  tracked.set(e, s);
  e.dataset.glassInk = scheme() === "light" ? rememberedInk() : "light";
  if (lensOK && e.matches(lensed)) {
    s.resize = new ResizeObserver(() => fitLens(e));
    s.resize.observe(e);
  }
  requestAnimationFrame(() => {
    if (tracked.has(e) && shown(e)) sampleOne(e);
  });
}

/** Stops following a surface; clean also takes off what the glass put on it (the glass turned off). */
function drop(e: HTMLElement, clean = false) {
  const s = tracked.get(e);
  if (!s) return;
  s.watch.disconnect();
  s.resize?.disconnect();
  if (s.lens) {
    clearTimeout(s.lens.settle);
    s.lens.svg.remove();
  }
  tracked.delete(e);
  if (clean) {
    delete e.dataset.glassInk;
    for (const p of ["--glass-density", "--glass-sat-kept", "--glass-lens"]) e.style.removeProperty(p);
  }
}

function sampleSoon(now: boolean) {
  const wait = now ? 0 : Math.max(0, lastSample + resampleMs - performance.now());
  clearTimeout(sampleTimer);
  sampleTimer = window.setTimeout(() => {
    const run = () => {
      sampleTimer = undefined;
      lastSample = performance.now();
      for (const e of tracked.keys()) if (shown(e)) sampleOne(e);
    };
    if (now || !("requestIdleCallback" in window)) run();
    else requestIdleCallback(run, { timeout: 500 });
  }, wait);
}

/** A colour in CSS's rgb() form, or none when it is clear. */
function parseColor(c: string): RGB | undefined {
  const m = c.match(/rgba?\(([\d.]+)[, ]+([\d.]+)[, ]+([\d.]+)(?:[,/ ]+([\d.]+))?/);
  if (!m || (m[4] !== undefined && Number(m[4]) < 0.5)) return undefined;
  return [Number(m[1]), Number(m[2]), Number(m[3])];
}

/** The probe's size: the 5 by 3 grid, each cell read at 4 by 2 points, for its mean and for the texture. */
const fx = 4;
const fy = 2;

/** The terminal canvas wholly under a surface, if one is. */
function canvasUnder(e: HTMLElement, r: DOMRect): HTMLCanvasElement | undefined {
  for (const hit of document.elementsFromPoint(r.left + r.width / 2, r.top + r.height / 2)) {
    if (e.contains(hit) || (hit instanceof HTMLElement && hit.matches(surfaces))) continue;
    if (!(hit instanceof HTMLCanvasElement) || !hit.classList.contains("screen")) {
      // A clear element (a column of panels) hides nothing; a painted one hides the terminal.
      if (parseColor(getComputedStyle(hit).backgroundColor)) return undefined;
      continue;
    }
    const c = hit.getBoundingClientRect();
    return r.left >= c.left && r.top >= c.top && r.right <= c.right && r.bottom <= c.bottom ? hit : undefined;
  }
  return undefined;
}

/** The colour of the page under a point, without the surface: the first painted background under it, else the page's. */
function under(e: HTMLElement, x: number, y: number): string {
  for (const hit of document.elementsFromPoint(x, y)) {
    if (e.contains(hit) || (hit instanceof HTMLElement && hit.matches(surfaces))) continue;
    if (hit instanceof HTMLCanvasElement && hit.classList.contains("screen")) {
      // Part over a terminal, part not: the terminal's background, which the pane takes too.
      const bg = hit.parentElement?.parentElement && getComputedStyle(hit.parentElement.parentElement).backgroundColor;
      if (bg && parseColor(bg)) return bg;
    }
    const c = getComputedStyle(hit).backgroundColor;
    if (parseColor(c)) return c;
  }
  return getComputedStyle(document.body).backgroundColor;
}

/** Reads under a surface: the 5 by 3 grid's cells and their 4 by 2 parts; undefined when it cannot be read. */
function samples(e: HTMLElement): { cells: RGB[]; parts: RGB[] } | undefined {
  if (probe === undefined) {
    const c = document.createElement("canvas");
    c.width = 5 * fx;
    c.height = 3 * fy;
    probe = c.getContext("2d");
  }
  if (!probe) return undefined;
  const r = e.getBoundingClientRect();
  try {
    // Wholly over one terminal: each part is the pixel at its centre (nearest, not smoothed: an average over a few
    // glyphs would hide how busy they are), each cell the mean of its 8 parts. Else each cell is the colour painted
    // under its centre.
    const c = canvasUnder(e, r);
    if (c) {
      const cr = c.getBoundingClientRect();
      const kx = c.width / cr.width;
      const ky = c.height / cr.height;
      probe.imageSmoothingEnabled = false;
      probe.drawImage(c, (r.left - cr.left) * kx, (r.top - cr.top) * ky, r.width * kx, r.height * ky, 0, 0, 5 * fx, 3 * fy);
    } else {
      for (let j = 0; j < 3; j++) {
        for (let i = 0; i < 5; i++) {
          probe.fillStyle = under(e, r.left + ((i + 0.5) * r.width) / 5, r.top + ((j + 0.5) * r.height) / 3);
          probe.fillRect(i * fx, j * fy, fx, fy);
        }
      }
    }
    const d = probe.getImageData(0, 0, 5 * fx, 3 * fy).data;
    const parts: RGB[] = [];
    const cells: RGB[] = [];
    for (let k = 0; k < 5 * fx * 3 * fy; k++) parts.push([d[k * 4], d[k * 4 + 1], d[k * 4 + 2]]);
    for (let j = 0; j < 3; j++) {
      for (let i = 0; i < 5; i++) {
        const sum: RGB = [0, 0, 0];
        for (let y = 0; y < fy; y++) for (let x = 0; x < fx; x++) parts[(j * fy + y) * 5 * fx + i * fx + x].forEach((v, n) => (sum[n] += v / (fx * fy)));
        cells.push(sum);
      }
    }
    return { cells, parts };
  } catch {
    return undefined;
  }
}
/** Reads under one surface and sets its ink, its density and the saturation it keeps. */
function sampleOne(e: HTMLElement) {
  const s = tracked.get(e);
  if (!s) return;
  const sc = scheme();
  const got = samples(e);
  let ink: Ink;
  let d = 1;
  let kept = 1;
  if (got) {
    const b: Backdrop = readBackdrop(got.cells, got.parts);
    ink = nextInk(sc, b.luminance, s.ink);
    d = density(b, ink);
    kept = saturationKept(b.chroma);
  } else {
    // Unreadable: the theme's ink on a dense tint.
    ink = sc === "light" ? "dark" : "light";
  }
  s.ink = ink;
  if (e.dataset.glassInk !== ink) e.dataset.glassInk = ink;
  e.style.setProperty("--glass-density", d.toFixed(3));
  e.style.setProperty("--glass-sat-kept", kept.toFixed(3));
  if (sc === "light") remember(ink);
}

/** The displacement maps made, by radius, rim and size, each a PNG decoded before use; the oldest go past 24. */
const maps = new Map<string, Promise<string>>();

function mapURL(w: number, h: number, radius: number, rim: number): Promise<string> {
  const key = `${radius}|${rim}|${w}x${h}`;
  let p = maps.get(key);
  if (!p) {
    const c = document.createElement("canvas");
    c.width = w;
    c.height = h;
    c.getContext("2d")!.putImageData(new ImageData(displacementMap(w, h, radius, rim), w, h), 0, 0);
    const url = c.toDataURL("image/png");
    const img = new Image();
    img.src = url;
    p = img.decode().then(() => url);
    p.catch(() => maps.delete(key));
    maps.set(key, p);
    if (maps.size > 24) maps.delete(maps.keys().next().value!);
  }
  return p;
}

const svgNS = "http://www.w3.org/2000/svg";

function node<K extends keyof SVGElementTagNameMap>(tag: K, attrs: Record<string, string | number>): SVGElementTagNameMap[K] {
  const n = document.createElementNS(svgNS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  return n;
}

/** A surface's own filter, in its own <svg>: the map, three displacements (red bent most, blue least), each channel kept alone, screened together. */
function makeLens(): Lens {
  const id = `gorex-lens-${++nextId}`;
  const svg = node("svg", { width: 0, height: 0, "aria-hidden": "true" });
  svg.style.position = "absolute";
  const filter = node("filter", { id, x: 0, y: 0, width: "100%", height: "100%", "color-interpolation-filters": "sRGB" });
  const image = node("feImage", { x: 0, y: 0, width: 1, height: 1, preserveAspectRatio: "none", result: "map" });
  filter.append(image);
  const maps: SVGFEDisplacementMapElement[] = [];
  const keep = ["1 0 0 0 0  0 0 0 0 0  0 0 0 0 0  0 0 0 1 0", "0 0 0 0 0  0 1 0 0 0  0 0 0 0 0  0 0 0 1 0", "0 0 0 0 0  0 0 0 0 0  0 0 1 0 0  0 0 0 1 0"];
  ["r", "g", "b"].forEach((ch, i) => {
    const m = node("feDisplacementMap", { in: "SourceGraphic", in2: "map", scale: 0, xChannelSelector: "R", yChannelSelector: "G", result: `d${ch}` });
    maps.push(m);
    filter.append(m, node("feColorMatrix", { in: `d${ch}`, type: "matrix", values: keep[i], result: ch }));
  });
  filter.append(node("feBlend", { in: "r", in2: "g", mode: "screen", result: "rg" }), node("feBlend", { in: "rg", in2: "b", mode: "screen" }));
  svg.append(filter);
  document.body.append(svg);
  return { svg, image, maps, key: "" };
}

/** Sizes the map and its offsets to w by h: until the right map is made, the last one stretched. */
function sizeLens(l: Lens, w: number, h: number, rim: number) {
  l.image.setAttribute("width", String(w));
  l.image.setAttribute("height", String(h));
  const scale = 2 * maxOffset(rim);
  [1 + split, 1, 1 - split].forEach((k, i) => l.maps[i].setAttribute("scale", (scale * k).toFixed(2)));
}

/** Gives a surface its rim lens, fitted to its size and corners, or takes it away (too small, or a layer of its own). */
function fitLens(e: HTMLElement) {
  const s = tracked.get(e);
  if (!s || !lensOK || !e.matches(lensed) || !shown(e)) return;
  const w = Math.round(e.offsetWidth);
  const h = Math.round(e.offsetHeight);
  const cs = getComputedStyle(e);
  if (w < 32 || h < 32 || cs.willChange !== "auto") {
    e.style.removeProperty("--glass-lens");
    return;
  }
  const radius = Math.min(parseFloat(cs.borderTopLeftRadius) || 0, w / 2, h / 2);
  const rim = Math.round(rimWidth(w, h));
  const key = `${radius}|${rim}|${w}x${h}`;
  if (s.lens?.key === key) return;
  const l = (s.lens ??= makeLens());
  if (l.key) {
    // Moved or resized: stretch the map now, make the right one once the size settles.
    sizeLens(l, w, h, rim);
    clearTimeout(l.settle);
    l.settle = window.setTimeout(() => build(e, l, w, h, radius, rim, key), 200);
    return;
  }
  build(e, l, w, h, radius, rim, key);
}

function build(e: HTMLElement, l: Lens, w: number, h: number, radius: number, rim: number, key: string) {
  mapURL(w, h, radius, rim).then(
    (url) => {
      if (!tracked.has(e)) return;
      l.image.setAttribute("href", url);
      sizeLens(l, w, h, rim);
      l.key = key;
      e.style.setProperty("--glass-lens", `url(#${l.svg.querySelector("filter")!.id})`);
    },
    () => e.style.removeProperty("--glass-lens"),
  );
}
