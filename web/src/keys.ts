// The window's shortcuts: the desktop's (commands.go) where a browser lets
// a page have them, and Alt+Shift chords for the ones it keeps (Ctrl/Cmd+W,
// Ctrl/Cmd+T, Ctrl+Tab). Keys are matched by their place (code), so that
// a layout or Option's characters do not change them. docs/web-client.md
// lists them, and the Settings dialog's Keyboard section (shortcutGroups).

import { PlainKey, t } from "./i18n";

export const mac = /Mac|iPhone|iPad/.test(navigator.platform);
const windows = /Win/.test(navigator.platform);

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
  // Rename Tab (desktop: Shift+Cmd+R, Ctrl+Shift+R, which reload the page) and New Space (Shift+Cmd+N, Ctrl+Shift+N, a new window).
  t.push([{ alt: true, shift: true, code: "KeyR" }, "rename-window"]);
  t.push([{ alt: true, shift: true, code: "KeyN" }, "new-space"]);
  // Copy, as Ghostty's: Cmd+C on the Mac, Ctrl+Shift+C elsewhere. The pane runs it (pane.ts).
  t.push([mac ? { meta: true, code: "KeyC" } : { ctrl: true, shift: true, code: "KeyC" }, "copy"]);
  // Paste, as the desktop's (GoRex's terminal binds them): Cmd+V on the Mac, Ctrl+Shift+V and
  // Shift+Insert elsewhere. These keys are not encoded: the browser pastes into the pane's
  // text field, and its paste event reaches the program (pane.ts).
  if (mac) {
    t.push([{ meta: true, code: "KeyV" }, "paste"]);
  } else {
    t.push([{ ctrl: true, shift: true, code: "KeyV" }, "paste"]);
    t.push([{ shift: true, code: "Insert" }, "paste"]);
    // On Windows Ctrl+V pastes too, as Windows Terminal's, when the clipboard holds text
    // (the desktop's performable bind): with none, Ctrl+V goes to the program as ^V.
    if (windows) t.push([{ ctrl: true, code: "KeyV" }, "paste-or-key"]);
  }
  // Settings, as the desktop's Accel+, (cmdSettings).
  t.push([mac ? { meta: true, code: "Comma" } : { ctrl: true, code: "Comma" }, "settings"]);
  return t;
}

const chords = table();

/** The command a key press is the shortcut of, if any. */
export function shortcut(e: KeyboardEvent): string | undefined {
  if (e.isComposing) return undefined;
  for (const [c, name] of chords) {
    if (
      e.code === c.code && e.ctrlKey === !!c.ctrl && e.shiftKey === !!c.shift && e.altKey === !!c.alt && e.metaKey === !!c.meta
    ) {
      // A held key repeats only an arrow's focus move and a paste (the browser's, key after key).
      return e.repeat && !c.code.startsWith("Arrow") && !name.startsWith("paste") ? undefined : name;
    }
  }
  return undefined;
}

/** A key's name in a shortcut, as the desktop's menus show it. */
function keyName(code: string): string {
  if (code.startsWith("Key")) return code.slice(3);
  const names: Record<string, string> = {
    Enter: mac ? "↩" : "Enter", ArrowLeft: "←", ArrowRight: "→", ArrowUp: "↑", ArrowDown: "↓",
    BracketLeft: "[", BracketRight: "]", Comma: ",",
  };
  return names[code] ?? code;
}

/** A chord as this system writes it: ⌃⌥⇧⌘D on the Mac, Ctrl+Alt+Shift+D elsewhere. */
function chordName(c: Chord, key = keyName(c.code)): string {
  if (mac) return (c.ctrl ? "⌃" : "") + (c.alt ? "⌥" : "") + (c.shift ? "⇧" : "") + (c.meta ? "⌘" : "") + key;
  return [c.ctrl && "Ctrl", c.alt && "Alt", c.shift && "Shift", c.meta && "Meta", key].filter(Boolean).join("+");
}

/** The keys of a command, or of a command in four directions (name-left, -right, -up, -down) as one: the modifiers and the arrows. */
function keysOf(name: string): string {
  const one = chords.find(([, n]) => n === name);
  if (one) return chordName(one[0]);
  const left = chords.find(([, n]) => n === `${name}-left`);
  return left ? chordName(left[0], "←→↑↓") : "";
}

/**
 * The shortcuts by group, with their titles in the page's language and
 * this system's keys, for the Keyboard section. The keys' names (Ctrl,
 * Shift, ⌘) are the keyboard's own, the same in every language.
 */
export function shortcutGroups(): { label: string; rows: [title: string, keys: string][] }[] {
  const rows = (list: [PlainKey, string][]) => list.map(([title, name]) => [t(title), keysOf(name)] as [string, string]);
  return [
    {
      label: t("keysPanes"),
      rows: rows([
        ["cmdSplitRight", "split-right"],
        ["cmdSplitDown", "split-down"],
        ["cmdZoom", "zoom"],
        ["cmdClosePane", "close"],
        ["cmdFocusPane", "focus"],
        ["cmdMoveDivider", "resize"],
      ]),
    },
    { label: t("keysTerminal"), rows: rows([["commonCopy", "copy"], ["commonPaste", "paste"]]) },
    {
      label: t("keysWindows"),
      rows: rows([
        ["cmdNewWindow", "new-window"],
        ["cmdCloseWindow", "close-window"],
        ["cmdNextWindow", "next-window"],
        ["cmdPreviousWindow", "previous-window"],
        ["cmdRenameWindow", "rename-window"],
      ]),
    },
    { label: t("keysSpaces"), rows: rows([["cmdNewSpace", "new-space"]]) },
    // The product's name, not translated.
    { label: "GoRex", rows: rows([["cmdSettings", "settings"]]) },
  ];
}
