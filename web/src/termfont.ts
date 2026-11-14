import { Ghostty, RGB } from "./ghostty";
import { css } from "./termtheme";

/** An underline (1 to 5, as GhosttySgrUnderline), a strikethrough or an overline, over cells x0 to x1. */
export interface Deco {
  x0: number;
  x1: number;
  kind: number;
  c: RGB;
}
export const decoStrike = 10, decoOverline = 11;

export function keyCode(g: Ghostty, code: string): number {
  const name = /^Key[A-Z]$/.test(code)
    ? code.slice(3)
    : /^F\d+$/.test(code)
      ? code
      : code.replace(/([a-z])([A-Z0-9])/g, "$1_$2").toUpperCase();
  return g.m.types.GhosttyKey.values![name] ?? g.e("GhosttyKey", "UNIDENTIFIED");
}

const unshiftedSymbols: Record<string, string> = {
  Minus: "-", Equal: "=", BracketLeft: "[", BracketRight: "]", Backslash: "\\", Semicolon: ";",
  Quote: "'", Backquote: "`", Comma: ",", Period: ".", Slash: "/", Space: " ",
};

/** The character the physical key types without Shift, on a US layout. */
export function unshiftedOf(e: KeyboardEvent): number {
  if (/^Key[A-Z]$/.test(e.code)) return e.code.charCodeAt(3) + 32;
  if (/^Digit\d$/.test(e.code)) return e.code.charCodeAt(5);
  const s = unshiftedSymbols[e.code];
  if (s) return s.charCodeAt(0);
  return [...e.key].length === 1 ? e.key.codePointAt(0)! : 0;
}

/**
 * Draws an underline, a strikethrough or an overline over a row's cells,
 * as the desktop's drawDeco does.
 */
export function drawDeco(ctx: CanvasRenderingContext2D, font: Font, d: Deco, ox: number, ry: number) {
  const { w, size } = font;
  const thick = Math.max(Math.round(size / 14), 1);
  const x0 = ox + d.x0 * w, x1 = ox + d.x1 * w;
  const ul = ry + font.baseline + Math.max(Math.round(size / 10), 1);
  ctx.fillStyle = css(d.c);
  switch (d.kind) {
    case decoStrike:
      ctx.fillRect(x0, Math.round(ry + font.baseline - size * 0.3), x1 - x0, thick);
      break;
    case decoOverline:
      ctx.fillRect(x0, ry, x1 - x0, thick);
      break;
    case 2: // double
      ctx.fillRect(x0, ul - thick, x1 - x0, thick);
      ctx.fillRect(x0, ul + thick, x1 - x0, thick);
      break;
    case 3: { // curly: a sine, a cell's width a period
      const amp = 1.5 * thick;
      const n = Math.max(1, Math.round(((x1 - x0) / w) * 8));
      ctx.beginPath();
      ctx.moveTo(x0, ul);
      for (let i = 1; i <= n; i++) {
        const x = x0 + ((x1 - x0) * i) / n;
        ctx.lineTo(x, ul + amp * Math.sin(((x - x0) / w) * 2 * Math.PI));
      }
      ctx.strokeStyle = ctx.fillStyle;
      ctx.lineWidth = thick;
      ctx.stroke();
      break;
    }
    case 4: // dotted
    case 5: { // dashed
      const [step, dash] = d.kind === 5 ? [w / 2, w / 4] : [2 * thick, thick];
      for (let x = x0; x < x1; x += step) ctx.fillRect(Math.round(x), ul, Math.round(Math.min(dash, x1 - x)), thick);
      break;
    }
    default:
      ctx.fillRect(x0, ul, x1 - x0, thick);
  }
}

/**
 * The terminal's font at the device's pixel ratio, with the desktop's
 * cell metrics (MyGo's view.layout): a cell is the widest ASCII advance
 * wide and the font's line (ascent and descent) high, each rounded up to
 * whole device pixels, with the text centred in it.
 */
export class Font {
  /** The cell's width and height, the baseline from its top, and the font's own advance, in device pixels. */
  w = 0;
  h = 0;
  baseline = 0;
  adv = 0;
  /** The size in device pixels. */
  readonly size: number;
  private widths = new Map<string, number>();
  private readonly ctx = document.createElement("canvas").getContext("2d")!;

  constructor(readonly family: string, px: number, readonly scale: number, lineHeight = 1) {
    this.size = px * scale;
    const c = this.ctx;
    c.font = this.css(0);
    let asc = 0, desc = 0;
    for (let i = 0x20; i < 0x7f; i++) {
      const m = c.measureText(String.fromCharCode(i));
      this.adv = Math.max(this.adv, m.width);
      asc = m.fontBoundingBoxAscent;
      desc = m.fontBoundingBoxDescent;
    }
    this.w = Math.max(Math.ceil(this.adv - 0.05), 1);
    this.h = Math.max(Math.ceil((asc + desc) * lineHeight - 0.05), 1);
    this.baseline = Math.round((this.h - (asc + desc)) / 2 + asc);
  }

  /** The CSS font of a variant (1 bold, 2 italic), at a fraction of the size. */
  css(variant: number, k = 1): string {
    return `${variant & 2 ? "italic " : ""}${variant & 1 ? 700 : 400} ${this.size * k}px ${this.family}`;
  }

  /** The width text draws at in a variant, cached. */
  measure(text: string, variant: number): number {
    const key = variant + text;
    let w = this.widths.get(key);
    if (w === undefined) {
      if (this.widths.size > 2048) this.widths.clear();
      this.ctx.font = this.css(variant);
      w = this.ctx.measureText(text).width;
      this.widths.set(key, w);
    }
    return w;
  }
}
