// The image viewer: a dialog over the window, as the Settings dialog (a
// frosted panel over a backdrop that only dims, DESIGN.md), showing an image
// of the host that files.ts fetched and checked. It shows the image fitted
// to the window at first, with its name and size; the buttons, with lucide
// icons, zoom in and out, show it at actual size, download it and close.
// Ctrl+wheel and a trackpad's pinch (which arrives as Ctrl+wheel) zoom at
// the pointer, two fingers zoom on a touch screen, and a drag pans an image
// larger than the window. Escape or a click outside closes it.
import { icon } from "./hosts";
import { formatSize, localize, Msg, msg, onLanguageChange, PlainKey, t } from "./i18n";

/** What a shown image needs of its opener. */
export interface ViewerHost {
  /** Downloads the image's file with the browser, with a ticket of its own. */
  download(): void;
  /** The viewer closed: the keyboard goes back to the pane. */
  closed(): void;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** A file's size in words, in the page's language, as 48 KB. */
export const sizeText = (n: number): string => formatSize(n, 1024);

/** The most pixels the viewer shows once an image is decoded; a larger image is offered as a download. */
export const maxViewPixels = 100_000_000;
/** The zoom's bounds, and a button's step. */
const minScale = 0.01, maxScale = 32, step = Math.SQRT2;
/** The size an SVG without one of its own is drawn at, as a browser's default replaced element. */
const defaultW = 300, defaultH = 150;

export class Viewer {
  private readonly dialog = el("dialog", "viewer");
  private readonly name = el("span", "viewer-name");
  private readonly info = el("span", "viewer-info");
  private readonly zoomText = el("span", "viewer-zoom");
  private readonly stage = el("div", "viewer-stage");
  private readonly img = el("img", "viewer-image");
  private readonly note = el("div", "viewer-note");
  private readonly zoomButtons: HTMLButtonElement[] = [];
  private host?: ViewerHost;
  private url = "";
  /** The image's size, and how it shows: its scale and its top left in the stage. */
  private w = 0;
  private h = 0;
  private scale = 1;
  private x = 0;
  private y = 0;
  /** Whether the image shows (decoded and not too large). */
  private shown = false;
  /** The pointers down on the stage, where they are, and whether one moved since the first came down. */
  private pointers = new Map<number, { x: number; y: number }>();
  private moved = false;
  /** The image's name, and what the info and the note say, kept to say them again in another language. */
  private label = "";
  private infoText: Msg = "";
  private noteText: Msg = "";

  constructor() {
    this.dialog.setAttribute("aria-label", t("viewerImage"));
    const head = el("header", "viewer-head");
    const title = el("div", "viewer-title");
    title.append(this.name, this.info);
    const buttons = el("div", "viewer-buttons");
    const button = (glyph: string, label: PlainKey, run: () => void) => {
      const b = el("button", "pane-action viewer-button");
      b.type = "button";
      localize(b, (b) => {
        b.title = t(label);
        b.setAttribute("aria-label", b.title);
      });
      b.append(icon(glyph));
      b.onclick = run;
      buttons.append(b);
      return b;
    };
    this.zoomButtons.push(
      button("zoom-out", "viewerZoomOut", () => this.zoomBy(1 / step)),
      button("zoom-in", "viewerZoomIn", () => this.zoomBy(step)),
      button("scan", "viewerActualSize", () => this.zoomTo(1)),
    );
    buttons.prepend(this.zoomText);
    button("download", "viewerDownload", () => this.host?.download());
    button("x", "commonClose", () => this.close());
    head.append(title, buttons);
    this.img.alt = "";
    this.img.draggable = false;
    this.note.hidden = true;
    this.stage.append(this.img, this.note);
    this.dialog.append(head, this.stage);

    // A click on the backdrop, outside the box, closes it, as the Settings dialog.
    this.dialog.addEventListener("click", (e) => {
      if (e.target !== this.dialog) return;
      const r = this.dialog.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) this.close();
    });
    // Escape closes it (the dialog's cancel); closed, its image goes.
    this.dialog.addEventListener("close", () => this.closed());
    // Not passive: Ctrl+wheel and a pinch zoom the image, not the page.
    this.stage.addEventListener("wheel", (e) => this.wheel(e), { passive: false });
    this.stage.addEventListener("pointerdown", (e) => this.pointerDown(e));
    this.stage.addEventListener("pointermove", (e) => this.pointerMove(e));
    this.stage.addEventListener("pointerup", (e) => this.pointerUp(e));
    this.stage.addEventListener("pointercancel", (e) => this.pointers.delete(e.pointerId));
    window.addEventListener("resize", () => this.dialog.open && this.place());
    onLanguageChange(() => this.relocalize());
    document.body.append(this.dialog);
  }

  /** Shows an image blob, a checked image of the host, named name. */
  show(blob: Blob, name: string, host: ViewerHost) {
    this.closed();
    this.host = host;
    this.url = URL.createObjectURL(blob);
    this.name.textContent = name;
    this.label = name;
    this.dialog.setAttribute("aria-label", name);
    this.setInfo(() => sizeText(blob.size));
    this.zoomText.textContent = "";
    this.shown = false;
    this.img.hidden = true;
    this.note.hidden = true;
    this.zoomButtons.forEach((b) => (b.disabled = true));
    if (!this.dialog.open) this.dialog.showModal();
    const img = this.img, url = this.url;
    img.onload = () => {
      if (this.url !== url) return;
      const w = img.naturalWidth || defaultW, h = img.naturalHeight || defaultH;
      if (img.naturalWidth * img.naturalHeight > maxViewPixels) return this.refuse(() => t("viewerTooLarge", { width: w, height: h }));
      // Decoded before it shows, so that it does not show in parts.
      img.decode().then(
        () => this.url === url && this.display(w, h, blob.size),
        () => this.url === url && this.refuse(() => t("viewerCantShow")),
      );
    };
    img.onerror = () => this.url === url && this.refuse(() => t("viewerCantShow"));
    img.src = url;
  }

  close() {
    if (this.dialog.open) this.dialog.close();
  }

  private setInfo(m: Msg) {
    this.infoText = m;
    this.info.textContent = msg(m);
  }

  /** The language changed: the info, the note and the zoom again in it. */
  private relocalize() {
    if (!this.label) this.dialog.setAttribute("aria-label", t("viewerImage"));
    this.info.textContent = msg(this.infoText);
    this.note.textContent = msg(this.noteText);
    if (this.shown) this.place();
  }

  /** Shows the image, fitted to the stage. */
  private display(w: number, h: number, bytes: number) {
    this.w = w;
    this.h = h;
    this.shown = true;
    this.img.hidden = false;
    this.setInfo(() => t("viewerInfo", { width: w, height: h, size: sizeText(bytes) }));
    this.zoomButtons.forEach((b) => (b.disabled = false));
    this.scale = this.fit();
    this.place(true);
  }

  /** In place of the image, a note: it is not shown, and the download is offered. */
  private refuse(text: Msg) {
    this.release();
    this.img.hidden = true;
    this.noteText = text;
    this.note.textContent = msg(text);
    this.note.hidden = false;
  }

  /** The image's blob goes: its URL revoked, and the img holds it no longer. */
  private release() {
    if (this.url) URL.revokeObjectURL(this.url);
    this.url = "";
    this.img.onload = this.img.onerror = null;
    this.img.removeAttribute("src");
    this.shown = false;
    this.zoomButtons.forEach((b) => (b.disabled = true));
  }

  private closed() {
    const host = this.host;
    this.host = undefined;
    this.release();
    this.pointers.clear();
    if (host && !this.dialog.open) host.closed();
  }

  /** The scale that fits the whole image in the stage, at most its actual size. */
  private fit(): number {
    const r = this.stage.getBoundingClientRect();
    return Math.min(1, r.width / this.w, r.height / this.h) || 1;
  }

  /**
   * Places the image: centred on an axis it is smaller than the stage on,
   * else kept over the whole stage on it. Centred is where it starts.
   */
  private place(centre = false) {
    if (!this.shown) return;
    const r = this.stage.getBoundingClientRect();
    const w = this.w * this.scale, h = this.h * this.scale;
    if (centre) {
      this.x = (r.width - w) / 2;
      this.y = (r.height - h) / 2;
    }
    this.x = w <= r.width ? (r.width - w) / 2 : Math.min(0, Math.max(r.width - w, this.x));
    this.y = h <= r.height ? (r.height - h) / 2 : Math.min(0, Math.max(r.height - h, this.y));
    this.img.style.width = `${w}px`;
    this.img.style.height = `${h}px`;
    this.img.style.transform = `translate(${this.x}px, ${this.y}px)`;
    this.stage.classList.toggle("pans", w > r.width || h > r.height);
    this.zoomText.textContent = t("percent", { value: Math.round(this.scale * 100) });
  }

  /** Zooms to scale, keeping the stage's point (px, py) still: by default its centre. */
  private zoomTo(scale: number, px?: number, py?: number) {
    if (!this.shown) return;
    const r = this.stage.getBoundingClientRect();
    px ??= r.width / 2;
    py ??= r.height / 2;
    const s = Math.min(maxScale, Math.max(minScale, scale));
    this.x = px - ((px - this.x) * s) / this.scale;
    this.y = py - ((py - this.y) * s) / this.scale;
    this.scale = s;
    this.place();
  }

  private zoomBy(k: number, px?: number, py?: number) {
    this.zoomTo(this.scale * k, px, py);
  }

  /** Ctrl+wheel and a pinch zoom at the pointer; a plain wheel pans. */
  private wheel(e: WheelEvent) {
    e.preventDefault();
    if (!this.shown) return;
    const unit = e.deltaMode === 1 ? 16 : e.deltaMode === 2 ? 400 : 1;
    const r = this.stage.getBoundingClientRect();
    if (e.ctrlKey) {
      // A mouse wheel's notch (a delta of about 100) zooms by 1.2; a pinch
      // comes in small deltas and zooms with the fingers.
      const d = -e.deltaY * unit;
      const by = Math.abs(d) >= 50 ? Math.pow(1.2, d / 100) : Math.exp(d / 100);
      this.zoomBy(by, e.clientX - r.left, e.clientY - r.top);
    } else {
      this.x -= e.deltaX * unit;
      this.y -= e.deltaY * unit;
      this.place();
    }
  }

  // A drag pans; two fingers zoom, around their middle, and pan with it.

  private pointerDown(e: PointerEvent) {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    if (this.pointers.size === 0) this.moved = false;
    this.stage.setPointerCapture(e.pointerId);
    this.pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
  }

  private pointerMove(e: PointerEvent) {
    const was = this.pointers.get(e.pointerId);
    if (!was || !this.shown) return;
    if (Math.hypot(e.clientX - was.x, e.clientY - was.y) > 0) this.moved = true;
    const others = [...this.pointers].filter(([id]) => id !== e.pointerId).map(([, p]) => p);
    if (others.length === 0) {
      this.x += e.clientX - was.x;
      this.y += e.clientY - was.y;
      this.place();
    } else {
      const o = others[0];
      const before = Math.hypot(was.x - o.x, was.y - o.y), after = Math.hypot(e.clientX - o.x, e.clientY - o.y);
      const r = this.stage.getBoundingClientRect();
      // The middle moves by half the finger's move: the image follows it.
      this.x += (e.clientX - was.x) / 2;
      this.y += (e.clientY - was.y) / 2;
      if (before > 0) this.zoomBy(after / before, (e.clientX + o.x) / 2 - r.left, (e.clientY + o.y) / 2 - r.top);
      else this.place();
    }
    this.pointers.set(e.pointerId, { x: e.clientX, y: e.clientY });
  }

  /** A click that did not drag, beside the image, closes the viewer, as a click outside it does. */
  private pointerUp(e: PointerEvent) {
    if (!this.pointers.delete(e.pointerId)) return;
    if (this.pointers.size > 0 || this.moved || e.target === this.img) return;
    if (e.target === this.stage) this.close();
  }
}
