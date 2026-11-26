// Shared hover-intent helpers: pointer velocity over a trailing window,
// safe-triangle geometry for diagonal submenu travel, and a single-clock
// timer manager that clears cleanly on cancel, dispose or AbortSignal.

export interface Point {
  x: number;
  y: number;
}

export interface Rect {
  left: number;
  top: number;
  right: number;
  bottom: number;
}

export interface PointerSample {
  x: number;
  y: number;
  t: number;
}

/** Trailing window (ms) used to measure pointer speed. */
export const POINTER_WINDOW_MS = 90;

/** Pointer speed (px/ms) above which motion is considered a fast sweep. */
export const FAST_SPEED_PX_MS = 0.1;

/**
 * Computes pointer speed in px/ms over the trailing window ending at `now`
 * (or the latest sample's timestamp if omitted). Returns 0 when fewer than
 * two samples fall in the window or elapsed time is non-positive.
 */
export function pointerSpeed(samples: readonly PointerSample[], now?: number, windowMs = POINTER_WINDOW_MS): number {
  if (samples.length < 2) return 0;
  const last = samples[samples.length - 1];
  const ref = now !== undefined ? Math.max(now, last.t) : last.t;
  const cutoff = ref - windowMs;
  if (last.t < cutoff) return 0;
  let firstIdx = samples.length - 1;
  while (firstIdx > 0 && samples[firstIdx - 1].t >= cutoff) {
    firstIdx--;
  }
  const first = samples[firstIdx];
  if (first === last) return 0;
  const dt = ref - first.t;
  if (dt <= 0) return 0;
  return Math.hypot(last.x - first.x, last.y - first.y) / dt;
}

/** Whether a pointer speed in px/ms is classified as a fast sweep (> 0.1 px/ms). */
export function isFastPointer(speedPxMs: number, threshold = FAST_SPEED_PX_MS): boolean {
  return speedPxMs > threshold;
}

/** Tracks pointer samples in a trailing window and reports current speed. */
export class PointerTracker {
  private samples: PointerSample[] = [];

  constructor(private readonly windowMs = POINTER_WINDOW_MS) {}

  record(x: number, y: number, t: number): void {
    this.samples.push({ x, y, t });
    this.prune(t);
  }

  private prune(now: number): void {
    const cutoff = now - this.windowMs;
    let keep = 0;
    while (keep < this.samples.length - 1 && this.samples[keep].t < cutoff) {
      keep++;
    }
    if (keep > 0) this.samples.splice(0, keep);
  }

  speed(now?: number): number {
    if (now !== undefined) this.prune(now);
    return pointerSpeed(this.samples, now, this.windowMs);
  }

  isFast(now?: number, threshold = FAST_SPEED_PX_MS): boolean {
    return isFastPointer(this.speed(now), threshold);
  }

  latest(): PointerSample | undefined {
    return this.samples[this.samples.length - 1];
  }

  reset(): void {
    this.samples.length = 0;
  }
}

function cross(a: Point, b: Point, p: Point): number {
  return (b.x - a.x) * (p.y - a.y) - (b.y - a.y) * (p.x - a.x);
}

function pointInTriangle(p: Point, a: Point, b: Point, c: Point): boolean {
  const area2 = cross(a, b, c);
  if (area2 === 0) return false;
  const d1 = cross(a, b, p);
  const d2 = cross(b, c, p);
  const d3 = cross(c, a, p);
  const hasNeg = d1 < 0 || d2 < 0 || d3 < 0;
  const hasPos = d1 > 0 || d2 > 0 || d3 > 0;
  return !(hasNeg && hasPos);
}

/**
 * Returns true if `to` lies inside `rect` or inside the safe triangle formed
 * from `from` to the near vertical edge of `rect` (the left edge when `rect`
 * is to the right of `from`, or the right edge when `rect` is to the left).
 */
export function inSafeTriangle(from: Point, to: Point, rect: Rect): boolean {
  if (rect.right <= rect.left || rect.bottom <= rect.top) return false;
  if (to.x >= rect.left && to.x <= rect.right && to.y >= rect.top && to.y <= rect.bottom) {
    return true;
  }
  let edgeX: number;
  if (rect.left >= from.x) {
    edgeX = rect.left;
  } else if (rect.right <= from.x) {
    edgeX = rect.right;
  } else {
    return false;
  }
  return pointInTriangle(to, from, { x: edgeX, y: rect.top }, { x: edgeX, y: rect.bottom });
}

export interface HoverClockOptions {
  signal?: AbortSignal;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (id: unknown) => void;
}

/**
 * Single-clock timer manager: scheduling a new callback cancels any pending
 * one, and `dispose()` or an aborted `AbortSignal` clears it permanently.
 */
export class HoverClock {
  private timer: unknown;
  private active = false;
  private disposed = false;
  private readonly setTimer: (fn: () => void, ms: number) => unknown;
  private readonly clearTimer: (id: unknown) => void;
  private readonly signal?: AbortSignal;
  private readonly onAbort = () => this.dispose();

  constructor(opts: HoverClockOptions = {}) {
    this.setTimer = opts.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = opts.clearTimer ?? ((id) => clearTimeout(id as ReturnType<typeof setTimeout>));
    this.signal = opts.signal;
    if (this.signal?.aborted) {
      this.disposed = true;
    } else if (this.signal) {
      this.signal.addEventListener("abort", this.onAbort, { once: true });
    }
  }

  schedule(delayMs: number, fn: () => void): void {
    this.cancel();
    if (this.disposed) return;
    this.active = true;
    this.timer = this.setTimer(() => {
      this.active = false;
      this.timer = undefined;
      if (!this.disposed) fn();
    }, delayMs);
  }

  cancel(): void {
    if (!this.active) return;
    this.active = false;
    const id = this.timer;
    this.timer = undefined;
    this.clearTimer(id);
  }

  pending(): boolean {
    return this.active;
  }

  dispose(): void {
    if (this.disposed) return;
    this.disposed = true;
    this.cancel();
    this.signal?.removeEventListener("abort", this.onAbort);
  }
}
