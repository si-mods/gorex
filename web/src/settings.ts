// The Settings dialog, as the desktop's settings window (settingsview.go):
// the host's card and the sections at the left, each with its coloured
// tile, and the chosen section at the right, its settings in grouped cards
// (settingsGroup, settingRow). It keeps what a browser has: no glass, no
// Dock badge, no tab placement, no language and no updates. Each change is
// kept (prefs.ts) and applied to the open panes at once by the page.
import { allowCommand, commandBox, Host, HostList, icon, Problem } from "./hosts";
import { mac, shortcutGroups } from "./keys";
import {
  Appearance, CursorStyle, defaultFontSize, maxFontSize, minFontSize, prefs, Prefs, setPrefs, textSize, clearAll,
} from "./prefs";
import { formatBytes } from "./progress";
import { Status, Tailnet, validDeviceName } from "./tailnet";
import { desktopTheme, minimumContrast, Theme } from "./terminal";
import { ThemeEntry, themeColours, themePicker } from "./themes";

/** What build.mjs writes in: the client's commit and build date, and the Ghostty commit of the wasm. */
declare const __GOREX_COMMIT__: string;
declare const __GOREX_BUILT__: string;
declare const __GHOSTTY_COMMIT__: string;

/** The chosen host's connection, as the desktop's serverState tells the server's. */
export type LinkState = "none" | "connecting" | "connected" | "unreachable";

/** What the dialog needs of the page. */
export interface SettingsApp {
  hosts: HostList;
  /** The chosen host, its connection, and what is wrong with it. */
  link(): { host?: Host; state: LinkState; problem?: Problem };
  themes: ThemeEntry[];
  /** A theme chosen, or the host's colours (none). */
  chooseTheme(t?: ThemeEntry): void;
  /** Applies a changed setting to the page and its panes. */
  apply(what: "appearance" | "headers" | "font" | "cursor" | "contrast", reset?: boolean): void;
  /** The dialog closed: the keyboard goes back to the terminal. */
  closed(): void;
  /** Built-in Tailscale, and its sign-in guide. */
  tailnet: Tailnet;
  guide(): void;
}

type Section = "host" | "appearance" | "terminal" | "theme" | "keyboard" | "hosts" | "tailscale" | "about";

/** The sections, each with the colour of its tile, as the desktop's: macOS's system colours, a hue per kind of setting. */
const sections: [id: Section, glyph: string, tile: string, label: string][] = [
  ["appearance", "contrast", "rgb(0, 122, 255)", "Appearance"], // blue
  ["terminal", "square-terminal", "rgb(58, 58, 60)", "Terminal"], // graphite
  ["theme", "sparkles", "rgb(175, 82, 222)", "Theme"], // purple
  ["keyboard", "keyboard", "rgb(142, 142, 147)", "Keyboard"], // gray
  ["hosts", "monitor", "rgb(52, 199, 89)", "Hosts"], // green
  ["tailscale", "network", "rgb(88, 86, 214)", "Tailscale"], // indigo
  ["about", "info", "rgb(142, 142, 147)", "About"], // gray
];

/** The minimum contrast picker's values, as the desktop's contrastChoices: 0 follows the default, 1 turns the correction off. */
const contrastChoices = [0, 1, 2, 3, 4.5, 7];

function contrastName(v: number): string {
  if (v === 0) return `Default (${minimumContrast})`;
  if (v === 1) return "Off";
  if (v === 4.5) return "4.5 (WCAG AA)";
  if (v === 7) return "7 (WCAG AAA)";
  return String(v);
}

/** Monospaced families common on macOS, Windows and Linux, offered when installed. */
const knownFonts = [
  "SF Mono", "Menlo", "Monaco", "Consolas", "Cascadia Code", "Cascadia Mono", "Lucida Console", "Courier New",
  "Fira Code", "Fira Mono", "Source Code Pro", "Hack", "IBM Plex Mono", "Iosevka", "Inconsolata", "Roboto Mono",
  "Ubuntu Mono", "DejaVu Sans Mono", "Liberation Mono", "Noto Sans Mono", "Victor Mono", "Berkeley Mono",
];

/**
 * Whether a font family is installed. document.fonts.check answers true
 * for any family it has no face of, so this measures text in it against
 * two generic families instead: an installed one changes the width of one
 * of them.
 */
function installed(family: string): boolean {
  const c = document.createElement("canvas").getContext("2d")!;
  const text = "mmmmmmmmmmlli0O@";
  return ["monospace", "serif"].some((generic) => {
    c.font = `72px ${generic}`;
    const base = c.measureText(text).width;
    c.font = `72px "${family}", ${generic}`;
    return c.measureText(text).width !== base;
  });
}

let fontsFound: string[] | undefined;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(label: string, cls = "host-add"): HTMLButtonElement {
  const b = el("button", cls, label);
  b.type = "button";
  return b;
}

/** A group of rows on one card, with an optional label and note above it and a footnote under it, as settingsGroup. */
function group(parent: HTMLElement, label = "", note = "", foot = ""): HTMLElement {
  const g = el("section", "settings-group");
  if (label || note) {
    const head = el("div", "settings-group-head");
    if (label) head.append(el("div", "settings-group-label", label));
    if (note) head.append(el("div", "settings-hint", note));
    g.append(head);
  }
  const card = el("div", "settings-card");
  g.append(card);
  if (foot) g.append(el("div", "settings-hint settings-foot", foot));
  parent.append(g);
  return card;
}

/** A row of a group: its label and controls on one line, and its hint under them, as settingRow. */
function row(card: HTMLElement, label: string, hint = "", ...controls: HTMLElement[]): HTMLElement {
  const r = el("div", "settings-row");
  const line = el("div", "settings-line");
  line.append(el("span", "settings-label", label));
  if (controls.length) {
    const c = el("span", "settings-control");
    c.append(...controls);
    line.append(c);
  }
  r.append(line);
  if (hint) r.append(el("div", "settings-hint", hint));
  card.append(r);
  return r;
}

/** A segmented control of options, the chosen one in the pill. */
function segmented<T extends string>(label: string, options: [T, string][], value: T, set: (v: T) => void): HTMLElement {
  const seg = el("div", "theme-filter settings-seg");
  seg.setAttribute("role", "radiogroup");
  seg.setAttribute("aria-label", label);
  const buttons = options.map(([v, text]) => {
    const b = button(text, "");
    b.setAttribute("role", "radio");
    b.onclick = () => {
      buttons.forEach((x, i) => {
        x.classList.toggle("on", options[i][0] === v);
        x.setAttribute("aria-checked", String(options[i][0] === v));
      });
      set(v);
    };
    b.classList.toggle("on", v === value);
    b.setAttribute("aria-checked", String(v === value));
    seg.append(b);
    return b;
  });
  return seg;
}

/** A switch, as the desktop's: a checkbox drawn as one. */
function toggle(label: string, on: boolean, set: (on: boolean) => void): HTMLElement {
  const s = el("input", "settings-switch");
  s.type = "checkbox";
  s.setAttribute("role", "switch");
  s.setAttribute("aria-label", label);
  s.checked = on;
  s.onchange = () => set(s.checked);
  return s;
}

function select(label: string, options: [string, string][], value: string, set: (v: string) => void): HTMLSelectElement {
  const s = el("select", "settings-select");
  s.setAttribute("aria-label", label);
  for (const [v, text] of options) {
    const o = el("option", "", text);
    o.value = v;
    s.append(o);
  }
  s.value = value;
  s.onchange = () => set(s.value);
  return s;
}

/** An icon on a tile of a colour, white, as the desktop's sidebarItem has it. */
function tile(glyph: string, colour: string): HTMLElement {
  const t = el("span", "settings-tile");
  t.style.background = colour;
  t.append(icon(glyph));
  return t;
}

/** Tailscale's state, as a chip's words and dot. */
const tsLabels: Record<Status, string> = {
  off: "Off", loading: "Loading", "load-failed": "Didn't load", starting: "Starting", "needs-login": "Not signed in",
  "signing-in": "Signing in", "needs-approval": "Waiting for approval", running: "Connected", expired: "Expired",
  locked: "Tailnet Lock", error: "Error",
};
const chipState: Record<Status, LinkState> = {
  off: "none", loading: "connecting", "load-failed": "unreachable", starting: "connecting", "needs-login": "none",
  "signing-in": "connecting", "needs-approval": "connecting", running: "connected", expired: "unreachable",
  locked: "unreachable", error: "unreachable",
};

/** The words under Tailscale's status. */
function statusHint(t: Tailnet): string {
  switch (t.status()) {
    case "running":
      return t.statusText() + ".";
    case "load-failed":
      return t.load.state === "failed" ? t.load.message : "";
    case "needs-approval":
      return `An admin of your tailnet must approve ${t.name} in the admin console.`;
    case "expired":
      return "Sign in again to reach your tailnet's hosts.";
    case "locked":
      return "Your tailnet uses Tailnet Lock, which this version of the page can't join.";
    case "error":
      return t.error;
    case "needs-login":
      return "Sign in to list your GoRex hosts.";
    default:
      return "";
  }
}

const stateLabels: Record<LinkState, string> = {
  none: "No host", connecting: "Connecting…", connected: "Connected", unreachable: "Unreachable",
};

export class Settings {
  private dialog = el("dialog", "settings");
  private nav = el("nav", "settings-nav");
  private title = el("h1", "settings-title");
  private page = el("div", "settings-page");
  private section: Section = "appearance";
  /** The host the Hosts section edits, "new" for one to add, or none for the list. */
  private editing?: Host | "new";
  private picker?: ReturnType<typeof themePicker>;

  constructor(private app: SettingsApp) {
    this.dialog.setAttribute("aria-label", "Settings");
    const close = el("button", "pane-action settings-close");
    close.type = "button";
    close.title = "Close";
    close.setAttribute("aria-label", "Close Settings");
    close.append(icon("x"));
    close.onclick = () => this.close();
    const head = el("header", "settings-head");
    head.append(this.title, close);
    const main = el("div", "settings-main");
    const scroll = el("div", "settings-scroll");
    scroll.append(this.page);
    main.append(head, scroll);
    this.dialog.append(this.nav, main);
    // A click on the backdrop, outside the box, closes it.
    this.dialog.addEventListener("click", (e) => {
      if (e.target === this.dialog) {
        const r = this.dialog.getBoundingClientRect();
        if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) this.close();
      }
    });
    // Escape closes it (the dialog's cancel), and the keyboard goes back to the terminal.
    this.dialog.addEventListener("close", () => this.app.closed());
    document.body.append(this.dialog);
  }

  get isOpen() {
    return this.dialog.open;
  }

  open(section?: Section) {
    if (section) this.section = section;
    this.editing = undefined;
    if (!this.dialog.open) this.dialog.showModal();
    this.render();
    this.nav.querySelector<HTMLElement>("[aria-selected=true]")?.focus();
  }

  close() {
    if (this.dialog.open) this.dialog.close();
  }

  toggle() {
    if (this.dialog.open) this.close();
    else this.open();
  }

  /** The host or its connection changed: the card, and the pages that show it, again. A form being filled stays. */
  update() {
    if (!this.dialog.open) return;
    this.drawNav();
    if (this.section === "host" || (this.section === "hosts" && !this.editing)) this.drawPage();
    // The Tailscale page follows the node, but not under a name being typed.
    const typing = document.activeElement instanceof HTMLInputElement && this.page.contains(document.activeElement);
    if (this.section === "tailscale" && !typing) {
      const key = (document.activeElement as HTMLElement | null)?.dataset?.key;
      this.drawPage();
      if (key) this.page.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus();
    }
  }

  private show(s: Section, editing?: Host | "new") {
    this.section = s;
    this.editing = editing;
    this.render();
  }

  private render() {
    this.drawNav();
    this.drawPage();
  }

  private drawNav() {
    const had = document.activeElement instanceof HTMLElement && this.nav.contains(document.activeElement) ? document.activeElement.dataset.section : undefined;
    const items: HTMLElement[] = [this.hostCard()];
    for (const [id, glyph, colour, label] of sections) {
      const b = el("button", "settings-item");
      b.type = "button";
      b.dataset.section = id;
      b.setAttribute("role", "tab");
      b.setAttribute("aria-selected", String(this.section === id));
      b.append(tile(glyph, colour), el("span", "", label));
      b.onclick = () => this.show(id);
      items.push(b);
    }
    this.nav.replaceChildren(...items);
    if (had) this.nav.querySelector<HTMLElement>(`[data-section="${had}"]`)?.focus();
  }

  /** The host's card atop the sections, as the desktop's serverCard: its icon, its name and its state's dot. */
  private hostCard(): HTMLElement {
    const { host, state } = this.app.link();
    const b = el("button", "settings-host");
    b.type = "button";
    b.dataset.section = "host";
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(this.section === "host"));
    b.setAttribute("aria-label", `${host?.name ?? "Host"}, ${stateLabels[state]}`);
    const glyph = el("span", "settings-host-icon");
    glyph.append(icon(host?.icon ?? "server"));
    const text = el("span", "settings-host-text");
    const line = el("span", "settings-host-state");
    line.append(el("i", `state-dot ${state}`), el("span", "", stateLabels[state]));
    text.append(el("span", "settings-host-name", host?.name ?? "No Host"), line);
    b.append(glyph, text);
    b.onclick = () => this.show("host");
    return b;
  }

  private drawPage() {
    const title = this.section === "host" ? "Host" : sections.find(([id]) => id === this.section)![3];
    this.title.textContent = title;
    this.dialog.setAttribute("aria-label", `Settings, ${title}`);
    this.page.replaceChildren();
    switch (this.section) {
      case "host":
        return this.hostPage();
      case "appearance":
        return this.appearancePage();
      case "terminal":
        return this.terminalPage();
      case "theme":
        return this.themePage();
      case "keyboard":
        return this.keyboardPage();
      case "hosts":
        return this.hostsPage();
      case "tailscale":
        return this.tailscalePage();
      case "about":
        return this.aboutPage();
    }
  }

  /** The host's page: the chosen host and its connection, as the desktop's server page, and how it trusts this page. */
  private hostPage() {
    const { host, state, problem } = this.app.link();
    const card = group(this.page);
    const top = el("div", "settings-row settings-hero");
    const glyph = el("span", "settings-hero-icon");
    glyph.append(icon(host?.icon ?? "server"));
    const text = el("span", "settings-hero-text");
    text.append(el("span", "settings-hero-name", host?.name ?? "No Host"), el("span", "settings-hint", host?.url ?? "Add a GoRex host to connect to."));
    const chip = el("span", `settings-chip ${state}`);
    chip.append(el("i", `state-dot ${state}`), el("span", "", stateLabels[state]));
    top.append(glyph, text, chip);
    card.append(top);
    if (host) row(card, "Address", "", el("span", "settings-value", host.url));
    row(card, "This page", "", el("span", "settings-value", location.origin));
    if (problem) row(card, problem.title, problem.text);
    const actions = el("div", "host-buttons");
    if (host) {
      const edit = button("Edit Host");
      edit.onclick = () => this.show("hosts", host);
      actions.append(edit);
    }
    const all = button(host ? "Choose Another Host" : "Add Host", host ? "host-add" : "host-add primary");
    all.onclick = () => this.show("hosts", host ? undefined : "new");
    actions.append(all);
    const buttons = el("div", "settings-row");
    buttons.append(actions);
    card.append(buttons);

    const trust = group(
      this.page, "Trust this page",
      "A host's gateway answers only pages it trusts. On each host, run once:",
      "The command adds this page's origin to the host's list (gorex rexapi web list shows it; remove takes it out).",
    );
    const r = el("div", "settings-row");
    r.append(commandBox(allowCommand()));
    trust.append(r);
  }

  private appearancePage() {
    const win = group(this.page);
    row(
      win, "Window", "System follows the terminal's background: light for a light theme, dark for a dark one.",
      segmented<Appearance>("Appearance", [["", "System"], ["light", "Light"], ["dark", "Dark"]], prefs.appearance, (v) => {
        setPrefs({ appearance: v });
        this.app.apply("appearance");
      }),
    );
    row(
      win, "Show pane headers", "Each pane's title, program and buttons above its terminal.",
      toggle("Show pane headers", !prefs.hidePaneHeaders, (on) => {
        setPrefs({ hidePaneHeaders: !on });
        this.app.apply("headers");
      }),
    );

    const text = group(this.page);
    const value = el("span", "settings-value", "");
    const minus = el("button", "pane-action settings-step");
    const plus = el("button", "pane-action settings-step");
    const reset = button("Reset");
    minus.type = plus.type = "button";
    minus.setAttribute("aria-label", "Smaller text");
    plus.setAttribute("aria-label", "Larger text");
    minus.append(icon("minus"));
    plus.append(icon("plus"));
    const show = () => {
      const size = textSize();
      value.textContent = `${+size.toFixed(1)} px`;
      minus.disabled = size <= minFontSize;
      plus.disabled = size >= maxFontSize;
      reset.disabled = prefs.fontSize === 0;
    };
    const set = (size: number) => {
      setPrefs({ fontSize: size && Math.min(maxFontSize, Math.max(minFontSize, Math.round(size * 10) / 10)) });
      show();
      this.app.apply("font");
    };
    minus.onclick = () => set(textSize() - 1);
    plus.onclick = () => set(textSize() + 1);
    reset.onclick = () => set(0);
    show();
    const stepper = el("span", "settings-stepper");
    stepper.append(minus, plus);
    row(text, "Text size", `The terminals' text, in CSS pixels: ${defaultFontSize}, the desktop's default, until set.`, value, stepper, reset);
  }

  private terminalPage() {
    fontsFound ??= knownFonts.filter(installed);
    const fonts: [string, string][] = [["", "JetBrains Mono (default)"], ...fontsFound.map((f): [string, string] => [f, f])];
    if (prefs.font && !fontsFound.includes(prefs.font)) fonts.push([prefs.font, prefs.font]);
    const font = group(this.page);
    const preview = el("div", "settings-preview");
    const drawPreview = () => {
      const t: Theme = this.previewTheme();
      const rgb = (c?: number[]) => (c ? `rgb(${c.join(",")})` : "");
      preview.style.background = rgb(t.background);
      preview.style.color = rgb(t.foreground);
      preview.style.fontSize = `${textSize()}px`;
    };
    preview.append(el("div", "", "The quick brown fox jumps over the lazy dog"), el("div", "", "0123456789 {}[]() => != <= ~/gorex $"));
    drawPreview();
    row(
      font, "Font", "Monospaced families installed on this device. JetBrains Mono comes with the page.",
      select("Font", fonts, prefs.font, (v) => {
        setPrefs({ font: v });
        this.app.apply("font");
        drawPreview();
      }),
    );
    const p = el("div", "settings-row");
    p.append(preview);
    font.append(p);

    const cursor = group(this.page, "", "", "Until a program sets its own cursor. Not set, the host's cursor stays.");
    row(
      cursor, "Cursor style", "",
      segmented<CursorStyle>("Cursor style", [["block", "Block"], ["bar", "Bar"], ["underline", "Underline"]], prefs.cursor ?? "block", (v) => {
        setPrefs({ cursor: v });
        this.app.apply("cursor");
      }),
    );
    row(
      cursor, "Cursor blink", "",
      toggle("Cursor blink", prefs.cursorBlink ?? true, (on) => {
        setPrefs({ cursorBlink: on });
        this.app.apply("cursor");
      }),
    );

    row(
      group(this.page), "Copy on selection", "Copies the text you select to the clipboard.",
      toggle("Copy on selection", prefs.copyOnSelect, (on) => setPrefs({ copyOnSelect: on })),
    );

    const values = contrastChoices.includes(prefs.minimumContrast) ? contrastChoices : [...contrastChoices, prefs.minimumContrast];
    row(
      group(this.page), "Minimum contrast",
      "Lightens or darkens text too close to its background, colors programs set (ANSI, 256 and RGB) too. The hue stays, " +
        "so errors and warnings keep apart; box drawing keeps its colors. Off shows programs' colors as they are.",
      select("Minimum contrast", values.map((v) => [String(v), contrastName(v)]), String(prefs.minimumContrast), (v) => {
        setPrefs({ minimumContrast: Number(v) });
        this.app.apply("contrast");
      }),
    );

    const reset = button("Reset to Defaults");
    reset.onclick = () => {
      const was: Partial<Prefs> = { font: "", cursor: undefined, cursorBlink: undefined, copyOnSelect: false, minimumContrast: 0 };
      setPrefs(was);
      this.app.apply("font");
      this.app.apply("contrast");
      this.app.apply("cursor", true);
      this.drawPage();
    };
    row(group(this.page), "Forgets the font, cursor, copy on selection and contrast chosen here, so the defaults apply.", "", reset);
  }

  /** The chosen theme's colours, else GoRex's, for the font's preview. */
  private previewTheme(): Theme {
    const t = this.app.themes.find((x) => x.name === prefs.theme);
    return t ? themeColours(t) : desktopTheme();
  }

  private themePage() {
    if (!this.app.themes.length) {
      row(group(this.page), "The themes could not be loaded.", "The terminals keep GoRex's colors.");
      return;
    }
    this.picker ??= themePicker(this.app.themes, (t) => this.app.chooseTheme(t));
    const card = group(this.page, "", "", "A theme goes over the host's colors; Host Colors keeps what the host sends.");
    card.classList.add("settings-themes");
    card.append(this.picker.el);
    this.picker.show();
  }

  private keyboardPage() {
    const note = el("div", "settings-hint settings-note",
      "The web client's shortcuts, which come before the terminal's keys. A browser keeps " +
        (mac ? "⌘W, ⌘T, ⌘N and ⌃Tab" : "Ctrl+W, Ctrl+T, Ctrl+N and Ctrl+Tab") +
        " for itself, so their commands take " + (mac ? "⌥⇧" : "Alt+Shift") + " chords here.");
    this.page.append(note);
    for (const g of shortcutGroups()) {
      const card = group(this.page, g.label);
      for (const [title, keys] of g.rows) row(card, title, "", el("kbd", "settings-keys", keys)).classList.add("compact");
    }
  }

  private hostsPage() {
    const hosts = this.app.hosts;
    if (this.editing) {
      const h = this.editing === "new" ? undefined : this.editing;
      // The form has its title, as in the host list's panel.
      const card = group(this.page);
      const r = el("div", "settings-row");
      const form = hosts.form(h, () => this.show("hosts"));
      r.append(form);
      card.append(r);
      form.querySelector("input")?.focus();
      return;
    }
    const card = group(this.page, "", "The GoRex hosts this browser connects to. Only the chosen one is connected.");
    for (const h of hosts.hosts) {
      const r = el("div", "settings-row settings-host-row");
      const pick = el("button", "settings-host-pick");
      pick.type = "button";
      const glyph = el("span", "settings-host-icon small");
      glyph.append(icon(h.icon));
      const text = el("span", "settings-host-text");
      text.append(el("span", "settings-host-name", h.name), el("span", "host-url", h.url));
      const chosen = h.id === hosts.chosen?.id;
      pick.append(glyph, text);
      if (chosen) {
        const c = el("span", "theme-check");
        c.append(icon("check"));
        pick.append(c);
      }
      pick.setAttribute("aria-current", String(chosen));
      pick.title = chosen ? "Connected to this host" : `Connect to ${h.name}`;
      pick.onclick = () => {
        if (!chosen) hosts.choose(h);
        this.update();
      };
      const edit = el("button", "pane-action");
      edit.type = "button";
      edit.title = "Edit";
      edit.setAttribute("aria-label", `Edit ${h.name}`);
      edit.append(icon("pencil"));
      edit.onclick = () => this.show("hosts", h);
      r.append(pick, edit);
      card.append(r);
    }
    if (!hosts.hosts.length) row(card, "No hosts yet.", "Add the address of a GoRex host's gateway.");
    const add = button("Add Host", "host-add primary");
    const plus = icon("plus");
    add.prepend(plus);
    add.onclick = () => this.show("hosts", "new");
    const actions = el("div", "settings-row");
    actions.append(add);
    card.append(actions);
  }

  /**
   * Built-in Tailscale (docs/web-tailscale.md, Signing in): on or off, its
   * state and account, this device's name, the hosts it found, and how
   * long the sign-in lasts in this version.
   */
  private tailscalePage() {
    const t = this.app.tailnet;
    const status = t.status();
    const card = group(this.page);
    const top = el("div", "settings-row settings-hero");
    const glyph = el("span", "settings-hero-icon");
    glyph.append(icon("network"));
    const text = el("span", "settings-hero-text");
    const sub =
      status === "off" ? "Reach your GoRex hosts through Tailscale, with no app on this device."
      : status === "running" ? `This device: ${t.name}${t.map?.self.address ? ` · ${t.map.self.address}` : ""}`
      : t.statusText();
    text.append(el("span", "settings-hero-name", "Built-in Tailscale"), el("span", "settings-hint", sub));
    const sw = toggle("Built-in Tailscale", status !== "off", (on) => {
      if (on) {
        t.turnOn();
        this.app.guide();
      } else {
        t.turnOff();
      }
    });
    sw.dataset.key = "switch";
    top.append(glyph, text, sw);
    card.append(top);
    if (status !== "off") {
      const chip = el("span", `settings-chip ${chipState[status]}`);
      chip.append(el("i", `state-dot ${chipState[status]}`), el("span", "", tsLabels[status]));
      row(card, "Status", statusHint(t), chip);
      if (status === "running") {
        row(card, "Account", "", el("span", "settings-value", t.account || "Unknown"));
        if (t.map?.tailnet) row(card, "Tailnet", "", el("span", "settings-value", t.map.tailnet));
        const n = t.hosts.length;
        row(card, "GoRex hosts", "", el("span", "settings-value", n ? `${n} found` : t.searching ? "Looking…" : "None found"));
      }
      if (t.load.state === "loading" && t.load.total) {
        row(card, "Download", "", el("span", "settings-value", `${formatBytes(t.load.received)} of ${formatBytes(t.load.total)}`));
      }
      const actions = el("div", "host-buttons");
      if (status === "running" || status === "locked" || status === "needs-approval") {
        const out = button("Sign Out", "host-add danger");
        out.dataset.key = "signout";
        out.prepend(icon("log-out"));
        out.onclick = () => t.signOut();
        actions.append(out);
      }
      const label = status === "running" ? "Show Hosts" : status === "load-failed" ? "Try Again" : "Sign In";
      const guide = button(label, status === "running" ? "host-add" : "host-add primary");
      guide.dataset.key = "guide";
      if (status !== "running") guide.prepend(icon(status === "load-failed" ? "rotate-ccw" : "log-in"));
      guide.onclick = () => {
        if (status === "load-failed") t.turnOn();
        this.app.guide();
      };
      actions.append(guide);
      const r = el("div", "settings-row");
      r.append(actions);
      card.append(r);
    }

    const dev = group(this.page, "", "", "The name your tailnet's admin console and other devices show. Letters, digits and hyphens, up to 63.");
    const input = el("input", "theme-search settings-name");
    input.value = t.name;
    input.spellcheck = false;
    input.autocomplete = "off";
    input.setAttribute("aria-label", "Device name");
    const error = el("div", "host-error");
    input.onchange = () => {
      const v = input.value.trim();
      if (!validDeviceName(v)) {
        error.textContent = "Use letters, digits and hyphens, not at the start or end, up to 63.";
        return;
      }
      error.textContent = "";
      t.rename(v).catch((e) => (error.textContent = `Could not rename: ${e instanceof Error ? e.message : e}`));
    };
    row(dev, "Device name", "", input).append(error);

    this.page.append(el("div", "settings-hint settings-note",
      "Turning it on downloads Tailscale's engine (about 6 MB), and this tab joins your tailnet as a device of its own. " +
        "It finds your GoRex hosts there (your own devices named <machine>-gorex that answer as GoRex) and reaches them through Tailscale's encrypted relays. " +
        "In this version the sign-in lasts for this tab only: reloading or closing the tab signs this browser out, and Tailscale removes the device soon after."));
    const trust = group(this.page, "Trust this page", "Each GoRex host must trust this page's origin. On the host, run once:");
    const tr = el("div", "settings-row");
    tr.append(commandBox(allowCommand()));
    trust.append(tr);
  }

  private aboutPage() {
    const about = group(this.page);
    const built = __GOREX_BUILT__ ? ` · built ${__GOREX_BUILT__}` : "";
    row(about, "GoRex Web", `Version ${__GOREX_COMMIT__ || "development build"}${built}`);
    row(about, "libghostty-vt", "The terminal emulator, compiled to WebAssembly.", el("span", "settings-value", `Ghostty ${__GHOSTTY_COMMIT__.slice(0, 12)}`));

    const reset = button("Reset All Settings", "host-add danger");
    reset.onclick = () => {
      if (!confirm("Reset all settings? This forgets the settings, the hosts and the chosen host in this browser, and reloads the page.")) return;
      clearAll();
      location.reload();
    };
    row(group(this.page), "Reset all settings", "Forgets the settings, the hosts and the chosen host kept in this browser.", reset);
  }
}
