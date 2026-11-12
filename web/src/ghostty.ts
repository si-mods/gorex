// libghostty-vt, compiled to WebAssembly (ghostty-vt.wasm), bound to the
// few calls the client makes. Like the Go binding (si-mods/mygo's
// plugins/terminal/internal/vt), it reads the library's type manifest
// (ghostty_type_json) as it loads, and refuses a module whose types lack
// what it uses: here every struct offset, enum value and packed-cell bit
// comes from the manifest itself, so the check is that each one exists,
// with the size this code reads it at.

import { t } from "./i18n";
import { countBody, LoadState, readBody } from "./progress";

type Field = { offset: number; size: number; type: string };
type TypeInfo = {
  kind: string;
  size: number;
  fields?: Record<string, Field>;
  values?: Record<string, number>;
  bits?: Record<string, any>;
};
type Manifest = { abi: { pointer_size: number; usize_size: number; endian: string }; types: Record<string, TypeInfo> };

/** The exports this binding calls. */
const functions = [
  "ghostty_wasm_alloc", "ghostty_wasm_free", "ghostty_wasm_alloc_opaque", "ghostty_wasm_free_opaque",
  "ghostty_wasm_take_opaque", "ghostty_type_json",
  "ghostty_terminal_free", "ghostty_terminal_vt_write", "ghostty_terminal_set",
  "ghostty_snapshot_decoder_new_buf", "ghostty_snapshot_decoder_decode", "ghostty_snapshot_decoder_free",
  "ghostty_render_state_new", "ghostty_render_state_update", "ghostty_render_state_get", "ghostty_render_state_clean",
  "ghostty_render_state_row_iterator_new", "ghostty_render_state_row_iterator_next", "ghostty_render_state_row_get",
  "ghostty_render_state_row_set", "ghostty_render_state_row_cells_new", "ghostty_render_state_row_cells_next",
  "ghostty_render_state_row_cells_get", "ghostty_terminal_resize",
  "ghostty_render_state_free", "ghostty_render_state_row_iterator_free", "ghostty_render_state_row_cells_free",
  "ghostty_key_encoder_new", "ghostty_key_encoder_free", "ghostty_key_encoder_setopt_from_terminal", "ghostty_key_encoder_encode",
  "ghostty_key_event_new", "ghostty_key_event_free", "ghostty_key_event_set_action", "ghostty_key_event_set_key",
  "ghostty_key_event_set_mods", "ghostty_key_event_set_consumed_mods", "ghostty_key_event_set_utf8",
  "ghostty_key_event_set_unshifted_codepoint", "ghostty_terminal_get", "ghostty_terminal_scroll_viewport",
  "ghostty_terminal_grid_ref", "ghostty_terminal_point_from_grid_ref", "ghostty_terminal_selection_format_buf",
  "ghostty_terminal_selection_ordered", "ghostty_selection_gesture_new", "ghostty_selection_gesture_free",
  "ghostty_selection_gesture_reset", "ghostty_selection_gesture_get", "ghostty_selection_gesture_event",
  "ghostty_selection_gesture_event_new", "ghostty_selection_gesture_event_free", "ghostty_selection_gesture_event_set",
  "ghostty_mouse_encoder_new", "ghostty_mouse_encoder_free", "ghostty_mouse_encoder_setopt",
  "ghostty_mouse_encoder_setopt_from_terminal", "ghostty_mouse_encoder_reset", "ghostty_mouse_encoder_encode",
  "ghostty_mouse_event_new", "ghostty_mouse_event_free", "ghostty_mouse_event_set_action", "ghostty_mouse_event_set_button",
  "ghostty_mouse_event_clear_button", "ghostty_mouse_event_set_mods", "ghostty_mouse_event_set_position",
  "ghostty_grid_ref_cell", "ghostty_grid_ref_graphemes", "ghostty_grid_ref_hyperlink_uri",
] as const;

type Exports = { memory: WebAssembly.Memory } & Record<(typeof functions)[number], (...a: number[]) => number>;

/** The struct fields this binding reads or writes, by the width it uses. */
const layouts: Record<string, Record<string, number>> = {
  GhosttyRenderStateColors: { size: 4, background: 3, foreground: 3, cursor: 3, cursor_has_value: 1, palette: 768 },
  GhosttyRenderStateCursor: {
    size: 4, viewport_has_value: 1, viewport_x: 2, viewport_y: 2, wide_tail: 1, visible: 1, blinking: 1, visual_style: 4,
  },
  GhosttyStyle: {
    size: 4, bold: 1, italic: 1, faint: 1, inverse: 1, invisible: 1, strikethrough: 1, overline: 1, underline: 4,
    underline_color: 16,
  },
  GhosttyStyleColor: { tag: 4, value: 8 },
  GhosttyStyleColorValue: { palette: 1, rgb: 3 },
  GhosttyTerminalScrollViewport: { tag: 4, value: 16 },
  GhosttyTerminalScrollViewportValue: { delta: 4 },
  GhosttyColorRgb: { r: 1, g: 1, b: 1 },
  GhosttyRenderStateRowSelection: { size: 4, start_x: 2, end_x: 2 },
  GhosttyGridRef: { size: 4 },
  GhosttySelection: { size: 4, start: 12, end: 12 },
  GhosttyPoint: { tag: 4, value: 16 },
  GhosttyPointCoordinate: { x: 2, y: 4 },
  GhosttySurfacePosition: { x: 8, y: 8 },
  GhosttySelectionGestureGeometry: { columns: 4, cell_width: 4, padding_left: 4, screen_height: 4 },
  GhosttyTerminalSelectionFormatOptions: { size: 4, emit: 4, unwrap: 1, trim: 1, selection: 4 },
  GhosttyMouseEncoderSize: {
    size: 4, screen_width: 4, screen_height: 4, cell_width: 4, cell_height: 4,
    padding_top: 4, padding_bottom: 4, padding_right: 4, padding_left: 4,
  },
  GhosttyMousePosition: { x: 4, y: 4 },
  GhosttyTerminalModeConfig: { mode: 2, value: 1 },
};

/** The enum values this binding uses. */
const enums: Record<string, string[]> = {
  GhosttyResult: ["SUCCESS", "NO_VALUE", "OUT_OF_SPACE"],
  GhosttyRenderStateData: ["COLS", "ROWS", "DIRTY", "ROW_ITERATOR", "CURSOR", "COLORS"],
  GhosttyRenderStateRowData: ["DIRTY", "CELLS", "SELECTION"],
  GhosttyRenderStateRowCellsData: ["RAW", "STYLE", "GRAPHEMES_LEN", "GRAPHEMES_BUF", "BG_COLOR", "FG_COLOR", "HAS_STYLING"],
  GhosttyRenderStateDirty: ["FALSE", "FULL"],
  GhosttyRenderStateCursorVisualStyle: ["BAR", "BLOCK", "UNDERLINE", "BLOCK_HOLLOW"],
  GhosttyCellWide: ["WIDE", "SPACER_TAIL", "SPACER_HEAD"],
  GhosttyKeyAction: ["PRESS", "REPEAT"],
  GhosttyKey: ["UNIDENTIFIED", "A", "ENTER", "ARROW_UP", "ARROW_DOWN"],
  GhosttyTerminalData: ["ACTIVE_SCREEN", "MOUSE_TRACKING", "VIEWPORT_ACTIVE", "SELECTION", "MODE", "COLS"],
  GhosttyTerminalScreen: ["ALTERNATE"],
  GhosttyTerminalScrollViewportTag: ["BOTTOM", "DELTA"],
  GhosttyStyleColorTag: ["NONE", "PALETTE", "RGB"],
  GhosttySgrUnderline: ["NONE", "SINGLE", "DOUBLE", "CURLY", "DOTTED", "DASHED"],
  GhosttyTerminalOption: ["COLOR_FOREGROUND", "COLOR_BACKGROUND", "COLOR_CURSOR", "COLOR_PALETTE", "SELECTION"],
  GhosttyPointTag: ["VIEWPORT", "SCREEN"],
  GhosttySelectionOrder: ["FORWARD", "REVERSE"],
  GhosttyFormatterFormat: ["PLAIN"],
  GhosttySelectionGestureEventType: ["PRESS", "RELEASE", "DRAG", "AUTOSCROLL_TICK"],
  GhosttySelectionGestureEventOption: ["REF", "POSITION", "REPEAT_DISTANCE", "TIME_NS", "REPEAT_INTERVAL_NS", "GEOMETRY", "VIEWPORT"],
  GhosttySelectionGestureData: ["CLICK_COUNT", "AUTOSCROLL"],
  GhosttySelectionGestureAutoscroll: ["NONE", "UP", "DOWN"],
  GhosttyMouseAction: ["PRESS", "RELEASE", "MOTION"],
  GhosttyMouseButton: ["LEFT", "RIGHT", "MIDDLE", "FOUR", "FIVE"],
  GhosttyMouseEncoderOption: ["SIZE", "ANY_BUTTON_PRESSED", "TRACK_LAST_CELL"],
};

/** Ghostty's modifier bits (key/event.h, GHOSTTY_MODS_*): defines, not in the manifest. */
export const Mods = { shift: 1 << 0, ctrl: 1 << 1, alt: 1 << 2, super: 1 << 3 };

export type RGB = [number, number, number];

export class Ghostty {
  private constructor(readonly x: Exports, readonly m: Manifest, private slot: number) {}

  /**
   * Loads the module at url, size bytes long, and checks it against what
   * this binding uses. Served as application/wasm, it compiles as it
   * arrives (instantiateStreaming on the fetch's own response, so Chrome
   * can keep the compiled code with the cached file), while a clone of the
   * response counts the bytes for progress; else the bytes are read, with
   * their progress, and instantiated. progress hears the download, then
   * the start.
   */
  static async load(url: string, size: number, progress: (s: LoadState) => void): Promise<Ghostty> {
    const res = await fetch(url);
    if (!res.ok || !res.body) throw new Error(t("loadServerAnswered", { url, status: String(res.status) }));
    const report = (received: number, total?: number) => progress({ phase: "download", received, total });
    let instance: WebAssembly.Instance;
    if ((res.headers.get("content-type") ?? "").split(";")[0].trim() === "application/wasm") {
      const counted = countBody(res.clone(), size, report).then(() => progress({ phase: "start" }), () => {});
      instance = (await WebAssembly.instantiateStreaming(res, {})).instance;
      await counted;
    } else {
      const bytes = await readBody(res, size, report);
      progress({ phase: "start" });
      instance = (await WebAssembly.instantiate(bytes.buffer as ArrayBuffer, {})).instance;
    }
    const x = instance.exports as unknown as Exports;
    for (const f of functions) if (typeof x[f] !== "function") throw new Error(`ghostty-vt.wasm has no ${f}`);
    const p = x.ghostty_type_json();
    const bytes = new Uint8Array(x.memory.buffer, p);
    const m: Manifest = JSON.parse(new TextDecoder().decode(bytes.subarray(0, bytes.indexOf(0))));
    check(m);
    const slot = x.ghostty_wasm_alloc_opaque();
    if (!slot) throw new Error("libghostty-vt: out of memory");
    return new Ghostty(x, m, slot);
  }

  /** An enum's value, which check made sure exists. */
  e(type: string, name: string): number {
    return this.m.types[type].values![name];
  }

  off(type: string, field: string): number {
    return this.m.types[type].fields![field].offset;
  }

  size(type: string): number {
    return this.m.types[type].size;
  }

  // Views of the module's memory, made again when it grows: growing
  // detaches the old buffer, so a view must not be held across a call that
  // may allocate. These getters are cheap enough to read at each use.
  private buf?: ArrayBuffer;
  private dv!: DataView;
  private u8!: Uint8Array;

  private sync() {
    const b = this.x.memory.buffer;
    if (b !== this.buf) {
      this.buf = b;
      this.dv = new DataView(b);
      this.u8 = new Uint8Array(b);
    }
  }

  get view(): DataView {
    this.sync();
    return this.dv;
  }

  get bytes(): Uint8Array {
    this.sync();
    return this.u8;
  }

  alloc(n: number): number {
    const p = this.x.ghostty_wasm_alloc(n);
    if (!p) throw new Error("libghostty-vt: out of memory");
    return p;
  }

  free(p: number, n: number) {
    this.x.ghostty_wasm_free(p, n);
  }

  /** Copies bytes into a new allocation. */
  put(b: Uint8Array): number {
    const p = this.alloc(Math.max(b.length, 1));
    this.bytes.set(b, p);
    return p;
  }

  /** Runs a constructor that writes a handle to its out slot, and returns the handle. */
  make(what: string, f: (slot: number) => number): number {
    const r = f(this.slot);
    if (r !== 0) throw new Error(`${what} failed: ${r}`);
    return this.x.ghostty_wasm_take_opaque(this.slot);
  }

  /** A sized struct, zeroed, with its size field set. */
  sized(type: string): number {
    const n = this.size(type);
    const p = this.alloc(n);
    this.bytes.fill(0, p, p + n);
    this.view.setUint32(p + this.off(type, "size"), n, true);
    return p;
  }

  rgb(p: number): RGB {
    const b = this.bytes;
    return [b[p], b[p + 1], b[p + 2]];
  }

  /** The cell bit field at name, or within the content union's arm. */
  cellBits(name: string, arm?: string): { lsb: number; width: number } {
    const bits = this.m.types.GhosttyCell.bits!;
    if (!arm) return bits[name];
    const content = bits.content;
    const b = content.arms[arm].bits[name];
    return { lsb: content.lsb + b.lsb, width: b.width };
  }
}

function check(m: Manifest) {
  if (m.abi.pointer_size !== 4 || m.abi.usize_size !== 4 || m.abi.endian !== "little") {
    throw new Error(`ghostty-vt.wasm is not a little-endian wasm32 build: ${JSON.stringify(m.abi)}`);
  }
  for (const [name, fields] of Object.entries(layouts)) {
    const t = m.types[name];
    if (!t?.fields) throw new Error(`ghostty-vt.wasm has no struct ${name}`);
    for (const [field, size] of Object.entries(fields)) {
      const f = t.fields[field];
      if (!f) throw new Error(`${name} has no field ${field}`);
      if (f.size !== size) throw new Error(`${name}.${field} is ${f.size} bytes, not ${size}`);
    }
  }
  for (const [name, values] of Object.entries(enums)) {
    for (const v of values) {
      if (typeof m.types[name]?.values?.[v] !== "number") throw new Error(`ghostty-vt.wasm has no ${name} ${v}`);
    }
  }
  const cell = m.types.GhosttyCell;
  if (cell?.kind !== "packed" || cell.size !== 8 || !cell.bits?.wide || !cell.bits?.content?.arms?.CODEPOINT) {
    throw new Error("GhosttyCell is not the packed 64-bit value this client reads");
  }
}
