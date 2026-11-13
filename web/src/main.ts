// GoRex's web client: the sessions a host's gateway serves, and a window's
// panes, live, over the same API the iOS app speaks. The page is hosted on
// its own; the host is one the user added (hosts.ts), or one built-in
// Tailscale found on the tailnet (tailnet.ts).
import { onAppChange, startApp } from "./app";
import { Chunk } from "./chunks";
import { Ghostty } from "./ghostty";
import type { Guide } from "./guide";
import { mountCorner } from "./corner";
import { chosenHost, commandBox, Host, HostList, hostURL, icon, loadHosts, mixedContent, Problem } from "./hosts";
import { HostConn, HostEvents } from "./hostconn";
import { localize, Msg, msg, onLanguageChange, PlainKey, t } from "./i18n";
import { shortcut } from "./keys";
import { Asset, loader } from "./loader";
import { closeMenu, confirmDanger, DropZone, focusRename, MenuItem, openMenu, renameField, zoneSplit } from "./manage";
import { describe } from "./pane";
import { ensureLatin, initMaple, mapleFamily, onChunk } from "./maple";
import { fontFamily, paneHeaders, prefs } from "./prefs";
import { LoadState, ProgressView } from "./progress";
import { programOf } from "./programs";
import { Control, directTransport } from "./rex";
import type { LinkState, Settings } from "./settings";
import { hold, initSidebar, paneName, sidebar as drawSidebar, sidebarBlock, spaceName, windowName, windowTitle } from "./sidebar";
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
/** The hosts' connections, by host ID (hostconn.ts). One at a time for now: choose() stops the others. */
const conns = new Map<string, HostConn>();
/** The host connected to, or connecting; one at a time. */
let current: Host | undefined;
/** Counts the hosts chosen, so that what waits for one chosen before ends. */
let generation = 0;
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
  for (const c of conns.values()) c.stop();
  conns.clear();
  hide();
  wantWindow = undefined;
  // What the sidebar held still for goes with the host.
  closeMenu();
  hold.renaming = undefined;
  hold.rowDragging = false;
  current = h;
  // Made before the sidebar is drawn: the sidebar draws the chosen host from it.
  const c = h && new HostConn(h, h.kind === "tailnet" ? tailnet.transport(h as TailnetHost) : directTransport(h.url), hostEvents, tailnet);
  if (c) conns.set(c.host.id, c);
  onboarding();
  sidebar();
  if (!h || !c) {
    status("");
    setLink("none");
    showWelcome();
    return;
  }
  // A tailnet host: no URL the browser opens, so no mixed content.
  const blocked = h.kind !== "tailnet" && mixedContent(h.url);
  if (blocked) {
    status(() => t("statusBlocked", { host: h.name }));
    setLink("unreachable", blocked);
    notice(blocked, { label: "hostEdit", run: () => hostList.open(h) });
    return;
  }
  notice();
  setLink("connecting");
  c.start();
}

/** The chosen host's connection. */
function currentConn(): HostConn | undefined {
  return current && conns.get(current.id);
}

/**
 * What the hosts' connections tell the page: the chosen host's shows. A
 * stopped host's answers still on their way change nothing.
 */
const hostEvents: HostEvents = {
  state(c) {
    if (c !== currentConn()) return;
    const h = c.host;
    if (c.state === "unreachable" && c.problem) {
      if (h.kind === "tailnet") notice(c.problem, { label: "noticeTailscaleSettings", run: () => withSettings((s) => s.open("tailscale")) });
      else notice(c.problem, { label: "hostEdit", run: () => hostList.open(h) });
    } else if (c.state === "connected") notice();
    setLink(c.state, c.problem);
    // The host's heading follows its state.
    sidebar();
  },
  status(c, s) {
    if (c === currentConn()) status(s);
  },
  sessions(c, destroyed) {
    if (c !== currentConn()) return;
    if (hold.renaming && !c.sessions.has(hold.renaming.session)) hold.renaming = undefined;
    if (shown?.conn === c && !c.sessions.has(shown.session)) {
      hide();
      // The space closed: show another, as the desktop's window does (closeWorkspace).
      // Not the keyboard from a field (a rename elsewhere) or a menu: it commits on blur.
      if (destroyed) showAny(c, !busyFocus());
    }
    sidebar();
  },
  view(c, s) {
    if (c !== currentConn()) return;
    const v = s.view;
    if (shown?.conn === c && shown.session === s.id) {
      const w = (v.windows ?? []).find((w: any) => w.window_id === shown!.window);
      if (w) {
        shown.update(w);
        // A pane's name may have changed.
        shown.retitle();
      } else {
        // The window closed: show the session's active one, as the desktop does.
        const next = v.active_window_id ?? v.windows?.[0]?.window_id;
        hide();
        if (next) show(c, s.id, next, false);
      }
    }
    sidebar();
    showWanted();
  },
  block(c, block) {
    if (c !== currentConn()) return;
    sidebarBlock(c, block);
    if (shown?.conn === c) shown.retitle();
  },
  attached(c) {
    if (shown?.conn === c) for (const p of shown.panes.values()) p.start();
  },
  live(c) {
    if (shown?.conn === c) for (const p of shown.panes.values()) p.resume();
  },
};

/** The sidebar, of the chosen host; force makes it while it holds still. */
function sidebar(force = false) {
  drawSidebar(currentConn(), force);
}

initSidebar({
  shown: () => shown,
  see,
  show,
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
  hosts: () => [...(hostList?.hosts ?? []), ...(tailnet.status() === "running" ? tailnet.hosts : [])],
  connect: (h) => hostList.choose(h),
  redraw: () => sidebar(),
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
      if (conn === currentConn()) sidebarBlock(conn, block);
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
  if (next) show(conn, session, next, true);
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
  conn.control?.call("gorex.merge_windows", { session_id: session, window_id: window, into_window_id: into }).then(
    () => {
      wantWindow = { host: conn.host.id, session, window: into };
      showWanted();
    },
    (e) => status(() => t("statusMergeWindowsFailed", { error: describe(e) })),
  );
}

/** Opens the field that renames a space in its heading, or a window in its row. */
function startRename(conn: HostConn, session: string, window?: string) {
  if (!conn.sessions.has(session) || (window && !windowOf(conn, session, window))) return;
  closeMenu();
  hold.renaming = { session, window };
  sidebar(true);
  focusRename($("list"));
}

/**
 * A space's new name, from its field: an empty one keeps the name it
 * has, as the desktop's renameWorkspace (session.set_label).
 */
function renamedSpace(conn: HostConn, session: string, name: string | undefined) {
  if (hold.renaming?.session === session && !hold.renaming.window) hold.renaming = undefined;
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
  if (hold.renaming?.window === window) hold.renaming = undefined;
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
  try {
    const r = (await c.call("session.create", { initial_windows: [{ layout: { block: shell } }] })).payload;
    const window = r.initial_windows?.[0]?.window_id;
    if (!r.session_id || !window) return;
    wantWindow = { host: conn.host.id, session: r.session_id, window };
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

/** Shows a session's window, its panes live. */
function show(conn: HostConn, session: string, window: string, keys: boolean) {
  if (shown?.conn === conn && shown.session === session && shown.window === window) {
    if (keys) shown.focusKeys();
    return;
  }
  const w = conn.sessions.get(session)?.view?.windows?.find((w: any) => w.window_id === window);
  if (!w) return;
  hide();
  shown = new WindowView(conn, session, window, stage, windowHost(conn));
  shown.update(w);
  if (keys) shown.focusKeys();
  sidebar();
}

function hide() {
  shown?.close();
  shown = undefined;
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
  try {
    const id = await conn.newWindow(session);
    // Shown once a view has it: this read's, or one already on its way.
    wantWindow = id ? { host: conn.host.id, session, window: id } : undefined;
    await conn.refresh(session);
    showWanted();
  } catch (e) {
    status(() => t("statusNewWindowFailed", { error: describe(e) }));
  }
}

function closeWindow(conn: HostConn, session: string, window: string) {
  conn.closeWindow(session, window)?.catch((e) => status(() => t("statusCloseWindowFailed", { error: describe(e) })));
}

/** The windows in the sidebar's order. */
function windows(conn: HostConn): [string, string][] {
  return [...conn.sessions.values()].flatMap((s) => (s.view?.windows ?? []).map((w: any) => [s.id, w.window_id] as [string, string]));
}

/** Runs one of the window's commands, from a shortcut or a pane's button. */
function run(name: string) {
  const w = shown;
  if (name === "next-window" || name === "previous-window") {
    const conn = currentConn();
    const all = conn ? windows(conn) : [];
    if (!conn || !all.length) return;
    const at = w ? all.findIndex(([, id]) => id === w.window) : -1;
    const n = all.length;
    const [s, id] = all[name === "next-window" ? (at + 1) % n : at < 0 ? n - 1 : (at - 1 + n) % n];
    show(conn, s, id, true);
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
  add.onclick = () => newSpace(currentConn());
  $("foot").append(add);
}

// No saved host: the first screen has the whole window from the first frame, as before main() has run.
document.body.classList.toggle("onboarding", !loadHosts().length && !chosenHost());
// The tabs' place from the first frame too.
document.body.dataset.tabs = prefs.tabPlacement || "above";
startApp();
main();
