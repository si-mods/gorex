// CrossHost (src/spaces.ts), with the cases of ios/GoRexTests/SpacesTests.swift.
import assert from "node:assert/strict";
import { test } from "node:test";
import { blocksOf, CrossHost, feature, Member, parse, read, Reader, Source } from "../src/spaces";

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

// Reader: one read at a time, and an answer for a replaced connection dropped.
/** A transport whose answers the test gives one by one. */
function fake() {
  const waiting: ((j: any) => void)[] = [];
  let calls = 0;
  return {
    get calls() {
      return calls;
    },
    answer: (machine: string) => waiting.shift()!({ machine_id: machine, sessions: [] }),
    transport: {
      kind: "direct" as const,
      socket: () => {
        throw new Error("no socket");
      },
      fetch: () => {
        calls++;
        return new Promise<Response>((ok) => waiting.push((j) => ok(new Response(JSON.stringify(j), { status: 200 }))));
      },
    },
  };
}
const tick = () => new Promise((r) => setTimeout(r, 5));
const welcome = () => ({ features: new Set([feature]) });

test("reader: requests during a read make one more read after it", async () => {
  const f = fake();
  const taken: (string | undefined)[] = [];
  const c = welcome();
  const r = new Reader(() => f.transport, () => c, (a) => taken.push(a?.machine_id));
  r.ask();
  r.ask();
  r.ask();
  await tick();
  assert.equal(f.calls, 1);
  f.answer("A");
  await tick();
  assert.equal(f.calls, 2);
  f.answer("B");
  await tick();
  assert.equal(f.calls, 2);
  assert.deepEqual(taken, ["A", "B"]);
  r.ask();
  await tick();
  assert.equal(f.calls, 3);
  f.answer("C");
  await tick();
  assert.deepEqual(taken, ["A", "B", "C"]);
});

test("reader: an answer for a replaced connection is dropped, and the new one is read", async () => {
  const f = fake();
  const taken: (string | undefined)[] = [];
  let c: { features: Set<string> } | undefined = welcome();
  const r = new Reader(() => f.transport, () => c, (a) => taken.push(a?.machine_id));
  r.ask();
  await tick();
  c = welcome();
  r.ask();
  f.answer("OLD");
  await tick();
  assert.equal(f.calls, 2);
  f.answer("NEW");
  await tick();
  assert.deepEqual(taken, ["NEW"]);
});

test("reader: no connection reads nothing, a server without the feature forgets, a failed read keeps", async () => {
  const f = fake();
  const taken: (string | undefined)[] = [];
  let c: { features: Set<string> } | undefined;
  const r = new Reader(() => f.transport, () => c, (a) => taken.push(a?.machine_id));
  r.ask();
  await tick();
  assert.equal(f.calls, 0);
  c = { features: new Set() };
  r.ask();
  await tick();
  assert.equal(f.calls, 0);
  assert.deepEqual(taken, [undefined]);
  c = welcome();
  let n = 0;
  const failing = new Reader(() => f.transport, () => c, (a) => taken.push(a?.machine_id), async () => {
    n++;
    throw new Error("down");
  });
  failing.ask();
  await tick();
  assert.equal(n, 1);
  assert.deepEqual(taken, [undefined]);
});

test("read: a transport that never answers times out", async () => {
  const t = { ...fake().transport, fetch: () => new Promise<Response>(() => {}) };
  await assert.rejects(read(t, undefined, 20), /timed out/);
});

test("a block entry that is null is skipped", () => {
  assert.deepEqual(blocksOf({ layers: [{ blocks: [null, { block_id: "a" }, {}] }, null] }), ["a"]);
});
