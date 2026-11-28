// The sidebar and the bar of projects (docs/MOD-002-projects.md, Web client): the
// space picker, each host with its projects of the space in order of last
// use, Older, and Tabs with no project; a project expands to its tabs and a
// New Tab. A host that is away shows its copy, greyed and read-only. Hosts
// with no projects of version 2 keep the rows of sidebar.ts. The rules are
// projectmodel.ts's; the calls are projectops.ts's.
import { el } from "./dom";
import type { Agent, HostConn } from "./hostconn";
import type { Host } from "./hosts";
import { icon } from "./hosts";
import { motionEase, motionMs } from "./prefs";
import { glyphUrl, programOf, smallTile, tilesWidth } from "./programs";
import { t } from "./i18n";
import { MenuItem, onMenu, openMenu, ownDrag, renameField } from "./manage";
import { dirName, groupLook, GroupLook, groupProjects, HostModel, projectColor, ProjectRow, TabRow } from "./projectmodel";
import type { HostView, ProjectOps } from "./projectops";
import { hostHead, problemLine, setTileColumn, spaceName, redraw, renaming, sidebarApp, tilesRoom, windowRow } from "./sidebar";
import { setTooltip } from "./tooltip";

/** The colors a space can take, as a dot in the picker. */
export const spaceColors = ["#e5484d", "#f76b15", "#e5a000", "#30a46c", "#3e63dd", "#8e4ec6"];

let ops: ProjectOps;
/** The width of the column of tab tiles in the list being drawn. */
let column = tilesWidth(0);

/** Sets what the projects do; once, as the page starts. */
export function initProjectView(o: ProjectOps) {
  ops = o;
}

const expandedKey = "gorex.web.expanded";
const expanded = new Map<string, boolean>();
try {
  for (const [k, v] of Object.entries(JSON.parse(localStorage.getItem(expandedKey) ?? "{}"))) if (typeof v === "boolean") expanded.set(k, v);
} catch {
  // Nothing kept: the defaults.
}

/** The groups that fold open or shut on the next draw: the sidebar is made again at each change, so the motion is played on the new elements. */
const folds = new Map<string, "open" | "close">();
let first = true;

/** The sessions whose tabs the list shows: the open groups of the space shown, and No Project. */
function listedSessions(vs: HostView[], space: string): { view?: any }[] {
  const app = sidebarApp();
  const out: { view?: any }[] = [];
  const shown = app.shown();
  for (const v of vs) {
    const c = v.online ? app.conn(v.model.host) : undefined;
    if (!c) continue;
    const mine = v.model.projects.map((p) => ({ ...p, space: ops.store.mapSpace(v.model.host, p.space) }));
    const { recent, older } = groupProjects(mine, space, Date.now());
    const seen = (id: string) => shown?.conn === c && shown?.session === id;
    const add = (id: string, key: string, fallback: boolean, tabs: number) => {
      const s = c.sessions.get(id);
      if (s && tabs > 0 && isOpen(key, fallback)) out.push(s);
    };
    for (const p of recent) add(p.id, `${v.model.host}:${p.id}`, seen(p.id), p.tabs.length);
    if (isOpen(`older:${v.model.host}`, false)) for (const p of older) add(p.id, `${v.model.host}:${p.id}`, seen(p.id), p.tabs.length);
    if (v.model.loose) add(v.model.loose.id, `loose:${v.model.host}`, true, v.model.loose.tabs.length);
  }
  return out;
}

/** The groups that have the face now: the shown project, open with tabs, or No Project. */
function shownFaces(vs: HostView[], space: string): Set<string> {
  const out = new Set<string>();
  const app = sidebarApp();
  const shown = app.shown();
  for (const v of vs) {
    const c = v.online ? app.conn(v.model.host) : undefined;
    if (!c || shown?.conn !== c) continue;
    const mine = v.model.projects.map((p) => ({ ...p, space: ops.store.mapSpace(v.model.host, p.space) }));
    const { recent, older } = groupProjects(mine, space, Date.now());
    const olderOpen = isOpen(`older:${v.model.host}`, false);
    for (const p of [...recent, ...(olderOpen ? older : [])]) {
      const key = `${v.model.host}:${p.id}`;
      if (p.id === shown.session && groupLook(isOpen(key, true), true, p.tabs.length).face) out.add(key);
    }
    const l = v.model.loose;
    if (l && l.id === shown.session && groupLook(isOpen(`loose:${v.model.host}`, true), true, l.tabs.length).face) out.add(`loose:${v.model.host}`);
  }
  return out;
}

/** The groups that had the face in the last draw and in this one, so that the face fades in and out. */
let faces = new Set<string>();
let nextFaces = new Set<string>();

/** Whether the groups hold still: Animation Off or a system that asks for less motion, the panes' rule. */
function reduced(): boolean {
  return motionMs() === 0;
}

/** Whether a project's (or a group's) tabs show: the user's choice, else the project shown, else shut. */
function isOpen(key: string, fallback: boolean): boolean {
  return expanded.get(key) ?? fallback;
}

function toggle(key: string, now: boolean) {
  expanded.set(key, !now);
  folds.set(key, now ? "close" : "open");
  try {
    localStorage.setItem(expandedKey, JSON.stringify(Object.fromEntries(expanded)));
  } catch {
    // Not kept.
  }
  redraw();
}

function iconButton(glyph: string, label: string, run: () => void, disabled = false): HTMLButtonElement {
  const b = el("button", "icon");
  b.type = "button";
  setTooltip(b, label, "project-sidebar");
  b.setAttribute("aria-label", label);
  b.append(icon(glyph));
  b.disabled = disabled;
  b.onclick = (e) => {
    e.stopPropagation();
    run();
  };
  return b;
}

/** Whether the page has a host with projects of version 2, live or as last seen: the projects' sidebar is shown. */
export function hasProjects(): boolean {
  return ops.views().length > 0;
}

/** The space picker's menu: the spaces, New Space…, and Rename, Delete (not Default's) and Color for the one shown. */
export function spaceMenu(): MenuItem[] {
  const vs = ops.views();
  const names = ops.spaces(vs);
  const now = ops.space(vs);
  const s = ops.store;
  const items: MenuItem[] = names.map((n) => ({ label: ops.label(n), checked: n === now, color: s.colors[n], run: () => pickSpace(n) }));
  items.push(
    "separator",
    { label: t("spaceNewMenu"), run: () => void ops.newSpace() },
    { label: t("spaceRenameMenu"), disabled: !now, run: () => void ops.renameSpace(now) },
    ...(now ? [{ label: t("spaceDeleteMenu"), run: () => void ops.deleteSpace(now) }] : []),
    {
      label: t("spaceColorMenu"),
      submenu: [
        ...spaceColors.map((c) => ({ label: "", color: c, checked: s.colors[now] === c, run: () => setColor(now, c) })),
        "separator" as const,
        { label: t("spaceColorNone"), disabled: !s.colors[now], run: () => setColor(now, undefined) },
      ],
    },
  );
  return items;
}

function pickSpace(n: string) {
  ops.store.setCurrent(n);
  redraw();
}

function setColor(n: string, c: string | undefined) {
  ops.store.setColor(n, c);
  redraw();
}

/** The space picker: the shown space's color and name; it opens the menu. */
function picker(): HTMLButtonElement {
  const now = ops.space();
  const b = el("button", "space-chip space-picker");
  b.type = "button";
  setTooltip(b, t("spacePickerLabel"), "project-sidebar");
  b.setAttribute("aria-label", t("spacePickerLabel"));
  b.setAttribute("aria-haspopup", "menu");
  const color = ops.store.colors[now];
  if (color) {
    const dot = el("i", "color-dot");
    dot.style.background = color;
    b.append(dot);
  }
  b.append(el("span", undefined, ops.label(now)), icon("chevron-down"));
  b.onclick = () => {
    const r = b.getBoundingClientRect();
    openMenu(r.left, r.bottom + 4, spaceMenu(), () => sidebarApp().shown()?.focusKeys());
  };
  return b;
}

/**
 * The projects' sidebar: the picker, then each host the page knows, with
 * its projects of the space. Returns false when no host has projects, and
 * nothing is drawn.
 */
export function projectTree(nav: HTMLElement): boolean {
  const app = sidebarApp();
  const vs = ops.views();
  const v2 = app.hosts().some((h) => app.conn(h.id)?.v2);
  if (!vs.length && !v2) return false;
  nav.classList.add("ptree");
  // One column of tab tiles for the whole list, so that every name and every title start at one place.
  column = tilesWidth(0);
  for (const s of listedSessions(vs, ops.space(vs))) if (s.view) column = Math.max(column, tilesRoom(s.view));
  setTileColumn(column);
  nav.append(picker());
  // The first draw has no earlier face to fade from.
  faces = first ? shownFaces(vs, ops.space(vs)) : nextFaces;
  first = false;
  nextFaces = new Set();
  const now = ops.space(vs);
  const byHost = new Map(vs.map((v) => [v.model.host, v]));
  for (const h of app.hosts()) {
    const c = app.conn(h.id);
    const v = byHost.get(h.id);
    if (v) {
      hostSection(nav, h, c, v, now);
    } else if (c && !c.v2) {
      // A host with no projects of version 2: its sessions as before.
      legacySection(nav, h, c);
    } else if (!c) {
      const head = hostHead(nav, h, undefined) as HTMLButtonElement;
      head.type = "button";
      setTooltip(head, t("sidebarConnectHost", { host: h.name }), "project-sidebar");
      head.onclick = () => app.connect(h);
    } else {
      hostHead(nav, h, c);
      problemLine(nav, c);
    }
  }
  folds.clear();
  return true;
}

/**
 * A host with no projects of version 2, as one section header like the
 * others: its name and state dot, and its sessions as rows under it, each
 * opening to its tabs.
 */
function legacySection(nav: HTMLElement, h: Host, c: HostConn) {
  const app = sidebarApp();
  const head = hostHead(nav, h, c);
  head.classList.add("flat");
  if (!head.querySelector(".state-dot")) head.append(el("i", c.state === "connected" ? "host-dot on" : "host-dot off"));
  problemLine(nav, c);
  for (const s of c.sessions.values()) {
    if (!s.view) continue;
    const dim = c.state !== "connected";
    const key = `${c.host.id}:${s.id}`;
    const open = isOpen(key, true);
    const row = el("div", "project");
    if (dim) row.classList.add("dimmed");
    if (renaming(c, s.id)) {
      row.append(renameField(s.label, spaceName(s), t("renameSpaceLabel"), (name) => app.renamedSpace(c, s.id, name)));
      nav.append(row);
      continue;
    }
    const b = el("button", "project-main");
    b.type = "button";
    b.setAttribute("aria-expanded", String(open));
    setTooltip(b, spaceName(s), "project-sidebar");
    b.append(el("i", "color-dot none"), el("span", "name", spaceName(s)));
    b.onclick = () => toggle(key, open);
    row.append(b, iconButton("plus", t("cmdNewWindow"), () => app.newWindow(c, s.id), !c.live));
    onMenu(row, (x, y) => openMenu(x, y, app.spaceMenu(c, s.id), () => app.shown()?.focusKeys()));
    nav.append(row);
    if (open) nav.append(tabs(dim ? undefined : c, s.id, []));
  }
}

/** A host's heading, its `+`, and its projects of the space. A host with no project of the space shows only while it is connected. */
function hostSection(nav: HTMLElement, h: Host, c: HostConn | undefined, v: HostView, space: string) {
  const model = v.model;
  const mine = model.projects.map((p) => ({ ...p, space: ops.store.mapSpace(model.host, p.space) }));
  const { recent, older } = groupProjects(mine, space, Date.now());
  if (!v.online && !recent.length && !older.length) return;
  const head = hostHead(nav, h, c);
  head.classList.add("flat");
  // The host's state, drawn: filled while it is connected, a ring while it is away. A reconnect or a failure has its own dot.
  if (!head.querySelector(".state-dot")) head.append(el("i", v.online ? "host-dot on" : "host-dot off"));
  if (!v.online) head.append(el("small", "host-offline", t("hostOffline")));
  if (!v.online) head.classList.add("dimmed");
  const conn = v.online ? c : undefined;
  if (conn) head.append(iconButton("plus", t("projectNewOn", { host: h.name }), () => void ops.newProjectOn(conn)));
  if (c) problemLine(nav, c);
  const shown = sidebarApp().shown();
  for (const p of recent) projectRow(nav, model, conn, p, shown?.conn === c && shown?.session === p.id);
  if (older.length) {
    const key = `older:${model.host}`;
    const open = isOpen(key, false);
    const look = groupLook(open, false, older.length);
    const row = groupHead({ key, look, name: t("projectsOlder"), count: older.length, room: column, dim: !v.online });
    row.classList.add("group");
    nav.append(projectGroup(key, look, row, undefined));
    if (open) for (const p of older) projectRow(nav, model, conn, p, shown?.conn === c && shown?.session === p.id);
  }
  if (model.loose?.tabs.length) loose(nav, model, conn, model.loose, shown?.conn === c && shown?.session === model.loose.id);
  if (v.online && !recent.length && !older.length && !model.loose?.tabs.length) nav.append(el("div", "host-note", t("projectsEmpty")));
}

/** What a group's head shows: its mark, name, chevron, and its count when it has one (Older). */
interface Head {
  key: string;
  look: GroupLook;
  name: string;
  title?: string;
  count?: number;
  /** The folder's color; muted with none. */
  tint?: string;
  /** The width of the column of the tab tiles, so that the name starts where the tabs' titles do. */
  room: number;
  dim: boolean;
}

/**
 * A group's head row, built the same for a project, Older and No Project: a
 * neutral tile of a tab's size with a folder in it, the name on one line, the
 * chevron at the end (none with no tabs), and the count of an Older group.
 */
function groupHead(o: Head): HTMLElement {
  const row = el("div", "project");
  if (o.dim) row.classList.add("dimmed");
  if (o.look.pill) row.classList.add("open");
  if (o.look.face) row.classList.add("current");
  const b = el("button", "project-main");
  b.type = "button";
  if (o.title) setTooltip(b, o.title, "project-sidebar");
  const slot = el("span", "folder-slot");
  slot.style.width = `${o.room}px`;
  const tile = el("i", "folder-tile");
  const folder = icon("folder");
  if (o.tint) folder.style.color = o.tint;
  tile.append(folder);
  slot.append(tile);
  b.append(slot, el("span", "name", o.name));
  const open = o.look.chevron === "open";
  if (o.look.chevron !== "none") {
    b.setAttribute("aria-expanded", String(open));
    const chev = el("i", "chev");
    const glyph = icon("chevron-down");
    chev.append(glyph);
    b.append(chev);
    const fold = folds.get(o.key);
    if (fold && !reduced()) glyph.animate([{ transform: `rotate(${fold === "open" ? -90 : 0}deg)` }, { transform: `rotate(${fold === "open" ? 0 : -90}deg)` }], { duration: motionMs(), easing: motionEase() });
  }
  if (o.count) b.append(el("span", "count", String(o.count)));
  if (o.look.chevron !== "none") b.onclick = () => toggle(o.key, open);
  row.append(b);
  return row;
}

/**
 * A group: its head, and its tabs under it in a box that folds open and shut.
 * Every group keeps the face's room; only the shown one, while open, has the
 * face's color, which fades in and out. `tabs` builds the box, or is none.
 */
function projectGroup(key: string, look: GroupLook, head: HTMLElement, tabs: (() => HTMLElement) | undefined): HTMLElement {
  const g = el("div", "pgroup");
  const face = el("i", "face-bg");
  g.append(face, head);
  if (look.face) {
    g.classList.add("face");
    nextFaces.add(key);
  }
  const animate = !reduced();
  if (animate && look.face !== faces.has(key)) face.animate({ opacity: look.face ? [0, 1] : [1, 0] }, { duration: motionMs(), easing: motionEase() });
  const fold = folds.get(key);
  if (tabs && (look.chevron === "open" || (fold === "close" && look.chevron !== "none"))) {
    const box = tabs();
    g.append(box);
    if (fold && animate) {
      const opening = fold === "open";
      queueMicrotask(() => {
        const h = box.offsetHeight;
        box.style.overflow = "hidden";
        const from = { height: "0px", marginTop: "0px", opacity: 0 };
        const to = { height: `${h}px`, marginTop: "2px", opacity: 1 };
        const a = box.animate(opening ? [from, to] : [to, from], { duration: motionMs(), easing: motionEase() });
        a.onfinish = () => {
          box.style.overflow = "";
          if (!opening) box.remove();
        };
      });
    } else if (fold === "close") {
      box.remove();
    }
  }
  return g;
}

/** A host's one session of tabs with no project, as a group named for it: in every space, open at first. */
function loose(nav: HTMLElement, model: HostModel, c: HostConn | undefined, l: NonNullable<HostModel["loose"]>, shownHere: boolean) {
  const key = `loose:${model.host}`;
  const open = isOpen(key, true);
  const look = groupLook(open, shownHere, l.tabs.length);
  const row = groupHead({ key, look, name: t("projectNoProjectName"), room: column, dim: !c });
  row.dataset.host = model.host;
  row.dataset.session = l.id;
  if (c) {
    row.append(iconButton("plus", t("projectNewTabNoProject"), () => void ops.newTab(c, l.id)));
    onMenu(row, (x, y) => openMenu(x, y, [{ label: t("projectNewTabNoProject"), run: () => void ops.newTab(c, l.id) }], () => sidebarApp().shown()?.focusKeys()));
    dropTarget(row, c, l.id);
  }
  nav.append(projectGroup(key, look, row, () => tabs(c, l.id, l.tabs)));
}

/** A tab of this host dropped on the row moves into the session, with its panes and their programs. */
function dropTarget(row: HTMLElement, c: HostConn, session: string) {
  row.addEventListener("dragover", (e) => {
    const d = ownDrag(e);
    if (!d || d.kind !== "window" || d.host !== c.host.id || d.session === session) return;
    e.preventDefault();
    e.dataTransfer!.dropEffect = "move";
    row.dataset.drop = "into";
  });
  row.addEventListener("dragleave", (e) => {
    if (!(e.relatedTarget instanceof Node && row.contains(e.relatedTarget))) delete row.dataset.drop;
  });
  row.addEventListener("drop", (e) => {
    const d = ownDrag(e);
    delete row.dataset.drop;
    if (!d || d.kind !== "window" || d.host !== c.host.id || d.session === session) return;
    e.preventDefault();
    void ops.moveTab(c, d.session, d.window, session);
  });
}

/** A project's group: its row (name, `+`, menu, drop target), and its tabs while it is open. */
function projectRow(nav: HTMLElement, model: HostModel, c: HostConn | undefined, p: ProjectRow, shownHere: boolean) {
  const app = sidebarApp();
  const key = `${model.host}:${p.id}`;
  const open = isOpen(key, shownHere);
  const look = groupLook(open, shownHere, p.tabs.length);
  const name = p.name || dirName(p.dir);
  if (c && renaming(c, p.id)) {
    const field = el("div", "project");
    field.append(renameField(p.name, dirName(p.dir), t("renameSpaceLabel"), (to) => app.renamedSpace(c, p.id, to)));
    nav.append(projectGroup(key, { ...look, chevron: "none" }, field, undefined));
    return;
  }
  // The folder is in the tooltip only.
  const row = groupHead({ key, look, name, title: `${name}\n${p.dir}`, tint: projectColor(p.color), room: column, dim: !c });
  row.dataset.host = model.host;
  row.dataset.session = p.id;
  if (c) {
    row.append(iconButton("plus", t("cmdNewWindow"), () => void ops.newTab(c, p.id)));
    onMenu(row, (x, y) => openMenu(x, y, projectMenu(c, p), () => app.shown()?.focusKeys()));
    dropTarget(row, c, p.id);
  }
  nav.append(projectGroup(key, look, row, () => tabs(c, p.id, p.tabs)));
}

/** A project's context menu: New Tab, Rename…, Move to Space, Close Project…. */
function projectMenu(c: HostConn, p: ProjectRow): MenuItem[] {
  const app = sidebarApp();
  const here = ops.store.mapSpace(c.host.id, p.space);
  return [
    { label: t("cmdNewWindow"), run: () => void ops.newTab(c, p.id) },
    "separator",
    { label: t("cmdRenameSpace"), run: () => app.startRename(c, p.id) },
    {
      label: t("projectMoveSpace"),
      submenu: ops.spaces().map((n) => ({ label: ops.label(n), checked: n === here, run: () => void ops.moveProjectToSpace(c, p.id, n) })),
    },
    ...(c.agents.length > 0 ? [{ label: t("projectQuickLaunchers"), submenu: launcherPinItems(c, p.id) }] : []),
    "separator",
    { label: t("cmdCloseSpace"), run: () => app.closeSpace(c, p.id) },
  ];
}

/** The tabs under a project: live rows with their menus and drags, or the copy's names. */
function tabs(c: HostConn | undefined, session: string, rows: TabRow[]): HTMLElement {
  const s = c?.sessions.get(session);
  const box = el("div", "project-tabs");
  if (c && s?.view) {
    for (const w of s.view.windows ?? []) box.append(windowRow(c, session, w, column));
    box.append(newTabRow(c, session));
  } else {
    for (const r of rows) {
      const row = el("div", "window dimmed copy");
      row.style.paddingLeft = `${12 + column + 8}px`;
      row.append(el("span", "title", r.title));
      box.append(row);
    }
  }
  return box;
}

/**
 * The pin toggles of a project's agent launchers (docs/MOD-010): a check
 * per installed agent, and Show All Installed while a pick is kept. The
 * project menu's Quick Launchers submenu and the New Tab row's menu share
 * the list.
 */
function launcherPinItems(c: HostConn, session: string): MenuItem[] {
  const pinned = c.sessions.get(session)?.project?.launchers;
  return [
    ...c.agents.map((a) => {
      const on = pinned === undefined || pinned.includes(a.id);
      return {
        label: a.name,
        checked: on,
        run: () => {
          const all = c.agents.map((x) => x.id);
          const cur = pinned ?? all;
          const next = on ? cur.filter((x) => x !== a.id) : all.filter((x) => x === a.id || cur.includes(x));
          const same = next.length === all.length && next.every((x, i) => x === all[i]);
          void ops.setProjectLaunchers(c, session, same ? null : next);
        },
      };
    }),
    ...(pinned !== undefined
      ? (["separator", { label: t("projectLaunchersShowAll"), run: () => void ops.setProjectLaunchers(c, session, null) }] as MenuItem[])
      : []),
  ];
}

/**
 * The menu of a New Tab row's chevron (docs/MOD-010): every installed
 * agent, its brand tile, its name and a check while the project pins it —
 * a click opens a tab that runs it — then the project's pin toggles, the
 * project menu's own.
 */
function agentMenu(c: HostConn, session: string): MenuItem[] {
  const pinned = c.sessions.get(session)?.project?.launchers;
  return [
    ...c.agents.map((a) => ({
      label: a.name,
      tile: smallTile(programOf(a.id)),
      checked: pinned === undefined || pinned.includes(a.id),
      disabled: !c.live,
      run: () => void ops.newTab(c, session, a.command),
    })),
    "separator" as const,
    { label: t("projectQuickLaunchers"), submenu: launcherPinItems(c, session) },
  ];
}

/** A pinned agent's button on the New Tab row: its brand glyph in the text's colour, as pane icons are — the tiles' colours stay the tabs'. */
function agentButton(c: HostConn, session: string, a: Agent): HTMLButtonElement {
  const b = el("button", "icon");
  b.type = "button";
  setTooltip(b, a.name, "project-sidebar");
  b.setAttribute("aria-label", `${t("cmdNewWindow")} (${a.name})`);
  const g = el("i", "pane-icon");
  g.style.setProperty("--icon", `url(${glyphUrl(programOf(a.id).glyph)})`);
  b.append(g);
  b.disabled = !c.live;
  b.onclick = (e) => {
    e.stopPropagation();
    void ops.newTab(c, session, a.command);
  };
  return b;
}

/**
 * The New Tab row under a session's tabs (docs/MOD-010): the `+` and the
 * words open a plain shell tab. On a host with agent CLIs a cluster at the
 * row's end holds the first three pinned agents' glyphs and the chevron of
 * the menu of every installed one; with none pinned the cluster is the
 * chevron alone.
 */
function newTabRow(c: HostConn, session: string): HTMLElement {
  const s = c.sessions.get(session);
  const b = el("button", "new-tab");
  b.type = "button";
  b.append(icon("plus"), el("span", undefined, t("cmdNewWindow")));
  b.disabled = !c.live;
  b.onclick = () => void ops.newTab(c, session);
  if (!s?.project?.dir || c.agents.length === 0) return b;
  const pinned = s.project.launchers;
  const agents = pinned === undefined ? c.agents : c.agents.filter((a) => pinned.includes(a.id));
  const row = el("div", "new-tab-row");
  row.append(b);
  const cluster = el("span", "agent-cluster");
  for (const a of agents.slice(0, 3)) cluster.append(agentButton(c, session, a));
  const more = iconButton("chevron-down", t("projectQuickLaunchers"), () => {
    const r = more.getBoundingClientRect();
    openMenu(r.left, r.bottom + 4, agentMenu(c, session), () => sidebarApp().shown()?.focusKeys());
  }, !c.live);
  more.setAttribute("aria-haspopup", "menu");
  cluster.append(more);
  row.append(cluster);
  return row;
}

/** Shows a project: its active tab. */
function openProject(c: HostConn, session: string) {
  const v = c.sessions.get(session)?.view;
  const next = v?.active_window_id ?? v?.windows?.[0]?.window_id;
  if (next) sidebarApp().show(c, session, next, true);
}

/** The project's menu in the bar above: the space's hosts and their projects, with their tabs' counts, and New Project…. */
function projectsMenu(current: string): MenuItem[] {
  const app = sidebarApp();
  const space = ops.space();
  const items: MenuItem[] = [];
  for (const v of ops.views()) {
    if (!v.online) continue;
    const c = app.conn(v.model.host);
    if (!c) continue;
    const mine = v.model.projects.map((p) => ({ ...p, space: ops.store.mapSpace(v.model.host, p.space) }));
    const { recent, older } = groupProjects(mine, space, Date.now());
    items.push({ label: v.model.name, disabled: true });
    for (const p of [...recent, ...older]) {
      items.push({ label: p.name || dirName(p.dir), detail: p.dir, title: `${p.name}\n${p.dir}`, checked: p.id === current, run: () => openProject(c, p.id) });
    }
    if (v.model.loose) items.push({ label: t("projectNoProjectName"), checked: v.model.loose.id === current, run: () => openProject(c, v.model.loose!.id) });
  }
  items.push("separator", { label: t("cmdNewSpace"), run: () => void ops.newProject(app.selected()) });
  return items;
}

/**
 * The bar above the panes, with the tabs on top: the space picker, `host ›
 * project`, the project's tabs and New Tab. False when the shown host has no
 * projects of version 2.
 */
export function projectBar(nav: HTMLElement, c: HostConn | undefined): boolean {
  if (!c || !ops.isLive(c)) return false;
  const app = sidebarApp();
  const shown = app.shown();
  const model = ops.views().find((v) => v.model.host === c.host.id)?.model;
  if (!model) return false;
  const first = model.projects.find((p) => ops.store.mapSpace(model.host, p.space) === ops.space())?.id ?? model.loose?.id;
  const id = shown?.conn === c && c.sessions.get(shown.session)?.project ? shown.session : first;
  const s = id ? c.sessions.get(id) : undefined;
  nav.append(picker());
  if (!s?.view) return true;
  const p = model.projects.find((x) => x.id === id);
  const chip = el("button", "space-chip project-chip");
  chip.type = "button";
  setTooltip(chip, p ? `${t("spaceMenuLabel")}: ${p.dir}` : t("spaceMenuLabel"), "project-sidebar");
  chip.setAttribute("aria-haspopup", "menu");
  chip.append(el("span", "host", c.host.name), icon("chevron-right"), el("span", undefined, p ? p.name || dirName(p.dir) : t("projectNoProjectName")), icon("chevron-down"));
  chip.onclick = () => {
    const r = chip.getBoundingClientRect();
    openMenu(r.left, r.bottom + 4, projectsMenu(s.id), () => app.shown()?.focusKeys());
  };
  nav.append(chip);
  const room = tilesRoom(s.view);
  for (const w of s.view.windows ?? []) nav.append(windowRow(c, s.id, w, room));
  const add = iconButton("plus", t("cmdNewWindow"), () => void ops.newTab(c, s.id), !c.live);
  nav.append(add);
  return true;
}
