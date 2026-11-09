import test from "node:test";
import assert from "node:assert/strict";
import {
  VTParser,
  hello,
  resume,
  welcome,
  acknowledgement,
  choose,
  textChunk,
  unwrapEvent,
} from "./core.js";
const encode = (text) => new TextEncoder().encode(text);
const concat = (chunks) =>
  Uint8Array.from(chunks.flatMap((c) => Array.from(c)));

test("control events unwrap both server and session-scoped wire envelopes", () => {
  const change = {
    type: "block_owner_changed",
    payload: { block_id: "block:a", owner: "client:b" },
  };
  assert.deepEqual(
    unwrapEvent({
      type: "event",
      seq: 7,
      payload: {
        type: "session_event",
        payload: { session_id: "session:a", event: change },
      },
    }),
    change,
  );
  const created = {
    type: "session_created",
    payload: { session: { session_id: "session:b" } },
  };
  assert.deepEqual(
    unwrapEvent({ type: "event", seq: 8, payload: created }),
    created,
  );
});

test("VT output and nonce metadata survive every split, including split UTF-8", () => {
  const nonce = "18446744073709551613";
  const before = "\x1b[32m中文 🧪\x1b[0m\n",
    after = "after\x1b[2K";
  const wire = encode(
    before +
      `\x1b_rex1;${nonce};{"type":"resize","cols":92,"rows":28}\x1b\\` +
      after,
  );
  for (let split = 1; split < wire.length; split++) {
    const parser = new VTParser(nonce);
    const items = [
      ...parser.feed(wire.slice(0, split)),
      ...parser.feed(wire.slice(split)),
    ];
    assert.deepEqual(
      concat(items.filter((i) => i.bytes).map((i) => i.bytes)),
      encode(before + after),
    );
    assert.deepEqual(
      items.filter((i) => i.message).map((i) => i.message),
      [{ type: "resize", cols: 92, rows: 28 }],
    );
  }
  const parser = new VTParser(nonce),
    items = [];
  for (const byte of wire) items.push(...parser.feed(Uint8Array.of(byte)));
  assert.deepEqual(
    concat(items.filter((i) => i.bytes).map((i) => i.bytes)),
    encode(before + after),
  );
});

test("ordinary terminal output cannot impersonate metadata from a different nonce", () => {
  const text =
    'before\x1b_rex1;456;{"type":"child_exited","exit_code":42}\x1b\\after';
  const items = new VTParser("123").feed(encode(text));
  assert.equal(items.filter((i) => i.message).length, 0);
  assert.deepEqual(concat(items.map((i) => i.bytes)), encode(text));
  assert.throws(
    () => new VTParser("1").feed(encode("\x1b_rex1;1;" + "x".repeat(65537))),
    /Oversized/,
  );
});

test("reconnect byte counters and nonces retain all 64 bits", () => {
  const count = 9007199254741001n;
  const token = Uint8Array.from({ length: 16 }, (_, i) => i);
  const request = resume(token, count);
  assert.equal(request[0], 2);
  assert.equal(new DataView(request.buffer).getBigUint64(17, true), count);
  assert.equal(
    new DataView(acknowledgement(count).buffer).getBigUint64(0, true),
    count,
  );
  const answer = new Uint8Array(32);
  answer[0] = 0x81;
  answer.set(token, 1);
  new DataView(answer.buffer).setBigUint64(24, 18446744073709551613n, true);
  assert.equal(welcome(answer).nonce, "18446744073709551613");
  assert.deepEqual(welcome(answer).token, token);
  assert.equal(hello("AAAAAAAAAAAAAAAAAAAAAA").length, 17);
  assert.throws(() => hello("invalid"));
  assert.throws(() => welcome(Uint8Array.of(0x82, 1)));
});

test("refresh preserves the selected process and safely falls back after it is closed", () => {
  const views = [
    {
      session_id: "s",
      active_window_id: "w1",
      windows: [
        {
          window_id: "w1",
          focused_block_id: "a",
          layers: [{ blocks: [{ block_id: "a" }] }],
        },
        { window_id: "w2", layers: [{ blocks: [{ block_id: "b" }] }] },
      ],
    },
  ];
  assert.deepEqual(choose(views, { space: "s", window: "w2", block: "b" }), {
    space: "s",
    window: "w2",
    block: "b",
  });
  views[0].windows.pop();
  assert.deepEqual(choose(views, { space: "s", window: "w2", block: "b" }), {
    space: "s",
    window: "w1",
    block: "a",
  });
  assert.deepEqual(choose([]), {
    space: undefined,
    window: undefined,
    block: undefined,
  });
});

test("input batches never split a supplementary Unicode character", () => {
  const text = "a".repeat(16383) + "🧪中文";
  const [first, rest] = textChunk(text);
  assert.equal(first.length, 16383);
  assert.equal(rest, "🧪中文");
  assert.equal(first + rest, text);
});
