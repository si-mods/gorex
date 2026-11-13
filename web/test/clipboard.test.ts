import assert from "node:assert/strict";
import { test } from "node:test";
import { writeClipboard } from "../src/clipboard";

function setupMockDOM(options: {
  isSecureContext: boolean;
  writeText?: (text: string) => Promise<void>;
  execCommand: () => boolean;
}) {
  const events: string[] = [];
  const ta = {
    value: "",
    readOnly: false,
    style: { cssText: "" },
    setAttribute() {},
    focus() { events.push("ta.focus"); },
    select() { events.push("ta.select"); },
    setSelectionRange() {},
    remove() { events.push("ta.remove"); },
  };
  const g = globalThis as Record<string, unknown>;
  const prevWindow = g.window;
  const prevNav = g.navigator;
  const prevDoc = g.document;
  g.window = { isSecureContext: options.isSecureContext };
  Object.defineProperty(g, "navigator", {
    configurable: true,
    value: { clipboard: options.writeText ? { writeText: options.writeText } : undefined },
  });
  g.document = {
    activeElement: undefined,
    createElement: () => ta,
    body: { append() { events.push("body.append"); } },
    execCommand: (cmd: string) => {
      events.push(`execCommand:${cmd}`);
      return options.execCommand();
    },
  };
  return {
    events,
    ta,
    restore() {
      g.window = prevWindow;
      Object.defineProperty(g, "navigator", { configurable: true, value: prevNav });
      g.document = prevDoc;
    },
  };
}

test("writeClipboard runs execCommand synchronously before Clipboard API rejects", async () => {
  let gestureActive = true;
  const dom = setupMockDOM({
    isSecureContext: true,
    writeText: async () => {
      // Simulate async rejection after gesture expires.
      await Promise.resolve();
      throw new DOMException("NotAllowedError");
    },
    execCommand: () => gestureActive,
  });
  try {
    const p = writeClipboard("hello");
    // Simulate synchronous event handler ending right after writeClipboard() is called.
    gestureActive = false;
    const ok = await p;
    assert.equal(ok, true);
    assert.equal(dom.ta.value, "hello");
    assert.ok(dom.events.includes("execCommand:copy"));
  } finally {
    dom.restore();
  }
});

test("writeClipboard returns true when Clipboard API resolves even if execCommand fails", async () => {
  const dom = setupMockDOM({
    isSecureContext: true,
    writeText: async () => {},
    execCommand: () => false,
  });
  try {
    assert.equal(await writeClipboard("world"), true);
  } finally {
    dom.restore();
  }
});

test("writeClipboard returns false when both Clipboard API and execCommand fail", async () => {
  const dom = setupMockDOM({
    isSecureContext: true,
    writeText: async () => { throw new Error("denied"); },
    execCommand: () => false,
  });
  try {
    assert.equal(await writeClipboard("nope"), false);
  } finally {
    dom.restore();
  }
});
