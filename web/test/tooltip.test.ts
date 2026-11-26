import assert from "node:assert/strict";
import { test } from "node:test";
import { TOOLTIP_OPEN_DELAY_MS, TOOLTIP_WARM_WINDOW_MS, TooltipState } from "../src/tooltip";

function createFakeClock() {
  let now = 0;
  let nextId = 1;
  const timers = new Map<number, { at: number; fn: () => void }>();

  const setTimer = (fn: () => void, ms: number) => {
    const id = nextId++;
    timers.set(id, { at: now + ms, fn });
    return id;
  };
  const clearTimer = (id: unknown) => {
    timers.delete(id as number);
  };
  const advance = (ms: number) => {
    now += ms;
    for (;;) {
      let dueId: number | undefined;
      let dueAt = Infinity;
      for (const [id, t] of timers) {
        if (t.at <= now && t.at < dueAt) {
          dueAt = t.at;
          dueId = id;
        }
      }
      if (dueId === undefined) break;
      const t = timers.get(dueId)!;
      timers.delete(dueId);
      t.fn();
    }
  };
  return { get now() { return now; }, setTimer, clearTimer, advance };
}

test("tooltip waits 500ms to open and resets wait when moving across triggers", () => {
  const clock = createFakeClock();
  const events: string[] = [];
  const state = new TooltipState<string>(
    {
      onShow: (id, instant) => events.push(`show:${id}:${instant ? "instant" : "fade"}`),
      onHide: () => events.push("hide"),
    },
    { setTimer: clock.setTimer, clearTimer: clock.clearTimer },
  );

  state.pointerEnter("a", "toolbar", clock.now);
  clock.advance(400);
  assert.deepEqual(events, []);

  // Moving to another trigger before 500ms resets the wait.
  state.pointerEnter("b", "toolbar", clock.now);
  clock.advance(400);
  assert.deepEqual(events, []);

  clock.advance(100);
  assert.deepEqual(events, ["show:b:fade"]);
  assert.equal(state.current(), "b");
  state.dispose();
});

test("warm group swaps immediately in-place and re-enters without wait within 400ms", () => {
  const clock = createFakeClock();
  const events: string[] = [];
  const state = new TooltipState<string>(
    {
      onShow: (id, instant) => events.push(`show:${id}:${instant ? "instant" : "fade"}`),
      onHide: () => events.push("hide"),
    },
    { setTimer: clock.setTimer, clearTimer: clock.clearTimer },
  );

  // Open first trigger after 500ms.
  state.pointerEnter("a", "pane-actions", clock.now);
  clock.advance(TOOLTIP_OPEN_DELAY_MS);
  assert.deepEqual(events, ["show:a:fade"]);

  // Move to neighbor in same group: instant swap, no hide.
  state.pointerEnter("b", "pane-actions", clock.now);
  assert.deepEqual(events, ["show:a:fade", "show:b:instant"]);

  // Leave group: hides immediately.
  state.pointerLeave(clock.now);
  assert.deepEqual(events, ["show:a:fade", "show:b:instant", "hide"]);

  // Re-enter same group within 400ms: shows immediately without waiting 500ms.
  clock.advance(TOOLTIP_WARM_WINDOW_MS - 50);
  state.pointerEnter("c", "pane-actions", clock.now);
  assert.deepEqual(events, ["show:a:fade", "show:b:instant", "hide", "show:c:fade"]);

  // Leave and wait past 400ms warm window: 500ms wait returns.
  state.pointerLeave(clock.now);
  clock.advance(TOOLTIP_WARM_WINDOW_MS + 1);
  state.pointerEnter("a", "pane-actions", clock.now);
  assert.equal(state.current(), undefined);
  clock.advance(TOOLTIP_OPEN_DELAY_MS - 1);
  assert.equal(state.current(), undefined);
  clock.advance(1);
  assert.equal(state.current(), "a");
  state.dispose();
});

test("pressing a trigger hides tooltip and suppresses it until pointer leaves", () => {
  const clock = createFakeClock();
  const events: string[] = [];
  const state = new TooltipState<string>(
    {
      onShow: (id) => events.push(`show:${id}`),
      onHide: () => events.push("hide"),
    },
    { setTimer: clock.setTimer, clearTimer: clock.clearTimer },
  );

  state.pointerEnter("btn", "bar", clock.now);
  clock.advance(TOOLTIP_OPEN_DELAY_MS);
  assert.deepEqual(events, ["show:btn"]);

  // Press hides tooltip and suppresses re-entry on same trigger.
  state.pointerDown("btn");
  assert.deepEqual(events, ["show:btn", "hide"]);
  state.pointerEnter("btn", "bar", clock.now);
  clock.advance(1000);
  assert.deepEqual(events, ["show:btn", "hide"]);

  // Leaving clears suppression; re-entering waits 500ms and shows again.
  state.pointerLeave(clock.now);
  state.pointerEnter("btn", "bar", clock.now);
  clock.advance(TOOLTIP_OPEN_DELAY_MS);
  assert.deepEqual(events, ["show:btn", "hide", "show:btn"]);
  state.dispose();
});

test("keyboard focus shows immediately and Escape or focusout hides", () => {
  const clock = createFakeClock();
  const events: string[] = [];
  const state = new TooltipState<string>(
    {
      onShow: (id) => events.push(`show:${id}`),
      onHide: () => events.push("hide"),
    },
    { setTimer: clock.setTimer, clearTimer: clock.clearTimer },
  );

  state.focusIn("k1", "bar");
  assert.deepEqual(events, ["show:k1"]);
  assert.equal(state.current(), "k1");

  state.escape();
  assert.deepEqual(events, ["show:k1", "hide"]);
  assert.equal(state.current(), undefined);

  state.focusIn("k2", "bar");
  assert.deepEqual(events, ["show:k1", "hide", "show:k2"]);
  state.focusOut("k2");
  assert.deepEqual(events, ["show:k1", "hide", "show:k2", "hide"]);
  state.dispose();
});
