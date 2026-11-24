// ToastHost (src/toast.ts) and Delete Space's Undo (src/deferdelete.ts), with timers and DOM faked.
import assert from "node:assert/strict";
import { mock, test } from "node:test";
import { deleteDeferred } from "../src/deferdelete";
import { SpaceStore } from "../src/projectmodel";
import { minAction, ToastEnd, ToastHost } from "../src/toast";
import { FakeDoc, FakeEl } from "./fakedom";

function make(restoreFocus?: () => void) {
  const doc = new FakeDoc();
  const host = new ToastHost(doc as unknown as Document, { close: () => "Dismiss", icon: () => new FakeEl("i") as unknown as Node, restoreFocus });
  const root = host.root as unknown as FakeEl;
  const live = root.find("toast-live")!;
  const panel = root.find("toast")!;
  return { doc, host, root, live, panel };
}

const ends = () => {
  const log: ToastEnd[] = [];
  return { log, onEnd: (w: ToastEnd) => log.push(w) };
};

test("one live region stays and its text changes", () => {
  const { host, root, live, panel } = make();
  assert.equal(live.getAttribute("role"), "status");
  assert.equal(live.getAttribute("aria-live"), "polite");
  assert.equal(panel.hidden, true);
  host.show({ text: "Deleted Work", action: "Undo" });
  assert.equal(live.textContent, "Deleted Work");
  assert.equal(panel.hidden, false);
  host.show({ text: "Restored Work", ms: 1500 });
  assert.equal(root.find("toast-live"), live, "the same element");
  assert.equal(root.children.filter((c) => c.attrs.get("role") === "status").length, 1);
  assert.equal(live.textContent, "Restored Work");
  host.end("close");
  assert.equal(live.textContent, "");
  assert.equal(panel.hidden, true);
  assert.equal(root.find("toast-live"), live);
});

test("a new toast replaces the old one, which ends first, once", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const { host } = make();
    const a = ends();
    const b = ends();
    host.show({ text: "a", action: "Undo", onEnd: a.onEnd });
    host.show({ text: "b", action: "Undo", onEnd: b.onEnd });
    assert.deepEqual(a.log, ["replaced"]);
    mock.timers.tick(minAction * 2);
    assert.deepEqual(a.log, ["replaced"]);
    assert.deepEqual(b.log, ["timeout"]);
  } finally {
    mock.timers.reset();
  }
});

test("an action toast stays at least 6 s; a note keeps its own time", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const { host } = make();
    const a = ends();
    host.show({ text: "a", action: "Undo", ms: 100, onEnd: a.onEnd });
    mock.timers.tick(minAction - 1);
    assert.deepEqual(a.log, []);
    mock.timers.tick(1);
    assert.deepEqual(a.log, ["timeout"]);
    const n = ends();
    host.show({ text: "n", ms: 1500, onEnd: n.onEnd });
    mock.timers.tick(1500);
    assert.deepEqual(n.log, ["timeout"]);
  } finally {
    mock.timers.reset();
  }
});

test("the pointer or focus on the panel stops the clock", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const { host, panel } = make();
    const a = ends();
    host.show({ text: "a", action: "Undo", onEnd: a.onEnd });
    mock.timers.tick(4000);
    panel.fire("pointerenter");
    mock.timers.tick(60000);
    assert.deepEqual(a.log, []);
    panel.fire("pointerleave");
    mock.timers.tick(1999);
    assert.deepEqual(a.log, []);
    mock.timers.tick(1);
    assert.deepEqual(a.log, ["timeout"]);

    const b = ends();
    host.show({ text: "b", action: "Undo", onEnd: b.onEnd });
    panel.fire("focusin");
    mock.timers.tick(60000);
    assert.deepEqual(b.log, []);
    panel.fire("focusout", { relatedTarget: null });
    mock.timers.tick(minAction);
    assert.deepEqual(b.log, ["timeout"]);
  } finally {
    mock.timers.reset();
  }
});

test("the action runs its handler, ends the toast with no onEnd, and may show another", () => {
  const { host, root, panel } = make();
  const a = ends();
  host.show({ text: "a", action: "Undo", onEnd: a.onEnd, onAction: () => host.show({ text: "again", ms: 1500 }) });
  root.find("toast-action")!.fire("click");
  assert.deepEqual(a.log, []);
  assert.equal(panel.hidden, false);
  assert.equal(root.find("toast-live")!.textContent, "again");
});

test("Escape and the close button close it", () => {
  const { host, root, panel } = make();
  const a = ends();
  host.show({ text: "a", action: "Undo", onEnd: a.onEnd });
  panel.fire("keydown", { key: "a" });
  assert.deepEqual(a.log, []);
  panel.fire("keydown", { key: "Escape" });
  assert.deepEqual(a.log, ["close"]);
  host.show({ text: "b", action: "Undo", onEnd: a.onEnd });
  root.find("toast-close")!.fire("click");
  assert.deepEqual(a.log, ["close", "close"]);
  assert.equal(host.showing, false);
});

test("focus in the panel goes back to the page when the toast goes", () => {
  let back = 0;
  const { doc, host, root } = make(() => back++);
  host.show({ text: "a", action: "Undo" });
  doc.activeElement = root.find("toast-close")!;
  host.end("close");
  assert.equal(back, 1);
  host.show({ text: "b", action: "Undo" });
  doc.activeElement = null;
  host.end("close");
  assert.equal(back, 1, "focus was elsewhere");
});

test("a pointer already on the panel holds a new toast", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const { host, panel } = make();
    host.show({ text: "a", action: "Undo" });
    panel.fire("pointerenter");
    const b = ends();
    host.show({ text: "b", action: "Undo", onEnd: b.onEnd });
    mock.timers.tick(60000);
    assert.deepEqual(b.log, []);
    panel.fire("pointerleave");
    mock.timers.tick(minAction);
    assert.deepEqual(b.log, ["timeout"]);
  } finally {
    mock.timers.reset();
  }
});

// Delete Space's Undo: what is sent is what the commit callback does.
function space() {
  const { host } = make();
  const store = new SpaceStore({ getItem: () => null, setItem: () => {} });
  const sent: string[] = [];
  let redraws = 0;
  const del = (name = "Work") =>
    deleteDeferred(name, {
      toasts: host,
      hidden: store.hidden,
      redraw: () => redraws++,
      commit: (n) => sent.push(n),
      say: { deleted: (n) => "Deleted " + n, restored: (n) => "Restored " + n, undo: "Undo" },
    });
  return { host, store, sent, del, redraws: () => redraws };
}

test("delete hides the space at once and sends nothing", () => {
  const { store, sent, del } = space();
  store.made.push("Work");
  assert.deepEqual(store.names(["Work"]), ["", "Work"]);
  assert.equal(store.mapSpace("h", "Work"), "Work");
  del();
  assert.equal(store.mapSpace("h", "Work"), "");
  assert.deepEqual(store.names(["Work"]), [""]);
  assert.deepEqual(sent, []);
});

test("Undo sends nothing, restores the space and says so for about 1.5 s", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const { host, store, sent, del } = space();
    del();
    ((host.root as unknown as FakeEl).find("toast-action")!).fire("click");
    assert.equal(store.mapSpace("h", "Work"), "Work");
    assert.equal((host.root as unknown as FakeEl).find("toast-live")!.textContent, "Restored Work");
    mock.timers.tick(1500);
    assert.equal(host.showing, false);
    mock.timers.tick(60000);
    assert.deepEqual(sent, []);
  } finally {
    mock.timers.reset();
  }
});

test("timeout, close, replacement and page hide each send exactly once", () => {
  mock.timers.enable({ apis: ["setTimeout", "Date"] });
  try {
    const s = space();
    s.del();
    mock.timers.tick(minAction);
    assert.deepEqual(s.sent, ["Work"]);

    s.del("A");
    s.host.end("close");
    s.host.end("close");
    assert.deepEqual(s.sent, ["Work", "A"]);

    s.del("B");
    s.del("C");
    assert.deepEqual(s.sent, ["Work", "A", "B"]);
    s.host.end("hide");
    mock.timers.tick(minAction * 2);
    assert.deepEqual(s.sent, ["Work", "A", "B", "C"]);
  } finally {
    mock.timers.reset();
  }
});
