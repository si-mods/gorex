/** Pure terminal-search helpers: finding matches across scrollback lines and mapping to viewport highlights. */

export type FindMatch = {
  /** Screen row (0 = top of scrollback). */
  row: number;
  /** Starting terminal column on that row. */
  col: number;
  /** Highlight width in terminal columns. */
  len: number;
};

function charColumns(cp: number): number {
  if (cp < 0x20) return 0;
  if (cp < 0x7f) return 1;
  // Common East Asian Wide / Fullwidth / Emoji ranges.
  if (
    (cp >= 0x1100 && cp <= 0x115f) ||
    (cp >= 0x2e80 && cp <= 0xa4cf && cp !== 0x303f) ||
    (cp >= 0xac00 && cp <= 0xd7a3) ||
    (cp >= 0xf900 && cp <= 0xfaff) ||
    (cp >= 0xfe10 && cp <= 0xfe6f) ||
    (cp >= 0xff00 && cp <= 0xff60) ||
    (cp >= 0xffe0 && cp <= 0xffe6) ||
    (cp >= 0x1f300 && cp <= 0x1faff) ||
    (cp >= 0x20000 && cp <= 0x2fffd)
  ) {
    return 2;
  }
  return 1;
}

function columnsOf(s: string): number {
  let cols = 0;
  for (const ch of s) cols += charColumns(ch.codePointAt(0) ?? 0);
  return cols;
}

/** Finds all case-insensitive occurrences of query across lines, returning screen-row/column spans. */
export function findMatches(lines: readonly string[], query: string): FindMatch[] {
  if (!query) return [];
  const needle = query.toLowerCase();
  const out: FindMatch[] = [];
  for (let row = 0; row < lines.length; row++) {
    const line = lines[row];
    const hay = line.toLowerCase();
    let from = 0;
    while (from <= hay.length - needle.length) {
      const idx = hay.indexOf(needle, from);
      if (idx === -1) break;
      const col = columnsOf(line.slice(0, idx));
      const len = Math.max(1, columnsOf(line.slice(idx, idx + needle.length)));
      out.push({ row, col, len });
      from = idx + needle.length;
    }
  }
  return out;
}

/** Steps the active match index forward (+1) or backward (-1), wrapping around. */
export function stepMatch(current: number, total: number, delta: 1 | -1): number {
  if (total <= 0) return -1;
  if (current < 0 || current >= total) return delta > 0 ? 0 : total - 1;
  return (current + delta + total) % total;
}

/** Projects screen-coordinate matches into viewport-relative highlights for Terminal.draw(). */
export function viewportHighlights(
  matches: readonly FindMatch[],
  activeIndex: number,
  viewportTop: number,
  viewportRows: number,
): { row: number; col: number; len: number; current: boolean }[] {
  const out: { row: number; col: number; len: number; current: boolean }[] = [];
  for (let i = 0; i < matches.length; i++) {
    const m = matches[i];
    const row = m.row - viewportTop;
    if (row < 0 || row >= viewportRows) continue;
    out.push({ row, col: m.col, len: m.len, current: i === activeIndex });
  }
  return out;
}
