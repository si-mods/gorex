// What New Project, New Tab, Move to Project and the spaces' renames do
// (docs/projects.md, Web client): the calls on the hosts' gateways, with the
// rules of projectmodel.ts. Bound to main's state as windowops.ts is.
import type { Host } from "./hosts";
import type { HostConn } from "./hostconn";
import { Msg, t } from "./i18n";
import { askText, confirmDanger } from "./manage";
import { describe } from "./pane";
import { chooseFolder } from "./projectdialog";
import { hostModel, HostModel, Offline, SpaceStore } from "./projectmodel";
import { isFolderMissing } from "./projects";
import { spaceName, windowTitle } from "./sidebar";

export interface ProjectOpsDeps {
  /** The hosts the page can connect to, in the sidebar's order. */
  hosts: () => Host[];
  conn: (id: string) => HostConn | undefined;
  picked: () => number;
  want: (since: number, w: { host: string; session: string; window: string }) => void;
  showWanted: () => void;
  status: (s: Msg) => void;
  /** A project of a host that has no projects of version 2: a session, as New Space was. */
  plainSpace: (conn: HostConn | undefined) => void;
  /** A tab in a session of a host that has no projects of version 2, as New Window was. */
  plainWindow: (conn: HostConn, session: string) => void;
  /** Draws the sidebar and the bar again. */
  redraw: () => void;
  focusKeys: () => void;
}

/** A host's projects as the page shows them: live while it is connected, else its copy as last seen. */
export interface HostView {
  model: HostModel;
  online: boolean;
}

export function createProjectOps(deps: ProjectOpsDeps) {
  const store = new SpaceStore();
  const offline = new Offline();

  /** Whether a host's projects are live: connected, its sessions read, and projects of version 2. */
  const isLive = (c: HostConn | undefined): c is HostConn => !!c && c.v2 && c.state === "connected" && c.live;

  /** Every host's projects in the sidebar's order: live ones are kept as the copy, the others come from it. */
  function views(): HostView[] {
    const out: HostView[] = [];
    for (const h of deps.hosts()) {
      const c = deps.conn(h.id);
      if (isLive(c)) {
        const model = hostModel({ host: c.host, sessions: c.sessions.values(), offered: c.offered }, (w) => windowTitle(c, w));
        offline.put(model);
        out.push({ model, online: true });
        continue;
      }
      const copy = offline.get(h.id);
      // The host keeps its own name; the copy has the name of the time it was read.
      if (copy) out.push({ model: { ...copy, name: h.name }, online: false });
    }
    return out;
  }

  /** The names of the spaces: carried by the projects of the hosts, and Default, and the ones made here. */
  function spaces(vs: HostView[] = views()): string[] {
    const carried = new Set<string>();
    for (const v of vs) {
      for (const p of v.model.projects) carried.add(store.mapSpace(v.model.host, p.space));
      for (const n of v.model.offered ?? []) carried.add(store.mapSpace(v.model.host, n));
    }
    if (vs.some((v) => v.online)) store.prune(carried);
    return store.names(carried);
  }

  /** The space shown. */
  function space(vs?: HostView[]): string {
    return store.shown(spaces(vs));
  }

  /** The word for a space: Default for "". */
  const label = (name: string) => name || t("spaceDefault");

  /** Opens a project's first tab, or its last used one. */
  function openProject(conn: HostConn, session: string) {
    const v = conn.sessions.get(session)?.view;
    const next = v?.active_window_id ?? v?.windows?.[0]?.window_id;
    if (next) deps.want(deps.picked(), { host: conn.host.id, session, window: next });
    deps.showWanted();
  }

  /** New Project…: the sheet with a host to choose, then the project and its first tab in the folder. */
  async function newProject(conn: HostConn | undefined) {
    const hosts = deps.hosts().map((h) => deps.conn(h.id)).filter((c): c is HostConn => !!c);
    if (!hosts.some((h) => h.v2)) return deps.plainSpace(conn);
    await choose(hosts, conn);
  }

  /** The `+` of a host: the folder sheet of that host, then the project. */
  async function newProjectOn(conn: HostConn) {
    await choose([conn], conn);
  }

  async function choose(hosts: HostConn[], conn: HostConn | undefined) {
    const chosen = await chooseFolder({ title: t("projectNewTitle"), action: t("projectCreate"), hosts, host: conn?.host.id, named: true });
    deps.focusKeys();
    if (!chosen) return;
    await create(chosen.conn, chosen.dir, chosen.name);
  }

  /** Makes the project in the space shown. A folder that is a project already opens that one, or offers to move it here. */
  async function create(conn: HostConn, dir: string, name: string) {
    const since = deps.picked();
    const here = space();
    try {
      const r = await conn.projectCreate(name, dir, here);
      if (!r.project_id) return;
      if (r.exists) return await existing(conn, r.project_id, here);
      if (r.tab_id) deps.want(since, { host: conn.host.id, session: r.project_id, window: r.tab_id });
      if (!conn.sessions.has(r.project_id)) conn.sessions.set(r.project_id, { id: r.project_id, label: name });
      await conn.refresh(r.project_id, true);
      deps.showWanted();
      deps.redraw();
    } catch (e) {
      deps.status(() => t("statusNewSpaceFailed", { error: describe(e) }));
    }
  }

  /** The folder is a project already: show it; from another space, ask whether it moves here. */
  async function existing(conn: HostConn, id: string, here: string) {
    await conn.refresh(id, true);
    const s = conn.sessions.get(id);
    if (!s) return;
    const was = s.project?.space ?? "";
    let shows = was;
    if (was !== here) {
      const yes = await confirmDanger(
        t("projectExistsTitle", { name: spaceName(s) }),
        t("projectExistsBody", { name: spaceName(s), was: label(was), here: label(here) }),
        t("projectMoveHere"),
        false,
      );
      deps.focusKeys();
      if (yes) {
        await moveProjectToSpace(conn, id, here);
        shows = here;
      }
    }
    // Declined: the project stays where it is, and its space shows.
    store.setCurrent(shows);
    openProject(conn, id);
    deps.redraw();
  }

  /** New Tab: in a project, on its host in its folder; with no project, in the home directory. */
  async function newTab(conn: HostConn, session: string) {
    if (!conn.v2) return deps.plainWindow(conn, session);
    const s = conn.sessions.get(session);
    const cwd = s?.project?.dir || undefined;
    const since = deps.picked();
    try {
      // The folder is checked first: a host's gateway may start the shell elsewhere when it is gone.
      if (cwd && conn.listsDirs && (await conn.dirList(cwd)).code) return folderGone(conn, s, cwd);
      const id = await conn.newWindow(session, cwd);
      if (id) deps.want(since, { host: conn.host.id, session, window: id });
      await conn.refresh(session);
      deps.showWanted();
    } catch (e) {
      if (isFolderMissing(e)) return folderGone(conn, s, cwd ?? "");
      deps.status(() => t("statusNewWindowFailed", { error: describe(e) }));
    }
  }

  function folderGone(conn: HostConn, s: { label: string; view?: any } | undefined, dir: string) {
    deps.status(() => t("statusFolderMissing", { dir, host: conn.host.name, name: s ? spaceName(s as any) : "" }));
  }

  /** Move to Project: a tab, with its panes and their programs, to another project of its host; "" is the tabs with no project. */
  async function moveTab(conn: HostConn, session: string, window: string, target: string) {
    if (target === session) return;
    try {
      await conn.projectMove(window, target);
      await Promise.all([conn.refresh(session, true), conn.refresh(target, true)]);
      deps.redraw();
    } catch (e) {
      deps.status(() => t("statusMoveTabFailed", { error: describe(e) }));
    }
  }

  /** Rename Project: only the name; never the folder or the tabs. */
  async function renameProject(conn: HostConn, session: string, name: string) {
    const s = conn.sessions.get(session);
    if (!s) return;
    const was = s.label;
    s.label = name;
    try {
      await conn.projectUpdate(session, { name });
    } catch (e) {
      if (s.label === name) s.label = was;
      deps.status(() => t("statusRenameSpaceFailed", { error: describe(e) }));
    }
    deps.redraw();
  }

  /** Move to Space: the project shows in another space. */
  async function moveProjectToSpace(conn: HostConn, session: string, to: string) {
    const s = conn.sessions.get(session);
    if (!s) return;
    const was = s.project?.space ?? "";
    if (s.project) s.project = { ...s.project, space: to };
    deps.redraw();
    try {
      await conn.projectUpdate(session, { space: to });
    } catch (e) {
      if (s.project?.space === to) s.project = { ...s.project, space: was };
      deps.status(() => t("statusMoveProjectFailed", { error: describe(e) }));
      deps.redraw();
    }
  }

  /** New Space…: a name that shows in the picker, empty, until a project moves in. */
  async function newSpace() {
    const name = await askText(t("spaceNewTitle"), t("spaceNameLabel"), "", t("spaceCreate"));
    deps.focusKeys();
    if (!name) return;
    if (!spaces().includes(name)) store.add(name);
    else store.setCurrent(name);
    deps.redraw();
  }

  /** Rename Space: each connected host renames it on its projects; a host away gets the rename when it connects. */
  async function renameSpace(from: string) {
    if (!from) return;
    const to = await askText(t("spaceRenameTitle"), t("spaceNameLabel"), from, t("spaceRename"));
    deps.focusKeys();
    if (!to || to === from) return;
    const vs = views();
    const away = vs.filter((v) => !v.online && v.model.projects.some((p) => store.mapSpace(v.model.host, p.space) === from)).map((v) => v.model.host);
    store.rename(from, to, away);
    const live = vs.filter((v) => v.online).map((v) => deps.conn(v.model.host)).filter(isLive);
    for (const c of live) for (const s of c.sessions.values()) if (s.project?.space === from) s.project = { ...s.project, space: to };
    deps.redraw();
    await Promise.all(
      live.map((c) =>
        c.spaceRename(from, to).catch((e) => deps.status(() => t("statusRenameSpaceHostFailed", { host: c.host.name, error: describe(e) }))),
      ),
    );
  }

  /** A host connected: it takes the renames it waited for, in order. */
  async function flush(conn: HostConn) {
    if (!conn.v2) return;
    for (const p of store.waiting(conn.host.id)) {
      try {
        await conn.spaceRename(p.from, p.to);
        store.done(p);
      } catch (e) {
        deps.status(() => t("statusRenameSpaceHostFailed", { host: conn.host.name, error: describe(e) }));
        return;
      }
    }
    deps.redraw();
  }

  return { store, views, spaces, space, label, newProject, newProjectOn, newTab, moveTab, renameProject, moveProjectToSpace, newSpace, renameSpace, flush, isLive };
}

export type ProjectOps = ReturnType<typeof createProjectOps>;
