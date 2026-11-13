// A window of a session, as the desktop shows a tab: its layout tree of
// splits, each leaf a pane with its own terminal, and the dividers between
// them. It follows the host's view: a new view keeps the panes whose
// blocks stay, opens the new ones and closes the gone ones.
import { FloatingLayer, floatingLayersOf, layoutOf, LayoutNode, leavesOf, Rect } from "./layerhelpers";
import type { HostConn } from "./hostconn";
import { localize, t } from "./i18n";
import { draggable, DropZone, onMenu, ownDrag, paneDropZone } from "./manage";
import { PaneHost, TerminalView } from "./pane";
import { motionMs, prefs } from "./prefs";
import { extractDroppedFiles } from "./upload";

export type { FloatingLayer, LayoutNode };
export { layoutOf, leavesOf };

type Split = NonNullable<LayoutNode["split"]>;

/** The gap between the panes, in CSS pixels (view.go, gap). */
const gap = 8;
const eps = 1e-4;
/** How far a divider moves: by the pointer, between these ratios, as on the desktop. */
const minRatio = 0.08, maxRatio = 0.92;

/**
 * The block of a split's side that lies on its border: down the splits of
 * the same direction towards it. GoRex moves a border by a block beside it
 * (session.resize_pane), so a block deeper in the side may move another one.
 */
export function borderLeaf(n: LayoutNode | undefined, direction: string, towardsAfter: boolean): string | undefined {
  if (!n) return undefined;
  if (n.block_id) return n.block_id;
  const s = n.split;
  if (!s) return undefined;
  return borderLeaf(s.direction === direction && towardsAfter ? s.after : s.before, direction, towardsAfter);
}

/** The callbacks of a window: a pane's host, and what the page does on a divider's move. */
export interface WindowHost extends Omit<PaneHost, "focused" | "isFocused"> {
  /** The user focused a pane: tell the host. */
  focusBlock(w: WindowView, block: string): void;
  /** The user moved a divider: tell the host the split's new ratio. */
  resizeSplit(w: WindowView, split: Split, ratio: number): void;
  /** A pane's title. */
  titleOf(block: string): string;
  /** A pane's header asked for its menu, at a point of the window. */
  paneMenu(w: WindowView, block: string, x: number, y: number): void;
  /** A pane dragged by its header was dropped on a zone of another (panedrop.go, dropPaneOn). */
  dropPane(w: WindowView, dragged: string, target: string, zone: DropZone): void;
  /** Whether a pane dropped on another's edge moves beside it: the host's API must take it. */
  edgeDrops(): boolean;
  /** Minimizes or restores a floating layer via session.set_layer_state. */
  setLayerState?(w: WindowView, layerId: string, state: "normal" | "minimized"): void;
}

export class WindowView {
  readonly panes = new Map<string, TerminalView>();
  private dividers = new Map<string, HTMLElement>();
  private tree?: LayoutNode;
  private floatLayers: FloatingLayer[] = [];
  private floats = new Map<string, FloatingLayer>();
  private floatDock?: HTMLElement;
  private zoomed?: string;
  /** The pane that has the keys here. */
  focus?: string;
  /** The focused block of the host's last view, to tell when the host moves its focus. */
  private hostFocus?: string;
  /** A block that is to have the focus when it shows, as a split's new block. */
  want?: string;
  private dragging = false;
  /** A view of the host's that came while a divider moved. */
  private pending?: any;
  /** Ends the divider's drag going on, as its pointer's release does. */
  private endDrag?: () => void;
  /** Where a pane dragged over another would go, and what shows it (panedrop.go, drawPaneDrop). */
  private drop?: { block: string; zone: DropZone };
  private readonly dropMark = Object.assign(document.createElement("div"), { className: "pane-drop" });
  /** Whether the panes were placed once: the first placing, as the window shows, does not animate. */
  private rendered = false;
  /** Ends the panes' move: their terminals take their new sizes then. */
  private settleTimer?: number;
  /** The window's room on the stage: its panes and dividers. A kept window takes it off the stage (park). */
  private readonly el = Object.assign(document.createElement("div"), { className: "window-room" });
  /** Whether the window is kept off the stage, its panes live (park). */
  private parked = false;

  /** The host connection the window is of: its panes' control and data streams are this host's. */
  constructor(readonly conn: HostConn, readonly session: string, readonly window: string, private readonly stage: HTMLElement, private readonly app: WindowHost) {
    stage.append(this.el);
  }

  get kept(): boolean {
    return this.parked;
  }

  /** The splits of the tree, by ID. */
  split(id: string): Split | undefined {
    const find = (n?: LayoutNode): Split | undefined =>
      !n?.split ? undefined : n.split.split_id === id ? n.split : find(n.split.before) ?? find(n.split.after);
    return find(this.tree);
  }

  get zoomedBlock() {
    return this.zoomed;
  }

  get blocks(): string[] {
    return [...new Set([...leavesOf(this.tree), ...this.floatLayers.flatMap((l) => l.blocks)])];
  }

  floatingLayer(block: string): FloatingLayer | undefined {
    return this.floats.get(block);
  }

  minimizedFloats(): { layer: FloatingLayer; block: string }[] {
    const out: { layer: FloatingLayer; block: string }[] = [];
    for (const l of this.floatLayers) {
      if (l.minimized && l.blocks[0]) out.push({ layer: l, block: l.blocks[0] });
    }
    return out;
  }

  /** Takes the host's view of the window; its changes move the panes with the Animation style, unless not animate. */
  update(w: any, animate = true) {
    if (this.dragging) {
      this.pending = w;
      return;
    }
    this.tree = layoutOf(w);
    this.floatLayers = floatingLayersOf(w);
    this.floats.clear();
    for (const l of this.floatLayers) {
      for (const b of l.blocks) this.floats.set(b, l);
    }
    const blocks = this.blocks;
    this.zoomed = blocks.includes(w.zoomed_block_id) ? w.zoomed_block_id : undefined;
    const host: string | undefined = w.focused_block_id;
    let focus = this.focus;
    if (this.want && blocks.includes(this.want)) {
      focus = this.want;
      this.want = undefined;
    } else if (host !== this.hostFocus && host && blocks.includes(host)) {
      // The host moved its focus: here too, as on the desktop.
      focus = host;
    }
    this.hostFocus = host;
    if (!focus || !blocks.includes(focus)) focus = host && blocks.includes(host) ? host : blocks[0];
    // A pane that had the keys may go (moved to another window): its successor takes them.
    const keys = this.el.contains(document.activeElement);
    this.render(animate && !this.parked);
    this.setFocus(focus, false);
    if (keys && !this.el.contains(document.activeElement)) this.focusKeys();
  }

  /**
   * Places the panes and dividers; opens the panes of new blocks and
   * closes those of gone ones. With animate, as the desktop's
   * paneTransition, the panes move to their new places, and new and gone
   * ones pop (scale 0.92, faded), with the Animation style; not as the
   * window shows, nor while a divider is dragged.
   */
  private render(animate = false) {
    const ms = animate && this.rendered && !this.dragging && !this.parked ? motionMs() : 0;
    if (ms) this.moving(ms);
    const leaves: [string, Rect][] = [];
    const splits: [Split, Rect][] = [];
    const walk = (n: LayoutNode | undefined, r: Rect) => {
      if (!n) return;
      if (n.block_id) return void leaves.push([n.block_id, r]);
      const s = n.split;
      if (!s) return;
      splits.push([s, r]);
      const [x, y, w, h] = r;
      if (s.direction === "vertical") {
        walk(s.before, [x, y, w, h * s.ratio]);
        walk(s.after, [x, y + h * s.ratio, w, h * (1 - s.ratio)]);
      } else {
        walk(s.before, [x, y, w * s.ratio, h]);
        walk(s.after, [x + w * s.ratio, y, w * (1 - s.ratio), h]);
      }
    };
    walk(this.tree, [0, 0, 1, 1]);
    const floatEntries: [string, FloatingLayer][] = [];
    for (const l of this.floatLayers) {
      for (const b of l.blocks) {
        if (!leaves.some(([id]) => id === b)) floatEntries.push([b, l]);
      }
    }
    const all = new Set([...leaves.map(([b]) => b), ...floatEntries.map(([b]) => b)]);
    for (const [block, p] of this.panes) {
      if (!all.has(block)) {
        this.panes.delete(block);
        const unsent = p.close(false);
        if (unsent) this.app.status(() => t("paneUnsentDropped", { n: unsent }));
        // The pane's last frame pops out, and goes.
        if (ms) {
          this.el.append(p.el);
          p.el.classList.add("leaving");
          setTimeout(() => p.el.remove(), ms);
        }
      }
    }
    const ensurePane = (block: string, rect: Rect, fl?: FloatingLayer) => {
      let p = this.panes.get(block);
      if (!p) {
        p = new TerminalView(this.session, block, {
          ...this.app,
          focused: (v) => this.setFocus(v.block, true),
          isFocused: (v) => this.focus === v.block,
        });
        this.panes.set(block, p);
        this.wire(p);
        this.el.append(p.el);
        if (this.parked) p.park();
        if (ms) {
          p.el.classList.add("entering");
          p.el.addEventListener("animationend", () => p!.el.classList.remove("entering"), { once: true });
        }
        if (fl) placeFloat(p.el, this.zoomed === block ? [0, 0, 1, 1] : rect);
        else place(p.el, this.zoomed ? [0, 0, 1, 1] : rect);
        p.setTitle(this.app.titleOf(block));
        p.setProgram(this.app.programOf(block));
        p.start();
      }
      const shown = fl ? (!this.zoomed && !fl.minimized) || this.zoomed === block : !this.zoomed || this.zoomed === block;
      p.el.hidden = !shown;
      p.el.classList.toggle("floating", !!fl && this.zoomed !== block);
      p.el.style.zIndex = fl ? String(this.zoomed === block ? 40 : fl.zIndex) : "";
      p.el.classList.toggle("zoomed", this.zoomed === block);
      if (shown) {
        if (fl) placeFloat(p.el, this.zoomed === block ? [0, 0, 1, 1] : rect);
        else place(p.el, this.zoomed ? [0, 0, 1, 1] : rect);
      }
    };
    for (const [block, rect] of leaves) ensurePane(block, rect);
    for (const [block, fl] of floatEntries) ensurePane(block, fl.bounds, fl);
    this.renderFloatDock();
    this.rendered = true;
    // The dividers, one for each split, when no pane is zoomed.
    const ids = new Set(this.zoomed ? [] : splits.map(([s]) => s.split_id));
    for (const [id, d] of this.dividers) {
      if (!ids.has(id)) {
        d.remove();
        this.dividers.delete(id);
      }
    }
    if (this.zoomed) return;
    for (const [s, r] of splits) {
      let d = this.dividers.get(s.split_id);
      if (!d) {
        d = document.createElement("div");
        d.setAttribute("role", "separator");
        localize(d, (d) => d.setAttribute("aria-label", t("paneDivider")));
        this.drag(d, s.split_id);
        this.dividers.set(s.split_id, d);
        this.el.append(d);
      }
      const vertical = s.direction === "vertical";
      d.className = "divider " + (vertical ? "row" : "column");
      d.setAttribute("aria-orientation", vertical ? "horizontal" : "vertical");
      const [x, y, w, h] = r;
      if (vertical) {
        d.style.left = `${x * 100}%`;
        d.style.width = `${w * 100}%`;
        d.style.top = `calc(${(y + h * s.ratio) * 100}% - ${gap / 2}px)`;
        d.style.height = `${gap}px`;
      } else {
        d.style.top = `${y * 100}%`;
        d.style.height = `${h * 100}%`;
        d.style.left = `calc(${(x + w * s.ratio) * 100}% - ${gap / 2}px)`;
        d.style.width = `${gap}px`;
      }
    }
  }

  /**
   * A pane's header has its menu and drags the pane, and the pane takes
   * another's drop, as the desktop's card: in its middle a pane of the
   * same window swaps with it; on an edge, where the host takes it, it
   * goes beside it. Not in a zoomed window, as the desktop's
   * canDropPane.
   */
  private wire(p: TerminalView) {
    const block = p.block;
    onMenu(p.head, (x, y) => this.app.paneMenu(this, block, x, y));
    draggable(
      p.head,
      () => (this.zoomed || this.floats.has(block) ? undefined : { kind: "pane", host: this.conn.host.id, session: this.session, window: this.window, block }),
      () => this.showDrop(undefined),
    );
    // A press on a focusable pane focuses it, and its text field takes
    // the focus on, which keeps Chrome from starting the header's drag:
    // the pane is not focusable while the header is pressed, and a click
    // focuses it after. The press ends anywhere (a drag takes the pointer
    // events, a zoomed window cancels the drag, the pointer may leave the
    // header), so it is undone on the first end of any kind in the page.
    const focusable = () => {
      p.el.tabIndex = -1;
    };
    // (blur is the window's own: the press itself blurs the text field, and an element's blur does not bubble.)
    const ends = ["pointerup", "pointercancel", "dragend", "blur"];
    const capture = (ev: string) => ev !== "blur";
    p.head.addEventListener("pointerdown", (e) => {
      if (e.button !== 0 || e.pointerType !== "mouse" || (e.target as Element).closest?.("button")) return;
      p.el.removeAttribute("tabindex");
      const end = () => {
        for (const ev of ends) window.removeEventListener(ev, end, capture(ev));
        focusable();
      };
      for (const ev of ends) window.addEventListener(ev, end, capture(ev));
    });
    // The drag took the focus to the page: the keys go back to the window's focused pane (a drop that
    // moves the focus moves it on, see setFocus).
    p.head.addEventListener("dragend", () => {
      focusable();
      this.focusKeys();
    });
    p.head.addEventListener("click", (e) => {
      if ((e.target as Element).closest?.("button")) return;
      focusable();
      this.setFocus(block, true);
      p.focusKeys();
    });
    const zone = (e: DragEvent): DropZone | undefined => {
      if (this.floats.has(block)) return undefined;
      const d = ownDrag(e);
      if (d?.kind !== "pane" || d.block === block || d.host !== this.conn.host.id || d.session !== this.session || this.zoomed) return undefined;
      return paneDropZone(p.el.getBoundingClientRect(), e.clientX, e.clientY, d.window === this.window, this.app.edgeDrops());
    };
    p.el.addEventListener("dragover", (e) => {
      if (!ownDrag(e) && e.dataTransfer?.types.includes("Files")) {
        this.showDrop({ block, zone: "center" });
        e.preventDefault();
        e.dataTransfer.dropEffect = "copy";
        return;
      }
      const z = zone(e);
      this.showDrop(z && { block, zone: z });
      if (!z) return;
      e.preventDefault();
      e.dataTransfer!.dropEffect = "move";
    });
    p.el.addEventListener("dragleave", (e) => {
      if (this.drop?.block === block && !(e.relatedTarget instanceof Node && p.el.contains(e.relatedTarget))) this.showDrop(undefined);
    });
    p.el.addEventListener("drop", (e) => {
      const d = ownDrag(e);
      if (!d && e.dataTransfer?.types.includes("Files")) {
        this.showDrop(undefined);
        e.preventDefault();
        const { files, skippedDir } = extractDroppedFiles(e.dataTransfer);
        if (files.length) p.uploadFiles(files);
        else if (skippedDir) p.warnFolder();
        return;
      }
      // The zone shown last, where the user let go.
      const z = this.drop?.block === block ? this.drop.zone : zone(e);
      this.showDrop(undefined);
      if (d?.kind !== "pane" || !z) return;
      e.preventDefault();
      this.app.dropPane(this, d.block, block, z);
    });
  }

  /** Shows where a dragged pane would go, or nothing. */
  private showDrop(at: { block: string; zone: DropZone } | undefined) {
    this.drop = at;
    const p = at && this.panes.get(at.block);
    if (!p || !at) return void this.dropMark.remove();
    this.dropMark.dataset.zone = at.zone;
    if (this.dropMark.parentElement !== p.el) p.el.append(this.dropMark);
  }

  /**
   * A divider follows the pointer: the panes move at once, their
   * terminals keep their size until it is let go, and then the host takes
   * the ratio. A double click sets it back to half, as on the desktop.
   */
  private drag(d: HTMLElement, id: string) {
    d.addEventListener("pointerdown", (e) => {
      const s = this.split(id);
      if (!s || e.button !== 0) return;
      e.preventDefault();
      // A move still going ends: the divider follows the pointer at once.
      this.settle();
      d.setPointerCapture(e.pointerId);
      d.classList.add("held");
      const from = s.ratio;
      // The split's room in the stage, from its two sides' panes.
      const room = this.roomOf(s);
      const vertical = s.direction === "vertical";
      this.hold(true);
      const move = (m: PointerEvent) => {
        const cur = this.split(id);
        if (!cur || !room) return;
        const at = vertical ? (m.clientY - room.top) / room.height : (m.clientX - room.left) / room.width;
        cur.ratio = Math.min(maxRatio, Math.max(minRatio, at));
        this.render();
      };
      const up = () => {
        if (this.endDrag !== up) return;
        this.endDrag = undefined;
        d.removeEventListener("pointermove", move);
        d.removeEventListener("pointerup", up);
        d.removeEventListener("pointercancel", up);
        d.removeEventListener("lostpointercapture", up);
        d.classList.remove("held");
        this.hold(false);
        const cur = this.split(id);
        const ratio = cur?.ratio;
        if (this.pending) {
          const w = this.pending;
          this.pending = undefined;
          this.update(w, false);
          // The host's view had the old ratio: keep the dragged one until the host takes it.
          const now = this.split(id);
          if (now && ratio !== undefined) this.setRatio(now, ratio, false);
        }
        const s = this.split(id);
        if (s && ratio !== undefined && Math.abs(ratio - from) > 0.001) this.app.resizeSplit(this, s, ratio);
      };
      this.endDrag = up;
      d.addEventListener("pointermove", move);
      d.addEventListener("pointerup", up);
      d.addEventListener("pointercancel", up);
      // The capture can go without a release (the divider left the page): the drag ends then too.
      d.addEventListener("lostpointercapture", up);
    });
    d.addEventListener("dblclick", () => {
      const s = this.split(id);
      if (!s) return;
      s.ratio = 0.5;
      this.render(true);
      this.app.resizeSplit(this, s, 0.5);
    });
  }

  /** The stage's box of a split's whole room. */
  private roomOf(s: Split): DOMRect | undefined {
    const st = this.stage.getBoundingClientRect();
    const find = (n: LayoutNode | undefined, r: Rect): Rect | undefined => {
      if (!n?.split) return undefined;
      if (n.split === s) return r;
      const t = n.split;
      const [x, y, w, h] = r;
      const [a, b]: [Rect, Rect] =
        t.direction === "vertical"
          ? [[x, y, w, h * t.ratio], [x, y + h * t.ratio, w, h * (1 - t.ratio)]]
          : [[x, y, w * t.ratio, h], [x + w * t.ratio, y, w * (1 - t.ratio), h]];
      return find(t.before, a) ?? find(t.after, b);
    };
    const r = find(this.tree, [0, 0, 1, 1]);
    return r && new DOMRect(st.left + r[0] * st.width, st.top + r[1] * st.height, r[2] * st.width, r[3] * st.height);
  }

  /**
   * The panes move for ms: their places follow on a transition, and their
   * terminals keep their sizes until they rest, as the desktop's do.
   */
  private moving(ms: number) {
    this.stage.style.setProperty("--motion", `${ms}ms`);
    // Snappy's spring has a little bounce (animation.go); Natural's settles without one.
    this.stage.style.setProperty("--ease", prefs.animation === "natural" ? "cubic-bezier(0.22, 1, 0.36, 1)" : "cubic-bezier(0.3, 1.25, 0.5, 1)");
    this.stage.classList.add("moving");
    for (const p of this.panes.values()) p.held = true;
    clearTimeout(this.settleTimer);
    this.settleTimer = window.setTimeout(() => this.settle(), ms + 40);
  }

  /** Ends the panes' move: they rest, and their terminals take their sizes. */
  private settle() {
    clearTimeout(this.settleTimer);
    this.settleTimer = undefined;
    if (!this.stage.classList.contains("moving")) return;
    this.stage.classList.remove("moving");
    if (this.dragging) return;
    for (const p of this.panes.values()) {
      p.held = false;
      p.layout();
    }
  }

  /** Holds the terminals' sizes while a divider moves, and lays them out when it rests. */
  private hold(on: boolean) {
    this.dragging = on;
    for (const p of this.panes.values()) {
      p.held = on;
      if (!on) p.layout();
    }
  }

  get holding() {
    return this.dragging;
  }

  /**
   * Gives a pane the keys and the highlight. By the user (tell), the host
   * hears of it too (session.focus_block), as the desktop's click focuses
   * the pane there.
   */
  setFocus(block: string | undefined, tell: boolean) {
    if (!block || !this.panes.has(block)) return;
    const was = this.focus;
    this.focus = block;
    for (const [b, p] of this.panes) {
      p.el.classList.toggle("focused", b === block);
      if (b === block || b === was) p.redraw();
    }
    // The keyboard follows the focus while it is in the window, but for a pane's rename field (main.ts),
    // whose blur would commit a name half typed.
    const p = this.panes.get(block)!;
    if (this.el.contains(document.activeElement) && !document.activeElement?.classList.contains("rename-field")) p.focusKeys();
    if (tell && block !== this.hostFocus) {
      this.hostFocus = block;
      this.app.focusBlock(this, block);
    }
  }

  /** Puts the keyboard in the focused pane. */
  focusKeys() {
    const p = this.focus && this.panes.get(this.focus);
    if (p) p.focusKeys();
  }

  /** The pane beside the focused one in a direction, by where they show. */
  neighbour(dx: number, dy: number): string | undefined {
    const from = this.focus && this.panes.get(this.focus)?.el.getBoundingClientRect();
    if (!from) return undefined;
    let best: string | undefined;
    let bestScore = Infinity;
    for (const [b, p] of this.panes) {
      if (b === this.focus || p.el.hidden) continue;
      const r = p.el.getBoundingClientRect();
      const ahead =
        dx < 0 ? r.right <= from.left + 1 : dx > 0 ? r.left >= from.right - 1 : dy < 0 ? r.bottom <= from.top + 1 : r.top >= from.bottom - 1;
      if (!ahead) continue;
      const overlap = dx
        ? Math.min(r.bottom, from.bottom) - Math.max(r.top, from.top)
        : Math.min(r.right, from.right) - Math.max(r.left, from.left);
      if (overlap <= 0) continue;
      const dist = dx ? Math.abs((dx < 0 ? from.left - r.right : r.left - from.right)) : Math.abs(dy < 0 ? from.top - r.bottom : r.top - from.bottom);
      // The nearest first, then the one most beside the focused pane.
      const score = dist * 10000 - overlap;
      if (score < bestScore) {
        bestScore = score;
        best = b;
      }
    }
    return best;
  }

  /** The split nearest above the focused pane that a divider of this orientation moves. */
  splitAround(vertical: boolean): Split | undefined {
    const path: Split[] = [];
    const find = (n?: LayoutNode): boolean => {
      if (!n) return false;
      if (n.block_id) return n.block_id === this.focus;
      if (!n.split) return false;
      path.push(n.split);
      if (find(n.split.before) || find(n.split.after)) return true;
      path.pop();
      return false;
    };
    find(this.tree);
    return path.reverse().find((s) => (s.direction === "vertical") === vertical);
  }

  /** Sets a split's ratio here before the host's view comes, as a key's move does. */
  setRatio(s: Split, ratio: number, animate = true) {
    s.ratio = Math.min(maxRatio, Math.max(minRatio, ratio));
    this.render(animate);
  }

  /** The title of each pane again, after a title changed. */
  retitle() {
    for (const [b, p] of this.panes) {
      p.setTitle(this.app.titleOf(b));
      p.setProgram(this.app.programOf(b));
    }
  }

  /**
   * Keeps the window live off the stage, as the desktop's park
   * (workspace.go): its room leaves the stage, its panes stop painting and
   * give their blocks back, and their streams stay open. Its view goes on
   * coming (update).
   */
  park() {
    if (this.parked) return;
    // A divider's drag ends where it is, and a view that came meanwhile applies: a kept window follows its views.
    this.endDrag?.();
    this.stopMoving();
    this.parked = true;
    this.showDrop(undefined);
    // The keyboard leaves the window first, so that an input method's composition ends (pane.ts, blur).
    if (this.el.contains(document.activeElement)) (document.activeElement as HTMLElement).blur();
    this.el.remove();
    for (const p of this.panes.values()) p.park();
  }

  /** Puts a kept window back on the stage: its panes take their blocks again, size them and paint. */
  unpark() {
    if (!this.parked) return;
    this.parked = false;
    this.stage.append(this.el);
    for (const p of this.panes.values()) p.unpark();
  }

  /** Ends a move of the panes at once, its terminals at their sizes. */
  private stopMoving() {
    clearTimeout(this.settleTimer);
    this.settleTimer = undefined;
    // The stage's move is the shown window's: a kept one has none.
    if (!this.parked) this.stage.classList.remove("moving");
    for (const p of this.panes.values()) p.held = false;
  }

  private renderFloatDock() {
    const mins = this.zoomed ? [] : this.minimizedFloats();
    if (mins.length === 0) {
      this.floatDock?.remove();
      this.floatDock = undefined;
      return;
    }
    if (!this.floatDock) {
      this.floatDock = document.createElement("div");
      this.floatDock.className = "float-dock";
      this.el.append(this.floatDock);
    }
    this.floatDock.replaceChildren();
    for (const { layer, block } of mins) {
      const btn = document.createElement("button");
      btn.type = "button";
      btn.className = "float-dock-btn";
      btn.textContent = this.app.titleOf(block);
      btn.title = t("paneFloatRestore");
      btn.onclick = () => this.app.setLayerState?.(this, layer.layerId, "normal");
      this.floatDock.append(btn);
    }
  }

  /**
   * Ends the window and its panes; they give their blocks back unless
   * disown is false (a kept window's gave them as it was kept). Returns
   * how many typed bytes its panes had still to write, which are lost.
   */
  close(disown = true): number {
    this.stopMoving();
    this.showDrop(undefined);
    this.floatDock?.remove();
    this.floatDock = undefined;
    let unsent = 0;
    for (const p of this.panes.values()) unsent += p.close(disown);
    this.panes.clear();
    for (const d of this.dividers.values()) d.remove();
    this.dividers.clear();
    this.el.remove();
    return unsent;
  }
}

function placeFloat(e: HTMLElement, [x, y, w, h]: Rect) {
  e.style.left = `${x * 100}%`;
  e.style.top = `${y * 100}%`;
  e.style.width = `${w * 100}%`;
  e.style.height = `${h * 100}%`;
}

/** Places a pane at its share of the stage, half the gap in from each side another pane is beside. */
function place(e: HTMLElement, [x, y, w, h]: Rect) {
  const l = x > eps ? gap / 2 : 0;
  const t = y > eps ? gap / 2 : 0;
  const r = x + w < 1 - eps ? gap / 2 : 0;
  const b = y + h < 1 - eps ? gap / 2 : 0;
  e.style.left = `calc(${x * 100}% + ${l}px)`;
  e.style.top = `calc(${y * 100}% + ${t}px)`;
  e.style.width = `calc(${w * 100}% - ${l + r}px)`;
  e.style.height = `calc(${h * 100}% - ${t + b}px)`;
}
