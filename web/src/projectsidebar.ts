// The sidebar and the bar of projects (docs/projects.md, Web client): the
// space picker, each host with its projects of the space in order of last
// use, Older, and Tabs with no project; a project expands to its tabs and a
// New Tab. A host that is away shows its copy, greyed and read-only. Hosts
// with no projects of version 2 keep the rows of sidebar.ts. The rules are
// projectmodel.ts's; the calls are projectops.ts's.
import type { HostConn } from "./hostconn";
import type { Host } from "./hosts";
import { icon } from "./hosts";
import { t } from "./i18n";
import { MenuItem, onMenu, openMenu, ownDrag, renameField } from "./manage";
import { dirName, dotColor, groupProjects, HostModel, ProjectRow, TabRow } from "./projectmodel";
import type { HostView, ProjectOps } from "./projectops";
import { hostHead, problemLine, spaceName, redraw, renaming, sidebarApp, tilesRoom, windowRow } from "./sidebar";

/** The colors a space can take, as a dot in the picker. */
export const spaceColors = ["#e5484d", "#f76b15", "#e5a000", "#30a46c", "#3e63dd", "#8e4ec6"];

let ops: ProjectOps;

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

/** Whether a project's (or a group's) tabs show: the user's choice, else the project shown, else shut. */
function isOpen(key: string, fallback: boolean): boolean {
  return expanded.get(key) ?? fallback;
}

function toggle(key: string, now: boolean) {
  expanded.set(key, !now);
  try {
    localStorage.setItem(expandedKey, JSON.stringify(Object.fromEntries(expanded)));
  } catch {
    // Not kept.
  }
  redraw();
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function iconButton(glyph: string, label: string, run: () => void, disabled = false): HTMLButtonElement {
  const b = el("button", "icon");
  b.type = "button";
  b.title = label;
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
  b.title = t("spacePickerLabel");
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
  nav.append(picker());
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
      head.title = t("sidebarConnectHost", { host: h.name });
      head.onclick = () => app.connect(h);
    } else {
      hostHead(nav, h, c);
      problemLine(nav, c);
    }
  }
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
    b.title = spaceName(s);
    b.append(el("i", "color-dot none"), el("span", "name", spaceName(s)), el("span", "count", String((s.view.windows ?? []).length)));
    b.onclick = () => toggle(key, open);
    row.append(b, iconButton("plus", t("cmdNewWindow"), () => app.newWindow(c, s.id), !c.live));
    onMenu(row, (x, y) => openMenu(x, y, app.spaceMenu(c, s.id), () => app.shown()?.focusKeys()));
    nav.append(row);
    if (open) tabs(nav, dim ? undefined : c, s.id, []);
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
    nav.append(groupRow(key, open, t("projectsOlder"), older.length, undefined, !v.online));
    if (open) for (const p of older) projectRow(nav, model, conn, p, shown?.conn === c && shown?.session === p.id);
  }
  if (model.loose?.tabs.length) loose(nav, model, conn, model.loose, shown?.conn === c && shown?.session === model.loose.id);
  if (v.online && !recent.length && !older.length && !model.loose?.tabs.length) nav.append(el("div", "host-note", t("projectsEmpty")));
}

/** A row that opens or shuts a group, at the project's level: its words and count, and what it adds. Its place of the dot is empty. */
function groupRow(key: string, open: boolean, text: string, count: number, add: HTMLElement | undefined, dim: boolean): HTMLElement {
  const row = el("div", "project group");
  if (dim) row.classList.add("dimmed");
  const b = el("button", "project-main");
  b.type = "button";
  b.setAttribute("aria-expanded", String(open));
  b.append(el("i", "color-dot none"), el("span", "name", text), el("span", "count", String(count)));
  b.onclick = () => toggle(key, open);
  row.append(b);
  if (add) row.append(add);
  return row;
}

/** The tabs of a host's one session of tabs with no project: in every space, shut at first. */
function loose(nav: HTMLElement, model: HostModel, c: HostConn | undefined, l: NonNullable<HostModel["loose"]>, shownHere: boolean) {
  const key = `loose:${model.host}`;
  const open = isOpen(key, shownHere);
  const add = c ? iconButton("plus", t("projectNewTabNoProject"), () => void ops.newTab(c, l.id)) : undefined;
  const row = groupRow(key, open, t("projectNoProjectName"), l.tabs.length, add, !c);
  if (c) onMenu(row, (x, y) => openMenu(x, y, [{ label: t("projectNewTabNoProject"), run: () => void ops.newTab(c, l.id) }], () => sidebarApp().shown()?.focusKeys()));
  nav.append(row);
  if (open) tabs(nav, c, l.id, l.tabs);
}

/** A project's row: its name and folder, its `+`, its menu; and its tabs while it is open. */
function projectRow(nav: HTMLElement, model: HostModel, c: HostConn | undefined, p: ProjectRow, shownHere: boolean) {
  const app = sidebarApp();
  const key = `${model.host}:${p.id}`;
  const open = isOpen(key, shownHere);
  const row = el("div", "project");
  row.dataset.host = model.host;
  row.dataset.session = p.id;
  if (!c) row.classList.add("dimmed");
  // One fill for the chosen place: the active tab when it shows, else the project row. The project that holds a shown tab is only marked current.
  if (shownHere) row.classList.add(open ? "current" : "open");
  if (c && renaming(c, p.id)) {
    row.append(renameField(p.name, dirName(p.dir), t("renameSpaceLabel"), (name) => app.renamedSpace(c, p.id, name)));
    nav.append(row);
    return;
  }
  const b = el("button", "project-main");
  b.type = "button";
  b.setAttribute("aria-expanded", String(open));
  // The folder is in the tooltip only.
  b.title = `${p.name || dirName(p.dir)}\n${p.dir}`;
  const dot = el("i", "color-dot");
  dot.style.background = dotColor(p.dir, p.color);
  b.append(dot, el("span", "name", p.name || dirName(p.dir)));
  if (p.tabs.length) b.append(el("span", "count", String(p.tabs.length)));
  b.onclick = () => toggle(key, open);
  row.append(b);
  if (c) {
    row.append(iconButton("plus", t("cmdNewWindow"), () => void ops.newTab(c, p.id)));
    onMenu(row, (x, y) => openMenu(x, y, projectMenu(c, p), () => app.shown()?.focusKeys()));
    // A tab of this host dropped here moves into the project, with its panes and their programs.
    row.addEventListener("dragover", (e) => {
      const d = ownDrag(e);
      if (!d || d.kind !== "window" || d.host !== c.host.id || d.session === p.id) return;
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
      if (!d || d.kind !== "window" || d.host !== c.host.id || d.session === p.id) return;
      e.preventDefault();
      void ops.moveTab(c, d.session, d.window, p.id);
    });
  }
  nav.append(row);
  if (open) tabs(nav, c, p.id, p.tabs);
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
    "separator",
    { label: t("cmdCloseSpace"), run: () => app.closeSpace(c, p.id) },
  ];
}

/** The tabs under a project: live rows with their menus and drags, or the copy's names. */
function tabs(nav: HTMLElement, c: HostConn | undefined, session: string, rows: TabRow[]) {
  const s = c?.sessions.get(session);
  const box = el("div", "project-tabs");
  if (c && s?.view) {
    const room = tilesRoom(s.view);
    for (const w of s.view.windows ?? []) box.append(windowRow(c, session, w, room));
    box.append(newTabRow(c, session));
  } else {
    for (const r of rows) {
      const row = el("div", "window dimmed copy");
      row.append(el("span", "title", r.title));
      box.append(row);
    }
  }
  nav.append(box);
}

function newTabRow(c: HostConn, session: string): HTMLElement {
  const b = el("button", "new-tab");
  b.type = "button";
  b.append(icon("plus"), el("span", undefined, t("cmdNewWindow")));
  b.disabled = !c.live;
  b.onclick = () => void ops.newTab(c, session);
  return b;
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
  chip.title = p ? `${t("spaceMenuLabel")}: ${p.dir}` : t("spaceMenuLabel");
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
