// One pane of a window: a block's terminal, its stream fed to
// libghostty-vt, drawn on its own canvas, the keys typed written back.
import { Ghostty } from "./ghostty";
import { shortcut } from "./keys";
import { loader } from "./loader";
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
/**
 * The most bytes one write carries. The gateway reads a message of 1 MiB at
 * most, with the data as a JSON string (a control character takes up to six
 * bytes of it), so a paste goes in pieces, which the server runs in order.
 */
const maxWrite = 128 << 10;

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
  /** The header: the tile, the title and the buttons; its menu and its drag are the window's (window.ts). */
  readonly head = el("div", "pane-head");
  private readonly body = el("div", "pane-body");
  private readonly canvas = el("canvas", "screen");
  /**
   * The text field that holds the pane's keys: transparent, at the cursor, so
   * that the input method's window opens there and a phone shows its keyboard.
   * It stays empty: what is typed into it goes to the program.
   */
  private readonly input = el("textarea", "pane-input");
  /** What an input method has composed so far, drawn at the cursor, as the field itself is transparent. */
  private readonly preedit = el("div", "pane-preedit");
  private composing = false;
  /** The text the last composition ended with, which some browsers say again as plain input. */
  private committed?: { text: string; at: number };
  /** When Ctrl+V was pressed (Windows): a paste of no text then sends ^V. */
  private ctrlV?: number;
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
    // The pane's focus goes to its text field (below); the pane itself takes the click that gives it.
    this.el.tabIndex = -1;
    this.el.dataset.block = block;
    const head = this.head;
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
    const input = this.input;
    input.rows = 1;
    input.wrap = "off";
    input.autocomplete = "off";
    input.autocapitalize = "none";
    input.spellcheck = false;
    input.setAttribute("autocorrect", "off");
    this.preedit.hidden = true;
    this.body.append(this.canvas, input, this.preedit);
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
      paste: (text) => this.paste(text),
      status: (s) => this.app.status(s),
      files: () => !!this.app.control()?.features.has("gorex.download"),
      // Opening files (files.ts) is a chunk of its own, loaded with the first link opened.
      openFile: (path) =>
        void loader.module("js/files", () => import("./files")).then(
          (m) =>
            m.openFile(this.session, this.block, path, {
              control: () => this.app.control(),
              status: (s) => this.app.status(s),
              back: () => {
                if (!this.closed) this.focusKeys();
              },
            }),
          (e) => this.app.status(() => t("fileOpenFailed", { path, error: describe(e) })),
        ),
    });
    this.el.addEventListener("keydown", (e) => this.key(e));
    this.body.addEventListener("wheel", (e) => this.pointer.wheel(e), { passive: false });
    // A click on the pane focuses it (focus does not bubble): its text field takes the focus on.
    this.el.addEventListener("focus", () => this.input.focus({ preventScroll: true }));
    this.el.addEventListener("focusin", (e) => {
      if (e.target === this.el) return;
      this.app.focused(this);
      this.wake();
    });
    this.el.addEventListener("focusout", () => this.redraw());
    // What an input method or a soft keyboard types, and the paste, come to the text field, not as keys.
    input.addEventListener("compositionstart", () => this.compose(true));
    input.addEventListener("compositionupdate", (e) => this.compose(true, e.data));
    input.addEventListener("compositionend", (e) => this.compose(false, e.data));
    input.addEventListener("beforeinput", (e) => this.beforeInput(e));
    input.addEventListener("paste", (e) => this.pasted(e));
    input.addEventListener("blur", () => this.composing && this.compose(false));
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

  /** Shows a field that renames the pane in its title's place, or, with none, the title again; the header drags only then. */
  editTitle(field?: HTMLElement) {
    const now = this.head.querySelector(".rename-field");
    if (field) (now ?? this.title).replaceWith(field);
    else now?.replaceWith(this.title);
    this.head.draggable = !field;
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
      const d = new DataStream(c.transport);
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
      this.placeInput();
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

  /** Gives the pane's text field the keyboard, unless something in the pane has it. */
  focusKeys() {
    if (!this.el.contains(document.activeElement)) this.input.focus({ preventScroll: true });
  }

  /**
   * Puts the text field, and the composition drawn over it, on the cursor's
   * cell, so that an input method opens its window there. It spans the rest
   * of the row, so that a long composition does not scroll in it; it is
   * transparent and takes no click, and the pane clips it.
   */
  private placeInput() {
    const c = this.term?.cursorCell();
    if (!c || !this.font) return;
    const s = this.font.scale;
    const x = (this.ox + c.x * this.font.w) / s;
    const h = this.font.h / s;
    for (const e of [this.input, this.preedit]) {
      e.style.left = `${x}px`;
      e.style.top = `${(this.oy + c.y * this.font.h) / s}px`;
      e.style.height = `${h}px`;
      e.style.lineHeight = `${h}px`;
    }
    this.input.style.width = `${Math.max(this.font.w / s, this.body.clientWidth - x)}px`;
    this.preedit.style.font = `${this.font.size / s}px/${h}px ${this.font.family}`;
    this.preedit.style.background = this.painted;
    this.preedit.style.color = this.theme.foreground ? `rgb(${this.theme.foreground.join(",")})` : "#e6e6ea";
  }

  /**
   * An input method composes (it shows what it has so far) or ends: the
   * text it committed goes to the program, once, and nothing before. A
   * composition that was cancelled ends with no text.
   */
  private compose(on: boolean, data?: string) {
    this.composing = on;
    this.preedit.hidden = !on || !data;
    if (on) {
      this.preedit.textContent = data ?? "";
      this.placeInput();
      return;
    }
    this.input.value = "";
    if (!data) return;
    this.committed = { text: data, at: performance.now() };
    this.submitText(data);
  }

  /**
   * What the text field is given, before it takes it: a letter from a soft
   * keyboard, a line break or a backspace the keys did not report. The
   * field takes none of it, and stays empty. A key that Ghostty's encoder
   * took (key()) gives no such event; nor does a composition's text, which
   * compose() has sent at its end.
   */
  private beforeInput(e: InputEvent) {
    if (e.cancelable) e.preventDefault();
    if (!this.term || e.isComposing || this.composing) return;
    switch (e.inputType) {
      case "insertText": {
        const text = e.data ?? "";
        // Some browsers give a composition's text again as plain input, just after it ended.
        const c = this.committed;
        if (c && c.text === text && performance.now() - c.at < 200) return;
        if (text) this.submitText(text);
        break;
      }
      case "insertLineBreak":
      case "insertParagraph":
        this.submitText("\r");
        break;
      case "deleteContentBackward":
        this.submitText("\x7f");
        break;
    }
  }

  private key(e: KeyboardEvent) {
    // A key an input method took (isComposing, or keyCode 229 as Safari and soft keyboards report it) is its own, not the program's.
    if (!this.term || e.isComposing || e.keyCode === 229 || e.defaultPrevented) return;
    this.pointer.hideMenu();
    const name = shortcut(e);
    // Copy (Ctrl+Shift+C, or Cmd+C on the Mac) copies the selection. It
    // never reaches the program, selection or not, so that it cannot
    // interrupt one as Ctrl+C would.
    if (name === "copy") {
      e.preventDefault();
      this.pointer.copy();
      return;
    }
    // Paste is the browser's: its paste event (pasted) has the text. On Windows Ctrl+V pastes only
    // text, as the desktop's does: with none, pasted sends ^V.
    if (name === "paste" || name === "paste-or-key") {
      this.ctrlV = name === "paste-or-key" ? performance.now() : undefined;
      return;
    }
    const b = this.term.encodeKey(e);
    if (!b) return;
    e.preventDefault();
    this.submit(b);
  }

  /** Types bytes to the program: the selection goes, the screen shows its end, the cursor shows. */
  private submit(b: Uint8Array) {
    if (!this.term) return;
    // Typing takes the selection away, as Ghostty's selection-clear-on-typing.
    this.pointer.typed();
    // Typing shows the screen again, as on the desktop.
    if (!this.term.atBottom()) this.term.scrollBy("bottom");
    this.wake();
    this.type(b);
  }

  private submitText(text: string) {
    this.submit(new TextEncoder().encode(text));
  }

  /** The browser's paste into the text field: Ctrl+V, Cmd+V, Ctrl+Shift+V or its menu. */
  private pasted(e: ClipboardEvent) {
    e.preventDefault();
    const ctrl = this.ctrlV !== undefined && performance.now() - this.ctrlV < 1000;
    this.ctrlV = undefined;
    const text = e.clipboardData?.getData("text/plain") ?? "";
    if (text) this.paste(text);
    else if (ctrl) this.submit(new Uint8Array([0x16]));
  }

  /**
   * Types pasted text, as a terminal pastes it, as the desktop does
   * (internal/rex/paste.go): lines end with a carriage return. A program
   * that asked for bracketed paste (mode 2004) gets the text between
   * ESC [ 200 ~ and ESC [ 201 ~ (so it does not run the lines as
   * commands), without that end marker inside the text; another program
   * gets it without the control characters that could run commands.
   * Past the bound of what waits to be written, all of it is dropped,
   * with a status message (type).
   */
  paste(text: string) {
    if (!this.term || !text) return;
    let s = text.replace(/\r\n?|\n/g, "\r");
    if (this.term.mode(2004)) {
      for (let p = ""; p !== s; ) s = (p = s).replace(/\x1b\[201~/g, "");
      s = `\x1b[200~${s}\x1b[201~`;
    } else {
      s = s.replace(/[\x00-\x08\x0a-\x0c\x0e-\x1f\x7f]/g, "");
    }
    this.submitText(s);
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
    const joined = new Uint8Array(this.typed.reduce((n, b) => n + b.length, 0));
    let at = 0;
    for (const b of this.typed) joined.set(b, (at += b.length) - b.length);
    // One write takes a piece, not cut inside a UTF-8 character; the rest goes next.
    let n = Math.min(joined.length, maxWrite);
    while (n < joined.length && n > 0 && (joined[n] & 0xc0) === 0x80) n--;
    if (n === 0) n = Math.min(joined.length, maxWrite);
    const all = joined.subarray(0, n);
    this.typed = n < joined.length ? [joined.subarray(n)] : [];
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
