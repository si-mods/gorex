// GoRex's web client: the sessions every host's gateway serves, and a
// window's panes, live, over the same API the iOS app speaks. The page is
// hosted on its own; each host is one the user added (hosts.ts), or one
// built-in Tailscale found on the tailnet (tailnet.ts). Every host stays
// connected at once (docs/web-multihost.md), each in its HostConn.
import { onAppChange, startApp } from "./app";
import { Chunk } from "./chunks";
import { Ghostty } from "./ghostty";
import type { Guide } from "./guide";
import { mountCorner } from "./corner";
import { commandBox, Host, HostLink, HostList, hostURL, icon, loadHosts, mixedContent, Problem, problem as newProblem } from "./hosts";
import { HostConn, HostEvents, Session } from "./hostconn";
import { localize, Msg, msg, onLanguageChange, PlainKey, t } from "./i18n";
import { commandTitle, keysOf, shortcut } from "./keys";
import { CommandPalette, PaletteItem } from "./palette";
import { Asset, loader } from "./loader";
import { closeMenu, confirmDanger, DropZone, focusRename, MenuItem, openMenu, renameField, zoneSplit } from "./manage";
import { describe, TerminalView } from "./pane";
import { ensureLatin, initMaple, mapleFamily, onChunk } from "./maple";
import { fontFamily, paneHeaders, prefs } from "./prefs";
import { LoadState, ProgressView } from "./progress";
import { closeTailnet, isTailnetID, loadHostsOff, openTailnet, Place, saveHostsOff, savePlace, startPlace, tailnetOpened } from "./place";
import { programOf } from "./programs";
import { Control, directTransport } from "./rex";
import type { Settings } from "./settings";
import { hold, initSidebar, paneName, redraw, sidebar as drawSidebar, sidebarBlock, spaceName, windowName, windowTitle } from "./sidebar";
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
/** The hosts' connections, by host ID (hostconn.ts): every host the page keeps, connected at once. */
const conns = new Map<string, HostConn>();
/** The selection: the host whose window the stage shows, or whose notice it shows while it has none. */
let current: Host | undefined;
/**
 * Where the stage goes once its host has read its sessions: the place
 * kept (gorex.place) as the page starts, or a host chosen. The user's own
 * choice of a window meanwhile ends it: the page does not jump after it.
 */
let pending: Place | undefined;
/** Counts the hosts chosen, so that what waits for one chosen before ends. */
let generation = 0;
let hostList: HostList;
let settings: Settings | undefined;
/** Built-in Tailscale, off until the user turns it on; its engine loads only then. */
const tailnet = new Tailnet();
let guide: Guide | undefined;
/** The first screen, while no host is chosen. */
let welcome: Welcome | undefined;
/**
 * The direct hosts the user disconnected (gorex.hostsOff): the page keeps
 * no connection to them until the user connects them again, across reloads.
 */
const off = loadHostsOff();

/** Marks a direct host disconnected, or not, and keeps the list. */
function setOff(id: string, on: boolean) {
  if (off.has(id) === on) return;
  if (on) off.add(id);
  else off.delete(id);
  saveHostsOff(off);
}
/** The theme chosen in this browser, over the desktop's colours; none keeps the host's. */
let chosen: Theme | undefined;
/** Its entry, for the Settings dialog's previews. */
let chosenEntry: ThemeEntry | undefined;

/** The window this page shows. */
let shown: WindowView | undefined;

/**
 * How many windows stay live: the shown one and the ones shown before it,
 * across hosts (docs/web-multihost.md, Kept windows). A constant, not a
 * setting: each kept pane holds a stream, a terminal and a canvas.
 */
const keptWindows = 4;
/**
 * The windows shown before the shown one, live off the stage, most
 * recently shown first: keptWindows - 1 at most. Showing one again takes
 * it back as it was: no new ticket, no snapshot.
 */
let kept: WindowView[] = [];

/** Every pane live on the page: the shown window's and the kept windows'. */
function livePanes(): TerminalView[] {
  return [shown, ...kept].flatMap((w) => (w ? [...w.panes.values()] : []));
}

/** Ends a window, shown or kept; bytes its panes had typed and not yet written are lost, and the status line says so. */
function closeView(w: WindowView, disown: boolean) {
  const n = w.close(disown);
  if (!n) return;
  const s: Msg = () => t("paneUnsentDropped", { n });
  status(s);
  // Said again once the stage has changed: selecting another host says that host's line.
  queueMicrotask(() => status(s));
}

/** Ends the kept windows that f picks: their blocks were given back as they were kept. */
function evict(f: (w: WindowView) => boolean) {
  const out = kept.filter(f);
  if (!out.length) return;
  kept = kept.filter((w) => !f(w));
  for (const w of out) closeView(w, false);
}

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

/** A host's connection, for the host list and the Settings dialog: "none" while the page keeps none. */
function linkOf(h: Host | undefined): HostLink {
  const c = h && conns.get(h.id);
  return { host: c?.host ?? h, state: c?.state ?? "none", problem: c?.problem };
}

/** Applies a changed setting to the page and to every open pane, as the desktop's applyPrefs. */
function applyPrefs(what: "appearance" | "tabs" | "headers" | "animation" | "font" | "cursor" | "contrast", reset = false) {
  const panes = livePanes();
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
      if (prefs.font === mapleFamily) void ensureLatin().then(() => livePanes().forEach((p) => p.restyle()));
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

/** What a window of conn's and its panes need of the page. */
function windowHost(conn: HostConn): WindowHost {
  return {
    control: () => conn.control,
    live: () => (conn.live ? conn.control : undefined),
    get g() {
      return g;
    },
    chosen: () => chosen,
    status,
    scheme,
    titleOf: (b) => paneName(conn, b) || conn.titles.get(b) || t("sidebarTerminal"),
    programOf: (b) => programOf(conn.programs.get(b)),
    action: (p, name) => {
      shown?.setFocus(p.block, true);
      run(name);
    },
    focusBlock: (w, block) => {
      conn.control?.call("session.focus_block", { session_id: w.session, block_id: block }).catch((e) => console.warn("focus", e));
    },
    resizeSplit: (w, s, ratio) => {
      const before = borderLeaf(s.before, s.direction, true), after = borderLeaf(s.after, s.direction, false);
      conn.control
        ?.call("session.resize_pane", { session_id: w.session, direction: s.direction, before_block_id: before, after_block_id: after, ratio })
        .catch((e) => {
          status(() => t("statusMoveDividerFailed", { error: describe(e) }));
          conn.refresh(w.session, true);
        });
    },
    paneMenu: (w, block, x, y) => openMenu(x, y, paneMenu(w, block), () => shown?.focusKeys()),
    dropPane,
    edgeDrops: () => gateway.edgeMoves(conn),
  };
}

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
  edgeMoves: (conn: HostConn): boolean => !!conn.control?.features.has("gorex.move_window"),
  /** The call that moves a window to another session (moveTabToSpace), undefined while the gateway has none. */
  /** gorex.merge_windows: a window's row dropped on another's middle merges the two (the desktop's mergeTab). */
  mergeWindows: (conn: HostConn): boolean => !!conn.control?.features.has("gorex.merge_windows"),
  /** gorex.move_block_to_new_window: the pane menu's Move Pane to New Window (movePaneToNewTab). */
  blockToNewWindow: (conn: HostConn): boolean => !!conn.control?.features.has("gorex.move_block_to_new_window"),
  moveWindowToSpace(conn: HostConn): undefined | ((c: Control, session: string, window: string, to: string) => Promise<unknown>) {
    if (!conn.control?.features.has("gorex.move_window")) return undefined;
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
      for (const p of livePanes()) p.setChosen(chosen);
    };
    pick(theme);
    chooseTheme = pick;
    hostList = new HostList($("hosts"), {
      choose, close: () => shown?.focusKeys(), changed: hostsChanged, tailnet, guide: openGuide,
      link: linkOf, connect, disconnect, removed, connections: () => conns.size,
    });
    for (const what of ["tabs", "font"] as const) applyPrefs(what);
    onAppChange(() => settings?.appChanged());
    tailnet.onChange(tailnetChanged);
    window.addEventListener("pagehide", () => tailnet.dispose());
    if (prefs.tailscalePersist && prefs.tailscaleOn) void tailnet.turnOn();
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
  let place = startAt;
  const linked = new URLSearchParams(location.search).get("host");
  if (linked) {
    const r = hostURL(linked);
    if ("url" in r) {
      hostList.addLink(r.url);
      if (hostList.chosen) {
        // A host linked is asked for: it connects, though the user disconnected it before.
        setOff(hostList.chosen.id, false);
        place = { host: hostList.chosen.id };
      }
    }
    history.replaceState(null, "", location.pathname);
  }
  // Every host the page keeps connects now (Decisions, 1), at most opensAtOnce at a time (hostconn.ts).
  syncConns();
  startAtPlace(place);
}

/**
 * The stage as the page starts: the place kept, once its host has read
 * its sessions, else the first host's first window. A tailnet host's
 * place waits for built-in Tailscale to find it.
 */
function startAtPlace(p: Place | undefined) {
  // A host the user disconnected is no place: it stays disconnected.
  const h = p && hostList.hosts.find((x) => x.id === p.host && !off.has(x.id));
  if (p && (h || (isTailnetID(p.host) && tailnetOpened().has(p.host)))) {
    pending = p;
    select(h);
    if (h) showPending(conns.get(h.id)!);
    return;
  }
  const first = hostList.hosts.find((x) => !off.has(x.id));
  pending = first && { host: first.id };
  select(first);
}

/** Applies a theme chosen in Settings; set once the page has started. */
let chooseTheme: (t?: ThemeEntry) => void = () => {};

/** Runs f with the Settings dialog, which is made the first time, once its code is in. */
function withSettings(f: (s: Settings) => void) {
  settingsCode.use((m) => {
    settings ??= new m.Settings({
      hosts: hostList,
      link: linkOf,
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

/** Whether built-in Tailscale ran at its last change. */
let tailnetRan = false;

/**
 * Built-in Tailscale changed: the host list and Settings show it. A
 * tailnet host connected once connects when it is found. A tailnet host
 * whose connection fails while Tailscale is off waits with its problem,
 * and tries again at once when Tailscale runs again.
 */
function tailnetChanged() {
  syncConns();
  const running = tailnet.status() === "running";
  if (running && !tailnetRan) for (const c of conns.values()) if (c.host.kind === "tailnet" && c.waiting) c.poke();
  tailnetRan = running;
  onboarding();
  hostList.refresh();
  sidebar();
  settings?.update();
  // The welcome's Tailscale card follows its state, and so does a waiting tailnet place's notice.
  if (!current) {
    welcome?.update();
    if (pending && isTailnetID(pending.host)) drawNotice();
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

/** A waiting tailnet place's notices: Tailscale is not running, it is still looking, or it does not find the host. */
const tailnetWait: Problem = newProblem(() => t("placeTailnetTitle"), () => t("placeTailnetText"));
const tailnetLooking: Problem = newProblem(() => t("placeTailnetTitle"), () => t("placeTailnetSearchingText"));
const tailnetGone: Problem = newProblem(() => t("placeTailnetGoneTitle"), () => t("placeTailnetGoneText"));

/**
 * The notice of a place on tailnet host id, while it is not shown: its
 * connection's problem once it has one, else what Tailscale is doing.
 */
function tailnetPlace(id: string): [Problem | undefined, ...NoticeAction[]] {
  const c = conns.get(id);
  if (c) return c.state === "unreachable" && c.problem ? [c.problem, tailscaleSettings] : [undefined];
  if (tailnet.status() === "in-use-other-tab") {
    return [
      newProblem(() => t("tsStatusOtherTab"), () => t("tsHintOtherTab")),
      { label: "tsTakeOver", run: () => void tailnet.takeOver(), primary: true },
      tailscaleSettings,
    ];
  }
  if (tailnet.status() !== "running") return [tailnetWait, tailscaleSettings];
  return tailnet.searching ? [tailnetLooking] : [tailnetGone, tailscaleSettings];
}

/** Opens Settings at Tailscale, from a notice. */
const tailscaleSettings: NoticeAction = { label: "noticeTailscaleSettings", run: () => withSettings((s) => s.open("tailscale")) };

/**
 * Shows host h, chosen in the host list, the sidebar or the guide: its
 * last shown window, else its first once it has read its sessions. A host
 * the page keeps no connection to connects; the others stay connected.
 */
function choose(h: Host | undefined, keys = true) {
  generation++;
  closeMenu();
  if (!h) {
    hide();
    pending = undefined;
    select(undefined);
    return;
  }
  // A host chosen connects, though the user disconnected it before.
  if (h.kind === "tailnet") openTailnet(h.id);
  else setOff(h.id, false);
  const c = ensure(h);
  const last = c.lastWindow;
  if (last && windowOf(c, last.session, last.window)) return pick(c, last.session, last.window, keys);
  hide();
  // A host chosen is the user's choice, as a window is (pick).
  picked++;
  wantWindow = undefined;
  pending = { host: h.id };
  select(h);
  if (c.waiting) c.poke();
  showPending(c);
}

/** What each host's dot in the lists said last (state and problem), so that they redraw only when it changes. */
const dotSaid = new WeakMap<HostConn, string>();

/** What the status line said last of each host, to say again when it is selected. */
const said = new WeakMap<HostConn, Msg>();

/** Selects a host: the host button, the status line, the notice and the bar above follow it. */
function select(h: Host | undefined) {
  current = h;
  hostList?.select(h);
  onboarding();
  const c = currentConn();
  status(c ? (said.get(c) ?? "") : "");
  drawNotice();
  // The Settings dialog's host card follows the selected host.
  settings?.update();
  sidebar();
}

/**
 * The notice of the selected host only: its problem, else nothing; with
 * no host, the welcome, or a tailnet place that waits for Tailscale.
 */
function drawNotice() {
  const c = currentConn();
  if (!current) {
    if (pending && isTailnetID(pending.host)) notice(...tailnetPlace(pending.host));
    else if (!conns.size) showWelcome();
    else notice();
    return;
  }
  if (c?.state === "unreachable" && c.problem) {
    if (c.host.kind === "tailnet") notice(c.problem, tailscaleSettings);
    else notice(c.problem, { label: "hostEdit", run: () => hostList.open(c.host) });
  } else notice();
}

/** A host's transport: the browser's to its address, or the tailnet node's. */
function transportOf(h: Host) {
  return h.kind === "tailnet" ? tailnet.transport(h as TailnetHost) : directTransport(h.url);
}

/**
 * The connection to h, made and started if the page keeps none. An edit
 * of h's address makes a new one (its transport); of its name or icon,
 * only the sidebar changes.
 */
function ensure(h: Host): HostConn {
  let c = conns.get(h.id);
  if (c && c.host.url !== h.url) {
    drop(c);
    c = undefined;
  }
  if (c) {
    c.host = h;
    if (current?.id === h.id) current = h;
    return c;
  }
  c = new HostConn(h, transportOf(h), hostEvents, tailnet);
  conns.set(h.id, c);
  begin(c);
  return c;
}

/** Starts a host's connection; a host this page can never reach (mixed content) stays with its problem. */
function begin(c: HostConn) {
  // A tailnet host: no URL the browser opens, so no mixed content.
  const blocked = c.host.kind !== "tailnet" && mixedContent(c.host.url);
  if (blocked) c.block(blocked);
  else c.start();
}

/** Stops a host's connection and forgets it: its window, its rows, what waited for it. */
function drop(c: HostConn) {
  c.stop();
  conns.delete(c.host.id);
  if (shown?.conn === c) hide(false);
  evict((w) => w.conn === c);
  if (wantWindow?.host === c.host.id) wantWindow = undefined;
  if (pending?.host === c.host.id && !hostList.hosts.some((h) => h.id === c.host.id)) pending = undefined;
  if (hold.renaming?.host === c.host.id) hold.renaming = undefined;
  sidebar();
}

/** Tries a host again now: Retry in its problem's line. An attempt on its way goes on: no second one stacks on it. */
function retry(c: HostConn) {
  if (c.waiting) c.poke();
  else if (!c.trying) begin(c);
}

/**
 * Keeps a connection to every host the page keeps (Decisions, 1): each
 * host of the list, and each tailnet host connected once, when built-in
 * Tailscale finds it. A tailnet host stays connected while Tailscale is
 * off: its connection fails, waits and tries again. A host the user
 * disconnected has none (gorex.hostsOff; a tailnet one is no longer
 * connected once).
 */
function syncConns() {
  if (!hostList) return;
  for (const h of hostList.hosts) if (!off.has(h.id)) ensure(h);
  if (tailnet.status() === "running") {
    const opened = tailnetOpened();
    for (const h of tailnet.hosts) if (opened.has(h.id) || conns.has(h.id) || pending?.host === h.id) ensure(h);
  }
  for (const c of [...conns.values()]) {
    if (c.host.kind !== "tailnet" && (off.has(c.host.id) || !hostList.hosts.some((h) => h.id === c.host.id))) drop(c);
  }
}

/** The host list changed (a host added, edited, removed, connected or disconnected): connections, lists and notice follow. */
function hostsChanged() {
  // A host added connects, one removed stops, one whose address changed connects again; the others go on.
  syncConns();
  if (!current) drawNotice();
  settings?.update();
  hostList.refresh();
  // The host view lists the hosts.
  sidebar();
}

/** Connects a host the user disconnected, or a tailnet host found and never connected. The stage stays, unless it shows nothing (the welcome). */
function connect(h: Host) {
  if (!current) {
    choose(h, !busyFocus());
    hostsChanged();
    return;
  }
  if (h.kind === "tailnet") openTailnet(h.id);
  else setOff(h.id, false);
  ensure(h);
  hostsChanged();
}

/**
 * Disconnects a host until the user connects it again: its connection
 * stops, its control and data sockets close (its shown and kept windows
 * end), its rows and cached sessions go. The other hosts go on. The stage
 * moves off it when it shows it.
 */
function disconnect(h: Host) {
  if (h.kind === "tailnet") closeTailnet(h.id);
  else setOff(h.id, true);
  if (pending?.host === h.id) pending = undefined;
  const c = conns.get(h.id);
  if (c) drop(c);
  if (current?.id === h.id) elsewhere();
  hostsChanged();
}

/** A host removed from the list: its connection ends as it is disconnected, and it is no longer kept as disconnected. */
function removed(h: Host) {
  setOff(h.id, false);
  if (pending?.host === h.id) pending = undefined;
  syncConns();
  if (current?.id === h.id) elsewhere();
  hostsChanged();
}

/**
 * Moves the stage off a host disconnected or removed: to the window shown
 * last of another host (a kept one), else to the window shown last of
 * another connected host, else its first, else the welcome.
 */
function elsewhere() {
  const k = kept[0];
  if (k) return pick(k.conn, k.session, k.window, !busyFocus());
  const all = orderedConns();
  const c = all.find((c) => c.state === "connected" && c.lastWindow) ?? all.find((c) => c.state === "connected") ?? all[0];
  // The keyboard stays where the user is: the host panel or Settings, from which the host went.
  choose(c?.host, !busyFocus());
}

/** The hosts in the sidebar's order: the host list's, then the tailnet's by name, connected or found. */
function sidebarHosts(): Host[] {
  const ts = new Map<string, Host>();
  for (const c of conns.values()) if (c.host.kind === "tailnet") ts.set(c.host.id, c.host);
  if (tailnet.status() === "running") for (const h of tailnet.hosts) if (!ts.has(h.id)) ts.set(h.id, h);
  return [...(hostList?.hosts ?? []), ...[...ts.values()].sort((a, b) => a.name.localeCompare(b.name))];
}

/** The hosts' connections in the sidebar's order. */
function orderedConns(): HostConn[] {
  return sidebarHosts().map((h) => conns.get(h.id)).filter((c): c is HostConn => !!c);
}

/** The selected host's connection. */
function currentConn(): HostConn | undefined {
  return current && conns.get(current.id);
}

/** Shows what waits for c, once it has read its sessions: the place's window, else its first. */
function showPending(c: HostConn) {
  const p = pending;
  if (!p || p.host !== c.host.id || !c.live) return;
  pending = undefined;
  if (p.session && p.window && windowOf(c, p.session, p.window)) show(c, p.session, p.window, !busyFocus());
  else showAny(c, !busyFocus());
}

// A host that waits to try again tries now when the network or the page comes back.
function pokeAll() {
  for (const c of conns.values()) if (c.waiting) c.poke();
}
window.addEventListener("online", pokeAll);
document.addEventListener("visibilitychange", () => {
  if (document.visibilityState === "visible") pokeAll();
});

/**
 * What the hosts' connections tell the page: every host's rows follow
 * it; the notice and the status line only the selected host's.
 */
const hostEvents: HostEvents = {
  state(c) {
    // A window asked for before a drop is not shown after it: the user asks again.
    if (c.state !== "connected" && wantWindow?.host === c.host.id) wantWindow = undefined;
    // The host's kept windows end as it drops: their tickets were that connection's (the desktop's unkeepAll).
    if (c.state !== "connected") evict((w) => w.conn === c);
    // The notice is the selected host's, or a waiting place's.
    if (c === currentConn() || (!current && pending?.host === c.host.id)) drawNotice();
    // The host's heading, its dot and its rows follow its state.
    sidebar();
    // Its dot in the host list and Settings › Hosts, and the Settings card, only when what they show changed:
    // a retry every few seconds does not redraw them under the keyboard.
    const shows = `${c.state}\n${c.problem?.title ?? ""}`;
    if (dotSaid.get(c) === shows) return;
    dotSaid.set(c, shows);
    hostList?.refresh();
    settings?.update();
  },
  status(c, s) {
    said.set(c, s);
    if (c === currentConn()) status(s);
  },
  sessions(c, destroyed) {
    if (hold.renaming?.host === c.host.id && !c.sessions.has(hold.renaming.session)) hold.renaming = undefined;
    evict((w) => w.conn === c && !c.sessions.has(w.session));
    if (shown?.conn === c && !c.sessions.has(shown.session)) {
      hide(false);
      // The space closed: show another, as the desktop's window does (closeWorkspace).
      // Not the keyboard from a field (a rename elsewhere) or a menu: it commits on blur.
      if (destroyed) showAny(c, !busyFocus());
    }
    sidebar();
  },
  view(c, s) {
    const v = s.view;
    // A kept window follows its view as the shown one does; one gone from it ends.
    const of = (w: WindowView) => (v.windows ?? []).find((x: any) => x.window_id === w.window);
    evict((w) => w.conn === c && w.session === s.id && !of(w));
    for (const w of kept) if (w.conn === c && w.session === s.id) w.update(of(w), false);
    if (shown?.conn === c && shown.session === s.id) {
      const w = (v.windows ?? []).find((w: any) => w.window_id === shown!.window);
      if (w) {
        shown.update(w);
        // A pane's name may have changed.
        shown.retitle();
      } else {
        // The window closed: show the session's active one, as the desktop does.
        const next = v.active_window_id ?? v.windows?.[0]?.window_id;
        hide(false);
        if (next) show(c, s.id, next, false);
      }
    }
    sidebar();
    showWanted();
  },
  block(c, block) {
    sidebarBlock(c, block);
    if (shown?.conn === c) shown.retitle();
  },
  attached(c) {
    // A window kept after the connection came back and before this read has the old connection's streams too.
    for (const w of [shown, ...kept]) if (w?.conn === c) for (const p of w.panes.values()) p.start();
  },
  live(c) {
    for (const w of [shown, ...kept]) if (w?.conn === c) for (const p of w.panes.values()) p.resume();
    showPending(c);
  },
};

/** The sidebar of every host, at the next animation frame; force makes it now, while it holds still too. */
function sidebar(force = false) {
  if (force) drawSidebar(true);
  else redraw();
}

initSidebar({
  shown: () => shown,
  see,
  show: pick,
  newWindow,
  closeWindow,
  startRename,
  renamedSpace,
  renamedWindow,
  spaceMenu,
  windowMenu,
  movePaneToWindow,
  moveWindowTo,
  mergesWindows: (c) => gateway.mergeWindows(c),
  mergeWindows,
  spacesMenu,
  hosts: sidebarHosts,
  conn: (id) => conns.get(id),
  selected: currentConn,
  connect: (h) => hostList.choose(h),
  retry,
  edit: (c) => hostList.open(c.host),
});

/**
 * Marks a shown block's done and error records seen, as the desktop's pane
 * does when it shows, and tells the server, which tells every client. A
 * server that keeps it is told first, and its answer marks them, so that a
 * call that failed is made again as the block is seen next; against one
 * that doesn't, this page marks them itself.
 */
function see(conn: HostConn, block: string) {
  const w = shown, c = conn.control;
  if (w?.conn !== conn || !w.panes.has(block) || document.visibilityState !== "visible") return;
  const finished = (conn.statuses.get(block) ?? []).filter((r) => r.state === "done" || r.state === "error");
  if (!finished.length) return;
  // A record without a time is seen as any time is.
  const latest = Math.max(0, ...finished.map((r) => r.updated ?? 0));
  if (latest <= (conn.seen.get(block) ?? -1)) return;
  if (!latest || !c?.features.has("gorex.see_block")) return conn.raiseSeen(block, latest);
  if (conn.seeing.has(block)) return;
  conn.seeing.add(block);
  c.call("gorex.see_block", { session_id: w.session, block_id: block, seen_at: new Date(latest).toISOString() })
    .then(() => {
      conn.raiseSeen(block, latest);
      sidebarBlock(conn, block);
    }, () => {})
    .finally(() => conn.seeing.delete(block));
}

/** The space chip's menu, as the desktop's workspace popover: every space to show, then New Space and the space's own items. */
function spacesMenu(conn: HostConn, current: string): MenuItem[] {
  const spaces: MenuItem[] = [...conn.sessions.values()]
    .filter((o) => o.view)
    .map((o) => ({ label: spaceName(o), disabled: o.id === current, run: () => showSpace(conn, o.id) }));
  return [...spaces, "separator", { label: t("cmdNewSpace"), run: () => newSpace(conn) }, ...spaceMenu(conn, current)];
}

/** Shows a space's active window. */
function showSpace(conn: HostConn, session: string) {
  const v = conn.sessions.get(session)?.view;
  const next = v?.active_window_id ?? v?.windows?.[0]?.window_id;
  if (next) pick(conn, session, next, true);
}

/** A window of a session's view. */
function windowOf(conn: HostConn, session: string, window: string): any {
  return conn.sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
}

/** The windows of a session, in the host's order. */
function windowIDs(conn: HostConn, session: string): string[] {
  return (conn.sessions.get(session)?.view?.windows ?? []).map((w: any) => w.window_id);
}

/** A space heading's menu, as the desktop's spaceMenu (workspaceview.go): rename it, or close it. */
function spaceMenu(conn: HostConn, session: string): MenuItem[] {
  return [
    { label: t("cmdRenameSpace"), run: () => startRename(conn, session) },
    { label: t("cmdCloseSpace"), run: () => closeSpace(conn, session) },
  ];
}

/**
 * A window row's menu, as the desktop's tabMenu (tabs.go) in the
 * sidebar: rename it or give its name back, move it up or down or to
 * another space, open a window, close it or the others.
 */
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
  const move = gateway.moveWindowToSpace(conn);
  const others = [...conn.sessions.values()].filter((o) => o.id !== session && o.view);
  if (move && others.length) {
    items.push({
      label: t("windowMoveSpace"),
      submenu: others.map((o) => ({
        label: spaceName(o),
        run: () => {
          if (conn.control) move(conn.control, session, window, o.id).catch((e) => status(() => t("statusMoveWindowFailed", { error: describe(e) })));
        },
      })),
    });
  }
  items.push(
    "separator",
    { label: t("cmdNewWindow"), run: () => newWindow(conn, session) },
    { label: t("cmdCloseWindow"), run: () => closeWindow(conn, session, window) },
    { label: t("cmdCloseOtherWindows"), disabled: ids.length < 2, run: () => ids.filter((o) => o !== window).forEach((o) => closeWindow(conn, session, o)) },
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
  if (paneName(w.conn, block)) items.push({ label: t("paneResetName"), run: () => renamePane(w.conn, w.session, block, "") });
  items.push("separator");
  const others = (w.conn.sessions.get(w.session)?.view?.windows ?? []).filter((o: any) => o.window_id !== w.window);
  if (others.length) {
    items.push({
      label: t("paneMoveWindow"),
      submenu: others.map((o: any) => ({ label: windowTitle(w.conn, o), run: () => movePaneToWindow(w.conn, w.session, block, o.window_id) })),
    });
  }
  if (gateway.blockToNewWindow(w.conn) && w.blocks.length > 1) {
    items.push({ label: t("paneMoveNewWindow"), disabled: !!w.zoomedBlock, run: () => moveToNewWindow(w.conn, w.session, block) });
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
  const name = paneName(w.conn, block) ?? "";
  const f = renameField(name, w.conn.titles.get(block) || t("sidebarTerminal"), t("renamePaneLabel"), (v) => {
    p.editTitle(undefined);
    if (v !== undefined && v !== name) renamePane(w.conn, w.session, block, v);
    // The keyboard goes back to the pane, unless another field took it.
    if (shown === w && !busyFocus()) w.focusKeys();
  });
  p.editTitle(f);
  f.focus({ preventScroll: true });
  f.select();
}

/** Names a pane; an empty name gives it its terminal's title again (session.set_block_label). */
function renamePane(conn: HostConn, session: string, block: string, label: string) {
  conn.control
    ?.call("session.set_block_label", { session_id: session, block_id: block, label })
    .catch((e) => status(() => t("statusRenamePaneFailed", { error: describe(e) })));
}

/** Moves a pane into a window of its own, after its window, as the desktop's movePaneToNewTab. Its program goes on. */
function moveToNewWindow(conn: HostConn, session: string, block: string) {
  conn.control
    ?.call("gorex.move_block_to_new_window", { session_id: session, block_id: block })
    .catch((e) => status(() => t("statusMovePaneFailed", { error: describe(e) })));
}

/** Merges a window into another, side by side, and shows that one, as the desktop's mergeTab. The programs go on. */
function mergeWindows(conn: HostConn, session: string, window: string, into: string) {
  const since = picked;
  conn.control?.call("gorex.merge_windows", { session_id: session, window_id: window, into_window_id: into }).then(
    () => {
      want(since, { host: conn.host.id, session, window: into });
      showWanted();
    },
    (e) => status(() => t("statusMergeWindowsFailed", { error: describe(e) })),
  );
}

/** Opens the field that renames a space in its heading, or a window in its row. */
function startRename(conn: HostConn, session: string, window?: string) {
  if (!conn.sessions.has(session) || (window && !windowOf(conn, session, window))) return;
  closeMenu();
  hold.renaming = { host: conn.host.id, session, window };
  sidebar(true);
  focusRename($("list"));
}

/**
 * A space's new name, from its field: an empty one keeps the name it
 * has, as the desktop's renameWorkspace (session.set_label).
 */
function renamedSpace(conn: HostConn, session: string, name: string | undefined) {
  if (hold.renaming?.host === conn.host.id && hold.renaming.session === session && !hold.renaming.window) hold.renaming = undefined;
  const s = conn.sessions.get(session);
  if (s && name && name !== s.label) {
    const was = s.label;
    s.label = name;
    conn.control?.call("session.set_label", { session_id: session, label: name }).catch((e) => {
      if (s.label === name) s.label = was;
      status(() => t("statusRenameSpaceFailed", { error: describe(e) }));
      sidebar();
    });
  }
  sidebar();
  // The keyboard goes back to the pane, unless another field took it.
  if (!hold.renaming) shown?.focusKeys();
}

/**
 * A window's new name, from its field or Reset Name: an empty one gives
 * the window its terminal's title again, as the desktop's renameField
 * (session.set_window_label).
 */
function renamedWindow(conn: HostConn, session: string, window: string, name: string | undefined) {
  if (hold.renaming?.host === conn.host.id && hold.renaming.window === window) hold.renaming = undefined;
  const w = windowOf(conn, session, window);
  if (w && name !== undefined && name !== (windowName(w) ?? "")) {
    conn.control?.call("session.set_window_label", { session_id: session, window_id: window, label: name }).catch((e) =>
      status(() => t("statusRenameWindowFailed", { error: describe(e) })),
    );
  }
  sidebar();
  // The keyboard goes back to the pane, unless another field took it.
  if (!hold.renaming) shown?.focusKeys();
}

/**
 * Moves a window of a session to the place of another: before it, or
 * after it (session.move_window takes the window it goes before, none
 * for the end).
 */
function moveWindowTo(conn: HostConn, session: string, window: string, target: string, before: boolean) {
  const ids = windowIDs(conn, session).filter((id) => id !== window);
  const at = ids.indexOf(target);
  if (at < 0) return;
  const next = before ? target : ids[at + 1];
  const args: Record<string, string> = { session_id: session, window_id: window };
  if (next) args.before_window_id = next;
  conn.control?.call("session.move_window", args).catch((e) => status(() => t("statusMoveWindowFailed", { error: describe(e) })));
}

/**
 * Moves a pane into another window of its session, beside the pane that
 * has its focus, as the desktop's movePaneToTab (session.move_block, side
 * by side, after it). Its program goes on.
 */
function movePaneToWindow(conn: HostConn, session: string, block: string, window: string) {
  const w = windowOf(conn, session, window);
  const blocks = leavesOf(layoutOf(w));
  const anchor = blocks.includes(w?.focused_block_id) ? w.focused_block_id : blocks[0];
  if (!anchor) return;
  conn.control
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
  const c = w.conn.control;
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
async function newSpace(conn: HostConn | undefined) {
  const c = conn?.control;
  if (!conn || !c) return;
  const since = picked;
  try {
    const r = (await c.call("session.create", { initial_windows: [{ layout: { block: shell } }] })).payload;
    const window = r.initial_windows?.[0]?.window_id;
    if (!r.session_id || !window) return;
    want(since, { host: conn.host.id, session: r.session_id, window });
    if (!conn.sessions.has(r.session_id)) conn.sessions.set(r.session_id, { id: r.session_id, label: "" });
    await conn.refresh(r.session_id, true);
    showWanted();
  } catch (e) {
    status(() => t("statusNewSpaceFailed", { error: describe(e) }));
  }
}

/**
 * Closes a space once the user says so, as the desktop's Close Space
 * (workspaceConfirm): its terminals end (session.destroy).
 */
async function closeSpace(conn: HostConn, session: string) {
  const s = conn.sessions.get(session);
  if (!s) return;
  let n = 0;
  for (const w of s.view?.windows ?? []) for (const l of w.layers ?? []) n += l.blocks?.length ?? 0;
  const yes = await confirmDanger(t("spaceCloseTitle", { name: spaceName(s) }), t("spaceCloseBody", { n }), t("cmdCloseSpace"));
  shown?.focusKeys();
  if (yes) conn.control?.call("session.destroy", { session_id: session }).catch((e) => status(() => t("statusCloseSpaceFailed", { error: describe(e) })));
}

/** Whether the keyboard is in a field or a menu that is not a pane's own. */
function busyFocus(): boolean {
  const a = document.activeElement;
  return !!a?.closest?.("input, textarea:not(.pane-input), select, dialog, .context-menu, .theme-panel");
}

/** Shows a window when none shows: the active one of the first session that has one. */
function showAny(conn: HostConn, keys: boolean) {
  if (shown) return;
  for (const s of conn.sessions.values()) {
    const next = s.view?.active_window_id ?? s.view?.windows?.[0]?.window_id;
    if (next && windowOf(conn, s.id, next)) return show(conn, s.id, next, keys);
  }
}

/**
 * Shows a window the user chose (a row, a key, a host, a menu): a window
 * made here and not shown yet (wantWindow) no longer takes the stage when
 * its view comes, as the page does not jump after the user chose.
 */
function pick(conn: HostConn, session: string, window: string, keys: boolean) {
  picked++;
  wantWindow = undefined;
  show(conn, session, window, keys);
}

/** Counts the windows the user chose (pick): a window made before a choice and answered after it does not show. */
let picked = 0;

/** Asks for a window made here, to show when its view has it; since: picked when it was asked for. */
function want(since: number, w: typeof wantWindow) {
  if (picked === since) wantWindow = w;
}

/**
 * Shows a session's window, its panes live: a kept one as it was left, else
 * a new one, whose panes open their streams from a snapshot.
 */
function show(conn: HostConn, session: string, window: string, keys: boolean) {
  if (shown?.conn === conn && shown.session === session && shown.window === window) {
    if (keys) shown.focusKeys();
    return;
  }
  const w = conn.sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
  if (!w) return;
  const at = kept.findIndex((k) => k.conn === conn && k.session === session && k.window === window);
  const back = at >= 0 ? kept.splice(at, 1)[0] : undefined;
  hide();
  pending = undefined;
  if (back) {
    shown = back;
    back.unpark();
    back.update(w, false);
    back.retitle();
  } else {
    shown = new WindowView(conn, session, window, stage, windowHost(conn));
    shown.update(w);
  }
  if (keys) shown.focusKeys();
  conn.lastWindow = { session, window };
  const s = conn.sessions.get(session);
  if (s) s.lastWindow = window;
  savePlace({ host: conn.host.id, session, window });
  // A window of another host selects it: the host button, the notice and the status line follow.
  if (current?.id !== conn.host.id) select(conn.host);
  else sidebar();
}

/**
 * Takes the shown window off the stage. With keep, it is kept live
 * (park), first in kept; the one shown least recently past keptWindows
 * ends. A window of a host that is not connected is not kept: its streams
 * were the connection's that dropped. Without keep (the window or its
 * session is gone, its host is removed), it ends.
 */
function hide(keep = true) {
  const w = shown;
  if (!w) return;
  shown = undefined;
  if (!keep || w.conn.state !== "connected") return closeView(w, true);
  w.park();
  kept.unshift(w);
  for (const old of kept.splice(keptWindows - 1)) closeView(old, false);
}

/** A window made here, to show when the session's view has it. */
let wantWindow: { host: string; session: string; window: string } | undefined;

function showWanted() {
  const w = wantWindow;
  const conn = w && conns.get(w.host);
  if (w && conn?.sessions.get(w.session)?.view?.windows?.some((x: any) => x.window_id === w.window)) {
    wantWindow = undefined;
    show(conn, w.session, w.window, true);
  }
}

async function newWindow(conn: HostConn, session: string) {
  const since = picked;
  try {
    const id = await conn.newWindow(session);
    // Shown once a view has it: this read's, or one already on its way.
    want(since, id ? { host: conn.host.id, session, window: id } : undefined);
    await conn.refresh(session);
    showWanted();
  } catch (e) {
    status(() => t("statusNewWindowFailed", { error: describe(e) }));
  }
}

function closeWindow(conn: HostConn, session: string, window: string) {
  conn.closeWindow(session, window)?.catch((e) => status(() => t("statusCloseWindowFailed", { error: describe(e) })));
}

/** The spaces with a window, in the sidebar's order across the connected hosts. */
function spaces(): [HostConn, Session][] {
  return orderedConns()
    .filter((c) => c.state === "connected")
    .flatMap((c) => [...c.sessions.values()].filter((s) => s.view?.windows?.length).map((s) => [c, s] as [HostConn, Session]));
}

/** The windows in the sidebar's order across the connected hosts. */
function windows(): [HostConn, string, string][] {
  return spaces().flatMap(([c, s]) => s.view.windows.map((w: any) => [c, s.id, w.window_id] as [HostConn, string, string]));
}

/** The item after (step 1) or before (-1) the one at index at, round; the first or the last when none is. */
function step<T>(all: T[], at: number, by: 1 | -1): T {
  const n = all.length;
  return all[by === 1 ? (at + 1) % n : at < 0 ? n - 1 : (at - 1 + n) % n];
}

/** Runs one of the window's commands, from a shortcut or a pane's button. */
function paletteItems(): PaletteItem[] {
  const out: PaletteItem[] = [];
  const cmd = (id: string, action: () => void) => {
    out.push({ id: `cmd:${id}`, title: commandTitle(id), keys: keysOf(id) || undefined, run: action });
  };
  if (shown) {
    const p = shown.focus ? shown.panes.get(shown.focus) : undefined;
    if (p) {
      cmd("find", () => p.openFind());
      cmd("find-next", () => p.findNext());
      cmd("find-previous", () => p.findPrevious());
      cmd("split-right", () => run("split-right"));
      cmd("split-down", () => run("split-down"));
      cmd("zoom", () => run("zoom"));
      cmd("close", () => run("close"));
    }
    cmd("new-window", () => run("new-window"));
    cmd("rename-window", () => run("rename-window"));
    cmd("close-window", () => run("close-window"));
  }
  if (windows().length > 1) {
    cmd("next-window", () => run("next-window"));
    cmd("previous-window", () => run("previous-window"));
  }
  if (currentConn()) cmd("new-space", () => run("new-space"));
  if (spaces().length > 1) {
    cmd("next-space", () => run("next-space"));
    cmd("previous-space", () => run("previous-space"));
  }
  if (hostList) cmd("settings", () => withSettings((s) => s.toggle()));
  for (const [c, sId, wId] of windows()) {
    const s = c.sessions.get(sId);
    const w = s?.view?.windows?.find((x: any) => x.window_id === wId);
    if (!w) continue;
    out.push({
      id: `win:${c.host.id}:${sId}:${wId}`,
      title: windowTitle(c, w),
      detail: `${c.host.name} · ${spaceName(s!)}`,
      run: () => pick(c, sId, wId, true),
    });
  }
  return out;
}

const palette = new CommandPalette(paletteItems, () => shown?.focusKeys());

function run(name: string) {
  if (name === "command-palette") {
    palette.toggle();
    return;
  }
  const w = shown;
  if (name === "next-window" || name === "previous-window") {
    const all = windows();
    if (!all.length) return;
    const at = w ? all.findIndex(([c, s, id]) => c === w.conn && s === w.session && id === w.window) : -1;
    const [c, s, id] = step(all, at, name === "next-window" ? 1 : -1);
    pick(c, s, id, true);
    return;
  }
  if (name === "next-space" || name === "previous-space") {
    // The space's window shown last, as the desktop's space keys show a space as it was left.
    const all = spaces();
    if (!all.length) return;
    const at = w ? all.findIndex(([c, s]) => c === w.conn && s.id === w.session) : -1;
    const [c, s] = step(all, at, name === "next-space" ? 1 : -1);
    const ids = windowIDs(c, s.id);
    const id = s.lastWindow && ids.includes(s.lastWindow) ? s.lastWindow : ids.includes(s.view.active_window_id) ? s.view.active_window_id : ids[0];
    if (id) pick(c, s.id, id, true);
    return;
  }
  if (name === "new-space") {
    newSpace(currentConn());
    return;
  }
  if (!w) return;
  const block = w.focus;
  const c = w.conn.control;
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
      newWindow(w.conn, w.session);
      break;
    case "close-window":
      closeWindow(w.conn, w.session, w.window);
      break;
    case "rename-window":
      startRename(w.conn, w.session, w.window);
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
    case "find":
      if (block) w.panes.get(block)?.openFind();
      break;
    case "find-next":
      if (block) w.panes.get(block)?.findNext();
      break;
    case "find-previous":
      if (block) w.panes.get(block)?.findPrevious();
      break;
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
      windowHost(w.conn).resizeSplit(w, s, s.ratio);
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
    // Settings and Command Palette open/close from anywhere; the rest only from the window.
    if (name === "settings" && hostList) {
      e.preventDefault();
      e.stopPropagation();
      withSettings((s) => s.toggle());
      return;
    }
    if (name === "command-palette") {
      e.preventDefault();
      e.stopPropagation();
      palette.toggle();
      return;
    }
    // Inside the find bar, allow find / find-next / find-previous chords to work.
    if (t.closest?.(".find-bar") && (name === "find" || name === "find-next" || name === "find-previous")) {
      e.preventDefault();
      e.stopPropagation();
      run(name);
      return;
    }
    // The pane's own text field (pane.ts) holds its keys: they are the window's, not a form's.
    if (t.closest?.("input, textarea:not(.pane-input), select, .theme-panel, dialog, .context-menu, .palette")) return;
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
  livePanes().forEach((p) => {
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
  add.onclick = () => newSpace(currentConn());
  $("foot").append(add);
}

/** The place kept, after gorex.host of a page from before moved into it (place.ts). */
const startAt = startPlace(loadHosts().map((h) => h.id));
// No saved host: the first screen has the whole window from the first frame, as before main() has run.
document.body.classList.toggle("onboarding", !loadHosts().length && !startAt);
// The tabs' place from the first frame too.
document.body.dataset.tabs = prefs.tabPlacement || "above";
startApp();
main();
