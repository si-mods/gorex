import type { Host } from "./hosts";

/** Joins a gateway API path onto a host base URL, keeping any path prefix (such as /h/<id>). */
export function joinHostURL(base: string, path: string): string {
  return base.replace(/\/+$/, "") + (path.startsWith("/") ? path : "/" + path);
}

export interface SharedGroup {
  phone: string;
  hosts: Host[];
}

interface RawShareHosts {
  phone?: unknown;
  hosts?: unknown;
}

/**
 * Parses the JSON returned by GET /share/hosts on the phone's origin.
 * Host IDs are prefixed with "share:" so they never collide with saved
 * direct hosts or "ts:" tailnet hosts.
 */
export function parseSharedHosts(raw: unknown, origin: string): SharedGroup | undefined {
  if (!raw || typeof raw !== "object") return undefined;
  const obj = raw as RawShareHosts;
  if (!Array.isArray(obj.hosts)) return undefined;
  const phone = typeof obj.phone === "string" && obj.phone.trim() ? obj.phone.trim() : "iPhone";
  const base = origin.replace(/\/+$/, "");
  const hosts: Host[] = [];
  for (const item of obj.hosts) {
    if (!item || typeof item !== "object") continue;
    const h = item as { id?: unknown; name?: unknown; icon?: unknown };
    if (typeof h.id !== "string" || !h.id || typeof h.name !== "string" || !h.name) continue;
    hosts.push({
      id: `share:${h.id}`,
      name: h.name,
      url: `${base}/h/${encodeURIComponent(h.id)}`,
      icon: typeof h.icon === "string" && h.icon ? h.icon : "laptop",
      kind: "share",
    });
  }
  return { phone, hosts };
}

/**
 * Queries /share/hosts on the page's own origin. Returns undefined when
 * the page is not served by Rex Go's hotspot share (for example, 404).
 */
export async function fetchSharedHosts(): Promise<SharedGroup | undefined> {
  try {
    const r = await fetch("/share/hosts", { cache: "no-store" });
    if (!r.ok) return undefined;
    return parseSharedHosts(await r.json(), location.origin);
  } catch {
    return undefined;
  }
}
