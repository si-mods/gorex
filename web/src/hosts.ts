// The hosts this page connects to, as the iOS app keeps them (ios/GoRex/Rex/Host.swift):
// a name, the gateway's URL and an icon, kept in this browser. The page is
// hosted on its own (docs/web-client.md), so it has no host of its own: the
// user adds each one, and each one must trust this page's origin. Hosts
// found on the tailnet by built-in Tailscale (tailnet.ts) are listed in a
// group of their own, and not kept.
import { localize, PlainKey, t } from "./i18n";
import type { LinkState } from "./settings";
import { joinHostURL, SharedGroup } from "./share";
import type { Tailnet, TailnetHost } from "./tailnet";

/** A host as the user added it, as built-in Tailscale found it (kind "tailnet"), or shared by Rex Go (kind "share"). */
export interface Host {
  id: string;
  name: string;
  /** The gateway, as http(s)://host[:port] or http://host[:port]/h/<id> when shared. */
  url: string;
  /** One of icons' names. */
  icon: string;
  kind?: "direct" | "tailnet" | "share";
}

/** The icons a host can take, as the iOS app offers them, from the repo's lucide set. */
export const icons: [name: string, label: PlainKey][] = [
  ["laptop", "hostIconLaptop"],
  ["monitor", "hostIconDesktop"],
  ["server", "hostIconServer"],
  ["globe", "hostIconGlobe"],
];

const listKey = "gorex.hosts";

/** A host's ID: random enough for one browser's list (crypto.randomUUID needs a secure context, which a dev server on a LAN address is not). */
const newID = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 10);

/** The hosts kept in this browser; none without storage. */
export function loadHosts(): Host[] {
  try {
    const list = JSON.parse(localStorage.getItem(listKey) ?? "[]");
    return Array.isArray(list)
      ? list.filter((h) => typeof h?.id === "string" && typeof h.url === "string" && typeof h.name === "string").map((h) => ({ icon: "laptop", ...h }))
      : [];
  } catch {
    return [];
  }
}

export function saveHosts(hosts: Host[]) {
  try {
    localStorage.setItem(listKey, JSON.stringify(hosts));
  } catch {
    // Without storage the list lasts until the page closes.
  }
}

/**
 * Reads a gateway's address as typed: http or https, a host and maybe a
 * port, nothing else. A bare host:port is taken as http://. Returns the
 * URL as http(s)://host[:port], or why it is not one.
 */
export function hostURL(typed: string): { url: string } | { error: string } {
  let s = typed.trim();
  if (!s) return { error: t("hostUrlEmpty") };
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) s = "http://" + s;
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return { error: t("hostUrlInvalid", { address: typed.trim() }) };
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return { error: t("hostUrlScheme") };
  if (u.username || u.password) return { error: t("hostUrlUser") };
  if ((u.pathname !== "/" && u.pathname !== "") || u.search || u.hash) return { error: t("hostUrlPath") };
  return { url: u.origin };
}

/** A host's name until the user gives one: the first label of a DNS name, else the whole host (an IP address). */
export function defaultName(url: string): string {
  const h = new URL(url).hostname;
  return /^[\d.]+$|^\[/.test(h) ? h : h.split(".")[0] || h;
}

/** The command that makes a host trust this page. */
export function allowCommand(): string {
  return `gorex rexapi web allow ${location.origin}`;
}

/**
 * Whether a browser may let an https page open plain ws:// to host:
 * Chrome's Local Network Access exempts loopback and private IP literals,
 * localhost and .local names from mixed content blocking (behind a
 * permission). Other browsers may still block them.
 */
function mayBeExempt(hostname: string): boolean {
  const h = hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (h === "localhost" || h.endsWith(".localhost") || h.endsWith(".local") || h === "::1") return true;
  const m = /^(\d+)\.(\d+)\.(\d+)\.(\d+)$/.exec(h);
  if (m) {
    const [a, b] = [Number(m[1]), Number(m[2])];
    return a === 127 || a === 10 || (a === 172 && b >= 16 && b < 32) || (a === 192 && b === 168) || (a === 169 && b === 254);
  }
  return /^f[cd]/.test(h) || h.startsWith("fe80:");
}

/**
 * What went wrong with a host, for the page to show. Its words are said as
 * they are read (problem() makes them getters), so that a problem shown
 * again after a change of language is in the new one.
 */
export interface Problem {
  readonly title: string;
  readonly text: string;
  /** A command to run on the host, shown to copy. */
  readonly command?: string;
  /** The technical why, behind a Learn more disclosure. */
  readonly details?: string;
}

/** A problem whose title, text and details are said in the page's language each time they are read. */
export function problem(title: () => string, text: () => string, command?: string, details?: () => string): Problem {
  return {
    get title() {
      return title();
    },
    get text() {
      return text();
    },
    command,
    get details() {
      return details?.();
    },
  };
}

/** A host that answers but does not trust this page: the command that makes it, and why, in plain words. */
export function untrusted(text: () => string): Problem {
  return problem(() => t("problemUntrustedTitle"), text, allowCommand(), () => t("problemUntrustedDetails", { origin: location.origin }));
}

/** Whether a host can never be reached from this page: plain http from an https page. */
export function mixedContent(url: string): Problem | undefined {
  const u = new URL(url);
  if (location.protocol !== "https:" || u.protocol !== "http:" || mayBeExempt(u.hostname)) return undefined;
  return mixedProblem;
}

const mixedProblem: Problem = problem(() => t("problemMixedTitle"), () => t("problemMixedText"));

/**
 * Tells why a host's control connection did not open. A WebSocket that
 * fails tells nothing, so this asks the gateway's help document twice: as
 * the page (CORS), which a gateway that trusts the page answers, and
 * without reading it (no-cors), which any server that is there answers.
 */
export async function diagnose(url: string): Promise<Problem> {
  const help = joinHostURL(url, "/api/help");
  const wait = () => AbortSignal.timeout(8000);
  try {
    const r = await fetch(help, { cache: "no-store", signal: wait() });
    if (r.status === 403) {
      let why = "";
      try {
        why = (await r.json()).error ?? "";
      } catch {}
      // The gateway's reason is its own words, in English.
      return problem(() => t("problemRefusedTitle"), () => why || t("problemRefusedText"));
    }
    const status = String(r.status);
    return problem(() => t("problemFailedTitle"), () => t("problemFailedText", { status }));
  } catch {
    // Not trusted, not there, or blocked by the browser.
  }
  try {
    await fetch(help, { mode: "no-cors", cache: "no-store", signal: wait() });
  } catch {
    const u = new URL(url);
    if (location.protocol === "https:" && u.protocol === "http:") return mixedProblem;
    return problem(() => t("problemUnreachableTitle"), () => t("problemUnreachableText", { url }));
  }
  return untrusted(() => t("problemUntrustedText", { url }));
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** A lucide icon of the repo, drawn in the text's colour; "tailscale" is Tailscale's own mark. */
export function icon(name: string): HTMLElement {
  if (name === "tailscale") return tailscaleMark();
  const i = el("i", "pane-icon");
  i.style.setProperty("--icon", `url(icons/${name}.svg)`);
  return i;
}

/**
 * Tailscale's mark, from its media kit (tailscale.com/press,
 * Tailscale_icon_*_rgb.svg): nine dots, the T solid and the rest at 40%,
 * in the text's colour. Inline, as one colour at two opacities is not a mask.
 */
function tailscaleMark(): HTMLElement {
  const i = el("i", "pane-icon ts-mark");
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 169 169");
  svg.setAttribute("aria-hidden", "true");
  const solid = new Set(["0,1", "1,1", "2,1", "1,2"]);
  for (let col = 0; col < 3; col++)
    for (let row = 0; row < 3; row++) {
      const c = document.createElementNS(ns, "circle");
      c.setAttribute("cx", String(30.5 + 54 * col));
      c.setAttribute("cy", String(30.5 + 54 * row));
      c.setAttribute("r", "18");
      c.setAttribute("fill", "currentColor");
      if (!solid.has(`${col},${row}`)) c.setAttribute("fill-opacity", "0.4");
      svg.append(c);
    }
  i.append(svg);
  return i;
}

/** The check column of a picker row: a check mark when chosen, else the same width empty. */
export function check(on: boolean): HTMLElement {
  const c = el("span", "theme-check");
  if (on) c.append(icon("check"));
  return c;
}

/** A command in a box, with a button that copies it. */
export function commandBox(command: string): HTMLElement {
  const box = el("div", "command");
  const code = el("code", "", command);
  const copy = el("button", "command-copy");
  copy.type = "button";
  localize(copy, (b) => (b.textContent = t("commonCopy")));
  copy.onclick = async () => {
    try {
      await navigator.clipboard.writeText(command);
      copy.textContent = t("commonCopied");
    } catch {
      getSelection()?.selectAllChildren(code);
      copy.textContent = t("commonSelected");
    }
    setTimeout(() => (copy.textContent = t("commonCopy")), 1500);
  };
  box.append(code, copy);
  return box;
}

/** A host's connection, as the lists show it: "none" while the page keeps none (the user disconnected it). */
export interface HostLink {
  host?: Host;
  state: LinkState;
  problem?: Problem;
}

/**
 * Above how many connected hosts the lists say that each one costs
 * (docs/web-multihost.md, Decisions, 5). Not a cap.
 */
export const manyHosts = 8;

/** A host's state, as its dot's words in the lists: "none" is a host the user disconnected. */
const linkWords: Record<LinkState, PlainKey> = {
  none: "hostStateOff", connecting: "linkConnecting", connected: "linkConnected", unreachable: "linkUnreachable",
};

/** What the host list needs of the page. */
export interface HostsApp {
  /** A host was chosen, to show: it connects if it is not; or the chosen one's address changed. */
  choose(h: Host | undefined): void;
  /** A host's connection now. */
  link(h: Host): HostLink;
  /** Connects a host the user disconnected, or a tailnet host never connected; the stage stays. */
  connect(h: Host): void;
  /** Disconnects a host until the user connects it again, across reloads (gorex.hostsOff). */
  disconnect(h: Host): void;
  /** How many hosts the page keeps connected: connected or trying to, all but those disconnected. */
  connections(): number;
  /** A host was removed from the list: its connection and its windows end. */
  removed(h: Host): void;
  /** The keyboard goes back to the terminal. */
  close(): void;
  /** The list changed: a host added, edited or removed. */
  changed(): void;
  /** Built-in Tailscale, whose hosts the list shows in their group. */
  tailnet: Tailnet;
  /** Opens the Tailscale sign-in guide. */
  guide(): void;
  /** Hosts shared by Rex Go over Personal Hotspot, when served by the phone. */
  share?(): SharedGroup | undefined;
}

/** A tailnet host's state, as its dot and words. */
export const tailnetStates: Record<TailnetHost["state"], [dot: string, text: PlainKey]> = {
  online: ["connected", "hostTailnetOnline"],
  offline: ["none", "hostTailnetOffline"],
  refused: ["unreachable", "hostTailnetRefused"],
};

/**
 * The host list: a button at the top of the sidebar with the chosen host,
 * the host of the shown window, and a glass panel to choose, add, edit
 * and remove hosts. The Settings dialog's Hosts section lists the same
 * hosts, with the same form. Every host stays connected until the user
 * disconnects it (its row's plug): choosing one shows it (main.ts).
 */
export class HostList {
  hosts = loadHosts();
  chosen?: Host;
  private button = el("button", "host-button");
  /** The app's name, in the button's place while there is no host. */
  private brand = el("div", "host-brand");
  private panel = el("div", "theme-panel host-panel");
  /** Whether the panel shows the list, which redraws as Tailscale's hosts change. */
  private listing = false;

  constructor(mount: HTMLElement, private app: HostsApp) {
    this.button.type = "button";
    this.button.setAttribute("aria-haspopup", "dialog");
    this.button.onclick = () => (this.panel.hidden ? this.open() : this.hide());
    this.panel.hidden = true;
    this.panel.setAttribute("role", "dialog");
    localize(this.panel, (p) => p.setAttribute("aria-label", t("hostList")));
    this.panel.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        this.hide();
      }
    });
    document.addEventListener("pointerdown", (e) => {
      if (!this.panel.hidden && !this.panel.contains(e.target as Node) && !this.button.contains(e.target as Node)) this.hide(false);
    });
    const logo = el("img");
    logo.src = "pwa/icon-192.png";
    logo.alt = "";
    logo.width = logo.height = 20;
    // GoRex is the app's name, not translated.
    this.brand.append(logo, el("span", "host-name", "GoRex"));
    mount.append(this.brand, this.button, this.panel);
    this.drawButton();
  }

  /** The language changed: the button, and the list if it shows; a form being filled says itself again (localize). */
  relocalize() {
    this.drawButton();
    this.refresh();
  }

  /** Adds the host of a quick link (?host=URL), or chooses the one with its address. */
  addLink(url: string) {
    const had = this.hosts.find((h) => h.url === url);
    if (had) {
      this.chosen = had;
    } else {
      const h: Host = { id: newID(), name: defaultName(url), url, icon: "laptop" };
      this.hosts.push(h);
      saveHosts(this.hosts);
      this.chosen = h;
    }
    this.drawButton();
  }

  /** The host the page shows now, as the button and the list's check name it; the page keeps its place. */
  select(h: Host | undefined) {
    if (this.chosen === h) return;
    this.chosen = h;
    this.refresh();
  }

  /**
   * The chosen host, as a button that opens the list. With no host at all
   * (none added, none found on Tailscale) the sidebar shows the app's name
   * in its place: the welcome over the stage offers the ways to add one.
   */
  private drawButton() {
    const h = this.chosen;
    const ts = this.app.tailnet;
    const none = !h && !this.hosts.length && !(ts.status() === "running" && ts.hosts.length);
    this.button.hidden = none;
    this.brand.hidden = !none;
    if (none) return;
    const chevron = el("span", "host-chevron");
    chevron.append(icon("chevron-down"));
    this.button.replaceChildren(icon(h?.icon ?? "laptop"), el("span", "host-name", h?.name ?? t("hostChoose")), chevron);
    this.button.title = h ? h.url : t("hostChoose");
  }

  open(form?: Host | "new") {
    this.panel.hidden = false;
    this.button.setAttribute("aria-expanded", "true");
    if (form) this.formIn(form === "new" ? undefined : form);
    else this.list();
  }

  /** Draws the button and the list again, if it shows, keeping the focus on the same row. */
  refresh() {
    this.drawButton();
    if (this.panel.hidden || !this.listing) return;
    const key = (document.activeElement as HTMLElement | null)?.dataset?.key;
    this.list(false);
    if (key) this.panel.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus();
  }

  hide(keys = true) {
    if (this.panel.hidden) return;
    this.panel.hidden = true;
    this.button.setAttribute("aria-expanded", "false");
    if (keys) this.app.close();
  }

  /** Chooses a host, which the page connects to. */
  choose(h: Host | undefined) {
    this.chosen = h;
    this.drawButton();
    this.app.choose(h);
  }

  private list(focus = true) {
    this.listing = true;
    const rows = el("div", "theme-list host-list");
    for (const h of this.hosts) {
      const row = el("div", "host-row");
      const pick = el("button", "theme-row");
      pick.type = "button";
      pick.dataset.key = h.id;
      const text = el("span", "host-text");
      text.append(el("span", "theme-name", h.name), el("span", "host-url", h.url));
      pick.append(icon(h.icon), text, this.dot(h));
      this.pickHost(pick, h);
      const edit = el("button", "pane-action");
      edit.type = "button";
      edit.dataset.key = h.id + ":edit";
      edit.title = t("commonEdit");
      edit.setAttribute("aria-label", t("hostEditName", { name: h.name }));
      edit.append(icon("pencil"));
      edit.onclick = () => this.formIn(h);
      row.append(pick, this.plug(h), edit);
      rows.append(row);
    }
    if (!this.hosts.length && !this.app.share?.()?.hosts.length) rows.append(el("div", "theme-empty", t("hostNone")));
    rows.append(...this.shareGroup());
    rows.append(...this.tailnetGroup());
    const add = el("button", "host-add", t("hostAdd"));
    add.type = "button";
    // Its key keeps the focus on it as the list redraws (refresh).
    add.dataset.key = "add";
    add.onclick = () => this.formIn(undefined);
    const many = this.manyNote();
    const note = many ? el("div", "host-note host-many", many) : undefined;
    if (note) note.dataset.key = "many";
    this.panel.replaceChildren(el("div", "host-title", t("hostList")), rows, ...(note ? [note] : []), add);
    if (focus) (rows.querySelector("button") ?? add).focus();
  }

  /**
   * A host row's click: a connected host shows its last window, one the
   * user disconnected connects and shows.
   */
  private pickHost(pick: HTMLButtonElement, h: Host) {
    const off = this.app.link(h).state === "none";
    pick.setAttribute("aria-current", String(h.id === this.chosen?.id));
    pick.onclick = () => {
      this.hide();
      if (off || h.id !== this.chosen?.id) this.choose(h);
    };
  }

  /**
   * A host's state as a dot, in the check's column: connected, connecting,
   * failed, or grey while the user disconnected it. Its words are its title
   * and its accessible name; it is never dimmed (DESIGN.md).
   */
  dot(h: Host): HTMLElement {
    const { state, problem } = this.app.link(h);
    const box = el("span", "theme-check");
    const d = el("i", `state-dot ${state}`);
    const words = t(linkWords[state]);
    d.title = state === "unreachable" && problem ? t("hostStateProblem", { state: words, problem: problem.title }) : words;
    d.setAttribute("role", "img");
    d.setAttribute("aria-label", d.title);
    box.append(d);
    return box;
  }

  /** A host's Connect or Disconnect button (Lucide plug, unplug). */
  plug(h: Host): HTMLButtonElement {
    const on = this.app.link(h).state !== "none";
    const b = el("button", "pane-action");
    b.type = "button";
    b.dataset.key = h.id + ":plug";
    b.title = t(on ? "hostDisconnect" : "hostConnect");
    b.setAttribute("aria-label", on ? t("hostDisconnectName", { name: h.name }) : t("settingsHostConnect", { name: h.name }));
    b.append(icon(on ? "unplug" : "plug"));
    b.onclick = () => (on ? this.app.disconnect(h) : this.app.connect(h));
    return b;
  }

  /** The note above manyHosts connected hosts: each connection costs. None below. */
  manyNote(): string | undefined {
    const n = this.app.connections();
    return n > manyHosts ? t("hostsManyNote", { n }) : undefined;
  }

  /**
   * The iPhone group: hosts shared by Rex Go over Personal Hotspot, when
   * the page is served by the phone. Not saved and not editable here.
   */
  private shareGroup(): HTMLElement[] {
    const g = this.app.share?.();
    if (!g || !g.hosts.length) return [];
    const head = el("div", "host-group");
    head.append(icon("laptop"), el("span", "host-group-name", g.phone));
    const out: HTMLElement[] = [head];
    for (const h of g.hosts) {
      const pick = el("button", "theme-row");
      pick.type = "button";
      pick.dataset.key = h.id;
      const text = el("span", "host-text");
      const url = el("span", "host-url", h.url);
      text.append(el("span", "theme-name", h.name), url);
      pick.append(icon(h.icon), text, this.dot(h));
      this.pickHost(pick, h);
      pick.title = h.url;
      const row = el("div", "host-row");
      row.append(pick, this.plug(h));
      out.push(row);
    }
    return out;
  }

  /**
   * The Tailscale group: the hosts built-in Tailscale found, each with its
   * state's dot, or the row that starts its sign-in guide, or what it is
   * doing while it finds none.
   */
  private tailnetGroup(): HTMLElement[] {
    const ts = this.app.tailnet;
    const status = ts.status();
    const head = el("div", "host-group");
    // Tailscale is a product's name, not translated.
    head.append(icon("tailscale"), el("span", "host-group-name", "Tailscale"));
    if (status !== "off") head.append(el("span", "host-group-state", ts.statusText()));
    const out: HTMLElement[] = [head];
    const guide = (label: string, glyph: string, key: string) => {
      const b = el("button", "theme-row host-guide");
      b.type = "button";
      b.dataset.key = key;
      b.append(icon(glyph), el("span", "theme-name", label));
      b.onclick = () => {
        this.hide(false);
        this.app.guide();
      };
      return b;
    };
    if (status !== "running") {
      out.push(guide(status === "off" ? t("hostTsAdd") : t("hostTsSignIn"), "log-in", "ts-guide"));
      return out;
    }
    for (const h of ts.hosts) {
      const pick = el("button", "theme-row");
      pick.type = "button";
      pick.dataset.key = h.id;
      const text = el("span", "host-text");
      const [dot, words] = tailnetStates[h.state];
      const line = el("span", "host-url host-state");
      // One dot a row: the connection's while the page keeps one, else Tailscale's word of the peer, in its line.
      const kept = this.app.link(h).state !== "none";
      if (!kept) line.append(el("i", `state-dot ${dot}`));
      line.append(el("span", "", h.state === "online" ? h.dns : t("hostTailnetLine", { state: t(words), address: h.dns })));
      text.append(el("span", "theme-name", h.name), line);
      pick.append(icon(h.icon), text, kept ? this.dot(h) : el("span", "theme-check"));
      this.pickHost(pick, h);
      pick.title = h.problem
        ? t("hostProblemTip", { title: h.problem.title, text: h.problem.text }) + (h.problem.command ? " " + h.problem.command : "")
        : h.url;
      const row = el("div", "host-row");
      row.append(pick, this.plug(h));
      out.push(row);
    }
    if (!ts.hosts.length) {
      out.push(ts.searching ? el("div", "theme-empty host-searching", t("hostSearching")) : guide(t("hostNoneFound"), "circle-alert", "ts-empty"));
    }
    return out;
  }

  /** The form in the panel: a saved host closes it, else the list shows again. */
  private formIn(h: Host | undefined) {
    this.listing = false;
    const form = this.form(h, (how) => (how === "save" ? this.hide() : this.list()));
    this.panel.replaceChildren(form);
    form.querySelector("input")?.focus();
  }

  /**
   * The form to add a host, or to edit or remove one. `done` tells how it
   * ended, after the list took the change.
   */
  form(h: Host | undefined, done: (how: "cancel" | "remove" | "save") => void): HTMLFormElement {
    const form = el("form", "host-form");
    // A div for the icons: a label would pass its clicks to the first one.
    // The form's words say themselves again after a change of language, keeping what was typed.
    const word = <E extends HTMLElement>(e: E, key: PlainKey) => localize(e, (x) => (x.textContent = t(key)));
    const field = (label: PlainKey, input: HTMLElement, tag: "label" | "div" = "label") => {
      const l = el(tag, "host-field");
      l.append(word(el("span"), label), input);
      return l;
    };
    const url = el("input", "theme-search");
    // An example address, as GoRex shows its local network one; not translated.
    url.placeholder = "192.168.1.20:7297";
    url.value = h?.url ?? "";
    url.required = true;
    url.autocomplete = "off";
    url.spellcheck = false;
    const name = el("input", "theme-search");
    localize(name, (n) => (n.placeholder = t("formName")));
    name.value = h?.name ?? "";
    // The icon: a segmented row of the icons, as iOS's picker.
    let chosenIcon = h?.icon ?? "laptop";
    const pick = el("div", "icon-pick");
    pick.setAttribute("role", "radiogroup");
    const iconButtons = icons.map(([v, label]) => {
      const b = el("button", "icon-choice");
      b.type = "button";
      localize(b, (b) => {
        b.title = t(label);
        b.setAttribute("aria-label", b.title);
      });
      b.setAttribute("role", "radio");
      b.append(icon(v));
      b.onclick = () => {
        chosenIcon = v;
        iconButtons.forEach((x, i) => x.setAttribute("aria-checked", String(icons[i][0] === v)));
      };
      b.setAttribute("aria-checked", String(v === chosenIcon));
      pick.append(b);
      return b;
    });
    const error = el("div", "host-error");
    const note = word(el("div", "host-note"), "formNote");
    const buttons = el("div", "host-buttons");
    const cancel = word(el("button", "host-add"), "commonCancel");
    cancel.type = "button";
    cancel.onclick = () => done("cancel");
    const save = word(el("button", "host-add primary"), h ? "formSave" : "formAdd");
    save.type = "submit";
    if (h) {
      const remove = word(el("button", "host-add danger"), "formRemove");
      remove.type = "button";
      remove.onclick = () => {
        if (!confirm(t("formRemoveAsk", { name: h.name }))) return;
        this.hosts = this.hosts.filter((x) => x.id !== h.id);
        saveHosts(this.hosts);
        // The page ends its connection, and moves the stage off it when it shows it.
        this.app.removed(h);
        done("remove");
      };
      buttons.append(remove);
    }
    buttons.append(cancel, save);
    form.onsubmit = (e) => {
      e.preventDefault();
      const r = hostURL(url.value);
      if ("error" in r) {
        error.textContent = r.error;
        url.focus();
        return;
      }
      const label = name.value.trim() || defaultName(r.url);
      const changed = !h || h.url !== r.url;
      const next: Host = { id: h?.id ?? newID(), name: label, url: r.url, icon: chosenIcon };
      this.hosts = h ? this.hosts.map((x) => (x.id === h.id ? next : x)) : [...this.hosts, next];
      saveHosts(this.hosts);
      done("save");
      if (!h || this.chosen?.id === h.id) {
        // A new host is chosen at once; the chosen one connects again when its address changed.
        if (changed) this.choose(next);
        else {
          this.chosen = next;
          this.drawButton();
        }
      }
      this.app.changed();
    };
    form.append(
      word(el("div", "host-title"), h ? "hostEdit" : "hostAdd"),
      field("formAddress", url),
      field("formName", name),
      field("formIcon", pick, "div"),
      error,
      note,
      buttons,
    );
    return form;
  }
}
