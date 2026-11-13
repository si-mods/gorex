/** Pure keyboard-chord helpers: serialization, validation, and conflict detection (keys.ts). */

export type Chord = {
  ctrl?: boolean;
  shift?: boolean;
  alt?: boolean;
  meta?: boolean;
  code: string;
};

const modifierCodes = new Set([
  "ShiftLeft", "ShiftRight", "ControlLeft", "ControlRight",
  "AltLeft", "AltRight", "MetaLeft", "MetaRight", "CapsLock",
]);

export function encodeChord(c: Chord): string {
  const parts: string[] = [];
  if (c.ctrl) parts.push("Ctrl");
  if (c.alt) parts.push("Alt");
  if (c.shift) parts.push("Shift");
  if (c.meta) parts.push("Meta");
  parts.push(c.code);
  return parts.join("+");
}

export function parseChord(s: string): Chord | undefined {
  if (!s) return undefined;
  const parts = s.split("+").filter(Boolean);
  if (parts.length === 0) return undefined;
  const code = parts[parts.length - 1];
  if (!code || modifierCodes.has(code)) return undefined;
  const c: Chord = { code };
  for (let i = 0; i < parts.length - 1; i++) {
    switch (parts[i]) {
      case "Ctrl": c.ctrl = true; break;
      case "Alt": c.alt = true; break;
      case "Shift": c.shift = true; break;
      case "Meta": c.meta = true; break;
      default: return undefined;
    }
  }
  return c;
}

export function sameChord(a: Chord, b: Chord): boolean {
  return (
    a.code === b.code &&
    !!a.ctrl === !!b.ctrl &&
    !!a.alt === !!b.alt &&
    !!a.shift === !!b.shift &&
    !!a.meta === !!b.meta
  );
}

export function chordFromEvent(e: Pick<KeyboardEvent, "ctrlKey" | "altKey" | "shiftKey" | "metaKey" | "code">): Chord | undefined {
  if (!e.code || modifierCodes.has(e.code)) return undefined;
  return {
    ctrl: e.ctrlKey || undefined,
    alt: e.altKey || undefined,
    shift: e.shiftKey || undefined,
    meta: e.metaKey || undefined,
    code: e.code,
  };
}

export function hasModifierOrFKey(c: Chord): boolean {
  if (c.ctrl || c.alt || c.shift || c.meta) return true;
  return /^F([1-9]|1[0-2])$/.test(c.code);
}

/** Shortcuts that browsers hard-reserve and do not reliably deliver to web pages. */
export function isBrowserReserved(c: Chord, mac: boolean): boolean {
  const accel = mac ? (!!c.meta && !c.ctrl) : (!!c.ctrl && !c.meta);
  if (accel && !c.alt) {
    if (!c.shift && (c.code === "KeyW" || c.code === "KeyT" || c.code === "KeyN" || c.code === "KeyR")) return true;
    if (c.shift && (c.code === "KeyN" || c.code === "KeyR")) return true;
    if (mac && !c.shift && c.code === "KeyQ") return true;
  }
  if (c.ctrl && !c.alt && !c.meta && c.code === "Tab") return true;
  return false;
}

export type ValidateResult =
  | { ok: true }
  | { ok: false; reason: "need-modifier" | "browser-reserved" }
  | { ok: false; reason: "conflict"; owner: string };

export function validateBinding(
  id: string,
  chord: Chord,
  entries: readonly { id: string; chord: Chord }[],
  mac: boolean,
): ValidateResult {
  if (!hasModifierOrFKey(chord)) return { ok: false, reason: "need-modifier" };
  if (isBrowserReserved(chord, mac)) return { ok: false, reason: "browser-reserved" };
  const clash = entries.find((e) => e.id !== id && sameChord(e.chord, chord));
  if (clash) return { ok: false, reason: "conflict", owner: clash.id };
  return { ok: true };
}
