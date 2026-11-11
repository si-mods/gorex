// One pane of a window: a block's terminal, its stream fed to
// libghostty-vt, drawn on its own canvas, the keys typed written back.
import { Ghostty } from "./ghostty";
import { Control, DataStream, RexError, TerminalStream } from "./rex";
import { desktopTheme, Font, Terminal, Theme, themeOf } from "./terminal";

/** What a pane needs of the page: the connection, the module, the chosen theme and the status line. */
export interface PaneHost {
  control(): Control | undefined;
  g: Ghostty;
  chosen(): Theme | undefined;
  status(s: string): void;
  /** The page's chrome follows the terminal's background. */
  scheme(bg: [number, number, number]): void;
  /** The pane is now the window's focused one, by the user's click or Tab. */
  focused(p: TerminalView): void;
  /** Whether the pane is the window's focused one. */
  isFocused(p: TerminalView): boolean;
  /** Runs one of the pane's header buttons. */
  action(p: TerminalView, name: string): void;
}

/** The desktop's terminal font (style.go, settings.go): its family, size in CSS pixels and line height. */
const fontFamily = () => getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() || "monospace";
const fontSize = 11.6;
const lineHeight = 1;
/** The padding around the grid, in CSS pixels, as the desktop's (MyGo's padX, padY). */
const padX = 6, padY = 4;
/** The cursor blinks every 600 ms while the terminal has the focus, as on the desktop. */
const blinkPeriod = 600;

export function describe(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** The header's buttons, as the desktop's (paneActions): split right, split down, zoom and close. */
const actions: [name: string, label: string, glyph: string][] = [
  ["split-right", "Split Right", "◫"],
  ["split-down", "Split Down", "⊟"],
  ["zoom", "Zoom", "⤢"],
  ["close", "Close Pane", "✕"],
];

export class TerminalView {
  /** The pane: its header and its body, with the canvas. */
  readonly el = el("div", "pane");
  private readonly title = el("span", "pane-title");
  private readonly body = el("div", "pane-body");
  private readonly canvas = el("canvas", "screen");
  private readonly ctx: CanvasRenderingContext2D;
  private term?: Terminal;
  private data?: DataStream;
  private stream?: TerminalStream;
  private closed = false;
  private generation = 0;
  private font!: Font;
  /** Set when the fonts loaded since the font was measured. */
  fontStale = false;
  /** While a divider is dragged the terminal keeps its size, as on the desktop. */
  held = false;
  /** The grid's top left on the canvas, in device pixels. */
  private ox = 0;
  private oy = 0;
  /** The colours of the stream's last theme message. */
  private host: Theme = {};
  /** The colours over the desktop's: the chosen theme, until a theme message replaces it. */
  private theme: Theme;
  /** Wheel scrolling not yet a whole row. */
  private scrolled = 0;
  private blinkStart = performance.now();
  private blinkTimer?: number;
  /** The background the canvas was filled with last. */
  private painted = "";
  private fitted?: { cols: number; rows: number };
  private sizing?: number;
  private frame?: number;
  private full = true;
  private typed: Uint8Array[] = [];
  private typing = false;
  /** Whether this view's claim of the block waits for its resize message, and a message it held meanwhile. */
  private claiming = false;
  private stale?: { cols: number; rows: number };
  private observer: ResizeObserver;

  constructor(readonly session: string, readonly block: string, private readonly app: PaneHost) {
    this.theme = app.chosen() ?? {};
    this.ctx = this.canvas.getContext("2d", { alpha: false })!;
    this.el.tabIndex = 0;
    this.el.dataset.block = block;
    const head = el("div", "pane-head");
    const buttons = el("span", "pane-actions");
    for (const [name, label, glyph] of actions) {
      const b = el("button", "pane-action", glyph);
      b.type = "button";
      b.title = label;
      b.setAttribute("aria-label", label);
      b.dataset.action = name;
      // The pane keeps the keyboard: a press on a button does not take it.
      b.onpointerdown = (e) => e.preventDefault();
      b.onclick = (e) => {
        e.stopPropagation();
        this.app.action(this, name);
      };
      buttons.append(b);
    }
    head.append(this.title, buttons);
    this.body.append(this.canvas);
    this.el.append(head, this.body);
    this.el.addEventListener("keydown", (e) => this.key(e));
    this.el.addEventListener("wheel", (e) => this.wheel(e), { passive: false });
    this.el.addEventListener("focus", () => {
      this.app.focused(this);
      this.wake();
    });
    this.el.addEventListener("blur", () => this.redraw());
    // The canvas takes the body's size in device pixels where the browser tells it.
    this.observer = new ResizeObserver((entries) => {
      const d = entries[0].devicePixelContentBoxSize?.[0];
      if (!this.held) this.layout(d && { width: d.inlineSize, height: d.blockSize });
    });
    this.observer.observe(this.body, { box: "device-pixel-content-box" });
  }

  setTitle(s: string) {
    if (this.title.textContent !== s) this.title.textContent = s;
  }

  /** Opens the stream from the start: a ticket, a HELLO, a new snapshot. */
  async start() {
    const c = this.app.control();
    if (!c || this.closed) return;
    const gen = ++this.generation;
    this.data?.close();
    try {
      const ticket = (
        await c.call("data.connect", {
          session_id: this.session, block_id: this.block, data_ref_id: "terminal", subprotocol: "rex.data.terminal.v1",
        })
      ).payload.ticket;
      if (gen !== this.generation) return;
      const d = new DataStream();
      this.data = d;
      d.onBytes = (b) => d === this.data && this.receive(b);
      d.onDrop = (code) => {
        if (d !== this.data || this.closed) return;
        // 4000: the block closed; the view's next read takes the pane away.
        if (code !== 4000) {
          this.app.status(`A terminal's connection dropped (${code}). Reopening…`);
          setTimeout(() => gen === this.generation && this.start(), 1000);
        }
      };
      await d.open(ticket);
      if (gen !== this.generation) return d.close();
      this.stream = new TerminalStream(d.nonce);
    } catch (e) {
      if (gen === this.generation && !this.closed) this.app.status(`Could not open a terminal: ${describe(e)}`);
    }
  }

  private receive(bytes: Uint8Array) {
    for (const item of this.stream!.feed(bytes)) {
      switch (item.kind) {
        case "snapshot":
          this.term?.free();
          try {
            this.term = Terminal.fromSnapshot(this.app.g, item.data);
          } catch (e) {
            this.term = undefined;
            this.app.status(`A terminal's snapshot could not be read: ${describe(e)}`);
            return;
          }
          // Drawn at this view's size, which the block takes next, as the
          // iOS app does: the program's repaint after the resize then lands
          // on a screen of its size.
          this.term.setTheme(desktopTheme());
          this.term.setTheme(this.theme);
          if (this.fitted) this.term.resize(this.fitted.cols, this.fitted.rows, this.font.w, this.font.h);
          this.full = true;
          this.claim();
          break;
        case "output":
          this.term?.write(item.data);
          break;
        case "message":
          this.message(item.message);
          break;
      }
    }
    this.redraw();
  }

  private message(m: any) {
    switch (m.type) {
      case "theme":
        // Each message replaces the last one's colours, the chosen theme's too.
        this.host = this.theme = themeOf(m);
        this.applyTheme();
        break;
      case "resize":
        // The server polls the size, so a message may follow the program's
        // repaint for it. This view's own size it has already; until the
        // message with this view as owner comes, one with the block's old
        // size is stale, and held in case the claim fails. Another owner's
        // size, as when the desktop took the block back, applies.
        if (m.owner === this.app.control()?.clientID) {
          this.claiming = false;
          this.stale = undefined;
        } else if (this.claiming) {
          this.stale = m;
        } else {
          this.resizeTo(m);
        }
        break;
      case "child_exited":
        this.app.status(`A program exited (${m.exit_code ?? 0}).`);
        break;
    }
  }

  private applyTheme() {
    this.term?.setTheme(desktopTheme());
    this.term?.setTheme(this.theme);
    this.full = true;
    this.redraw();
  }

  /** A theme chosen now, or the host's colours (none), as a theme message from the stream would be. */
  setChosen(t?: Theme) {
    this.theme = t ?? this.host;
    this.applyTheme();
  }

  private resizeTo(m: { cols: number; rows: number }) {
    this.term?.resize(m.cols, m.rows, this.font.w, this.font.h);
    this.full = true;
  }

  /** Owns the block, so that it takes this view's size. */
  private async claim() {
    this.claiming = true;
    try {
      await this.app.control()?.call("session.own_block", { session_id: this.session, block_ids: [this.block], mode: "take" });
      this.sendSize();
    } catch (e) {
      console.warn("could not own the block", e);
      // Not owned, the block keeps its size: draw at it.
      this.claiming = false;
      if (this.stale) this.resizeTo(this.stale);
      this.stale = undefined;
    }
  }

  /**
   * Sizes the canvas to the body in whole device pixels, and the terminal
   * to the canvas less its padding, at the device's pixel ratio.
   */
  layout(device?: { width: number; height: number }) {
    if (this.closed) return;
    const scale = window.devicePixelRatio || 1;
    const r = this.body.getBoundingClientRect();
    const canvas = this.canvas;
    canvas.width = Math.max(1, device?.width ?? Math.round(r.width * scale));
    canvas.height = Math.max(1, device?.height ?? Math.round(r.height * scale));
    canvas.style.width = `${canvas.width / scale}px`;
    canvas.style.height = `${canvas.height / scale}px`;
    if (this.font?.scale !== scale || this.fontStale) {
      this.font = new Font(fontFamily(), fontSize, scale, lineHeight);
      this.fontStale = false;
    }
    this.ox = Math.round(padX * scale);
    this.oy = Math.round(padY * scale);
    this.full = true;
    const cols = Math.max(2, Math.floor((canvas.width - 2 * this.ox) / this.font.w));
    const rows = Math.max(1, Math.floor((canvas.height - 2 * this.oy) / this.font.h));
    if (this.fitted?.cols !== cols || this.fitted?.rows !== rows) {
      this.fitted = { cols, rows };
      this.term?.resize(cols, rows, this.font.w, this.font.h);
      clearTimeout(this.sizing);
      this.sizing = window.setTimeout(() => this.sendSize(), 100);
    }
    this.redraw();
  }

  private sendSize() {
    const s = this.fitted;
    if (!s || !this.term || this.closed) return;
    this.app
      .control()
      ?.callBlock("resize", this.session, this.block, {
        columns: s.cols, rows: s.rows, cell_width_px: this.font.w, cell_height_px: this.font.h,
      })
      .catch((e) => console.warn("resize", e));
  }

  /** Draws on the next frame what changed since the last. */
  redraw() {
    if (this.frame !== undefined || this.closed) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = undefined;
      if (!this.term || this.closed) return;
      // The cursor is solid in the window's focused pane while the page has the focus.
      const focused = this.app.isFocused(this) && document.hasFocus() && this.el.contains(document.activeElement);
      const ctx = this.ctx;
      // The padding takes the background too, as a program may change it (OSC 11).
      const bg = `rgb(${this.term.background().join(",")})`;
      if (this.full || bg !== this.painted) {
        this.full = true;
        this.painted = ctx.fillStyle = this.el.style.background = bg;
        this.app.scheme(this.term.background());
        ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
      }
      const since = performance.now() - this.blinkStart;
      this.term.draw(ctx, {
        font: this.font, ox: this.ox, oy: this.oy, full: this.full, focused,
        blinkOff: since % (2 * blinkPeriod) >= blinkPeriod,
      });
      this.full = false;
      // The next phase of the blink.
      clearTimeout(this.blinkTimer);
      if (focused && this.term.blinking) this.blinkTimer = window.setTimeout(() => this.redraw(), blinkPeriod - (since % blinkPeriod));
    });
  }

  /** Restarts the blink with the cursor on, as typing and the focus do. */
  wake() {
    this.blinkStart = performance.now();
    this.redraw();
  }

  /**
   * Scrolls as the desktop's wheel does: the scrollback, or the arrow keys
   * in a full-screen program (alternate scroll). A program that takes the
   * mouse gets nothing, as this client reports no mouse yet.
   */
  private wheel(e: WheelEvent) {
    const t = this.term;
    if (!t || e.ctrlKey) return;
    e.preventDefault();
    const rowPx = this.font.h / this.font.scale;
    const dy = e.deltaMode === 1 ? e.deltaY * rowPx : e.deltaMode === 2 ? e.deltaY * this.body.clientHeight : e.deltaY;
    this.scrolled += dy / rowPx;
    const rows = Math.trunc(this.scrolled);
    this.scrolled -= rows;
    if (rows === 0 || t.mouseTracking()) return;
    if (t.altScreen()) {
      const key = rows < 0 ? "ArrowUp" : "ArrowDown";
      const b = t.encodeKey(new KeyboardEvent("keydown", { code: key, key }));
      if (b) for (let i = 0; i < Math.abs(rows); i++) this.type(b);
      return;
    }
    t.scrollBy(rows);
    this.redraw();
  }

  private key(e: KeyboardEvent) {
    if (!this.term || e.isComposing || e.defaultPrevented) return;
    const b = this.term.encodeKey(e);
    if (!b) return;
    e.preventDefault();
    // Typing shows the screen again, as on the desktop.
    if (!this.term.atBottom()) this.term.scrollBy("bottom");
    this.wake();
    this.type(b);
  }

  /**
   * Writes what was typed: one write at a time, as the server runs writes
   * in any order, and what comes meanwhile goes together next.
   */
  private type(b: Uint8Array) {
    this.typed.push(b);
    this.write();
  }

  private async write() {
    if (this.typing || this.closed || !this.typed.length) return;
    const all = new Uint8Array(this.typed.reduce((n, b) => n + b.length, 0));
    let at = 0;
    for (const b of this.typed) all.set(b, (at += b.length) - b.length);
    this.typed = [];
    this.typing = true;
    try {
      await this.app.control()?.callBlock("write", this.session, this.block, { data: new TextDecoder().decode(all) });
    } catch (e) {
      if (e instanceof RexError && e.code === "offline") this.typed.unshift(all);
      else console.warn("dropped a write", e);
    }
    this.typing = false;
    this.write();
  }

  /**
   * Ends the pane: its stream, its terminal and its element. A block that
   * still exists is given back (disowned), so that the desktop sizes it again.
   */
  close(disown: boolean) {
    this.closed = true;
    this.generation++;
    this.data?.close();
    this.observer.disconnect();
    if (this.frame !== undefined) cancelAnimationFrame(this.frame);
    clearTimeout(this.blinkTimer);
    clearTimeout(this.sizing);
    this.term?.free();
    this.term = undefined;
    this.el.remove();
    if (disown) this.app.control()?.call("session.disown_block", { session_id: this.session, block_ids: [this.block] }).catch(() => {});
  }
}
