// One host's connection, as the desktop's hostConn and Rex Go's Host: its
// control connection, its reconnects, the sessions it serves and their
// views, and what its blocks show (titles, programs, records). The page
// (main.ts) shows its windows and draws its sidebar (sidebar.ts) from it.
import { diagnose, Host, Problem } from "./hosts";
import { Msg, msg, t } from "./i18n";
import { describe } from "./pane";
import { programName, Status, statusOf } from "./programs";
import * as projects from "./projects";
import { featureVersion, parseProject, ProjectInfo } from "./projectmodel";
import { Control, Transport } from "./rex";
import type { LinkState } from "./settings";
import type { Tailcat, TailcatHost } from "./tailcat";
import type { Tailnet, TailnetHost } from "./tailnet";
import { webTransportAvailable, webTransportProblem } from "./webtransport";
import { layoutOf, leavesOf } from "./window";

/** A session the gateway serves, and its last view. */
export interface Session {
  id: string;
  label: string;
  /** A host of version 2 says where the session belongs (projectmodel.ts): a project, or `{dir: ""}` for the host's tabs with no project. */
  project?: ProjectInfo;
  view?: any;
  /** The connection a read of the view is on its way over. */
  reading?: Control;
  /** A read asked for while one was on its way, and whether it asked to attach. */
  again?: boolean;
  attach?: boolean;
  /** The window of it the stage showed last, for the space keys. */
  lastWindow?: string;
}

/** An installed agent CLI a host reports (gorex.agents.list): its program id, its name and the argv a tab runs. */
export interface Agent {
  id: string;
  name: string;
  command: string[];
}

/** What a host's connection tells the page. */
export interface HostEvents {
  /** The link state or the problem changed. */
  state(c: HostConn): void;
  /** What the status line says of the host. */
  status(c: HostConn, s: Msg): void;
  /** A session came, went or was renamed; destroyed: by session_destroyed. */
  sessions(c: HostConn, destroyed: boolean): void;
  /** A session's view was applied. */
  view(c: HostConn, s: Session): void;
  /** A block's title, program or records changed. */
  block(c: HostConn, block: string): void;
  /** The sessions were read again after the control opened: the shown window's panes start. */
  attached(c: HostConn): void;
  /** The host is live: what was typed while offline goes now. */
  live(c: HostConn): void;
}

/**
 * How long a host waits after its nth failed attempt: hosts.go's
 * hostBackoff doubling, but from 3 s (the desktop starts at 1 s), up to 30 s.
 */
export function backoff(n: number): number {
  return Math.min(3000 * 2 ** Math.max(n - 1, 0), 30000);
}

/** How long a connection stays up for the failures before to be forgotten (hosts.go's hostHealthy). */
export const healthyAfter = 10000;

/**
 * How many control connections open at once, page-wide, as tailnet.ts's
 * probesAtOnce: over the tailnet each one is a TLS handshake in the
 * engine, on the page's thread. The others wait their turn.
 */
export const opensAtOnce = 4;
let opening = 0;
const waiting: (() => void)[] = [];

/**
 * How long one open holds its slot at most. An open that takes longer (an
 * address that never answers, until openWait) goes on outside the count,
 * so that four of them do not keep a healthy host waiting.
 */
export const slotWait = 3000;

/** Gives a slot to the next open waiting, else frees it. */
function release() {
  const next = waiting.shift();
  if (next) next();
  else opening--;
}

/**
 * Runs f once fewer than opensAtOnce others run, for at most slotWait of
 * the slot. Work whose signal aborted while it waited does not run: its
 * slot goes on at once.
 */
async function staggered<T>(signal: AbortSignal, f: () => Promise<T>): Promise<T> {
  if (opening < opensAtOnce) opening++;
  else await new Promise<void>((r) => waiting.push(r));
  let held = true;
  const free = () => {
    if (!held) return;
    held = false;
    clearTimeout(timer);
    release();
  };
  const timer = setTimeout(free, slotWait);
  try {
    if (signal.aborted) throw new Error("stopped");
    return await f();
  } finally {
    free();
  }
}

function recordsOf(j: any): Status[] {
  return (Array.isArray(j) ? j : []).map(statusOf).filter((r): r is Status => !!r);
}

export class HostConn {
  control?: Control;
  /** Whether control's sessions were read (attached) since it opened: panes write only then. */
  live = false;
  state: LinkState = "connecting";
  problem?: Problem;
  /** The failed attempts since the last connection, for the backoff. */
  attempt = 0;
  /** The window the stage showed last on this host. */
  lastWindow?: { session: string; window: string };
  private token?: string;
  /** Counts start() and stop(), so that an attempt of one before ends. */
  private generation = 0;
  private retry?: ReturnType<typeof setTimeout>;
  /** The control connection being opened, waiting for its turn or its welcome; stop() aborts it. */
  private opening?: AbortController;
  /** Forgets the failed attempts once the connection has stayed up healthyAfter. */
  private healthy?: ReturnType<typeof setTimeout>;
  /** The sessions, in the order the host lists them. */
  readonly sessions = new Map<string, Session>();
  /** The terminals' titles, by block, kept up by title_changed. */
  readonly titles = new Map<string, string>();
  /** The foreground programs' names, by block, kept up by process_changed. */
  readonly programs = new Map<string, string | undefined>();
  /** What the programs in each block reported (OSC 7501), by block. */
  readonly statuses = new Map<string, Status[]>();
  /**
   * When the latest done or error record the user saw was updated, by block:
   * those till then were seen. The server keeps it for every client
   * (seen_at, gorex.see_block).
   */
  readonly seen = new Map<string, number>();
  /** The blocks whose gorex.see_block call is in flight. */
  readonly seeing = new Set<string>();
  /** The blocks whose program and records were read; read again after a reconnect. */
  readonly described = new Set<string>();

  constructor(
    /** Its host; an edit of the name or the icon replaces it, one of the address makes a new HostConn. */
    public host: Host,
    readonly transport: Transport,
    private readonly events: HostEvents,
    private readonly tailnet: Tailnet,
    private readonly tailcat: Tailcat,
  ) {}

  /** Connects, and again after a drop or a failure, until stop(). */
  start() {
    this.stop();
    this.setState("connecting");
    void this.connect(this.generation);
  }

  /** Closes the control connection and cancels the retry. The caches stay. */
  stop() {
    this.generation++;
    clearTimeout(this.retry);
    this.retry = undefined;
    clearTimeout(this.healthy);
    this.opening?.abort();
    this.opening = undefined;
    const c = this.control;
    this.control = undefined;
    this.live = false;
    c?.close();
  }

  /** Stays unreachable with p, which no attempt can mend (mixed content): no retry. */
  block(p: Problem) {
    this.stop();
    this.setState("unreachable", p);
  }

  /** Whether the next attempt waits for its backoff. */
  get waiting(): boolean {
    return this.retry !== undefined;
  }

  /** Whether a control connection is being opened now: another attempt would only stack on it. */
  get trying(): boolean {
    return this.opening !== undefined;
  }

  /** Makes the next attempt now, when one waits. */
  poke() {
    if (this.retry === undefined) return;
    clearTimeout(this.retry);
    this.retry = undefined;
    void this.connect(this.generation);
  }

  /** A call on the control connection; it fails while there is none. */
  call(method: string, payload?: any): Promise<{ seq: number; payload: any }> {
    const c = this.control;
    return c ? c.call(method, payload) : Promise.reject(new Error("not connected"));
  }

  /** Makes a window in a session, running the shell or command; its ID. */
  async newWindow(session: string, cwd?: string, command?: string[]): Promise<string | undefined> {
    const block: any = { flavor: "com.superlogical.terminal.shell" };
    const options: any = {};
    if (cwd) options.cwd = cwd;
    if (command && command.length > 0) options.command = command;
    if (Object.keys(options).length > 0) block.options = options;
    const r = (await this.call("session.new_window", { session_id: session, focus: false, layout: { block } })).payload;
    return r.window_id;
  }

  /** Whether the host keeps projects on itself, in spaces (gorex.projects, version 2). A host without it shows its sessions as before. */
  get v2(): boolean {
    const c = this.control;
    return !!c && c.features.has(projects.feature) && featureVersion(c.welcome, projects.feature) >= projects.projectsVersion;
  }

  /** A session's `project` as this host writes it; none from a host that has no projects of version 2. */
  private project(j: any): ProjectInfo | undefined {
    return this.v2 ? parseProject(j) : undefined;
  }

  /** Whether the host lists its folders (gorex.dir_list). */
  get listsDirs(): boolean {
    return !!this.control?.features.has(projects.dirFeature);
  }

  /** Lists a folder of this host (gorex.dir.list); "" is its home directory. */
  async dirList(path: string): Promise<projects.DirAnswer> {
    return projects.parseDirs((await this.call("gorex.dir.list", { path })).payload);
  }

  /** Makes a project from a folder of this host and opens its first tab there (gorex.project.create). */
  async projectCreate(name: string, dir: string, space: string): Promise<{ project_id: string; tab_id?: string; exists: boolean }> {
    try {
      const r = (await this.call("gorex.project.create", { name, dir, space })).payload;
      return { project_id: String(r?.project_id ?? ""), tab_id: r?.tab_id, exists: false };
    } catch (e) {
      // A folder that is a project already is an error that names the project.
      const x = e as { code?: string; projectID?: string };
      if (x?.code === "exists" && x.projectID) return { project_id: x.projectID, exists: true };
      throw e;
    }
  }

  /** Moves a tab, with its panes, to a project of this host; "" is the host's tabs with no project (gorex.project.move). */
  async projectMove(tab: string, project: string): Promise<void> {
    await this.call("gorex.project.move", { tab_id: tab, project_id: project });
  }

  /** Changes a project's name, color, space or pinned launchers (gorex.project.update). */
  async projectUpdate(project: string, fields: { name?: string; color?: string; space?: string; launchers?: string[] | null }): Promise<void> {
    await this.call("gorex.project.update", { project_id: project, ...fields });
  }

  /** Renames a space on every project of this host (gorex.space.rename). */
  async spaceRename(from: string, to: string): Promise<void> {
    await this.call("gorex.space.rename", { from, to });
    await this.readSpaces();
  }

  /**
   * Deletes a space of this host: its projects go to Default, and the name is offered no more
   * (gorex.space.delete). A host of v0.41.0, which has no such method, gets a rename to Default.
   * It answers how many projects moved.
   */
  async spaceDelete(name: string): Promise<number> {
    let r: any;
    try {
      r = (await this.call("gorex.space.delete", { name })).payload;
    } catch (e) {
      if ((e as { code?: string })?.code !== "method_not_found") throw e;
      r = (await this.call("gorex.space.rename", { from: name, to: "" })).payload;
    }
    await this.readSpaces();
    return Number(r?.projects ?? 0);
  }

  /** Installed known agent CLIs reported by this host (gorex.agents.list). */
  agents: Agent[] = [];

  /** Reads gorex.agents.list when the host advertises gorex.agents. */
  async readAgents(): Promise<void> {
    if (!this.control?.features.has("gorex.agents")) return;
    try {
      const r = (await this.call("gorex.agents.list")).payload;
      const list = Array.isArray(r?.agents) ? r.agents : [];
      this.agents = list
        .filter((x: any) => x && typeof x.id === "string" && x.id && Array.isArray(x.command) && x.command.length > 0)
        .map((x: any) => ({ id: x.id, name: typeof x.name === "string" ? x.name : x.id, command: x.command.map(String) }));
      this.events.sessions(this, false);
    } catch (e) {
      console.warn("agents list", e);
    }
  }

  /** The space names this host offers, those of its projects first (gorex.space.list); "" is Default. */
  offered: string[] = [];

  /** Reads gorex.space.list; a failure keeps what was read. */
  async readSpaces(): Promise<void> {
    if (!this.v2) return;
    try {
      const r = (await this.call("gorex.space.list")).payload;
      this.offered = (Array.isArray(r?.spaces) ? r.spaces : []).filter((x: unknown): x is string => typeof x === "string");
      this.events.sessions(this, false);
    } catch (e) {
      console.warn("space list", e);
    }
  }

  /** Closes a window of a session; nothing while there is no control connection. */
  closeWindow(session: string, window: string): Promise<unknown> | undefined {
    return this.control?.call("session.close_window", { session_id: session, window_id: window });
  }

  private setState(s: LinkState, p?: Problem) {
    this.state = s;
    this.problem = p;
    this.events.state(this);
  }

  private status(s: Msg) {
    this.events.status(this, s);
  }

  /** The next attempt, after the backoff of the failures so far (attempt, counted by the caller). */
  private again(gen: number) {
    this.retry = setTimeout(() => {
      this.retry = undefined;
      void this.connect(gen);
    }, backoff(this.attempt));
  }

  /** Opens the control connection to the host, again after it drops. */
  private async connect(gen: number) {
    const h = this.host;
    this.status(() => t("statusConnecting", { host: h.name }));
    if (this.state !== "unreachable") this.setState("connecting");
    let c: Control;
    const opening = (this.opening = new AbortController());
    try {
      c = await staggered(opening.signal, () => Control.open(this.transport, this.token, opening.signal));
    } catch (e) {
      if (this.opening === opening) this.opening = undefined;
      if (gen !== this.generation) return;
      const p = h.certHash
        ? await webTransportProblem(h.url, e)
        : h.kind === "tailnet"
          ? await this.tailnet.diagnose(h as TailnetHost)
          : h.kind === "tailcat"
            ? await this.tailcat.diagnose(h as TailcatHost)
            : await diagnose(h.url, h.token);
      if (gen !== this.generation) return;
      if (h.certHash && !webTransportAvailable()) {
        // No attempt can mend a browser without WebTransport: say so once.
        this.status(() => t("statusBlocked", { host: h.name }));
        this.block(p);
        return;
      }
      this.attempt++;
      this.setState("unreachable", p);
      this.status(() => t("statusRetrying", { host: h.name, problem: p.title }));
      this.again(gen);
      return;
    }
    if (this.opening === opening) this.opening = undefined;
    if (gen !== this.generation) {
      c.close();
      return;
    }
    this.healthy = setTimeout(() => (this.attempt = 0), healthyAfter);
    this.control = c;
    this.live = false;
    this.described.clear();
    this.token = c.reconnectToken;
    this.setState("connected");
    c.onEvent = (e) => this.event(e.type, e.sessionID, e.payload);
    c.onClose = (reason) => {
      if (this.control !== c) return;
      this.control = undefined;
      this.live = false;
      clearTimeout(this.healthy);
      this.attempt++;
      this.setState("connecting");
      this.status(() => t("statusDisconnected", { host: h.name, reason: msg(reason) }));
      this.again(gen);
    };
    try {
      await c.call("session.bootstrap", { event_scope: "all" });
      await this.list();
      void this.readSpaces();
      void this.readAgents();
      // Connected is the usual state: the status line says nothing, and an earlier problem goes.
      this.status("");
      this.events.attached(this);
    } catch (e) {
      this.status(() => t("statusSessionsFailed", { error: describe(e) }));
    }
    if (this.control !== c) return;
    // What was typed while offline goes now.
    this.live = true;
    this.events.live(this);
  }

  /**
   * Follows the host: a session made or gone, a view changed (a split, a
   * closed pane, a ratio, a window), a title. Only what changed is read again.
   */
  private event(type: string, sid: string | undefined, p: any) {
    const sessions = this.sessions;
    switch (type) {
      case "session_created": {
        const id = p.session?.session_id;
        if (id && !sessions.has(id)) {
          sessions.set(id, { id, label: p.session.label ?? "", project: this.project(p.session.project) });
          this.refresh(id, true);
        }
        break;
      }
      case "session_destroyed": {
        const id = p.session_id ?? sid;
        if (!id) break;
        sessions.delete(id);
        this.events.sessions(this, true);
        break;
      }
      case "session_label_changed": {
        const s = sid && sessions.get(sid);
        if (s) {
          s.label = p.label ?? s.label;
          this.events.sessions(this, false);
        }
        break;
      }
      case "session_view_changed": {
        const s = sid && sessions.get(sid);
        if (s && (p.revision ?? Infinity) > (s.view?.revision ?? -1)) {
          this.refresh(s.id);
        }
        break;
      }
      case "block_event":
        if (p.name === "title_changed" && p.block_id) {
          this.titles.set(p.block_id, p.payload?.title ?? "");
          this.events.block(this, p.block_id);
        } else if (p.name === "process_changed" && p.block_id) {
          this.programs.set(p.block_id, programName(p.payload?.foreground));
          this.events.block(this, p.block_id);
        } else if (p.name === "program_status_changed" && p.block_id) {
          this.statuses.set(p.block_id, recordsOf(p.payload?.records));
          this.raiseSeen(p.block_id, Date.parse(p.payload?.seen_at));
          this.events.block(this, p.block_id);
        } else if (p.name === "program_status_removed" && p.block_id) {
          const ids: string[] = p.payload?.ids ?? [];
          this.statuses.set(p.block_id, (this.statuses.get(p.block_id) ?? []).filter((r) => !ids.includes(r.id)));
          this.events.block(this, p.block_id);
        }
        break;
    }
  }

  /** Lists the sessions and reads each one's view. */
  private async list() {
    const c = this.control;
    if (!c) return;
    const sessions = this.sessions;
    const listed: any[] = (await c.call("session.list")).payload.sessions ?? [];
    const ids = new Set(listed.map((s) => s.session_id));
    for (const id of sessions.keys()) if (!ids.has(id)) sessions.delete(id);
    for (const s of listed) {
      const had = sessions.get(s.session_id);
      if (had) {
        had.label = s.label ?? had.label;
        had.project = this.project(s.project);
      } else sessions.set(s.session_id, { id: s.session_id, label: s.label ?? "", project: this.project(s.project) });
    }
    this.events.sessions(this, false);
    await Promise.all(listed.map((s) => this.refresh(s.session_id, true)));
    this.events.sessions(this, false);
  }

  /**
   * Reads a session's view: attached first, as data connections and the
   * layout methods need. One read at a time on a connection; a change
   * meanwhile reads again, attaching if any of the reads asked for it. A
   * read on a connection that was replaced stops, and the new one reads.
   */
  async refresh(id: string, attach = false) {
    const sessions = this.sessions;
    const s = sessions.get(id);
    const c = this.control;
    if (!s || !c) return;
    s.again = true;
    if (attach) s.attach = true;
    if (s.reading === c) return;
    s.reading = c;
    try {
      while (s.again && s.reading === c) {
        s.again = false;
        const fresh = !!s.attach;
        s.attach = false;
        let v: any;
        try {
          v = fresh
            ? (await c.call("session.attach", { session_id: id })).payload.view
            : (await c.call("session.view", { session_id: id })).payload;
        } catch (e) {
          // The next read attaches in its place.
          if (fresh && s.reading === c) s.attach = true;
          throw e;
        }
        if (sessions.get(id) !== s || s.reading !== c) return;
        if (fresh || !s.view || v.revision > s.view.revision) this.apply(s, v);
      }
    } catch (e) {
      console.warn("session view", id, e);
    } finally {
      if (s.reading === c) s.reading = undefined;
    }
  }

  /** Takes a session's new view: titles and programs of new blocks; the page shows it (events.view). */
  private apply(s: Session, v: any) {
    s.view = v;
    if (v.label) s.label = v.label;
    s.project = this.project(v.project) ?? s.project;
    for (const w of v.windows ?? []) {
      for (const b of leavesOf(layoutOf(w))) {
        if (!this.described.has(b)) {
          this.described.add(b);
          this.readProgram(s.id, b);
        }
        if (this.titles.has(b)) continue;
        this.titles.set(b, "");
        this.readTitle(s.id, b, 3);
      }
    }
    this.events.view(this, s);
  }

  /**
   * Reads a block's title. A new block's shell may set it only after this
   * read and before the gateway first looks, which then tells no
   * title_changed: an empty title is read again a few times.
   */
  private readTitle(session: string, b: string, tries: number) {
    this.control?.callBlock("title", session, b).then(
      (r) => {
        if (this.titles.get(b)) return;
        if (r.title) {
          this.titles.set(b, r.title);
          this.events.block(this, b);
        } else if (tries > 1) setTimeout(() => this.readTitle(session, b, tries - 1), 1000);
      },
      () => {},
    );
  }

  /** Takes a block's seen time, which only moves forward; NaN, no seen_at, leaves it. */
  raiseSeen(block: string, at: number) {
    if (at > (this.seen.get(block) ?? -1)) this.seen.set(block, at);
  }

  /**
   * Reads a block's foreground program and records, which process_changed
   * and program_status_changed keep up to date. An answer applies when no
   * event changed what it read while it was in flight.
   */
  private readProgram(session: string, block: string) {
    const c = this.control;
    if (!c) return;
    const programs = this.programs, statuses = this.statuses;
    const hadProgram = programs.get(block), hadRecords = statuses.get(block);
    c.callBlock("process", session, block).then(
      (r) => {
        if (!programs.has(block) || programs.get(block) === hadProgram) {
          programs.set(block, programName(r.foreground));
          this.events.block(this, block);
        }
      },
      () => this.described.delete(block),
    );
    c.callBlock("program_status", session, block).then(
      (r) => {
        this.raiseSeen(block, Date.parse(r.seen_at));
        if (!statuses.has(block) || statuses.get(block) === hadRecords) {
          statuses.set(block, recordsOf(r.records));
          this.events.block(this, block);
        }
      },
      () => {},
    );
  }
}
