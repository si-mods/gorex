import assert from "node:assert/strict";
import { test } from "node:test";
import {
  FAST_SPEED_PX_MS, HoverClock, inSafeTriangle, isFastPointer, POINTER_WINDOW_MS, pointerSpeed, PointerTracker, Rect,
} from "../src/hoverintent";

const near = (a: number, b: number, eps = 1e-6) => assert.ok(Math.abs(a - b) <= eps, `${a} is not ${b}`);

test("pointerSpeed computes px/ms over the trailing 90ms window", () => {
  assert.equal(pointerSpeed([]), 0);
  assert.equal(pointerSpeed([{ x: 10, y: 20, t: 100 }]), 0);

  // 18px over 90ms = 0.2 px/ms (fast)
  const fast = [
    { x: 0, y: 0, t: 100 },
    { x: 18, y: 0, t: 190 },
  ];
  near(pointerSpeed(fast), 0.2);
  assert.equal(isFastPointer(pointerSpeed(fast)), true);

  // 9px over 90ms = 0.1 px/ms (boundary: slow/resting)
  const boundary = [
    { x: 0, y: 0, t: 100 },
    { x: 9, y: 0, t: 190 },
  ];
  near(pointerSpeed(boundary), FAST_SPEED_PX_MS);
  assert.equal(isFastPointer(pointerSpeed(boundary)), false);

  // Diagonal 3-4-5 triangle: 5px over 100ms (with 100ms window) = 0.05 px/ms
  near(pointerSpeed([{ x: 0, y: 0, t: 0 }, { x: 3, y: 4, t: 100 }], undefined, 100), 0.05);
});

test("pointerSpeed ignores samples older than the 90ms window and decays when resting", () => {
  const samples = [
    { x: 0, y: 0, t: 0 },
    { x: 100, y: 0, t: 20 },
    { x: 102, y: 0, t: 100 },
    { x: 104, y: 0, t: 120 },
  ];
  // At t=120, cutoff is 30ms -> only t=100 and t=120 remain: 2px / 20ms = 0.1 px/ms.
  near(pointerSpeed(samples, 120, POINTER_WINDOW_MS), 0.1);
  assert.equal(isFastPointer(pointerSpeed(samples, 120)), false);

  // After pointer stops moving for > 90ms, speed is 0.
  assert.equal(pointerSpeed(samples, 220), 0);
});

test("PointerTracker tracks fast sweeps vs slow resting", () => {
  const tracker = new PointerTracker();
  tracker.record(0, 0, 1000);
  tracker.record(30, 0, 1050);
  assert.equal(tracker.isFast(1050), true);
  near(tracker.speed(1050), 0.6);

  // Resting 100ms later: older samples pruned, speed drops to 0.
  assert.equal(tracker.isFast(1150), false);
  assert.equal(tracker.speed(1150), 0);

  // Slow drift: 4px over 80ms = 0.05 px/ms.
  tracker.reset();
  tracker.record(10, 10, 2000);
  tracker.record(14, 10, 2080);
  assert.equal(tracker.isFast(2080), false);
  near(tracker.speed(2080), 0.05);
});

test("inSafeTriangle covers diagonal travel toward a right-opening submenu", () => {
  const sub: Rect = { left: 200, top: 80, right: 360, bottom: 240 };
  const from = { x: 100, y: 100 };

  // Moving diagonally down-right across sibling rows toward submenu: inside triangle.
  assert.equal(inSafeTriangle(from, { x: 150, y: 130 }, sub), true);
  assert.equal(inSafeTriangle(from, { x: 180, y: 200 }, sub), true);
  // Already inside submenu rect.
  assert.equal(inSafeTriangle(from, { x: 220, y: 120 }, sub), true);

  // Moving vertically straight down or away (left): outside triangle.
  assert.equal(inSafeTriangle(from, { x: 100, y: 160 }, sub), false);
  assert.equal(inSafeTriangle(from, { x: 80, y: 100 }, sub), false);
  // Moving above or below the submenu's vertical span.
  assert.equal(inSafeTriangle(from, { x: 150, y: 60 }, sub), false);
  assert.equal(inSafeTriangle(from, { x: 150, y: 260 }, sub), false);
});

test("inSafeTriangle covers diagonal travel toward a left-opening submenu", () => {
  const sub: Rect = { left: 20, top: 80, right: 180, bottom: 240 };
  const from = { x: 260, y: 100 };

  // Moving diagonally down-left toward right edge of submenu: inside triangle.
  assert.equal(inSafeTriangle(from, { x: 220, y: 130 }, sub), true);
  assert.equal(inSafeTriangle(from, { x: 190, y: 210 }, sub), true);
  // Inside submenu rect.
  assert.equal(inSafeTriangle(from, { x: 120, y: 150 }, sub), true);

  // Moving away (right) or above/below: outside triangle.
  assert.equal(inSafeTriangle(from, { x: 290, y: 110 }, sub), false);
  assert.equal(inSafeTriangle(from, { x: 220, y: 50 }, sub), false);
  assert.equal(inSafeTriangle(from, { x: 220, y: 260 }, sub), false);
});

test("HoverClock cancels prior timers, respects dispose and AbortSignal", () => {
  let nextId = 1;
  const tasks = new Map<number, { delay: number; fn: () => void }>();
  const setTimer = (fn: () => void, ms: number) => {
    const id = nextId++;
    tasks.set(id, { delay: ms, fn });
    return id;
  };
  const clearTimer = (id: unknown) => {
    tasks.delete(id as number);
  };

  const fired: string[] = [];
  const ac = new AbortController();
  const clock = new HoverClock({ signal: ac.signal, setTimer, clearTimer });

  clock.schedule(150, () => fired.push("first"));
  assert.equal(clock.pending(), true);
  assert.equal(tasks.size, 1);

  // Scheduling a second callback cancels the first.
  clock.schedule(60, () => fired.push("second"));
  assert.equal(tasks.size, 1);
  const [entry] = tasks.values();
  assert.equal(entry.delay, 60);

  // Fire the active timer.
  const [activeId] = tasks.keys();
  tasks.delete(activeId);
  entry.fn();
  assert.deepEqual(fired, ["second"]);
  assert.equal(clock.pending(), false);

  // AbortSignal cancels pending timer and prevents further scheduling.
  clock.schedule(300, () => fired.push("third"));
  assert.equal(tasks.size, 1);
  ac.abort();
  assert.equal(tasks.size, 0);
  assert.equal(clock.pending(), false);

  clock.schedule(50, () => fired.push("after-abort"));
  assert.equal(tasks.size, 0);
  assert.deepEqual(fired, ["second"]);
});
