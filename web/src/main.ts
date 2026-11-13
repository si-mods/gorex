// GoRex's web client: the sessions a host's gateway serves, and a window's
// panes, live, over the same API the iOS app speaks. The page is hosted on
// its own; the host is one the user added (hosts.ts), or one built-in
// Tailscale found on the tailnet (tailnet.ts).
import { onAppChange, startApp } from "./app";
import { Chunk } from "./chunks";
import { Ghostty } from "./ghostty";
import type { Guide } from "./guide";
import { mountCorner } from "./corner";
import { chosenHost, commandBox, diagnose, Host, HostList, hostURL, icon, loadHosts, mixedContent, Problem } from "./hosts";
import { localize, Msg, msg, onLanguageChange, PlainKey, t } from "./i18n";
import { shortcut } from "./keys";
import { Asset, loader } from "./loader";
import { closeMenu, confirmDanger, draggable, DropZone, focusRename, MenuItem, onMenu, openMenu, ownDrag, renameField, zoneSplit } from "./manage";
import { describe } from "./pane";
import { ensureLatin, initMaple, mapleFamily, onChunk } from "./maple";
import { fontFamily, paneHeaders, prefs } from "./prefs";
import { LoadState, ProgressView } from "./progress";
import { mostUrgent, programName, programOf, rank, Status, statusOf, tiles, tilesWidth } from "./programs";
import { Control, directTransport, Transport } from "./rex";
import type { LinkState, Settings } from "./settings";
import { Tailnet, TailnetHost } from "./tailnet";
import { desktopTheme, Theme } from "./terminal";
import { isLight, startTheme, ThemeEntry, themeColours } from "./themes";
import type { Welcome } from "./welcome";
import { borderLeaf, layoutOf, leavesOf, WindowHost, WindowView } from "./window";

/**
 * The asset manifest (loader.ts) and the files the page loads as it
 * starts, which build.mjs writes into this script: the loader takes those
 * at once, the rest once it has read the manifest.
 */
declare const __ASSET_MANIFEST__: string;
declare const __BOOT_ASSETS__: Record<string, Asset>;
loader.configure(
  typeof __ASSET_MANIFEST__ === "string" ? __ASSET_MANIFEST__ : "",
  typeof __BOOT_ASSETS__ === "object" ? __BOOT_ASSETS__ : {},
);

/**
 * The parts of the page's code loaded when first used (chunks.ts): the
 * Settings dialog, the Tailscale guide and the first visit's welcome.
 */
const settingsCode = new Chunk("settings", () => t("chunkSettings"), () => import("./settings"));
const guideCode = new Chunk("guide", () => t("chunkGuide"), () => import("./guide"));
const welcomeCode = new Chunk("welcome", () => t("chunkWelcome"), () => import("./welcome"));

const $ = (id: string) => document.getElementById(id)!;
const stage = $("stage");
const shell = { flavor: "com.superlogical.terminal.shell" };

let g: Ghostty;
let control: Control | undefined;
/** Whether control's sessions were read (attached) since it opened: panes write only then. */
let live = false;
let token: string | undefined;
/** How the page reaches current: made by choose(), taken by Control.open. */
let transport: Transport | undefined;
/** The host connected to, or connecting; one at a time. */
let current: Host | undefined;
/** Counts the hosts chosen, so that a connection to one chosen before ends. */
let generation = 0;
let retry: ReturnType<typeof setTimeout> | undefined;
let hostList: HostList;
let settings: Settings | undefined;
/** Built-in Tailscale, off until the user turns it on; its engine loads only then. */
const tailnet = new Tailnet();
let guide: Guide | undefined;
/** The first screen, while no host is chosen. */
let welcome: Welcome | undefined;
/** The chosen host's connection, and what is wrong with it, for the Settings dialog's host card. */
let link: LinkState = "none";
let problem: Problem | undefined;
/** The theme chosen in this browser, over the desktop's colours; none keeps the host's. */
let chosen: Theme | undefined;
/** Its entry, for the Settings dialog's previews. */
let chosenEntry: ThemeEntry | undefined;

/** A session the gateway serves, and its last view. */
interface Session {
  id: string;
  label: string;
  view?: any;
  /** The connection a read of the view is on its way over. */
  reading?: Control;
  /** A read asked for while one was on its way, and whether it asked to attach. */
  again?: boolean;
  attach?: boolean;
}

/** The sessions, in the order the host lists them. */
const sessions = new Map<string, Session>();
/** The terminals' titles, by block, kept up by title_changed. */
const titles = new Map<string, string>();
/** The foreground programs' names, by block, kept up by process_changed. */
const programs = new Map<string, string | undefined>();
/** What the programs in each block reported (OSC 7501), by block. */
const statuses = new Map<string, Status[]>();
/**
 * When the latest done or error record the user saw was updated, by block:
 * those till then were seen. The server keeps it for every client
 * (seen_at, gorex.see_block).
 */
const seen = new Map<string, number>();
/** The blocks whose gorex.see_block call is in flight. */
const seeing = new Set<string>();
/** The blocks whose program and records were read; read again after a reconnect. */
const described = new Set<string>();
/** The window this page shows. */
let shown: WindowView | undefined;

/** The terminal background the page's chrome follows. */
let lastBg: [number, number, number] = desktopTheme().background!;

/** The page's chrome: the Window setting, else the terminal's background (light for a light one). */
function scheme(bg: [number, number, number]) {
  lastBg = bg;
  const s = prefs.appearance || (isLight(bg) ? "light" : "dark");
  document.documentElement.dataset.scheme = s;
  // The app window's title bar, and a phone's browser bar, in the page's colour (index.html's --page).
  document.querySelector('meta[name="theme-color"]')?.setAttribute("content", s === "light" ? "#ececf0" : "#1c1c1e");
}

/** Sets the connection's state, and shows it in the Settings dialog. */
function setLink(s: LinkState, p?: Problem) {
  link = s;
  problem = p;
  settings?.update();
}

/** Applies a changed setting to the page and to every open pane, as the desktop's applyPrefs. */
function applyPrefs(what: "appearance" | "tabs" | "headers" | "animation" | "font" | "cursor" | "contrast", reset = false) {
  const panes = [...(shown?.panes.values() ?? [])];
  switch (what) {
    case "appearance":
      scheme(lastBg);
      break;
    case "tabs":
      // The page's layout (index.html, body[data-tabs]) and what the bar or the sidebar lists; the headers follow the place.
      document.body.dataset.tabs = prefs.tabPlacement || "above";
      document.body.classList.toggle("no-pane-headers", !paneHeaders());
      sidebar();
      break;
    case "headers":
      document.body.classList.toggle("no-pane-headers", !paneHeaders());
      break;
    case "animation":
      // Read as the panes move (window.ts).
      break;
    case "font":
      // The previews and command boxes take the family too.
      document.documentElement.style.setProperty("--mono", fontFamily());
      panes.forEach((p) => p.restyle());
      // Maple chosen: its Latin chunks, which the cells are measured with, then measured again.
      if (prefs.font === mapleFamily) void ensureLatin().then(() => shown?.panes.forEach((p) => p.restyle()));
      break;
    case "contrast":
      panes.forEach((p) => p.redraw());
      break;
    case "cursor":
      panes.forEach((p) => p.applyCursor(reset));
      break;
  }
}

/** What the status line says, kept to say it again after a change of language. */
let statusText: Msg = "";

function status(s: Msg) {
  statusText = s;
  $("status").textContent = msg(s);
}

/** What a window and its panes need of the page. */
const host: WindowHost = {
  control: () => control,
  live: () => (live ? control : undefined),
  get g() {
    return g;
  },
  chosen: () => chosen,
  status,
  scheme,
  titleOf: (b) => paneName(b) || titles.get(b) || t("sidebarTerminal"),
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
        status(() => t("statusMoveDividerFailed", { error: describe(e) }));
        refresh(w.session, true);
      });
  },
  paneMenu: (w, block, x, y) => openMenu(x, y, paneMenu(w, block), () => shown?.focusKeys()),
  dropPane,
  edgeDrops: () => gateway.edgeMoves(),
};

/**
 * What the host's gateway takes past what every GoRex gateway did, which
 * turns on the desktop's moves: its welcome lists gorex.move_window.
 */
const gateway = {
  /**
   * session.move_block within a window and before its anchor: a pane
   * dropped on another's edge (panedrop.go). Rex's document has it; GoRex's
   * gateway took it in the same release as gorex.move_window, the feature
   * that tells it (an older one answers conflict).
   */
  edgeMoves: (): boolean => !!control?.features.has("gorex.move_window"),
  /** The call that moves a window to another session (moveTabToSpace), undefined while the gateway has none. */
  /** gorex.merge_windows: a window's row dropped on another's middle merges the two (the desktop's mergeTab). */
  mergeWindows: (): boolean => !!control?.features.has("gorex.merge_windows"),
  /** gorex.move_block_to_new_window: the pane menu's Move Pane to New Window (movePaneToNewTab). */
  blockToNewWindow: (): boolean => !!control?.features.has("gorex.move_block_to_new_window"),
  get moveWindowToSpace(): undefined | ((c: Control, session: string, window: string, to: string) => Promise<unknown>) {
    if (!control?.features.has("gorex.move_window")) return undefined;
    return (c, session, window, to) => c.call("gorex.move_window", { session_id: session, window_id: window, to_session_id: to });
  },
};

/** How long the page loads before its progress shows: a load from the cache shows none. */
const progressAfter = 150;

/**
 * The startup load's progress over the stage: shown only once the load
 * takes progressAfter, gone when it is done, and its failure with Retry.
 */
function bootProgress() {
  const box = $("boot");
  const view = new ProgressView(() => t("progressTerminal"), {
    download: () => t("bootLoading"),
    start: () => t("bootStarting"),
    failed: () => t("bootFailed"),
  }, () => {
    box.hidden = true;
    main();
  });
  const card = document.createElement("div");
  card.className = "boot-card";
  card.append(view.el);
  box.replaceChildren(card);
  let over = false;
  const timer = setTimeout(() => (box.hidden = over), progressAfter);
  const end = () => {
    over = true;
    clearTimeout(timer);
  };
  return {
    update: (s: LoadState) => view.update(s),
    fail(message: Msg) {
      end();
      view.update({ phase: "failed", message });
      box.hidden = false;
      view.focusRetry();
    },
    done() {
      end();
      box.hidden = true;
    },
  };
}

async function main() {
  status(() => t("statusLoading"));
  const boot = bootProgress();
  try {
    // The font too: the cells are measured from it.
    // Maple Mono NF CN's index (maple.ts), and its Latin chunks when it is the chosen font.
    const faces = [
      ...["400", "700", "italic 400", "italic 700"].map((v) => document.fonts.load(`${v} 12px "JetBrains Mono"`)),
      initMaple().then(() => (prefs.font === mapleFamily ? ensureLatin() : undefined)),
    ];
    // The chosen theme with it, so that the first frame has its colours (themes.ts: without the list).
    let theme: ThemeEntry | undefined;
    [g, theme] = await Promise.all([Ghostty.load(boot.update), startTheme(), Promise.allSettled(faces)]).then((r) => [r[0], r[1]]);
    boot.done();
    const pick = (t?: ThemeEntry) => {
      chosenEntry = t;
      chosen = t && themeColours(t);
      scheme(chosen?.background ?? desktopTheme().background!);
      for (const p of shown?.panes.values() ?? []) p.setChosen(chosen);
    };
    pick(theme);
    chooseTheme = pick;
    hostList = new HostList($("hosts"), {
      choose, close: () => shown?.focusKeys(), changed: () => {
        settings?.update();
        // The host view lists the hosts.
        sidebar();
      }, tailnet, guide: openGuide,
    });
    for (const what of ["tabs", "font"] as const) applyPrefs(what);
    onAppChange(() => settings?.appChanged());
    tailnet.onChange(tailnetChanged);
    $("open-settings").onclick = () => withSettings((s) => s.toggle());
    mountCorner($("corner"), () => withSettings((s) => s.toggle()));
    // A change of language says the page's words again in it: the dialogs say theirs themselves.
    onLanguageChange(() => {
      closeMenu();
      status(statusText);
      hostList.relocalize();
      sidebar();
      shown?.retitle();
      if (shownNotice) notice(...shownNotice);
    });
  } catch (e) {
    status(() => t("statusLoadFailed", { error: describe(e) }));
    boot.fail(() => t("bootFailedText", { error: describe(e) }));
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

/** Applies a theme chosen in Settings; set once the page has started. */
let chooseTheme: (t?: ThemeEntry) => void = () => {};

/** Runs f with the Settings dialog, which is made the first time, once its code is in. */
function withSettings(f: (s: Settings) => void) {
  settingsCode.use((m) => {
    settings ??= new m.Settings({
      hosts: hostList,
      link: () => ({ host: current, state: link, problem }),
      theme: () => chosenEntry,
      chooseTheme: (t) => chooseTheme(t),
      apply: applyPrefs,
      closed: () => shown?.focusKeys(),
      tailnet,
      guide: openGuide,
    });
    f(settings);
  });
}

/** Opens the Tailscale guide, in place of the Settings dialog. */
function openGuide() {
  settings?.close();
  guideCode.use((m) => {
    guide ??= new m.Guide(tailnet, { connect: (h) => hostList.choose(h), closed: () => shown?.focusKeys() });
    guide.open();
  });
}

/**
 * Built-in Tailscale changed: the host list and Settings show it, and a
 * tailnet host chosen that is gone (signed out, Tailscale off) gives way
 * to the first host added.
 */
function tailnetChanged() {
  onboarding();
  hostList.refresh();
  sidebar();
  settings?.update();
  if (current?.kind === "tailnet" && tailnet.status() !== "running" && !tailnet.hosts.some((h) => h.id === current!.id)) {
    hostList.choose(hostList.hosts[0]);
  } else if (!current) {
    // The welcome's Tailscale card follows its state.
    welcome?.update();
  }
}

/** A button of the notice: its words, as a key or said when drawn. */
type NoticeAction = { label: PlainKey | (() => string); run: () => void; primary?: boolean };

/** The notice shown, drawn again after a change of language. */
let shownNotice: [Problem | undefined, ...NoticeAction[]] | undefined;

/**
 * The first screen has the whole window: no sidebar, no status line. That is while no host is chosen and none
 * exists to choose. Hosts that Tailscale found keep the sidebar, as its host list is where they are chosen.
 */
function onboarding() {
  document.body.classList.toggle("onboarding", !current && !hostList.hosts.length && !tailnet.hosts.length);
}

/** Shows the welcome over the empty stage: no host is chosen. Its code loads only then, as on the first visit. */
function showWelcome() {
  shownNotice = undefined;
  const gen = generation;
  welcomeCode.use((m) => {
    // A host chosen meanwhile has the stage.
    if (gen !== generation || current) return;
    welcome ??= new m.Welcome({ hosts: hostList, tailnet, guide: openGuide });
    const box = $("notice");
    welcome.update();
    if (box.firstElementChild !== welcome.el) box.replaceChildren(welcome.el);
    box.hidden = false;
  });
}

/** Shows what is wrong with the host, or nothing. */
function notice(p?: Problem, ...actions: NoticeAction[]) {
  const box = $("notice");
  box.hidden = !p;
  shownNotice = p && [p, ...actions];
  if (!p) return;
  // A retry finds the same problem every few seconds: the card stays, with its focus and its open details.
  const said = JSON.stringify([generation, document.documentElement.lang, p.title, p.text, p.command, p.details, actions.map((a) => (typeof a.label === "function" ? a.label() : a.label))]);
  if (box.firstElementChild?.classList.contains("notice-card") && box.dataset.said === said) return;
  box.dataset.said = said;
  const card = document.createElement("div");
  card.className = "notice-card";
  const h = document.createElement("h1");
  h.textContent = p.title;
  const text = document.createElement("p");
  text.textContent = p.text;
  card.append(h, text);
  if (p.command) card.append(commandBox(p.command));
  if (p.details) {
    const more = document.createElement("details");
    more.className = "notice-more";
    const s = document.createElement("summary");
    const label = document.createElement("span");
    label.textContent = t("problemLearnMore");
    s.append(icon("info"), label);
    const why = document.createElement("p");
    why.textContent = p.details;
    more.append(s, why);
    card.append(more);
  }
  if (actions.length) {
    const row = document.createElement("div");
    row.className = "notice-actions";
    actions.forEach((a, i) => {
      const b = document.createElement("button");
      b.type = "button";
      b.className = a.primary ?? i === 0 ? "host-add primary" : "host-add";
      b.textContent = typeof a.label === "function" ? a.label() : t(a.label);
      b.onclick = a.run;
      row.append(b);
    });
    card.append(row);
  }
  box.replaceChildren(card);
}

/** Leaves the host connected to, and connects to h. Only the chosen host is connected. */
function choose(h: Host | undefined) {
  generation++;
  clearTimeout(retry);
  const c = control;
  control = undefined;
  live = false;
  c?.close();
  hide();
  token = undefined;
  wantWindow = undefined;
  // What the sidebar held still for goes with the host.
  closeMenu();
  renaming = undefined;
  rowDragging = false;
  for (const m of [sessions, titles, programs, statuses, seen, seeing]) m.clear();
  described.clear();
  current = h;
  onboarding();
  sidebar();
  if (!h) {
    status("");
    setLink("none");
    showWelcome();
    return;
  }
  if (h.kind === "tailnet") {
    // A tailnet host: no URL the browser opens, so no mixed content.
    transport = tailnet.transport(h as TailnetHost);
    notice();
    setLink("connecting");
    connect(generation);
    return;
  }
  transport = directTransport(h.url);
  const blocked = mixedContent(h.url);
  if (blocked) {
    status(() => t("statusBlocked", { host: h.name }));
    setLink("unreachable", blocked);
    notice(blocked, { label: "hostEdit", run: () => hostList.open(h) });
    return;
  }
  notice();
  setLink("connecting");
  connect(generation);
}

/** Opens the control connection to the chosen host, again after it drops. */
async function connect(gen: number) {
  const h = current!;
  status(() => t("statusConnecting", { host: h.name }));
  if (link !== "unreachable") setLink("connecting");
  let c: Control;
  try {
    c = await Control.open(transport!, token);
  } catch (e) {
    if (gen !== generation) return;
    const p = h.kind === "tailnet" ? await tailnet.diagnose(h as TailnetHost) : await diagnose(h.url);
    if (gen !== generation) return;
    if (h.kind === "tailnet") notice(p, { label: "noticeTailscaleSettings", run: () => withSettings((s) => s.open("tailscale")) });
    else notice(p, { label: "hostEdit", run: () => hostList.open(h) });
    setLink("unreachable", p);
    status(() => t("statusRetrying", { host: h.name, problem: p.title }));
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
  live = false;
  described.clear();
  token = c.reconnectToken;
  c.onEvent = (e) => event(e.type, e.sessionID, e.payload);
  c.onClose = (reason) => {
    if (control !== c) return;
    control = undefined;
    live = false;
    setLink("connecting");
    status(() => t("statusDisconnected", { host: h.name, reason: msg(reason) }));
    retry = setTimeout(() => connect(gen), 3000);
  };
  try {
    await c.call("session.bootstrap", { event_scope: "all" });
    await list();
    // Connected is the usual state: the status line says nothing, and an earlier problem goes.
    status("");
    for (const p of shown?.panes.values() ?? []) p.start();
  } catch (e) {
    status(() => t("statusSessionsFailed", { error: describe(e) }));
  }
  if (control !== c) return;
  // What was typed while offline goes now.
  live = true;
  for (const p of shown?.panes.values() ?? []) p.resume();
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
      if (renaming?.session === id) renaming = undefined;
      if (shown?.session === id) {
        // The space closed: show another, as the desktop's window does (closeWorkspace).
        hide();
        // Not the keyboard from a field (a rename elsewhere) or a menu: it commits on blur.
        showAny(!busyFocus());
      }
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
        sidebarBlock(p.block_id);
        shown?.retitle();
      } else if (p.name === "process_changed" && p.block_id) {
        programs.set(p.block_id, programName(p.payload?.foreground));
        sidebarBlock(p.block_id);
        shown?.retitle();
      } else if (p.name === "program_status_changed" && p.block_id) {
        statuses.set(p.block_id, recordsOf(p.payload?.records));
        raiseSeen(p.block_id, Date.parse(p.payload?.seen_at));
        see(p.block_id);
        sidebarBlock(p.block_id);
      } else if (p.name === "program_status_removed" && p.block_id) {
        const ids: string[] = p.payload?.ids ?? [];
        statuses.set(p.block_id, (statuses.get(p.block_id) ?? []).filter((r) => !ids.includes(r.id)));
        sidebarBlock(p.block_id);
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
 * layout methods need. One read at a time on a connection; a change
 * meanwhile reads again, attaching if any of the reads asked for it. A
 * read on a connection that was replaced stops, and the new one reads.
 */
async function refresh(id: string, attach = false) {
  const s = sessions.get(id);
  const c = control;
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
      if (fresh || !s.view || v.revision > s.view.revision) apply(s, v);
    }
  } catch (e) {
    console.warn("session view", id, e);
  } finally {
    if (s.reading === c) s.reading = undefined;
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
    if (w) {
      shown.update(w);
      // A pane's name may have changed.
      shown.retitle();
    } else {
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
        sidebarBlock(b);
        shown?.retitle();
      } else if (tries > 1) setTimeout(() => readTitle(session, b, tries - 1), 1000);
    },
    () => {},
  );
}

function recordsOf(j: any): Status[] {
  return (Array.isArray(j) ? j : []).map(statusOf).filter((r): r is Status => !!r);
}

/** Takes a block's seen time, which only moves forward; NaN, no seen_at, leaves it. */
function raiseSeen(block: string, at: number) {
  if (at > (seen.get(block) ?? -1)) seen.set(block, at);
}

/**
 * Marks a shown block's done and error records seen, as the desktop's pane
 * does when it shows, and tells the server, which tells every client. A
 * server that keeps it is told first, and its answer marks them, so that a
 * call that failed is made again as the block is seen next; against one
 * that doesn't, this page marks them itself.
 */
function see(block: string) {
  const w = shown, c = control;
  if (!w?.panes.has(block) || document.visibilityState !== "visible") return;
  const finished = (statuses.get(block) ?? []).filter((r) => r.state === "done" || r.state === "error");
  if (!finished.length) return;
  // A record without a time is seen as any time is.
  const latest = Math.max(0, ...finished.map((r) => r.updated ?? 0));
  if (latest <= (seen.get(block) ?? -1)) return;
  if (!latest || !c?.features.has("gorex.see_block")) return raiseSeen(block, latest);
  if (seeing.has(block)) return;
  seeing.add(block);
  c.call("gorex.see_block", { session_id: w.session, block_id: block, seen_at: new Date(latest).toISOString() })
    .then(() => {
      raiseSeen(block, latest);
      sidebarBlock(block);
    }, () => {})
    .finally(() => seeing.delete(block));
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
        sidebarBlock(block);
        shown?.retitle();
      }
    },
    () => described.delete(block),
  );
  c.callBlock("program_status", session, block).then(
    (r) => {
      raiseSeen(block, Date.parse(r.seen_at));
      if (!statuses.has(block) || statuses.get(block) === hadRecords) {
        statuses.set(block, recordsOf(r.records));
        see(block);
        sidebarBlock(block);
      }
    },
    () => {},
  );
}

/**
 * The name the user gave a window; undefined when its label is the
 * host's default, "Window " and the end of its ID, as the iOS app tells.
 */
function windowName(w: any): string | undefined {
  const label: string = w.label ?? "";
  return label && label !== "Window " + String(w.window_id).slice(-8) ? label : undefined;
}

/** A window's title, as the desktop names a tab (Tab.label): its name, else its terminal's title, else its label. */
function windowTitle(w: any): string {
  const blocks = leavesOf(layoutOf(w));
  const focused = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
  return windowName(w) || (focused && (paneName(focused) || titles.get(focused))) || w.label || t("sidebarWindow");
}

/**
 * The name the user gave a pane (session.set_block_label), as the
 * desktop's Pane.Name; undefined while it has the gateway's default.
 * shortcut: a pane its user named "shell" reads as unnamed, as the view
 * gives no other sign; upgrade if the gateway tells a default label apart.
 */
function paneName(block: string): string | undefined {
  for (const s of sessions.values())
    for (const w of s.view?.windows ?? [])
      for (const l of w.layers ?? [])
        for (const b of l.blocks ?? []) if (b.block_id === block) return b.label && b.label !== "shell" ? b.label : undefined;
  return undefined;
}

/** The room of the tiles of a session's window with the most, so that the titles line up. */
function tilesRoom(v: any): number {
  return tilesWidth(Math.max(0, ...(v.windows ?? []).map((w: any) => leavesOf(layoutOf(w)).length - 1)));
}

/** A window row's button: its programs' tiles, its title, its most urgent record and its count of panes. */
function fillWindow(b: HTMLElement, w: any, room: number) {
  const title = document.createElement("span");
  title.className = "title";
  title.textContent = windowTitle(w);
  const blocks = leavesOf(layoutOf(w));
  const front = blocks.includes(w.focused_block_id) ? w.focused_block_id : blocks[0];
  const urgent = blocks
    .map((x) => mostUrgent(statuses.get(x) ?? [], seen.get(x)))
    .reduce<Status | undefined>((a, r) => (r && (!a || rank(r) > rank(a)) ? r : a), undefined);
  b.replaceChildren(
    tiles(front ? programs.get(front) : undefined, blocks.filter((x) => x !== front).map((x) => programs.get(x)), urgent?.state === "done", room),
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
function sidebarBlock(block: string) {
  for (const b of shown?.panes.keys() ?? []) see(b);
  const list = $("list");
  for (const s of sessions.values()) {
    const v = s.view;
    if (!v) continue;
    for (const w of v.windows ?? []) {
      if (!leavesOf(layoutOf(w)).includes(block)) continue;
      const b = list.querySelector<HTMLElement>(`.window[data-window="${CSS.escape(w.window_id)}"] > .block`);
      if (!b) return sidebar();
      fillWindow(b, w, tilesRoom(v));
    }
  }
}

/** A space or a window being renamed in the sidebar; the sidebar is not made again meanwhile. */
let renaming: { session: string; window?: string } | undefined;
/** Whether one of the sidebar's rows drags: the drag would end with its row. */
let rowDragging = false;
/** Whether the sidebar waits to be made again, once it no longer holds still. */
let sidebarStale = false;
/** A window's row clicked last, and when: a second click soon after renames it, as the desktop's double click. */
let lastClick: { window: string; at: number } | undefined;

/**
 * The tabs, as the Tab placement setting puts them: the bar above the
 * panes, or the sidebar of each session and a row for each of its
 * windows, under their host in the host view. It holds still while a
 * field renames or a row drags, which would go with their elements, and
 * is made once they are done; force makes it now.
 */
function sidebar(force = false) {
  for (const b of shown?.panes.keys() ?? []) see(b);
  if (!force && (renaming || rowDragging)) {
    sidebarStale = true;
    return;
  }
  sidebarStale = false;
  const nav = document.createElement("nav");
  if (!prefs.tabPlacement) tabBar(nav);
  else if (prefs.sidebarView === "host") hostSections(nav);
  else for (const s of sessions.values()) spaceSection(nav, s);
  if (!nav.children.length) nav.textContent = !current || !control ? "" : sessions.size ? "" : t("sidebarNoSessions");
  $("list").replaceChildren(nav);
}

/** A session in the sidebar: its heading, with its menu and New Window, and a row for each of its windows. */
function spaceSection(nav: HTMLElement, s: Session) {
  const v = s.view;
  if (!v) return;
  const h = document.createElement("h2");
  h.dataset.session = s.id;
  const add = newWindowButton(s.id);
  if (renaming?.session === s.id && !renaming.window) {
    h.append(renameField(s.label, spaceName(s), t("renameSpaceLabel"), (name) => renamedSpace(s.id, name)), add);
  } else {
    const name = document.createElement("span");
    name.textContent = spaceName(s);
    h.append(name, add);
  }
  onMenu(h, (x, y) => openMenu(x, y, spaceMenu(s.id), () => shown?.focusKeys()));
  nav.append(h);
  const room = tilesRoom(v);
  for (const w of v.windows ?? []) nav.append(windowRow(s.id, w, room));
}

function newWindowButton(session: string): HTMLButtonElement {
  const add = document.createElement("button");
  add.className = "icon";
  add.type = "button";
  add.append(icon("plus"));
  add.title = t("cmdNewWindow");
  add.onclick = () => newWindow(session);
  return add;
}

/**
 * The bar above the panes, as the desktop's title bar with the tabs above
 * (workspaceChip, tabStrip): the shown space's name, which opens the
 * spaces' menu, a row for each of its windows, and New Window.
 */
function tabBar(nav: HTMLElement) {
  const s = (shown && sessions.get(shown.session)) || [...sessions.values()].find((o) => o.view);
  if (!s?.view) return;
  if (renaming?.session === s.id && !renaming.window) {
    nav.append(renameField(s.label, spaceName(s), t("renameSpaceLabel"), (name) => renamedSpace(s.id, name)));
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
      openMenu(r.left, r.bottom + 4, spacesMenu(s.id), () => shown?.focusKeys());
    };
    onMenu(chip, (x, y) => openMenu(x, y, spaceMenu(s.id), () => shown?.focusKeys()));
    nav.append(chip);
  }
  const room = tilesRoom(s.view);
  for (const w of s.view.windows ?? []) nav.append(windowRow(s.id, w, room));
  nav.append(newWindowButton(s.id));
}

/** The space chip's menu, as the desktop's workspace popover: every space to show, then New Space and the space's own items. */
function spacesMenu(current: string): MenuItem[] {
  const spaces: MenuItem[] = [...sessions.values()]
    .filter((o) => o.view)
    .map((o) => ({ label: spaceName(o), disabled: o.id === current, run: () => showSpace(o.id) }));
  return [...spaces, "separator", { label: t("cmdNewSpace"), run: () => newSpace() }, ...spaceMenu(current)];
}

/** Shows a space's active window. */
function showSpace(session: string) {
  const v = sessions.get(session)?.view;
  const next = v?.active_window_id ?? v?.windows?.[0]?.window_id;
  if (next) show(session, next, true);
}

/**
 * The sidebar's host view, as the desktop's hostSessions: each host the
 * page can connect to (its host list, and Tailscale's), under its name,
 * the connected one with its sessions. The page connects to one host at a
 * time: another host's heading connects to it.
 */
function hostSections(nav: HTMLElement) {
  const all: Host[] = [...(hostList?.hosts ?? []), ...(tailnet.status() === "running" ? tailnet.hosts : [])];
  if (current && !all.some((h) => h.id === current!.id)) all.unshift(current);
  for (const h of all) {
    const on = h.id === current?.id;
    const head = document.createElement(on ? "h3" : "button");
    head.className = "host-head";
    const name = document.createElement("span");
    name.textContent = h.name;
    head.append(icon(h.icon), name);
    nav.append(head);
    if (on) {
      for (const s of sessions.values()) spaceSection(nav, s);
      continue;
    }
    (head as HTMLButtonElement).type = "button";
    head.title = t("sidebarConnectHost", { host: h.name });
    head.onclick = () => hostList.choose(h);
  }
}

/** A space's name: its label, else its view's, else a word. */
function spaceName(s: Session): string {
  return s.label || s.view?.label || t("sidebarSession");
}

/**
 * A window's row: a click shows it, a second click renames it, its menu,
 * and its drag to another row's place (tabEvents in tabs.go). A pane
 * dropped on it goes into its window (tabRest's drop, movePaneToTab).
 */
function windowRow(session: string, w: any, room: number): HTMLElement {
  const id: string = w.window_id;
  const row = document.createElement("div");
  row.className = "window";
  row.dataset.window = id;
  if (shown?.window === id) row.classList.add("open");
  if (renaming?.session === session && renaming.window === id) {
    row.classList.add("renaming");
    row.append(renameField(windowName(w) ?? "", windowTitle(w), t("renameWindowLabel"), (name) => renamedWindow(session, id, name)));
    return row;
  }
  const b = document.createElement("button");
  b.className = "block";
  b.type = "button";
  fillWindow(b, w, room);
  b.onclick = () => {
    const now = performance.now();
    const again = lastClick?.window === id && now - lastClick.at < 500;
    lastClick = again ? undefined : { window: id, at: now };
    show(session, id, !again);
    if (again) startRename(session, id);
  };
  const x = document.createElement("button");
  x.className = "icon close";
  x.type = "button";
  x.append(icon("x"));
  x.title = t("cmdCloseWindow");
  x.onclick = () => closeWindow(session, id);
  row.append(b, x);
  onMenu(row, (x, y) => openMenu(x, y, windowMenu(session, id), () => shown?.focusKeys()));
  draggable(
    row,
    () => {
      rowDragging = true;
      return { kind: "window", session, window: id };
    },
    () => {
      rowDragging = false;
      markDrop(undefined);
      if (sidebarStale) sidebar();
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
    if (gateway.mergeWindows() && at >= size / 4 && at < (size * 3) / 4) return "into";
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
    if (d.kind === "pane") movePaneToWindow(session, d.block, id);
    else if (z === "into") mergeWindows(session, d.window, id);
    else moveWindowTo(session, d.window, id, z === "before");
  });
  return row;
}

/** The row a drag shows its drop on, and how: a line before or after it, or a ring for a pane going in. */
function markDrop(at: { row: HTMLElement; zone: "before" | "after" | "into" } | undefined) {
  for (const r of $("list").querySelectorAll<HTMLElement>(".window[data-drop]")) if (r !== at?.row) delete r.dataset.drop;
  if (at) at.row.dataset.drop = at.zone;
}

/** A window of a session's view. */
function windowOf(session: string, window: string): any {
  return sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
}

/** The windows of a session, in the host's order. */
function windowIDs(session: string): string[] {
  return (sessions.get(session)?.view?.windows ?? []).map((w: any) => w.window_id);
}

/** A space heading's menu, as the desktop's spaceMenu (workspaceview.go): rename it, or close it. */
function spaceMenu(session: string): MenuItem[] {
  return [
    { label: t("cmdRenameSpace"), run: () => startRename(session) },
    { label: t("cmdCloseSpace"), run: () => closeSpace(session) },
  ];
}

/**
 * A window row's menu, as the desktop's tabMenu (tabs.go) in the
 * sidebar: rename it or give its name back, move it up or down or to
 * another space, open a window, close it or the others.
 */
function windowMenu(session: string, window: string): MenuItem[] {
  const w = windowOf(session, window);
  if (!w) return [];
  const ids = windowIDs(session);
  const i = ids.indexOf(window);
  const items: MenuItem[] = [{ label: t("cmdRenameWindow"), run: () => startRename(session, window) }];
  if (windowName(w)) items.push({ label: t("windowResetName"), run: () => renamedWindow(session, window, "") });
  items.push(
    "separator",
    { label: t("windowMoveUp"), disabled: i <= 0, run: () => moveWindowTo(session, window, ids[i - 1], true) },
    { label: t("windowMoveDown"), disabled: i >= ids.length - 1, run: () => moveWindowTo(session, window, ids[i + 1], false) },
  );
  const move = gateway.moveWindowToSpace;
  const others = [...sessions.values()].filter((o) => o.id !== session && o.view);
  if (move && others.length) {
    items.push({
      label: t("windowMoveSpace"),
      submenu: others.map((o) => ({
        label: spaceName(o),
        run: () => {
          if (control) move(control, session, window, o.id).catch((e) => status(() => t("statusMoveWindowFailed", { error: describe(e) })));
        },
      })),
    });
  }
  items.push(
    "separator",
    { label: t("cmdNewWindow"), run: () => newWindow(session) },
    { label: t("cmdCloseWindow"), run: () => closeWindow(session, window) },
    { label: t("cmdCloseOtherWindows"), disabled: ids.length < 2, run: () => ids.filter((o) => o !== window).forEach((o) => closeWindow(session, o)) },
  );
  return items;
}

/**
 * A pane header's menu, as the desktop's paneMenu: rename the pane or give
 * its name back, move it to another window of its session
 * (movePaneToTab), or to a window of its own (movePaneToNewTab).
 */
function paneMenu(w: WindowView, block: string): MenuItem[] {
  const items: MenuItem[] = [{ label: t("paneRename"), run: () => startPaneRename(w, block) }];
  if (paneName(block)) items.push({ label: t("paneResetName"), run: () => renamePane(w.session, block, "") });
  items.push("separator");
  const others = (sessions.get(w.session)?.view?.windows ?? []).filter((o: any) => o.window_id !== w.window);
  if (others.length) {
    items.push({
      label: t("paneMoveWindow"),
      submenu: others.map((o: any) => ({ label: windowTitle(o), run: () => movePaneToWindow(w.session, block, o.window_id) })),
    });
  }
  if (gateway.blockToNewWindow() && w.blocks.length > 1) {
    items.push({ label: t("paneMoveNewWindow"), disabled: !!w.zoomedBlock, run: () => moveToNewWindow(w.session, block) });
  }
  return items;
}

/** Opens the field that renames a pane in its header, as the desktop's startPaneRename. */
function startPaneRename(w: WindowView, block: string) {
  const p = w.panes.get(block);
  if (!p) return;
  closeMenu();
  // The pane renamed is the focused one, as the desktop's.
  w.setFocus(block, true);
  const name = paneName(block) ?? "";
  const f = renameField(name, titles.get(block) || t("sidebarTerminal"), t("renamePaneLabel"), (v) => {
    p.editTitle(undefined);
    if (v !== undefined && v !== name) renamePane(w.session, block, v);
    // The keyboard goes back to the pane, unless another field took it.
    if (shown === w && !busyFocus()) w.focusKeys();
  });
  p.editTitle(f);
  f.focus({ preventScroll: true });
  f.select();
}

/** Names a pane; an empty name gives it its terminal's title again (session.set_block_label). */
function renamePane(session: string, block: string, label: string) {
  control
    ?.call("session.set_block_label", { session_id: session, block_id: block, label })
    .catch((e) => status(() => t("statusRenamePaneFailed", { error: describe(e) })));
}

/** Moves a pane into a window of its own, after its window, as the desktop's movePaneToNewTab. Its program goes on. */
function moveToNewWindow(session: string, block: string) {
  control
    ?.call("gorex.move_block_to_new_window", { session_id: session, block_id: block })
    .catch((e) => status(() => t("statusMovePaneFailed", { error: describe(e) })));
}

/** Merges a window into another, side by side, and shows that one, as the desktop's mergeTab. The programs go on. */
function mergeWindows(session: string, window: string, into: string) {
  control?.call("gorex.merge_windows", { session_id: session, window_id: window, into_window_id: into }).then(
    () => {
      wantWindow = { session, window: into };
      showWanted();
    },
    (e) => status(() => t("statusMergeWindowsFailed", { error: describe(e) })),
  );
}

/** Opens the field that renames a space in its heading, or a window in its row. */
function startRename(session: string, window?: string) {
  if (!sessions.has(session) || (window && !windowOf(session, window))) return;
  closeMenu();
  renaming = { session, window };
  sidebar(true);
  focusRename($("list"));
}

/**
 * A space's new name, from its field: an empty one keeps the name it
 * has, as the desktop's renameWorkspace (session.set_label).
 */
function renamedSpace(session: string, name: string | undefined) {
  if (renaming?.session === session && !renaming.window) renaming = undefined;
  const s = sessions.get(session);
  if (s && name && name !== s.label) {
    const was = s.label;
    s.label = name;
    control?.call("session.set_label", { session_id: session, label: name }).catch((e) => {
      if (s.label === name) s.label = was;
      status(() => t("statusRenameSpaceFailed", { error: describe(e) }));
      sidebar();
    });
  }
  sidebar();
  // The keyboard goes back to the pane, unless another field took it.
  if (!renaming) shown?.focusKeys();
}

/**
 * A window's new name, from its field or Reset Name: an empty one gives
 * the window its terminal's title again, as the desktop's renameField
 * (session.set_window_label).
 */
function renamedWindow(session: string, window: string, name: string | undefined) {
  if (renaming?.window === window) renaming = undefined;
  const w = windowOf(session, window);
  if (w && name !== undefined && name !== (windowName(w) ?? "")) {
    control?.call("session.set_window_label", { session_id: session, window_id: window, label: name }).catch((e) =>
      status(() => t("statusRenameWindowFailed", { error: describe(e) })),
    );
  }
  sidebar();
  // The keyboard goes back to the pane, unless another field took it.
  if (!renaming) shown?.focusKeys();
}

/**
 * Moves a window of a session to the place of another: before it, or
 * after it (session.move_window takes the window it goes before, none
 * for the end).
 */
function moveWindowTo(session: string, window: string, target: string, before: boolean) {
  const ids = windowIDs(session).filter((id) => id !== window);
  const at = ids.indexOf(target);
  if (at < 0) return;
  const next = before ? target : ids[at + 1];
  const args: Record<string, string> = { session_id: session, window_id: window };
  if (next) args.before_window_id = next;
  control?.call("session.move_window", args).catch((e) => status(() => t("statusMoveWindowFailed", { error: describe(e) })));
}

/**
 * Moves a pane into another window of its session, beside the pane that
 * has its focus, as the desktop's movePaneToTab (session.move_block, side
 * by side, after it). Its program goes on.
 */
function movePaneToWindow(session: string, block: string, window: string) {
  const w = windowOf(session, window);
  const blocks = leavesOf(layoutOf(w));
  const anchor = blocks.includes(w?.focused_block_id) ? w.focused_block_id : blocks[0];
  if (!anchor) return;
  control
    ?.call("session.move_block", { session_id: session, block_id: block, anchor_block_id: anchor, direction: "horizontal", side: "after" })
    .catch((e) => status(() => t("statusMovePaneFailed", { error: describe(e) })));
}

/**
 * A pane dropped on another, as the desktop's dropPaneOn: in its middle
 * they swap places (session.swap_blocks), and the dragged one has the
 * focus; on an edge it goes beside it (session.move_block), where the
 * host takes that.
 */
function dropPane(w: WindowView, dragged: string, target: string, zone: DropZone) {
  const c = control;
  if (!c) return;
  const split = zoneSplit(zone);
  const call = split
    ? c.call("session.move_block", { session_id: w.session, block_id: dragged, anchor_block_id: target, ...split, ratio: 0.5 })
    : c.call("session.swap_blocks", { session_id: w.session, block_id: dragged, other_block_id: target });
  call.then(
    () => {
      if (shown === w) w.setFocus(dragged, true);
    },
    (e) => status(() => t(split ? "statusMovePaneFailed" : "statusSwapPanesFailed", { error: describe(e) })),
  );
}

/**
 * Makes a space with a window, and shows it, as the iOS app's New
 * Session and the desktop's New Space (session.create; the host names it).
 */
async function newSpace() {
  const c = control;
  if (!c) return;
  try {
    const r = (await c.call("session.create", { initial_windows: [{ layout: { block: shell } }] })).payload;
    const window = r.initial_windows?.[0]?.window_id;
    if (!r.session_id || !window) return;
    wantWindow = { session: r.session_id, window };
    if (!sessions.has(r.session_id)) sessions.set(r.session_id, { id: r.session_id, label: "" });
    await refresh(r.session_id, true);
    showWanted();
  } catch (e) {
    status(() => t("statusNewSpaceFailed", { error: describe(e) }));
  }
}

/**
 * Closes a space once the user says so, as the desktop's Close Space
 * (workspaceConfirm): its terminals end (session.destroy).
 */
async function closeSpace(session: string) {
  const s = sessions.get(session);
  if (!s) return;
  let n = 0;
  for (const w of s.view?.windows ?? []) for (const l of w.layers ?? []) n += l.blocks?.length ?? 0;
  const yes = await confirmDanger(t("spaceCloseTitle", { name: spaceName(s) }), t("spaceCloseBody", { n }), t("cmdCloseSpace"));
  shown?.focusKeys();
  if (yes) control?.call("session.destroy", { session_id: session }).catch((e) => status(() => t("statusCloseSpaceFailed", { error: describe(e) })));
}

/** Whether the keyboard is in a field or a menu that is not a pane's own. */
function busyFocus(): boolean {
  const a = document.activeElement;
  return !!a?.closest?.("input, textarea:not(.pane-input), select, dialog, .context-menu, .theme-panel");
}

/** Shows a window when none shows: the active one of the first session that has one. */
function showAny(keys: boolean) {
  if (shown) return;
  for (const s of sessions.values()) {
    const next = s.view?.active_window_id ?? s.view?.windows?.[0]?.window_id;
    if (next && windowOf(s.id, next)) return show(s.id, next, keys);
  }
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
    status(() => t("statusNewWindowFailed", { error: describe(e) }));
  }
}

function closeWindow(session: string, window: string) {
  control?.call("session.close_window", { session_id: session, window_id: window }).catch((e) => status(() => t("statusCloseWindowFailed", { error: describe(e) })));
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
  if (name === "new-space") {
    newSpace();
    return;
  }
  if (!w) return;
  const block = w.focus;
  const c = control;
  if (!c) return;
  const fail = (what: "statusSplitFailed" | "statusClosePaneFailed" | "statusZoomFailed") => (e: unknown) =>
    status(() => t(what, { error: describe(e) }));
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
      }, fail("statusSplitFailed"));
      break;
    case "close":
      if (block) c.call("block.close", { session_id: w.session, block_id: block }).catch(fail("statusClosePaneFailed"));
      break;
    case "zoom":
      if (block) c.call("session.zoom_block", { session_id: w.session, block_id: block, zoom: w.zoomedBlock !== block }).catch(fail("statusZoomFailed"));
      break;
    case "new-window":
      newWindow(w.session);
      break;
    case "close-window":
      closeWindow(w.session, w.window);
      break;
    case "rename-window":
      startRename(w.session, w.window);
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
    // Copy and paste are the focused pane's own (pane.ts): it copies its selection, and the browser's paste event reaches it.
    if (!name || name === "copy" || name.startsWith("paste")) return;
    // Settings opens and closes from anywhere; the rest only from the window.
    if (name === "settings" && hostList) {
      e.preventDefault();
      e.stopPropagation();
      withSettings((s) => s.toggle());
      return;
    }
    // The pane's own text field (pane.ts) holds its keys: they are the window's, not a form's.
    if (t.closest?.("input, textarea:not(.pane-input), select, .theme-panel, dialog, .context-menu")) return;
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
// A CJK font chunk arrived: the panes draw the characters that waited for it (maple.ts).
onChunk(() => shown?.panes.forEach((p) => p.repaint()));
// A font that loads late changes the cells.
document.fonts.addEventListener("loadingdone", () => {
  shown?.panes.forEach((p) => {
    p.fontStale = true;
    p.layout();
  });
});

// The Settings button, in the page's language from the start; and New
// Space, at the other end of the sidebar's foot, as the desktop's
// spaceStrip has its + button.
{
  const open = $("open-settings");
  const label = document.createElement("span");
  open.replaceChildren(icon("settings-2"), label);
  localize(open, () => {
    label.textContent = t("sidebarSettings");
    open.title = label.textContent;
  });
  const add = document.createElement("button");
  add.type = "button";
  add.className = "icon";
  add.id = "new-space";
  add.append(icon("plus"));
  localize(add, () => {
    add.title = t("cmdNewSpace");
    add.setAttribute("aria-label", add.title);
  });
  add.onclick = () => newSpace();
  $("foot").append(add);
}

// No saved host: the first screen has the whole window from the first frame, as before main() has run.
document.body.classList.toggle("onboarding", !loadHosts().length && !chosenHost());
// The tabs' place from the first frame too.
document.body.dataset.tabs = prefs.tabPlacement || "above";
startApp();
main();
