// Where the page was, kept in this browser (docs/web-multihost.md,
// Persistence): the shown window, by its host (gorex.place), and the
// tailnet hosts the user connected once (gorex.tailnetOpened), which
// connect again when built-in Tailscale finds them. A page from before
// kept only the chosen host (gorex.host); it moves into gorex.place once.
// Every read and write goes on without storage: the page then forgets
// when it closes. No DOM here: web/test/place.test.ts runs it in Node.

/** The shown window, by its host; a host alone shows its first window. */
export interface Place {
  host: string;
  session?: string;
  window?: string;
}

/** What the keys are kept in: localStorage, or a test's. */
export interface Store {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export const placeKey = "gorex.place";
export const openedKey = "gorex.tailnetOpened";
/** The chosen host's ID, as a page from before kept it. */
export const oldHostKey = "gorex.host";

/** Whether a host's ID is a tailnet host's, ts:<StableNodeID> (tailnet.ts). */
export function isTailnetID(id: string): boolean {
  return id.startsWith("ts:");
}

/** A place as kept, or undefined when it is none. */
export function parsePlace(s: string | null): Place | undefined {
  try {
    const p = JSON.parse(s ?? "null");
    if (typeof p?.host !== "string" || !p.host) return undefined;
    const out: Place = { host: p.host };
    if (typeof p.session === "string" && typeof p.window === "string") {
      out.session = p.session;
      out.window = p.window;
    }
    return out;
  } catch {
    return undefined;
  }
}

/** The tailnet hosts' IDs as kept. */
export function parseOpened(s: string | null): string[] {
  try {
    const l = JSON.parse(s ?? "[]");
    return Array.isArray(l) ? l.filter((x): x is string => typeof x === "string" && isTailnetID(x)) : [];
  } catch {
    return [];
  }
}

/**
 * What the keys hold once gorex.host has moved into gorex.place. A place
 * kept already wins. Else the old host is the place: a direct host only
 * while the host list (hosts, its IDs) still has it, a tailnet host always,
 * as the node that finds it may come later, and it was connected once.
 */
export function migrate(old: { host: string | null; place: string | null; opened: string | null }, hosts: string[]): { place?: Place; opened: string[] } {
  const opened = parseOpened(old.opened);
  let place = parsePlace(old.place);
  const id = old.host ?? "";
  if (!place && id && (isTailnetID(id) || hosts.includes(id))) place = { host: id };
  if (isTailnetID(id) && !opened.includes(id)) opened.push(id);
  return { place, opened };
}

function storage(): Store | undefined {
  try {
    return localStorage;
  } catch {
    return undefined;
  }
}

/**
 * The place kept, after moving gorex.host into it if a page from before
 * left one; as the page starts. hosts: the host list's IDs.
 */
export function startPlace(hosts: string[], store = storage()): Place | undefined {
  let r: ReturnType<typeof migrate>;
  try {
    if (!store) return undefined;
    const host = store.getItem(oldHostKey);
    if (host === null) return parsePlace(store.getItem(placeKey));
    r = migrate({ host, place: store.getItem(placeKey), opened: store.getItem(openedKey) }, hosts);
  } catch {
    return undefined;
  }
  try {
    if (r.place) store.setItem(placeKey, JSON.stringify(r.place));
    else store.removeItem(placeKey);
    if (r.opened.length) store.setItem(openedKey, JSON.stringify(r.opened));
    store.removeItem(oldHostKey);
  } catch {
    // Storage that reads but does not write: this load has the place, the next reads gorex.host again.
  }
  return r.place;
}

/** Keeps the shown window's place. */
export function savePlace(p: Place, store = storage()) {
  try {
    store?.setItem(placeKey, JSON.stringify(p));
  } catch {
    // Without storage the place lasts until the page closes.
  }
}

/** The tailnet hosts the user connected once, by ID. */
export function tailnetOpened(store = storage()): Set<string> {
  try {
    return new Set(parseOpened(store?.getItem(openedKey) ?? null));
  } catch {
    return new Set();
  }
}

/** Keeps a tailnet host as connected once: it connects again whenever Tailscale finds it. */
export function openTailnet(id: string, store = storage()) {
  const all = tailnetOpened(store);
  if (all.has(id)) return;
  all.add(id);
  try {
    store?.setItem(openedKey, JSON.stringify([...all]));
  } catch {
    // As savePlace.
  }
}
