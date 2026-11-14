import { RGB } from "./ghostty";

export interface Theme {
  background?: RGB;
  foreground?: RGB;
  cursor?: RGB;
  palette?: Map<number, RGB>;
  /** A Ghostty theme's selection-background and selection-foreground; without them the selection inverts its cells, as Ghostty's does. */
  selection?: RGB;
  selectionText?: RGB;
}

/** A theme message's colour: RRGGBB or #RRGGBB. */
export function hex(s: unknown): RGB | undefined {
  if (typeof s !== "string") return undefined;
  const h = s.startsWith("#") ? s.slice(1) : s;
  if (!/^[0-9a-fA-F]{6}$/.test(h)) return undefined;
  const v = parseInt(h, 16);
  return [v >> 16, (v >> 8) & 0xff, v & 0xff];
}

/** The theme a stream's theme message gives. */
export function themeOf(m: any): Theme {
  const t: Theme = { background: hex(m.background), foreground: hex(m.foreground), cursor: hex(m.cursor) };
  if (Array.isArray(m.palette)) {
    t.palette = new Map();
    for (const e of m.palette) {
      const [i, c] = String(e).split("=");
      const rgb = hex(c);
      if (rgb && Number(i) >= 0 && Number(i) < 256) t.palette.set(Number(i), rgb);
    }
  }
  return t;
}

/**
 * The desktop's colours when no Ghostty theme is set: GoRex's dark
 * terminal theme (style.go, darkTerm), over xterm's 256-colour cube and
 * grays, as MyGo's Theme.palette fills them.
 */
export function desktopTheme(): Theme {
  const base = [
    0x3a3a40, 0xff6b7f, 0x7bd88f, 0xe5c07b, 0x6cb2ff, 0xc792ea, 0x56d4dd, 0xd0d0d6,
    0x6e6e78, 0xff8c9c, 0x9be8a8, 0xf2d28a, 0x8ec5ff, 0xd7a8f2, 0x7fe3ea, 0xffffff,
  ];
  const palette = new Map<number, RGB>();
  base.forEach((v, i) => palette.set(i, [v >> 16, (v >> 8) & 0xff, v & 0xff]));
  const levels = [0, 95, 135, 175, 215, 255];
  for (let i = 0; i < 216; i++) palette.set(16 + i, [levels[(i / 36) | 0], levels[((i / 6) | 0) % 6], levels[i % 6]]);
  for (let i = 0; i < 24; i++) palette.set(232 + i, [8 + 10 * i, 8 + 10 * i, 8 + 10 * i]);
  return { background: [0x1e, 0x1e, 0x22], foreground: [0xe6, 0xe6, 0xea], cursor: [0xe6, 0xe6, 0xea], palette };
}

/** The desktop's minimum contrast of text with its background when none is set (settings.go). */
export const minimumContrast = 3;

export const css = (c: RGB) => `rgb(${c[0]},${c[1]},${c[2]})`;
export const same = (a: RGB, b: RGB) => a[0] === b[0] && a[1] === b[1] && a[2] === b[2];

const linear = Array.from({ length: 256 }, (_, v) => {
  const f = v / 255;
  return f <= 0.04045 ? f / 12.92 : ((f + 0.055) / 1.055) ** 2.4;
});
const luminance = (c: RGB) => 0.2126 * linear[c[0]] + 0.7152 * linear[c[1]] + 0.0722 * linear[c[2]];
const contrast = (a: number, b: number) => (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
const mix = (a: RGB, b: RGB, t: number): RGB => [0, 1, 2].map((i) => Math.round(a[i] + (b[i] - a[i]) * t)) as RGB;

/**
 * fg with at least the contrast ratio with bg, as WCAG reckons it, as the
 * desktop corrects it (MyGo's contrasted): fg mixed toward black or white,
 * whichever contrasts more with bg, as little as reaches it.
 */
export function contrasted(fg: RGB, bg: RGB, ratio: number): RGB {
  const lb = luminance(bg);
  if (contrast(luminance(fg), lb) >= ratio) return fg;
  const to: RGB = contrast(1, lb) > contrast(0, lb) ? [255, 255, 255] : [0, 0, 0];
  let lo = 0, hi = 1;
  for (let i = 0; i < 8; i++) {
    const mid = (lo + hi) / 2;
    if (contrast(luminance(mix(fg, to, mid)), lb) >= ratio) hi = mid;
    else lo = mid;
  }
  return mix(fg, to, hi);
}
