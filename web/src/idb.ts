/** Opens (or creates) a version-1 IndexedDB database with a single object store. */
export function openDB(dbName: string, storeName: string): Promise<IDBDatabase> {
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

/**
 * Runs a single object-store request inside a transaction, awaiting
 * `tx.oncomplete` for readwrite transactions before closing the database.
 */
export async function withStore<T>(
  dbName: string,
  storeName: string,
  mode: IDBTransactionMode,
  f: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  const db = await openDB(dbName, storeName);
  try {
    return await new Promise<T>((resolve, reject) => {
      const tx = db.transaction(storeName, mode);
      let value!: T;
      const req = f(tx.objectStore(storeName));
      req.onsuccess = () => {
        value = req.result;
        if (mode === "readonly") resolve(value);
      };
      req.onerror = () => reject(req.error ?? new Error("indexedDB request failed"));
      tx.onabort = () => reject(tx.error ?? new Error("indexedDB transaction aborted"));
      if (mode !== "readonly") {
        tx.oncomplete = () => resolve(value);
        tx.onerror = () => reject(tx.error ?? new Error("indexedDB transaction failed"));
      }
    });
  } finally {
    db.close();
  }
}
