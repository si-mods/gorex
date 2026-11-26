// Shared Web Tooltip component: one singleton element with document-level
// event delegation, 500ms rest delay, warm-group instant swap, 400ms warm
// window after hide, press-to-suppress, keyboard focus-visible + Escape +
// aria-describedby, and viewport-clamped positioning.
import { HoverClock, HoverClockOptions } from "./hoverintent";

export const TOOLTIP_OPEN_DELAY_MS = 500;
export const TOOLTIP_WARM_WINDOW_MS = 400;
export const TOOLTIP_FADE_OUT_MS = 60;
const TOOLTIP_ID = "gorex-tooltip";
const TOOLTIP_GAP_PX = 6;
const VIEWPORT_MARGIN_PX = 8;

export interface TooltipCallbacks<T> {
  /** Called when a trigger's tooltip should be shown. `instant` is true when swapping in-place inside a warm group. */
  onShow: (trigger: T, instant: boolean) => void;
  /** Called when the active tooltip should hide. */
  onHide: () => void;
}

/**
 * Pure tooltip state machine (DOM-free, unit-testable):
 * - Opens after 500ms pointer rest; moving to another trigger resets the wait.
 * - Moving to a neighbor in the same group while open swaps immediately in place.
 * - Leaving hides immediately; re-entering the same group within 400ms shows immediately.
 * - Pressing hides and suppresses until the pointer leaves that trigger.
 * - Keyboard focus shows immediately; Escape hides immediately.
 */
export class TooltipState<T> {
  private visible: T | undefined;
  private visibleGroup: unknown;
  private hovered: T | undefined;
  private suppressed: T | undefined;
  private warmGroup: unknown;
  private warmUntil = -Infinity;
  private source: "pointer" | "focus" | undefined;
  private readonly clock: HoverClock;

  constructor(private readonly cb: TooltipCallbacks<T>, clockOpts: HoverClockOptions = {}) {
    this.clock = new HoverClock(clockOpts);
  }

  current(): T | undefined {
    return this.visible;
  }

  pointerEnter(trigger: T, group: unknown, now: number): void {
    if (this.suppressed === trigger) return;
    if (this.hovered === trigger && this.visible === trigger) return;
    this.hovered = trigger;
    this.suppressed = undefined;
    this.clock.cancel();

    if (this.visible !== undefined) {
      if (group !== undefined && group === this.visibleGroup) {
        this.visible = trigger;
        this.source = "pointer";
        this.cb.onShow(trigger, true);
        return;
      }
      // Different group: hide the old tooltip immediately, record its warm window,
      // and wait the full open delay for the new group.
      this.warmGroup = this.visibleGroup;
      this.warmUntil = now + TOOLTIP_WARM_WINDOW_MS;
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    } else if (group !== undefined && group === this.warmGroup && now <= this.warmUntil) {
      this.visible = trigger;
      this.visibleGroup = group;
      this.source = "pointer";
      this.cb.onShow(trigger, false);
      return;
    }

    this.clock.schedule(TOOLTIP_OPEN_DELAY_MS, () => {
      if (this.hovered !== trigger || this.suppressed === trigger) return;
      this.visible = trigger;
      this.visibleGroup = group;
      this.source = "pointer";
      this.cb.onShow(trigger, false);
    });
  }

  pointerLeave(now: number): void {
    this.clock.cancel();
    this.hovered = undefined;
    this.suppressed = undefined;
    if (this.visible !== undefined && this.source === "pointer") {
      this.warmGroup = this.visibleGroup;
      this.warmUntil = now + TOOLTIP_WARM_WINDOW_MS;
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    }
  }

  pointerDown(trigger: T | undefined): void {
    this.clock.cancel();
    if (trigger !== undefined) this.suppressed = trigger;
    if (this.visible !== undefined) {
      this.warmGroup = undefined;
      this.warmUntil = -Infinity;
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    }
  }

  focusIn(trigger: T, group: unknown): void {
    this.clock.cancel();
    this.visible = trigger;
    this.visibleGroup = group;
    this.source = "focus";
    this.cb.onShow(trigger, false);
  }

  focusOut(trigger?: T): void {
    if (this.source === "focus" && (trigger === undefined || this.visible === trigger)) {
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    }
  }

  escape(): void {
    this.clock.cancel();
    if (this.visible !== undefined) {
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    }
  }

  /** Hides unconditionally (e.g., on scroll or window blur). */
  reset(): void {
    this.clock.cancel();
    this.hovered = undefined;
    if (this.visible !== undefined) {
      this.visible = undefined;
      this.visibleGroup = undefined;
      this.source = undefined;
      this.cb.onHide();
    }
  }

  dispose(): void {
    this.clock.dispose();
  }
}

let initialized = false;
let tipEl: HTMLDivElement | undefined;
let state: TooltipState<HTMLElement> | undefined;
let describedEl: HTMLElement | undefined;
let hideTimer: ReturnType<typeof setTimeout> | undefined;

/**
 * Sets or clears a custom tooltip on `el`, removing any native `title`
 * attribute so the browser tooltip never doubles up.
 */
export function setTooltip(el: HTMLElement, text: string | undefined, group?: string): void {
  el.removeAttribute("title");
  if (text) {
    el.dataset.tip = text;
    if (group !== undefined) el.dataset.tipGroup = group;
    if (state?.current() === el && tipEl && !tipEl.hidden) {
      tipEl.textContent = text;
      positionTip(tipEl, el);
    }
  } else {
    delete el.dataset.tip;
    delete el.dataset.tipGroup;
    if (state?.current() === el) state.reset();
  }
}

function groupOf(el: HTMLElement): unknown {
  return el.dataset.tipGroup || el.parentElement || el;
}

function findTrigger(target: EventTarget | null): HTMLElement | undefined {
  if (!(target instanceof Element)) return undefined;
  const el = target.closest<HTMLElement>("[data-tip]");
  return el && el.dataset.tip ? el : undefined;
}

function isHoverNone(): boolean {
  return typeof window !== "undefined" && !!window.matchMedia?.("(hover: none)").matches;
}

function matchesFocusVisible(el: HTMLElement): boolean {
  try {
    return el.matches(":focus-visible");
  } catch {
    return true;
  }
}

function attachDescribedBy(el: HTMLElement): void {
  if (describedEl && describedEl !== el) detachDescribedBy();
  const cur = el.getAttribute("aria-describedby") ?? "";
  const tokens = cur.split(/\s+/).filter(Boolean);
  if (!tokens.includes(TOOLTIP_ID)) {
    tokens.push(TOOLTIP_ID);
    el.setAttribute("aria-describedby", tokens.join(" "));
  }
  describedEl = el;
}

function detachDescribedBy(): void {
  const el = describedEl;
  describedEl = undefined;
  if (!el) return;
  const cur = el.getAttribute("aria-describedby") ?? "";
  const tokens = cur.split(/\s+/).filter((tok) => tok && tok !== TOOLTIP_ID);
  if (tokens.length) el.setAttribute("aria-describedby", tokens.join(" "));
  else el.removeAttribute("aria-describedby");
}

function mountFor(tip: HTMLElement, trigger: HTMLElement): void {
  // Mount inside an open modal <dialog> so the tooltip renders in the top layer above it.
  const host = trigger.closest("dialog[open]") ?? document.body;
  if (tip.parentElement !== host) host.append(tip);
}

function positionTip(tip: HTMLElement, trigger: HTMLElement): void {
  const r = trigger.getBoundingClientRect();
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const tr = tip.getBoundingClientRect();
  const tw = tr.width;
  const th = tr.height;

  const fitsAbove = r.top - TOOLTIP_GAP_PX - th >= VIEWPORT_MARGIN_PX;
  const side: "top" | "bottom" = fitsAbove || r.bottom + TOOLTIP_GAP_PX + th > vh - VIEWPORT_MARGIN_PX ? "top" : "bottom";
  const top = side === "top"
    ? Math.max(VIEWPORT_MARGIN_PX, r.top - TOOLTIP_GAP_PX - th)
    : Math.min(vh - VIEWPORT_MARGIN_PX - th, r.bottom + TOOLTIP_GAP_PX);
  const idealLeft = r.left + r.width / 2 - tw / 2;
  const left = Math.max(VIEWPORT_MARGIN_PX, Math.min(idealLeft, vw - VIEWPORT_MARGIN_PX - tw));

  tip.dataset.side = side;
  tip.style.left = `${Math.round(left)}px`;
  tip.style.top = `${Math.round(top)}px`;
}

function showDom(trigger: HTMLElement, instant: boolean): void {
  if (!tipEl || !trigger.isConnected) return;
  const text = trigger.dataset.tip;
  if (!text) return;
  clearTimeout(hideTimer);
  hideTimer = undefined;
  mountFor(tipEl, trigger);
  tipEl.textContent = text;
  if (instant) {
    tipEl.dataset.instant = "true";
    tipEl.hidden = false;
    positionTip(tipEl, trigger);
    tipEl.dataset.state = "open";
  } else {
    delete tipEl.dataset.instant;
    const wasHidden = tipEl.hidden;
    tipEl.hidden = false;
    if (wasHidden) {
      delete tipEl.dataset.state;
      positionTip(tipEl, trigger);
      // Force layout before toggling open state so the fade-in transition plays.
      void tipEl.offsetWidth;
      tipEl.dataset.state = "open";
    } else {
      positionTip(tipEl, trigger);
      tipEl.dataset.state = "open";
    }
  }
  attachDescribedBy(trigger);
}

function hideDom(): void {
  detachDescribedBy();
  if (!tipEl || tipEl.hidden) return;
  delete tipEl.dataset.instant;
  delete tipEl.dataset.state;
  clearTimeout(hideTimer);
  hideTimer = setTimeout(() => {
    hideTimer = undefined;
    if (tipEl && tipEl.dataset.state !== "open") tipEl.hidden = true;
  }, TOOLTIP_FADE_OUT_MS);
}

/**
 * Initializes the singleton tooltip element and document-level event
 * delegation. Safe to call multiple times (idempotent).
 */
export function initTooltips(): void {
  if (initialized || typeof document === "undefined") return;
  initialized = true;

  tipEl = document.createElement("div");
  tipEl.id = TOOLTIP_ID;
  tipEl.className = "tooltip";
  tipEl.setAttribute("role", "tooltip");
  tipEl.hidden = true;
  document.body.append(tipEl);

  const s = new TooltipState<HTMLElement>({
    onShow: showDom,
    onHide: hideDom,
  });
  state = s;

  document.addEventListener("pointerover", (e) => {
    if (e.pointerType === "touch" || isHoverNone()) return;
    const trigger = findTrigger(e.target);
    if (!trigger) return;
    const prev = findTrigger(e.relatedTarget);
    if (prev === trigger) return;
    s.pointerEnter(trigger, groupOf(trigger), performance.now());
  });

  document.addEventListener("pointerout", (e) => {
    if (e.pointerType === "touch" || isHoverNone()) return;
    const trigger = findTrigger(e.target);
    if (!trigger) return;
    const next = findTrigger(e.relatedTarget);
    if (next === trigger) return;
    if (!next) s.pointerLeave(performance.now());
  });

  document.addEventListener("pointerdown", (e) => {
    const trigger = findTrigger(e.target);
    s.pointerDown(trigger);
  }, { capture: true });

  document.addEventListener("focusin", (e) => {
    const trigger = findTrigger(e.target);
    if (!trigger || !matchesFocusVisible(trigger)) return;
    s.focusIn(trigger, groupOf(trigger));
  });

  document.addEventListener("focusout", (e) => {
    const trigger = findTrigger(e.target);
    if (trigger) s.focusOut(trigger);
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") s.escape();
  }, { capture: true });

  window.addEventListener("scroll", () => s.reset(), { capture: true, passive: true });
  window.addEventListener("blur", () => s.reset());
}
