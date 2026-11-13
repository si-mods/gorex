// Multi-tab coordination and graceful storage fallback (src/tailstore.ts).
import assert from "node:assert/strict";
import { test } from "node:test";
import { ChannelAPI, clearTailnetState, loadTailnetState, LockAPI, TabCoordinator, TabMessage, writeTailnetState } from "../src/tailstore";

function createBus(): () => ChannelAPI {
  const peers = new Set<ChannelAPI>();
  return () => {
    const ch: ChannelAPI = {
      onmessage: null,
      postMessage(msg: TabMessage) {
        queueMicrotask(() => {
          for (const p of peers) if (p !== ch) p.onmessage?.({ data: msg });
        });
      },
      close() {
        peers.delete(ch);
      },
    };
    peers.add(ch);
    return ch;
  };
}

function createLocks(): LockAPI {
  const held = new Set<string>();
  const waiters = new Map<string, Array<() => void>>();
  return {
    async request<T>(name: string, options: { ifAvailable?: boolean }, cb: (lock: unknown | null) => Promise<T>): Promise<T> {
      if (held.has(name)) {
        if (options.ifAvailable) return cb(null);
        await new Promise<void>((resolve) => {
          const q = waiters.get(name) ?? [];
          q.push(resolve);
          waiters.set(name, q);
        });
      }
      held.add(name);
      try {
        return await cb({ name });
      } finally {
        held.delete(name);
        const next = waiters.get(name)?.shift();
        next?.();
      }
    },
  };
}

test("second tab sees lock in use, takes over, and first tab yields", async () => {
  const locks = createLocks();
  const bus = createBus();
  const tabA = new TabCoordinator(locks, bus);
  const tabB = new TabCoordinator(locks, bus);

  let aTakenOver = 0;
  let bAvailable = 0;
  const noop = () => {};

  assert.equal(await tabA.acquire({ onTakeover: () => aTakenOver++, onAvailable: noop, onSignedOut: noop }), true);
  assert.equal(await tabB.acquire({ onTakeover: noop, onAvailable: () => bAvailable++, onSignedOut: noop }), false);

  await tabB.takeOver({ onTakeover: noop, onAvailable: () => bAvailable++, onSignedOut: noop });
  assert.equal(aTakenOver, 1);

  // Now tabB holds the lock, so tabA cannot acquire without taking over.
  assert.equal(await tabA.acquire({ onTakeover: () => aTakenOver++, onAvailable: noop, onSignedOut: noop }), false);

  tabB.dispose();
  tabA.dispose();
});

test("release notifies waiting tab that lock is available", async () => {
  const locks = createLocks();
  const bus = createBus();
  const tabA = new TabCoordinator(locks, bus);
  const tabB = new TabCoordinator(locks, bus);

  let bAvailable = 0;
  const noop = () => {};

  assert.equal(await tabA.acquire({ onTakeover: noop, onAvailable: noop, onSignedOut: noop }), true);
  assert.equal(await tabB.acquire({ onTakeover: noop, onAvailable: () => bAvailable++, onSignedOut: noop }), false);

  tabA.release();
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(bAvailable, 1);
  assert.equal(await tabB.acquire({ onTakeover: noop, onAvailable: noop, onSignedOut: noop }), true);

  tabB.dispose();
  tabA.dispose();
});

test("sign out notifies other tabs", async () => {
  const locks = createLocks();
  const bus = createBus();
  const tabA = new TabCoordinator(locks, bus);
  const tabB = new TabCoordinator(locks, bus);

  let bSignedOut = 0;
  const noop = () => {};

  await tabA.acquire({ onTakeover: noop, onAvailable: noop, onSignedOut: noop });
  await tabB.acquire({ onTakeover: noop, onAvailable: noop, onSignedOut: () => bSignedOut++ });

  tabA.notifySignedOut();
  await new Promise((r) => setTimeout(r, 10));
  assert.equal(bSignedOut, 1);

  tabB.dispose();
  tabA.dispose();
});

test("fallback without navigator.locks acquires immediately", async () => {
  const tab = new TabCoordinator(undefined, () => undefined);
  const noop = () => {};
  assert.equal(await tab.acquire({ onTakeover: noop, onAvailable: noop, onSignedOut: noop }), true);
  await tab.takeOver({ onTakeover: noop, onAvailable: noop, onSignedOut: noop });
  tab.dispose();
});

test("indexedDB helpers degrade gracefully when indexedDB is unavailable", async () => {
  assert.deepEqual(await loadTailnetState(), {});
  await writeTailnetState("k", new Uint8Array([1, 2, 3]));
  await clearTailnetState();
});
