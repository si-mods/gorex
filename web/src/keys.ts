// The window's shortcuts: the desktop's (commands.go) where a browser lets
// a page have them, and Alt+Shift chords for the ones it keeps (Ctrl/Cmd+W,
// Ctrl/Cmd+T, Ctrl+Tab). Keys are matched by their place (code), so that
// a layout or Option's characters do not change them. docs/web-client.md
// lists them.

const mac = /Mac|iPhone|iPad/.test(navigator.platform);

/** A chord: the modifiers held, exactly, and the key's code. */
type Chord = { ctrl?: boolean; shift?: boolean; alt?: boolean; meta?: boolean; code: string };

const arrows = { Left: "ArrowLeft", Right: "ArrowRight", Up: "ArrowUp", Down: "ArrowDown" } as const;

function table(): [Chord, string][] {
  const t: [Chord, string][] = [];
  if (mac) {
    // The desktop's Accel keys on the Mac.
    t.push([{ meta: true, code: "KeyD" }, "split-right"]);
    t.push([{ meta: true, shift: true, code: "KeyD" }, "split-down"]);
    t.push([{ meta: true, shift: true, code: "Enter" }, "zoom"]);
    for (const [d, code] of Object.entries(arrows)) {
      t.push([{ meta: true, alt: true, code }, `focus-${d.toLowerCase()}`]);
      t.push([{ meta: true, ctrl: true, code }, `resize-${d.toLowerCase()}`]);
    }
  } else {
    // The desktop's other keys (Other in commands.go), on Windows and Linux.
    t.push([{ ctrl: true, shift: true, code: "KeyD" }, "split-right"]);
    t.push([{ ctrl: true, shift: true, code: "KeyE" }, "split-down"]);
    t.push([{ ctrl: true, shift: true, code: "Enter" }, "zoom"]);
    for (const [d, code] of Object.entries(arrows)) {
      t.push([{ alt: true, code }, `focus-${d.toLowerCase()}`]);
      t.push([{ alt: true, shift: true, code }, `resize-${d.toLowerCase()}`]);
    }
  }
  // What the browser keeps for itself, on every platform.
  t.push([{ alt: true, shift: true, code: "KeyW" }, "close"]);
  t.push([{ alt: true, shift: true, code: "KeyT" }, "new-window"]);
  t.push([{ alt: true, shift: true, code: "KeyQ" }, "close-window"]);
  t.push([{ alt: true, shift: true, code: "BracketRight" }, "next-window"]);
  t.push([{ alt: true, shift: true, code: "BracketLeft" }, "previous-window"]);
  return t;
}

const chords = table();

/** The command a key press is the shortcut of, if any. */
export function shortcut(e: KeyboardEvent): string | undefined {
  if (e.isComposing || e.repeat && !e.code.startsWith("Arrow")) return undefined;
  for (const [c, name] of chords) {
    if (
      e.code === c.code && e.ctrlKey === !!c.ctrl && e.shiftKey === !!c.shift && e.altKey === !!c.alt && e.metaKey === !!c.meta
    ) return name;
  }
  return undefined;
}
