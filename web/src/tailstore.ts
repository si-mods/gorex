// IndexedDB persistence and multi-tab coordination for built-in Tailscale
// (docs/web-tailscale.md, State & Tabs). No DOM here so Node tests can
// exercise the coordination logic directly.

const dbName = "gorex-tailnet";
const storeName = "state";
const lockName = "gorex-tailnet-node";
const channelName = "gorex-tailnet";

function openDB(): Promise<IDBDatabase> {
  return new Promise((resolve, reject) => {
    const idb = typeof indexedDB !== "undefined" ? indexedDB : undefined;
    if (!idb) return reject(new Error("indexedDB unavailable"));
    const req = idb.open(dbName, 1);
    req.onupgradeneeded = () => {
      const db = req.result;
      if (!db.objectStoreNames.contains(storeName)) db.createObjectStore(storeName);
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error ?? new Error("indexedDB open failed"));
  });
}

/** Reads every persisted Tailscale state key from IndexedDB; returns {} if unavailable. */
export async function loadTailnetState(): Promise<Record<string, Uint8Array>> {
  try {
    const db = await openDB();
    try {
      return await new Promise<Record<string, Uint8Array>>((resolve, reject) => {
        const tx = db.transaction(storeName, "readonly");
        const store = tx.objectStore(storeName);
        const out: Record<string, Uint8Array> = {};
        const req = store.openCursor();
        req.onsuccess = () => {
          const cur = req.result;
          if (!cur) return resolve(out);
          if (typeof cur.key === "string") {
            const val = cur.value;
            if (val instanceof Uint8Array) out[cur.key] = val;
            else if (val instanceof ArrayBuffer) out[cur.key] = new Uint8Array(val);
          }
          cur.continue();
        };
        req.onerror = () => reject(req.error ?? new Error("indexedDB cursor failed"));
      });
    } finally {
      db.close();
    }
  } catch {
    return {};
  }
}

/** Writes one Tailscale state key to IndexedDB. */
export async function writeTailnetState(key: string, data: Uint8Array): Promise<void> {
  try {
    const db = await openDB();
    try {
      const copy = new Uint8Array(data);
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(storeName, "readwrite");
        tx.objectStore(storeName).put(copy, key);
        tx.oncomplete = () => resolve();
        tx.onerror = () => reject(tx.error ?? new Error("indexedDB write failed"));
      });
    } finally {
      db.close();
    }
  } catch {
    // Best-effort: in-memory store still serves the running session.
  }
}

/** Clears all persisted Tailscale state from IndexedDB (on sign out or switching to ephemeral). */
export async function clearTailnetState(): Promise<void> {
  try {
    const db = await openDB();
    try {
      await new Promise<void>((resolve, reject) => {
        const tx = db.transaction(storeName, "readwrite");
        tx.objectStore(storeName).clear();
        tx.oncomplete = () => resolve();
        tx.onerror = () => reject(tx.error ?? new Error("indexedDB clear failed"));
      });
    } finally {
      db.close();
    }
  } catch {
    // Ignore when storage is unavailable.
  }
}

export type TabMessage =
  | { type: "takeover"; from: string }
  | { type: "released"; from: string }
  | { type: "signed-out"; from: string };

export interface LockAPI {
  request<T>(name: string, options: { ifAvailable?: boolean }, cb: (lock: unknown | null) => Promise<T>): Promise<T>;
}

export interface ChannelAPI {
  postMessage(msg: TabMessage): void;
  onmessage: ((ev: { data: TabMessage }) => void) | null;
  close(): void;
}

export interface TabHandlers {
  onTakeover: () => void;
  onAvailable: () => void;
  onSignedOut: () => void;
}

function defaultLocks(): LockAPI | undefined {
  return typeof navigator !== "undefined" && navigator.locks ? (navigator.locks as unknown as LockAPI) : undefined;
}

function defaultChannel(): ChannelAPI | undefined {
  return typeof BroadcastChannel !== "undefined" ? (new BroadcastChannel(channelName) as unknown as ChannelAPI) : undefined;
}

/**
 * Coordinates a single persistent Tailscale node across browser tabs and
 * PWA windows: one tab holds the Web Lock; another tab sees it is in use
 * and can take over over BroadcastChannel.
 */
export class TabCoordinator {
  readonly id = Math.random().toString(36).slice(2);
  private holdRelease?: () => void;
  private held = false;
  private channel?: ChannelAPI;
  private handlers?: TabHandlers;

  constructor(
    private locks: LockAPI | undefined = defaultLocks(),
    private makeChannel: () => ChannelAPI | undefined = defaultChannel,
  ) {}

  private ensureChannel(handlers: TabHandlers) {
    this.handlers = handlers;
    if (this.channel) return;
    this.channel = this.makeChannel();
    if (!this.channel) return;
    this.channel.onmessage = (ev) => {
      const m = ev.data;
      if (!m || m.from === this.id) return;
      if (m.type === "takeover" && this.held) {
        this.held = false;
        this.handlers?.onTakeover();
        this.release();
      } else if (m.type === "released" && !this.held) {
        this.handlers?.onAvailable();
      } else if (m.type === "signed-out") {
        this.handlers?.onSignedOut();
      }
    };
  }

  /**
   * Tries to acquire the node lock without waiting. Returns true if this
   * tab now owns the node, or false if another tab is holding it.
   */
  async acquire(handlers: TabHandlers): Promise<boolean> {
    this.ensureChannel(handlers);
    if (this.held) return true;
    if (!this.locks) {
      this.held = true;
      return true;
    }
    return new Promise<boolean>((resolve) => {
      let settled = false;
      void this.locks!.request(lockName, { ifAvailable: true }, (lock) => {
        if (!lock) {
          if (!settled) {
            settled = true;
            resolve(false);
          }
          return Promise.resolve();
        }
        this.held = true;
        const wait = new Promise<void>((rel) => {
          this.holdRelease = rel;
        });
        if (!settled) {
          settled = true;
          resolve(true);
        }
        return wait;
      }).catch(() => {
        if (!settled) {
          settled = true;
          this.held = true;
          resolve(true);
        }
      });
    });
  }

  /**
   * Asks the current holding tab to stop and release its lock, then waits
   * to acquire the lock for this tab.
   */
  async takeOver(handlers: TabHandlers): Promise<void> {
    this.ensureChannel(handlers);
    if (this.held) return;
    this.channel?.postMessage({ type: "takeover", from: this.id });
    if (!this.locks) {
      this.held = true;
      return;
    }
    return new Promise<void>((resolve) => {
      let settled = false;
      void this.locks!.request(lockName, {}, () => {
        this.held = true;
        const wait = new Promise<void>((rel) => {
          this.holdRelease = rel;
        });
        if (!settled) {
          settled = true;
          resolve();
        }
        return wait;
      }).catch(() => {
        if (!settled) {
          settled = true;
          this.held = true;
          resolve();
        }
      });
    });
  }

  /** Releases the node lock held by this tab and notifies waiting tabs. */
  release() {
    const wasHeld = this.held;
    this.held = false;
    const rel = this.holdRelease;
    this.holdRelease = undefined;
    rel?.();
    if (wasHeld) this.channel?.postMessage({ type: "released", from: this.id });
  }

  /** Tells other tabs that the persistent node has signed out. */
  notifySignedOut() {
    this.channel?.postMessage({ type: "signed-out", from: this.id });
  }

  dispose() {
    this.release();
    this.channel?.close();
    this.channel = undefined;
    this.handlers = undefined;
  }
}
