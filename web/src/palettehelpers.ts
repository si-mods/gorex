/** Pure filtering/ranking helpers for the Command Palette (palette.ts). */

export type PaletteItem = {
  id: string;
  title: string;
  detail?: string;
  keys?: string;
  run: () => void;
};

/**
 * Filters palette items by space-separated case-insensitive query tokens,
 * preferring prefix/title matches before detail matches.
 */
export function filterPaletteItems(items: readonly PaletteItem[], query: string): PaletteItem[] {
  const tokens = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length === 0) return [...items];

  const scored: { item: PaletteItem; score: number }[] = [];
  for (const item of items) {
    const title = item.title.toLowerCase();
    const detail = (item.detail ?? "").toLowerCase();
    const hay = `${title} ${detail}`;
    if (!tokens.every((tok) => hay.includes(tok))) continue;
    let score = 0;
    if (tokens.every((tok) => title.includes(tok))) score += 2;
    if (title.startsWith(tokens[0])) score += 3;
    scored.push({ item, score });
  }
  scored.sort((a, b) => b.score - a.score);
  return scored.map((s) => s.item);
}
