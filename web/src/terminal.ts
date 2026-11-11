// One terminal: a libghostty-vt terminal restored from the snapshot a
// stream starts with, fed the program's output, drawn on a canvas from its
// render state, and encoding the keys typed for the program's modes.
import { drawBox, drawn, noMinContrast } from "./box";
import { Ghostty, Mods, RGB } from "./ghostty";

export interface Theme {
  background?: RGB;
  foreground?: RGB;
  cursor?: RGB;
  palette?: Map<number, RGB>;
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

const css = (c: RGB) => `rgb(${c[0]},${c[1]},${c[2]})`;
const same = (a: RGB, b: RGB) => a[0] === b[0] && a[1] === b[1] && a[2] === b[2];

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

/** A cell of a row, as it draws. */
interface Cell {
  col: number;
  span: number;
  text: string;
  cp: number;
  fg: RGB;
  variant: number; // 1 bold, 2 italic
  faint: boolean;
}

/** An underline (1 to 5, as GhosttySgrUnderline), a strikethrough or an overline, over cells x0 to x1. */
interface Deco {
  x0: number;
  x1: number;
  kind: number;
  c: RGB;
}
const decoStrike = 10, decoOverline = 11;

/** What draw needs besides the terminal: where the grid starts, and the cursor's state. */
export interface Frame {
  font: Font;
  /** The grid's top left in device pixels. */
  ox: number;
  oy: number;
  full: boolean;
  focused: boolean;
  /** The cursor is in the off phase of its blink. */
  blinkOff: boolean;
}

/** Whether a cell's text goes with its neighbours in one run, in the primary font. */
const simple = (c: Cell) => c.span === 1 && c.text.length === 1 && c.cp >= 0x20 && c.cp < 0x2000 && !(c.cp >= 0x300 && c.cp < 0x370);

export class Terminal {
  private rs: number;
  private readonly rows: number;
  private readonly cells: number;
  private readonly encoder: number;
  /** Scratch for the get calls: a style, colours, the cursor, a viewport scroll, a u64, graphemes. */
  private style: number;
  private colors: number;
  private cursor: number;
  private scroll: number;
  private scratch: number;
  private graphemes = 0;
  private graphemesCap = 0;
  private freed = false;
  /** The row the cursor was drawn on last, which must draw again when it moves. */
  private cursorRow = -1;
  private contrasts = new Map<string, RGB>();
  /** Whether the cursor blinks, as of the last draw. */
  blinking = false;

  private constructor(private g: Ghostty, private t: number) {
    const x = g.x;
    this.rs = g.make("render state", (s) => x.ghostty_render_state_new(0, s));
    this.rows = g.make("row iterator", (s) => x.ghostty_render_state_row_iterator_new(0, s));
    this.cells = g.make("row cells", (s) => x.ghostty_render_state_row_cells_new(0, s));
    this.encoder = g.make("key encoder", (s) => x.ghostty_key_encoder_new(0, s));
    this.style = g.sized("GhosttyStyle");
    this.colors = g.sized("GhosttyRenderStateColors");
    this.cursor = g.sized("GhosttyRenderStateCursor");
    this.scroll = g.alloc(g.size("GhosttyTerminalScrollViewport"));
    this.scratch = g.alloc(16);
  }

  /** Decodes a complete GHOSTSNP snapshot into a terminal. */
  static fromSnapshot(g: Ghostty, snapshot: Uint8Array): Terminal {
    const x = g.x;
    const p = g.put(snapshot);
    try {
      const dec = g.make("snapshot decoder", (s) => x.ghostty_snapshot_decoder_new_buf(0, s, p, snapshot.length));
      try {
        return new Terminal(g, g.make("snapshot decode", (s) => x.ghostty_snapshot_decoder_decode(dec, s)));
      } finally {
        x.ghostty_snapshot_decoder_free(dec);
      }
    } finally {
      g.free(p, Math.max(snapshot.length, 1));
    }
  }

  free() {
    if (this.freed) return;
    this.freed = true;
    const { g } = this;
    g.free(this.style, g.size("GhosttyStyle"));
    g.free(this.colors, g.size("GhosttyRenderStateColors"));
    g.free(this.cursor, g.size("GhosttyRenderStateCursor"));
    g.free(this.scroll, g.size("GhosttyTerminalScrollViewport"));
    g.free(this.scratch, 16);
    if (this.graphemes) g.free(this.graphemes, this.graphemesCap * 4);
    g.x.ghostty_key_encoder_free(this.encoder);
    g.x.ghostty_render_state_row_cells_free(this.cells);
    g.x.ghostty_render_state_row_iterator_free(this.rows);
    g.x.ghostty_render_state_free(this.rs);
    g.x.ghostty_terminal_free(this.t);
  }

  write(b: Uint8Array) {
    if (b.length === 0) return;
    const p = this.g.put(b);
    this.g.x.ghostty_terminal_vt_write(this.t, p, b.length);
    this.g.free(p, b.length);
  }

  resize(cols: number, rows: number, cellW: number, cellH: number) {
    if (cols > 0 && rows > 0) this.g.x.ghostty_terminal_resize(this.t, cols, rows, cellW, cellH);
  }

  /** Sets the default colours; one the theme leaves out stays as it is. */
  setTheme(theme: Theme) {
    const { g } = this;
    const x = g.x;
    const set = (opt: string, c?: RGB) => {
      if (!c) return;
      g.bytes.set(c, this.scratch);
      x.ghostty_terminal_set(this.t, g.e("GhosttyTerminalOption", opt), this.scratch);
    };
    set("COLOR_BACKGROUND", theme.background);
    set("COLOR_FOREGROUND", theme.foreground);
    set("COLOR_CURSOR", theme.cursor);
    if (theme.palette?.size) {
      // The palette goes whole: start from the frame's own.
      this.update();
      const pal = this.colors + g.off("GhosttyRenderStateColors", "palette");
      const p = g.alloc(768);
      g.bytes.copyWithin(p, pal, pal + 768);
      for (const [i, c] of theme.palette) g.bytes.set(c, p + i * 3);
      x.ghostty_terminal_set(this.t, g.e("GhosttyTerminalOption", "COLOR_PALETTE"), p);
      g.free(p, 768);
    }
  }

  /** Updates the render state from the terminal, and reads its colours and cursor. */
  private update(): boolean {
    const { g } = this;
    const x = g.x;
    if (x.ghostty_render_state_update(this.rs, this.t) !== 0) return false;
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "COLORS"), this.colors);
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "CURSOR"), this.cursor);
    return true;
  }

  /** The frame's default background. */
  background(): RGB {
    this.update();
    return this.g.rgb(this.colors + this.g.off("GhosttyRenderStateColors", "background"));
  }

  /** The size in cells. */
  size(): { cols: number; rows: number } {
    const { g } = this;
    g.x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "COLS"), this.scratch);
    const cols = g.view.getUint16(this.scratch, true);
    g.x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "ROWS"), this.scratch);
    return { cols, rows: g.view.getUint16(this.scratch, true) };
  }

  /** A terminal datum that is one byte (a bool) or a 32-bit enum. */
  private datum(name: string, bytes: 1 | 4): number {
    const { g } = this;
    g.view.setUint32(this.scratch, 0, true);
    if (g.x.ghostty_terminal_get(this.t, g.e("GhosttyTerminalData", name), this.scratch) !== 0) return 0;
    return bytes === 1 ? g.bytes[this.scratch] : g.view.getInt32(this.scratch, true);
  }

  /** Whether the program takes the mouse, and so the wheel. */
  mouseTracking(): boolean {
    return this.datum("MOUSE_TRACKING", 1) !== 0;
  }

  /** Whether the alternate screen shows, as in full-screen programs. */
  altScreen(): boolean {
    return this.datum("ACTIVE_SCREEN", 4) === this.g.e("GhosttyTerminalScreen", "ALTERNATE");
  }

  /** Whether the viewport follows the active screen, rather than showing the scrollback. */
  atBottom(): boolean {
    return this.datum("VIEWPORT_ACTIVE", 1) !== 0;
  }

  /** Moves the viewport by rows (negative up), or to the bottom. */
  scrollBy(rows: number | "bottom") {
    const { g } = this;
    const p = this.scroll;
    g.bytes.fill(0, p, p + g.size("GhosttyTerminalScrollViewport"));
    const tag = g.e("GhosttyTerminalScrollViewportTag", rows === "bottom" ? "BOTTOM" : "DELTA");
    g.view.setInt32(p + g.off("GhosttyTerminalScrollViewport", "tag"), tag, true);
    if (rows !== "bottom") g.view.setInt32(p + g.off("GhosttyTerminalScrollViewport", "value"), rows, true);
    g.x.ghostty_terminal_scroll_viewport(this.t, p);
  }

  private contrasted(fg: RGB, bg: RGB): RGB {
    const k = `${fg}|${bg}`;
    let c = this.contrasts.get(k);
    if (!c) {
      if (this.contrasts.size > 4096) this.contrasts.clear();
      c = contrasted(fg, bg, minimumContrast);
      this.contrasts.set(k, c);
    }
    return c;
  }

  /** A style colour (underline_color) as RGB, or undefined for none. */
  private styleColor(p: number, palette: number): RGB | undefined {
    const { g } = this;
    const tag = g.view.getInt32(p + g.off("GhosttyStyleColor", "tag"), true);
    const v = p + g.off("GhosttyStyleColor", "value");
    if (tag === g.e("GhosttyStyleColorTag", "PALETTE")) return g.rgb(palette + g.bytes[v] * 3);
    if (tag === g.e("GhosttyStyleColorTag", "RGB")) return g.rgb(v);
    return undefined;
  }

  /**
   * Draws what changed since the last draw, or everything when full, as the
   * desktop's terminal does (MyGo's paint.go): each row's backgrounds, a
   * block cursor, the text, the drawn characters and the lines, then a
   * cursor of another shape.
   */
  draw(ctx: CanvasRenderingContext2D, f: Frame) {
    const { g } = this;
    const x = g.x;
    if (!this.update()) return;
    const v = g.view;
    let full = f.full;
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "DIRTY"), this.scratch);
    if (v.getInt32(this.scratch, true) === g.e("GhosttyRenderStateDirty", "FULL")) full = true;
    const co = (k: string) => this.colors + g.off("GhosttyRenderStateColors", k);
    const bg = g.rgb(co("background"));
    const fg = g.rgb(co("foreground"));
    const palette = co("palette");
    const cursorColor = g.bytes[co("cursor_has_value")] ? g.rgb(co("cursor")) : fg;
    const cu = (k: string) => this.cursor + g.off("GhosttyRenderStateCursor", k);
    this.blinking = g.bytes[cu("blinking")] !== 0;
    const cursorIn = g.bytes[cu("viewport_has_value")] !== 0 && g.bytes[cu("visible")] !== 0;
    const showCursor = cursorIn && !(f.focused && this.blinking && f.blinkOff);
    let cx = v.getUint16(cu("viewport_x"), true);
    const cy = cursorIn ? v.getUint16(cu("viewport_y"), true) : -1;
    if (g.bytes[cu("wide_tail")] && cx > 0) cx--;
    const st = (s: string) => g.e("GhosttyRenderStateCursorVisualStyle", s);
    let cursorStyle = v.getInt32(cu("visual_style"), true);
    if (!f.focused && cursorStyle === st("BLOCK")) cursorStyle = st("BLOCK_HOLLOW");
    const block = showCursor && cursorStyle === st("BLOCK");
    const { cols } = this.size();
    const { font, ox, oy } = f;
    const { w, h } = font;
    const lastCursorRow = this.cursorRow;
    this.cursorRow = cy;

    const rowData = (d: string) => g.e("GhosttyRenderStateRowData", d);
    const cellData = (d: string) => g.e("GhosttyRenderStateRowCellsData", d);
    const styleOff = (k: string) => this.style + g.off("GhosttyStyle", k);
    const wide = g.cellBits("wide");
    const wideOf = (raw: bigint) => Number((raw >> BigInt(wide.lsb)) & BigInt((1 << wide.width) - 1));
    const WIDE = g.e("GhosttyCellWide", "WIDE");
    const SPACER_TAIL = g.e("GhosttyCellWide", "SPACER_TAIL");
    const SPACER_HEAD = g.e("GhosttyCellWide", "SPACER_HEAD");

    ctx.textBaseline = "alphabetic";
    // ROW_ITERATOR and CELLS fill the handle their out pointer points at.
    v.setUint32(this.scratch, this.rows, true);
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "ROW_ITERATOR"), this.scratch);
    let y = -1;
    while (x.ghostty_render_state_row_iterator_next(this.rows)) {
      y++;
      x.ghostty_render_state_row_get(this.rows, rowData("DIRTY"), this.scratch);
      const dirty = g.bytes[this.scratch] !== 0;
      if (!full && !dirty && y !== cy && y !== lastCursorRow) continue;
      v.setUint32(this.scratch, this.cells, true);
      x.ghostty_render_state_row_get(this.rows, rowData("CELLS"), this.scratch);
      const cells = this.cells;
      const ry = oy + y * h;
      const bgs: { x0: number; x1: number; c: RGB }[] = [];
      const decos: Deco[] = [];
      const row: Cell[] = [];
      const deco = (x0: number, x1: number, kind: number, c: RGB) => {
        const d = decos[decos.length - 1];
        if (d && d.x1 === x0 && d.kind === kind && same(d.c, c)) d.x1 = x1;
        else decos.push({ x0, x1, kind, c });
      };
      let col = -1;
      while (x.ghostty_render_state_row_cells_next(cells)) {
        col++;
        x.ghostty_render_state_row_cells_get(cells, cellData("RAW"), this.scratch);
        const kind = wideOf(v.getBigUint64(this.scratch, true));
        if (kind === SPACER_TAIL || kind === SPACER_HEAD) continue;
        const span = kind === WIDE ? 2 : 1;
        let cfg = fg;
        let cbg: RGB | undefined;
        if (x.ghostty_render_state_row_cells_get(cells, cellData("FG_COLOR"), this.scratch) === 0) cfg = g.rgb(this.scratch);
        if (x.ghostty_render_state_row_cells_get(cells, cellData("BG_COLOR"), this.scratch) === 0) cbg = g.rgb(this.scratch);
        let variant = 0, faint = false, invisible = false, underline = 0, strike = false, overline = false;
        let ul: RGB | undefined;
        x.ghostty_render_state_row_cells_get(cells, cellData("HAS_STYLING"), this.scratch);
        if (g.bytes[this.scratch]) {
          v.setUint32(styleOff("size"), g.size("GhosttyStyle"), true);
          x.ghostty_render_state_row_cells_get(cells, cellData("STYLE"), this.style);
          const b = g.bytes;
          if (b[styleOff("bold")]) variant |= 1;
          if (b[styleOff("italic")]) variant |= 2;
          faint = b[styleOff("faint")] !== 0;
          strike = b[styleOff("strikethrough")] !== 0;
          overline = b[styleOff("overline")] !== 0;
          invisible = b[styleOff("invisible")] !== 0;
          underline = v.getInt32(styleOff("underline"), true);
          ul = this.styleColor(styleOff("underline_color"), palette);
          if (b[styleOff("inverse")]) {
            const was = cfg;
            cfg = cbg ?? bg;
            cbg = was;
          }
        }
        if (cbg && !same(cbg, bg)) {
          const last = bgs[bgs.length - 1];
          if (last && last.x1 === col && same(last.c, cbg)) last.x1 = col + span;
          else bgs.push({ x0: col, x1: col + span, c: cbg });
        }
        x.ghostty_render_state_row_cells_get(cells, cellData("GRAPHEMES_LEN"), this.scratch);
        const n = v.getUint32(this.scratch, true);
        let text = "";
        let cp = 0;
        if (n > 0 && !invisible) {
          if (n > this.graphemesCap) {
            if (this.graphemes) g.free(this.graphemes, this.graphemesCap * 4);
            this.graphemesCap = Math.max(n, 8);
            this.graphemes = g.alloc(this.graphemesCap * 4);
          }
          x.ghostty_render_state_row_cells_get(cells, cellData("GRAPHEMES_BUF"), this.graphemes);
          cp = v.getUint32(this.graphemes, true);
          for (let i = 0; i < n; i++) text += String.fromCodePoint(v.getUint32(this.graphemes + i * 4, true));
        }
        const tfg = noMinContrast(cp) ? cfg : this.contrasted(cfg, cbg ?? bg);
        if (underline) deco(col, col + span, underline, ul ?? tfg);
        if (strike) deco(col, col + span, decoStrike, tfg);
        if (overline) deco(col, col + span, decoOverline, tfg);
        if (text && cp !== 0x20) row.push({ col, span, text, cp, fg: tfg, variant, faint });
      }

      ctx.save();
      ctx.beginPath();
      ctx.rect(ox, ry, cols * w, h);
      ctx.clip();
      ctx.fillStyle = css(bg);
      ctx.fillRect(ox, ry, cols * w, h);
      for (const b of bgs) {
        ctx.fillStyle = css(b.c);
        ctx.fillRect(ox + b.x0 * w, ry, (b.x1 - b.x0) * w, h);
      }
      // The cursor's cell: twice as wide on a wide character.
      const under = y === cy ? row.find((c) => c.col === cx) : undefined;
      const cw = under?.span === 2 ? 2 * w : w;
      if (block && y === cy) {
        ctx.fillStyle = css(cursorColor);
        ctx.fillRect(ox + cx * w, ry, cw, h);
      }
      this.text(ctx, font, row, ox, ry);
      for (const d of decos) drawDeco(ctx, font, d, ox, ry);
      if (showCursor && y === cy) {
        ctx.fillStyle = css(cursorColor);
        const px = Math.max(Math.round(1.5 * font.scale), 1);
        if (cursorStyle === st("BAR")) ctx.fillRect(ox + cx * w, ry, px, h);
        else if (cursorStyle === st("UNDERLINE")) ctx.fillRect(ox + cx * w, ry + h - px, cw, px);
        else if (cursorStyle === st("BLOCK_HOLLOW")) {
          const lw = Math.max(1, Math.round(font.scale));
          ctx.strokeStyle = css(cursorColor);
          ctx.lineWidth = lw;
          ctx.strokeRect(ox + cx * w + lw / 2, ry + lw / 2, cw - lw, h - lw);
        } else if (under) {
          // The text under a block cursor, in the cursor's text colour: the background.
          ctx.beginPath();
          ctx.rect(ox + cx * w, ry, cw, h);
          ctx.clip();
          this.text(ctx, font, [{ ...under, fg: bg, faint: false }], ox, ry);
        }
      }
      ctx.restore();
      // Drawn: the row is clean until it changes again.
      g.bytes[this.scratch] = 0;
      x.ghostty_render_state_row_set(this.rows, 0, this.scratch);
    }
    g.bytes[this.scratch] = 0;
    x.ghostty_render_state_clean(this.rs);
  }

  /**
   * Draws a row's text: runs of narrow characters in one style in one call,
   * stretched from the font's advance to the cells' width (a few percent at
   * most), so the font's contextual alternates (its ligatures) apply as on
   * the desktop; letter spacing would turn them off. Every other character centred in
   * its cells, smaller when too wide, as emoji are; drawn characters as
   * shapes.
   */
  private text(ctx: CanvasRenderingContext2D, font: Font, row: Cell[], ox: number, ry: number) {
    const { w, h } = font;
    const base = ry + font.baseline;
    const stretch = w / font.adv;
    let i = 0;
    while (i < row.length) {
      const c = row[i];
      ctx.fillStyle = css(c.fg);
      ctx.globalAlpha = c.faint ? 0.5 : 1;
      if (drawn(c.cp)) {
        drawBox(ctx, c.cp, ox + c.col * w, ry, w * c.span, h, font.scale);
        i++;
        continue;
      }
      ctx.font = font.css(c.variant);
      if (simple(c)) {
        let j = i + 1;
        let text = c.text;
        while (j < row.length) {
          const d = row[j];
          if (!simple(d) || d.variant !== c.variant || d.faint !== c.faint || !same(d.fg, c.fg) || drawn(d.cp)) break;
          // Spaces between draw nothing; they join the run.
          text += " ".repeat(d.col - row[j - 1].col - 1) + d.text;
          j++;
        }
        ctx.save();
        ctx.translate(ox + c.col * w, base);
        ctx.scale(stretch, 1);
        ctx.fillText(text, 0, 0);
        ctx.restore();
        i = j;
        continue;
      }
      const room = c.span * w;
      let tw = font.measure(c.text, c.variant);
      let dy = 0;
      const fit = room / tw;
      if (fit < 0.95 && tw > 0) {
        // Too wide: the same text, smaller, toward the middle of the row.
        const k = Math.max(fit, 0.5);
        ctx.font = font.css(c.variant, k);
        tw *= k;
        dy = -(1 - k) * (font.baseline - h / 2);
      }
      let off = (room - tw) / 2;
      if (Math.abs(off) < 0.5) off = 0;
      ctx.fillText(c.text, ox + c.col * w + off, base + dy);
      i++;
    }
    ctx.globalAlpha = 1;
  }

  /**
   * The bytes a key press sends the program, encoded for the modes it set
   * (cursor keys, the Kitty keyboard protocol, modifyOtherKeys...), or
   * undefined for a key that sends nothing.
   */
  encodeKey(e: KeyboardEvent): Uint8Array | undefined {
    const { g } = this;
    const x = g.x;
    x.ghostty_key_encoder_setopt_from_terminal(this.encoder, this.t);
    const ev = g.make("key event", (s) => x.ghostty_key_event_new(0, s));
    let text = 0, textLen = 0;
    try {
      x.ghostty_key_event_set_action(ev, g.e("GhosttyKeyAction", e.repeat ? "REPEAT" : "PRESS"));
      x.ghostty_key_event_set_key(ev, keyCode(g, e.code));
      let mods = 0;
      if (e.shiftKey) mods |= Mods.shift;
      if (e.ctrlKey) mods |= Mods.ctrl;
      if (e.altKey) mods |= Mods.alt;
      if (e.metaKey) mods |= Mods.super;
      x.ghostty_key_event_set_mods(ev, mods);
      // The text the key types: one character, the shift it used up.
      if ([...e.key].length === 1) {
        const b = new TextEncoder().encode(e.key);
        textLen = b.length;
        text = g.put(b);
        x.ghostty_key_event_set_utf8(ev, text, textLen);
        if (e.shiftKey && e.key.toLowerCase() !== e.key) x.ghostty_key_event_set_consumed_mods(ev, Mods.shift);
      }
      const unshifted = unshiftedOf(e);
      if (unshifted) x.ghostty_key_event_set_unshifted_codepoint(ev, unshifted);
      const cap = 128;
      const out = g.alloc(cap);
      try {
        if (x.ghostty_key_encoder_encode(this.encoder, ev, out, cap, this.scratch) !== 0) return undefined;
        const n = g.view.getUint32(this.scratch, true);
        return n > 0 ? g.bytes.slice(out, out + n) : undefined;
      } finally {
        g.free(out, cap);
      }
    } finally {
      if (text) g.free(text, textLen);
      x.ghostty_key_event_free(ev);
    }
  }
}

/** Ghostty's key for a KeyboardEvent.code, as the manifest names it: KeyA is A, ArrowUp ARROW_UP, Numpad1 NUMPAD_1. */
function keyCode(g: Ghostty, code: string): number {
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
function unshiftedOf(e: KeyboardEvent): number {
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
function drawDeco(ctx: CanvasRenderingContext2D, font: Font, d: Deco, ox: number, ry: number) {
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
