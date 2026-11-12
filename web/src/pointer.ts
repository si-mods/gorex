// The pointer over a pane's grid, as the iOS app and Ghostty take it
// (ios/GoRex/Terminal/TerminalView+Selection.swift): a mouse's press and
// drag select by cell, a double click by word, a triple by line, and past
// the top or bottom edge the scrollback scrolls on; a finger's long press
// selects the word and its drag extends it by words, two handles move the
// ends, and a small menu copies. libghostty-vt selects (Terminal's
// press, drag, autoscrollTick...), as Ghostty's own gesture does. A
// program that tracks the mouse gets the clicks, drags, motion and wheel,
// encoded for its modes; Shift then selects instead, and is not reported.
// Ctrl (Cmd on the Mac) over a link underlines it, and a click opens it,
// as on the desktop (links.ts): a URL in a new tab, a host's file with
// files.ts.
import { Mods } from "./ghostty";
import { icon } from "./hosts";
import { localize, Msg, t } from "./i18n";
import { mac } from "./keys";
import { Link } from "./links";
import { prefs } from "./prefs";
import { Autoscroll, Geometry, MouseReport, MouseSize, Pointer, Terminal } from "./terminal";

/** What the pointer needs of its pane. */
export interface PointerHost {
  term(): Terminal | undefined;
  /** The cell's size and the grid's top left on the canvas, in device pixels, and the device's pixel ratio. */
  grid(): { w: number; h: number; ox: number; oy: number; scale: number };
  /** Whether the pane has the keyboard. */
  focused(): boolean;
  /** Draws again: everything when full, as when the selection changed. */
  redraw(full: boolean): void;
  /** Writes bytes to the program, as typed keys go. */
  send(b: Uint8Array): void;
  /** Types text pasted from the clipboard, as the program's paste mode asks. */
  paste(text: string): void;
  status(s: Msg): void;
  /** Whether the host sends files (gorex.download): its file paths are links. */
  files(): boolean;
  /** Opens a file of the host, a link's path. */
  openFile(path: string): void;
}

/** A long press is a finger held this long without moving more than slop, as UIKit's. */
const longPressMs = 500;
const slop = 10;
/** How often a drag past an edge scrolls a row, as Ghostty suggests. */
const autoscrollMs = 15;
/** A handle's knob, in CSS pixels, as iOS's SelectionHandle. */
const knob = 10;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** A key event's or pointer event's modifiers, as Ghostty's Mods. */
function modsOf(e: { shiftKey: boolean; ctrlKey: boolean; altKey: boolean; metaKey: boolean }): number {
  return (e.shiftKey ? Mods.shift : 0) | (e.ctrlKey ? Mods.ctrl : 0) | (e.altKey ? Mods.alt : 0) | (e.metaKey ? Mods.super : 0);
}

/** Whether an event holds the key that opens links: Cmd on the Mac, Ctrl elsewhere, as the desktop's ui.Cmd. */
const linkKey = (e: { ctrlKey: boolean; metaKey: boolean }) => (mac ? e.metaKey : e.ctrlKey);

/**
 * Whether the link key is held, as the last key or mouse event had it, and
 * the panes the mouse is over: pressing or letting go of the key with the
 * pointer still underlines a link or takes the underline away, as on the
 * desktop.
 */
let linkHeld = false;
const hovered = new Set<PointerInput>();

function holdLinkKey(held: boolean) {
  if (held === linkHeld) return;
  linkHeld = held;
  for (const p of hovered) p.hover(true);
}

window.addEventListener("keydown", (e) => holdLinkKey(linkKey(e)), { capture: true });
window.addEventListener("keyup", (e) => holdLinkKey(linkKey(e)), { capture: true });
window.addEventListener("blur", () => holdLinkKey(false));

/** A DOM button (0 left, 1 middle, 2 right) as Ghostty's (1 left, 3 middle, 2 right); 0 for others. */
const buttonOf = (b: number) => ({ 0: 1, 1: 3, 2: 2 } as Record<number, number>)[b] ?? 0;

/**
 * Writes text to the clipboard: the Clipboard API where the page has it,
 * else, as on a LAN page over http (no secure context, no
 * navigator.clipboard), a hidden text area copied with execCommand, which
 * works only within the click or key that asked. The keyboard goes back
 * where it was.
 */
export async function writeClipboard(text: string): Promise<boolean> {
  if (window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // Refused: try the old way, while the gesture may still count.
    }
  }
  const was = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.readOnly = true;
  ta.setAttribute("aria-hidden", "true");
  ta.style.cssText = "position:fixed;left:0;top:0;width:1px;height:1px;padding:0;border:0;opacity:0;pointer-events:none";
  document.body.append(ta);
  ta.focus({ preventScroll: true });
  ta.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  ta.remove();
  was?.focus({ preventScroll: true });
  return ok;
}

type Touch = {
  id: number;
  x0: number;
  y0: number;
  lastY: number;
  /** wait: maybe a tap or a long press; scroll: a swipe; select: after a long press. */
  mode: "wait" | "scroll" | "select";
  timer?: number;
  /** The pane had no keyboard as the finger came down: a tap only gives it. */
  focusOnly: boolean;
};

export class PointerInput {
  private readonly menu = el("div", "select-menu");
  private readonly copyButton = el("button", "select-menu-item");
  private readonly pasteButton = el("button", "select-menu-item");
  private readonly handles = { start: el("div", "select-handle start"), end: el("div", "select-handle end") };
  /**
   * The mouse button down, and what it does: selects, or goes to the
   * program (report), or only focused the pane, or opened a link (link),
   * whose moves and release then go to no one.
   */
  private down?: { id: number; button: number; kind: "select" | "report" | "focus" | "link" };
  /** Where the mouse is over the canvas, in client pixels; none while it is elsewhere. */
  private over?: { clientX: number; clientY: number };
  /** The link the link key is held over, underlined, which a click opens. */
  private link?: Link;
  private touch?: Touch;
  /** A handle being dragged: which, and where the finger took it from the stem's middle, in CSS pixels. */
  private steering?: { id: number; first: boolean; dx: number; dy: number };
  /** Where the pointer was last, for the autoscroll's ticks. */
  private last?: Pointer;
  private autoscrollTimer?: number;
  /** Whether the handles show, after a finger's selection. */
  private showsHandles = false;
  /** Wheel scrolling not yet a whole row. */
  private scrolled = 0;
  private lastPointerTouch = false;

  constructor(private readonly canvas: HTMLCanvasElement, private readonly layer: HTMLElement, private readonly host: PointerHost) {
    const b = this.copyButton;
    b.type = "button";
    b.append(icon("copy"), localize(el("span", ""), (e) => (e.textContent = t("commonCopy"))));
    // The pane keeps the keyboard: a press on the menu does not take it.
    b.addEventListener("pointerdown", (e) => e.preventDefault());
    b.addEventListener("click", () => {
      this.copy();
      this.hideMenu();
    });
    // Paste reads the clipboard, which a page may only in a secure context (and a browser may ask first).
    const pb = this.pasteButton;
    pb.type = "button";
    pb.append(icon("clipboard-paste"), localize(el("span", ""), (e) => (e.textContent = t("commonPaste"))));
    pb.addEventListener("pointerdown", (e) => e.preventDefault());
    pb.addEventListener("click", () => {
      this.hideMenu();
      navigator.clipboard.readText().then(
        (text) => this.host.paste(text),
        () => this.host.status(() => t("pasteFailed")),
      );
    });
    pb.hidden = !navigator.clipboard?.readText;
    this.menu.setAttribute("role", "menu");
    b.setAttribute("role", "menuitem");
    pb.setAttribute("role", "menuitem");
    this.menu.append(b, pb);
    this.menu.hidden = true;
    for (const [first, h] of [[true, this.handles.start], [false, this.handles.end]] as const) {
      h.hidden = true;
      h.addEventListener("pointerdown", (e) => this.steerDown(e, first));
      h.addEventListener("pointermove", (e) => this.steerMove(e));
      h.addEventListener("pointerup", (e) => this.steerUp(e));
      h.addEventListener("pointercancel", (e) => this.steerUp(e));
    }
    layer.append(this.handles.start, this.handles.end, this.menu);

    canvas.addEventListener("pointerdown", (e) => this.pointerDown(e));
    canvas.addEventListener("pointermove", (e) => this.pointerMove(e));
    canvas.addEventListener("pointerup", (e) => this.pointerUp(e));
    canvas.addEventListener("pointercancel", (e) => this.pointerCancel(e));
    canvas.addEventListener("pointerleave", (e) => {
      this.host.term()?.resetMouse();
      if (e.pointerType === "touch") return;
      this.over = undefined;
      hovered.delete(this);
      this.hover(true);
    });
    canvas.addEventListener("contextmenu", (e) => this.contextMenu(e));
    // A press anywhere else takes the menu away.
    document.addEventListener("pointerdown", (e) => {
      if (!this.menu.hidden && !this.menu.contains(e.target as Node)) this.hideMenu();
    }, { capture: true });
  }

  // Where the pointer is.

  /** The canvas position of an event, in device pixels, and the viewport cell under it, clamped to the grid. */
  private at(e: { clientX: number; clientY: number }, t: Terminal): Pointer {
    const r = this.canvas.getBoundingClientRect();
    const { w, h, ox, oy, scale } = this.host.grid();
    const x = (e.clientX - r.left) * scale, y = (e.clientY - r.top) * scale;
    const { cols, rows } = t.size();
    const clamp = (v: number, n: number) => Math.max(0, Math.min(n - 1, v));
    return { col: clamp(Math.floor((x - ox) / w), cols), row: clamp(Math.floor((y - oy) / h), rows), x, y };
  }

  private geometry(t: Terminal): Geometry {
    const { w, ox } = this.host.grid();
    return { cols: t.size().cols, cellW: w, padLeft: ox, height: this.canvas.height };
  }

  private mouseSize(t: Terminal): MouseSize {
    const { w, h, ox, oy } = this.host.grid();
    const { cols, rows } = t.size();
    const c = this.canvas;
    return {
      width: c.width, height: c.height, cellW: w, cellH: h, padTop: oy, padLeft: ox,
      padRight: Math.max(0, c.width - ox - cols * w), padBottom: Math.max(0, c.height - oy - rows * h),
    };
  }

  /** Sends a mouse event to the program, if its modes take it. */
  private report(t: Terminal, m: MouseReport) {
    const b = t.encodeMouse(m, this.mouseSize(t));
    if (b) this.host.send(b);
  }

  /**
   * Whether a press selects rather than going to the program: always
   * while no program tracks the mouse; with Shift while one does, as
   * Ghostty and the iOS app have it. Shift is then not reported.
   */
  private selects(t: Terminal, e: { shiftKey: boolean }): boolean {
    return !t.mouseTracking() || e.shiftKey;
  }

  // Links: the link key held over one underlines it (the pane draws
  // linkShown), with a hand for the pointer.

  /** The link under the mouse, as the desktop's linkUnder: none without the link key, or while a press selects or goes to the program. */
  private linkUnder(t: Terminal): Link | undefined {
    if (!linkHeld || !this.over || (this.down && this.down.kind !== "link")) return undefined;
    const p = this.at(this.over, t);
    return t.linkAt(p.col, p.row, this.host.files());
  }

  /**
   * Finds the link under the mouse again, as the key, the mouse or the text
   * under it changed; draws again when it is another (redraw), and reports
   * whether it is.
   */
  hover(redraw: boolean): boolean {
    const t = this.host.term();
    const l = t && this.linkUnder(t);
    const was = this.link;
    if (l?.url === was?.url && l?.row === was?.row && l?.x0 === was?.x0 && l?.x1 === was?.x1) return false;
    this.link = l;
    this.canvas.style.cursor = l ? "pointer" : "";
    if (redraw) this.host.redraw(true);
    return true;
  }

  /** The cells to underline: the link the key is held over. */
  linkShown(): Link | undefined {
    return this.link;
  }

  /** Where the mouse is, and the link key, from a mouse event over the canvas. */
  private track(e: PointerEvent) {
    this.over = { clientX: e.clientX, clientY: e.clientY };
    hovered.add(this);
    holdLinkKey(linkKey(e));
  }

  // The mouse (and a pen).

  private pointerDown(e: PointerEvent) {
    this.lastPointerTouch = e.pointerType === "touch";
    if (e.pointerType === "touch") return this.touchDown(e);
    const t = this.host.term();
    if (!t || this.down) return;
    this.hideMenu();
    this.hideHandles();
    this.track(e);
    const p = this.at(e, t);
    const button = buttonOf(e.button);
    // The link key and the primary button over a link: it opens, and the
    // press is the link's, before a program that tracks the mouse sees it.
    // Over a hyperlink's cells with no link to open (a program's autolink
    // over prose, or a scheme the page does not open), the press goes to no
    // one either, as on the desktop.
    if (e.button === 0 && linkKey(e)) {
      this.hover(true);
      const link = this.link;
      if (link || t.hyperlinkAt(p.col, p.row) !== "") {
        this.canvas.setPointerCapture(e.pointerId);
        this.down = { id: e.pointerId, button, kind: "link" };
        if (link?.path !== undefined) this.host.openFile(link.path);
        else if (link) window.open(link.url, "_blank", "noopener,noreferrer");
        return;
      }
    }
    // A click into a pane without the keyboard only gives it the
    // keyboard, as Ghostty's split focus does: the program gets nothing.
    const focusOnly = !this.host.focused();
    if (!this.selects(t, e)) {
      if (!button) return;
      this.canvas.setPointerCapture(e.pointerId);
      this.down = { id: e.pointerId, button, kind: focusOnly ? "focus" : "report" };
      if (!focusOnly) this.report(t, { action: "PRESS", button, mods: modsOf(e), x: p.x, y: p.y, held: true });
      return;
    }
    if (e.button !== 0) return; // The right button opens the menu (contextmenu).
    this.canvas.setPointerCapture(e.pointerId);
    this.down = { id: e.pointerId, button, kind: "select" };
    this.last = p;
    t.press(p, e.timeStamp, this.host.grid().w);
    this.host.redraw(true);
  }

  private pointerMove(e: PointerEvent) {
    if (e.pointerType === "touch") return this.touchMove(e);
    const t = this.host.term();
    if (!t) return;
    this.track(e);
    this.hover(true);
    const p = this.at(e, t);
    const d = this.down;
    if (d?.id === e.pointerId && d.kind === "select") {
      this.last = p;
      this.follow(t, t.drag(p, this.geometry(t)));
      this.host.redraw(true);
      return;
    }
    if (d?.kind === "focus" || d?.kind === "link") return;
    // Motion, with the button held or not: the encoder keeps what the
    // modes ask (1002 with a button, 1003 always), once a cell.
    if (!t.mouseTracking() || (!d && e.shiftKey)) return;
    const button = d?.kind === "report" ? d.button : 0;
    this.report(t, { action: "MOTION", button, mods: modsOf(e) & ~Mods.shift, x: p.x, y: p.y, held: !!button });
  }

  private pointerUp(e: PointerEvent) {
    if (e.pointerType === "touch") return this.touchUp(e);
    const d = this.down;
    const t = this.host.term();
    if (d?.id !== e.pointerId) return;
    this.down = undefined;
    this.stopAutoscroll();
    if (!t) return;
    const p = this.at(e, t);
    if (d.kind === "report") {
      this.report(t, { action: "RELEASE", button: d.button, mods: modsOf(e) & ~Mods.shift, x: p.x, y: p.y, held: false });
    } else if (d.kind === "select") {
      t.release(p);
      this.host.redraw(true);
      this.copyOnSelect(t);
    }
  }

  private pointerCancel(e: PointerEvent) {
    if (e.pointerType === "touch") return this.touchCancel();
    if (this.down?.id !== e.pointerId) return;
    if (this.down.kind === "select") this.host.term()?.resetGesture();
    this.down = undefined;
    this.stopAutoscroll();
  }

  /** The right button, or a long press the browser reports: the menu, unless the program takes the click. */
  private contextMenu(e: MouseEvent) {
    e.preventDefault();
    const t = this.host.term();
    if (!t || this.lastPointerTouch) return;
    if (!this.selects(t, e)) return;
    const r = this.layer.getBoundingClientRect();
    this.showMenu(e.clientX - r.left, e.clientY - r.top, false);
  }

  // A drag past the top or bottom edge scrolls on, a row a tick.

  private follow(t: Terminal, a: Autoscroll) {
    if (a === "none") return this.stopAutoscroll();
    if (this.autoscrollTimer !== undefined) return;
    this.autoscrollTimer = window.setInterval(() => {
      const p = this.last;
      if (!p || this.host.term() !== t) return this.stopAutoscroll();
      if (t.autoscrollTick(p, this.geometry(t)) === "none") this.stopAutoscroll();
      this.host.redraw(true);
    }, autoscrollMs);
  }

  private stopAutoscroll() {
    clearInterval(this.autoscrollTimer);
    this.autoscrollTimer = undefined;
  }

  // A finger.

  private touchDown(e: PointerEvent) {
    const t = this.host.term();
    if (!t) return;
    if (this.touch) {
      // A second finger: neither a tap nor a selection.
      this.touchCancel();
      return;
    }
    this.hideMenu();
    this.canvas.setPointerCapture(e.pointerId);
    const touch: Touch = { id: e.pointerId, x0: e.clientX, y0: e.clientY, lastY: e.clientY, mode: "wait", focusOnly: !this.host.focused() };
    touch.timer = window.setTimeout(() => this.longPress(touch, e.timeStamp + longPressMs), longPressMs);
    this.touch = touch;
  }

  /** A finger held still: the word under it selected, as iOS's long press, with a drag to follow. */
  private longPress(touch: Touch, time: number) {
    const t = this.host.term();
    if (this.touch !== touch || !t) return;
    touch.mode = "select";
    this.hideHandles();
    const p = this.at({ clientX: touch.x0, clientY: touch.y0 }, t);
    this.last = p;
    t.longPress(p, time, this.host.grid().w);
    navigator.vibrate?.(10);
    this.host.redraw(true);
  }

  private touchMove(e: PointerEvent) {
    const touch = this.touch;
    const t = this.host.term();
    if (!touch || touch.id !== e.pointerId || !t) return;
    if (touch.mode === "wait" && Math.hypot(e.clientX - touch.x0, e.clientY - touch.y0) > slop) {
      clearTimeout(touch.timer);
      touch.mode = "scroll";
    }
    if (touch.mode === "scroll") {
      // The content follows the finger: up shows what came later.
      this.scrollPixels(t, (touch.lastY - e.clientY), this.at(e, t), false);
    } else if (touch.mode === "select") {
      const p = this.at(e, t);
      this.last = p;
      this.follow(t, t.drag(p, this.geometry(t)));
      this.host.redraw(true);
    }
    touch.lastY = e.clientY;
  }

  private touchUp(e: PointerEvent) {
    const touch = this.touch;
    const t = this.host.term();
    if (!touch || touch.id !== e.pointerId) return;
    clearTimeout(touch.timer);
    this.touch = undefined;
    this.stopAutoscroll();
    if (!t) return;
    const p = this.at(e, t);
    if (touch.mode === "select") {
      t.release(p);
      this.host.redraw(true);
      this.showSelection();
      this.copyOnSelect(t);
    } else if (touch.mode === "wait") {
      // A tap: it takes a selection away; else, to a program that tracks
      // the mouse, it is a click, unless it only gave the pane the keyboard.
      if (t.hasSelection()) {
        t.clearSelection();
        this.hideHandles();
        this.host.redraw(true);
      } else if (t.mouseTracking() && !touch.focusOnly) {
        const at = this.at({ clientX: touch.x0, clientY: touch.y0 }, t);
        this.report(t, { action: "PRESS", button: 1, mods: 0, x: at.x, y: at.y, held: true });
        this.report(t, { action: "RELEASE", button: 1, mods: 0, x: at.x, y: at.y, held: false });
      }
    }
  }

  private touchCancel() {
    const touch = this.touch;
    if (!touch) return;
    clearTimeout(touch.timer);
    if (touch.mode === "select") this.host.term()?.resetGesture();
    this.touch = undefined;
    this.stopAutoscroll();
  }

  // The wheel, and a finger's swipe.

  /** A wheel event over the grid. */
  wheel(e: WheelEvent) {
    const t = this.host.term();
    if (!t || e.ctrlKey) return;
    e.preventDefault();
    this.hideMenu();
    const rowPx = this.host.grid().h / this.host.grid().scale;
    const dy = e.deltaMode === 1 ? e.deltaY * rowPx : e.deltaMode === 2 ? e.deltaY * this.layer.clientHeight : e.deltaY;
    this.scrollPixels(t, dy, this.at(e, t), e.shiftKey, modsOf(e));
  }

  /**
   * Scrolls by CSS pixels (down positive), as Ghostty's wheel: with Shift
   * the scrollback always; to a program that tracks the mouse, wheel
   * buttons at the pointer; on the alternate screen with alternate scroll
   * (1007) on, the arrow keys; else the scrollback.
   */
  private scrollPixels(t: Terminal, dy: number, at: Pointer, shift: boolean, mods = 0) {
    const rowPx = this.host.grid().h / this.host.grid().scale;
    this.scrolled += dy / rowPx;
    const rows = Math.trunc(this.scrolled);
    this.scrolled -= rows;
    if (rows === 0) return;
    if (!shift && t.mouseTracking()) {
      // The selection, made with Shift, goes, as Ghostty clears it.
      if (t.hasSelection()) {
        t.clearSelection();
        this.hideHandles();
        this.host.redraw(true);
      }
      for (let i = 0; i < Math.abs(rows); i++) {
        this.report(t, { action: "PRESS", button: rows < 0 ? 4 : 5, mods: mods & ~Mods.shift, x: at.x, y: at.y, held: false });
      }
      return;
    }
    if (!shift && t.altScreen() && t.mode(1007)) {
      const key = rows < 0 ? "ArrowUp" : "ArrowDown";
      const b = t.encodeKey(new KeyboardEvent("keydown", { code: key, key }));
      if (b) for (let i = 0; i < Math.abs(rows); i++) this.host.send(b);
      return;
    }
    t.scrollBy(rows);
    this.host.redraw(false);
  }

  // Handles, as iOS's SelectionHandle: a stem a line high at the outer edge of an end cell, a knob above the first or below the last.

  private steerDown(e: PointerEvent, first: boolean) {
    const t = this.host.term();
    if (!t || this.steering) return;
    e.preventDefault();
    e.stopPropagation();
    if (!t.beginMoveEnd(first)) return;
    const h = first ? this.handles.start : this.handles.end;
    h.setPointerCapture(e.pointerId);
    this.hideMenu();
    // The stem's middle, from the handle's place (its box less the wider border it is taken by).
    const l = this.layer.getBoundingClientRect();
    const left = l.left + parseFloat(h.style.left), top = l.top + parseFloat(h.style.top), height = parseFloat(h.style.height);
    const stemY = first ? top + (knob + height) / 2 : top + (height - knob) / 2;
    this.steering = { id: e.pointerId, first, dx: e.clientX - (left + knob), dy: e.clientY - stemY };
  }

  private steerMove(e: PointerEvent) {
    const s = this.steering;
    const t = this.host.term();
    if (!s || s.id !== e.pointerId || !t) return;
    const r = this.canvas.getBoundingClientRect();
    const { w, h, ox, oy, scale } = this.host.grid();
    const x = (e.clientX - s.dx - r.left) * scale, y = (e.clientY - s.dy - r.top) * scale;
    const { cols, rows } = t.size();
    // The stem stands at the first cell's left or the last cell's right.
    const col = Math.round((x - ox) / w) - (s.first ? 0 : 1);
    const row = Math.floor((y - oy) / h);
    t.moveEnd(Math.max(0, Math.min(cols - 1, col)), Math.max(0, Math.min(rows - 1, row)));
    this.host.redraw(true);
  }

  private steerUp(e: PointerEvent) {
    const s = this.steering;
    if (!s || s.id !== e.pointerId) return;
    this.steering = undefined;
    const t = this.host.term();
    this.showSelection();
    if (t && e.type === "pointerup") this.copyOnSelect(t);
  }

  /** The handles at the selection's ends and the menu above it, after a finger made it. */
  private showSelection() {
    const t = this.host.term();
    if (!t?.hasSelection()) return this.hideHandles();
    this.showsHandles = true;
    const box = this.place(t);
    if (box) this.showMenu((box.left + box.right) / 2, box.top, true, box.bottom);
  }

  /** Puts the handles at the selection's ends, each hidden when its end is out of view; the box of the selection in view, in CSS pixels. */
  private place(t: Terminal): { left: number; right: number; top: number; bottom: number } | undefined {
    const ends = t.selectionEnds();
    if (!ends) return undefined;
    const { w, h, ox, oy, scale } = this.host.grid();
    const { rows } = t.size();
    const lh = h / scale;
    const cx = (col: number) => (ox + col * w) / scale;
    const cy = (row: number) => (oy + row * h) / scale;
    const put = (handle: HTMLElement, x: number, row: number, first: boolean) => {
      const shown = row >= 0 && row < rows;
      handle.hidden = !(shown && this.showsHandles);
      if (!shown) return;
      handle.style.left = `${x - knob}px`;
      handle.style.top = `${first ? cy(row) - knob : cy(row)}px`;
      handle.style.height = `${lh + knob}px`;
    };
    put(this.handles.start, cx(ends.start.x), ends.start.y, true);
    put(this.handles.end, cx(ends.end.x + 1), ends.end.y, false);
    const top = Math.max(0, ends.start.y), bottom = Math.min(rows - 1, ends.end.y);
    if (top > bottom) return undefined;
    const one = ends.start.y === ends.end.y;
    return {
      left: one ? cx(ends.start.x) : cx(0),
      right: one ? cx(ends.end.x + 1) : cx(t.size().cols),
      top: cy(top) - knob,
      bottom: cy(bottom + 1) + knob,
    };
  }

  private hideHandles() {
    this.showsHandles = false;
    this.handles.start.hidden = this.handles.end.hidden = true;
    this.hideMenu();
  }

  // The menu, as iOS's edit menu: a Copy button.

  /** Shows the menu at x, y in the layer: centred above that point (with a box's bottom to go under when there is no room), or at the pointer. */
  private showMenu(x: number, y: number, above: boolean, below = y) {
    const t = this.host.term();
    this.copyButton.disabled = !t?.hasSelection();
    const m = this.menu;
    m.hidden = false;
    const lw = this.layer.clientWidth, lh = this.layer.clientHeight;
    const mw = m.offsetWidth, mh = m.offsetHeight;
    let left = above ? x - mw / 2 : x;
    let top = above ? y - mh - 6 : y;
    if (above && top < 4) top = below + 6;
    left = Math.max(4, Math.min(lw - mw - 4, left));
    top = Math.max(4, Math.min(lh - mh - 4, top));
    m.style.left = `${left}px`;
    m.style.top = `${top}px`;
  }

  hideMenu() {
    this.menu.hidden = true;
  }

  // What the pane asks.

  /** Copies the selection; false when there is none. */
  copy(): boolean {
    const text = this.host.term()?.selectionText();
    if (!text) return false;
    writeClipboard(text).then((ok) => {
      if (!ok) this.host.status(() => t("copyFailed"));
    });
    return true;
  }

  private copyOnSelect(t: Terminal) {
    if (prefs.copyOnSelect && t.hasSelection()) this.copy();
  }

  /** A key was typed to the program: the selection goes, as Ghostty clears it on typing. */
  typed() {
    const t = this.host.term();
    this.hideHandles();
    if (t?.hasSelection()) {
      t.clearSelection();
      this.host.redraw(true);
    }
  }

  /** The pane drew: the handles follow the selection as the text moves, and go with it. */
  drawn() {
    if (!this.showsHandles) return;
    const t = this.host.term();
    if (!t?.hasSelection()) return this.hideHandles();
    this.place(t);
  }

  /** A new terminal, or the pane closing: nothing held from the old one. */
  reset() {
    this.stopAutoscroll();
    if (this.touch) clearTimeout(this.touch.timer);
    this.touch = undefined;
    this.down = undefined;
    this.steering = undefined;
    this.hideHandles();
    this.link = undefined;
    this.canvas.style.cursor = "";
  }

  /** The pane closed: it no longer follows the link key. */
  dispose() {
    this.reset();
    hovered.delete(this);
  }
}
