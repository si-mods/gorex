// Projects, spaces and tabs as the web client shows them (docs/projects.md,
// Web client): what a host's sessions say, grouped for the sidebar. A host
// keeps its projects (a session with a folder, in a space) and one session
// of tabs with no project. A space is only a name on projects; this client
// keeps each space's color and order, the spaces it made and the renames an
// offline host still waits for. No DOM here; sidebar's drawing is
// projectsidebar.ts, and the calls are projectops.ts.

/** A project is not shown under Older until this many days passed since its last use. */
export const olderDays = 30;
const day = 86400000;

/** What a v2 session says of its project: `{}` for the host's tabs with no project. */
export interface ProjectInfo {
  /** The project's folder on its host; "" for the tabs with no project. */
  dir: string;
  /** The space's name; "" is Default. */
  space: string;
  /** When a tab last took input or was made (ms); 0 when unknown. */
  used: number;
}

/** Reads a session's `project` as a host of version 2 writes it; none when the field is not an object. */
export function parseProject(j: any): ProjectInfo | undefined {
  if (!j || typeof j !== "object" || Array.isArray(j)) return undefined;
  const t = typeof j.used === "string" ? Date.parse(j.used) : typeof j.used === "number" ? (j.used < 1e12 ? j.used * 1000 : j.used) : 0;
  return { dir: typeof j.dir === "string" ? j.dir : "", space: typeof j.space === "string" ? j.space : "", used: Number.isFinite(t) && t > 0 ? t : 0 };
}

/** The welcome's version of a feature: a map of versions, or a feature written as an object. 0 when none. */
export function featureVersion(welcome: any, name: string): number {
  for (const map of [welcome?.feature_versions, welcome?.features_version, welcome?.versions]) {
    const v = map?.[name];
    if (typeof v === "number") return v;
  }
  const f = (Array.isArray(welcome?.features) ? welcome.features : []).find((x: any) => x && typeof x === "object" && (x.name === name || x.id === name || x.feature === name));
  return typeof f?.version === "number" ? f.version : 0;
}

/** A tab of a project, as the sidebar names it. */
export interface TabRow {
  id: string;
  title: string;
}

/** A project of a host. */
export interface ProjectRow {
  /** The host's own ID of it (a session ID). */
  id: string;
  name: string;
  dir: string;
  space: string;
  used: number;
  tabs: TabRow[];
}

/** A host's projects, as last read. */
export interface HostModel {
  host: string;
  name: string;
  projects: ProjectRow[];
  /** The host's tabs with no project; none when the host has not listed it. */
  loose?: { id: string; tabs: TabRow[] };
  /** Space names the host offers though no project carries them (gorex.space.list). */
  offered?: string[];
}

/** What the model reads of a host's connection. */
export interface Source {
  host: { id: string; name: string };
  sessions: Iterable<{ id: string; label: string; project?: ProjectInfo; view?: any }>;
  offered?: string[];
}

/** A host's model from its live sessions; `title` names a window of a view. */
export function hostModel(c: Source, title: (w: any) => string): HostModel {
  const m: HostModel = { host: c.host.id, name: c.host.name, projects: [], offered: c.offered?.length ? [...c.offered] : undefined };
  for (const s of c.sessions) {
    if (!s.project || !s.view) continue;
    const tabs = (s.view.windows ?? []).map((w: any) => ({ id: String(w.window_id), title: title(w) }));
    if (!s.project.dir) {
      m.loose = { id: s.id, tabs };
      continue;
    }
    m.projects.push({ id: s.id, name: s.label || s.view.label || "", dir: s.project.dir, space: s.project.space, used: s.project.used, tabs });
  }
  return m;
}

/** The projects of a space, the latest used first; the ones not used for olderDays apart. */
export function groupProjects(projects: ProjectRow[], space: string, now: number): { recent: ProjectRow[]; older: ProjectRow[] } {
  const mine = projects.filter((p) => p.space === space).sort((a, b) => b.used - a.used);
  const old = (p: ProjectRow) => p.used > 0 && now - p.used > olderDays * day;
  return { recent: mine.filter((p) => !old(p)), older: mine.filter(old) };
}

/** A folder's name in a path, for a project that has no name. */
export function dirName(dir: string): string {
  return dir.split(/[\\/]+/).filter(Boolean).pop() ?? dir;
}

/** What a rename asks of a host that is not connected: it keeps the old name until it is. */
export interface Pending {
  host: string;
  from: string;
  to: string;
}

interface Saved {
  current: string;
  colors: Record<string, string>;
  order: string[];
  made: string[];
  pending: Pending[];
}

const key = "gorex.web.spaces";

/**
 * This client's own part of the spaces: the space shown, each space's color
 * and order, the empty spaces it made, and its renames waiting for a host.
 * The storage may be missing or throw (a private window): the page works
 * without it, and forgets at reload.
 */
export class SpaceStore {
  current = "";
  colors: Record<string, string> = {};
  order: string[] = [];
  made: string[] = [];
  pending: Pending[] = [];

  constructor(private readonly storage: Pick<Storage, "getItem" | "setItem"> | undefined = safeStorage()) {
    try {
      const j: Partial<Saved> = JSON.parse(storage?.getItem(key) ?? "{}");
      const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
      if (typeof j.current === "string") this.current = j.current;
      if (j.colors && typeof j.colors === "object") for (const [k, v] of Object.entries(j.colors)) if (typeof v === "string") this.colors[k] = v;
      this.order = strings(j.order);
      this.made = strings(j.made);
      if (Array.isArray(j.pending)) this.pending = j.pending.filter((p) => p && typeof p.host === "string" && typeof p.from === "string" && typeof p.to === "string");
    } catch {
      // Nothing kept, or unreadable: start empty.
    }
  }

  private save() {
    try {
      const s: Saved = { current: this.current, colors: this.colors, order: this.order, made: this.made, pending: this.pending };
      this.storage?.setItem(key, JSON.stringify(s));
    } catch {
      // Not kept: the page still works.
    }
  }

  setCurrent(name: string) {
    this.current = name;
    this.save();
  }

  setColor(name: string, color: string | undefined) {
    if (color) this.colors[name] = color;
    else delete this.colors[name];
    this.save();
  }

  /** New Space…: the name shows, empty, until a project moves in. */
  add(name: string) {
    if (!this.made.includes(name)) this.made.push(name);
    this.current = name;
    this.save();
  }

  /** The space a host's project shows in, after the renames that host waits for. */
  mapSpace(host: string, space: string): string {
    let s = space;
    for (const p of this.pending) if (p.host === host && p.from === s) s = p.to;
    return s;
  }

  /** Renames a space on this client. `offline`: the hosts that keep it and are not connected; each gets the rename when it connects. */
  rename(from: string, to: string, offline: string[]) {
    if (this.current === from) this.current = to;
    const swap = (l: string[]) => [...new Set(l.map((x) => (x === from ? to : x)))];
    this.order = swap(this.order);
    this.made = swap(this.made);
    if (from in this.colors) {
      this.colors[to] = this.colors[from];
      delete this.colors[from];
    }
    for (const host of offline) this.pending.push({ host, from, to });
    this.save();
  }

  /** The renames a host waits for, in order. */
  waiting(host: string): Pending[] {
    return this.pending.filter((p) => p.host === host);
  }

  /** A host took a rename. */
  done(p: Pending) {
    const i = this.pending.indexOf(p);
    if (i >= 0) this.pending.splice(i, 1);
    this.save();
  }

  /** A space a project carries is a space of the hosts now: it leaves the list of those made here, and drops with its last project. */
  prune(carried: Set<string>) {
    const kept = this.made.filter((n) => !carried.has(n));
    if (kept.length === this.made.length) return;
    this.made = kept;
    this.save();
  }

  /** The picker's names: the spaces the projects carry, Default, and the ones made here; in the user's order. */
  names(carried: Iterable<string>): string[] {
    const all = new Set<string>(["", ...carried, ...this.made]);
    const first = this.order.filter((n) => all.has(n));
    const rest = [...all].filter((n) => !first.includes(n)).sort((a, b) => (a === "" ? -1 : b === "" ? 1 : a.localeCompare(b)));
    return [...first, ...rest];
  }

  /** The space to show: the saved one while it is in names, else Default. */
  shown(names: string[]): string {
    return names.includes(this.current) ? this.current : "";
  }
}

function safeStorage(): Pick<Storage, "getItem" | "setItem"> | undefined {
  try {
    return typeof localStorage === "undefined" ? undefined : localStorage;
  } catch {
    return undefined;
  }
}

const cacheKey = "gorex.web.projects-cache";

/** Keeps each host's projects as last read, to show a host that is away. The copy is shown, never changed. */
export class Offline {
  private hosts: Record<string, HostModel> = {};

  constructor(private readonly storage: Pick<Storage, "getItem" | "setItem"> | undefined = safeStorage()) {
    try {
      this.hosts = decode(JSON.parse(storage?.getItem(cacheKey) ?? "{}"));
    } catch {
      this.hosts = {};
    }
  }

  get(host: string): HostModel | undefined {
    return this.hosts[host];
  }

  /** Replaces a host's copy with what was read; nothing is written when it is the same. */
  put(m: HostModel) {
    const was = JSON.stringify(this.hosts[m.host]);
    if (was === JSON.stringify(m)) return;
    this.hosts[m.host] = m;
    try {
      this.storage?.setItem(cacheKey, JSON.stringify(this.hosts));
    } catch {
      // Not kept.
    }
  }

  /** Forgets a host's copy, as the host is removed. */
  drop(host: string) {
    if (!(host in this.hosts)) return;
    delete this.hosts[host];
    try {
      this.storage?.setItem(cacheKey, JSON.stringify(this.hosts));
    } catch {
      // Not kept.
    }
  }
}

function decode(j: any): Record<string, HostModel> {
  const out: Record<string, HostModel> = {};
  const str = (v: unknown) => (typeof v === "string" ? v : "");
  const tabs = (v: unknown): TabRow[] => (Array.isArray(v) ? v.filter((x) => x && typeof x.id === "string").map((x) => ({ id: x.id, title: str(x.title) })) : []);
  for (const [host, m] of Object.entries<any>(j && typeof j === "object" ? j : {})) {
    if (!m || typeof m !== "object") continue;
    out[host] = {
      host,
      name: str(m.name),
      projects: (Array.isArray(m.projects) ? m.projects : [])
        .filter((p: any) => p && typeof p.id === "string")
        .map((p: any) => ({ id: p.id, name: str(p.name), dir: str(p.dir), space: str(p.space), used: typeof p.used === "number" ? p.used : 0, tabs: tabs(p.tabs) })),
      offered: Array.isArray(m.offered) ? m.offered.filter((x: unknown) => typeof x === "string") : undefined,
      loose: m.loose && typeof m.loose.id === "string" ? { id: m.loose.id, tabs: tabs(m.loose.tabs) } : undefined,
    };
  }
  return out;
}
