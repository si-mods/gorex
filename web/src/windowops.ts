import { HostConn } from "./hostconn";
import { Msg, t } from "./i18n";
import { closeMenu, confirmDanger, DropZone, focusRename, MenuItem, renameField, zoneSplit } from "./manage";
import { describe } from "./pane";
import { hold, paneName, sidebar as drawSidebar, spaceName, windowName, windowTitle } from "./sidebar";
import { layoutOf, leavesOf, WindowView } from "./window";

export interface WindowOpsDeps {
  listEl: () => HTMLElement;
  shell: unknown;
  shown: () => WindowView | undefined;
  picked: () => number;
  busyFocus: () => boolean;
  pick: (conn: HostConn, session: string, window: string, keys: boolean) => void;
  want: (since: number, w: { host: string; session: string; window: string }) => void;
  showWanted: () => void;
  newWindow: (conn: HostConn, session: string) => void;
  closeWindow: (conn: HostConn, session: string, window: string) => void;
  status: (s: Msg) => void;
  canBlockToNewWindow: (conn: HostConn) => boolean;
  moveWindowToSpace: (conn: HostConn) => ((control: NonNullable<HostConn["control"]>, session: string, window: string, target: string) => Promise<unknown>) | undefined;
}

/** A window of a session's view. */
export function windowOf(conn: HostConn, session: string, window: string): any {
  return conn.sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
}

/** The windows of a session, in the host's order. */
export function windowIDs(conn: HostConn, session: string): string[] {
  return (conn.sessions.get(session)?.view?.windows ?? []).map((w: any) => w.window_id);
}

/** Builds the space, window, and pane menu/rename/drag-drop operations bound to main's state. */
export function createWindowOps(deps: WindowOpsDeps) {
  function showSpace(conn: HostConn, session: string) {
    const v = conn.sessions.get(session)?.view;
    const next = v?.active_window_id ?? v?.windows?.[0]?.window_id;
    if (next) deps.pick(conn, session, next, true);
  }

  function spacesMenu(conn: HostConn, current: string): MenuItem[] {
    const spaces: MenuItem[] = [...conn.sessions.values()]
      .filter((o) => o.view)
      .map((o) => ({ label: spaceName(o), disabled: o.id === current, run: () => showSpace(conn, o.id) }));
    return [...spaces, "separator", { label: t("cmdNewSpace"), run: () => newSpace(conn) }, ...spaceMenu(conn, current)];
  }

  function spaceMenu(conn: HostConn, session: string): MenuItem[] {
    return [
      { label: t("cmdRenameSpace"), run: () => startRename(conn, session) },
      { label: t("cmdCloseSpace"), run: () => closeSpace(conn, session) },
    ];
  }

  function windowMenu(conn: HostConn, session: string, window: string): MenuItem[] {
    const w = windowOf(conn, session, window);
    if (!w) return [];
    const ids = windowIDs(conn, session);
    const i = ids.indexOf(window);
    const items: MenuItem[] = [{ label: t("cmdRenameWindow"), run: () => startRename(conn, session, window) }];
    if (windowName(w)) items.push({ label: t("windowResetName"), run: () => renamedWindow(conn, session, window, "") });
    items.push(
      "separator",
      { label: t("windowMoveUp"), disabled: i <= 0, run: () => moveWindowTo(conn, session, window, ids[i - 1], true) },
      { label: t("windowMoveDown"), disabled: i >= ids.length - 1, run: () => moveWindowTo(conn, session, window, ids[i + 1], false) },
    );
    const move = deps.moveWindowToSpace(conn);
    const others = [...conn.sessions.values()].filter((o) => o.id !== session && o.view);
    if (move && others.length) {
      items.push({
        label: t("windowMoveSpace"),
        submenu: others.map((o) => ({
          label: spaceName(o),
          run: () => {
            if (conn.control) move(conn.control, session, window, o.id).catch((e) => deps.status(() => t("statusMoveWindowFailed", { error: describe(e) })));
          },
        })),
      });
    }
    items.push(
      "separator",
      { label: t("cmdNewWindow"), run: () => deps.newWindow(conn, session) },
      { label: t("cmdCloseWindow"), run: () => deps.closeWindow(conn, session, window) },
      { label: t("cmdCloseOtherWindows"), disabled: ids.length < 2, run: () => ids.filter((o) => o !== window).forEach((o) => deps.closeWindow(conn, session, o)) },
    );
    return items;
  }

  function paneMenu(w: WindowView, block: string): MenuItem[] {
    const items: MenuItem[] = [{ label: t("paneRename"), run: () => startPaneRename(w, block) }];
    if (paneName(w.conn, block)) items.push({ label: t("paneResetName"), run: () => renamePane(w.conn, w.session, block, "") });
    items.push("separator");
    const others = (w.conn.sessions.get(w.session)?.view?.windows ?? []).filter((o: any) => o.window_id !== w.window);
    if (others.length) {
      items.push({
        label: t("paneMoveWindow"),
        submenu: others.map((o: any) => ({ label: windowTitle(w.conn, o), run: () => movePaneToWindow(w.conn, w.session, block, o.window_id) })),
      });
    }
    if (deps.canBlockToNewWindow(w.conn) && !w.floatingLayer(block) && w.blocks.length > 1) {
      items.push({ label: t("paneMoveNewWindow"), disabled: !!w.zoomedBlock, run: () => moveToNewWindow(w.conn, w.session, block) });
    }
    const fl = w.floatingLayer(block);
    if (fl && !fl.minimized) {
      items.push("separator");
      items.push({
        label: t("paneFloatMinimize"),
        run: () => w.conn.control?.call("session.set_layer_state", { session_id: w.session, layer_id: fl.layerId, state: "minimized" }),
      });
    }
    return items;
  }

  function startPaneRename(w: WindowView, block: string) {
    const p = w.panes.get(block);
    if (!p) return;
    closeMenu();
    w.setFocus(block, true);
    const name = paneName(w.conn, block) ?? "";
    const f = renameField(name, w.conn.titles.get(block) || t("sidebarTerminal"), t("renamePaneLabel"), (v) => {
      p.editTitle(undefined);
      if (v !== undefined && v !== name) renamePane(w.conn, w.session, block, v);
      if (deps.shown() === w && !deps.busyFocus()) w.focusKeys();
    });
    p.editTitle(f);
    f.focus({ preventScroll: true });
    f.select();
  }

  function renamePane(conn: HostConn, session: string, block: string, label: string) {
    conn.control
      ?.call("session.set_block_label", { session_id: session, block_id: block, label })
      .catch((e) => deps.status(() => t("statusRenamePaneFailed", { error: describe(e) })));
  }

  function moveToNewWindow(conn: HostConn, session: string, block: string) {
    conn.control
      ?.call("gorex.move_block_to_new_window", { session_id: session, block_id: block })
      .catch((e) => deps.status(() => t("statusMovePaneFailed", { error: describe(e) })));
  }

  function mergeWindows(conn: HostConn, session: string, window: string, into: string) {
    const since = deps.picked();
    conn.control?.call("gorex.merge_windows", { session_id: session, window_id: window, into_window_id: into }).then(
      () => {
        deps.want(since, { host: conn.host.id, session, window: into });
        deps.showWanted();
      },
      (e) => deps.status(() => t("statusMergeWindowsFailed", { error: describe(e) })),
    );
  }

  function startRename(conn: HostConn, session: string, window?: string) {
    if (!conn.sessions.has(session) || (window && !windowOf(conn, session, window))) return;
    closeMenu();
    hold.renaming = { host: conn.host.id, session, window };
    drawSidebar(true);
    focusRename(deps.listEl());
  }

  function renamedSpace(conn: HostConn, session: string, name: string | undefined) {
    if (hold.renaming?.host === conn.host.id && hold.renaming.session === session && !hold.renaming.window) hold.renaming = undefined;
    const s = conn.sessions.get(session);
    if (s && name && name !== s.label) {
      const was = s.label;
      s.label = name;
      conn.control?.call("session.set_label", { session_id: session, label: name }).catch((e) => {
        if (s.label === name) s.label = was;
        deps.status(() => t("statusRenameSpaceFailed", { error: describe(e) }));
        drawSidebar();
      });
    }
    drawSidebar();
    if (!hold.renaming) deps.shown()?.focusKeys();
  }

  function renamedWindow(conn: HostConn, session: string, window: string, name: string | undefined) {
    if (hold.renaming?.host === conn.host.id && hold.renaming.window === window) hold.renaming = undefined;
    const w = windowOf(conn, session, window);
    if (w && name !== undefined && name !== (windowName(w) ?? "")) {
      conn.control?.call("session.set_window_label", { session_id: session, window_id: window, label: name }).catch((e) =>
        deps.status(() => t("statusRenameWindowFailed", { error: describe(e) })),
      );
    }
    drawSidebar();
    if (!hold.renaming) deps.shown()?.focusKeys();
  }

  function moveWindowTo(conn: HostConn, session: string, window: string, target: string, before: boolean) {
    const ids = windowIDs(conn, session).filter((id) => id !== window);
    const at = ids.indexOf(target);
    if (at < 0) return;
    const next = before ? target : ids[at + 1];
    const args: Record<string, string> = { session_id: session, window_id: window };
    if (next) args.before_window_id = next;
    conn.control?.call("session.move_window", args).catch((e) => deps.status(() => t("statusMoveWindowFailed", { error: describe(e) })));
  }

  function movePaneToWindow(conn: HostConn, session: string, block: string, window: string) {
    const w = windowOf(conn, session, window);
    const blocks = leavesOf(layoutOf(w));
    const anchor = blocks.includes(w?.focused_block_id) ? w.focused_block_id : blocks[0];
    if (!anchor) return;
    conn.control
      ?.call("session.move_block", { session_id: session, block_id: block, anchor_block_id: anchor, direction: "horizontal", side: "after" })
      .catch((e) => deps.status(() => t("statusMovePaneFailed", { error: describe(e) })));
  }

  function dropPane(w: WindowView, dragged: string, target: string, zone: DropZone) {
    const c = w.conn.control;
    if (!c) return;
    const split = zoneSplit(zone);
    const call = split
      ? c.call("session.move_block", { session_id: w.session, block_id: dragged, anchor_block_id: target, ...split, ratio: 0.5 })
      : c.call("session.swap_blocks", { session_id: w.session, block_id: dragged, other_block_id: target });
    call.then(
      () => {
        if (deps.shown() === w) w.setFocus(dragged, true);
      },
      (e) => deps.status(() => t(split ? "statusMovePaneFailed" : "statusSwapPanesFailed", { error: describe(e) })),
    );
  }

  async function newSpace(conn: HostConn | undefined) {
    const c = conn?.control;
    if (!conn || !c) return;
    const since = deps.picked();
    try {
      const r = (await c.call("session.create", { initial_windows: [{ layout: { block: deps.shell } }] })).payload;
      const window = r.initial_windows?.[0]?.window_id;
      if (!r.session_id || !window) return;
      deps.want(since, { host: conn.host.id, session: r.session_id, window });
      if (!conn.sessions.has(r.session_id)) conn.sessions.set(r.session_id, { id: r.session_id, label: "" });
      await conn.refresh(r.session_id, true);
      deps.showWanted();
    } catch (e) {
      deps.status(() => t("statusNewSpaceFailed", { error: describe(e) }));
    }
  }

  async function closeSpace(conn: HostConn, session: string) {
    const s = conn.sessions.get(session);
    if (!s) return;
    let n = 0;
    for (const w of s.view?.windows ?? []) for (const l of w.layers ?? []) n += l.blocks?.length ?? 0;
    const yes = await confirmDanger(t("spaceCloseTitle", { name: spaceName(s) }), t("spaceCloseBody", { n }), t("cmdCloseSpace"));
    deps.shown()?.focusKeys();
    if (yes) conn.control?.call("session.destroy", { session_id: session }).catch((e) => deps.status(() => t("statusCloseSpaceFailed", { error: describe(e) })));
  }

  return {
    spacesMenu,
    spaceMenu,
    windowMenu,
    paneMenu,
    startRename,
    renamedSpace,
    renamedWindow,
    moveWindowTo,
    movePaneToWindow,
    mergeWindows,
    dropPane,
    newSpace,
  };
}
