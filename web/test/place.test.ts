// gorex.host moves into gorex.place (src/place.ts).
import assert from "node:assert/strict";
import { test } from "node:test";
import { migrate, openedKey, oldHostKey, placeKey, startPlace, Store } from "../src/place";

/** A store in memory, as localStorage. */
function memory(init: Record<string, string> = {}): Store & { data: Map<string, string> } {
  const data = new Map(Object.entries(init));
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

const hosts = ["a1", "b2"];

test("no key: no place, nothing written", () => {
  const s = memory();
  assert.equal(startPlace(hosts, s), undefined);
  assert.equal(s.data.size, 0);
});

test("a place kept already is read as it is", () => {
  const s = memory({ [placeKey]: JSON.stringify({ host: "b2", session: "s", window: "w" }) });
  assert.deepEqual(startPlace(hosts, s), { host: "b2", session: "s", window: "w" });
});

test("a direct host moves into the place", () => {
  const s = memory({ [oldHostKey]: "b2" });
  assert.deepEqual(startPlace(hosts, s), { host: "b2" });
  assert.deepEqual(JSON.parse(s.data.get(placeKey)!), { host: "b2" });
  assert.equal(s.data.has(oldHostKey), false);
  assert.equal(s.data.has(openedKey), false);
});

test("a tailnet host moves into the place and counts as connected once", () => {
  const s = memory({ [oldHostKey]: "ts:nABC", [openedKey]: JSON.stringify(["ts:nX"]) });
  assert.deepEqual(startPlace(hosts, s), { host: "ts:nABC" });
  assert.deepEqual(JSON.parse(s.data.get(openedKey)!), ["ts:nX", "ts:nABC"]);
  assert.equal(s.data.has(oldHostKey), false);
});

test("a host no longer in the list gives no place", () => {
  const s = memory({ [oldHostKey]: "gone" });
  assert.equal(startPlace(hosts, s), undefined);
  assert.equal(s.data.has(placeKey), false);
  assert.equal(s.data.has(oldHostKey), false);
});

test("an empty old key gives no place", () => {
  assert.deepEqual(migrate({ host: "", place: null, opened: null }, hosts), { place: undefined, opened: [] });
});

test("a place kept already wins over the old key", () => {
  const r = migrate({ host: "a1", place: JSON.stringify({ host: "b2", session: "s", window: "w" }), opened: null }, hosts);
  assert.deepEqual(r.place, { host: "b2", session: "s", window: "w" });
});

test("broken JSON reads as none", () => {
  const r = migrate({ host: null, place: "{", opened: "nope" }, hosts);
  assert.deepEqual(r, { place: undefined, opened: [] });
});

test("storage that throws: no place, no error", () => {
  const s: Store = {
    getItem() {
      throw new Error("SecurityError");
    },
    setItem() {
      throw new Error("SecurityError");
    },
    removeItem() {
      throw new Error("SecurityError");
    },
  };
  assert.equal(startPlace(hosts, s), undefined);
});

test("storage that throws on write: the place is still read", () => {
  const s = memory({ [oldHostKey]: "a1" });
  s.setItem = () => {
    throw new Error("QuotaExceededError");
  };
  // This load has the place; the next one reads gorex.host again.
  assert.deepEqual(startPlace(hosts, s), { host: "a1" });
  assert.equal(s.data.get(oldHostKey), "a1");
});

test("no storage at all", () => {
  assert.equal(startPlace(hosts, undefined), undefined);
});
