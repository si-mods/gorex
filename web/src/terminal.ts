// One terminal: a libghostty-vt terminal restored from the snapshot a
// stream starts with, fed the program's output, drawn on a canvas from its
// render state, and encoding the keys typed for the program's modes.
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

const css = (c: RGB) => `rgb(${c[0]},${c[1]},${c[2]})`;

export class Terminal {
  private rs: number;
  private readonly rows: number;
  private readonly cells: number;
  private readonly encoder: number;
  /** Scratch for the get calls: a style, colours, the cursor, a u64, graphemes. */
  private style: number;
  private colors: number;
  private cursor: number;
  private scratch: number;
  private graphemes = 0;
  private graphemesCap = 0;
  private freed = false;

  private constructor(private g: Ghostty, private t: number) {
    const x = g.x;
    this.rs = g.make("render state", (s) => x.ghostty_render_state_new(0, s));
    this.rows = g.make("row iterator", (s) => x.ghostty_render_state_row_iterator_new(0, s));
    this.cells = g.make("row cells", (s) => x.ghostty_render_state_row_cells_new(0, s));
    this.encoder = g.make("key encoder", (s) => x.ghostty_key_encoder_new(0, s));
    this.style = g.sized("GhosttyStyle");
    this.colors = g.sized("GhosttyRenderStateColors");
    this.cursor = g.sized("GhosttyRenderStateCursor");
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

  /** The frame's default background, for the page around the canvas. */
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

  /**
   * Draws what changed since the last draw, or everything when full: each
   * dirty row's backgrounds, then its text, then the cursor.
   */
  draw(ctx: CanvasRenderingContext2D, font: Font, full: boolean, focused: boolean) {
    const { g } = this;
    const x = g.x;
    if (!this.update()) return;
    const v = g.view;
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "DIRTY"), this.scratch);
    if (v.getInt32(this.scratch, true) === g.e("GhosttyRenderStateDirty", "FULL")) full = true;
    const co = (f: string) => this.colors + g.off("GhosttyRenderStateColors", f);
    const bg = g.rgb(co("background"));
    const fg = g.rgb(co("foreground"));
    const cursorColor = g.bytes[co("cursor_has_value")] ? g.rgb(co("cursor")) : fg;
    const cu = (f: string) => this.cursor + g.off("GhosttyRenderStateCursor", f);
    const cursorIn = g.bytes[cu("viewport_has_value")] !== 0 && g.bytes[cu("visible")] !== 0;
    const cx = v.getUint16(cu("viewport_x"), true);
    const cy = v.getUint16(cu("viewport_y"), true);
    const cursorStyle = v.getInt32(cu("visual_style"), true);
    const { cols } = this.size();
    const { w, h } = font;

    const rowData = (d: string) => g.e("GhosttyRenderStateRowData", d);
    const cellData = (d: string) => g.e("GhosttyRenderStateRowCellsData", d);
    const styleOff = (f: string) => this.style + g.off("GhosttyStyle", f);
    const wide = g.cellBits("wide");
    const wideOf = (raw: bigint) => Number((raw >> BigInt(wide.lsb)) & BigInt((1 << wide.width) - 1));
    const WIDE = g.e("GhosttyCellWide", "WIDE");
    const SPACER_TAIL = g.e("GhosttyCellWide", "SPACER_TAIL");

    // ROW_ITERATOR and CELLS fill the handle their out pointer points at.
    v.setUint32(this.scratch, this.rows, true);
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "ROW_ITERATOR"), this.scratch);
    let y = -1;
    while (x.ghostty_render_state_row_iterator_next(this.rows)) {
      y++;
      x.ghostty_render_state_row_get(this.rows, rowData("DIRTY"), this.scratch);
      const dirty = g.bytes[this.scratch] !== 0;
      if (!full && !dirty && !(cursorIn && y === cy)) continue;
      g.view.setUint32(this.scratch, this.cells, true);
      x.ghostty_render_state_row_get(this.rows, rowData("CELLS"), this.scratch);
      const cells = this.cells;
      ctx.fillStyle = css(bg);
      ctx.fillRect(0, y * h, cols * w, h);
      const texts: { x: number; text: string; color: RGB; bold: boolean; italic: boolean; faint: boolean; underline: boolean; strike: boolean; span: number }[] = [];
      let col = -1;
      while (x.ghostty_render_state_row_cells_next(cells)) {
        col++;
        x.ghostty_render_state_row_cells_get(cells, cellData("RAW"), this.scratch);
        const kind = wideOf(this.g.view.getBigUint64(this.scratch, true));
        if (kind === SPACER_TAIL) continue;
        const span = kind === WIDE ? 2 : 1;
        let cfg = fg;
        let cbg: RGB | undefined;
        if (x.ghostty_render_state_row_cells_get(cells, cellData("FG_COLOR"), this.scratch) === 0) cfg = g.rgb(this.scratch);
        if (x.ghostty_render_state_row_cells_get(cells, cellData("BG_COLOR"), this.scratch) === 0) cbg = g.rgb(this.scratch);
        let bold = false, italic = false, faint = false, underline = false, strike = false, invisible = false;
        x.ghostty_render_state_row_cells_get(cells, cellData("HAS_STYLING"), this.scratch);
        if (g.bytes[this.scratch]) {
          g.view.setUint32(this.style + g.off("GhosttyStyle", "size"), g.size("GhosttyStyle"), true);
          x.ghostty_render_state_row_cells_get(cells, cellData("STYLE"), this.style);
          const b = g.bytes;
          bold = b[styleOff("bold")] !== 0;
          italic = b[styleOff("italic")] !== 0;
          faint = b[styleOff("faint")] !== 0;
          strike = b[styleOff("strikethrough")] !== 0;
          invisible = b[styleOff("invisible")] !== 0;
          underline = g.view.getInt32(styleOff("underline"), true) !== 0;
          if (b[styleOff("inverse")]) {
            const was = cfg;
            cfg = cbg ?? bg;
            cbg = was;
          }
        }
        if (cbg) {
          ctx.fillStyle = css(cbg);
          ctx.fillRect(col * w, y * h, span * w, h);
        }
        x.ghostty_render_state_row_cells_get(cells, cellData("GRAPHEMES_LEN"), this.scratch);
        const n = g.view.getUint32(this.scratch, true);
        if (n > 0 && !invisible) {
          if (n > this.graphemesCap) {
            if (this.graphemes) g.free(this.graphemes, this.graphemesCap * 4);
            this.graphemesCap = Math.max(n, 8);
            this.graphemes = g.alloc(this.graphemesCap * 4);
          }
          x.ghostty_render_state_row_cells_get(cells, cellData("GRAPHEMES_BUF"), this.graphemes);
          let text = "";
          for (let i = 0; i < n; i++) text += String.fromCodePoint(g.view.getUint32(this.graphemes + i * 4, true));
          texts.push({ x: col, text, color: cfg, bold, italic, faint, underline, strike, span });
        }
      }
      if (cursorIn && y === cy) {
        ctx.fillStyle = css(cursorColor);
        const st = (s: string) => g.e("GhosttyRenderStateCursorVisualStyle", s);
        if (!focused || cursorStyle === st("BLOCK_HOLLOW")) {
          ctx.strokeStyle = css(cursorColor);
          ctx.lineWidth = Math.max(1, Math.round(font.scale));
          ctx.strokeRect(cx * w + 0.5, y * h + 0.5, w - 1, h - 1);
        } else if (cursorStyle === st("BAR")) {
          ctx.fillRect(cx * w, y * h, Math.max(2, Math.round(font.scale * 2)), h);
        } else if (cursorStyle === st("UNDERLINE")) {
          ctx.fillRect(cx * w, (y + 1) * h - Math.max(2, font.scale * 2), w, Math.max(2, font.scale * 2));
        } else {
          ctx.fillRect(cx * w, y * h, w, h);
        }
      }
      ctx.textBaseline = "alphabetic";
      for (const t of texts) {
        const blockCursor = focused && cursorIn && y === cy && t.x === cx && cursorStyle === g.e("GhosttyRenderStateCursorVisualStyle", "BLOCK");
        ctx.font = font.css(t.bold, t.italic);
        ctx.fillStyle = css(blockCursor ? bg : t.color);
        ctx.globalAlpha = t.faint ? 0.5 : 1;
        ctx.save();
        ctx.beginPath();
        ctx.rect(t.x * w, y * h, t.span * w, h);
        ctx.clip();
        ctx.fillText(t.text, t.x * w, y * h + font.baseline);
        ctx.restore();
        ctx.globalAlpha = 1;
        if (t.underline) ctx.fillRect(t.x * w, y * h + font.baseline + font.scale, t.span * w, Math.max(1, font.scale));
        if (t.strike) ctx.fillRect(t.x * w, y * h + Math.round(h / 2), t.span * w, Math.max(1, font.scale));
      }
      // Drawn: the row is clean until it changes again.
      g.bytes[this.scratch] = 0;
      x.ghostty_render_state_row_set(this.rows, 0, this.scratch);
    }
    g.bytes[this.scratch] = 0;
    x.ghostty_render_state_clean(this.rs);
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

/** The terminal's font, at the device's pixel ratio. */
export class Font {
  w = 0;
  h = 0;
  baseline = 0;
  constructor(readonly family: string, readonly px: number, readonly scale: number) {
    const c = document.createElement("canvas").getContext("2d")!;
    c.font = this.css(false, false);
    const m = c.measureText("M");
    this.w = Math.ceil(m.width);
    this.h = Math.ceil(px * scale * 1.25);
    const ascent = m.fontBoundingBoxAscent ?? px * scale * 0.8;
    const descent = m.fontBoundingBoxDescent ?? px * scale * 0.2;
    this.baseline = Math.round((this.h - (ascent + descent)) / 2 + ascent);
  }

  css(bold: boolean, italic: boolean): string {
    return `${italic ? "italic " : ""}${bold ? "bold " : ""}${this.px * this.scale}px ${this.family}`;
  }
}
