// CrossHost (src/spaces.ts), with the cases of ios/GoRexTests/SpacesTests.swift.
import assert from "node:assert/strict";
import { test } from "node:test";
import { CrossHost, Member, parse, Source } from "../src/spaces";

/** A session's view of windows, each with its tiled blocks and, optionally, a floating one. */
function view(session: string, windows: [string, string[], string?][]) {
  return {
    revision: 1,
    session_id: session,
    windows: windows.map(([id, tiled, float]) => {
      const layers: any[] = [{ blocks: tiled.map((block_id) => ({ block_id })) }];
      if (float) layers.push({ blocks: [{ block_id: float }] });
      return { window_id: id, layers };
    }),
  };
}

const member = (machine: string, block_id: string): Member => ({ machine, block_id });
const mac = "mac", studio = "studio", other = "other";

/**
 * The desktop "mac" (machine M) with space "work": its own window w1,
 * and panes of machine S (one in a floating layer), of a machine the app
 * does not reach, and of a block S does not show. S is "studio".
 */
function sources(): Source[] {
  return [
    {
      host: mac,
      machine: "M",
      sessions: [{ id: "work", view: view("work", [["w1", ["b1"]]]), remote: [member("S", "s1"), member("S", "s3"), member("X", "x1"), member("S", "gone")] }],
    },
    {
      host: studio,
      machine: "S",
      sessions: [{ id: "desk", view: view("desk", [["d1", ["s1"]], ["d2", ["s2"]], ["d3", ["s4"], "s3"]]), remote: [] }],
    },
  ];
}

test("the answer reads as the gateway writes it", () => {
  const a = parse({ machine_id: "9c63", sessions: [{ session_id: "session:a", revision: 4, remote_blocks: [{ machine: "f4b7", block_id: "block:b" }] }] });
  assert.equal(a.machine_id, "9c63");
  assert.deepEqual(a.sessions, [{ session_id: "session:a", remote_blocks: [member("f4b7", "block:b")] }]);
  assert.throws(() => parse({ sessions: [] }));
});

test("a space takes the windows of other hosts that hold its panes", () => {
  const c = new CrossHost(sources());
  assert.deepEqual(c.rows(mac, "work"), [
    { host: studio, session: "desk", window: "d1" },
    { host: studio, session: "desk", window: "d3" },
  ]);
  assert.ok(c.isMoved(studio, "desk", "d1"));
  assert.ok(c.isMoved(studio, "desk", "d3"));
  assert.ok(!c.isMoved(studio, "desk", "d2"));
  assert.ok(!c.isMoved(mac, "work", "w1"));
  assert.deepEqual(c.rows(studio, "desk"), []);
});

test("a session is gone when every window of it is in another host's space", () => {
  const c = new CrossHost(sources());
  assert.ok(c.isGone(studio, "desk", ["d1", "d3"]));
  assert.ok(!c.isGone(studio, "desk", ["d1", "d2", "d3"]));
  assert.ok(!c.isGone(studio, "empty", []));
  assert.ok(!c.isGone(mac, "work", ["w1"]));
  // Its own window in another host's space, but it holds studio's.
  const s = sources();
  s.push({ host: other, machine: "O", sessions: [{ id: "o", view: view("o", []), remote: [member("M", "b1")] }] });
  const moved = new CrossHost(s);
  assert.ok(moved.isMoved(mac, "work", "w1"));
  assert.ok(!moved.isGone(mac, "work", ["w1"]));
});

test("only hosts the page reaches show, and a host without a machine matches nothing", () => {
  const c = new CrossHost(sources().slice(0, 1));
  assert.deepEqual(c.rows(mac, "work"), []);
  assert.equal(c.movedCount, 0);
  const s = sources();
  s[1] = { ...s[1], machine: undefined };
  assert.ok(new CrossHost(s).empty);
});

test("each window shows once: the first space to hold it", () => {
  const s = sources();
  s.push({ host: other, machine: "O", sessions: [{ id: "copy", view: view("copy", []), remote: [member("S", "s1"), member("O", "o1")] }] });
  const c = new CrossHost(s);
  assert.deepEqual(c.rows(mac, "work").map((r) => r.window), ["d1", "d3"]);
  assert.deepEqual(c.rows(other, "copy"), []);
});

// Decision 3 (docs/web-multihost.md): the page does not dedupe a machine
// reached by two hosts: both stay in the list with all their windows. The
// iOS test of the same name checks that a remote block resolves to the
// first host of its machine; the port keeps that, and adds that CrossHost
// does not hide the other host: its windows stay unmoved.
test("a machine added twice: matched by the first host, the second is not hidden", () => {
  const s = sources();
  s.splice(1, 0, { host: other, machine: "S", sessions: [{ id: "lan", view: view("lan", [["l1", ["s1"]]]), remote: [] }] });
  const c = new CrossHost(s);
  assert.deepEqual(c.rows(mac, "work"), [{ host: other, session: "lan", window: "l1" }]);
  assert.ok(!c.isMoved(studio, "desk", "d1"));
  assert.ok(!c.isGone(studio, "desk", ["d1", "d2", "d3"]));
});
