// How a program shows: its name and the tile of its windows. A copy of
// the iOS app's ProgramTile.swift (itself GoRex's programs.go), drawn in
// CSS. The glyphs are the SVGs of assets/icons and assets/brands, which
// web/build.mjs copies into dist; a mask paints them in the tile's colour,
// as iOS draws its template images. The programs' names are theirs (as
// on the desktop), not translated.

import { t } from "./i18n";

export interface Program {
  name: string;
  /** The program's icon; "brand:x" is a mark of assets/brands. */
  glyph: string;
  tileBg: number;
  tileFg: number;
  /** The glyph on the tile, glyph when empty. */
  tileGlyph: string;
  /** Outlines light tiles. */
  tileBorder: boolean;
  /** A shell at its prompt. */
  shell: boolean;
}

type Opts = { tileGlyph?: string; tileBorder?: boolean; shell?: boolean };
const p = (name: string, glyph: string, tileBg: number, tileFg: number, o: Opts = {}): Program => ({
  name, glyph, tileBg, tileFg, tileGlyph: o.tileGlyph ?? "", tileBorder: o.tileBorder ?? false, shell: o.shell ?? false,
});

const shellTile = p("", "square-terminal", 0x1d2420, 0x5fd38d, { tileGlyph: "terminal", shell: true });

const activity = p("Activity", "activity", 0x1f6f43, 0xa7f3c4);
const python = p("Python", "brand:python", 0x3776ab, 0xffd43b);
const vim = p("Vim", "brand:vim", 0x019733, 0xffffff);
const ruby = p("Ruby", "brand:ruby", 0xcc342d, 0xffffff);
const rust = (n: string) => p(n, "brand:rust", 0x2b2b2b, 0xf4a261);
const globe = (n: string) => p(n, "globe", 0x5b4bd6, 0xffffff);
const claude = p("Claude Code", "brand:claude", 0xd97757, 0xffffff);
const pager = p("Pager", "file-code", 0x475569, 0xffffff);

const programs = new Map<string, Program>(Object.entries({
  codex: p("Codex", "brand:openai", 0xf7f7f7, 0x1a1a1a, { tileBorder: true }),
  claude,
  node: p("Node", "node-hex", 0x5fa04e, 0xffffff, { tileGlyph: "brand:nodedotjs" }),
  bun: p("Bun", "brand:bun", 0xfbf0df, 0x3b2a20, { tileBorder: true }),
  deno: p("Deno", "brand:deno", 0x1b1b1b, 0xffffff),
  python,
  lazygit: p("Git Changes", "plus-minus-circle", 0x3f8f4f, 0xffffff, { tileGlyph: "diff" }),
  tig: p("Git Log", "git-branch", 0xf05032, 0xffffff),
  git: p("Git", "brand:git", 0xf05032, 0xffffff),
  vim,
  nvim: p("Neovim", "brand:neovim", 0x2b7a3d, 0xffffff),
  hx: p("Helix", "brand:helix", 0x281733, 0xc7a3f5),
  emacs: p("Emacs", "brand:gnuemacs", 0x7f5ab6, 0xffffff),
  htop: activity,
  btop: activity,
  top: activity,
  ssh: globe("SSH"),
  mosh: globe("Mosh"),
  docker: p("Docker", "brand:docker", 0x2496ed, 0xffffff),
  go: p("Go", "brand:go", 0x00add8, 0xffffff),
  cargo: rust("Cargo"),
  rustc: rust("Rust"),
  npm: p("npm", "brand:npm", 0xcb3837, 0xffffff),
  pnpm: p("pnpm", "brand:pnpm", 0xf69220, 0xffffff),
  yarn: p("Yarn", "brand:yarn", 0x2c8ebb, 0xffffff),
  ruby,
  irb: ruby,
  lua: p("Lua", "brand:lua", 0x2c2d72, 0xffffff),
  php: p("PHP", "brand:php", 0x777bb4, 0xffffff),
  psql: p("PostgreSQL", "brand:postgresql", 0x336791, 0xffffff),
  mysql: p("MySQL", "brand:mysql", 0x00758f, 0xffffff),
  "redis-cli": p("Redis", "brand:redis", 0xd82c20, 0xffffff),
  tmux: p("tmux", "brand:tmux", 0x1bb91f, 0xffffff),
  make: p("Make", "cpu", 0x6b7280, 0xffffff),
  swift: p("Swift", "brand:swift", 0xf05138, 0xffffff),
  kotlin: p("Kotlin", "brand:kotlin", 0x7f52ff, 0xffffff),
  omp: p("omp", "brand:omp", 0x0f0a14, 0xb66cff),
  // Windows shells: at their prompt they show as shells, with their own tile.
  pwsh: p("PowerShell", "brand:powershell", 0x5391fe, 0xffffff, { shell: true }),
  powershell: p("PowerShell", "brand:powershell", 0x5391fe, 0xffffff, { shell: true }),
  cmd: p("Command Prompt", "square-terminal", 0x0c0c0c, 0xcccccc, { tileGlyph: "terminal", shell: true }),
  python3: python,
  ipython: python,
  view: vim,
  vi: vim,
  less: pager,
  man: pager,
  // Claude Code reports itself as claude-code in the status protocol.
  "claude-code": claude,
}));

const shells = new Set(["zsh", "bash", "fish", "sh", "dash", "nu", "pwsh", "elvish", "xonsh", "tcsh", "csh", "ksh", "cmd", "powershell"]);

/** The glyphs of assets/icons the tiles use, drawn as the SF Symbols iOS maps them to. */
const symbols = new Set(["activity", "cpu", "diff", "file-code", "git-branch", "globe", "terminal"]);

/** How a program shows, by the name of its process; none is the shell at its prompt. */
export function programOf(process?: string): Program {
  if (!process) return shellTile;
  let name = process.slice(process.lastIndexOf("/") + 1);
  if (name.startsWith("-")) name = name.slice(1);
  const known = programs.get(name.toLowerCase());
  if (known) return known;
  if (shells.has(name)) return { ...shellTile, name };
  // Any other program: its own name, on a tile of a color of its own.
  return p(name, "square-terminal", hashColor(name), 0xffffff, { tileGlyph: "terminal" });
}

/** A color for a name, the same every time: FNV-1a over its bytes. */
function hashColor(s: string): number {
  const palette = [0x6d5bd0, 0x2f80ed, 0x0f9d8a, 0xd9822b, 0xc2417d, 0x3a7d44, 0x8a5a44, 0x4e5d94];
  let h = 2166136261;
  for (const b of new TextEncoder().encode(s)) h = Math.imul(h ^ b, 16777619) >>> 0;
  return palette[h % palette.length];
}

/** The name of a foreground process, from process or process_changed: argv0's, else the name. */
export function programName(fg: any): string | undefined {
  if (!fg || typeof fg !== "object") return undefined;
  let argv0: string | undefined;
  if (typeof fg.argv0 === "string") {
    // argv0 is a path by / or, from a Windows host, by \ with .exe.
    argv0 = fg.argv0.slice(Math.max(fg.argv0.lastIndexOf("/"), fg.argv0.lastIndexOf("\\")) + 1);
    if (argv0!.toLowerCase().endsWith(".exe")) argv0 = argv0!.slice(0, -4);
  }
  return [argv0, fg.name].find((s) => typeof s === "string" && s !== "");
}

const hex = (n: number) => "#" + n.toString(16).padStart(6, "0");

/** The glyph's mask: icons are SVGs of assets/icons, brand marks of assets/brands. */
function glyphUrl(g: string): string {
  return g.startsWith("brand:") ? `brands/${g.slice(6)}.svg` : `icons/${g}.svg`;
}

/** A program's tile, 21×16 DIPs, as the desktop's; its glyph only when it is in front. */
export function tile(program: Program, glyph = true): HTMLElement {
  const t = document.createElement("span");
  t.className = "tile" + (program.tileBorder ? " bordered" : "");
  t.style.setProperty("--tile-bg", hex(program.tileBg));
  if (glyph) {
    const g = program.tileGlyph || program.glyph;
    const i = document.createElement("i");
    i.className = "tile-glyph" + (symbols.has(g) ? " symbol" : "");
    i.style.setProperty("--tile-fg", hex(program.tileFg));
    i.style.setProperty("--glyph", `url(${glyphUrl(g)})`);
    t.append(i);
  }
  return t;
}

/** A CSS pixel is a desktop DIP, so the tiles are drawn at scale 1; tilesWidth is the width of tiles with this many behind. */
const k = 1;
export const tilesWidth = (behind: number) => (21 + 5 * Math.min(behind, 2) + 2) * k;

/** The tiles of a window's programs: the focused block's in front, up to two others peeking out behind it. */
export function tiles(front: string | undefined, back: (string | undefined)[], done: boolean, width: number): HTMLElement {
  const box = document.createElement("span");
  box.className = "tiles";
  box.style.width = width + "px";
  back.slice(0, 2).forEach((b, i) => {
    const t = tile(programOf(b), false);
    const inset = (i + 1) * 0.8 * k;
    t.style.left = k + 5 * (i + 1) * k + "px";
    t.style.top = 2 + inset + "px";
    t.style.height = 16 * k - 2 * inset + "px";
    t.style.zIndex = String(2 - i);
    box.append(t);
  });
  const f = tile(programOf(front));
  f.style.left = k + "px";
  f.style.top = "2px";
  f.style.zIndex = "3";
  if (done) {
    const d = document.createElement("i");
    d.className = "done-dot";
    d.title = t("programDone");
    f.append(d);
  }
  box.append(f);
  return box;
}

/** A program's tile alone, as the title of iOS's terminal screen: small, 0.85 of its size. */
export function smallTile(program: Program): HTMLElement {
  const t = tile(program);
  t.classList.add("small");
  return t;
}

/** A record a program reported (OSC 7501), as program_status gives it. */
export interface Status {
  id: string;
  state: string;
  progress?: number;
  updated?: number;
}

export function statusOf(j: any): Status | undefined {
  if (!j || typeof j.state !== "string") return undefined;
  const t = typeof j.updated_at === "string" ? Date.parse(j.updated_at) : NaN;
  // The server writes Go's zero time for none.
  return { id: j.id ?? "", state: j.state, progress: typeof j.progress === "number" ? j.progress : undefined, updated: t > 0 ? t : undefined };
}

export const rank = (s: Status) => ({ idle: 1, working: 2, done: 3, error: 4, blocked: 5 } as Record<string, number>)[s.state] ?? 0;
/** Whether it needs the user more than another, or as much and later. */
const outranks = (a: Status, b: Status) => rank(a) > rank(b) || (rank(a) === rank(b) && (a.updated ?? 0) > (b.updated ?? 0));

/**
 * The record that most needs the user, the latest of those alike; done and
 * error records updated by seen were seen, and do not count.
 */
export function mostUrgent(rs: Status[], seen: number | undefined): Status | undefined {
  let best: Status | undefined;
  for (const r of rs) {
    if ((r.state === "done" || r.state === "error") && seen !== undefined && (r.updated ?? 0) <= seen) continue;
    if (!best || outranks(r, best)) best = r;
  }
  return best;
}
