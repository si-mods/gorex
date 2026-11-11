// GoRex's web client: the sessions the gateway serves, and one terminal
// at a time, live, over the same API the iOS app speaks.
import { Ghostty } from "./ghostty";
import { Control, DataStream, RexError, TerminalStream } from "./rex";
import { desktopTheme, Font, Terminal, Theme, themeOf } from "./terminal";
import { chosenName, isLight, loadThemes, mountPicker, ThemeEntry, themeColours } from "./themes";

const $ = (id: string) => document.getElementById(id)!;
const canvas = $("screen") as HTMLCanvasElement;
const pane = $("pane");
const ctx = canvas.getContext("2d", { alpha: false })!;

/** The desktop's terminal font (style.go, settings.go): its family, size in CSS pixels and line height. */
const fontFamily = getComputedStyle(document.documentElement).getPropertyValue("--mono").trim() || "monospace";
const fontSize = 11.6;
const lineHeight = 1;
/** The padding around the grid, in CSS pixels, as the desktop's (MyGo's padX, padY). */
const padX = 6, padY = 4;
/** The cursor blinks every 600 ms while the terminal has the focus, as on the desktop. */
const blinkPeriod = 600;

let g: Ghostty;
let control: Control | undefined;
let token: string | undefined;
let open: TerminalView | undefined;
/** The theme chosen in this browser, over the desktop's colours; none keeps the host's. */
let chosen: Theme | undefined;

/** The page's chrome follows the terminal's background: light for a light one. */
function scheme(bg: [number, number, number]) {
  document.documentElement.dataset.scheme = isLight(bg) ? "light" : "dark";
}

function status(s: string) {
  $("status").textContent = s;
}

function describe(e: unknown): string {
  return e instanceof Error ? e.message : String(e);
}

async function main() {
  status("Loading libghostty-vt…");
  try {
    // The font too: the cells are measured from it.
    const faces = ["400", "700", "italic 400", "italic 700"].map((v) => document.fonts.load(`${v} 12px "JetBrains Mono"`));
    // The themes load with it, so that the first frame has the chosen colours.
    let themes: ThemeEntry[];
    [g, themes] = await Promise.all([Ghostty.load("ghostty-vt.wasm"), loadThemes(), Promise.allSettled(faces)]).then((r) => [r[0], r[1]]);
    const pick = (t?: ThemeEntry) => {
      chosen = t && themeColours(t);
      scheme(chosen?.background ?? desktopTheme().background!);
      open?.setChosen(chosen);
    };
    pick(themes.find((t) => t.name === chosenName()));
    if (themes.length) mountPicker($("themes"), themes, pick, () => pane.focus());
  } catch (e) {
    status(`Could not load libghostty-vt: ${describe(e)}`);
    return;
  }
  connect();
}

/** Opens the control connection, again after it drops. */
async function connect() {
  status("Connecting…");
  let c: Control;
  try {
    c = await Control.open(token);
  } catch (e) {
    status(`Could not connect: ${describe(e)}. Trying again…`);
    setTimeout(connect, 3000);
    return;
  }
  control = c;
  token = c.reconnectToken;
  c.onEvent = (e) => {
    if (["session_created", "session_destroyed", "session_view_changed", "session_label_changed"].includes(e.type)) relist();
  };
  c.onClose = (reason) => {
    if (control !== c) return;
    control = undefined;
    status(`Disconnected (${reason}). Reconnecting…`);
    setTimeout(connect, 3000);
  };
  try {
    await c.call("session.bootstrap", { event_scope: "all" });
    await list();
    status("Connected");
    open?.start();
  } catch (e) {
    status(`Connected, but the sessions could not be read: ${describe(e)}`);
  }
}

let listing: Promise<void> | undefined;
let listAgain = false;

/** Reads the sessions again: one read at a time, repeated when asked meanwhile. */
function relist() {
  if (listing) {
    listAgain = true;
    return;
  }
  listing = list().finally(() => {
    listing = undefined;
    if (listAgain) {
      listAgain = false;
      relist();
    }
  });
}

/** Lists each session's windows, and each window's terminals. */
async function list() {
  const c = control;
  if (!c) return;
  const sessions: any[] = (await c.call("session.list")).payload.sessions ?? [];
  const nav = document.createElement("nav");
  for (const s of sessions) {
    let v: any;
    try {
      v = (await c.call("session.attach", { session_id: s.session_id })).payload.view;
    } catch {
      continue;
    }
    const h = document.createElement("h2");
    h.textContent = s.label || v.label || "Session";
    nav.append(h);
    for (const w of v.windows ?? []) {
      const blocks: string[] = (w.layers ?? []).flatMap((l: any) => (l.blocks ?? []).map((b: any) => b.block_id));
      blocks.forEach((block, i) => {
        const b = document.createElement("button");
        b.className = "block";
        b.dataset.block = block;
        b.textContent = (w.label || "Window") + (blocks.length > 1 ? ` · ${i + 1}` : "");
        if (open?.block === block) b.classList.add("open");
        b.onclick = () => show(s.session_id, block);
        nav.append(b);
        c.callBlock("title", s.session_id, block).then(
          (r) => r.title && (b.textContent = r.title + (blocks.length > 1 ? ` · ${i + 1}` : "")),
          () => {},
        );
      });
    }
  }
  if (!nav.children.length) nav.textContent = "No sessions on this machine.";
  $("list").replaceChildren(nav);
}

function show(session: string, block: string) {
  if (open?.block === block) return pane.focus();
  open?.close();
  open = new TerminalView(session, block);
  for (const b of document.querySelectorAll<HTMLElement>("button.block")) b.classList.toggle("open", b.dataset.block === block);
  open.start();
  pane.focus();
}

/** One block's terminal: its stream fed to libghostty-vt, drawn on the canvas, the keys typed written back. */
class TerminalView {
  private term?: Terminal;
  private data?: DataStream;
  private stream?: TerminalStream;
  private closed = false;
  private generation = 0;
  private font!: Font;
  /** Set when the fonts loaded since the font was measured. */
  fontStale = false;
  /** The grid's top left on the canvas, in device pixels. */
  private ox = 0;
  private oy = 0;
  /** The colours of the stream's last theme message. */
  private host: Theme = {};
  /** The colours over the desktop's: the chosen theme, until a theme message replaces it. */
  private theme: Theme = chosen ?? {};
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

  constructor(readonly session: string, readonly block: string) {
    this.layout();
  }

  /** Opens the stream from the start: a ticket, a HELLO, a new snapshot. */
  async start() {
    const c = control;
    if (!c || this.closed) return;
    const gen = ++this.generation;
    this.data?.close();
    status("Opening the terminal…");
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
        if (code === 4000) status("The window closed.");
        else {
          status(`The terminal's connection dropped (${code}). Reopening…`);
          setTimeout(() => gen === this.generation && this.start(), 1000);
        }
      };
      await d.open(ticket);
      if (gen !== this.generation) return d.close();
      this.stream = new TerminalStream(d.nonce);
      status("Connected");
    } catch (e) {
      if (gen === this.generation) status(`Could not open the terminal: ${describe(e)}`);
    }
  }

  private receive(bytes: Uint8Array) {
    for (const item of this.stream!.feed(bytes)) {
      switch (item.kind) {
        case "snapshot":
          this.term?.free();
          try {
            this.term = Terminal.fromSnapshot(g, item.data);
          } catch (e) {
            this.term = undefined;
            status(`The terminal's snapshot could not be read: ${describe(e)}`);
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
        if (m.owner === control?.clientID) {
          this.claiming = false;
          this.stale = undefined;
        } else if (this.claiming) {
          this.stale = m;
        } else {
          this.resizeTo(m);
        }
        break;
      case "child_exited":
        status(`The program exited (${m.exit_code ?? 0}).`);
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
      await control?.call("session.own_block", { session_id: this.session, block_ids: [this.block], mode: "take" });
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
   * Sizes the canvas to the pane in whole device pixels, and the terminal
   * to the canvas less its padding, at the device's pixel ratio.
   */
  layout(device?: { width: number; height: number }) {
    const scale = window.devicePixelRatio || 1;
    const r = pane.getBoundingClientRect();
    canvas.width = Math.max(1, device?.width ?? Math.round(r.width * scale));
    canvas.height = Math.max(1, device?.height ?? Math.round(r.height * scale));
    canvas.style.width = `${canvas.width / scale}px`;
    canvas.style.height = `${canvas.height / scale}px`;
    if (this.font?.scale !== scale || this.fontStale) {
      this.font = new Font(fontFamily, fontSize, scale, lineHeight);
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
    control
      ?.callBlock("resize", this.session, this.block, {
        columns: s.cols, rows: s.rows, cell_width_px: this.font.w, cell_height_px: this.font.h,
      })
      .catch((e) => console.warn("resize", e));
  }

  /** Draws on the next frame what changed since the last. */
  redraw() {
    if (this.frame !== undefined) return;
    this.frame = requestAnimationFrame(() => {
      this.frame = undefined;
      if (!this.term || this.closed) return;
      const focused = document.activeElement === pane && document.hasFocus();
      // The padding takes the background too, as a program may change it (OSC 11).
      const bg = `rgb(${this.term.background().join(",")})`;
      if (this.full || bg !== this.painted) {
        this.full = true;
        this.painted = ctx.fillStyle = pane.style.background = bg;
        scheme(this.term.background());
        ctx.fillRect(0, 0, canvas.width, canvas.height);
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
  wheel(e: WheelEvent) {
    const t = this.term;
    if (!t || e.ctrlKey) return;
    e.preventDefault();
    const rowPx = this.font.h / this.font.scale;
    const dy = e.deltaMode === 1 ? e.deltaY * rowPx : e.deltaMode === 2 ? e.deltaY * pane.clientHeight : e.deltaY;
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

  key(e: KeyboardEvent) {
    if (!this.term || e.isComposing) return;
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
      await control?.callBlock("write", this.session, this.block, { data: new TextDecoder().decode(all) });
    } catch (e) {
      if (e instanceof RexError && e.code === "offline") this.typed.unshift(all);
      else console.warn("dropped a write", e);
    }
    this.typing = false;
    this.write();
  }

  close() {
    this.closed = true;
    this.generation++;
    this.data?.close();
    if (this.frame !== undefined) cancelAnimationFrame(this.frame);
    clearTimeout(this.blinkTimer);
    this.term?.free();
    this.term = undefined;
    control?.call("session.disown_block", { session_id: this.session, block_ids: [this.block] }).catch(() => {});
  }
}

pane.addEventListener("keydown", (e) => open?.key(e));
pane.addEventListener("wheel", (e) => open?.wheel(e), { passive: false });
for (const t of [pane, window]) {
  t.addEventListener("focus", () => open?.wake());
  t.addEventListener("blur", () => open?.redraw());
}
// The canvas takes the pane's size in device pixels where the browser tells it.
new ResizeObserver((entries) => {
  const d = entries[0].devicePixelContentBoxSize?.[0];
  open?.layout(d && { width: d.inlineSize, height: d.blockSize });
}).observe(pane, { box: "device-pixel-content-box" });
// A change of the pixel ratio, as when the window moves to another display,
// may leave the pane's CSS size as it was.
(function watchScale() {
  const q = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
  q.addEventListener("change", () => {
    open?.layout();
    watchScale();
  }, { once: true });
})();
// A font that loads late changes the cells.
document.fonts.addEventListener("loadingdone", () => {
  if (!open) return;
  open.fontStale = true;
  open.layout();
});

main();
