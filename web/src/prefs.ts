// The user's choices in this browser, as the desktop keeps settings.json
// (settings.go): one object in localStorage (gorex.settings), read once as
// the page loads and written on every change. The Settings dialog
// (settings.ts) changes them; the page applies each change to every open
// pane at once.
import { mapleFamily } from "./maple";
import { minimumContrast } from "./terminal";

export type Appearance = "" | "light" | "dark";
export type CursorStyle = "block" | "bar" | "underline";
/** Where the tabs (windows) go, as the desktop's TabPlacement: "" above the panes, or a sidebar at the left or the right. */
export type TabPlacement = "" | "left" | "right";
/** What a sidebar lists, as the desktop's SidebarView: "" the spaces, "host" the spaces under their hosts. */
export type SidebarView = "" | "host";
/** How the panes move, as the desktop's Animation: "" snappy springs, "natural" softer ones, "off" none. */
export type Animation = "" | "natural" | "off";

export interface Prefs {
  /** The page's colours: "light", "dark", or "" to follow the terminal's background, as the desktop's System follows the theme's colours. */
  appearance: Appearance;
  /** Where the tabs go. */
  tabPlacement: TabPlacement;
  /** What the sidebar lists, while there is one. */
  sidebarView: SidebarView;
  /** Hides the panes' headers, while the tabs are in a sidebar at the left (paneHeaders). */
  hidePaneHeaders: boolean;
  /** How the panes move. */
  animation: Animation;
  /** The terminals' text size in CSS pixels, 0 for defaultFontSize. */
  fontSize: number;
  /** A font family, "" for the bundled JetBrains Mono; Maple Mono NF CN is bundled too. */
  font: string;
  /** The cursor until a program sets one (DECSCUSR); unset keeps the host's. */
  cursor?: CursorStyle;
  cursorBlink?: boolean;
  /** The least contrast of text with its background, 1 (off) to 21, 0 for the desktop's default. */
  minimumContrast: number;
  /** A Ghostty theme's name, "" for the host's colours. */
  theme: string;
  /**
   * The chosen theme's colours, as its entry in the list (themes.ts), so
   * that the page starts with them without loading the list.
   */
  themeColors?: number[];
  /** Copies a selection to the clipboard as it is made, as the desktop's Copy on selection. */
  copyOnSelect: boolean;
  /** Built-in Tailscale's device name, "" for a new gorex-web-xxxx per tab (tailnet.ts). */
  tailscaleName: string;
  /** The page's language, as "zh-Hans", or "" for System: the browser's languages (i18n.ts). */
  language: string;
}

/**
 * The terminals' text size until set, in whole CSS pixels. The desktop's
 * default is 11.6 points (settings.go, defaultFontSize); 11.6 CSS pixels
 * read small in a browser.
 * The stepper's range is the desktop's (settingsview.go).
 */
export const defaultFontSize = 15;
export const minFontSize = 8;
export const maxFontSize = 32;

/** The theme until one is chosen, as on iOS. */
export const standardTheme = "Gruvbox Dark";

const key = "gorex.settings";
/** Where the theme was kept before gorex.settings. */
const oldThemeKey = "gorex.theme";

const defaults = (): Prefs => ({
  appearance: "",
  tabPlacement: "",
  sidebarView: "",
  hidePaneHeaders: false,
  animation: "",
  fontSize: 0,
  font: "",
  minimumContrast: 0,
  theme: standardTheme,
  copyOnSelect: false,
  tailscaleName: "",
  language: "",
});

/** Reads the choices, taking the theme kept under gorex.theme once; out of range values count as none, as loadSettings does. */
function load(): Prefs {
  const p = defaults();
  try {
    const raw = localStorage.getItem(key);
    if (raw) {
      const j = JSON.parse(raw) ?? {};
      if (["", "light", "dark"].includes(j.appearance)) p.appearance = j.appearance;
      if (["", "left", "right"].includes(j.tabPlacement)) p.tabPlacement = j.tabPlacement;
      if (["", "host"].includes(j.sidebarView)) p.sidebarView = j.sidebarView;
      p.hidePaneHeaders = j.hidePaneHeaders === true;
      if (["", "natural", "off"].includes(j.animation)) p.animation = j.animation;
      if (typeof j.fontSize === "number" && j.fontSize >= minFontSize && j.fontSize <= maxFontSize) p.fontSize = Math.round(j.fontSize);
      if (typeof j.font === "string") p.font = j.font;
      if (["block", "bar", "underline"].includes(j.cursor)) p.cursor = j.cursor;
      if (typeof j.cursorBlink === "boolean") p.cursorBlink = j.cursorBlink;
      if (typeof j.minimumContrast === "number" && j.minimumContrast >= 0 && j.minimumContrast <= 21) {
        p.minimumContrast = j.minimumContrast === 0 ? 0 : Math.max(1, j.minimumContrast);
      }
      if (typeof j.theme === "string") p.theme = j.theme;
      if (Array.isArray(j.themeColors) && j.themeColors.length === 22 && j.themeColors.every((c: unknown) => Number.isInteger(c))) {
        p.themeColors = j.themeColors;
      }
      p.copyOnSelect = j.copyOnSelect === true;
      if (typeof j.tailscaleName === "string") p.tailscaleName = j.tailscaleName;
      if (typeof j.language === "string") p.language = j.language;
    } else {
      const old = localStorage.getItem(oldThemeKey);
      if (old !== null) {
        p.theme = old;
        localStorage.setItem(key, JSON.stringify(p));
        localStorage.removeItem(oldThemeKey);
      }
    }
  } catch {
    // Without storage the choices last until the page closes.
  }
  return p;
}

export const prefs: Prefs = load();

/** Changes the choices and keeps them. */
export function setPrefs(change: Partial<Prefs>) {
  Object.assign(prefs, change);
  for (const k of Object.keys(change) as (keyof Prefs)[]) if (change[k] === undefined) delete prefs[k];
  try {
    localStorage.setItem(key, JSON.stringify(prefs));
  } catch {
    // As load.
  }
}

/** Forgets every choice of the page in this browser: the settings, the hosts and the chosen host. */
export function clearAll() {
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith("gorex.")) localStorage.removeItem(k);
  } catch {
    // Nothing kept.
  }
}

/** The text size in use. */
export const textSize = () => prefs.fontSize || defaultFontSize;

/** The minimum contrast in use: the desktop's default when none is set. */
export const contrastRatio = () => prefs.minimumContrast || minimumContrast;

/** The font until one is chosen, which the page bundles as the desktop does (style.go, defaultFontFamily). */
export const defaultFont = "JetBrains Mono";

/**
 * The terminals' CSS font family: the chosen font, then Maple Mono NF CN
 * for what it lacks (CJK; maple.ts), then monospace for what neither has.
 */
export function fontFamily(): string {
  const font = (prefs.font || defaultFont).replace(/["\\]/g, "");
  return font === mapleFamily ? `"${mapleFamily}", monospace` : `"${font}", "${mapleFamily}", monospace`;
}

/** The sequence (DECSCUSR) that sets the chosen cursor, as the desktop's cursorSeq; none while neither is chosen. */
export function cursorSeq(): string | undefined {
  if (prefs.cursor === undefined && prefs.cursorBlink === undefined) return undefined;
  let n = { block: 1, underline: 3, bar: 5 }[prefs.cursor ?? "block"];
  if (prefs.cursorBlink === false) n++;
  return `\x1b[${n} q`;
}

/** Whether the panes show their headers, as the desktop's paneHeaders: always, but when hidden with the tabs at the left. */
export const paneHeaders = () => !prefs.hidePaneHeaders || prefs.tabPlacement !== "left";

/**
 * How long the panes take to move, in ms, as the desktop's springs take
 * (animation.go: Snappy 320 ms, Natural 500 ms); 0 with Off, or while the
 * system asks for less motion, as the desktop's motion.
 */
export function motionMs(): number {
  if (prefs.animation === "off" || matchMedia("(prefers-reduced-motion: reduce)").matches) return 0;
  return prefs.animation === "natural" ? 500 : 320;
}
