// The desktop's spaces across hosts, outside Rex's API (docs/rex-api.md,
// Spaces across hosts): a gateway that lists gorex.spaces answers
// GET /api/spaces with its machine and, for each session whose space holds
// panes of other machines, those panes. CrossHost is a port of Rex Go's
// (ios/GoRex/Rex/Spaces.swift) with the same rules; it has no DOM.
import type { Transport } from "./rex";

/** The welcome's feature of a gateway that answers /api/spaces. */
export const feature = "gorex.spaces";

/** A pane of another machine in a space: its machine and its block there. */
export interface Member {
  machine: string;
  block_id: string;
}

/** /api/spaces's answer, as the gateway writes it. */
export interface Answer {
  machine_id: string;
  sessions: { session_id: string; remote_blocks: Member[] }[];
}

/** Reads an answer's JSON; it throws for one without a machine. */
export function parse(j: any): Answer {
  if (!j || typeof j.machine_id !== "string") throw new Error("spaces: no machine_id");
  const sessions = (Array.isArray(j.sessions) ? j.sessions : []).map((s: any) => ({
    session_id: String(s?.session_id ?? ""),
    remote_blocks: (Array.isArray(s?.remote_blocks) ? s.remote_blocks : [])
      .filter((m: any) => typeof m?.machine === "string" && typeof m?.block_id === "string")
      .map((m: any) => ({ machine: m.machine, block_id: m.block_id })),
  }));
  return { machine_id: j.machine_id, sessions };
}

/** Reads the spaces of the host behind transport, as Spaces.read: 10 s at most, 200 only. */
export async function read(transport: Transport, signal?: AbortSignal): Promise<Answer> {
  const timeout = AbortSignal.timeout(10000);
  const r = await transport.fetch("/api/spaces", { signal: signal ? AbortSignal.any([signal, timeout]) : timeout });
  if (r.status !== 200) throw new Error(`spaces: ${r.status} ${r.statusText}`.trim());
  return parse(await r.json());
}

/** A host's session. */
export interface Place {
  host: string;
  session: string;
}

/** A host's window, and the session it is in there. */
export interface Row extends Place {
  window: string;
}

/** A host as the grouping reads it: what the list shows of it. */
export interface Source {
  /** The host's ID (Host.id). */
  host: string;
  /** Its machine_id; none for a server without gorex.spaces, or before it answered. */
  machine?: string;
  sessions: { id: string; view?: any; remote: Member[] }[];
}

/** Every block of every layer of a window (Spaces.swift: SessionView.Window.blocks). */
export function blocksOf(w: any): string[] {
  return (w?.layers ?? []).flatMap((l: any) => (l?.blocks ?? []).map((b: any) => b.block_id));
}

const placeKey = (host: string, session: string) => JSON.stringify([host, session]);
const rowKey = (host: string, session: string, window: string) => JSON.stringify([host, session, window]);

/**
 * Where each window shows, grouped by the desktop's spaces: a window of
 * one host that a space of another host holds shows in that space, and
 * not in its own host's. Each window shows once: the first space to hold
 * it, in the hosts' order, has it.
 */
export class CrossHost {
  private joined = new Map<string, Row[]>();
  private moved = new Set<string>();

  constructor(sources: Source[] = []) {
    // Where each block is, by machine: the first host of a machine
    // reached by two addresses has it.
    const at = new Map<string, Map<string, Row>>();
    for (const h of sources) {
      if (h.machine === undefined || at.has(h.machine)) continue;
      const blocks = new Map<string, Row>();
      for (const s of h.sessions) {
        for (const w of s.view?.windows ?? []) {
          for (const b of blocksOf(w)) if (!blocks.has(b)) blocks.set(b, { host: h.host, session: s.id, window: w.window_id });
        }
      }
      at.set(h.machine, blocks);
    }
    for (const h of sources) {
      for (const s of h.sessions) {
        for (const m of s.remote) {
          const row = at.get(m.machine)?.get(m.block_id);
          if (!row || row.host === h.host) continue;
          const k = rowKey(row.host, row.session, row.window);
          if (this.moved.has(k)) continue;
          this.moved.add(k);
          const p = placeKey(h.host, s.id);
          this.joined.set(p, [...(this.joined.get(p) ?? []), row]);
        }
      }
    }
  }

  /** The windows of other hosts a session shows, in its space's order. */
  rows(host: string, session: string): Row[] {
    return this.joined.get(placeKey(host, session)) ?? [];
  }

  /** Whether a host's window shows in another host's session instead. */
  isMoved(host: string, session: string, window: string): boolean {
    return this.moved.has(rowKey(host, session, window));
  }

  /**
   * Whether a host's session is no space of its own: every window of it
   * shows in another host's space, and it holds none of another's.
   */
  isGone(host: string, session: string, windows: string[]): boolean {
    return windows.length > 0 && this.rows(host, session).length === 0 && windows.every((w) => this.isMoved(host, session, w));
  }

  /** Whether nothing joins or moves. */
  get empty(): boolean {
    return this.joined.size === 0;
  }

  /** How many windows moved. */
  get movedCount(): number {
    return this.moved.size;
  }
}
