// The hosts this page connects to, as the iOS app keeps them (ios/GoRex/Rex/Host.swift):
// a name, the gateway's URL and an icon, kept in this browser. The page is
// hosted on its own (docs/web-client.md), so it has no host of its own: the
// user adds each one, and each one must trust this page's origin.

/** A host as the user added it. */
export interface Host {
  id: string;
  name: string;
  /** The gateway, as http(s)://host[:port]. */
  url: string;
  /** One of icons' names. */
  icon: string;
}

/** The icons a host can take, as the iOS app offers them, from the repo's lucide set. */
export const icons: [name: string, label: string][] = [
  ["laptop", "Laptop"],
  ["monitor", "Desktop"],
  ["server", "Server"],
  ["globe", "Globe"],
];

const listKey = "gorex.hosts";

/** A host's ID: random enough for one browser's list (crypto.randomUUID needs a secure context, which a dev server on a LAN address is not). */
const newID = () => Date.now().toString(36) + Math.random().toString(36).slice(2, 10);
const chosenKey = "gorex.host";

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

/** The host chosen last in this browser, by ID. */
export function chosenHost(): string {
  try {
    return localStorage.getItem(chosenKey) ?? "";
  } catch {
    return "";
  }
}

export function rememberHost(id: string) {
  try {
    localStorage.setItem(chosenKey, id);
  } catch {
    // As saveHosts.
  }
}

/**
 * Reads a gateway's address as typed: http or https, a host and maybe a
 * port, nothing else. A bare host:port is taken as http://. Returns the
 * URL as http(s)://host[:port], or why it is not one.
 */
export function hostURL(typed: string): { url: string } | { error: string } {
  let s = typed.trim();
  if (!s) return { error: "Enter the gateway's address, such as https://studio-gorex.tail1234.ts.net." };
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(s)) s = "http://" + s;
  let u: URL;
  try {
    u = new URL(s);
  } catch {
    return { error: `${typed.trim()} is not an address.` };
  }
  if (u.protocol !== "http:" && u.protocol !== "https:") return { error: "The address starts with http:// or https://." };
  if (u.username || u.password) return { error: "The address has no user name." };
  if ((u.pathname !== "/" && u.pathname !== "") || u.search || u.hash) return { error: "The address is the gateway's only, with no path." };
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

/** What went wrong with a host, for the page to show. */
export interface Problem {
  title: string;
  text: string;
  /** A command to run on the host, shown to copy. */
  command?: string;
}

/** Whether a host can never be reached from this page: plain http from an https page. */
export function mixedContent(url: string): Problem | undefined {
  const u = new URL(url);
  if (location.protocol !== "https:" || u.protocol !== "http:" || mayBeExempt(u.hostname)) return undefined;
  return mixedProblem;
}

const mixedProblem: Problem = {
  title: "This page can't reach a plain http:// host",
  text:
    "This page was opened over https, and browsers block its connections to http:// and ws:// addresses (mixed content). " +
    "Use the host's tailnet address (https://<machine>-gorex.<tailnet>.ts.net, from built-in Tailscale), " +
    "or open the web client from the local dev server (npm run dev in web/, http://127.0.0.1) to reach a local network gateway.",
};

/**
 * Tells why a host's control connection did not open. A WebSocket that
 * fails tells nothing, so this asks the gateway's help document twice: as
 * the page (CORS), which a gateway that trusts the page answers, and
 * without reading it (no-cors), which any server that is there answers.
 */
export async function diagnose(url: string): Promise<Problem> {
  const help = new URL("/api/help", url).href;
  const wait = () => AbortSignal.timeout(8000);
  try {
    const r = await fetch(help, { cache: "no-store", signal: wait() });
    if (r.status === 403) {
      let why = "";
      try {
        why = (await r.json()).error ?? "";
      } catch {}
      return { title: "The host refused this browser", text: why || "The gateway's access rule refused this device." };
    }
    return { title: "The host answered, but the connection failed", text: `The gateway answered ${r.status}. Reconnecting…` };
  } catch {
    // Not trusted, not there, or blocked by the browser.
  }
  try {
    await fetch(help, { mode: "no-cors", cache: "no-store", signal: wait() });
  } catch {
    const u = new URL(url);
    if (location.protocol === "https:" && u.protocol === "http:") return mixedProblem;
    return {
      title: "The host can't be reached",
      text:
        `Nothing answered at ${url}. Check that the address is right, that GoRex's gateway runs there ` +
        "(the local network listener or built-in Tailscale is on), and that this device can reach it. " +
        "A browser may also ask before a page reaches the local network; allow it.",
    };
  }
  return {
    title: "The host doesn't trust this page",
    text: `The gateway at ${url} answered, but refuses web pages from ${location.origin}. Run this on the host, then wait a moment:`,
    command: allowCommand(),
  };
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/** A lucide icon of the repo, drawn in the text's colour. */
export function icon(name: string): HTMLElement {
  const i = el("i", "pane-icon");
  i.style.setProperty("--icon", `url(icons/${name}.svg)`);
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
  const copy = el("button", "command-copy", "Copy");
  copy.type = "button";
  copy.onclick = async () => {
    try {
      await navigator.clipboard.writeText(command);
      copy.textContent = "Copied";
    } catch {
      getSelection()?.selectAllChildren(code);
      copy.textContent = "Select";
    }
    setTimeout(() => (copy.textContent = "Copy"), 1500);
  };
  box.append(code, copy);
  return box;
}

/** What the host list needs of the page. */
export interface HostsApp {
  /** The chosen host changed, or the chosen one's address did. */
  choose(h: Host | undefined): void;
  /** The keyboard goes back to the terminal. */
  close(): void;
  /** The list changed: a host added, edited or removed. */
  changed(): void;
}

/**
 * The host list: a button at the top of the sidebar with the chosen host,
 * and a glass panel to choose, add, edit and remove hosts. The Settings
 * dialog's Hosts section lists the same hosts, with the same form.
 */
export class HostList {
  hosts = loadHosts();
  chosen?: Host;
  private button = el("button", "host-button");
  private panel = el("div", "theme-panel host-panel");

  constructor(mount: HTMLElement, private app: HostsApp) {
    this.button.type = "button";
    this.button.setAttribute("aria-haspopup", "dialog");
    this.button.onclick = () => (this.panel.hidden ? this.open() : this.hide());
    this.panel.hidden = true;
    this.panel.setAttribute("role", "dialog");
    this.panel.setAttribute("aria-label", "Hosts");
    this.panel.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        this.hide();
      }
    });
    document.addEventListener("pointerdown", (e) => {
      if (!this.panel.hidden && !this.panel.contains(e.target as Node) && !this.button.contains(e.target as Node)) this.hide(false);
    });
    mount.append(this.button, this.panel);
    this.chosen = this.hosts.find((h) => h.id === chosenHost()) ?? this.hosts[0];
    this.drawButton();
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
    rememberHost(this.chosen.id);
    this.drawButton();
  }

  private drawButton() {
    const h = this.chosen;
    const chevron = el("span", "host-chevron");
    chevron.append(icon("chevron-down"));
    this.button.replaceChildren(icon(h?.icon ?? "plus"), el("span", "host-name", h?.name ?? "Add a Host"), chevron);
    this.button.title = h ? h.url : "Add a GoRex host";
  }

  open(form?: Host | "new") {
    this.panel.hidden = false;
    this.button.setAttribute("aria-expanded", "true");
    if (form) this.formIn(form === "new" ? undefined : form);
    else this.list();
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
    rememberHost(h?.id ?? "");
    this.drawButton();
    this.app.choose(h);
  }

  private list() {
    const rows = el("div", "theme-list host-list");
    for (const h of this.hosts) {
      const row = el("div", "host-row");
      const pick = el("button", "theme-row");
      pick.type = "button";
      const text = el("span", "host-text");
      text.append(el("span", "theme-name", h.name), el("span", "host-url", h.url));
      pick.append(icon(h.icon), text, check(h.id === this.chosen?.id));
      pick.onclick = () => {
        this.hide();
        if (h.id !== this.chosen?.id) this.choose(h);
      };
      const edit = el("button", "pane-action");
      edit.type = "button";
      edit.title = "Edit";
      edit.setAttribute("aria-label", `Edit ${h.name}`);
      edit.append(icon("pencil"));
      edit.onclick = () => this.formIn(h);
      row.append(pick, edit);
      rows.append(row);
    }
    if (!this.hosts.length) rows.append(el("div", "theme-empty", "No hosts yet."));
    const add = el("button", "host-add", "Add Host");
    add.type = "button";
    add.onclick = () => this.formIn(undefined);
    this.panel.replaceChildren(el("div", "host-title", "Hosts"), rows, add);
    (rows.querySelector("button") ?? add).focus();
  }

  /** The form in the panel: a saved host closes it, else the list shows again. */
  private formIn(h: Host | undefined) {
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
    const field = (label: string, input: HTMLElement, tag: "label" | "div" = "label") => {
      const l = el(tag, "host-field");
      l.append(el("span", "", label), input);
      return l;
    };
    const url = el("input", "theme-search");
    url.placeholder = "https://studio-gorex.tail1234.ts.net";
    url.value = h?.url ?? "";
    url.required = true;
    url.autocomplete = "off";
    url.spellcheck = false;
    const name = el("input", "theme-search");
    name.placeholder = "Name";
    name.value = h?.name ?? "";
    // The icon: a segmented row of the icons, as iOS's picker.
    let chosenIcon = h?.icon ?? "laptop";
    const pick = el("div", "icon-pick");
    pick.setAttribute("role", "radiogroup");
    const iconButtons = icons.map(([v, label]) => {
      const b = el("button", "icon-choice");
      b.type = "button";
      b.title = label;
      b.setAttribute("role", "radio");
      b.setAttribute("aria-label", label);
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
    const note = el("div", "host-note", "The host must trust this page. On the host, run:");
    const buttons = el("div", "host-buttons");
    const cancel = el("button", "host-add", "Cancel");
    cancel.type = "button";
    cancel.onclick = () => done("cancel");
    const save = el("button", "host-add primary", h ? "Save" : "Add");
    save.type = "submit";
    if (h) {
      const remove = el("button", "host-add danger", "Remove");
      remove.type = "button";
      remove.onclick = () => {
        if (!confirm(`Remove ${h.name}? Its sessions keep running on the host.`)) return;
        this.hosts = this.hosts.filter((x) => x.id !== h.id);
        saveHosts(this.hosts);
        if (this.chosen?.id === h.id) this.choose(this.hosts[0]);
        this.app.changed();
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
      el("div", "host-title", h ? "Edit Host" : "Add Host"),
      field("Address", url),
      field("Name", name),
      field("Icon", pick, "div"),
      error,
      note,
      commandBox(allowCommand()),
      buttons,
    );
    return form;
  }
}
