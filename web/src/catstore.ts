// The web client's tailcat device key (docs/WEB-006-tailcat.md): kept in
// IndexedDB so a reload does not pair again, in memory when the user
// chose this-tab-only. The key is a secret: the page keeps it here and
// shows only its public half.

const dbName = "gorex-tailcat";
const storeName = "key";
const id = "device";

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

async function withStore<T>(mode: IDBTransactionMode, f: (s: IDBObjectStore) => IDBRequest<T>): Promise<T | undefined> {
  try {
    const db = await openDB();
    try {
      return await new Promise<T | undefined>((resolve, reject) => {
        const tx = db.transaction(storeName, mode);
        const req = f(tx.objectStore(storeName));
        req.onsuccess = () => resolve(req.result);
        req.onerror = () => reject(req.error ?? new Error("indexedDB request failed"));
      });
    } finally {
      db.close();
    }
  } catch {
    return undefined;
  }
}

/** The device private key's text, kept across reloads; undefined without storage. */
export const loadKey = () => withStore("readonly", (s) => s.get(id) as IDBRequest<string>);

/** Keeps the device private key's text across reloads. */
export const saveKey = (key: string) => withStore("readwrite", (s) => s.put(key, id));

/** Forgets the device key (this tab only chosen, or reset). */
export const clearKey = () => withStore("readwrite", (s) => s.delete(id));
