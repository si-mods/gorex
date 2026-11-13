// The Settings dialog, as the desktop's settings window (settingsview.go):
// the host's card and the sections at the left, each with its coloured
// tile, and the chosen section at the right, its settings in grouped cards
// (settingsGroup, settingRow). It keeps what a browser has: no glass and no
// Dock badge; updates are the Service Worker's
// (About checks for one). Each change is kept
// (prefs.ts) and applied to the open panes at once by the page; a change
// of language draws the dialog again in the new one.
import { checkForUpdate, install, installKind } from "./app";
import { allowCommand, commandBox, Host, HostLink, HostList, icon } from "./hosts";
import { mac, shortcutGroups } from "./keys";
import {
  Animation, Appearance, CursorStyle, maxFontSize, minFontSize, prefs, Prefs, setPrefs, SidebarView, TabPlacement, textSize, clearAll,
} from "./prefs";
import { formatDate, formatList, Language, languages, onLanguageChange, PlainKey, setLanguage, t, tIn } from "./i18n";
import { blockedCJK, ensure, ensureLatin, mapleFamily, noMaple } from "./maple";
import { formatBytes, ProgressView } from "./progress";
import { Status, Tailnet, validDeviceName } from "./tailnet";
import { desktopTheme, minimumContrast, Theme } from "./terminal";
import { loadThemes, ThemeEntry, themeColours, themePicker } from "./themes";

/** What build.mjs writes in: the client's commit and build date, and the Ghostty commit of the wasm. */
declare const __GOREX_COMMIT__: string;
declare const __GOREX_BUILT__: string;
declare const __GHOSTTY_COMMIT__: string;

/** A host's connection, as the desktop's serverState tells the server's; "none" while the page keeps none. */
export type LinkState = "none" | "connecting" | "connected" | "unreachable";

/** What the dialog needs of the page. */
export interface SettingsApp {
  hosts: HostList;
  /** A host's connection, and what is wrong with it: the card's host is the shown window's (hosts.chosen). */
  link(host: Host | undefined): HostLink;
  /** The theme the page applies, none for the host's colours. */
  theme(): ThemeEntry | undefined;
  /** A theme chosen, or the host's colours (none). */
  chooseTheme(t?: ThemeEntry): void;
  /** Applies a changed setting to the page and its panes. */
  apply(what: "appearance" | "tabs" | "headers" | "animation" | "font" | "cursor" | "contrast", reset?: boolean): void;
  /** The dialog closed: the keyboard goes back to the terminal. */
  closed(): void;
  /** Built-in Tailscale, and its sign-in guide. */
  tailnet: Tailnet;
  guide(): void;
}

type Section = "host" | "appearance" | "terminal" | "theme" | "keyboard" | "hosts" | "tailscale" | "about";

/** The sections, each with the colour of its tile, as the desktop's: macOS's system colours, a hue per kind of setting. */
const sections: [id: Section, glyph: string, tile: string, label: () => string][] = [
  ["appearance", "contrast", "rgb(0, 122, 255)", () => t("settingsAppearance")], // blue
  ["terminal", "square-terminal", "rgb(58, 58, 60)", () => t("settingsTerminal")], // graphite
  ["theme", "sparkles", "rgb(175, 82, 222)", () => t("settingsTheme")], // purple
  ["keyboard", "keyboard", "rgb(142, 142, 147)", () => t("settingsKeyboard")], // gray
  ["hosts", "monitor", "rgb(52, 199, 89)", () => t("settingsHosts")], // green
  // A product's name, not translated.
  ["tailscale", "tailscale", "#1f1e1e", () => "Tailscale"], // the media kit's squircle
  ["about", "info", "rgb(142, 142, 147)", () => t("settingsAbout")], // gray
];

/** The minimum contrast picker's values, as the desktop's contrastChoices: 0 follows the default, 1 turns the correction off. */
const contrastChoices = [0, 1, 2, 3, 4.5, 7];

function contrastName(v: number): string {
  if (v === 0) return t("contrastDefault", { value: minimumContrast });
  if (v === 1) return t("contrastOff");
  // WCAG's levels keep their names.
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
const tsLabels: Record<Status, PlainKey> = {
  off: "tsStateOff", loading: "tsStateLoading", "load-failed": "tsStateLoadFailed", starting: "tsStateStarting",
  "needs-login": "tsStateNeedsLogin", "signing-in": "tsStateSigningIn", "needs-approval": "tsStateApproval",
  running: "linkConnected", expired: "tsStateExpired", locked: "tsStateLocked", error: "tsStateError",
};
const chipState: Record<Status, LinkState> = {
  off: "none", loading: "connecting", "load-failed": "unreachable", starting: "connecting", "needs-login": "none",
  "signing-in": "connecting", "needs-approval": "connecting", running: "connected", expired: "unreachable",
  locked: "unreachable", error: "unreachable",
};

/** The words under Tailscale's status. */
function statusHint(ts: Tailnet): string {
  switch (ts.status()) {
    case "running":
      return ts.statusText();
    case "load-failed":
      return ts.load.state === "failed" ? ts.load.message : "";
    case "needs-approval":
      return t("tsHintApproval", { name: ts.name });
    case "expired":
      return t("tsHintExpired");
    case "locked":
      return t("tsHintLocked");
    case "error":
      return ts.error;
    case "needs-login":
      return t("tsHintNeedsLogin");
    default:
      return "";
  }
}

const stateKeys: Record<LinkState, PlainKey> = {
  none: "linkNone", connecting: "linkConnecting", connected: "linkConnected", unreachable: "linkUnreachable",
};
const stateLabel = (s: LinkState) => t(stateKeys[s]);

export class Settings {
  private dialog = el("dialog", "settings");
  private nav = el("nav", "settings-nav");
  private title = el("h1", "settings-title");
  private page = el("div", "settings-page");
  /** The page's scroller: a redraw of the same page keeps its place. */
  private scroll = el("div", "settings-scroll");
  private section: Section = "appearance";
  /** The host the Hosts section edits, "new" for one to add, or none for the list. */
  private editing?: Host | "new";
  private picker?: ReturnType<typeof themePicker>;
  /** The theme list, once loaded (themes.ts); loading while the promise is unsettled. */
  private themes?: ThemeEntry[];
  private themesLoad?: Promise<ThemeEntry[]>;

  constructor(private app: SettingsApp) {
    this.dialog.setAttribute("aria-label", t("settingsTitle"));
    const close = el("button", "pane-action settings-close");
    close.type = "button";
    this.closeButton = close;
    this.labelClose();
    close.append(icon("x"));
    close.onclick = () => this.close();
    const head = el("header", "settings-head");
    head.append(this.title, close);
    const main = el("div", "settings-main");
    const scroll = this.scroll;
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
    onLanguageChange(() => this.relocalize());
  }

  private closeButton: HTMLButtonElement;

  private labelClose() {
    this.closeButton.title = t("commonClose");
    this.closeButton.setAttribute("aria-label", t("settingsClose"));
  }

  /**
   * The language changed: the dialog again in it, keeping the focus on the
   * control that has it (the language picker). A host form being filled
   * stays, and says itself again (hosts.ts).
   */
  private relocalize() {
    this.picker = undefined;
    this.labelClose();
    if (!this.dialog.open) return;
    const key = (document.activeElement as HTMLElement | null)?.dataset?.key;
    this.drawNav();
    if (this.section === "hosts" && this.editing) this.drawTitle();
    else this.drawPage();
    if (key) this.page.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus();
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

  /** The browser offered to install the page, or it was installed: the About section again. */
  appChanged() {
    if (this.dialog.open && this.section === "about") this.drawPage();
  }

  /** A host or a connection changed: the card, and the pages that show it, again, keeping the focus. A form being filled stays. */
  update() {
    if (!this.dialog.open) return;
    this.drawNav();
    // The Tailscale page follows the node, but not under a name being typed.
    const typing = document.activeElement instanceof HTMLInputElement && this.page.contains(document.activeElement);
    if (this.section === "host" || (this.section === "hosts" && !this.editing) || (this.section === "tailscale" && !typing)) {
      const key = (document.activeElement as HTMLElement | null)?.dataset?.key;
      const top = this.scroll.scrollTop;
      this.drawPage();
      this.scroll.scrollTop = top;
      if (key) this.page.querySelector<HTMLElement>(`[data-key="${CSS.escape(key)}"]`)?.focus({ preventScroll: true });
    }
  }

  /** The shown window's host, and its connection. */
  private link(): HostLink {
    return this.app.link(this.app.hosts.chosen);
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
      b.append(tile(glyph, colour), el("span", "", label()));
      b.onclick = () => this.show(id);
      items.push(b);
    }
    this.nav.replaceChildren(...items);
    if (had) this.nav.querySelector<HTMLElement>(`[data-section="${had}"]`)?.focus();
  }

  /** The host's card atop the sections, as the desktop's serverCard: its icon, its name and its state's dot. */
  private hostCard(): HTMLElement {
    const { host, state } = this.link();
    const b = el("button", "settings-host");
    b.type = "button";
    b.dataset.section = "host";
    b.setAttribute("role", "tab");
    b.setAttribute("aria-selected", String(this.section === "host"));
    b.setAttribute("aria-label", t("settingsHostLabel", { host: host?.name ?? t("settingsHost"), state: stateLabel(state) }));
    const glyph = el("span", "settings-host-icon");
    glyph.append(icon(host?.icon ?? "server"));
    const text = el("span", "settings-host-text");
    const line = el("span", "settings-host-state");
    line.append(el("i", `state-dot ${state}`), el("span", "", stateLabel(state)));
    text.append(el("span", "settings-host-name", host?.name ?? t("settingsNoHost")), line);
    b.append(glyph, text);
    b.onclick = () => this.show("host");
    return b;
  }

  /** The open section's title, atop the page and in the dialog's label. */
  private drawTitle() {
    const title = this.section === "host" ? t("settingsHost") : sections.find(([id]) => id === this.section)![3]();
    this.title.textContent = title;
    this.dialog.setAttribute("aria-label", t("settingsLabel", { section: title }));
  }

  private drawPage() {
    this.drawTitle();
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
    const { host, state, problem } = this.link();
    const card = group(this.page);
    const top = el("div", "settings-row settings-hero");
    const glyph = el("span", "settings-hero-icon");
    glyph.append(icon(host?.icon ?? "server"));
    const text = el("span", "settings-hero-text");
    text.append(el("span", "settings-hero-name", host?.name ?? t("settingsNoHost")), el("span", "settings-hint", host?.url ?? t("hostPageAddHint")));
    const chip = el("span", `settings-chip ${state}`);
    chip.append(el("i", `state-dot ${state}`), el("span", "", stateLabel(state)));
    top.append(glyph, text, chip);
    card.append(top);
    if (host) row(card, t("formAddress"), "", el("span", "settings-value", host.url));
    row(card, t("hostPageThisPage"), "", el("span", "settings-value", location.origin));
    if (problem) row(card, problem.title, problem.text);
    const actions = el("div", "host-buttons");
    if (host) {
      const edit = button(t("hostEdit"));
      edit.onclick = () => this.show("hosts", host);
      actions.append(edit);
    }
    const all = button(host ? t("hostPageChoose") : t("hostAdd"), host ? "host-add" : "host-add primary");
    all.onclick = () => this.show("hosts", host ? undefined : "new");
    actions.append(all);
    const buttons = el("div", "settings-row");
    buttons.append(actions);
    card.append(buttons);

    const trust = group(this.page, t("trustTitle"), t("trustNote"), t("trustFoot"));
    const r = el("div", "settings-row");
    r.append(commandBox(allowCommand()));
    trust.append(r);
  }

  private appearancePage() {
    const win = group(this.page);
    row(
      win, t("settingsWindow"), t("settingsWindowHint"),
      segmented<Appearance>(
        t("settingsAppearance"),
        [["", t("appearanceSystem")], ["light", t("appearanceLight")], ["dark", t("appearanceDark")]],
        prefs.appearance,
        (v) => {
          setPrefs({ appearance: v });
          this.app.apply("appearance");
        },
      ),
    );
    // As the desktop's appearanceWindow: the tabs' place, what the sidebar lists (only while there is one), the
    // pane headers and the animations.
    let view: HTMLElement | undefined;
    row(
      win, t("settingsTabPlacement"), t("settingsTabPlacementHint"),
      segmented<TabPlacement>(
        t("settingsTabPlacement"),
        [["", t("settingsPlacementAbove")], ["left", t("settingsPlacementLeft")], ["right", t("settingsPlacementRight")]],
        prefs.tabPlacement,
        (v) => {
          setPrefs({ tabPlacement: v });
          view!.hidden = !v;
          this.app.apply("tabs");
        },
      ),
    );
    view = row(
      win, t("settingsSidebarView"), t("settingsSidebarViewHint"),
      segmented<SidebarView>(
        t("settingsSidebarView"),
        [["", t("settingsSidebarSpaces")], ["host", t("settingsSidebarHosts")]],
        prefs.sidebarView,
        (v) => {
          setPrefs({ sidebarView: v });
          this.app.apply("tabs");
        },
      ),
    );
    view.hidden = !prefs.tabPlacement;
    row(
      win, t("settingsPaneHeaders"), t("settingsPaneHeadersHint"),
      toggle(t("settingsPaneHeaders"), !prefs.hidePaneHeaders, (on) => {
        setPrefs({ hidePaneHeaders: !on });
        this.app.apply("headers");
      }),
    );
    row(
      win, t("settingsAnimation"), t("settingsAnimationHint"),
      segmented<Animation>(
        t("settingsAnimation"),
        [["", t("animationSnappy")], ["natural", t("animationNatural")], ["off", t("animationOff")]],
        prefs.animation,
        (v) => {
          setPrefs({ animation: v });
          this.app.apply("animation");
        },
      ),
    );

    const text = group(this.page);
    const value = el("span", "settings-value", "");
    const minus = el("button", "pane-action settings-step");
    const plus = el("button", "pane-action settings-step");
    const reset = button(t("settingsReset"));
    minus.type = plus.type = "button";
    minus.setAttribute("aria-label", t("settingsSmaller"));
    plus.setAttribute("aria-label", t("settingsLarger"));
    minus.append(icon("minus"));
    plus.append(icon("plus"));
    const show = () => {
      const size = textSize();
      value.textContent = t("settingsTextSizeValue", { size });
      minus.disabled = size <= minFontSize;
      plus.disabled = size >= maxFontSize;
      reset.disabled = prefs.fontSize === 0;
    };
    const set = (size: number) => {
      setPrefs({ fontSize: size && Math.min(maxFontSize, Math.max(minFontSize, Math.round(size))) });
      show();
      this.app.apply("font");
    };
    minus.onclick = () => set(textSize() - 1);
    plus.onclick = () => set(textSize() + 1);
    reset.onclick = () => set(0);
    show();
    const stepper = el("span", "settings-stepper");
    stepper.append(minus, plus);
    row(text, t("settingsTextSize"), t("settingsTextSizeHint"), value, stepper, reset);

    // The language, as the desktop's: System, then each language by its own name.
    const choices: [string, string][] = [["", t("settingsLanguageSystem")], ...languages.map((l): [string, string] => [l, tIn(l, "languageName")])];
    const lang = select(t("settingsLanguage"), choices, (languages as readonly string[]).includes(prefs.language) ? prefs.language : "", (v) =>
      setLanguage(v as "" | Language),
    );
    lang.dataset.key = "language";
    row(text, t("settingsLanguage"), t("settingsLanguageHint"), lang);
  }

  private terminalPage() {
    fontsFound ??= knownFonts.filter(installed);
    // The two the page brings, then the installed ones.
    const fonts: [string, string][] = [["", t("settingsFontDefault")], ...[mapleFamily, ...fontsFound].map((f): [string, string] => [f, f])];
    if (prefs.font && !fonts.some(([f]) => f === prefs.font)) fonts.push([prefs.font, prefs.font]);
    const font = group(this.page);
    const preview = el("div", "settings-preview");
    const drawPreview = () => {
      const t: Theme = this.previewTheme();
      const rgb = (c?: number[]) => (c ? `rgb(${c.join(",")})` : "");
      preview.style.background = rgb(t.background);
      preview.style.color = rgb(t.foreground);
      preview.style.fontSize = `${textSize()}px`;
      // Shown once the CJK font's chunks of its text are in, so that it never shows the system's CJK font (maple.ts).
      preview.style.visibility = "hidden";
      const shown = prefs.font;
      void Promise.all([ensure(preview.textContent ?? "", [0]), prefs.font === mapleFamily ? ensureLatin() : undefined]).then(() => {
        if (prefs.font === shown) preview.style.visibility = "";
      });
    };
    // The second line is code, the same in every language.
    // Without Maple, a sample in CJK would show in the system's font: it does not show (maple.ts).
    const sample = el("div", "", t("settingsFontSample"));
    sample.hidden = blockedCJK(sample.textContent ?? "");
    preview.append(sample, el("div", "", "0123456789 {}[]() => != <= ~/gorex $"));
    drawPreview();
    row(
      font, t("settingsFont"), noMaple() ? `${t("settingsFontHint")} ${t("settingsFontNoMaple")}` : t("settingsFontHint"),
      select(t("settingsFont"), fonts, prefs.font, (v) => {
        setPrefs({ font: v });
        this.app.apply("font");
        drawPreview();
      }),
    );
    const p = el("div", "settings-row");
    p.append(preview);
    font.append(p);

    const cursor = group(this.page, "", "", t("settingsCursorFoot"));
    row(
      cursor, t("settingsCursorStyle"), "",
      segmented<CursorStyle>(
        t("settingsCursorStyle"),
        [["block", t("settingsCursorBlock")], ["bar", t("settingsCursorBar")], ["underline", t("settingsCursorUnderline")]],
        prefs.cursor ?? "block",
        (v) => {
          setPrefs({ cursor: v });
          this.app.apply("cursor");
        },
      ),
    );
    row(
      cursor, t("settingsCursorBlink"), "",
      toggle(t("settingsCursorBlink"), prefs.cursorBlink ?? true, (on) => {
        setPrefs({ cursorBlink: on });
        this.app.apply("cursor");
      }),
    );

    row(
      group(this.page), t("settingsCopyOnSelect"), t("settingsCopyOnSelectHint"),
      toggle(t("settingsCopyOnSelect"), prefs.copyOnSelect, (on) => setPrefs({ copyOnSelect: on })),
    );

    const values = contrastChoices.includes(prefs.minimumContrast) ? contrastChoices : [...contrastChoices, prefs.minimumContrast];
    row(
      group(this.page), t("settingsMinContrast"), t("settingsMinContrastHint"),
      select(t("settingsMinContrast"), values.map((v) => [String(v), contrastName(v)]), String(prefs.minimumContrast), (v) => {
        setPrefs({ minimumContrast: Number(v) });
        this.app.apply("contrast");
      }),
    );

    const reset = button(t("settingsResetDefaults"));
    reset.onclick = () => {
      const was: Partial<Prefs> = { font: "", cursor: undefined, cursorBlink: undefined, copyOnSelect: false, minimumContrast: 0 };
      setPrefs(was);
      this.app.apply("font");
      this.app.apply("contrast");
      this.app.apply("cursor", true);
      this.drawPage();
    };
    row(group(this.page), t("settingsResetDefaultsHint"), "", reset);
  }

  /** The chosen theme's colours, else GoRex's, for the font's preview. */
  private previewTheme(): Theme {
    const t = this.app.theme();
    return t ? themeColours(t) : desktopTheme();
  }

  /**
   * The Theme section: the picker, once the list has loaded (on demand,
   * the first time the section opens). Meanwhile a progress bar, after
   * 150 ms as the page's others, and a failure with Retry.
   */
  private themePage() {
    if (!this.themes) {
      const card = group(this.page);
      const view = new ProgressView(() => t("settingsThemesLoading"), {
        download: () => t("settingsThemesLoading"),
        start: () => t("settingsThemesLoading"),
        failed: () => t("settingsThemesFailed"),
      }, () => {
        this.themesLoad = undefined;
        this.drawPage();
      });
      view.el.classList.add("settings-load");
      view.el.hidden = true;
      card.append(view.el);
      const timer = setTimeout(() => (view.el.hidden = false), 150);
      const load = (this.themesLoad ??= loadThemes({
        progress: (received, total) => view.update({ phase: "download", received, total }),
      }));
      load.then(
        (list) => {
          clearTimeout(timer);
          this.themes = list;
          if (this.dialog.open && this.section === "theme" && card.isConnected) this.drawPage();
        },
        (e) => {
          clearTimeout(timer);
          console.warn("could not load the themes", e);
          const error = e instanceof Error ? e.message : String(e);
          view.update({ phase: "failed", message: () => t("chunkFailedText", { error }) });
          view.el.hidden = false;
        },
      );
      return;
    }
    this.picker ??= themePicker(this.themes, (t) => this.app.chooseTheme(t));
    const card = group(this.page, "", "", t("settingsThemeFoot"));
    card.classList.add("settings-themes");
    card.append(this.picker.el);
    this.picker.show();
  }

  private keyboardPage() {
    // The keys' names are the keyboard's, the same in every language.
    const keys = formatList(mac ? ["⌘W", "⌘T", "⌘N", "⇧⌘N", "⇧⌘R", "⌃Tab"] : ["Ctrl+W", "Ctrl+T", "Ctrl+N", "Ctrl+Shift+N", "Ctrl+Shift+R", "Ctrl+Tab"]);
    const note = el("div", "settings-hint settings-note", t("settingsKeyboardNote", { keys, chord: mac ? "⌥⇧" : "Alt+Shift" }));
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
    const card = group(this.page, "", t("settingsHostsNote"));
    // The host list's rows: each host with its state's dot, Connect or Disconnect, and Edit (whose form removes it).
    for (const h of hosts.hosts) {
      const r = el("div", "settings-row settings-host-row");
      const pick = el("button", "settings-host-pick");
      pick.type = "button";
      pick.dataset.key = h.id;
      const glyph = el("span", "settings-host-icon small");
      glyph.append(icon(h.icon));
      const text = el("span", "settings-host-text");
      text.append(el("span", "settings-host-name", h.name), el("span", "host-url", h.url));
      const chosen = h.id === hosts.chosen?.id;
      const off = this.app.link(h).state === "none";
      pick.append(glyph, text, hosts.dot(h));
      pick.setAttribute("aria-current", String(chosen));
      pick.title = off ? t("settingsHostConnect", { name: h.name }) : t("hostShowName", { name: h.name });
      pick.onclick = () => {
        // A connected host shows its last window; one the user disconnected connects.
        if (off || !chosen) hosts.choose(h);
        this.update();
      };
      const edit = el("button", "pane-action");
      edit.type = "button";
      edit.dataset.key = h.id + ":edit";
      edit.title = t("commonEdit");
      edit.setAttribute("aria-label", t("hostEditName", { name: h.name }));
      edit.append(icon("pencil"));
      edit.onclick = () => this.show("hosts", h);
      r.append(pick, hosts.plug(h), edit);
      card.append(r);
    }
    if (!hosts.hosts.length) row(card, t("settingsNoHosts"), t("settingsNoHostsHint"));
    const many = hosts.manyNote();
    if (many) {
      const r = el("div", "settings-row");
      r.dataset.key = "many";
      r.append(el("div", "settings-hint host-many", many));
      card.append(r);
    }
    const add = button(t("hostAdd"), "host-add primary");
    add.dataset.key = "add";
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
    const ts = this.app.tailnet;
    const status = ts.status();
    const card = group(this.page);
    const top = el("div", "settings-row settings-hero");
    const glyph = el("span", "settings-hero-icon ts-tile");
    glyph.append(icon("tailscale"));
    const text = el("span", "settings-hero-text");
    const sub =
      status === "off" ? t("tsOffNote")
      : status === "running" ? t("tsThisDevice", { name: ts.name }) + (ts.map?.self.address ? t("listSeparator") + ts.map.self.address : "")
      : ts.statusText();
    text.append(el("span", "settings-hero-name", t("tsBuiltIn")), el("span", "settings-hint", sub));
    const sw = toggle(t("tsBuiltIn"), status !== "off", (on) => {
      if (on) {
        ts.turnOn();
        this.app.guide();
      } else {
        ts.turnOff();
      }
    });
    sw.dataset.key = "switch";
    top.append(glyph, text, sw);
    card.append(top);
    if (status !== "off") {
      const chip = el("span", `settings-chip ${chipState[status]}`);
      chip.append(el("i", `state-dot ${chipState[status]}`), el("span", "", t(tsLabels[status])));
      row(card, t("tsStatus"), statusHint(ts), chip);
      if (status === "running") {
        row(card, t("tsAccount"), "", el("span", "settings-value", ts.account || t("tsUnknown")));
        if (ts.map?.tailnet) row(card, t("tsTailnet"), "", el("span", "settings-value", ts.map.tailnet));
        const n = ts.hosts.length;
        row(card, t("tsHosts"), "", el("span", "settings-value", n ? t("tsFound", { n }) : ts.searching ? t("tsLooking") : t("tsNoneFound")));
      }
      if (ts.load.state === "loading" && ts.load.total) {
        const received = formatBytes(ts.load.received), total = formatBytes(ts.load.total);
        row(card, t("tsDownload"), "", el("span", "settings-value", t("progressOf", { received, total })));
      }
      const actions = el("div", "host-buttons");
      if (status === "running" || status === "locked" || status === "needs-approval") {
        const out = button(t("tsSignOut"), "host-add danger");
        out.dataset.key = "signout";
        out.prepend(icon("log-out"));
        out.onclick = () => ts.signOut();
        actions.append(out);
      }
      const label = status === "running" ? t("tsShowHosts") : status === "load-failed" ? t("commonTryAgain") : t("tsSignIn");
      const guide = button(label, status === "running" ? "host-add" : "host-add primary");
      guide.dataset.key = "guide";
      if (status !== "running") guide.prepend(icon(status === "load-failed" ? "rotate-ccw" : "log-in"));
      guide.onclick = () => {
        if (status === "load-failed") ts.turnOn();
        this.app.guide();
      };
      actions.append(guide);
      const r = el("div", "settings-row");
      r.append(actions);
      card.append(r);
    }

    const dev = group(this.page, "", "", t("tsDeviceFoot"));
    const input = el("input", "theme-search settings-name");
    input.value = ts.name;
    input.spellcheck = false;
    input.autocomplete = "off";
    input.setAttribute("aria-label", t("tsDeviceName"));
    const error = el("div", "host-error");
    input.onchange = () => {
      const v = input.value.trim();
      if (!validDeviceName(v)) {
        error.textContent = t("tsDeviceInvalid");
        return;
      }
      error.textContent = "";
      ts.rename(v).catch((e) => (error.textContent = t("tsRenameFailed", { error: e instanceof Error ? e.message : String(e) })));
    };
    row(dev, t("tsDeviceName"), "", input).append(error);

    this.page.append(el("div", "settings-hint settings-note", t("tsNote")));
    // Both extras stay closed: how it works, and the command a computer may ask for.
    const extra = group(this.page);
    const more = el("details", "settings-more");
    const moreHead = el("summary");
    moreHead.append(icon("info"), el("span", "", t("problemLearnMore")));
    more.append(moreHead, el("p", "settings-hint", t("tsMoreFind")), el("p", "settings-hint", t("tsMoreTab")));
    const trust = el("details", "settings-more");
    const trustHead = el("summary");
    trustHead.append(icon("shield-check"), el("span", "", t("tsTrustAsk")));
    trust.append(trustHead, el("p", "settings-hint", t("tsTrustNote")), commandBox(allowCommand()));
    for (const d of [more, trust]) {
      const r = el("div", "settings-row");
      r.append(d);
      extra.append(r);
    }
  }

  private aboutPage() {
    const about = group(this.page);
    const version = __GOREX_COMMIT__ || t("aboutDevBuild");
    // The product's and the library's names are theirs, not translated.
    row(about, "GoRex Web", __GOREX_BUILT__ ? t("aboutVersionBuilt", { version, date: formatDate(__GOREX_BUILT__) }) : t("aboutVersion", { version }));
    row(about, "libghostty-vt", t("aboutGhostty"), el("span", "settings-value", `Ghostty ${__GHOSTTY_COMMIT__.slice(0, 12)}`));

    // Install App, where the browser offers it; iOS's Add to Home Screen, by hand; nothing once installed.
    const kind = installKind();
    if (kind === "prompt") {
      const b = button(t("aboutInstallApp"), "host-add");
      b.prepend(icon("app-window"));
      b.onclick = () => void install();
      row(group(this.page), t("aboutInstall"), t("aboutInstallHint"), b);
    } else if (kind === "ios") {
      row(group(this.page), t("aboutHomeScreen"), t("aboutHomeScreenHint"));
    }

    const check = button(t("aboutCheckUpdate"));
    const state = el("span", "settings-value");
    state.setAttribute("role", "status");
    check.onclick = async () => {
      check.disabled = true;
      state.textContent = t("aboutChecking");
      const result = await checkForUpdate();
      state.textContent = t(
        { current: "aboutUpToDate", ready: "aboutUpdateReady", failed: "aboutUpdateFailed", unsupported: "aboutUpdateUnsupported" }[result] as PlainKey,
      );
      check.disabled = false;
    };
    row(group(this.page), t("aboutUpdates"), "", state, check);

    const reset = button(t("aboutResetAll"), "host-add danger");
    reset.onclick = () => {
      if (!confirm(t("aboutResetAllAsk"))) return;
      clearAll();
      location.reload();
    };
    row(group(this.page), t("aboutResetAllTitle"), t("aboutResetAllHint"), reset);
  }
}
