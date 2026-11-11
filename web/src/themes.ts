// Ghostty themes: the list GoRex's iOS app ships (ios/GoRex/ghostty-themes.json,
// copied into the build), the choice kept in the browser, and the picker.
import { check, icon } from "./hosts";
import type { RGB } from "./ghostty";
import type { Theme } from "./terminal";

/** A Ghostty theme as ios/scripts/ghostty-themes writes it: 16 palette colours, then background, foreground, cursor, cursor text, selection, selection text, as 0xRRGGBB. */
export interface ThemeEntry {
  name: string;
  colors: number[];
}

/** The theme until one is chosen, as on iOS. */
export const standard = "Gruvbox Dark";
const key = "gorex.theme";

const rgb = (v: number): RGB => [v >> 16, (v >> 8) & 0xff, v & 0xff];

/** The list, empty when it cannot be loaded: the client then keeps the desktop's colours. */
export async function loadThemes(): Promise<ThemeEntry[]> {
  try {
    const r = await fetch("ghostty-themes.json");
    if (!r.ok) throw new Error(String(r.status));
    return await r.json();
  } catch (e) {
    console.warn("could not load the themes", e);
    return [];
  }
}

/** The name chosen in this browser; "" keeps the host's colours. */
export function chosenName(): string {
  try {
    return localStorage.getItem(key) ?? standard;
  } catch {
    return standard;
  }
}

function remember(name: string) {
  try {
    localStorage.setItem(key, name);
  } catch {
    // Without storage the choice lasts until the page closes.
  }
}

/** The theme as a stream's theme message gives one: the colours and the 16 palette entries. */
export function themeColours(t: ThemeEntry): Theme {
  const palette = new Map<number, RGB>();
  for (let i = 0; i < 16; i++) palette.set(i, rgb(t.colors[i]));
  return { background: rgb(t.colors[16]), foreground: rgb(t.colors[17]), cursor: rgb(t.colors[18]), palette };
}

/** Whether a background is light, by its luma, as the iOS app tells. */
export const isLight = ([r, g, b]: RGB) => 0.299 * r + 0.587 * g + 0.114 * b >= 128;

const hex = (v: number) => `#${v.toString(16).padStart(6, "0")}`;

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/**
 * The picker: a button and the panel it opens, in `host`. `onChoose` gets
 * the chosen theme, or undefined for the host's colours.
 */
export function mountPicker(host: HTMLElement, themes: ThemeEntry[], onChoose: (t?: ThemeEntry) => void, onClose: () => void) {
  let selected = chosenName();
  let filter: "all" | "dark" | "light" = "all";
  let query = "";

  const button = el("button", "theme-button", "Theme");
  button.type = "button";
  button.setAttribute("aria-haspopup", "dialog");
  const panel = el("div", "theme-panel");
  panel.hidden = true;
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", "Terminal theme");
  const preview = el("div", "theme-preview");
  const seg = el("div", "theme-filter");
  const search = el("input", "theme-search");
  search.type = "search";
  search.placeholder = "Search themes";
  search.setAttribute("aria-label", "Search themes");
  const list = el("div", "theme-list");
  list.setAttribute("role", "listbox");
  panel.append(preview, seg, search, list);
  host.append(button, panel);

  const segButtons = (["all", "dark", "light"] as const).map((f) => {
    const b = el("button", "", f[0].toUpperCase() + f.slice(1));
    b.type = "button";
    b.onclick = () => {
      filter = f;
      render();
    };
    seg.append(b);
    return b;
  });

  function row(t: ThemeEntry | undefined): HTMLButtonElement {
    const name = t?.name ?? "";
    const b = el("button", "theme-row");
    b.type = "button";
    b.dataset.name = name;
    b.setAttribute("role", "option");
    b.setAttribute("aria-selected", String(name === selected));
    const sw = el("span", "theme-swatch");
    if (t) {
      sw.style.background = hex(t.colors[16]);
      for (let i = 1; i <= 5; i++) {
        const d = el("i");
        d.style.background = hex(t.colors[i]);
        sw.append(d);
      }
    } else {
      sw.classList.add("host");
      sw.append(icon("square-terminal"));
    }
    b.append(sw, el("span", "theme-name", t?.name ?? "Host Colors"), check(name === selected));
    b.onclick = () => {
      selected = name;
      remember(name);
      onChoose(t);
      render();
      // The list was drawn again: the keyboard stays on the row.
      rowsOf().find((r) => r.dataset.name === name)?.focus();
    };
    return b;
  }

  /** The chosen theme's colours on a few lines of a shell. */
  function showPreview() {
    const t = themes.find((x) => x.name === selected);
    preview.hidden = !t;
    if (!t) return;
    preview.style.background = hex(t.colors[16]);
    preview.style.color = hex(t.colors[17]);
    const line = (...runs: [string, number][]) => {
      const d = el("div");
      for (const [s, c] of runs) {
        const span = el("span", "", s);
        span.style.color = hex(t.colors[c]);
        d.append(span);
      }
      return d;
    };
    // Palette 4 is blue, 2 green, 1 red; 17 the foreground, 18 the cursor.
    preview.replaceChildren(
      line(["~/gorex ", 4], ["ls", 17]),
      line(["docs  src  ", 4], ["build.sh  ", 2], ["README.md", 17]),
      line(["~/gorex ", 4], ["git status -s", 17]),
      line([" M ", 1], ["main.go", 17]),
      line(["?? ", 1], ["notes.txt", 17]),
      line(["~/gorex ", 4], ["█", 18]),
    );
  }

  function render() {
    showPreview();
    segButtons.forEach((b, i) => b.classList.toggle("on", ["all", "dark", "light"][i] === filter));
    const q = query.trim().toLowerCase();
    const rows: HTMLElement[] = [];
    if (filter === "all" && !q) rows.push(row(undefined));
    for (const t of themes) {
      if (filter !== "all" && isLight(rgb(t.colors[16])) !== (filter === "light")) continue;
      if (q && !t.name.toLowerCase().includes(q)) continue;
      rows.push(row(t));
    }
    if (rows.length === 0) rows.push(el("div", "theme-empty", "No themes match."));
    list.replaceChildren(...rows);
  }

  const rowsOf = () => [...list.querySelectorAll<HTMLButtonElement>("button.theme-row")];

  function open() {
    panel.hidden = false;
    button.setAttribute("aria-expanded", "true");
    render();
    list.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "center" });
    search.focus();
  }

  function close(refocus = true) {
    if (panel.hidden) return;
    panel.hidden = true;
    button.setAttribute("aria-expanded", "false");
    if (refocus) onClose();
  }

  button.onclick = () => (panel.hidden ? open() : close());
  search.oninput = () => {
    query = search.value;
    render();
  };
  // Escape closes it wherever the focus is.
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !panel.hidden) {
      e.preventDefault();
      close();
    }
  });
  panel.addEventListener("keydown", (e) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const rows = rowsOf();
    if (!rows.length) return;
    const i = rows.indexOf(document.activeElement as HTMLButtonElement);
    const next = e.key === "ArrowDown" ? (i < 0 ? 0 : i + 1) : i - 1;
    if (next < 0) search.focus();
    else rows[Math.min(next, rows.length - 1)].focus();
    e.preventDefault();
  });
  // A click elsewhere closes the panel, and the page's own focus stays where it went.
  document.addEventListener("pointerdown", (e) => {
    if (!panel.hidden && !host.contains(e.target as Node)) close(false);
  });
}
