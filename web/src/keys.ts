// The window's shortcuts: the desktop's (commands.go) where a browser lets
// a page have them, and Alt+Shift chords for the ones it keeps (Ctrl/Cmd+W,
// Ctrl/Cmd+T, Ctrl+Tab). Keys are matched by their place (code), so that
// a layout or Option's characters do not change them. docs/web-client.md
// lists them, and the Settings dialog's Keyboard section (shortcutGroups).

import { PlainKey, t } from "./i18n";
import {
  Chord, encodeChord, parseChord, sameChord, validateBinding, ValidateResult,
} from "./keyhelpers";
import { prefs, setPrefs } from "./prefs";

export type { Chord, ValidateResult };
export { chordFromEvent, encodeChord, parseChord, sameChord } from "./keyhelpers";

export const mac = /Mac|iPhone|iPad/.test(navigator.platform);
const windows = /Win/.test(navigator.platform);

const arrows = { Left: "ArrowLeft", Right: "ArrowRight", Up: "ArrowUp", Down: "ArrowDown" } as const;

let recording = false;

export function setRecordingShortcuts(on: boolean) {
  recording = on;
}

export function isRecordingShortcuts(): boolean {
  return recording;
}

function defaultTable(): [Chord, string][] {
  const list: [Chord, string][] = [];
  if (mac) {
    // The desktop's Accel keys on the Mac.
    list.push([{ meta: true, code: "KeyD" }, "split-right"]);
    list.push([{ meta: true, shift: true, code: "KeyD" }, "split-down"]);
    list.push([{ meta: true, shift: true, code: "Enter" }, "zoom"]);
    for (const [d, code] of Object.entries(arrows)) {
      list.push([{ meta: true, alt: true, code }, `focus-${d.toLowerCase()}`]);
      list.push([{ meta: true, ctrl: true, code }, `resize-${d.toLowerCase()}`]);
    }
    // Next and previous space, the desktop's own keys.
    list.push([{ ctrl: true, meta: true, code: "BracketRight" }, "next-space"]);
    list.push([{ ctrl: true, meta: true, code: "BracketLeft" }, "previous-space"]);
  } else {
    // The desktop's other keys (Other in commands.go), on Windows and Linux.
    list.push([{ ctrl: true, shift: true, code: "KeyD" }, "split-right"]);
    list.push([{ ctrl: true, shift: true, code: "KeyE" }, "split-down"]);
    list.push([{ ctrl: true, shift: true, code: "Enter" }, "zoom"]);
    for (const [d, code] of Object.entries(arrows)) {
      list.push([{ alt: true, code }, `focus-${d.toLowerCase()}`]);
      list.push([{ alt: true, shift: true, code }, `resize-${d.toLowerCase()}`]);
    }
    // Next and previous space: the desktop's Ctrl+Shift+PageDown and PageUp move the browser's tabs.
    list.push([{ alt: true, shift: true, code: "PageDown" }, "next-space"]);
    list.push([{ alt: true, shift: true, code: "PageUp" }, "previous-space"]);
  }
  // What the browser keeps for itself, on every platform.
  list.push([{ alt: true, shift: true, code: "KeyW" }, "close"]);
  list.push([{ alt: true, shift: true, code: "KeyT" }, "new-window"]);
  list.push([{ alt: true, shift: true, code: "KeyQ" }, "close-window"]);
  list.push([{ alt: true, shift: true, code: "BracketRight" }, "next-window"]);
  list.push([{ alt: true, shift: true, code: "BracketLeft" }, "previous-window"]);
  // Rename Tab (desktop: Shift+Cmd+R, Ctrl+Shift+R, which reload the page) and New Space (Shift+Cmd+N, Ctrl+Shift+N, a new window).
  list.push([{ alt: true, shift: true, code: "KeyR" }, "rename-window"]);
  list.push([{ alt: true, shift: true, code: "KeyN" }, "new-space"]);
  // Find in terminal scrollback (commands.go: cmdFind, cmdFindNext, cmdFindPrev).
  list.push([mac ? { meta: true, code: "KeyF" } : { ctrl: true, shift: true, code: "KeyF" }, "find"]);
  list.push([mac ? { meta: true, code: "KeyG" } : { ctrl: true, shift: true, code: "KeyG" }, "find-next"]);
  list.push([mac ? { meta: true, shift: true, code: "KeyG" } : { alt: true, shift: true, code: "KeyG" }, "find-previous"]);
  // Copy, as Ghostty's: Cmd+C on the Mac, Ctrl+Shift+C elsewhere. The pane runs it (pane.ts).
  list.push([mac ? { meta: true, code: "KeyC" } : { ctrl: true, shift: true, code: "KeyC" }, "copy"]);
  // Paste, as the desktop's (GoRex's terminal binds them): Cmd+V on the Mac, Ctrl+Shift+V and
  // Shift+Insert elsewhere.
  if (mac) {
    list.push([{ meta: true, code: "KeyV" }, "paste"]);
  } else {
    list.push([{ ctrl: true, shift: true, code: "KeyV" }, "paste"]);
    list.push([{ shift: true, code: "Insert" }, "paste"]);
    if (windows) list.push([{ ctrl: true, code: "KeyV" }, "paste-or-key"]);
  }
  // Command Palette & Settings (commands.go: cmdPalette, cmdSettings).
  list.push([mac ? { meta: true, shift: true, code: "KeyP" } : { ctrl: true, shift: true, code: "KeyP" }, "command-palette"]);
  list.push([mac ? { meta: true, code: "Comma" } : { ctrl: true, code: "Comma" }, "settings"]);
  return list;
}

const defaults = defaultTable();

/** Active chord list with user overrides from prefs.shortcuts applied. */
export function activeChords(): [Chord, string][] {
  const custom = prefs.shortcuts ?? {};
  const overridden = new Set(Object.keys(custom));
  const out: [Chord, string][] = [];
  for (const [chord, name] of defaults) {
    if (overridden.has(name)) continue;
    out.push([chord, name]);
  }
  for (const [name, raw] of Object.entries(custom)) {
    const parsed = parseChord(raw);
    if (parsed) out.push([parsed, name]);
  }
  return out;
}

/** The command a key press is the shortcut of, if any. */
export function shortcut(e: KeyboardEvent): string | undefined {
  if (e.isComposing || recording) return undefined;
  for (const [c, name] of activeChords()) {
    if (sameChord(c, { ctrl: e.ctrlKey, shift: e.shiftKey, alt: e.altKey, meta: e.metaKey, code: e.code })) {
      return e.repeat && !c.code.startsWith("Arrow") && !name.startsWith("paste") ? undefined : name;
    }
  }
  return undefined;
}

/** A key's name in a shortcut, as the desktop's menus show it. */
function keyName(code: string): string {
  if (code.startsWith("Key")) return code.slice(3);
  if (code.startsWith("Digit")) return code.slice(5);
  const names: Record<string, string> = {
    Enter: mac ? "↩" : "Enter", ArrowLeft: "←", ArrowRight: "→", ArrowUp: "↑", ArrowDown: "↓",
    BracketLeft: "[", BracketRight: "]", Comma: ",", Period: ".", Slash: "/", Semicolon: ";",
    Quote: "'", Backquote: "`", Minus: "-", Equal: "=", Backslash: "\\", Space: "Space",
    PageUp: "PgUp", PageDown: "PgDn",
  };
  return names[code] ?? code;
}

/** A chord as this system writes it: ⌃⌥⇧⌘D on the Mac, Ctrl+Alt+Shift+D elsewhere. */
export function chordName(c: Chord, key = keyName(c.code)): string {
  if (mac) return (c.ctrl ? "⌃" : "") + (c.alt ? "⌥" : "") + (c.shift ? "⇧" : "") + (c.meta ? "⌘" : "") + key;
  return [c.ctrl && "Ctrl", c.alt && "Alt", c.shift && "Shift", c.meta && "Meta", key].filter(Boolean).join("+");
}

/** The keys of a command, or of a command in four directions (name-left, -right, -up, -down) as one. */
export function keysOf(name: string): string {
  const list = activeChords();
  const one = list.find(([, n]) => n === name);
  if (one) return chordName(one[0]);
  const left = list.find(([, n]) => n === `${name}-left`);
  return left ? chordName(left[0], "←→↑↓") : "";
}

const commandTitleKeys: Record<string, PlainKey> = {
  "split-right": "cmdSplitRight",
  "split-down": "cmdSplitDown",
  "zoom": "cmdZoom",
  "close": "cmdClosePane",
  "focus-left": "cmdFocusLeft",
  "focus-right": "cmdFocusRight",
  "focus-up": "cmdFocusUp",
  "focus-down": "cmdFocusDown",
  "resize-left": "cmdResizeLeft",
  "resize-right": "cmdResizeRight",
  "resize-up": "cmdResizeUp",
  "resize-down": "cmdResizeDown",
  "copy": "commonCopy",
  "paste": "commonPaste",
  "find": "cmdFind",
  "find-next": "findNext",
  "find-previous": "findPrevious",
  "new-window": "cmdNewWindow",
  "close-window": "cmdCloseWindow",
  "next-window": "cmdNextWindow",
  "previous-window": "cmdPreviousWindow",
  "rename-window": "cmdRenameWindow",
  "new-space": "cmdNewSpace",
  "next-space": "cmdNextSpace",
  "previous-space": "cmdPreviousSpace",
  "command-palette": "cmdCommandPalette",
  "settings": "cmdSettings",
};

export function commandTitle(id: string): string {
  const k = commandTitleKeys[id];
  return k ? t(k) : id;
}

export function isShortcutModified(id: string): boolean {
  return !!prefs.shortcuts?.[id];
}

export function hasModifiedShortcuts(): boolean {
  return !!prefs.shortcuts && Object.keys(prefs.shortcuts).length > 0;
}

export function trySetShortcut(id: string, chord: Chord): ValidateResult {
  const entries = activeChords().map(([c, name]) => ({ id: name, chord: c }));
  const res = validateBinding(id, chord, entries, mac);
  if (!res.ok) return res;
  const def = defaults.find(([, n]) => n === id)?.[0];
  const next = { ...(prefs.shortcuts ?? {}) };
  if (def && sameChord(def, chord)) {
    delete next[id];
  } else {
    next[id] = encodeChord(chord);
  }
  setPrefs({ shortcuts: Object.keys(next).length > 0 ? next : undefined });
  return res;
}

export function resetShortcut(id: string) {
  if (!prefs.shortcuts?.[id]) return;
  const next = { ...prefs.shortcuts };
  delete next[id];
  setPrefs({ shortcuts: Object.keys(next).length > 0 ? next : undefined });
}

export function resetAllShortcuts() {
  if (!hasModifiedShortcuts()) return;
  setPrefs({ shortcuts: undefined });
}

export type ShortcutRow = {
  id: string;
  title: string;
  keys: string;
  editable: boolean;
  modified: boolean;
};

/**
 * The shortcuts by group, with their titles in the page's language and
 * this system's keys, for the Keyboard section.
 */
export function shortcutGroups(): { label: string; rows: ShortcutRow[] }[] {
  const row = (id: string, title: string, editable = true): ShortcutRow => ({
    id,
    title,
    keys: keysOf(id),
    editable,
    modified: editable && isShortcutModified(id),
  });
  return [
    {
      label: t("keysPanes"),
      rows: [
        row("split-right", t("cmdSplitRight")),
        row("split-down", t("cmdSplitDown")),
        row("zoom", t("cmdZoom")),
        row("close", t("cmdClosePane")),
        row("focus", t("cmdFocusPane"), false),
        row("resize", t("cmdMoveDivider"), false),
      ],
    },
    {
      label: t("keysTerminal"),
      rows: [
        row("copy", t("commonCopy")),
        row("paste", t("commonPaste")),
        row("find", t("cmdFind")),
        row("find-next", t("findNext")),
        row("find-previous", t("findPrevious")),
      ],
    },
    {
      label: t("keysWindows"),
      rows: [
        row("new-window", t("cmdNewWindow")),
        row("close-window", t("cmdCloseWindow")),
        row("next-window", t("cmdNextWindow")),
        row("previous-window", t("cmdPreviousWindow")),
        row("rename-window", t("cmdRenameWindow")),
      ],
    },
    {
      label: t("keysSpaces"),
      rows: [
        row("new-space", t("cmdNewSpace")),
        row("next-space", t("cmdNextSpace")),
        row("previous-space", t("cmdPreviousSpace")),
      ],
    },
    {
      label: "GoRex",
      rows: [
        row("command-palette", t("cmdCommandPalette")),
        row("settings", t("cmdSettings")),
      ],
    },
  ];
}
