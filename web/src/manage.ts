// What manages the spaces, windows and panes, as the desktop's: a right
// click's menu (ui.Menu, tabMenu and spaceMenu in tabs.go and
// workspaceview.go), renaming in place (renameField), Close Space's
// question (workspaceConfirm), and the drags of a window's row and of a
// pane's header (tabEvents, panedrop.go). main.ts says what each item
// does; these only show and take them, so that they can move with the
// sidebar.
import { HoverClock, inSafeTriangle, Point, PointerTracker } from "./hoverintent";
import { icon } from "./hosts";
import { t } from "./i18n";
import { hideTooltip, setTooltip } from "./tooltip";

const SUBMENU_OPEN_DELAY_MS = 150;
const SUBMENU_SWAP_DELAY_MS = 60;
const SUBMENU_CLOSE_GRACE_MS = 300;
const SUBMENU_EXIT_MS = 120;

/** An item of a menu: its words and what it runs, or a submenu; or a line between groups. */
export type MenuItem = { label: string; /** A second line under the label. */ detail?: string; /** A tooltip. */ title?: string; run?: () => void; disabled?: boolean; submenu?: MenuItem[]; /** A check before the label: the item is the one chosen. */ checked?: boolean; /** A dot of this color before the label. */ color?: string; /** A mark before the label, where a color dot would go: a program's tile. */ tile?: HTMLElement } | "separator";

/** The menu open, one at a time, and what closes it. */
let shown: { el: HTMLElement; done: AbortController; closed?: () => void } | undefined;

/** Closes the menu open, if any. */
export function closeMenu() {
  const m = shown;
  if (!m) return;
  shown = undefined;
  m.done.abort();
  m.el.remove();
  hideTooltip();
  m.closed?.();
}

/**
 * Opens a menu at a point of the window, as a right click's: a small
 * glass panel, its submenus beside it. A click outside, Escape, a scroll
 * or the window's resize closes it; the arrow keys move between its
 * items. closed runs once it closes, chosen or not.
 */
export function openMenu(x: number, y: number, items: MenuItem[], closed?: () => void) {
  closeMenu();
  if (!items.some((i) => i !== "separator")) return;
  const done = new AbortController();
  const el = panel(items, done.signal);
  document.body.append(el);
  shown = { el, done, closed };
  place(el, x, y, x);
  const opts = { capture: true, signal: done.signal };
  // A press outside closes it, and goes on to what it pressed.
  window.addEventListener("pointerdown", (e) => {
    if (!(e.target instanceof Node) || !el.contains(e.target)) closeMenu();
  }, opts);
  window.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      closeMenu();
    }
  }, opts);
  for (const ev of ["resize", "blur"]) window.addEventListener(ev, () => closeMenu(), { signal: done.signal });
  window.addEventListener("scroll", () => closeMenu(), opts);
  (el.querySelector("button:not(:disabled)") as HTMLElement | null)?.focus({ preventScroll: true });
}

/** The item that opened each submenu, which ArrowLeft goes back to. */
const openers = new WeakMap<HTMLElement, HTMLElement>();

/** A panel of items, and the submenu each opens beside it. */
function panel(items: MenuItem[], signal: AbortSignal, onClose?: () => void): HTMLElement {
  const el = document.createElement("div");
  el.className = "context-menu";
  el.setAttribute("role", "menu");
  const tracker = new PointerTracker();
  const openClock = new HoverClock({ signal });
  const closeClock = new HoverClock({ signal });
  let sub: HTMLElement | undefined;
  let subOpener: HTMLButtonElement | undefined;
  let subAbort: AbortController | undefined;
  let lastPoint: Point | undefined;

  const closeSub = (immediate = false) => {
    closeClock.cancel();
    if (!sub) return;
    subOpener?.setAttribute("aria-expanded", "false");
    subAbort?.abort();
    const closing = sub;
    sub = undefined;
    subOpener = undefined;
    subAbort = undefined;
    if (immediate || signal.aborted) {
      closing.remove();
      return;
    }
    closing.classList.add("closing");
    const timer = setTimeout(() => closing.remove(), SUBMENU_EXIT_MS);
    signal.addEventListener("abort", () => {
      clearTimeout(timer);
      closing.remove();
    }, { once: true });
  };

  el.addEventListener("pointermove", (e) => {
    if (e.pointerType === "touch") return;
    const pt: Point = { x: e.clientX, y: e.clientY };
    const prev = lastPoint;
    tracker.record(pt.x, pt.y, performance.now());
    lastPoint = pt;
    if (sub && prev && (!(e.target instanceof Node) || (!sub.contains(e.target) && !subOpener?.contains(e.target)))) {
      if (inSafeTriangle(prev, pt, sub.getBoundingClientRect())) {
        openClock.cancel();
        closeClock.schedule(SUBMENU_CLOSE_GRACE_MS, () => closeSub());
      }
    }
  }, { signal });

  el.addEventListener("pointerleave", (e) => {
    if (e.pointerType === "touch") return;
    openClock.cancel();
    if (sub) closeClock.schedule(SUBMENU_CLOSE_GRACE_MS, () => closeSub());
  }, { signal });

  for (const it of items) {
    if (it === "separator") {
      // Not first, not last, not twice.
      if (el.lastElementChild && !el.lastElementChild.classList.contains("context-sep")) el.append(sep());
      continue;
    }
    const b = document.createElement("button");
    b.type = "button";
    b.className = "context-item";
    b.setAttribute("role", "menuitem");
    const label = document.createElement("span");
    label.textContent = it.label;
    if (it.title) setTooltip(b, it.title, "menu");
    if (it.checked !== undefined || it.color || it.tile) {
      const mark = document.createElement("span");
      mark.className = "context-mark";
      if (it.checked) mark.append(icon("check"));
      b.append(mark);
      if (it.color) {
        const dot = document.createElement("i");
        dot.className = "color-dot";
        dot.style.background = it.color;
        b.append(dot);
      }
      if (it.tile) b.append(it.tile);
    }
    if (it.detail) {
      const text = document.createElement("div");
      text.className = "context-text";
      const detail = document.createElement("small");
      detail.className = "context-detail";
      detail.textContent = it.detail;
      text.append(label, detail);
      b.append(text);
    } else b.append(label);
    b.disabled = !!it.disabled || (!!it.submenu && !it.submenu.length);
    // The menu keeps the page's focus where it was: a press does not take it.
    let lastDownType = "";
    b.onpointerdown = (e) => {
      lastDownType = e.pointerType;
      e.preventDefault();
    };
    if (it.submenu) {
      b.setAttribute("aria-haspopup", "menu");
      b.setAttribute("aria-expanded", "false");
      b.append(icon("chevron-right"));
      const open = (focus: boolean) => {
        if (b.disabled) return;
        openClock.cancel();
        closeClock.cancel();
        if (subOpener === b && sub) {
          if (focus) (sub.querySelector("button:not(:disabled)") as HTMLElement | null)?.focus({ preventScroll: true });
          return;
        }
        closeSub(true);
        const childAbort = new AbortController();
        signal.addEventListener("abort", () => childAbort.abort(), { once: true });
        subAbort = childAbort;
        const child = panel(it.submenu!, childAbort.signal, () => closeSub(true));
        child.classList.add("sub");
        sub = child;
        subOpener = b;
        b.setAttribute("aria-expanded", "true");
        openers.set(child, b);
        child.addEventListener("pointerenter", (e) => {
          if (e.pointerType === "touch") return;
          closeClock.cancel();
          openClock.cancel();
        }, { signal: childAbort.signal });
        child.addEventListener("pointerleave", (e) => {
          if (e.pointerType === "touch") return;
          if (e.relatedTarget instanceof Node && el.contains(e.relatedTarget)) return;
          closeClock.schedule(SUBMENU_CLOSE_GRACE_MS, () => closeSub());
        }, { signal: childAbort.signal });
        el.append(child);
        const r = b.getBoundingClientRect();
        place(child, r.right + 2, r.top - 5, r.left - 2);
        if (focus) (child.querySelector("button:not(:disabled)") as HTMLElement | null)?.focus({ preventScroll: true });
      };
      const scheduleHoverOpen = (delayMs: number) => {
        openClock.schedule(delayMs, () => {
          if (tracker.isFast(performance.now())) {
            scheduleHoverOpen(SUBMENU_SWAP_DELAY_MS);
            return;
          }
          open(false);
        });
      };
      b.onpointerenter = (e) => {
        if (e.pointerType === "touch") return;
        const pt: Point = { x: e.clientX, y: e.clientY };
        const prev = lastPoint;
        tracker.record(pt.x, pt.y, performance.now());
        lastPoint = pt;
        if (subOpener === b && sub) {
          closeClock.cancel();
          openClock.cancel();
          return;
        }
        if (sub) {
          closeClock.schedule(SUBMENU_CLOSE_GRACE_MS, () => closeSub());
          if (prev && inSafeTriangle(prev, pt, sub.getBoundingClientRect())) {
            // If the pointer dwells on this sibling past the safe-triangle grace,
            // open this sibling's submenu once the previous one closes.
            scheduleHoverOpen(SUBMENU_CLOSE_GRACE_MS);
            return;
          }
          scheduleHoverOpen(SUBMENU_SWAP_DELAY_MS);
        } else {
          scheduleHoverOpen(SUBMENU_OPEN_DELAY_MS);
        }
      };
      b.onclick = () => {
        const touch = lastDownType === "touch";
        lastDownType = "";
        if (touch && subOpener === b && sub) {
          closeSub();
          return;
        }
        open(!touch);
      };
      b.onkeydown = (e) => {
        if (e.key === "ArrowRight") {
          e.preventDefault();
          open(true);
        }
      };
    } else {
      b.onpointerenter = (e) => {
        if (e.pointerType === "touch") return;
        openClock.cancel();
        if (sub) closeClock.schedule(SUBMENU_CLOSE_GRACE_MS, () => closeSub());
      };
      b.onclick = () => {
        closeMenu();
        it.run?.();
      };
    }
    el.append(b);
  }
  if (el.lastElementChild?.classList.contains("context-sep")) el.lastElementChild.remove();
  el.addEventListener("keydown", (e) => {
    const own = [...el.children].filter((c): c is HTMLButtonElement => c instanceof HTMLButtonElement && !c.disabled);
    const at = own.indexOf(document.activeElement as HTMLButtonElement);
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      if (sub?.contains(document.activeElement)) return;
      e.preventDefault();
      e.stopPropagation();
      const n = own.length, down = e.key === "ArrowDown";
      own[at < 0 ? (down ? 0 : n - 1) : (at + (down ? 1 : n - 1)) % n]?.focus({ preventScroll: true });
    } else if (e.key === "ArrowLeft" && el.classList.contains("sub") && at >= 0) {
      // Back to the item that opened it.
      e.preventDefault();
      e.stopPropagation();
      if (onClose) onClose();
      else el.remove();
      openers.get(el)?.focus({ preventScroll: true });
    }
  }, { signal });
  return el;
}

function sep(): HTMLElement {
  const s = document.createElement("div");
  s.className = "context-sep";
  s.setAttribute("role", "separator");
  return s;
}

/** Puts a panel at x, y, inside the window: else to the left of alt, or up. */
function place(el: HTMLElement, x: number, y: number, alt: number) {
  el.style.left = "0px";
  el.style.top = "0px";
  const r = el.getBoundingClientRect();
  const w = window.innerWidth, h = window.innerHeight;
  const flipped = x + r.width > w - 8;
  const left = flipped ? Math.max(8, alt - r.width) : x;
  const top = Math.max(8, Math.min(y, h - 8 - r.height));
  if (el.classList.contains("sub")) el.dataset.side = flipped ? "left" : "right";
  // A submenu is placed in its parent's box.
  const base = el.offsetParent?.getBoundingClientRect() ?? new DOMRect();
  el.style.left = `${left - (el.classList.contains("sub") ? base.left : 0)}px`;
  el.style.top = `${top - (el.classList.contains("sub") ? base.top : 0)}px`;
}

/**
 * Opens an element's menu on a right click, and on a touch held still,
 * as a phone's long press: the click that ends the press is not a click.
 */
export function onMenu(e: HTMLElement, open: (x: number, y: number) => void) {
  e.addEventListener("contextmenu", (ev) => {
    // A text field in it (the rename field) has the browser's own menu.
    if ((ev.target as Element).closest?.("input, textarea")) return;
    ev.preventDefault();
    ev.stopPropagation();
    // The menu key opens it at the element.
    if (ev.button !== 2 && ev.clientX === 0 && ev.clientY === 0) {
      const r = e.getBoundingClientRect();
      open(r.left + 12, r.bottom);
    } else open(ev.clientX, ev.clientY);
  });
  let timer: ReturnType<typeof setTimeout> | undefined;
  let held = false;
  e.addEventListener("pointerdown", (ev) => {
    if (ev.pointerType !== "touch") return;
    held = false;
    const x = ev.clientX, y = ev.clientY;
    clearTimeout(timer);
    timer = setTimeout(() => {
      held = true;
      open(x, y);
    }, 500);
    const stop = (m: PointerEvent) => {
      if (m.type === "pointermove" && Math.hypot(m.clientX - x, m.clientY - y) < 10) return;
      clearTimeout(timer);
      e.removeEventListener("pointermove", stop);
      e.removeEventListener("pointerup", stop);
      e.removeEventListener("pointercancel", stop);
    };
    e.addEventListener("pointermove", stop);
    e.addEventListener("pointerup", stop);
    e.addEventListener("pointercancel", stop);
  });
  e.addEventListener("click", (ev) => {
    if (!held) return;
    held = false;
    ev.preventDefault();
    ev.stopPropagation();
  }, { capture: true });
}

/**
 * Renames in place, as the desktop's renameField: a field with the name,
 * all of it chosen once in the page (focusRename). Enter or the focus going elsewhere keeps it, Escape
 * goes back; done gets the name, or undefined for Escape.
 */
export function renameField(name: string, placeholder: string, label: string, done: (name: string | undefined) => void): HTMLInputElement {
  const f = document.createElement("input");
  f.className = "rename-field";
  f.value = name;
  f.placeholder = placeholder;
  f.setAttribute("aria-label", label);
  f.spellcheck = false;
  f.autocomplete = "off";
  let over = false;
  const end = (keep: boolean) => {
    if (over) return;
    over = true;
    done(keep ? f.value.trim() : undefined);
  };
  f.addEventListener("keydown", (e) => {
    // The field's keys are its own, not the window's shortcuts.
    e.stopPropagation();
    // A key an input method took (isComposing, or keyCode 229 as Safari reports it) is its own.
    if (e.isComposing || e.keyCode === 229) return;
    if (e.key === "Enter") {
      e.preventDefault();
      end(true);
    } else if (e.key === "Escape") {
      e.preventDefault();
      end(false);
    }
  });
  f.addEventListener("blur", () => end(true));
  // A click in the field is not the row's.
  f.addEventListener("click", (e) => e.stopPropagation());
  f.addEventListener("dblclick", (e) => e.stopPropagation());
  return f;
}

/**
 * Asks before something that ends terminals, as the desktop's
 * workspaceConfirm: its title, what ends, Cancel and the red button.
 * It answers true for the red button.
 */
export function confirmDanger(title: string, body: string, action: string, danger = true): Promise<boolean> {
  return new Promise((resolve) => {
    const d = document.createElement("dialog");
    d.className = "confirm";
    d.setAttribute("aria-label", title);
    const h = document.createElement("h2");
    h.textContent = title;
    const p = document.createElement("p");
    p.textContent = body;
    const row = document.createElement("div");
    row.className = "confirm-buttons";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "host-add";
    cancel.textContent = t("commonCancel");
    const ok = document.createElement("button");
    ok.type = "button";
    ok.className = danger ? "host-add destroy" : "host-add primary";
    ok.textContent = action;
    row.append(cancel, ok);
    d.append(h, p, row);
    let answer = false;
    cancel.onclick = () => d.close();
    ok.onclick = () => {
      answer = true;
      d.close();
    };
    d.addEventListener("close", () => {
      d.remove();
      resolve(answer);
    });
    document.body.append(d);
    d.showModal();
    cancel.focus();
  });
}

/**
 * Asks for one line of text, in the frosted dialog of confirmDanger: New
 * Space and Rename Space. It answers the text, trimmed, or undefined for
 * Cancel or an empty field.
 */
export function askText(title: string, label: string, value: string, action: string): Promise<string | undefined> {
  return new Promise((resolve) => {
    const d = document.createElement("dialog");
    d.className = "confirm";
    d.setAttribute("aria-label", title);
    const h = document.createElement("h2");
    h.textContent = title;
    const field = document.createElement("input");
    field.className = "sheet-field";
    field.type = "text";
    field.value = value;
    field.spellcheck = false;
    field.autocomplete = "off";
    field.setAttribute("aria-label", label);
    const row = document.createElement("div");
    row.className = "confirm-buttons";
    const cancel = document.createElement("button");
    cancel.type = "button";
    cancel.className = "host-add";
    cancel.textContent = t("commonCancel");
    const ok = document.createElement("button");
    ok.type = "button";
    ok.className = "host-add primary";
    ok.textContent = action;
    row.append(cancel, ok);
    d.append(h, field, row);
    let answer: string | undefined;
    const sync = () => (ok.disabled = !field.value.trim());
    field.oninput = sync;
    sync();
    field.onkeydown = (e) => {
      e.stopPropagation();
      if (e.key === "Enter" && !e.isComposing && e.keyCode !== 229 && !ok.disabled) ok.click();
    };
    cancel.onclick = () => d.close();
    ok.onclick = () => {
      answer = field.value.trim();
      d.close();
    };
    d.addEventListener("close", () => {
      d.remove();
      resolve(answer || undefined);
    });
    document.body.append(d);
    d.showModal();
    field.focus();
    field.select();
  });
}

/**
 * What is being dragged, as the page drags it: a window's row, or a pane's
 * header. Each names its host: two hosts can give the same IDs.
 */
export type Dragged =
  | { kind: "window"; host: string; session: string; window: string }
  | { kind: "pane"; host: string; session: string; window: string; block: string };

/** The drag going on. DataTransfer's data cannot be read while it goes, only at the drop. */
export let dragging: Dragged | undefined;

/** The type a drag of the page carries, so that other pages' and files' drags are not taken. */
const dragType = "application/x-gorex-drag";

/**
 * Makes an element drag d: dimmed while it goes, as the desktop's
 * dimmed(e, 0.4). ended runs when it ends, dropped or not.
 */
export function draggable(e: HTMLElement, d: () => Dragged | undefined, ended?: () => void) {
  e.draggable = true;
  e.addEventListener("dragstart", (ev) => {
    const what = d();
    if (!what || !ev.dataTransfer) {
      ev.preventDefault();
      return;
    }
    dragging = what;
    ev.dataTransfer.effectAllowed = "move";
    ev.dataTransfer.setData(dragType, JSON.stringify(what));
    // Dimmed once the browser took its picture of it.
    requestAnimationFrame(() => e.classList.add("dragging"));
  });
  e.addEventListener("dragend", () => {
    dragging = undefined;
    e.classList.remove("dragging");
    ended?.();
  });
}

/** Whether a drag over the page is the page's own. */
export function ownDrag(ev: DragEvent): Dragged | undefined {
  return dragging && ev.dataTransfer?.types.includes(dragType) ? dragging : undefined;
}

/** The part of a pane a pane dragged over it is over, as panedrop.go's dropZone: in its place, or beside it on a side. */
export type DropZone = "center" | "left" | "right" | "top" | "bottom";

/**
 * Where on a pane a pane dragged over it goes, as panedrop.go's
 * paneDropZone: the middle, 0.2 of its width and height each way of its
 * centre, swaps the two (only with center); else the nearest edge moves
 * it beside the pane (only with edges), the diagonals dividing them.
 * Undefined where the pane takes nothing.
 */
export function paneDropZone(r: DOMRect, x: number, y: number, center: boolean, edges: boolean): DropZone | undefined {
  if (r.width <= 0 || r.height <= 0) return undefined;
  const nx = (x - r.left) / r.width, ny = (y - r.top) / r.height;
  if (center && Math.abs(nx - 0.5) < 0.2 && Math.abs(ny - 0.5) < 0.2) return "center";
  if (!edges) return undefined;
  const m = Math.min(nx, 1 - nx, ny, 1 - ny);
  return m === nx ? "left" : m === 1 - nx ? "right" : m === ny ? "top" : "bottom";
}

/** How a zone splits the pane, for session.move_block: none for the centre. */
export function zoneSplit(z: DropZone): { direction: "horizontal" | "vertical"; side: "before" | "after" } | undefined {
  switch (z) {
    case "left":
      return { direction: "horizontal", side: "before" };
    case "right":
      return { direction: "horizontal", side: "after" };
    case "top":
      return { direction: "vertical", side: "before" };
    case "bottom":
      return { direction: "vertical", side: "after" };
  }
  return undefined;
}

/** Gives a rename's field in el the keyboard, its name chosen, once it is in the page. */
export function focusRename(el: HTMLElement) {
  const f = el.querySelector<HTMLInputElement>(".rename-field");
  if (!f) return;
  f.focus({ preventScroll: true });
  f.select();
  f.scrollIntoView({ block: "nearest" });
}
