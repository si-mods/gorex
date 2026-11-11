// The user's choices in this browser, as the desktop keeps settings.json
// (settings.go): one object in localStorage (gorex.settings), read once as
// the page loads and written on every change. The Settings dialog
// (settings.ts) changes them; the page applies each change to every open
// pane at once.
import { minimumContrast } from "./terminal";

export type Appearance = "" | "light" | "dark";
export type CursorStyle = "block" | "bar" | "underline";

export interface Prefs {
  /** The page's colours: "light", "dark", or "" to follow the terminal's background, as the desktop's System follows the theme's colours. */
  appearance: Appearance;
  /** Hides the panes' headers. */
  hidePaneHeaders: boolean;
  /** The terminals' text size in CSS pixels, 0 for defaultFontSize. */
  fontSize: number;
  /** A font family, "" for the bundled JetBrains Mono. */
  font: string;
  /** The cursor until a program sets one (DECSCUSR); unset keeps the host's. */
  cursor?: CursorStyle;
  cursorBlink?: boolean;
  /** The least contrast of text with its background, 1 (off) to 21, 0 for the desktop's default. */
  minimumContrast: number;
  /** A Ghostty theme's name, "" for the host's colours. */
  theme: string;
}

/** The desktop's text size (settings.go, defaultFontSize), and the stepper's range (settingsview.go). */
export const defaultFontSize = 11.6;
export const minFontSize = 8;
export const maxFontSize = 32;

/** The theme until one is chosen, as on iOS. */
export const standardTheme = "Gruvbox Dark";

const key = "gorex.settings";
/** Where the theme was kept before gorex.settings. */
const oldThemeKey = "gorex.theme";

const defaults = (): Prefs => ({
  appearance: "",
  hidePaneHeaders: false,
  fontSize: 0,
  font: "",
  minimumContrast: 0,
  theme: standardTheme,
});

/** Reads the choices, taking the theme kept under gorex.theme once; out of range values count as none, as loadSettings does. */
function load(): Prefs {
  const p = defaults();
  try {
    const raw = localStorage.getItem(key);
    if (raw) {
      const j = JSON.parse(raw) ?? {};
      if (["", "light", "dark"].includes(j.appearance)) p.appearance = j.appearance;
      p.hidePaneHeaders = j.hidePaneHeaders === true;
      if (typeof j.fontSize === "number" && j.fontSize >= minFontSize && j.fontSize <= maxFontSize) p.fontSize = j.fontSize;
      if (typeof j.font === "string") p.font = j.font;
      if (["block", "bar", "underline"].includes(j.cursor)) p.cursor = j.cursor;
      if (typeof j.cursorBlink === "boolean") p.cursorBlink = j.cursorBlink;
      if (typeof j.minimumContrast === "number" && j.minimumContrast >= 0 && j.minimumContrast <= 21) {
        p.minimumContrast = j.minimumContrast === 0 ? 0 : Math.max(1, j.minimumContrast);
      }
      if (typeof j.theme === "string") p.theme = j.theme;
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

/** The bundled family and the desktop's fallbacks (style.go, defaultFontFamily). */
export const defaultFamily = `"JetBrains Mono", "SF Mono", Menlo, monospace`;

/** The terminals' CSS font family: the chosen one before the default stack. */
export const fontFamily = () => (prefs.font ? `"${prefs.font.replace(/["\\]/g, "")}", ${defaultFamily}` : defaultFamily);

/** The sequence (DECSCUSR) that sets the chosen cursor, as the desktop's cursorSeq; none while neither is chosen. */
export function cursorSeq(): string | undefined {
  if (prefs.cursor === undefined && prefs.cursorBlink === undefined) return undefined;
  let n = { block: 1, underline: 3, bar: 5 }[prefs.cursor ?? "block"];
  if (prefs.cursorBlink === false) n++;
  return `\x1b[${n} q`;
}
