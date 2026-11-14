// One terminal: a libghostty-vt terminal restored from the snapshot a
// stream starts with, fed the program's output, drawn on a canvas from its
// render state, and encoding the keys typed for the program's modes.
import { drawBox, drawn, noMinContrast } from "./box";
import { Ghostty, Mods, RGB } from "./ghostty";
import { Link, linkAt, RowText } from "./links";
import { ready, textReady } from "./maple";
import { Deco, decoOverline, decoStrike, drawDeco, Font, keyCode, unshiftedOf } from "./termfont";
import { contrasted, css, desktopTheme, hex, minimumContrast, same, Theme, themeOf } from "./termtheme";

export { contrasted, desktopTheme, Font, hex, minimumContrast, Theme, themeOf };

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
  /** The least contrast of text with its background (the Minimum contrast setting), 1 for none. */
  minContrast: number;
  /** The selection's colours (Theme.selection, selectionText); none inverts. */
  selection?: RGB;
  selectionText?: RGB;
  /** The link the modifier is held over (pointer.ts), underlined. */
  link?: { row: number; x0: number; x1: number };
  /** Search matches visible in the viewport (find.ts). */
  highlights?: { row: number; col: number; len: number; current: boolean }[];
}

/** A pointer over the grid: the viewport cell under it, clamped to the grid, and where it is on the canvas, in device pixels. */
export interface Pointer {
  col: number;
  row: number;
  x: number;
  y: number;
}

/** What a selection's drag needs of the canvas, as GhosttySelectionGestureGeometry: in device pixels. */
export interface Geometry {
  cols: number;
  cellW: number;
  padLeft: number;
  height: number;
}

/** Whether a drag past an edge wants the viewport to scroll, and which way. */
export type Autoscroll = "none" | "up" | "down";

/** The scratch of the selection calls: a surface position, a geometry, two u64s, a f64, a coordinate, a point, an out value. */
const sx = { pos: 0, geo: 16, time: 32, interval: 40, distance: 48, coord: 56, point: 64, out: 88, mpos: 96, flag: 104, size: 112 };

/** What the mouse encoder needs of the canvas, as GhosttyMouseEncoderSize: in device pixels. */
export interface MouseSize {
  width: number;
  height: number;
  cellW: number;
  cellH: number;
  padTop: number;
  padBottom: number;
  padLeft: number;
  padRight: number;
}

/**
 * A mouse event for the program: an action, a button (Ghostty's: 1 left,
 * 2 right, 3 middle, 4 and 5 the wheel; 0 none), modifiers (Mods) and
 * where on the canvas, in device pixels.
 */
export interface MouseReport {
  action: "PRESS" | "RELEASE" | "MOTION";
  button: number;
  mods: number;
  x: number;
  y: number;
  /** Whether a button is held, this event's own too. */
  held: boolean;
}
/** Room for one encoded mouse event. */
const mouseCap = 64;
/** Ghostty's click-repeat-interval: presses this close make a double or a triple click. */
const clickInterval = 500;

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
  /** The ratio the contrasts were corrected to. */
  private ratio = minimumContrast;
  /** Whether the cursor blinks, as of the last draw. */
  blinking = false;
  /** Selecting: Ghostty's gesture and its events, a grid ref, two selections, a row's range and scratch. */
  private gesture: number;
  private ev: { press: number; release: number; drag: number; tick: number };
  private ref: number;
  private sel: number;
  private sel2: number;
  private rowSel: number;
  private sx: number;
  /** Reporting the mouse: Ghostty's encoder, an event, its size and the bytes it writes. */
  private mouseEnc: number;
  private mouseEv: number;
  private mouseSize: number;
  private mouseOut: number;
  /** A hyperlink's URI, read into a buffer grown as one needs. */
  private uri = 0;
  private uriCap = 0;

  private constructor(private g: Ghostty, private t: number) {
    const x = g.x;
    this.rs = g.make("render state", (s) => x.ghostty_render_state_new(0, s));
    this.rows = g.make("row iterator", (s) => x.ghostty_render_state_row_iterator_new(0, s));
    this.cells = g.make("row cells", (s) => x.ghostty_render_state_row_cells_new(0, s));
    this.encoder = g.make("key encoder", (s) => x.ghostty_key_encoder_new(0, s));
    this.style = g.zeroed("GhosttyStyle");
    this.colors = g.sized("GhosttyRenderStateColors");
    this.cursor = g.sized("GhosttyRenderStateCursor");
    this.scroll = g.alloc(g.size("GhosttyTerminalScrollViewport"));
    this.scratch = g.alloc(16);
    this.gesture = g.make("selection gesture", (s) => x.ghostty_selection_gesture_new(0, s));
    const event = (type: string) =>
      g.make("selection event", (s) => x.ghostty_selection_gesture_event_new(0, s, g.e("GhosttySelectionGestureEventType", type)));
    this.ev = { press: event("PRESS"), release: event("RELEASE"), drag: event("DRAG"), tick: event("AUTOSCROLL_TICK") };
    this.ref = g.zeroed("GhosttyGridRef");
    this.sel = g.zeroed("GhosttySelection");
    this.sel2 = g.zeroed("GhosttySelection");
    this.rowSel = g.sized("GhosttyRenderStateRowSelection");
    this.sx = g.alloc(sx.size);
    g.view.setBigUint64(this.sx + sx.interval, BigInt(clickInterval * 1e6), true);
    this.opt(this.ev.press, "REPEAT_INTERVAL_NS", this.sx + sx.interval);
    this.mouseEnc = g.make("mouse encoder", (s) => x.ghostty_mouse_encoder_new(0, s));
    this.mouseEv = g.make("mouse event", (s) => x.ghostty_mouse_event_new(0, s));
    this.mouseSize = g.sized("GhosttyMouseEncoderSize");
    this.mouseOut = g.alloc(mouseCap);
    // Motion is reported once a cell, not once a pixel.
    g.bytes[this.sx + sx.flag] = 1;
    x.ghostty_mouse_encoder_set(this.mouseEnc, g.e("GhosttyMouseEncoderOption", "TRACK_LAST_CELL"), this.sx + sx.flag);
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
    g.x.ghostty_selection_gesture_free(this.gesture, this.t);
    for (const e of Object.values(this.ev)) g.x.ghostty_selection_gesture_event_free(e);
    g.free(this.ref, g.size("GhosttyGridRef"));
    g.free(this.sel, g.size("GhosttySelection"));
    g.free(this.sel2, g.size("GhosttySelection"));
    g.free(this.rowSel, g.size("GhosttyRenderStateRowSelection"));
    g.free(this.sx, sx.size);
    g.x.ghostty_mouse_event_free(this.mouseEv);
    g.x.ghostty_mouse_encoder_free(this.mouseEnc);
    g.free(this.mouseSize, g.size("GhosttyMouseEncoderSize"));
    g.free(this.mouseOut, mouseCap);
    if (this.uri) g.free(this.uri, this.uriCap);
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

  /** The terminal's width in cells, as of now: the render state's (size) is as of the last draw. A u16, read as 4 bytes zeroed first. */
  private columns(): number {
    return this.datum("COLS", 4) & 0xffff;
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

  /** The cursor's cell in the viewport as of the last draw, whether it shows or not; none while it is scrolled out. */
  cursorCell(): { x: number; y: number } | undefined {
    const { g } = this;
    const cu = (k: string) => this.cursor + g.off("GhosttyRenderStateCursor", k);
    if (!g.bytes[cu("viewport_has_value")]) return undefined;
    const x = g.view.getUint16(cu("viewport_x"), true);
    return { x: g.bytes[cu("wide_tail")] && x > 0 ? x - 1 : x, y: g.view.getUint16(cu("viewport_y"), true) };
  }

  /** A terminal mode (DEC private unless ansi), as the program set it. */
  mode(value: number, ansi = false): boolean {
    const { g } = this;
    const t = "GhosttyTerminalModeConfig";
    const p = this.sx + sx.out;
    g.bytes.fill(0, p, p + 8);
    g.view.setUint16(p + g.off(t, "mode"), (value & 0x7fff) | (ansi ? 0x8000 : 0), true);
    if (g.x.ghostty_terminal_get(this.t, g.e("GhosttyTerminalData", "MODE"), p) !== 0) return false;
    return g.bytes[p + g.off(t, "value")] !== 0;
  }

  /**
   * The bytes a mouse event sends the program, as its mouse modes ask
   * (X10 9, normal 1000, button 1002, any 1003) in its format (X10, UTF-8
   * 1005, SGR 1006, urxvt 1015, SGR pixels 1016), by Ghostty's encoder:
   * none for an event the modes leave out, for motion within the cell
   * reported last, or outside the grid with no button held.
   */
  encodeMouse(m: MouseReport, size: MouseSize): Uint8Array | undefined {
    const { g } = this;
    const x = g.x;
    const enc = this.mouseEnc;
    const opt = (n: string, p: number) => x.ghostty_mouse_encoder_set(enc, g.e("GhosttyMouseEncoderOption", n), p);
    x.ghostty_mouse_encoder_set_from_terminal(enc, this.t);
    const t = "GhosttyMouseEncoderSize";
    const s = this.mouseSize;
    const field = (k: string, n: number) => g.view.setUint32(s + g.off(t, k), Math.max(0, Math.round(n)), true);
    g.view.setUint32(s + g.off(t, "size"), g.size(t), true);
    field("screen_width", size.width);
    field("screen_height", size.height);
    field("cell_width", Math.max(1, size.cellW));
    field("cell_height", Math.max(1, size.cellH));
    field("padding_top", size.padTop);
    field("padding_bottom", size.padBottom);
    field("padding_left", size.padLeft);
    field("padding_right", size.padRight);
    opt("SIZE", s);
    g.bytes[this.sx + sx.flag] = m.held ? 1 : 0;
    opt("ANY_BUTTON_PRESSED", this.sx + sx.flag);
    const ev = this.mouseEv;
    x.ghostty_mouse_event_set_action(ev, g.e("GhosttyMouseAction", m.action));
    if (m.button) x.ghostty_mouse_event_set_button(ev, m.button);
    else x.ghostty_mouse_event_clear_button(ev);
    x.ghostty_mouse_event_set_mods(ev, m.mods);
    const pos = this.sx + sx.mpos;
    g.view.setFloat32(pos + g.off("GhosttyMousePosition", "x"), m.x, true);
    g.view.setFloat32(pos + g.off("GhosttyMousePosition", "y"), m.y, true);
    x.ghostty_mouse_event_set_position(ev, pos);
    const out = this.sx + sx.out;
    g.view.setUint32(out, 0, true);
    if (x.ghostty_mouse_encoder_encode(enc, ev, this.mouseOut, mouseCap, out) !== 0) return undefined;
    const n = g.view.getUint32(out, true);
    return n > 0 ? g.bytes.slice(this.mouseOut, this.mouseOut + n) : undefined;
  }

  /** Forgets the cell motion was reported in last, as when the pointer leaves. */
  resetMouse() {
    this.g.x.ghostty_mouse_encoder_reset(this.mouseEnc);
  }

  // Selecting, with libghostty-vt's own selection (selection.h): its
  // gesture turns presses and drags into a selection by cell, word or line,
  // as Ghostty's mouse does, and scrolls on past an edge; the terminal
  // keeps the selection, tracked, so that it follows the text as output
  // comes and the scrollback moves, and clears it as Ghostty does.

  private opt(ev: number, name: string, p: number) {
    this.g.x.ghostty_selection_gesture_event_set(ev, this.g.e("GhosttySelectionGestureEventOption", name), p);
  }

  /** Points this.ref at a viewport cell; false when there is none. */
  private refAt(col: number, row: number): boolean {
    const { g } = this;
    const pt = this.sx + sx.point;
    g.bytes.fill(0, pt, pt + g.size("GhosttyPoint"));
    g.view.setInt32(pt + g.off("GhosttyPoint", "tag"), g.e("GhosttyPointTag", "VIEWPORT"), true);
    const c = pt + g.off("GhosttyPoint", "value");
    g.view.setUint16(c + g.off("GhosttyPointCoordinate", "x"), col, true);
    g.view.setUint32(c + g.off("GhosttyPointCoordinate", "y"), row, true);
    return g.x.ghostty_terminal_grid_ref(this.t, pt, this.ref) === 0;
  }

  /** Writes a pointer's position and the geometry into the scratch, and sets them on an event. */
  private placed(ev: number, p: Pointer, geo?: Geometry) {
    const { g } = this;
    const pos = this.sx + sx.pos;
    g.view.setFloat64(pos + g.off("GhosttySurfacePosition", "x"), p.x, true);
    g.view.setFloat64(pos + g.off("GhosttySurfacePosition", "y"), p.y, true);
    this.opt(ev, "POSITION", pos);
    if (!geo) return;
    const gp = this.sx + sx.geo;
    const field = (k: string, n: number) => g.view.setUint32(gp + g.off("GhosttySelectionGestureGeometry", k), Math.max(0, Math.round(n)), true);
    field("columns", geo.cols);
    field("cell_width", geo.cellW);
    field("padding_left", geo.padLeft);
    field("screen_height", geo.height);
    this.opt(ev, "GEOMETRY", gp);
  }

  /** Runs a gesture event; on success this.sel holds the selection it makes. */
  private run(ev: number, out = true): number {
    const { g } = this;
    return g.x.ghostty_selection_gesture_event(this.gesture, this.t, ev, out ? this.sel : 0);
  }

  /** Sets the terminal's selection to p, or clears it (0). */
  private select(p: number) {
    this.g.x.ghostty_terminal_set(this.t, this.g.e("GhosttyTerminalOption", "SELECTION"), p);
  }

  /** A gesture's datum: the click count (a byte) or the autoscroll (an enum). */
  private gestureDatum(name: "CLICK_COUNT" | "AUTOSCROLL"): number {
    const { g } = this;
    const out = this.sx + sx.out;
    g.view.setUint32(out, 0, true);
    if (g.x.ghostty_selection_gesture_get(this.gesture, this.t, g.e("GhosttySelectionGestureData", name), out) !== 0) return 0;
    return name === "CLICK_COUNT" ? g.bytes[out] : g.view.getInt32(out, true);
  }

  private autoscroll(): Autoscroll {
    const a = this.gestureDatum("AUTOSCROLL");
    const e = (n: string) => this.g.e("GhosttySelectionGestureAutoscroll", n);
    return a === e("UP") ? "up" : a === e("DOWN") ? "down" : "none";
  }

  /**
   * A press of the pointer, at a time in milliseconds, which Ghostty counts
   * into a double or triple click when close to the last in time and in
   * distance (pixels). A single press clears the selection; a double
   * selects the word, a triple the line.
   */
  press(p: Pointer, timeMs: number, distance: number) {
    const { g } = this;
    if (!this.refAt(p.col, p.row)) return;
    const ev = this.ev.press;
    this.opt(ev, "REF", this.ref);
    this.placed(ev, p);
    g.view.setBigUint64(this.sx + sx.time, BigInt(Math.max(0, Math.round(timeMs * 1e6))), true);
    this.opt(ev, "TIME_NS", this.sx + sx.time);
    g.view.setFloat64(this.sx + sx.distance, distance, true);
    this.opt(ev, "REPEAT_DISTANCE", this.sx + sx.distance);
    const r = this.run(ev);
    if (r === 0) this.select(this.sel);
    else if (r === g.e("GhosttyResult", "NO_VALUE") && this.gestureDatum("CLICK_COUNT") === 1) this.select(0);
  }

  /** A long press, as iOS's: a double click that stays down, so that the word is selected and a drag extends it by words. */
  longPress(p: Pointer, timeMs: number, distance: number) {
    this.press(p, timeMs, distance);
    this.release(p);
    this.press(p, timeMs, distance);
  }

  /** A drag of the pressed pointer: the selection from the press to it, by the press's unit. Returns where it wants to scroll. */
  drag(p: Pointer, geo: Geometry): Autoscroll {
    if (!this.refAt(p.col, p.row)) return this.autoscroll();
    const ev = this.ev.drag;
    this.opt(ev, "REF", this.ref);
    this.placed(ev, p, geo);
    this.settle(this.run(ev));
    return this.autoscroll();
  }

  /** One step of the scroll a drag past an edge asks for: a row, the selection extended to the row then under the pointer. */
  autoscrollTick(p: Pointer, geo: Geometry): Autoscroll {
    const { g } = this;
    if (this.gestureDatum("CLICK_COUNT") === 0) return "none";
    const ev = this.ev.tick;
    const c = this.sx + sx.coord;
    g.view.setUint16(c + g.off("GhosttyPointCoordinate", "x"), p.col, true);
    g.view.setUint32(c + g.off("GhosttyPointCoordinate", "y"), p.row, true);
    this.opt(ev, "VIEWPORT", c);
    this.placed(ev, p, geo);
    const r = this.run(ev);
    if (this.gestureDatum("CLICK_COUNT") !== 0) this.settle(r);
    return this.autoscroll();
  }

  /** A drag's result, as Ghostty's Surface takes it: the selection, or none. */
  private settle(r: number) {
    if (r === 0) this.select(this.sel);
    else if (r === this.g.e("GhosttyResult", "NO_VALUE")) this.select(0);
  }

  /** The pointer let go, where it is if over the grid. The click count stays, for a double click. */
  release(p?: Pointer) {
    const ev = this.ev.release;
    this.opt(ev, "REF", p && this.refAt(p.col, p.row) ? this.ref : 0);
    this.run(ev, false);
  }

  /** Abandons the gesture, as when the pointer is lost: the next press is a single click. */
  resetGesture() {
    this.g.x.ghostty_selection_gesture_reset(this.gesture, this.t);
  }

  /** Reads the terminal's selection into p; false when there is none. */
  private current(p: number): boolean {
    const { g } = this;
    return g.x.ghostty_terminal_get(this.t, g.e("GhosttyTerminalData", "SELECTION"), p) === 0;
  }

  hasSelection(): boolean {
    return this.current(this.sel2);
  }

  clearSelection() {
    this.select(0);
  }

  /** The selected text as Ghostty copies it (Screen.selectionString): plain, wrapped lines joined, trailing spaces trimmed. */
  selectionText(): string | undefined {
    if (!this.current(this.sel2)) return undefined;
    return this.formatPlain(true, true);
  }

  /** Every physical row of scrollback + active screen as plain text lines for search. */
  screenLines(): string[] {
    const raw = this.formatPlain(false, false);
    return raw ? raw.split("\n") : [];
  }

  /** The screen row (scrollback and all) currently at viewport row 0. */
  viewportTop(): number {
    return (this.refAt(0, 0) && this.screenPoint(this.ref)?.y) || 0;
  }

  /** Scrolls the viewport so screenRow is visible (preferring the upper third). */
  scrollToRow(screenRow: number) {
    const top = this.viewportTop();
    const { rows } = this.size();
    if (screenRow >= top && screenRow < top + rows) return;
    const target = Math.max(0, screenRow - Math.floor(rows / 3));
    this.scrollBy(target - top);
  }

  private formatPlain(selectionOnly: boolean, unwrap: boolean): string | undefined {
    const { g } = this;
    const x = g.x;
    const f = g.make("formatter", (s) => x.ghostty_formatter_terminal_new(0, s, this.t));
    const out = this.sx + sx.out;
    try {
      const set = (opt: string, p: number) => x.ghostty_formatter_set(f, g.e("GhosttyFormatterOption", opt), p) === 0;
      g.view.setInt32(out, g.e("GhosttyFormatterFormat", "PLAIN"), true);
      if (!set("EMIT", out)) return undefined;
      g.bytes[out] = unwrap ? 1 : 0;
      if (!set("UNWRAP", out)) return undefined;
      g.bytes[out] = 1;
      if (!set("TRIM", out)) return undefined;
      if (selectionOnly && !set("SELECTION", this.sel2)) return undefined;
      g.view.setUint32(out, 0, true);
      const r = x.ghostty_formatter_format_buf(f, 0, 0, out);
      if (r === 0) return "";
      if (r !== g.e("GhosttyResult", "OUT_OF_SPACE")) return undefined;
      const n = g.view.getUint32(out, true);
      const buf = g.alloc(n);
      try {
        if (x.ghostty_formatter_format_buf(f, buf, n, out) !== 0) return undefined;
        return new TextDecoder().decode(g.bytes.slice(buf, buf + g.view.getUint32(out, true)));
      } finally {
        g.free(buf, n);
      }
    } finally {
      x.ghostty_formatter_free(f);
    }
  }

  /** A grid ref's place in the screen (scrollback and all): column and row. */
  private screenPoint(ref: number): { x: number; y: number } | undefined {
    const { g } = this;
    const c = this.sx + sx.coord;
    if (g.x.ghostty_terminal_point_from_grid_ref(this.t, ref, g.e("GhosttyPointTag", "SCREEN"), c) !== 0) return undefined;
    return { x: g.view.getUint16(c + g.off("GhosttyPointCoordinate", "x"), true), y: g.view.getUint32(c + g.off("GhosttyPointCoordinate", "y"), true) };
  }

  /**
   * The selection's first and last cells, in viewport cells: a row above
   * the viewport is negative, one below it is rows or more. None without a
   * selection.
   */
  selectionEnds(): { start: { x: number; y: number }; end: { x: number; y: number } } | undefined {
    const { g } = this;
    if (!this.current(this.sel2)) return undefined;
    if (g.x.ghostty_terminal_selection_ordered(this.t, this.sel2, g.e("GhosttySelectionOrder", "FORWARD"), this.sel) !== 0) return undefined;
    const start = this.screenPoint(this.sel + g.off("GhosttySelection", "start"));
    const end = this.screenPoint(this.sel + g.off("GhosttySelection", "end"));
    const top = this.refAt(0, 0) ? this.screenPoint(this.ref) : undefined;
    if (!start || !end || !top) return undefined;
    return { start: { x: start.x, y: start.y - top.y }, end: { x: end.x, y: end.y - top.y } };
  }

  /**
   * Starts moving one end of the selection, as a handle does: the other end
   * becomes the selection's start, which stays, and the moving one its end.
   * False without a selection.
   */
  beginMoveEnd(first: boolean): boolean {
    const { g } = this;
    if (!this.current(this.sel2)) return false;
    const order = g.e("GhosttySelectionOrder", first ? "REVERSE" : "FORWARD");
    if (g.x.ghostty_terminal_selection_ordered(this.t, this.sel2, order, this.sel) !== 0) return false;
    this.select(this.sel);
    return true;
  }

  /** Moves the selection's end (beginMoveEnd's) to a viewport cell. */
  moveEnd(col: number, row: number) {
    const { g } = this;
    if (!this.current(this.sel2) || !this.refAt(col, row)) return;
    const end = this.sel2 + g.off("GhosttySelection", "end");
    g.bytes.copyWithin(end, this.ref, this.ref + g.size("GhosttyGridRef"));
    this.select(this.sel2);
  }

  // Links (links.ts), as the desktop's view reads them (vt's RowText and
  // Hyperlink): a row's text and a cell's hyperlink, through grid refs.

  /** The URL of the hyperlink (OSC 8) at a viewport cell, or "". */
  hyperlinkAt(col: number, row: number): string {
    const { g } = this;
    if (!this.refAt(col, row)) return "";
    const out = this.sx + sx.out;
    for (let tries = 0; tries < 2; tries++) {
      g.view.setUint32(out, 0, true);
      const r = g.x.ghostty_grid_ref_hyperlink_uri(this.ref, this.uri, this.uriCap, out);
      const n = g.view.getUint32(out, true);
      if (r === 0) return n > 0 ? new TextDecoder().decode(g.bytes.slice(this.uri, this.uri + n)) : "";
      if (r !== g.e("GhosttyResult", "OUT_OF_SPACE") || n === 0) return "";
      if (this.uri) g.free(this.uri, this.uriCap);
      this.uriCap = Math.max(n, 256);
      this.uri = g.alloc(this.uriCap);
    }
    return "";
  }

  /** A viewport row's text: each cell's characters with its column, a blank cell a space, the spacer of a wide one left out. */
  rowText(row: number): RowText {
    const { g } = this;
    const x = g.x;
    const wide = g.cellBits("wide");
    const SPACER_TAIL = g.e("GhosttyCellWide", "SPACER_TAIL");
    const SPACER_HEAD = g.e("GhosttyCellWide", "SPACER_HEAD");
    // Room for a cell's grapheme, as the desktop's: 16 code points.
    if (this.graphemesCap < 16) {
      if (this.graphemes) g.free(this.graphemes, this.graphemesCap * 4);
      this.graphemesCap = 16;
      this.graphemes = g.alloc(this.graphemesCap * 4);
    }
    const out = this.sx + sx.out;
    const text: string[] = [], cols: number[] = [];
    const n = this.columns();
    for (let col = 0; col < n; col++) {
      if (!this.refAt(col, row)) break;
      if (x.ghostty_grid_ref_cell(this.ref, this.scratch) === 0) {
        const kind = Number((g.view.getBigUint64(this.scratch, true) >> BigInt(wide.lsb)) & BigInt((1 << wide.width) - 1));
        if (kind === SPACER_TAIL || kind === SPACER_HEAD) continue;
      }
      g.view.setUint32(out, 0, true);
      const len = x.ghostty_grid_ref_graphemes(this.ref, this.graphemes, this.graphemesCap, out) === 0 ? g.view.getUint32(out, true) : 0;
      if (len === 0) {
        text.push(" ");
        cols.push(col);
        continue;
      }
      for (let i = 0; i < len; i++) {
        text.push(String.fromCodePoint(g.view.getUint32(this.graphemes + i * 4, true)));
        cols.push(col);
      }
    }
    return { text, cols };
  }

  /** The link at a viewport cell (links.ts linkAt), if any; with files, the host's file paths too. */
  linkAt(col: number, row: number, files = false): Link | undefined {
    return linkAt(
      {
        cols: this.columns(),
        rowText: (r) => this.rowText(r),
        hyperlinkAt: (c, r) => this.hyperlinkAt(c, r),
        // Scrolled back: the viewport's first row has rows above it.
        scrolledBack: () => (this.refAt(0, 0) && (this.screenPoint(this.ref)?.y ?? 0) > 0) || false,
      },
      col,
      row,
      files,
    );
  }

  private contrasted(fg: RGB, bg: RGB): RGB {
    const k = `${fg}|${bg}`;
    let c = this.contrasts.get(k);
    if (!c) {
      if (this.contrasts.size > 4096) this.contrasts.clear();
      c = contrasted(fg, bg, this.ratio);
      this.contrasts.set(k, c);
    }
    return c;
  }

  /** A style colour (underline_color) as RGB, or undefined for none. */
  private styleColor(p: number, palette: number): RGB | undefined {
    const { g } = this;
    const tag = g.bytes[p + g.off("GhosttyStyleColor", "tag")];
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
    let full = f.full;
    if (f.minContrast !== this.ratio) {
      this.ratio = f.minContrast;
      this.contrasts.clear();
      full = true;
    }
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "DIRTY"), this.scratch);
    if (g.view.getInt32(this.scratch, true) === g.e("GhosttyRenderStateDirty", "FULL")) full = true;
    const co = (k: string) => this.colors + g.off("GhosttyRenderStateColors", k);
    const bg = g.rgb(co("background"));
    const fg = g.rgb(co("foreground"));
    const palette = co("palette");
    const cursorColor = g.bytes[co("cursor_has_value")] ? g.rgb(co("cursor")) : fg;
    const cu = (k: string) => this.cursor + g.off("GhosttyRenderStateCursor", k);
    this.blinking = g.bytes[cu("blinking")] !== 0;
    const cursorIn = g.bytes[cu("viewport_has_value")] !== 0 && g.bytes[cu("visible")] !== 0;
    const showCursor = cursorIn && !(f.focused && this.blinking && f.blinkOff);
    let cx = g.view.getUint16(cu("viewport_x"), true);
    const cy = cursorIn ? g.view.getUint16(cu("viewport_y"), true) : -1;
    if (g.bytes[cu("wide_tail")] && cx > 0) cx--;
    const st = (s: string) => g.e("GhosttyCursorStyle", s);
    let cursorStyle = g.view.getInt32(cu("visual_style"), true);
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
    const flag = (k: string) => {
      const b = g.styleBits(k);
      return (g.view.getUint16(styleOff("flags"), true) >> b.lsb) & ((1 << b.width) - 1);
    };
    const wide = g.cellBits("wide");
    const wideOf = (raw: bigint) => Number((raw >> BigInt(wide.lsb)) & BigInt((1 << wide.width) - 1));
    const WIDE = g.e("GhosttyCellWide", "WIDE");
    const SPACER_TAIL = g.e("GhosttyCellWide", "SPACER_TAIL");
    const SPACER_HEAD = g.e("GhosttyCellWide", "SPACER_HEAD");

    ctx.textBaseline = "alphabetic";
    // ROW_ITERATOR and CELLS fill the handle their out pointer points at.
    g.view.setUint32(this.scratch, this.rows, true);
    x.ghostty_render_state_get(this.rs, g.e("GhosttyRenderStateData", "ROW_ITERATOR"), this.scratch);
    let y = -1;
    while (x.ghostty_render_state_row_iterator_next(this.rows)) {
      y++;
      x.ghostty_render_state_row_get(this.rows, rowData("DIRTY"), this.scratch);
      const dirty = g.bytes[this.scratch] !== 0;
      if (!full && !dirty && y !== cy && y !== lastCursorRow) continue;
      g.view.setUint32(this.scratch, this.cells, true);
      x.ghostty_render_state_row_get(this.rows, rowData("CELLS"), this.scratch);
      const cells = this.cells;
      const ry = oy + y * h;
      // The row's selected columns, sa to sb, as Ghostty's render state has them.
      let sa = -1, sb = -2;
      g.view.setUint32(this.rowSel + g.off("GhosttyRenderStateRowSelection", "size"), g.size("GhosttyRenderStateRowSelection"), true);
      if (x.ghostty_render_state_row_get(this.rows, rowData("SELECTION"), this.rowSel) === 0) {
        sa = g.view.getUint16(this.rowSel + g.off("GhosttyRenderStateRowSelection", "start_x"), true);
        sb = g.view.getUint16(this.rowSel + g.off("GhosttyRenderStateRowSelection", "end_x"), true);
      }
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
        const kind = wideOf(g.view.getBigUint64(this.scratch, true));
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
          x.ghostty_render_state_row_cells_get(cells, cellData("STYLE"), this.style);
          if (flag("bold")) variant |= 1;
          if (flag("italic")) variant |= 2;
          faint = flag("faint") !== 0;
          strike = flag("strikethrough") !== 0;
          overline = flag("overline") !== 0;
          invisible = flag("invisible") !== 0;
          underline = flag("underline");
          ul = this.styleColor(styleOff("underline_color"), palette);
          if (flag("inverse")) {
            const was = cfg;
            cfg = cbg ?? bg;
            cbg = was;
          }
        }
        // A selected cell: the theme's selection colours, else its own inverted, as Ghostty draws it.
        if (col <= sb && col + span - 1 >= sa) {
          if (f.selection) {
            cbg = f.selection;
            if (f.selectionText) cfg = f.selectionText;
          } else {
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
        const n = g.view.getUint32(this.scratch, true);
        let text = "";
        let cp = 0;
        if (n > 0 && !invisible) {
          if (n > this.graphemesCap) {
            if (this.graphemes) g.free(this.graphemes, this.graphemesCap * 4);
            this.graphemesCap = Math.max(n, 8);
            this.graphemes = g.alloc(this.graphemesCap * 4);
          }
          // GRAPHEMES_BUF fills a GhosttyCodepointBuffer, here in the scratch.
          const cb = (k: string) => this.scratch + g.off("GhosttyCodepointBuffer", k);
          g.view.setUint32(cb("ptr"), this.graphemes, true);
          g.view.setUint32(cb("cap"), this.graphemesCap, true);
          g.view.setUint32(cb("len"), 0, true);
          x.ghostty_render_state_row_cells_get(cells, cellData("GRAPHEMES_BUF"), this.scratch);
          cp = g.view.getUint32(this.graphemes, true);
          for (let i = 0; i < n; i++) text += String.fromCodePoint(g.view.getUint32(this.graphemes + i * 4, true));
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
      if (f.highlights) {
        for (const m of f.highlights) {
          if (m.row !== y) continue;
          ctx.fillStyle = m.current ? "rgba(255, 145, 0, 0.55)" : "rgba(255, 210, 60, 0.28)";
          ctx.fillRect(ox + m.col * w, ry, m.len * w, h);
        }
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
      // The link the modifier is held over: a single underline in its text's colour, as the desktop's linkColor.
      const link = f.link;
      if (link && link.row === y) {
        const c = row.find((c) => c.col >= link.x0 && c.col < link.x1)?.fg ?? fg;
        drawDeco(ctx, font, { x0: link.x0, x1: link.x1, kind: 1, c }, ox, ry);
      }
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
      x.ghostty_render_state_row_set(this.rows, g.e("GhosttyRenderStateRowOption", "DIRTY"), this.scratch);
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
      // A character whose CJK font chunk has not arrived draws nothing,
      // rather than in the system's font, until the chunk does (maple.ts).
      if (!textReady(c.text, c.variant)) {
        i++;
        continue;
      }
      ctx.font = font.css(c.variant);
      if (simple(c)) {
        let j = i + 1;
        let text = c.text;
        while (j < row.length) {
          const d = row[j];
          if (!simple(d) || d.variant !== c.variant || d.faint !== c.faint || !same(d.fg, c.fg) || drawn(d.cp) || !ready(d.cp, d.variant)) break;
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
    x.ghostty_key_encoder_set_from_terminal(this.encoder, this.t);
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
        // A GhosttyString, which wasm32 passes by its address.
        const s = this.sx + sx.out;
        g.view.setUint32(s + g.off("GhosttyString", "ptr"), text, true);
        g.view.setUint32(s + g.off("GhosttyString", "len"), textLen, true);
        x.ghostty_key_event_set_utf8(ev, s);
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
