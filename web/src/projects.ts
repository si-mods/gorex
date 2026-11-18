// Projects (docs/projects.md): the folder browser's state over
// gorex.dir.list and the rules of New Project. What a host's projects, spaces
// and tabs are is projectmodel.ts's.

/** The welcome's feature of a gateway that keeps projects; version 2 is this design's. */
export const feature = "gorex.projects";
/** The welcome's feature of a gateway that answers gorex.dir.list. */
export const dirFeature = "gorex.dir_list";
/** The version of `gorex.projects` that keeps a project on its host, inside a space. */
export const projectsVersion = 2;

/** Whether a failed new_window is the gateway's folder_missing (a `conflict` that names the code). */
export function isFolderMissing(e: unknown): boolean {
  const x = e as { code?: string; message?: string } | undefined;
  return x?.code === "conflict" && /folder_missing/.test(x.message ?? "");
}

/** A directory's answer, as gorex.dir.list writes it. */
export interface DirAnswer {
  path: string;
  parent: string;
  home: string;
  dirs: string[];
  more: boolean;
  /** "not_found" or "denied", with path, parent and home still set. */
  code?: string;
}

/** Reads gorex.dir.list's answer. */
export function parseDirs(j: any): DirAnswer {
  if (!j || typeof j.path !== "string") throw new Error("dir.list: no path");
  return {
    path: j.path,
    parent: typeof j.parent === "string" ? j.parent : "",
    home: typeof j.home === "string" ? j.home : "",
    dirs: Array.isArray(j.dirs) ? j.dirs.filter((d: unknown): d is string => typeof d === "string") : [],
    more: !!j.more,
    code: typeof j.code === "string" && j.code ? j.code : undefined,
  };
}

/** A name that is a path of its own: a drive's root, a share, or an absolute path. */
const absolute = /^([A-Za-z]:[\\/]|\\\\|\/)/;

/**
 * The path of a folder listed in path: a name is joined with the path's
 * own separator; at Windows' drives each name is a path already.
 */
export function childPath(path: string, name: string): string {
  if (absolute.test(name)) return name;
  const sep = path.includes("\\") && !path.startsWith("/") ? "\\" : "/";
  return path.endsWith("/") || path.endsWith("\\") ? path + name : path + sep + name;
}

/** The parts of a path, from its root: each with the path that shows it. */
export function crumbs(path: string): { name: string; path: string }[] {
  if (!path) return [];
  const win = path.includes("\\") || /^[A-Za-z]:/.test(path);
  const sep = win ? "\\" : "/";
  const parts = path.split(win ? /[\\/]+/ : /\/+/).filter((p) => p);
  const out: { name: string; path: string }[] = [];
  let at = "";
  if (win && path.startsWith("\\\\") && parts.length >= 2) {
    at = `\\\\${parts[0]}\\${parts[1]}`;
    out.push({ name: at, path: at });
    parts.splice(0, 2);
  } else if (win && /^[A-Za-z]:$/.test(parts[0] ?? "")) {
    at = parts[0] + "\\";
    out.push({ name: at, path: at });
    parts.shift();
  } else if (!win) {
    at = "/";
    out.push({ name: "/", path: "/" });
  }
  for (const p of parts) {
    at = at.endsWith(sep) ? at + p : at + sep + p;
    out.push({ name: p, path: at });
  }
  return out;
}

/** The path's crumbs for the folder browser: a drives listing is one crumb, `thisPC`; a Windows path starts with it. */
export function browserCrumbs(path: string, atDrives: boolean, thisPC: string): { name: string; path: string }[] {
  if (atDrives) return [{ name: thisPC, path: "/" }];
  const cs = crumbs(path);
  return /^([A-Za-z]:|\\\\)/.test(cs[0]?.name ?? "") ? [{ name: thisPC, path: "/" }, ...cs] : cs;
}

/**
 * Which of the crumbs show when the room is short: the first, a "…" for
 * `hidden` of those after it (the ones nearest the first go), then the
 * rest, the last always. `hidden` is clamped to what can go.
 */
export function collapseCrumbs<T>(cs: T[], hidden: number): { head: T[]; gone: T[]; tail: T[] } {
  const k = Math.max(0, Math.min(hidden, cs.length - 2));
  return { head: cs.slice(0, 1), gone: cs.slice(1, 1 + k), tail: cs.slice(1 + k) };
}

/** A folder's name: the project's default name. A drive's root is its letter; the machine's root has none. */
export function folderName(path: string): string {
  const last = crumbs(path).pop();
  if (!last) return "";
  return /^[A-Za-z]:\\$/.test(last.name) ? last.name.slice(0, 2) : last.name === "/" ? "" : last.name;
}

/**
 * The folder browser's state: the folder listed, and what a click, Up,
 * Home or a typed path does. The page draws it (projectdialog.ts) and
 * redraws on changed. Of two requests the later wins, whatever order
 * their answers come in; a request that fails keeps the listing before it.
 */
export class FolderBrowser {
  path = "";
  parent = "";
  home = "";
  dirs: string[] = [];
  more = false;
  /** "not_found" or "denied" for the folder asked; the listing is empty then. */
  code?: string;
  /** What failed the last request, in words; none after an answer. */
  error?: string;
  loading = false;
  /** The folder asked for that is not there, when the listing shows the nearest one that is. */
  missing?: string;
  private sequence = 0;

  constructor(
    private readonly list: (path: string) => Promise<DirAnswer>,
    private readonly changed: () => void = () => {},
  ) {}

  /** Lists path: "" is the host's home directory. */
  async go(path: string): Promise<void> {
    return this.run(path, false);
  }

  /**
   * Lists path, or when it is not there, the nearest folder above it that
   * is, else the home directory; `missing` then holds path.
   */
  goNearest(path: string): Promise<void> {
    return this.run(path, true);
  }

  private async run(path: string, nearest: boolean): Promise<void> {
    const n = ++this.sequence;
    this.loading = true;
    this.changed();
    try {
      let a = await this.list(path);
      let missing: string | undefined;
      if (nearest && a.code === "not_found") {
        missing = path;
        const up = crumbs(path).slice(0, -1).reverse();
        for (const c of up) {
          a = await this.list(c.path);
          if (a.code !== "not_found") break;
        }
        if (a.code) a = await this.list("");
      }
      if (n !== this.sequence) return;
      this.missing = missing;
      this.path = a.path;
      this.parent = a.parent;
      this.home = a.home;
      this.dirs = a.code ? [] : a.dirs;
      this.more = a.more;
      this.code = a.code;
      this.error = undefined;
    } catch (e) {
      if (n !== this.sequence) return;
      this.error = e instanceof Error ? e.message : String(e);
    }
    this.loading = false;
    this.changed();
  }

  /** Whether the listing is Windows' drives (path "/", each folder a root), which is no folder. */
  get atDrives(): boolean {
    return this.path === "/" && !this.parent && !!this.dirs.length && this.dirs.every((d) => /^([A-Za-z]:[\\/]?|\\\\)/.test(d));
  }

  /** The folder above, when there is one. */
  up(): Promise<void> {
    return this.parent ? this.go(this.parent) : Promise.resolve();
  }

  /** The host's home directory. */
  goHome(): Promise<void> {
    return this.go(this.home || "");
  }

  /** A folder of the listing, by name. */
  open(name: string): Promise<void> {
    return this.go(childPath(this.path, name));
  }

  get canUp(): boolean {
    return !!this.parent && !this.loading;
  }

  /** Whether the folder listed can be the project's: it was listed, with no failure. */
  get canChoose(): boolean {
    return !!this.path && !this.atDrives && !this.code && !this.error && !this.loading;
  }
}

/** Why a host can or cannot make a new project. */
export type HostState = "ok" | "offline" | "old";

/** Whether a host can be picked in New Project: connected, with projects of version 2 and the gateway's folder listing. */
export function hostState(h: { connected: boolean; listsDirs: boolean; projectsV2: boolean }): HostState {
  if (!h.connected) return "offline";
  if (!h.listsDirs || !h.projectsV2) return "old";
  return "ok";
}

/** The host New Project starts on: the preferred one if it can be picked, else the first that can. */
export function defaultHost(hosts: { id: string; state: HostState }[], preferred?: string): string | undefined {
  const ok = hosts.filter((h) => h.state === "ok");
  return (ok.find((h) => h.id === preferred) ?? ok[0])?.id;
}
