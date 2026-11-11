// Links in a terminal's text, as the desktop finds them (MyGo's
// plugins/terminal/links.go and view.go's linkAt, with GoRex's find.go):
// a hyperlink (OSC 8), a URL printed as text, and a web address printed
// without its scheme, as support.github.com/tickets, which opens over
// https. On a host whose gateway sends files (gorex.download), a file path
// printed in the pane and a file:// hyperlink are links too, as the
// desktop's in a host's pane (find.go isLink, upload.go download); files.ts
// opens them. docs/web-client.md and docs/web-open-files.md have the rules.

/** A row's text: its characters (code points) and the column each is in. */
export interface RowText {
  text: string[];
  cols: number[];
}

/** What finding a link needs of a terminal's viewport. */
export interface LinkGrid {
  cols: number;
  rowText(row: number): RowText;
  /** The URL of the hyperlink (OSC 8) at a cell, or "". */
  hyperlinkAt(col: number, row: number): string;
  /** Whether the row above the viewport's first is scrolled out of sight. */
  scrolledBack(): boolean;
}

/** A link and its cells on a row of the viewport, columns x0 up to x1. */
export interface Link {
  url: string;
  row: number;
  x0: number;
  x1: number;
  /** For a link to a file on the host, the path to ask the gateway for. */
  path?: string;
}

// urlPattern finds the URLs programs print as text. A URL sends what is not
// ASCII encoded, so it ends at the first such character: the CJK text or
// full-width punctuation that often follows one without a space.
const urlBody = "[^\\t\\n\\f\\r \"'<>`\\x00-\\x1f\\x7f-\\u{10ffff}]+";
const urlPattern = new RegExp(`(?:(?:https?|ftp|file)://|mailto:)${urlBody}`, "gu");
const urlWhole = new RegExp(`^(?:(?:https?|ftp|file)://|mailto:)${urlBody}`, "u");
const urlAny = new RegExp(`(?:(?:https?|ftp|file)://|mailto:)${urlBody}`, "u");

// proseDelimiters end a path printed in Chinese or Japanese prose, which
// puts no space around it; a quoted path may hold them. Go's \s is ASCII's
// space, tab, newline, form feed and carriage return, written out here.
const proseDelimiters = "，。；：！？、（）【】《》「」『』";
const pathToken = new RegExp(`[^\\t\\n\\f\\r "'<>\`${proseDelimiters}]+`, "gu");
const place = "(?::\\d+(?::\\d+)?)?";
const pathInToken = new RegExp(
  `(?:(?:~|\\.{1,2})?/[^\\t\\n\\f\\r :]+|[\\p{L}\\p{N}_.@+-]+(?:/[\\p{L}\\p{N}_.@+-]+)+/?)${place}` +
    `|[\\p{L}\\p{N}_@+-][\\p{L}\\p{N}_.@+-]*\\.[\\p{L}\\p{N}_]+${place}`,
  "gu",
);
const quotedPlace = /^:\d+(?::\d+)?/;
const pathPlace = /:(\d+)(?::(\d+))?:?$|\((\d+)(?:,(\d+))?\)$|#L(\d+)(?:C(\d+))?$/;
const linkLine = /^(.*?)(:\d+)(?::\d+)?$/;

/** The code points of s. */
const runes = (s: string) => [...s].length;

const isAlnum = (c: string | undefined) => !!c && /^[a-zA-Z0-9]$/.test(c);

/** Go's unicode.IsSpace, as strings.TrimSpace trims. */
const goSpace = "[\\t\\n\\v\\f\\r \\u0085\\u00a0\\u1680\\u2000-\\u200a\\u2028\\u2029\\u202f\\u205f\\u3000]";
const trimSpace = (s: string) => s.replace(new RegExp(`^${goSpace}+|${goSpace}+$`, "gu"), "");

/**
 * Trims what likely ends the sentence around a URL or a path rather than
 * it: punctuation, as the 。 or … of prose, and closing brackets that it
 * did not open.
 */
export function trimURL(u: string): string {
  const count = (s: string, c: string) => s.split(c).length - 1;
  while (u.length > 0) {
    // The last character, which may be a surrogate pair.
    const low = u.charCodeAt(u.length - 1);
    const r = low >= 0xdc00 && low <= 0xdfff && u.length > 1 ? u.slice(-2) : u.slice(-1);
    if (r.codePointAt(0)! >= 0x80 && /^\p{P}$/u.test(r)) {
      u = u.slice(0, u.length - r.length);
      continue;
    }
    const last = u[u.length - 1];
    if (".,;:!?".includes(last)) {
      u = u.slice(0, -1);
      continue;
    }
    const open = ({ ")": "(", "]": "[", "}": "{" } as Record<string, string>)[last];
    if (open && count(u, open) < count(u, last)) {
      u = u.slice(0, -1);
      continue;
    }
    return u;
  }
  return u;
}

/** The URL printed as text at column col of a row, and the columns it spans, x0 up to x1. */
export function urlAt(text: string[], cols: number[], col: number): { url: string; x0: number; x1: number } | undefined {
  const s = text.join("");
  for (const m of s.matchAll(urlPattern)) {
    const u = trimURL(m[0]);
    if (u === "") continue;
    const start = runes(s.slice(0, m.index));
    const end = start + runes(u);
    if (start < cols.length && cols[start] <= col && col <= cols[end - 1]) return { url: u, x0: cols[start], x1: cols[end - 1] + 1 };
  }
  return undefined;
}

/** s up to a closing bracket it did not open, as the ) of "(image.png)已保存". */
function balanced(s: string): string {
  const depth = [0, 0, 0];
  for (let i = 0; i < s.length; i++) {
    let k = "([{".indexOf(s[i]);
    if (k >= 0) {
      depth[k]++;
      continue;
    }
    k = ")]}".indexOf(s[i]);
    if (k >= 0) {
      if (depth[k] === 0) return s.slice(0, i);
      depth[k]--;
    }
  }
  return s;
}

/** The index of the ( that opens the ) s ends with, or -1. */
function opening(s: string): number {
  let depth = 0;
  for (let i = s.length - 1; i >= 0; i--) {
    if (s[i] === ")") depth++;
    else if (s[i] === "(" && --depth === 0) return i;
  }
  return -1;
}

/**
 * The quoted run of s that opens at index i, a up to b, with the place after
 * it, or b < 0. A quote between letters or digits, as in don't, is an
 * apostrophe, and the inside of a quoted path neither starts nor ends with a
 * space.
 */
function quotedAt(s: string, i: number): [number, number] {
  const q = s[i];
  if ((q !== '"' && q !== "'") || (i > 0 && isAlnum(s[i - 1]))) return [0, -1];
  const j = s.indexOf(q, i + 1);
  if (j < 0) return [0, -1];
  const inner = s.slice(i + 1, j);
  if (inner === "" || /[\r\n]/.test(inner) || trimSpace(inner) !== inner) return [0, -1];
  let b = j + 1;
  b += quotedPlace.exec(s.slice(b))?.[0].length ?? 0;
  if (b < s.length && isAlnum(s[b])) return [0, -1];
  return [i, b];
}

/** The path a token of pathToken, or a quoted run, prints, and where in the token it shows, a up to b; "" for none. */
function pathOf(tok: string): [string, number, number] {
  let a = 0, b = tok.length;
  const quoted = tok[0] === '"' || tok[0] === "'";
  if (!quoted) {
    // Brackets and emphasis around a path are not part of it, nor is the
    // call a path is the argument of, as write(notes.md); a bracket the
    // path opens is, as icon_(copy).png.
    a = tok.length - tok.replace(/^[([{*]+/, "").length;
    // The name of an option or a variable the path is the value of.
    const eq = tok.indexOf("=", a);
    if (eq >= 0 && !tok.slice(a, eq).includes("/")) a = eq + 1;
    b = a + balanced(tok.slice(a)).length;
    if (tok.slice(a, b).endsWith(")") && !pathPlace.test(tok.slice(a, b))) {
      const i = opening(tok.slice(a, b));
      if (i > 0) [a, b] = [a + i + 1, b - 1];
    }
    while (b > a && ",;.!?]".includes(tok[b - 1])) b--;
    if (!pathPlace.test(tok.slice(a, b))) b = a + trimURL(tok.slice(a, b)).length;
    if (b <= a) return ["", 0, 0];
  }
  let name = tok.slice(a, b), line = 0, col = 0;
  const m = pathPlace.exec(name);
  if (m) {
    const placeText = name;
    name = placeText.slice(0, m.index);
    for (const i of [1, 3, 5]) {
      if (m[i] !== undefined) {
        line = parseInt(m[i], 10);
        if (m[i + 1] !== undefined) col = parseInt(m[i + 1], 10);
        break;
      }
    }
    if (placeText.endsWith(":")) b--; // the colon after main.go:12:3: ends a compiler's location
  }
  if (quoted) name = name.slice(1, -1);
  // ~/ ./ and ../ alone are directories, as docs/ is. They hold no letter.
  const dirOnly = name === "~/" || name === "./" || name === "../";
  if (!dirOnly && (name === "" || /[:\x00\r\n]/.test(name) || !/[\p{L}\p{N}]/u.test(name))) return ["", 0, 0];
  // Words are not paths: a name without a slash takes a letter, and an
  // extension or a line, so that version 1.2.3 and 12:30 are neither.
  if (!name.includes("/")) {
    const dot = name.lastIndexOf(".");
    if (!/\p{L}/u.test(name) || (line === 0 && (dot < 0 || dot === name.length - 1))) return ["", 0, 0];
  }
  let path = name;
  if (line > 0) {
    path += `:${line}`;
    if (col > 0) path += `:${col}`;
  }
  return [path, a, b];
}

/**
 * The file path printed at column col of a row, as urlAt, or none:
 * absolute, from ~, ./ or ../, with a slash, or a file name with an
 * extension or a line; the place in the file is written path:line:col
 * however it was printed, and a quoted path loses its quotes.
 */
export function pathAt(text: string[], cols: number[], col: number): { path: string; x0: number; x1: number } | undefined {
  const s = text.join("");
  const span = (a: number, b: number): [number, number] | undefined => {
    const start = runes(s.slice(0, a));
    const end = start + runes(s.slice(a, b));
    if (end > start && end <= cols.length && cols[start] <= col && col <= cols[end - 1]) return [cols[start], cols[end - 1] + 1];
    return undefined;
  };
  // A quoted path first, as it may hold spaces and what the tokens below end at.
  for (let i = 0; i < s.length; i++) {
    const [a, b] = quotedAt(s, i);
    if (b < 0) continue;
    const [p, pa, pb] = pathOf(s.slice(a, b));
    if (p === "") continue;
    const x = span(a + pa, a + pb);
    if (x) return { path: p, x0: x[0], x1: x[1] };
    i = b - 1;
  }
  for (const m of s.matchAll(pathToken)) {
    const tok = m[0], t0 = m.index!;
    const [p, a, b] = pathOf(tok);
    if (p !== "") {
      const x = span(t0 + a, t0 + b);
      if (x) return { path: p, x0: x[0], x1: x[1] };
      continue;
    }
    for (const n of tok.matchAll(pathInToken)) {
      const [p, a, b] = pathOf(n[0]);
      if (p === "") continue;
      const x = span(t0 + n.index! + a, t0 + n.index! + b);
      if (x) return { path: p, x0: x[0], x1: x[1] };
    }
  }
  return undefined;
}

// Top-level domains a web address without its scheme may end in. The
// desktop asks the public suffix list (golang.org/x/net/publicsuffix),
// which the page does not ship: these are every two-letter country code
// the list has, as of x/net v0.59.0, and common generic ones.
const tlds = new Set(
  (
    "ac ad ae af ag ai al am ao aq ar as at au aw ax az ba bb bd be bf bh bi bj bm bn bo br bs bt bv bw by bz ca cc cd cf cg ch ci " +
    "cl cm cn co cr cu cv cw cx cy cz de dj dk dm do dz ec ee eg es et eu fi fj fm fo fr ga gb gd ge gf gg gh gi gl gm gn gp gq gr gs " +
    "gt gu gw gy hk hm hn hr ht hu id ie il im in io iq ir is it je jo jp ke kg ki km kn kp kr kw ky kz la lb lc li lk lr ls lt lu lv " +
    "ly ma mc md me mg mh mk ml mn mo mp mq mr ms mt mu mv mw mx my mz na nc ne nf ng ni nl no nr nu nz om pa pe pf ph pk pl pm pn pr " +
    "ps pt pw py qa re ro rs ru rw sa sb sc sd sg sh si sj sk sl sm sn so sr ss st su sv sx sy sz tc td tf tg th tj tk tl tm tn to tr " +
    "tt tv tw tz ua ug uk us uy uz va vc ve vg vi vn vu wf ws ye yt za zm zw " +
    "com net org edu gov mil int info biz name pro mobi aero asia cat coop jobs museum tel travel post " +
    "dev app page blog cloud tech online site store shop xyz top live news ink wiki world space website club design digital " +
    "email link fun art icu vip work today run rocks one zone systems software solutions network media agency studio team " +
    "tools codes engineering services academy center chat social group global company host hosting moe google"
  ).split(" "),
);

/** Second-level names a country's registry keeps, as co.uk: a public suffix itself, not a host. */
const registryNames = new Set("ac co com edu go gob gov mil ne net nic or org".split(" "));

/**
 * Whether path reads as a web address without its scheme: a relative path
 * whose first part is a host name under a top-level domain (tlds), and not
 * a registry's own name such as co.uk.
 */
export function webPath(path: string): boolean {
  const slash = path.indexOf("/");
  if (slash < 0) return false;
  const name = path.slice(0, slash).toLowerCase();
  if (!name.includes(".")) return false;
  const labels = name.split(".");
  const tld = labels[labels.length - 1];
  if (!tlds.has(tld)) return false;
  return !(labels.length === 2 && tld.length === 2 && registryNames.has(labels[0]));
}

/** The file path a printed link names, :line:col trimmed, as the desktop's linkPath; undefined for a URL. */
export function linkPath(link: string): string | undefined {
  if (link.includes("://")) return undefined;
  return linkLine.exec(link)?.[1] ?? link;
}

/** The https URL a printed path names when it is a web address without its scheme (webPath), :line:col trimmed. */
export function webURL(link: string): string | undefined {
  const path = linkPath(link);
  return path !== undefined && webPath(path) ? `https://${path}` : undefined;
}

/**
 * The path a file:// URL names on the host, as the desktop's download
 * reads it: the host ignored, the path percent-decoded, and a Windows
 * drive's leading slash taken off (file:///C:/x is C:/x). Undefined for
 * another URL, or one without a path.
 */
export function fileURLPath(url: string): string | undefined {
  if (!/^file:\/\//i.test(url)) return undefined;
  let path: string;
  try {
    path = decodeURIComponent(new URL(url).pathname);
  } catch {
    return undefined;
  }
  if (path.length > 2 && path[2] === ":") path = path.slice(1);
  return path === "" || path === "/" ? undefined : path;
}

/** Whether the page opens a URL: http, https, ftp and mailto. A file:// URL names a file on the host, which only the gateway sends (fileURLPath). */
export function opens(url: string): boolean {
  return /^(?:https?|ftp):\/\//i.test(url) || /^mailto:/i.test(url);
}

/** Whether a cell of the row has the hyperlink url. */
function hasHyperlink(g: LinkGrid, url: string, row: number): boolean {
  for (let x = 0; x < g.cols && row >= 0; x++) if (g.hyperlinkAt(x, row) === url) return true;
  return false;
}

/**
 * Reads a hyperlink whose cells print its URL, as a Markdown autolink's
 * do, as that URL printed as text: such a program may take the prose after
 * a URL for part of it, as the 。工单 of https://x.y/a。工单. Given the
 * hyperlink's cells on row, x0 up to x1, it returns the URL urlAt finds and
 * the cells it spans, or "" when the cells print only the prose. The cells
 * print the URL from its start, or a part of it that goes on from the row
 * above, which holds the same hyperlink (a long URL wraps). Undefined for a
 * URL urlAt takes whole, and for cells that print other text.
 */
function autolink(g: LinkGrid, url: string, row: number, t: RowText, x0: number, x1: number): { url: string; x0: number; x1: number } | undefined {
  const m = urlWhole.exec(url);
  if (!m || trimURL(m[0]) === url) return undefined;
  const printed: string[] = [];
  let first = -1;
  t.cols.forEach((c, i) => {
    if (x0 <= c && c < x1) {
      if (first < 0) first = i;
      printed.push(t.text[i]);
    }
  });
  let off = url.indexOf(printed.join(""));
  if (printed.length === 0 || off < 0) return undefined;
  off = runes(url.slice(0, off));
  const outOfSight = row === 0 && g.scrolledBack();
  if (off > 0 && !outOfSight && !hasHyperlink(g, url, row - 1)) return undefined;
  const u = trimURL(m[0]);
  if (!urlAny.test(u)) return undefined;
  const n = runes(u) - off; // the characters of u on this row
  if (n <= 0) return { url: "", x0, x1 };
  if (n < printed.length) x1 = t.cols[first + n];
  return { url: u, x0, x1 };
}

/**
 * The link at a cell, as the desktop's linkAt: a hyperlink (OSC 8), a URL
 * printed as text, or a web address printed without its scheme; none for
 * one the page does not open (opens). With files (the host sends files), a
 * file:// URL and a printed file path are links to the host's file, with
 * its path: as the desktop's isLink in a host's pane, any path but a
 * folder's (a trailing /), and none written with backslashes (C:\dir\x).
 */
export function linkAt(g: LinkGrid, col: number, row: number, files = false): Link | undefined {
  const found = ((): Link | undefined => {
    const url = g.hyperlinkAt(col, row);
    if (url !== "") {
      // The hyperlink's cells are those around the cell with its URL.
      let x0 = col, x1 = col + 1;
      while (x0 > 0 && g.hyperlinkAt(x0 - 1, row) === url) x0--;
      while (x1 < g.cols && g.hyperlinkAt(x1, row) === url) x1++;
      const a = autolink(g, url, row, g.rowText(row), x0, x1);
      if (a) return a.url === "" || col < a.x0 || col >= a.x1 ? undefined : { ...a, row };
      return { url, row, x0, x1 };
    }
    const t = g.rowText(row);
    const u = urlAt(t.text, t.cols, col);
    if (u) return { ...u, row };
    const p = pathAt(t.text, t.cols, col);
    if (!p) return undefined;
    const web = webURL(p.path);
    if (web) return { url: web, row, x0: p.x0, x1: p.x1 };
    const path = linkPath(p.path);
    if (files && path && !path.endsWith("/") && !backslashed(t, p.path, p.x0, p.x1)) return { url: p.path, row, x0: p.x0, x1: p.x1, path };
    return undefined;
  })();
  if (!found) return undefined;
  if (found.path !== undefined || opens(found.url)) return found;
  const path = files ? fileURLPath(found.url) : undefined;
  return path !== undefined ? { ...found, path } : undefined;
}

/** Whether a path printed at columns x0 up to x1 of a row is part of one written with backslashes, as the x.txt of C:\dir\x.txt. */
function backslashed(t: RowText, path: string, x0: number, x1: number): boolean {
  if (path.includes("\\")) return true;
  const i = t.cols.indexOf(x0), j = t.cols.indexOf(x1);
  return (i > 0 && t.text[i - 1] === "\\") || (j >= 0 && t.text[j] === "\\");
}
