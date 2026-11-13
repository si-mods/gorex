// The tabs, as the Tab placement setting puts them: the bar above the
// panes, or the sidebar of a host's sessions, each with a row for each of
// its windows, their programs' tiles, titles and records, under their
// host in the host view. It draws what a HostConn (hostconn.ts) holds;
// what its rows and menus do is the page's (main.ts, SidebarApp).
import type { HostConn, Session } from "./hostconn";
import { Host, icon } from "./hosts";
import { t } from "./i18n";
import { draggable, MenuItem, onMenu, openMenu, ownDrag, renameField } from "./manage";
import { prefs } from "./prefs";
import { mostUrgent, rank, Status, tiles, tilesWidth } from "./programs";
import { layoutOf, leavesOf, WindowView } from "./window";

/** What the sidebar's rows do, which the page does. */
export interface SidebarApp {
  shown(): WindowView | undefined;
  see(c: HostConn, block: string): void;
  show(c: HostConn, session: string, window: string, keys: boolean): void;
  newWindow(c: HostConn, session: string): void;
  closeWindow(c: HostConn, session: string, window: string): void;
  startRename(c: HostConn, session: string, window?: string): void;
  renamedSpace(c: HostConn, session: string, name: string | undefined): void;
  renamedWindow(c: HostConn, session: string, window: string, name: string | undefined): void;
  spaceMenu(c: HostConn, session: string): MenuItem[];
  windowMenu(c: HostConn, session: string, window: string): MenuItem[];
  movePaneToWindow(c: HostConn, session: string, block: string, window: string): void;
  moveWindowTo(c: HostConn, session: string, window: string, target: string, before: boolean): void;
  /** Whether c's gateway merges a window into another (gorex.merge_windows). */
  mergesWindows(c: HostConn): boolean;
  mergeWindows(c: HostConn, session: string, window: string, into: string): void;
  /** The space chip's menu. */
  spacesMenu(c: HostConn, session: string): MenuItem[];
  /** The hosts the page can connect to, for the host view. */
  hosts(): Host[];
  /** Connects to a host, from its heading in the host view. */
  connect(h: Host): void;
}

let app: SidebarApp;

/** Sets what the rows do; once, as the page starts. */
export function initSidebar(a: SidebarApp) {
  app = a;
}

const list = () => document.getElementById("list")!;

/** What the sidebar holds still for. */
export const hold: {
  /** A space or a window being renamed in the sidebar; the sidebar is not made again meanwhile. */
  renaming?: { session: string; window?: string };
  /** Whether one of the sidebar's rows drags: the drag would end with its row. */
  rowDragging: boolean;
  /** Whether the sidebar waits to be made again, once it no longer holds still. */
  stale: boolean;
} = { rowDragging: false, stale: false };

/** A window's row clicked last, and when: a second click soon after renames it, as the desktop's double click. */
let lastClick: { host: string; window: string; at: number } | undefined;

/** The shown window's blocks, when it is c's. */
function shownBlocks(c: HostConn): string[] {
  const w = app.shown();
  return w?.conn === c ? [...w.panes.keys()] : [];
}

/**
 * The name the user gave a window; undefined when its label is the
 * host's default, "Window " and the end of its ID, as the iOS app tells.
 */
export function windowName(w: any): string | undefined {
  const label: string = w.label ?? "";
  return label && label !== "Window " + String(w.window_id).slice(-8) ? label : undefined;
}

/** A window's title, as the desktop names a tab (Tab.label): its name, else its terminal's title, else its label. */
export function windowTitle(c: HostConn, w: any): string {
  const blocks = leavesOf(layoutOf(w));
  const focused = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
  return windowName(w) || (focused && (paneName(c, focused) || c.titles.get(focused))) || w.label || t("sidebarWindow");
}

/**
 * The name the user gave a pane (session.set_block_label), as the
 * desktop's Pane.Name; undefined while it has the gateway's default.
 * shortcut: a pane its user named "shell" reads as unnamed, as the view
 * gives no other sign; upgrade if the gateway tells a default label apart.
 */
export function paneName(c: HostConn, block: string): string | undefined {
  for (const s of c.sessions.values())
    for (const w of s.view?.windows ?? [])
      for (const l of w.layers ?? [])
        for (const b of l.blocks ?? []) if (b.block_id === block) return b.label && b.label !== "shell" ? b.label : undefined;
  return undefined;
}

/** The room of the tiles of a session's window with the most, so that the titles line up. */
export function tilesRoom(v: any): number {
  return tilesWidth(Math.max(0, ...(v.windows ?? []).map((w: any) => leavesOf(layoutOf(w)).length - 1)));
}

/** A window row's button: its programs' tiles, its title, its most urgent record and its count of panes. */
export function fillWindow(c: HostConn, b: HTMLElement, w: any, room: number) {
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = windowTitle(c, w);
  const blocks = leavesOf(layoutOf(w));
  const front = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
  const urgent = blocks
    .map((x) => mostUrgent(c.statuses.get(x) ?? [], c.seen.get(x)))
    .reduce<Status | undefined>((a, r) => (r && (!a || rank(r) > rank(a)) ? r : a), undefined);
  b.replaceChildren(
    tiles(front ? c.programs.get(front) : undefined, blocks.filter((x) => x !== front).map((x) => c.programs.get(x)), urgent?.state === "done", room),
    title,
  );
  if (urgent?.state === "blocked") {
    const dot = document.createElement("i");
    dot.className = "attention-dot";
    dot.title = t("sidebarNeedsInput");
    b.append(dot);
  } else if (urgent?.progress !== undefined) {
    const ring = document.createElement("i");
    ring.className = "progress-ring";
    ring.style.setProperty("--p", String(urgent.progress));
    ring.title = t("percent", { value: urgent.progress });
    b.append(ring);
  }
  const n = blocks.length;
  if (n > 1) {
    const count = document.createElement("span");
    count.className = "count";
    count.textContent = String(n);
    b.append(count);
  }
}

/**
 * Updates the sidebar's rows of the windows that show a block, after its
 * title, program or records changed: only their buttons' contents, so
 * that the rows keep the pointer's hover and the focus. Without a row,
 * the whole sidebar is made again.
 */
export function sidebarBlock(c: HostConn, block: string) {
  for (const b of shownBlocks(c)) app.see(c, b);
  const nav = list();
  for (const s of c.sessions.values()) {
    const v = s.view;
    if (!v) continue;
    for (const w of v.windows ?? []) {
      if (!leavesOf(layoutOf(w)).includes(block)) continue;
      const b = nav.querySelector<HTMLElement>(`.window[data-host="${CSS.escape(c.host.id)}"][data-window="${CSS.escape(w.window_id)}"] > .block`);
      if (!b) return sidebar(c);
      fillWindow(c, b, w, tilesRoom(v));
    }
  }
}

/**
 * The tabs of c, as the Tab placement setting puts them: the bar above
 * the panes, or the sidebar of each session and a row for each of its
 * windows, under their host in the host view. It holds still while a
 * field renames or a row drags, which would go with their elements, and
 * is made once they are done; force makes it now.
 */
export function sidebar(c: HostConn | undefined, force = false) {
  if (c) for (const b of shownBlocks(c)) app.see(c, b);
  if (!force && (hold.renaming || hold.rowDragging)) {
    hold.stale = true;
    return;
  }
  hold.stale = false;
  const nav = document.createElement("nav");
  if (!prefs.tabPlacement) tabBar(nav, c);
  else if (prefs.sidebarView === "host") hostSections(nav, c);
  else for (const s of c?.sessions.values() ?? []) spaceSection(nav, c!, s);
  if (!nav.children.length) nav.textContent = !c || !c.control ? "" : c.sessions.size ? "" : t("sidebarNoSessions");
  list().replaceChildren(nav);
}

/** A session in the sidebar: its heading, with its menu and New Window, and a row for each of its windows. */
function spaceSection(nav: HTMLElement, c: HostConn, s: Session) {
  const v = s.view;
  if (!v) return;
  const h = document.createElement("h2");
  h.dataset.host = c.host.id;
  h.dataset.session = s.id;
  const add = newWindowButton(c, s.id);
  if (hold.renaming?.session === s.id && !hold.renaming.window) {
    h.append(renameField(s.label, spaceName(s), t("renameSpaceLabel"), (name) => app.renamedSpace(c, s.id, name)), add);
  } else {
    const name = document.createElement("span");
    name.textContent = spaceName(s);
    h.append(name, add);
  }
  onMenu(h, (x, y) => openMenu(x, y, app.spaceMenu(c, s.id), () => app.shown()?.focusKeys()));
  nav.append(h);
  const room = tilesRoom(v);
  for (const w of v.windows ?? []) nav.append(windowRow(c, s.id, w, room));
}

function newWindowButton(c: HostConn, session: string): HTMLButtonElement {
  const add = document.createElement("button");
  add.className = "icon";
  add.type = "button";
  add.append(icon("plus"));
  add.title = t("cmdNewWindow");
  add.onclick = () => app.newWindow(c, session);
  return add;
}

/**
 * The bar above the panes, as the desktop's title bar with the tabs above
 * (workspaceChip, tabStrip): the shown space's name, which opens the
 * spaces' menu, a row for each of its windows, and New Window.
 */
function tabBar(nav: HTMLElement, c: HostConn | undefined) {
  if (!c) return;
  const shown = app.shown();
  const s = (shown?.conn === c && c.sessions.get(shown.session)) || [...c.sessions.values()].find((o) => o.view);
  if (!s?.view) return;
  if (hold.renaming?.session === s.id && !hold.renaming.window) {
    nav.append(renameField(s.label, spaceName(s), t("renameSpaceLabel"), (name) => app.renamedSpace(c, s.id, name)));
  } else {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = "space-chip";
    chip.title = t("spaceMenuLabel");
    chip.setAttribute("aria-haspopup", "menu");
    const name = document.createElement("span");
    name.textContent = spaceName(s);
    chip.append(name, icon("chevron-down"));
    chip.onclick = () => {
      const r = chip.getBoundingClientRect();
      openMenu(r.left, r.bottom + 4, app.spacesMenu(c, s.id), () => app.shown()?.focusKeys());
    };
    onMenu(chip, (x, y) => openMenu(x, y, app.spaceMenu(c, s.id), () => app.shown()?.focusKeys()));
    nav.append(chip);
  }
  const room = tilesRoom(s.view);
  for (const w of s.view.windows ?? []) nav.append(windowRow(c, s.id, w, room));
  nav.append(newWindowButton(c, s.id));
}

/**
 * The sidebar's host view, as the desktop's hostSessions: each host the
 * page can connect to (its host list, and Tailscale's), under its name,
 * the connected one (c's) with its sessions. The page connects to one
 * host at a time: another host's heading connects to it.
 */
function hostSections(nav: HTMLElement, c: HostConn | undefined) {
  const current = c?.host;
  const all = app.hosts();
  if (current && !all.some((h) => h.id === current.id)) all.unshift(current);
  for (const h of all) {
    const on = h.id === current?.id;
    const head = document.createElement(on ? "h3" : "button");
    head.className = "host-head";
    head.dataset.host = h.id;
    const name = document.createElement("span");
    name.textContent = h.name;
    head.append(icon(h.icon), name);
    nav.append(head);
    if (on) {
      for (const s of c!.sessions.values()) spaceSection(nav, c!, s);
      continue;
    }
    (head as HTMLButtonElement).type = "button";
    head.title = t("sidebarConnectHost", { host: h.name });
    head.onclick = () => app.connect(h);
  }
}

/** A space's name: its label, else its view's, else a word. */
export function spaceName(s: Session): string {
  return s.label || s.view?.label || t("sidebarSession");
}

/**
 * A window's row: a click shows it, a second click renames it, its menu,
 * and its drag to another row's place (tabEvents in tabs.go). A pane
 * dropped on it goes into its window (tabRest's drop, movePaneToTab).
 */
function windowRow(c: HostConn, session: string, w: any, room: number): HTMLElement {
  const id: string = w.window_id;
  const row = document.createElement("div");
  row.className = "window";
  row.dataset.host = c.host.id;
  row.dataset.window = id;
  const shown = app.shown();
  if (shown?.conn === c && shown.window === id) row.classList.add("open");
  if (hold.renaming?.session === session && hold.renaming.window === id) {
    row.classList.add("renaming");
    row.append(renameField(windowName(w) ?? "", windowTitle(c, w), t("renameWindowLabel"), (name) => app.renamedWindow(c, session, id, name)));
    return row;
  }
  const b = document.createElement("button");
  b.className = "block";
  b.type = "button";
  fillWindow(c, b, w, room);
  b.onclick = () => {
    const now = performance.now();
    const again = lastClick?.host === c.host.id && lastClick.window === id && now - lastClick.at < 500;
    lastClick = again ? undefined : { host: c.host.id, window: id, at: now };
    app.show(c, session, id, !again);
    if (again) app.startRename(c, session, id);
  };
  const x = document.createElement("button");
  x.className = "icon close";
  x.type = "button";
  x.append(icon("x"));
  x.title = t("cmdCloseWindow");
  x.onclick = () => app.closeWindow(c, session, id);
  row.append(b, x);
  onMenu(row, (x, y) => openMenu(x, y, app.windowMenu(c, session, id), () => app.shown()?.focusKeys()));
  draggable(
    row,
    () => {
      hold.rowDragging = true;
      return { kind: "window", session, window: id };
    },
    () => {
      hold.rowDragging = false;
      markDrop(undefined);
      if (hold.stale) sidebar(c);
    },
  );
  // Where a row or a pane dropped here goes: before or after the row, or into its window. A row's middle half
  // merges its window into this one, as the desktop's tabDropZone, where the host takes it.
  const zone = (e: DragEvent): "before" | "after" | "into" | undefined => {
    const d = ownDrag(e);
    if (!d || d.session !== session || d.window === id) return undefined;
    if (d.kind === "pane") return "into";
    const r = row.getBoundingClientRect();
    const across = !prefs.tabPlacement;
    const at = across ? e.clientX - r.left : e.clientY - r.top, size = across ? r.width : r.height;
    if (app.mergesWindows(c) && at >= size / 4 && at < (size * 3) / 4) return "into";
    return at < size / 2 ? "before" : "after";
  };
  row.addEventListener("dragover", (e) => {
    const z = zone(e);
    markDrop(z && { row, zone: z });
    if (!z) return;
    e.preventDefault();
    e.dataTransfer!.dropEffect = "move";
  });
  row.addEventListener("dragleave", (e) => {
    if (row.dataset.drop && !(e.relatedTarget instanceof Node && row.contains(e.relatedTarget))) markDrop(undefined);
  });
  row.addEventListener("drop", (e) => {
    const d = ownDrag(e);
    const z = zone(e);
    markDrop(undefined);
    if (!d || !z) return;
    e.preventDefault();
    if (d.kind === "pane") app.movePaneToWindow(c, session, d.block, id);
    else if (z === "into") app.mergeWindows(c, session, d.window, id);
    else app.moveWindowTo(c, session, d.window, id, z === "before");
  });
  return row;
}

/** The row a drag shows its drop on, and how: a line before or after it, or a ring for a pane going in. */
function markDrop(at: { row: HTMLElement; zone: "before" | "after" | "into" } | undefined) {
  for (const r of list().querySelectorAll<HTMLElement>(".window[data-drop]")) if (r !== at?.row) delete r.dataset.drop;
  if (at) at.row.dataset.drop = at.zone;
}
