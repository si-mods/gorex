---
name: REF-011
description: "How GoRex reads Ghostty's config: the files, the options it maps, the ones it leaves out, and precedence against Settings. Use when you change Ghostty config handling or add an option."
metadata:
  created: "2026-11-24"
  updated: "2026-11-24"
---

# REF-011: The Ghostty configuration

Status: reference. It describes the code in main.

GoRex reads Ghostty's config. The file is `$XDG_CONFIG_HOME/ghostty/config.ghostty` (`config`), else `%LOCALAPPDATA%` on Windows or `~/.config`. On macOS it also reads `~/Library/Application Support/com.mitchellh.ghostty/`. `config-file` includes work. `GOREX_GHOSTTY_CONFIG` names one file instead.

Supported: `font-family`, `font-size`, `font-feature`, `font-thicken`, `adjust-cell-height` (percent), `theme` (also `light:…,dark:…`), `background`, `foreground`, `cursor-color`, `cursor-text`, `selection-background`, `selection-foreground`, `palette` (0–15), `cursor-style`, `cursor-style-blink`, `scrollback-limit`, `macos-option-as-alt`, `copy-on-select` (`clipboard`. `true` is Linux's selection clipboard, so it copies nothing), `right-click-action`, `minimum-contrast`, `clipboard-read`, `clipboard-write`, `window-padding-x`, `window-padding-y`, `window-padding-color`, `image-storage-limit`, `search-background`, `search-foreground`, `search-selected-background`, `search-selected-foreground`, `keybind`.

`keybind` takes `clear`, `performable:`, and the actions unbind, ignore, copy_to_clipboard, paste_from_clipboard, select_all, clear_screen, increase_font_size, decrease_font_size, reset_font_size, and scroll_page_up, scroll_page_down, scroll_to_top, scroll_to_bottom. `global:`, `all:`, and `unconsumed:` binds, sequences (`a>b`), and other actions are skipped. GoRex's own shortcuts come before keybinds. A text size set with ⌘+ ⌘− wins over `font-size`. ⌘0 returns to `font-size`. With a theme or background, panes draw its background. Unless View ▸ Appearance names one, the window turns dark or light by it, as Ghostty's `window-theme = auto` does. `light:…,dark:…` does not do this. It follows the system. "Reload Ghostty Config" (command palette, View menu) applies fonts, themes, `copy-on-select`, and `minimum-contrast` to open panes. The other options apply to new panes.

`minimum-contrast` is 3 unless Ghostty's config or Settings ▸ Terminal ▸ Minimum contrast (`terminal.minimumContrast` in settings.json) sets it. Text closer than that to its background, colors programs set too (ANSI, 256 and RGB), turns lighter or darker in its hue, in local and remote panes alike. Box drawing keeps its colors. 1 turns the correction off. Unset, Ghostty's is 1, so GoRex corrects colors where Ghostty would not.

Options GoRex maps fully: the colors and fonts above, `cursor-style`, `cursor-style-blink`, `scrollback-limit`, `macos-option-as-alt`, `copy-on-select`, `right-click-action`, `minimum-contrast`, `clipboard-read` (allow, ask or deny), `clipboard-write` (allow or deny. GoRex treats ask as deny), the first value of `window-padding-x` and `window-padding-y` (the library has one width and one height), `window-padding-color`, `image-storage-limit` (0 refuses images), and the supported `keybind`s. Options it reads but leaves out, as the library has no mapping: `background-opacity`, `background-blur`, `mouse-hide-while-typing`, `scroll-multiplier`, `window-padding-balance`, and window decorations. Other options are ignored. `gorex config check` lists the Ghostty options it ignores.

## Precedence

What you set in Settings wins over Ghostty's config, which wins over GoRex's defaults. "Reload Ghostty Config" reads the config again but never replaces a choice made in Settings. "Use Ghostty config" (Terminal) forgets the font, cursor, and copy-on-selection choices. Theme's "Follow Ghostty config" forgets the theme's. A theme chosen in Settings also replaces the config's `background`, `foreground`, `palette`, and the other colors, which belonged to its own theme. Font, theme, copy on selection, and the cursor apply to open panes at once.
