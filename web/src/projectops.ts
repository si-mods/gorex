// What New Project, Change Folder and New Window do (docs/projects.md):
// the calls on the hosts' gateways, with the rules of projects.ts. Bound
// to main's state as windowops.ts is.
import type { HostConn } from "./hostconn";
import { Msg, t } from "./i18n";
import { confirmDanger } from "./manage";
import { describe } from "./pane";
import { chooseFolder } from "./projectdialog";
import { isFolderMissing, Line, planNewWindow, projectLine } from "./projects";
import { spaceName } from "./sidebar";

export interface ProjectOpsDeps {
  /** Every host's connection, in the sidebar's order. */
  conns: () => HostConn[];
  picked: () => number;
  want: (since: number, w: { host: string; session: string; window: string }) => void;
  showWanted: () => void;
  status: (s: Msg) => void;
  /** A project with no folder, as New Space was. */
  plainSpace: (conn: HostConn | undefined) => void;
  /** A window in a session with no folder, as New Window was. */
  plainWindow: (conn: HostConn, session: string) => void;
  focusKeys: () => void;
}

const shell = "com.superlogical.terminal.shell";

export function createProjectOps(deps: ProjectOpsDeps) {
  /** What the sidebar shows under a project's name. */
  function line(c: HostConn, session: string): Line | undefined {
    const p = c.sessions.get(session)?.project;
    return p ? projectLine(p, c.ref, deps.conns().map((x) => x.ref)) : undefined;
  }

  /** Makes a project on target: a session with a window in dir, its home set, named. */
  async function make(target: HostConn, dir: string, name: string) {
    const since = deps.picked();
    const c = target.control;
    if (!c || !target.machine) return;
    const r = (await c.call("session.create", { initial_windows: [{ layout: { block: { flavor: shell, options: { cwd: dir } } } }] })).payload;
    const window = r.initial_windows?.[0]?.window_id;
    if (!r.session_id || !window) return;
    deps.want(since, { host: target.host.id, session: r.session_id, window });
    if (!target.sessions.has(r.session_id)) target.sessions.set(r.session_id, { id: r.session_id, label: "" });
    try {
      await target.setHome(r.session_id, target.machine, dir);
    } catch (e) {
      deps.status(() => t("statusFolderFailed", { error: describe(e) }));
    }
    if (name) await c.call("session.set_label", { session_id: r.session_id, label: name }).then(
      () => {
        const s = target.sessions.get(r.session_id);
        if (s) s.label = name;
      },
      (e) => deps.status(() => t("statusRenameSpaceFailed", { error: describe(e) })),
    );
    await target.refresh(r.session_id, true);
    deps.showWanted();
  }

  /** New Project…: the sheet, then the project and its first window in the folder. */
  async function newProject(conn: HostConn | undefined) {
    const hosts = deps.conns();
    if (!hosts.some((h) => h.keepsProjects && h.listsDirs)) return deps.plainSpace(conn);
    const chosen = await chooseFolder({ title: t("projectNewTitle"), action: t("projectCreate"), hosts, host: conn?.host.id, named: true });
    deps.focusKeys();
    if (!chosen) return;
    try {
      await make(chosen.conn, chosen.dir, chosen.name);
    } catch (e) {
      deps.status(() => t("statusNewSpaceFailed", { error: describe(e) }));
    }
  }

  /** Change Folder…: the sheet, then the session's home set to the folder chosen. */
  async function changeFolder(conn: HostConn, session: string) {
    const s = conn.sessions.get(session);
    if (!s) return;
    const hosts = deps.conns();
    const home = s.project && hosts.find((h) => h.machine && h.machine === s.project?.home_machine && h.ref.connected);
    const chosen = await chooseFolder({
      title: t("projectChangeTitle"),
      action: t("projectChange"),
      hosts,
      host: (home ?? conn).host.id,
      dir: home ? s.project?.dir : undefined,
      named: false,
    });
    deps.focusKeys();
    if (!chosen?.conn.machine) return;
    try {
      await conn.setHome(session, chosen.conn.machine, chosen.dir);
    } catch (e) {
      deps.status(() => t("statusFolderFailed", { error: describe(e) }));
    }
  }

  /** The folder is gone: say so, and offer another. No window opens. */
  async function folderMissing(conn: HostConn, session: string, host: string, dir: string) {
    const s = conn.sessions.get(session);
    const yes = await confirmDanger(t("folderMissingTitle"), t("folderMissingBody", { dir, host, name: s ? spaceName(s) : "" }), t("cmdChangeFolder"), false);
    deps.focusKeys();
    if (yes) await changeFolder(conn, session);
  }

  /** Removes a project's folder: it groups terminals only. */
  async function removeFolder(conn: HostConn, session: string) {
    try {
      await conn.setHome(session, "", "");
    } catch (e) {
      deps.status(() => t("statusFolderFailed", { error: describe(e) }));
    }
  }

  /**
   * New Window: in a project with a folder, on the home host in the folder
   * (planNewWindow); never elsewhere when the home is out of reach.
   */
  async function newWindow(conn: HostConn, session: string) {
    const s = conn.sessions.get(session);
    const hosts = deps.conns();
    const plan = planNewWindow(s?.project, conn.host.id, hosts.map((h) => h.ref));
    if (plan.kind === "plain") return deps.plainWindow(conn, session);
    const name = s ? spaceName(s) : "";
    if (plan.kind === "unknown") return deps.status(() => t("statusProjectHomeUnknown", { project: name }));
    if (plan.kind === "offline") return deps.status(() => t("statusProjectHomeOffline", { project: name, host: plan.host.name }));
    const target = hosts.find((h) => h.host.id === plan.host.id);
    if (!target) return deps.status(() => t("statusProjectHomeUnknown", { project: name }));
    const since = deps.picked();
    try {
      // The folder is checked first on a host the session is not on, whose gateway does not check it.
      if (target.listsDirs && (await target.dirList(plan.cwd)).code) return void (await folderMissing(conn, session, plan.host.name, plan.cwd));
      if (plan.here) {
        const id = await conn.newWindow(session, plan.cwd);
        if (id) deps.want(since, { host: conn.host.id, session, window: id });
        await conn.refresh(session);
        deps.showWanted();
        return;
      }
      // Another host is the home: a window of its own gateway, in a project there with the same folder.
      const same = [...target.sessions.values()].find((o) => o.project?.dir === plan.cwd && o.project.home_machine === target.machine);
      if (same) {
        const id = await target.newWindow(same.id, plan.cwd);
        if (id) deps.want(since, { host: target.host.id, session: same.id, window: id });
        await target.refresh(same.id);
        deps.showWanted();
      } else {
        await make(target, plan.cwd, s?.label ?? "");
      }
    } catch (e) {
      if (isFolderMissing(e)) return void (await folderMissing(conn, session, plan.host.name, plan.cwd));
      deps.status(() => t("statusNewWindowFailed", { error: describe(e) }));
    }
  }

  return { line, newProject, changeFolder, removeFolder, newWindow };
}
