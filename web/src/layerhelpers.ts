/** Pure layout/layer helpers for tiled and floating layers in session.view (window.ts). */

export interface LayoutNode {
  block_id?: string;
  split?: { split_id: string; direction: "horizontal" | "vertical"; ratio: number; before: LayoutNode; after: LayoutNode };
}

export type Rect = [number, number, number, number];

export type FloatingLayer = {
  layerId: string;
  bounds: Rect;
  minimized: boolean;
  blocks: string[];
  zIndex: number;
};

/** The tiled layout of a window from session.view: its tree, or its one block. */
export function layoutOf(w: any): LayoutNode | undefined {
  const layers: any[] = w?.layers ?? [];
  const tiled = layers.find((l) => l.kind === "tiled") ?? layers[0];
  if (tiled?.layout && (tiled.layout.block_id || tiled.layout.split)) return tiled.layout;
  const first = tiled?.blocks?.[0]?.block_id;
  return first ? { block_id: first } : undefined;
}

/** The blocks of a tree, in layout order. */
export function leavesOf(n?: LayoutNode): string[] {
  if (!n) return [];
  if (n.block_id) return [n.block_id];
  return n.split ? [...leavesOf(n.split.before), ...leavesOf(n.split.after)] : [];
}

function clampBounds(raw: unknown): Rect {
  if (!Array.isArray(raw) || raw.length !== 4 || !raw.every((n) => typeof n === "number" && Number.isFinite(n))) {
    return [0.15, 0.15, 0.7, 0.7];
  }
  const w = Math.max(0.08, Math.min(1, raw[2]));
  const h = Math.max(0.08, Math.min(1, raw[3]));
  const x = Math.max(0, Math.min(1 - w, raw[0]));
  const y = Math.max(0, Math.min(1 - h, raw[1]));
  return [x, y, w, h];
}

/** Floating layers of a window, back to front, with normalized bounds and block IDs. */
export function floatingLayersOf(w: any): FloatingLayer[] {
  const layers: any[] = w?.layers ?? [];
  const tiled = layers.find((l) => l.kind === "tiled") ?? layers[0];
  const out: FloatingLayer[] = [];
  for (let i = 0; i < layers.length; i++) {
    const l = layers[i];
    if (!l || l === tiled || l.kind === "tiled") continue;
    const blocks: string[] = l.layout
      ? leavesOf(l.layout)
      : (l.blocks ?? []).map((b: any) => b?.block_id).filter((id: unknown): id is string => typeof id === "string");
    if (blocks.length === 0) continue;
    out.push({
      layerId: typeof l.layer_id === "string" ? l.layer_id : `float-${i}`,
      bounds: clampBounds(l.bounds),
      minimized: l.state === "minimized",
      blocks,
      zIndex: 10 + out.length,
    });
  }
  return out;
}
