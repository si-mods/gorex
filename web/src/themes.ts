// Ghostty themes: the list GoRex's iOS app ships (ios/GoRex/ghostty-themes.json,
// copied into the build), and the picker of the Settings dialog's Theme
// section. The choice is a setting (prefs.ts, theme).
import { check, icon } from "./hosts";
import type { RGB } from "./ghostty";
import { prefs, setPrefs } from "./prefs";
import type { Theme } from "./terminal";

/** A Ghostty theme as ios/scripts/ghostty-themes writes it: 16 palette colours, then background, foreground, cursor, cursor text, selection, selection text, as 0xRRGGBB. */
export interface ThemeEntry {
  name: string;
  colors: number[];
}

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
export const chosenName = () => prefs.theme;

/** The theme as a stream's theme message gives one: the colours and the 16 palette entries, with the selection's colours, which a message has not. */
export function themeColours(t: ThemeEntry): Theme {
  const palette = new Map<number, RGB>();
  for (let i = 0; i < 16; i++) palette.set(i, rgb(t.colors[i]));
  return {
    background: rgb(t.colors[16]), foreground: rgb(t.colors[17]), cursor: rgb(t.colors[18]), palette,
    selection: rgb(t.colors[20]), selectionText: rgb(t.colors[21]),
  };
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
 * The picker, as the iOS app's Themes settings: a preview of the chosen
 * theme, a filter, a search and the list. `onChoose` gets the chosen
 * theme, or undefined for the host's colours. The list shows once
 * `show` is called, as the section opens.
 */
export function themePicker(themes: ThemeEntry[], onChoose: (t?: ThemeEntry) => void): { el: HTMLElement; show(): void } {
  let selected = chosenName();
  let filter: "all" | "dark" | "light" = "all";
  let query = "";

  const panel = el("div", "theme-picker");
  const preview = el("div", "theme-preview");
  const seg = el("div", "theme-filter");
  const search = el("input", "theme-search");
  search.type = "search";
  search.placeholder = "Search themes";
  search.setAttribute("aria-label", "Search themes");
  const list = el("div", "theme-list");
  list.setAttribute("role", "listbox");
  panel.append(preview, seg, search, list);

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
      setPrefs({ theme: name });
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

  function show() {
    selected = chosenName();
    render();
    // The chosen row in sight, within the list only: the page keeps its scroll.
    const row = list.querySelector<HTMLElement>('[aria-selected="true"]');
    if (row) list.scrollTop = row.offsetTop - (list.clientHeight - row.offsetHeight) / 2;
  }

  search.oninput = () => {
    query = search.value;
    render();
  };
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
  return { el: panel, show };
}
