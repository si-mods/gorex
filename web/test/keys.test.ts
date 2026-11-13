import assert from "node:assert/strict";
import { test } from "node:test";
import {
  chordFromEvent, encodeChord, isBrowserReserved, parseChord, sameChord, validateBinding,
} from "../src/keyhelpers";

test("encodeChord and parseChord round-trip", () => {
  const c = { ctrl: true, shift: true, code: "KeyP" };
  const s = encodeChord(c);
  assert.equal(s, "Ctrl+Shift+KeyP");
  const parsed = parseChord(s);
  assert.ok(parsed && sameChord(parsed, c));
  assert.equal(parseChord("ShiftLeft"), undefined);
  assert.equal(parseChord(""), undefined);
});

test("chordFromEvent ignores bare modifier keys", () => {
  assert.equal(chordFromEvent({ ctrlKey: true, altKey: false, shiftKey: false, metaKey: false, code: "ControlLeft" }), undefined);
  const c = chordFromEvent({ ctrlKey: false, altKey: true, shiftKey: true, metaKey: false, code: "KeyF" });
  assert.deepEqual(c, { ctrl: undefined, alt: true, shift: true, meta: undefined, code: "KeyF" });
});

test("isBrowserReserved detects browser-owned chords", () => {
  assert.equal(isBrowserReserved({ meta: true, code: "KeyW" }, true), true);
  assert.equal(isBrowserReserved({ alt: true, shift: true, code: "KeyW" }, true), false);
  assert.equal(isBrowserReserved({ ctrl: true, code: "KeyT" }, false), true);
  assert.equal(isBrowserReserved({ ctrl: true, shift: true, code: "KeyP" }, false), false);
  assert.equal(isBrowserReserved({ ctrl: true, code: "Tab" }, true), true);
});

test("validateBinding checks modifier, browser reservation, and conflicts", () => {
  const entries = [
    { id: "find", chord: { meta: true, code: "KeyF" } },
    { id: "command-palette", chord: { meta: true, shift: true, code: "KeyP" } },
  ];
  assert.deepEqual(validateBinding("find", { code: "KeyA" }, entries, true), { ok: false, reason: "need-modifier" });
  assert.deepEqual(validateBinding("find", { code: "F2" }, entries, true), { ok: true });
  assert.deepEqual(validateBinding("find", { meta: true, code: "KeyW" }, entries, true), { ok: false, reason: "browser-reserved" });
  assert.deepEqual(validateBinding("find", { meta: true, shift: true, code: "KeyP" }, entries, true), {
    ok: false, reason: "conflict", owner: "command-palette",
  });
  assert.deepEqual(validateBinding("find", { meta: true, code: "KeyF" }, entries, true), { ok: true });
});
