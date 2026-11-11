// GoRex's web client: the sessions a host's gateway serves, and a window's
// panes, live, over the same API the iOS app speaks. The page is hosted on
// its own; the host is one the user added (hosts.ts).
import { Ghostty } from "./ghostty";
import { commandBox, diagnose, Host, HostList, hostURL, icon, mixedContent, Problem } from "./hosts";
import { shortcut } from "./keys";
import { describe } from "./pane";
import { fontFamily, prefs } from "./prefs";
import { mostUrgent, programName, programOf, rank, seenKey, Status, statusOf, tiles, tilesWidth } from "./programs";
import { Control, setHost } from "./rex";
import { LinkState, Settings } from "./settings";
import { desktopTheme, Theme } from "./terminal";
import { chosenName, isLight, loadThemes, ThemeEntry, themeColours } from "./themes";
import { borderLeaf, layoutOf, leavesOf, WindowHost, WindowView } from "./window";

const $ = (id: string) => document.getElementById(id)!;
const stage = $("stage");
const shell = { flavor: "com.superlogical.terminal.shell" };

let g: Ghostty;
let control: Control | undefined;
let token: string | undefined;
/** The host connected to, or connecting; one at a time. */
let current: Host | undefined;
/** Counts the hosts chosen, so that a connection to one chosen before ends. */
let generation = 0;
let retry: ReturnType<typeof setTimeout> | undefined;
let hostList: HostList;
let settings: Settings | undefined;
/** The chosen host's connection, and what is wrong with it, for the Settings dialog's host card. */
let link: LinkState = "none";
let problem: Problem | undefined;
/** The theme chosen in this browser, over the desktop's colours; none keeps the host's. */
let chosen: Theme | undefined;

/** A session the gateway serves, and its last view. */
interface Session {
  id: string;
  label: string;
  view?: any;
  reading?: boolean;
  again?: boolean;
}

/** The sessions, in the order the host lists them. */
const sessions = new Map<string, Session>();
/** The terminals' titles, by block, kept up by title_changed. */
const titles = new Map<string, string>();
/** The foreground programs' names, by block, kept up by process_changed. */
const programs = new Map<string, string | undefined>();
/** What the programs in each block reported (OSC 7501), by block. */
const statuses = new Map<string, Status[]>();
/** The done and error records the user saw, by block, as their keys. */
const seen = new Map<string, Set<string>>();
/** The blocks whose program and records were read; read again after a reconnect. */
const described = new Set<string>();
/** The window this page shows. */
let shown: WindowView | undefined;

/** The terminal background the page's chrome follows. */
let lastBg: [number, number, number] = desktopTheme().background!;

/** The page's chrome: the Window setting, else the terminal's background (light for a light one). */
function scheme(bg: [number, number, number]) {
  lastBg = bg;
  document.documentElement.dataset.scheme = prefs.appearance || (isLight(bg) ? "light" : "dark");
}

/** Sets the connection's state, and shows it in the Settings dialog. */
function setLink(s: LinkState, p?: Problem) {
  link = s;
  problem = p;
  settings?.update();
}

/** Applies a changed setting to the page and to every open pane, as the desktop's applyPrefs. */
function applyPrefs(what: "appearance" | "headers" | "font" | "cursor" | "contrast", reset = false) {
  const panes = [...(shown?.panes.values() ?? [])];
  switch (what) {
    case "appearance":
      scheme(lastBg);
      break;
    case "headers":
      document.body.classList.toggle("no-pane-headers", prefs.hidePaneHeaders);
      break;
    case "font":
      // The previews and command boxes take the family too.
      document.documentElement.style.setProperty("--mono", fontFamily());
      panes.forEach((p) => p.restyle());
      break;
    case "contrast":
      panes.forEach((p) => p.redraw());
      break;
    case "cursor":
      panes.forEach((p) => p.applyCursor(reset));
      break;
  }
}

function status(s: string) {
  $("status").textContent = s;
}

/** What a window and its panes need of the page. */
const host: WindowHost = {
  control: () => control,
  get g() {
    return g;
  },
  chosen: () => chosen,
  status,
  scheme,
  titleOf: (b) => titles.get(b) || "Terminal",
  programOf: (b) => programOf(programs.get(b)),
  action: (p, name) => {
    shown?.setFocus(p.block, true);
    run(name);
  },
  focusBlock: (w, block) => {
    control?.call("session.focus_block", { session_id: w.session, block_id: block }).catch((e) => console.warn("focus", e));
  },
  resizeSplit: (w, s, ratio) => {
    const before = borderLeaf(s.before, s.direction, true), after = borderLeaf(s.after, s.direction, false);
    control
      ?.call("session.resize_pane", { session_id: w.session, direction: s.direction, before_block_id: before, after_block_id: after, ratio })
      .catch((e) => {
        status(`Could not move the divider: ${describe(e)}`);
        refresh(w.session, true);
      });
  },
};

async function main() {
  status("Loading libghostty-vt…");
  try {
    // The font too: the cells are measured from it.
    const faces = ["400", "700", "italic 400", "italic 700"].map((v) => document.fonts.load(`${v} 12px "JetBrains Mono"`));
    // The themes load with it, so that the first frame has the chosen colours.
    let themes: ThemeEntry[];
    [g, themes] = await Promise.all([Ghostty.load("ghostty-vt.wasm"), loadThemes(), Promise.allSettled(faces)]).then((r) => [r[0], r[1]]);
    const pick = (t?: ThemeEntry) => {
      chosen = t && themeColours(t);
      scheme(chosen?.background ?? desktopTheme().background!);
      for (const p of shown?.panes.values() ?? []) p.setChosen(chosen);
    };
    pick(themes.find((t) => t.name === chosenName()));
    for (const what of ["headers", "font"] as const) applyPrefs(what);
    hostList = new HostList($("hosts"), { choose, close: () => shown?.focusKeys(), changed: () => settings?.update() });
    settings = new Settings({
      hosts: hostList,
      link: () => ({ host: current, state: link, problem }),
      themes,
      chooseTheme: pick,
      apply: applyPrefs,
      closed: () => shown?.focusKeys(),
    });
    const open = $("open-settings");
    open.prepend(icon("settings-2"));
    open.onclick = () => settings!.toggle();
  } catch (e) {
    status(`Could not load libghostty-vt: ${describe(e)}`);
    return;
  }
  // A quick link: ?host=URL adds the host, or chooses it, and leaves the address bar.
  const linked = new URLSearchParams(location.search).get("host");
  if (linked) {
    const r = hostURL(linked);
    if ("url" in r) hostList.addLink(r.url);
    history.replaceState(null, "", location.pathname);
  }
  choose(hostList.chosen);
}

/** Shows what is wrong with the host, or nothing. */
function notice(p?: Problem, action?: { label: string; run: () => void }) {
  const box = $("notice");
  box.hidden = !p;
  if (!p) return;
  const card = document.createElement("div");
  card.className = "notice-card";
  const h = document.createElement("h1");
  h.textContent = p.title;
  const t = document.createElement("p");
  t.textContent = p.text;
  card.append(h, t);
  if (p.command) card.append(commandBox(p.command));
  if (action) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "host-add primary";
    b.textContent = action.label;
    b.onclick = action.run;
    card.append(b);
  }
  box.replaceChildren(card);
}

/** Leaves the host connected to, and connects to h. Only the chosen host is connected. */
function choose(h: Host | undefined) {
  generation++;
  clearTimeout(retry);
  const c = control;
  control = undefined;
  c?.close();
  hide();
  token = undefined;
  wantWindow = undefined;
  for (const m of [sessions, titles, programs, statuses, seen]) m.clear();
  described.clear();
  current = h;
  sidebar();
  if (!h) {
    status("");
    setLink("none");
    notice(
      {
        title: "Add a GoRex host",
        text:
          "This page connects to GoRex's Rex API gateway on your machines: the tailnet address of built-in Tailscale " +
          "(https://<machine>-gorex.<tailnet>.ts.net), or the local network listener (http://192.168.1.2:7297). " +
          "Each host must trust this page's origin first. On the host, run:",
        command: `gorex rexapi web allow ${location.origin}`,
      },
      { label: "Add Host", run: () => hostList.open("new") },
    );
    return;
  }
  setHost(h.url);
  const blocked = mixedContent(h.url);
  if (blocked) {
    status(`${h.name}: blocked`);
    setLink("unreachable", blocked);
    notice(blocked, { label: "Edit Host", run: () => hostList.open(h) });
    return;
  }
  notice();
  setLink("connecting");
  connect(generation);
}

/** Opens the control connection to the chosen host, again after it drops. */
async function connect(gen: number) {
  const h = current!;
  status(`Connecting to ${h.name}…`);
  if (link !== "unreachable") setLink("connecting");
  let c: Control;
  try {
    c = await Control.open(token);
  } catch (e) {
    if (gen !== generation) return;
    const p = await diagnose(h.url);
    if (gen !== generation) return;
    notice(p, { label: "Edit Host", run: () => hostList.open(h) });
    setLink("unreachable", p);
    status(`${h.name}: ${p.title}. Trying again…`);
    retry = setTimeout(() => connect(gen), 4000);
    return;
  }
  if (gen !== generation) {
    c.close();
    return;
  }
  notice();
  setLink("connected");
  control = c;
  described.clear();
  token = c.reconnectToken;
  c.onEvent = (e) => event(e.type, e.sessionID, e.payload);
  c.onClose = (reason) => {
    if (control !== c) return;
    control = undefined;
    setLink("connecting");
    status(`${h.name}: disconnected (${reason}). Reconnecting…`);
    retry = setTimeout(() => connect(gen), 3000);
  };
  try {
    await c.call("session.bootstrap", { event_scope: "all" });
    await list();
    status(`Connected to ${h.name}`);
    for (const p of shown?.panes.values() ?? []) p.start();
  } catch (e) {
    status(`Connected, but the sessions could not be read: ${describe(e)}`);
  }
}

/**
 * Follows the host: a session made or gone, a view changed (a split, a
 * closed pane, a ratio, a window), a title. Only what changed is read again.
 */
function event(type: string, sid: string | undefined, p: any) {
  switch (type) {
    case "session_created": {
      const id = p.session?.session_id;
      if (id && !sessions.has(id)) {
        sessions.set(id, { id, label: p.session.label ?? "" });
        refresh(id, true);
      }
      break;
    }
    case "session_destroyed": {
      const id = p.session_id ?? sid;
      if (!id) break;
      sessions.delete(id);
      if (shown?.session === id) hide();
      sidebar();
      break;
    }
    case "session_label_changed": {
      const s = sid && sessions.get(sid);
      if (s) {
        s.label = p.label ?? s.label;
        sidebar();
      }
      break;
    }
    case "session_view_changed": {
      const s = sid && sessions.get(sid);
      if (s && (p.revision ?? Infinity) > (s.view?.revision ?? -1)) refresh(s.id);
      break;
    }
    case "block_event":
      if (p.name === "title_changed" && p.block_id) {
        titles.set(p.block_id, p.payload?.title ?? "");
        sidebar();
        shown?.retitle();
      } else if (p.name === "process_changed" && p.block_id) {
        programs.set(p.block_id, programName(p.payload?.foreground));
        sidebar();
        shown?.retitle();
      } else if (p.name === "program_status_changed" && p.block_id) {
        statuses.set(p.block_id, recordsOf(p.payload?.records));
        see(p.block_id);
        sidebar();
      } else if (p.name === "program_status_removed" && p.block_id) {
        const ids: string[] = p.payload?.ids ?? [];
        statuses.set(p.block_id, (statuses.get(p.block_id) ?? []).filter((r) => !ids.includes(r.id)));
        sidebar();
      }
      break;
  }
}

/** Lists the sessions and reads each one's view. */
async function list() {
  const c = control;
  if (!c) return;
  const listed: any[] = (await c.call("session.list")).payload.sessions ?? [];
  const ids = new Set(listed.map((s) => s.session_id));
  for (const id of sessions.keys()) if (!ids.has(id)) sessions.delete(id);
  for (const s of listed) {
    const had = sessions.get(s.session_id);
    if (had) had.label = s.label ?? had.label;
    else sessions.set(s.session_id, { id: s.session_id, label: s.label ?? "" });
  }
  if (shown && !sessions.has(shown.session)) hide();
  await Promise.all(listed.map((s) => refresh(s.session_id, true)));
  sidebar();
}

/**
 * Reads a session's view: attached first, as data connections and the
 * layout methods need. One read at a time; a change meanwhile reads again.
 */
async function refresh(id: string, attach = false) {
  const s = sessions.get(id);
  const c = control;
  if (!s || !c) return;
  if (s.reading) {
    s.again = true;
    return;
  }
  s.reading = true;
  try {
    do {
      s.again = false;
      const fresh = attach;
      const v = attach
        ? (await c.call("session.attach", { session_id: id })).payload.view
        : (await c.call("session.view", { session_id: id })).payload;
      attach = false;
      if (sessions.get(id) !== s) return;
      if (fresh || !s.view || v.revision > s.view.revision) apply(s, v);
    } while (s.again);
  } catch (e) {
    console.warn("session view", id, e);
  } finally {
    s.reading = false;
  }
}

/** Takes a session's new view: the sidebar, titles of new blocks, and the shown window. */
function apply(s: Session, v: any) {
  s.view = v;
  if (v.label) s.label = v.label;
  for (const w of v.windows ?? []) {
    for (const b of leavesOf(layoutOf(w))) {
      if (!described.has(b)) {
        described.add(b);
        readProgram(s.id, b);
      }
      if (titles.has(b)) continue;
      titles.set(b, "");
      readTitle(s.id, b, 3);
    }
  }
  if (shown?.session === s.id) {
    const w = (v.windows ?? []).find((w: any) => w.window_id === shown!.window);
    if (w) shown.update(w);
    else {
      // The window closed: show the session's active one, as the desktop does.
      const next = v.active_window_id ?? v.windows?.[0]?.window_id;
      hide();
      if (next) show(s.id, next, false);
    }
  }
  sidebar();
  showWanted();
}

/**
 * Reads a block's title. A new block's shell may set it only after this
 * read and before the gateway first looks, which then tells no
 * title_changed: an empty title is read again a few times.
 */
function readTitle(session: string, b: string, tries: number) {
  control?.callBlock("title", session, b).then(
    (r) => {
      if (titles.get(b)) return;
      if (r.title) {
        titles.set(b, r.title);
        sidebar();
        shown?.retitle();
      } else if (tries > 1) setTimeout(() => readTitle(session, b, tries - 1), 1000);
    },
    () => {},
  );
}

function recordsOf(j: any): Status[] {
  return (Array.isArray(j) ? j : []).map(statusOf).filter((r): r is Status => !!r);
}

/** Marks a shown block's done and error records seen, as the desktop's pane does when it shows. */
function see(block: string) {
  if (!shown?.panes.has(block) || document.visibilityState !== "visible") return;
  const keys = (statuses.get(block) ?? []).filter((r) => r.state === "done" || r.state === "error").map(seenKey);
  if (!keys.length) return;
  const had = seen.get(block) ?? new Set<string>();
  seen.set(block, new Set([...had, ...keys]));
}

/**
 * Reads a block's foreground program and records, which process_changed
 * and program_status_changed keep up to date. An answer applies when no
 * event changed what it read while it was in flight.
 */
function readProgram(session: string, block: string) {
  const c = control;
  if (!c) return;
  const hadProgram = programs.get(block), hadRecords = statuses.get(block);
  c.callBlock("process", session, block).then(
    (r) => {
      if (!programs.has(block) || programs.get(block) === hadProgram) {
        programs.set(block, programName(r.foreground));
        sidebar();
        shown?.retitle();
      }
    },
    () => described.delete(block),
  );
  c.callBlock("program_status", session, block).then(
    (r) => {
      if (!statuses.has(block) || statuses.get(block) === hadRecords) {
        statuses.set(block, recordsOf(r.records));
        see(block);
        sidebar();
      }
    },
    () => {},
  );
}

/** A window's title, as the desktop names a tab: its terminal's title, else its label. */
function windowTitle(w: any): string {
  const blocks = leavesOf(layoutOf(w));
  const focused = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
  return (focused && titles.get(focused)) || w.label || "Window";
}

/** The sidebar: each session, and a row for each of its windows. */
function sidebar() {
  for (const b of shown?.panes.keys() ?? []) see(b);
  const nav = document.createElement("nav");
  for (const s of sessions.values()) {
    const v = s.view;
    if (!v) continue;
    const h = document.createElement("h2");
    const name = document.createElement("span");
    name.textContent = s.label || v.label || "Session";
    const add = document.createElement("button");
    add.className = "icon";
    add.type = "button";
    add.append(icon("plus"));
    add.title = "New Window";
    add.onclick = () => newWindow(s.id);
    h.append(name, add);
    nav.append(h);
    // The room of the tiles of the window with the most, so that the titles line up.
    const room = tilesWidth(Math.max(0, ...(v.windows ?? []).map((w: any) => leavesOf(layoutOf(w)).length - 1)));
    for (const w of v.windows ?? []) {
      const row = document.createElement("div");
      row.className = "window";
      row.dataset.window = w.window_id;
      if (shown?.window === w.window_id) row.classList.add("open");
      const b = document.createElement("button");
      b.className = "block";
      b.type = "button";
      const title = document.createElement("span");
      title.className = "title";
      title.textContent = windowTitle(w);
      const blocks = leavesOf(layoutOf(w));
      const front = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
      const urgent = blocks
        .map((x) => mostUrgent(statuses.get(x) ?? [], seen.get(x) ?? new Set()))
        .reduce<Status | undefined>((a, r) => (r && (!a || rank(r) > rank(a)) ? r : a), undefined);
      b.append(tiles(front ? programs.get(front) : undefined, blocks.filter((x) => x !== front).map((x) => programs.get(x)), urgent?.state === "done", room), title);
      if (urgent?.state === "blocked") {
        const dot = document.createElement("i");
        dot.className = "attention-dot";
        dot.title = "Needs Input";
        b.append(dot);
      } else if (urgent?.progress !== undefined) {
        const ring = document.createElement("i");
        ring.className = "progress-ring";
        ring.style.setProperty("--p", String(urgent.progress));
        ring.title = `${urgent.progress}%`;
        b.append(ring);
      }
      const n = blocks.length;
      if (n > 1) {
        const count = document.createElement("span");
        count.className = "count";
        count.textContent = String(n);
        b.append(count);
      }
      b.onclick = () => show(s.id, w.window_id, true);
      const x = document.createElement("button");
      x.className = "icon close";
      x.type = "button";
      x.append(icon("x"));
      x.title = "Close Window";
      x.onclick = () => closeWindow(s.id, w.window_id);
      row.append(b, x);
      nav.append(row);
    }
  }
  if (!nav.children.length) nav.textContent = !current || !control ? "" : sessions.size ? "" : "No sessions on this host.";
  $("list").replaceChildren(nav);
}

/** Shows a session's window, its panes live. */
function show(session: string, window: string, keys: boolean) {
  if (shown?.session === session && shown.window === window) {
    if (keys) shown.focusKeys();
    return;
  }
  const w = sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
  if (!w) return;
  hide();
  shown = new WindowView(session, window, stage, host);
  shown.update(w);
  if (keys) shown.focusKeys();
  sidebar();
}

function hide() {
  shown?.close();
  shown = undefined;
}

/** A window made here, to show when the session's view has it. */
let wantWindow: { session: string; window: string } | undefined;

function showWanted() {
  const w = wantWindow;
  if (w && sessions.get(w.session)?.view?.windows?.some((x: any) => x.window_id === w.window)) {
    wantWindow = undefined;
    show(w.session, w.window, true);
  }
}

async function newWindow(session: string) {
  try {
    const r = (await control!.call("session.new_window", { session_id: session, focus: false, layout: { block: shell } })).payload;
    // Shown once a view has it: this read's, or one already on its way.
    wantWindow = r.window_id && { session, window: r.window_id };
    await refresh(session);
    showWanted();
  } catch (e) {
    status(`Could not open a window: ${describe(e)}`);
  }
}

function closeWindow(session: string, window: string) {
  control?.call("session.close_window", { session_id: session, window_id: window }).catch((e) => status(`Could not close the window: ${describe(e)}`));
}

/** The windows in the sidebar's order. */
function windows(): [string, string][] {
  return [...sessions.values()].flatMap((s) => (s.view?.windows ?? []).map((w: any) => [s.id, w.window_id] as [string, string]));
}

/** Runs one of the window's commands, from a shortcut or a pane's button. */
function run(name: string) {
  const w = shown;
  if (name === "next-window" || name === "previous-window") {
    const all = windows();
    if (!all.length) return;
    const at = w ? all.findIndex(([, id]) => id === w.window) : -1;
    const n = all.length;
    const [s, id] = all[name === "next-window" ? (at + 1) % n : at < 0 ? n - 1 : (at - 1 + n) % n];
    show(s, id, true);
    return;
  }
  if (!w) return;
  const block = w.focus;
  const c = control;
  if (!c) return;
  const fail = (what: string) => (e: unknown) => status(`Could not ${what}: ${describe(e)}`);
  switch (name) {
    case "split-right":
    case "split-down":
      if (!block) return;
      c.call("session.new_split", {
        session_id: w.session, anchor_id: block, direction: name === "split-right" ? "horizontal" : "vertical",
        side: "after", focus: true, layout: { block: shell },
      }).then((r) => {
        const id = r.payload.block_ids?.[0];
        if (id && shown === w) {
          w.want = id;
          // Where the keyboard is in the window, it goes to the new pane.
          if (w.panes.has(id)) w.setFocus(id, false);
        }
      }, fail("split the pane"));
      break;
    case "close":
      if (block) c.call("block.close", { session_id: w.session, block_id: block }).catch(fail("close the pane"));
      break;
    case "zoom":
      if (block) c.call("session.zoom_block", { session_id: w.session, block_id: block, zoom: w.zoomedBlock !== block }).catch(fail("zoom the pane"));
      break;
    case "new-window":
      newWindow(w.session);
      break;
    case "close-window":
      closeWindow(w.session, w.window);
      break;
    case "focus-left":
    case "focus-right":
    case "focus-up":
    case "focus-down": {
      const [dx, dy] = { "focus-left": [-1, 0], "focus-right": [1, 0], "focus-up": [0, -1], "focus-down": [0, 1] }[name];
      const b = w.neighbour(dx, dy);
      if (b) {
        w.setFocus(b, true);
        w.focusKeys();
      }
      break;
    }
    case "resize-left":
    case "resize-right":
    case "resize-up":
    case "resize-down": {
      // The nearest split of that orientation moves its divider a step, as the desktop's keys do.
      const vertical = name === "resize-up" || name === "resize-down";
      const s = w.splitAround(vertical);
      if (!s) return;
      const step = name === "resize-left" || name === "resize-up" ? -0.05 : 0.05;
      w.setRatio(s, s.ratio + step);
      host.resizeSplit(w, s, s.ratio);
      break;
    }
  }
}

// The window's shortcuts, before a pane's terminal sees the keys. Only
// the chords below are taken; every other key goes to the terminal.
window.addEventListener(
  "keydown",
  (e) => {
    const t = e.target as HTMLElement;
    const name = shortcut(e);
    if (!name) return;
    // Settings opens and closes from anywhere; the rest only from the window.
    if (name === "settings" && settings) {
      e.preventDefault();
      e.stopPropagation();
      settings.toggle();
      return;
    }
    if (t.closest?.("input, textarea, select, .theme-panel, dialog")) return;
    e.preventDefault();
    e.stopPropagation();
    run(name);
  },
  { capture: true },
);
window.addEventListener("focus", () => shown?.panes.forEach((p) => p.wake()));
window.addEventListener("blur", () => shown?.panes.forEach((p) => p.redraw()));
// A change of the pixel ratio, as when the window moves to another display,
// may leave the panes' CSS size as it was.
(function watchScale() {
  const q = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
  q.addEventListener("change", () => {
    shown?.panes.forEach((p) => p.layout());
    watchScale();
  }, { once: true });
})();
// A font that loads late changes the cells.
document.fonts.addEventListener("loadingdone", () => {
  shown?.panes.forEach((p) => {
    p.fontStale = true;
    p.layout();
  });
});

main();
