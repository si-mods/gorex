// One pane of a window: a block's terminal, its stream fed to
// libghostty-vt, drawn on its own canvas, the keys typed written back.
import { openFile } from "./files";
import { Ghostty } from "./ghostty";
import { shortcut } from "./keys";
import { PointerInput } from "./pointer";
import { contrastRatio, cursorSeq, fontFamily, textSize } from "./prefs";
import { Program, smallTile } from "./programs";
import { localize, Msg, PlainKey, t } from "./i18n";
import { Control, DataStream, describe, RexError, TerminalStream } from "./rex";
import { desktopTheme, Font, Terminal, Theme, themeOf } from "./terminal";

/** What a pane needs of the page: the connection, the module, the chosen theme and the status line. */
export interface PaneHost {
  control(): Control | undefined;
  /** The connection once its sessions were read again (attached) after it opened, for writes. */
  live(): Control | undefined;
  g: Ghostty;
  chosen(): Theme | undefined;
  status(s: Msg): void;
  /** The page's chrome follows the terminal's background. */
  scheme(bg: [number, number, number]): void;
  /** The pane is now the window's focused one, by the user's click or Tab. */
  focused(p: TerminalView): void;
  /** Whether the pane is the window's focused one. */
  isFocused(p: TerminalView): boolean;
  /** How the block's program shows: its tile heads the pane. */
  programOf(block: string): Program;
  /** Runs one of the pane's header buttons. */
  action(p: TerminalView, name: string): void;
}

/** The terminal's line height, as the desktop's; its family and size are the settings' (prefs.ts). */
const lineHeight = 1;
/** The padding around the grid, in CSS pixels, as the desktop's (MyGo's padX, padY). */
const padX = 6, padY = 4;
/** The cursor blinks every 600 ms while the terminal has the focus, as on the desktop. */
const blinkPeriod = 600;
/** The most typed bytes that wait to be written while offline, as the iOS app's 4 MB. */
const maxTyped = 4 << 20;

export { describe };

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** The header's buttons, as the desktop's (view.go paneActions), with its lucide icons: split right, split down, zoom and close. */
const actions: [name: string, label: PlainKey][] = [
  ["split-right", "cmdSplitRight"],
  ["split-down", "cmdSplitDown"],
  ["zoom", "cmdZoom"],
  ["close", "cmdClosePane"],
];

export class TerminalView {
  /** The pane: its header and its body, with the canvas. */
  readonly el = el("div", "pane");
  private readonly tile = el("span", "pane-tile");
  private shownProgram = "";
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
  /** The pointer over the grid: selecting, the wheel, and the mouse a program tracks. */
  private readonly pointer: PointerInput;
  private blinkStart = performance.now();
  private blinkTimer?: number;
  /** The background the canvas was filled with last. */
  private painted = "";
  private fitted?: { cols: number; rows: number };
  /** Set when a layout found the pane hidden or without a size: the next one sends its size. */
  private unsized = false;
  private sizing?: number;
  private frame?: number;
  private full = true;
  private typed: Uint8Array[] = [];
  private typing = false;
  /** While offline, the wait before the typed bytes go again. */
  private writeRetry?: number;
  /** Whether this view's claim of the block waits for its resize message, and a message it held meanwhile. */
  private claiming = false;
  private stale?: { cols: number; rows: number };
  private observer: ResizeObserver;

  constructor(readonly session: string, readonly block: string, private readonly app: PaneHost) {
    this.theme = app.chosen() ?? {};
    this.ctx = this.canvas.getContext("2d", { alpha: false })!;
    this.paintBlank();
    this.el.tabIndex = 0;
    this.el.dataset.block = block;
    const head = el("div", "pane-head");
    const buttons = el("span", "pane-actions");
    for (const [name, label] of actions) {
      const b = el("button", "pane-action");
      b.append(el("span", "pane-icon"));
      b.type = "button";
      localize(b, (b) => {
        b.title = t(label);
        b.setAttribute("aria-label", b.title);
      });
      b.dataset.action = name;
      // The pane keeps the keyboard: a press on a button does not take it.
      b.onpointerdown = (e) => e.preventDefault();
      b.onclick = (e) => {
        e.stopPropagation();
        this.app.action(this, name);
      };
      buttons.append(b);
    }
    head.append(this.tile, this.title, buttons);
    this.body.append(this.canvas);
    this.el.append(head, this.body);
    this.pointer = new PointerInput(this.canvas, this.body, {
      term: () => this.term,
      grid: () => ({ w: this.font.w, h: this.font.h, ox: this.ox, oy: this.oy, scale: this.font.scale }),
      focused: () => this.el.contains(document.activeElement),
      redraw: (full) => {
        if (full) this.full = true;
        this.redraw();
      },
      send: (b) => this.type(b),
      status: (s) => this.app.status(s),
      files: () => !!this.app.control()?.features.has("gorex.download"),
      openFile: (path) =>
        openFile(this.session, this.block, path, {
          control: () => this.app.control(),
          status: (s) => this.app.status(s),
          back: () => {
            if (!this.closed) this.el.focus({ preventScroll: true });
          },
        }),
    });
    this.el.addEventListener("keydown", (e) => this.key(e));
    this.body.addEventListener("wheel", (e) => this.pointer.wheel(e), { passive: false });
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

  /** The tile of the pane's program, as the title of iOS's terminal screen has it. */
  setProgram(p: Program) {
    const key = JSON.stringify(p);
    if (key === this.shownProgram) return;
    this.shownProgram = key;
    this.tile.replaceChildren(smallTile(p));
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
          this.app.status(() => t("paneDropped", { code: String(code) }));
          setTimeout(() => gen === this.generation && this.start(), 1000);
        }
      };
      await d.open(ticket);
      if (gen !== this.generation) return d.close();
      this.stream = new TerminalStream(d.nonce);
    } catch (e) {
      if (gen === this.generation && !this.closed) this.app.status(() => t("paneOpenFailed", { error: describe(e) }));
    }
  }

  private receive(bytes: Uint8Array) {
    for (const item of this.stream!.feed(bytes)) {
      switch (item.kind) {
        case "snapshot":
          this.pointer.reset();
          this.term?.free();
          try {
            this.term = Terminal.fromSnapshot(this.app.g, item.data);
          } catch (e) {
            this.term = undefined;
            this.app.status(() => t("paneSnapshotFailed", { error: describe(e) }));
            return;
          }
          // Drawn at this view's size, which the block takes next, as the
          // iOS app does: the program's repaint after the resize then lands
          // on a screen of its size.
          this.term.setTheme(desktopTheme());
          this.term.setTheme(this.theme);
          this.applyCursor();
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
        this.app.status(() => t("paneExited", { code: String(m.exit_code ?? 0) }));
        break;
    }
  }

  private applyTheme() {
    this.term?.setTheme(desktopTheme());
    this.term?.setTheme(this.theme);
    this.full = true;
    this.redraw();
  }

  /**
   * Sets the cursor the settings choose, as the desktop's setCursor does:
   * with the sequence a program would send, so that a program can still
   * set its own after it. None while the settings choose none; reset
   * sets the terminal's default again (DECSCUSR 0).
   */
  applyCursor(reset = false) {
    const seq = cursorSeq() ?? (reset ? "\x1b[0 q" : undefined);
    if (!seq || !this.term) return;
    this.term.write(new TextEncoder().encode(seq));
    this.wake();
  }

  /** The settings' font, size or contrast changed: measure the font again and draw it all. */
  restyle() {
    this.fontStale = true;
    this.layout();
  }

  /** Draws it all again on the next frame, as when a font chunk arrived. */
  repaint() {
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

  /** The background to show: the last painted, else the chosen theme's, else the default. */
  private blank(): string {
    const b = this.theme.background;
    return this.painted || (b ? `rgb(${b.join(",")})` : "#1e1e22");
  }

  /** Fills the canvas and the pane with the blank background, until the terminal draws. */
  private paintBlank() {
    const bg = (this.el.style.background = this.blank());
    this.ctx.fillStyle = bg;
    this.ctx.fillRect(0, 0, this.canvas.width, this.canvas.height);
  }

  /**
   * Sizes the canvas to the body in whole device pixels, and the terminal
   * to the canvas less its padding, at the device's pixel ratio.
   */
  layout(device?: { width: number; height: number }) {
    if (this.closed) return;
    const scale = window.devicePixelRatio || 1;
    if (this.font?.scale !== scale || this.fontStale) {
      this.font = new Font(fontFamily(), textSize(), scale, lineHeight);
      this.fontStale = false;
    }
    const r = this.body.getBoundingClientRect();
    const width = device?.width ?? Math.round(r.width * scale);
    const height = device?.height ?? Math.round(r.height * scale);
    // A hidden pane, as another one is zoomed, has no size: it keeps the
    // one it had, and the block keeps it too. Shown again, it lays out and
    // sends its size once more.
    if (this.el.hidden || width <= 0 || height <= 0) {
      this.unsized = true;
      return;
    }
    const canvas = this.canvas;
    canvas.width = width;
    canvas.height = height;
    canvas.style.width = `${canvas.width / scale}px`;
    canvas.style.height = `${canvas.height / scale}px`;
    // Resizing cleared the canvas to black: fill it before the browser paints.
    this.paintBlank();
    this.ox = Math.round(padX * scale);
    this.oy = Math.round(padY * scale);
    this.full = true;
    const cols = Math.max(2, Math.floor((canvas.width - 2 * this.ox) / this.font.w));
    const rows = Math.max(1, Math.floor((canvas.height - 2 * this.oy) / this.font.h));
    if (this.fitted?.cols !== cols || this.fitted?.rows !== rows || this.unsized) {
      this.unsized = false;
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
      // The link under the mouse, found again as the text under it changes: its row draws again.
      if (this.pointer.hover(false)) this.full = true;
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
        blinkOff: since % (2 * blinkPeriod) >= blinkPeriod, minContrast: contrastRatio(),
        selection: this.theme.selection, selectionText: this.theme.selectionText, link: this.pointer.linkShown(),
      });
      this.full = false;
      this.pointer.drawn();
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

  private key(e: KeyboardEvent) {
    if (!this.term || e.isComposing || e.defaultPrevented) return;
    this.pointer.hideMenu();
    // Copy (Ctrl+Shift+C, or Cmd+C on the Mac) copies the selection. It
    // never reaches the program, selection or not, so that it cannot
    // interrupt one as Ctrl+C would.
    if (shortcut(e) === "copy") {
      e.preventDefault();
      this.pointer.copy();
      return;
    }
    const b = this.term.encodeKey(e);
    if (!b) return;
    e.preventDefault();
    // Typing takes the selection away, as Ghostty's selection-clear-on-typing.
    this.pointer.typed();
    // Typing shows the screen again, as on the desktop.
    if (!this.term.atBottom()) this.term.scrollBy("bottom");
    this.wake();
    this.type(b);
  }

  /**
   * Writes what was typed: one write at a time, as the server runs writes
   * in any order, and what comes meanwhile goes together next. What was
   * not sent, offline, goes again a second later or when the connection
   * is back (resume); what was sent but not answered is dropped, as the
   * server may have run it. What is typed past a bound meanwhile is lost,
   * as in the iOS app (TerminalSession.type).
   */
  private type(b: Uint8Array) {
    const waiting = this.typed.reduce((n, x) => n + x.length, 0);
    if (waiting + b.length > maxTyped) {
      this.app.status(() => t("paneDroppedTyped", { n: b.length, waiting }));
      return;
    }
    this.typed.push(b);
    this.write();
  }

  /** The connection is back: what waits to be written goes now. */
  resume() {
    if (this.writeRetry === undefined) return;
    clearTimeout(this.writeRetry);
    this.writeRetry = undefined;
    this.write();
  }

  private async write() {
    if (this.typing || this.closed || !this.typed.length || this.writeRetry !== undefined) return;
    const all = new Uint8Array(this.typed.reduce((n, b) => n + b.length, 0));
    let at = 0;
    for (const b of this.typed) all.set(b, (at += b.length) - b.length);
    this.typed = [];
    const c = this.app.live();
    let offline = !c;
    if (c) {
      this.typing = true;
      try {
        await c.callBlock("write", this.session, this.block, { data: new TextDecoder().decode(all) });
      } catch (e) {
        if (e instanceof RexError && e.code === "offline") offline = true;
        else console.warn("dropped a write", e);
      }
      this.typing = false;
    }
    if (this.closed) return;
    if (offline) {
      // First, before what was typed meanwhile; tried again in a second, not at once.
      this.typed.unshift(all);
      this.writeRetry = window.setTimeout(() => {
        this.writeRetry = undefined;
        this.write();
      }, 1000);
      return;
    }
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
    clearTimeout(this.writeRetry);
    this.pointer.dispose();
    this.term?.free();
    this.term = undefined;
    this.el.remove();
    if (disown) this.app.control()?.call("session.disown_block", { session_id: this.session, block_ids: [this.block] }).catch(() => {});
  }
}
