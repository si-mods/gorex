// The math of the liquid glass (docs/WEB-005-liquid-glass.md), with no DOM:
// what the glass reads from the terminal under it, which ink it takes, how
// dense its tint is, and the displacement map of its rim lens. glass.ts
// applies it to the page; test/glass.test.ts checks it.

export type RGB = [number, number, number];
/** The text colour on a glass surface: dark ink on light glass, light ink on dark glass. */
export type Ink = "light" | "dark";
export type Scheme = "light" | "dark";

/** A colour's luminance, 0 to 1, as the glass reads it: (0.299 R + 0.587 G + 0.114 B) / 255. */
export const luminance = ([r, g, b]: RGB): number => (0.299 * r + 0.587 * g + 0.114 * b) / 255;

/** The average of the samples without the `drop` darkest and the `drop` brightest, so that a cursor or one glyph does not decide. */
export function trimmedMean(samples: RGB[], drop = 2): RGB {
  if (!samples.length) return [0, 0, 0];
  const sorted = [...samples].sort((a, b) => luminance(a) - luminance(b));
  const kept = sorted.length > 2 * drop ? sorted.slice(drop, sorted.length - drop) : sorted;
  const sum = kept.reduce<RGB>((s, c) => [s[0] + c[0], s[1] + c[1], s[2] + c[2]], [0, 0, 0]);
  return [sum[0] / kept.length, sum[1] / kept.length, sum[2] / kept.length];
}

/** What the glass reads from what is under it. */
export interface Backdrop {
  /** The trimmed mean colour. */
  mean: RGB;
  /** The trimmed mean's luminance, 0 to 1. */
  luminance: number;
  /** How busy it is: the brightest sample's luminance less the darkest's, 0 to 1. */
  texture: number;
  /** How coloured the mean is: its largest channel less its smallest, over 255. */
  chroma: number;
}

/**
 * Reads a backdrop from its samples (a 5 by 3 grid under the surface), and
 * its texture from finer parts of the same grid when given: a cell
 * averaged over a few glyphs hides how busy they are.
 */
export function readBackdrop(samples: RGB[], parts: RGB[] = samples): Backdrop {
  const mean = trimmedMean(samples);
  const lums = parts.map(luminance);
  return {
    mean,
    luminance: luminance(mean),
    texture: lums.length ? Math.max(...lums) - Math.min(...lums) : 0,
    chroma: (Math.max(...mean) - Math.min(...mean)) / 255,
  };
}

/** The half width of the band around 0.5 where the ink keeps what it was, so that it does not flicker. */
export const inkHysteresis = 0.05;

/**
 * The ink over a backdrop of this luminance: in the light scheme dark ink
 * on light glass, light ink on dark glass under 0.5, with a band of ±0.05
 * where the ink stays as it was; in the dark scheme always light ink.
 */
export function nextInk(scheme: Scheme, lum: number, prev?: Ink): Ink {
  if (scheme === "dark") return "light";
  if (lum < 0.5 - inkHysteresis) return "light";
  if (lum > 0.5 + inkHysteresis) return "dark";
  return prev ?? (lum < 0.5 ? "light" : "dark");
}

const clamp = (v: number, lo = 0, hi = 1) => Math.min(hi, Math.max(lo, v));

/**
 * How dense the tint is, 0 (thin) to 1 (dense): denser when the backdrop's
 * brightness is close to the ink's (the ink would fade into it), when it is
 * busy, and when it is coloured.
 */
export function density(b: Backdrop, ink: Ink): number {
  const closeness = 1 - Math.abs(b.luminance - (ink === "light" ? 1 : 0));
  return clamp(0.55 * closeness + 0.7 * b.texture + 0.3 * b.chroma);
}

/** How much of the material's saturation the glass keeps over a coloured backdrop, 0.45 to 1. */
export const saturationKept = (chroma: number): number => Math.max(0.45, 1 - 0.75 * chroma);

/** A glass material: its tint, how thin and how dense, and its backdrop filter. Mirrored by the --glass-* tokens in index.html. */
export interface Material {
  tint: RGB;
  /** The tint's alpha at density 0 and at density 1, at tint scale 1. */
  thin: number;
  dense: number;
  /** The backdrop filter's brightness and saturate amounts. */
  brightness: number;
  saturate: number;
  /** The inner light's strength: 1 on light glass, about a third on dark glass. */
  light: number;
}

/** The three materials, as index.html's tokens: light glass in the light scheme, dark glass in it, and the dark scheme's glass. */
export const materials: Record<"lightGlass" | "darkGlass" | "darkScheme", Material> = {
  lightGlass: { tint: [255, 255, 255], thin: 0.06, dense: 0.22, brightness: 1.06, saturate: 1.8, light: 1 },
  darkGlass: { tint: [16, 18, 22], thin: 0.1, dense: 0.62, brightness: 0.85, saturate: 1.7, light: 0.33 },
  darkScheme: { tint: [20, 20, 24], thin: 0.16, dense: 0.9, brightness: 0.8, saturate: 1.6, light: 0.33 },
};

/** The material in use for a scheme and an ink. */
export const materialOf = (scheme: Scheme, ink: Ink): Material =>
  scheme === "dark" ? materials.darkScheme : ink === "dark" ? materials.lightGlass : materials.darkGlass;

/** The tint's alpha: thin to dense by density, times the tint scale (Settings, 0 to 1.5). */
export const tintAlpha = (m: Material, d: number, scale: number): number => clamp((m.thin + (m.dense - m.thin) * d) * scale);

/** A backdrop-filter amount (brightness or saturate) at a tint scale: below 1 it moves towards 1 (no change), at 0 it is 1. */
export const filterAmount = (amount: number, scale: number): number => 1 + (amount - 1) * Math.min(1, Math.max(0, scale));

/** CSS's saturate() on an sRGB colour (Filter Effects, feColorMatrix saturate). */
function saturate([r, g, b]: RGB, s: number): RGB {
  return [
    (0.213 + 0.787 * s) * r + (0.715 - 0.715 * s) * g + (0.072 - 0.072 * s) * b,
    (0.213 - 0.213 * s) * r + (0.715 + 0.285 * s) * g + (0.072 - 0.072 * s) * b,
    (0.213 - 0.213 * s) * r + (0.715 - 0.715 * s) * g + (0.072 + 0.928 * s) * b,
  ].map((v) => clamp(v, 0, 255)) as RGB;
}

/**
 * The glass's colour over a backdrop colour: the backdrop saturated and
 * brightened as the backdrop filter does, the tint over it, and the inner
 * light at its brightest (the top left) over that.
 */
export function glassColor(backdrop: RGB, m: Material, d: number, scale: number, satKept = 1): RGB {
  const s = filterAmount(1 + (m.saturate * satKept - 1), scale);
  const br = filterAmount(m.brightness, scale);
  const c = saturate(backdrop, s).map((v) => clamp(v * br, 0, 255));
  const a = tintAlpha(m, d, scale);
  const tinted = c.map((v, i) => m.tint[i] * a + v * (1 - a));
  const l = 0.3 * m.light * Math.min(1, scale);
  return tinted.map((v) => 255 * l + v * (1 - l)) as RGB;
}

/** The inks: on light glass black and a grey, multiplied; on dark glass white and a light grey, added (plus-lighter). */
export const inks = {
  dark: { primary: { c: [0, 0, 0] as RGB, a: 0.86 }, secondary: { c: [60, 60, 67] as RGB, a: 0.7 } },
  light: { primary: { c: [255, 255, 255] as RGB, a: 0.94 }, secondary: { c: [235, 235, 245] as RGB, a: 0.64 } },
};

/** The ink's colour as drawn on the glass, with its blend mode (multiply for dark ink, plus-lighter for light). */
export function inkOn(glass: RGB, ink: Ink, level: "primary" | "secondary"): RGB {
  const { c, a } = inks[ink][level];
  if (ink === "dark") return glass.map((v, i) => (1 - a) * v + a * ((v * c[i]) / 255)) as RGB;
  return glass.map((v, i) => clamp(v + a * c[i], 0, 255)) as RGB;
}

/** WCAG 2's relative luminance of an sRGB colour. */
function relLum(c: RGB): number {
  const [r, g, b] = c.map((v) => {
    const s = v / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/** WCAG 2's contrast ratio of two colours, 1 to 21. */
export function contrast(a: RGB, b: RGB): number {
  const [x, y] = [relLum(a), relLum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

/** The rim lens's width for a surface: a 3.5th of its short side, 4 to 44 px. */
export const rimWidth = (w: number, h: number): number => Math.max(4, Math.min(44, Math.min(w, h) / 3.5));

/** How far inward a point at distance d from the edge looks, in px: 1.1 rim at the edge, falling to 0 at d = rim. */
export function rimOffset(d: number, rim: number): number {
  if (d >= rim || rim <= 0) return 0;
  return 1.1 * rim * (1 - Math.max(0, d) / rim) ** 2.6;
}

/** The largest offset of a rim, at the edge. */
export const maxOffset = (rim: number): number => 1.1 * rim;

/**
 * The rim lens's displacement map for a w by h rounded rectangle of corner
 * radius r: RGBA, R the x bend and G the y bend (128 none, 0 and 255 the
 * largest offset, maxOffset(rim), each way), B 128, A 255. A point at
 * distance d inside the edge looks inward by rimOffset(d, rim), along the
 * inward normal of the nearest edge, the corners' arcs included. With
 * feDisplacementMap at scale 2 maxOffset(rim), the map's offsets are those.
 */
export function displacementMap(w: number, h: number, r: number, rim = rimWidth(w, h)): Uint8ClampedArray<ArrayBuffer> {
  const out = new Uint8ClampedArray(w * h * 4);
  r = Math.max(0, Math.min(r, w / 2, h / 2));
  const max = maxOffset(rim);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      // The pixel's centre, from the centre of the box, folded into its first quadrant.
      const px = x + 0.5 - w / 2;
      const py = y + 0.5 - h / 2;
      const qx = Math.abs(px) - (w / 2 - r);
      const qy = Math.abs(py) - (h / 2 - r);
      let d: number;
      let nx: number;
      let ny: number;
      if (qx > 0 && qy > 0) {
        // A corner: the distance to the arc, along its radius.
        const len = Math.hypot(qx, qy);
        d = r - len;
        nx = qx / len;
        ny = qy / len;
      } else if (qx > qy) {
        d = r - qx;
        nx = 1;
        ny = 0;
      } else {
        d = r - qy;
        nx = 0;
        ny = 1;
      }
      const i = (y * w + x) * 4;
      let ox = 0;
      let oy = 0;
      if (d >= 0) {
        // Inward is against the outward normal, which points away from the centre.
        const o = rimOffset(d, rim);
        ox = -Math.sign(px) * nx * o;
        oy = -Math.sign(py) * ny * o;
      }
      out[i] = Math.round(128 + (127 * ox) / max);
      out[i + 1] = Math.round(128 + (127 * oy) / max);
      out[i + 2] = 128;
      out[i + 3] = 255;
    }
  }
  return out;
}
