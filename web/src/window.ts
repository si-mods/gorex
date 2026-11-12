// A window of a session, as the desktop shows a tab: its layout tree of
// splits, each leaf a pane with its own terminal, and the dividers between
// them. It follows the host's view: a new view keeps the panes whose
// blocks stay, opens the new ones and closes the gone ones.
import { localize, t } from "./i18n";
import { PaneHost, TerminalView } from "./pane";

/** A node of a layer's layout, as session.view gives it. */
export interface LayoutNode {
  block_id?: string;
  split?: { split_id: string; direction: "horizontal" | "vertical"; ratio: number; before: LayoutNode; after: LayoutNode };
}

type Split = NonNullable<LayoutNode["split"]>;
/** A place in the window, as fractions of its room: x, y, width, height. */
type Rect = [number, number, number, number];

/** The gap between the panes, in CSS pixels (view.go, gap). */
const gap = 8;
const eps = 1e-4;
/** How far a divider moves: by the pointer, between these ratios, as on the desktop. */
const minRatio = 0.08, maxRatio = 0.92;

/** The tiled layout of a window from session.view: its tree, or its one block. */
export function layoutOf(w: any): LayoutNode | undefined {
  const layers: any[] = w?.layers ?? [];
  const tiled = layers.find((l) => l.kind === "tiled") ?? layers[0];
  if (tiled?.layout && (tiled.layout.block_id || tiled.layout.split)) return tiled.layout;
  const first = tiled?.blocks?.[0]?.block_id;
  return first ? { block_id: first } : undefined;
}

/** The blocks of a tree, in layout order. */
export function leavesOf(n?: LayoutNode): string[] {
  if (!n) return [];
  if (n.block_id) return [n.block_id];
  return n.split ? [...leavesOf(n.split.before), ...leavesOf(n.split.after)] : [];
}

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
}

export class WindowView {
  readonly panes = new Map<string, TerminalView>();
  private dividers = new Map<string, HTMLElement>();
  private tree?: LayoutNode;
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

  constructor(readonly session: string, readonly window: string, private readonly stage: HTMLElement, private readonly app: WindowHost) {}

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
    return leavesOf(this.tree);
  }

  /** Takes the host's view of the window. */
  update(w: any) {
    if (this.dragging) {
      this.pending = w;
      return;
    }
    this.tree = layoutOf(w);
    const blocks = leavesOf(this.tree);
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
    this.render();
    this.setFocus(focus, false);
  }

  /** Places the panes and dividers; opens the panes of new blocks and closes those of gone ones. */
  private render() {
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
    const all = new Set(leaves.map(([b]) => b));
    for (const [block, p] of this.panes) {
      if (!all.has(block)) {
        this.panes.delete(block);
        p.close(false);
      }
    }
    for (const [block, rect] of leaves) {
      let p = this.panes.get(block);
      if (!p) {
        p = new TerminalView(this.session, block, {
          ...this.app,
          focused: (v) => this.setFocus(v.block, true),
          isFocused: (v) => this.focus === v.block,
        });
        this.panes.set(block, p);
        this.stage.append(p.el);
        place(p.el, this.zoomed ? [0, 0, 1, 1] : rect);
        p.setTitle(this.app.titleOf(block));
        p.setProgram(this.app.programOf(block));
        p.start();
      }
      const shown = !this.zoomed || this.zoomed === block;
      p.el.hidden = !shown;
      // A zoomed pane's zoom button shows minimize-2, as the desktop's.
      p.el.classList.toggle("zoomed", this.zoomed === block);
      if (shown) place(p.el, this.zoomed ? [0, 0, 1, 1] : rect);
    }
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
        this.stage.append(d);
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
   * A divider follows the pointer: the panes move at once, their
   * terminals keep their size until it is let go, and then the host takes
   * the ratio. A double click sets it back to half, as on the desktop.
   */
  private drag(d: HTMLElement, id: string) {
    d.addEventListener("pointerdown", (e) => {
      const s = this.split(id);
      if (!s || e.button !== 0) return;
      e.preventDefault();
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
        d.removeEventListener("pointermove", move);
        d.removeEventListener("pointerup", up);
        d.removeEventListener("pointercancel", up);
        d.classList.remove("held");
        this.hold(false);
        const cur = this.split(id);
        const ratio = cur?.ratio;
        if (this.pending) {
          const w = this.pending;
          this.pending = undefined;
          this.update(w);
          // The host's view had the old ratio: keep the dragged one until the host takes it.
          const now = this.split(id);
          if (now && ratio !== undefined) this.setRatio(now, ratio);
        }
        const s = this.split(id);
        if (s && ratio !== undefined && Math.abs(ratio - from) > 0.001) this.app.resizeSplit(this, s, ratio);
      };
      d.addEventListener("pointermove", move);
      d.addEventListener("pointerup", up);
      d.addEventListener("pointercancel", up);
    });
    d.addEventListener("dblclick", () => {
      const s = this.split(id);
      if (!s) return;
      s.ratio = 0.5;
      this.render();
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
    // The keyboard follows the focus while it is in the window.
    const p = this.panes.get(block)!;
    if (this.stage.contains(document.activeElement)) p.focusKeys();
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
  setRatio(s: Split, ratio: number) {
    s.ratio = Math.min(maxRatio, Math.max(minRatio, ratio));
    this.render();
  }

  /** The title of each pane again, after a title changed. */
  retitle() {
    for (const [b, p] of this.panes) {
      p.setTitle(this.app.titleOf(b));
      p.setProgram(this.app.programOf(b));
    }
  }

  close() {
    for (const p of this.panes.values()) p.close(true);
    this.panes.clear();
    for (const d of this.dividers.values()) d.remove();
    this.dividers.clear();
  }
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
