import assert from "node:assert/strict";
import { test } from "node:test";
import { BytesStream, Control, DataStream, openWait, RexError, type SocketLike, type Transport } from "../src/rex";
import { RexProtocol } from "../src/rex-protocol.gen";

class MockSocket implements SocketLike {
  binaryType: BinaryType = "blob";
  sent: Array<string | Uint8Array> = [];
  closedWith?: { code?: number; reason?: string };
  onopen: ((e: Event) => any) | null = null;
  onmessage: ((m: MessageEvent) => any) | null = null;
  onclose: ((e: CloseEvent) => any) | null = null;

  constructor(readonly path: string, readonly protocol: string) {}

  send(data: string | ArrayBufferLike | ArrayBufferView): void {
    if (typeof data === "string") {
      this.sent.push(data);
    } else if (ArrayBuffer.isView(data)) {
      this.sent.push(new Uint8Array(data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength)));
    } else {
      this.sent.push(new Uint8Array(data.slice(0)));
    }
  }

  close(code?: number, reason?: string): void {
    this.closedWith = { code, reason };
  }

  emitOpen(): void {
    this.onopen?.(new Event("open"));
  }

  emitText(text: string): void {
    this.onmessage?.({ data: text } as MessageEvent);
  }

  emitBytes(bytes: Uint8Array): void {
    const buf = bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength);
    this.onmessage?.({ data: buf } as MessageEvent);
  }

  emitClose(code = 1000, reason = ""): void {
    this.onclose?.({ code, reason } as CloseEvent);
  }
}

function makeTransport(): { transport: Transport; sockets: MockSocket[] } {
  const sockets: MockSocket[] = [];
  const transport: Transport = {
    socket(path: string, protocol?: string) {
      const ws = new MockSocket(path, protocol ?? "");
      sockets.push(ws);
      return ws;
    },
    fetch() {
      throw new Error("fetch not implemented in mock");
    },
    url(path: string) {
      return `http://localhost${path}`;
    },
  };
  return { transport, sockets };
}

function makeTicketBase64Url(fill: number): string {
  const bytes = new Uint8Array(16).fill(fill);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function makeWelcomeFrame(tokenFill: number, keepMillis: number, nonce: bigint): Uint8Array {
  const out = new Uint8Array(32);
  out[0] = RexProtocol.dataWelcomeByte;
  out.fill(tokenFill, 1, 17);
  const view = new DataView(out.buffer);
  view.setUint32(20, keepMillis, true);
  view.setBigUint64(24, nonce, true);
  return out;
}

test("Control.open sends hello and applies welcome metadata", async () => {
  const { transport, sockets } = makeTransport();
  const p = Control.open(transport, "tok-prev");
  assert.equal(sockets.length, 1);
  const ws = sockets[0];
  assert.equal(ws.path, "/api/control");
  assert.equal(ws.protocol, RexProtocol.controlProtocol);

  ws.emitOpen();
  assert.equal(ws.sent.length, 1);
  const hello = JSON.parse(ws.sent[0] as string);
  assert.equal(hello.type, "hello");
  assert.equal(hello.api_revision, RexProtocol.apiRevision);
  assert.equal(hello.api_min_revision, RexProtocol.apiMinRevision);
  assert.equal(hello.reconnect_token, "tok-prev");

  ws.emitText(
    JSON.stringify({
      type: "welcome",
      protocol: RexProtocol.controlProtocol,
      api_revision: 14,
      client: { id: "cli-1", reconnect_token: "tok-next" },
      features: ["tunnel", "download"],
    }),
  );

  const c = await p;
  assert.equal(c.clientID, "cli-1");
  assert.equal(c.reconnectToken, "tok-next");
  assert.equal(c.apiRevision, 14);
  assert.equal(c.features.has("tunnel"), true);

  const events: Array<{ type: string; payload: any }> = [];
  c.onEvent = (e) => events.push({ type: e.type, payload: e.payload });
  ws.emitText(JSON.stringify({ type: "event", seq: 1, payload: { type: "session_changed", payload: { id: "s1" } } }));
  assert.equal(events.length, 1);
  assert.equal(events[0].type, "session_changed");

  const callPromise = c.call("session.list", {});
  const req = JSON.parse(ws.sent[1] as string);
  assert.equal(req.type, "request");
  assert.equal(req.method, "session.list");
  ws.emitText(JSON.stringify({ type: "response", id: req.id, seq: 2, ok: true, payload: { sessions: [] } }));
  const reply = await callPromise;
  assert.deepEqual(reply.payload, { sessions: [] });

  c.close();
});

test("DataStream open, welcome token/nonce, resume frame, and onDrop", async () => {
  const { transport, sockets } = makeTransport();
  const ds = new DataStream(transport);
  const received: Uint8Array[] = [];
  let droppedCode: number | undefined;
  ds.onBytes = (b) => received.push(b);
  ds.onDrop = (code) => { droppedCode = code; };

  const openPromise = ds.open(makeTicketBase64Url(0xaa));
  assert.equal(sockets.length, 1);
  const ws1 = sockets[0];
  assert.equal(ws1.path, "/api/data");
  assert.equal(ws1.protocol, RexProtocol.terminalProtocol);

  ws1.emitOpen();
  const hello = ws1.sent[0] as Uint8Array;
  assert.equal(hello.length, 17);
  assert.equal(hello[0], 0x01);
  assert.deepEqual(hello.slice(1), new Uint8Array(16).fill(0xaa));

  ws1.emitBytes(makeWelcomeFrame(0xbb, 5000, 42n));
  await openPromise;
  assert.equal(ds.nonce, 42n);
  assert.deepEqual(ds.token, new Uint8Array(16).fill(0xbb));

  ws1.emitBytes(new Uint8Array([1, 2, 3]));
  assert.equal(received.length, 1);
  assert.deepEqual(received[0], new Uint8Array([1, 2, 3]));

  ws1.emitClose(1006);
  assert.equal(droppedCode, 1006);

  const resumePromise = ds.resume(1234n);
  assert.equal(sockets.length, 2);
  const ws2 = sockets[1];
  ws2.emitOpen();
  const resume = ws2.sent[0] as Uint8Array;
  assert.equal(resume.length, 25);
  assert.equal(resume[0], 0x02);
  assert.deepEqual(resume.slice(1, 17), new Uint8Array(16).fill(0xbb));
  assert.equal(new DataView(resume.buffer).getBigUint64(17, true), 1234n);

  ws2.emitBytes(makeWelcomeFrame(0xcc, 5000, 99n));
  await resumePromise;
  assert.equal(ds.nonce, 99n);
  assert.deepEqual(ds.token, new Uint8Array(16).fill(0xcc));
  ds.close();
});

test("DataStream rejects on 0x82, invalid frames, invalid tickets, pre-welcome close, and timeout", async (t) => {
  const { transport, sockets } = makeTransport();
  const ds = new DataStream(transport);
  await assert.rejects(() => ds.open("!!!invalid-base64!!!"), (err: unknown) => err instanceof RexError && err.code === "protocol");

  const p1 = ds.open(makeTicketBase64Url(0x11));
  sockets[0].emitOpen();
  sockets[0].emitBytes(new Uint8Array([RexProtocol.dataRejectByte, 0x04]));
  await assert.rejects(p1, (err: unknown) => err instanceof RexError && err.code === "rejected");
  assert.equal(sockets[0].closedWith?.code, 1000);

  const pMalformed = ds.open(makeTicketBase64Url(0x12));
  sockets[1].emitOpen();
  sockets[1].emitBytes(new Uint8Array([0x99]));
  await assert.rejects(pMalformed, (err: unknown) => err instanceof RexError && err.code === "protocol");
  assert.equal(sockets[1].closedWith?.code, 1000);

  const pClosed = ds.open(makeTicketBase64Url(0x13));
  sockets[2].emitOpen();
  sockets[2].emitClose(1006);
  await assert.rejects(pClosed, (err: unknown) => err instanceof RexError && err.code === "closed");

  t.mock.timers.enable({ apis: ["setTimeout"] });
  const p2 = ds.open(makeTicketBase64Url(0x22));
  sockets[3].emitOpen();
  t.mock.timers.tick(openWait + 1);
  await assert.rejects(p2, (err: unknown) => err instanceof RexError && err.code === "timeout");
  assert.equal(sockets[3].closedWith?.code, 1000);
});

test("BytesStream open, send, halfClose, onBytes/onClose, 0x82 rejection, and timeout", async (t) => {
  const { transport, sockets } = makeTransport();
  const bs = new BytesStream(transport);
  const chunks: Uint8Array[] = [];
  let closed: { code: number; reason: string } | undefined;
  bs.onBytes = (b) => chunks.push(b);
  bs.onClose = (code, reason) => { closed = { code, reason }; };

  const p = bs.open(makeTicketBase64Url(0x33));
  assert.equal(sockets[0].protocol, RexProtocol.bytesProtocol);
  sockets[0].emitOpen();
  sockets[0].emitBytes(makeWelcomeFrame(0x44, 1000, 0n));
  await p;

  bs.send(new Uint8Array([9, 8]));
  bs.halfClose();
  assert.deepEqual(sockets[0].sent[1], new Uint8Array([9, 8]));
  assert.deepEqual(sockets[0].sent[2], new Uint8Array(0));

  sockets[0].emitBytes(new Uint8Array([7, 6]));
  assert.deepEqual(chunks, [new Uint8Array([7, 6])]);

  sockets[0].emitClose(1000, "done");
  assert.deepEqual(closed, { code: 1000, reason: "done" });

  const pReject = bs.open(makeTicketBase64Url(0x55));
  sockets[1].emitOpen();
  sockets[1].emitBytes(new Uint8Array([RexProtocol.dataRejectByte, 0x03]));
  await assert.rejects(pReject, (err: unknown) => err instanceof RexError && err.code === "rejected");

  t.mock.timers.enable({ apis: ["setTimeout"] });
  const pTimeout = bs.open(makeTicketBase64Url(0x66));
  sockets[2].emitOpen();
  t.mock.timers.tick(openWait + 1);
  await assert.rejects(pTimeout, (err: unknown) => err instanceof RexError && err.code === "timeout");
});
