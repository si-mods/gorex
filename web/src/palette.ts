// Command palette (palette.ts), matching desktop's cmdCommandPalette (commands.go).

import { localize, t } from "./i18n";
import { filterPaletteItems, PaletteItem } from "./palettehelpers";

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, text?: string) => {
  const e = document.createElement(tag);
  e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
};

export type { PaletteItem };

export class CommandPalette {
  private backdrop?: HTMLElement;
  private input?: HTMLInputElement;
  private list?: HTMLElement;
  private items: PaletteItem[] = [];
  private shown: PaletteItem[] = [];
  private selected = 0;

  constructor(
    private readonly provide: () => PaletteItem[],
    private readonly onDismiss: () => void,
  ) {}

  isOpen(): boolean {
    return !!this.backdrop;
  }

  toggle() {
    if (this.backdrop) this.close();
    else this.open();
  }

  open() {
    if (this.backdrop) {
      this.input?.focus();
      this.input?.select();
      return;
    }
    this.items = this.provide();
    this.selected = 0;

    const backdrop = el("div", "palette-backdrop");
    const box = el("div", "palette");
    box.setAttribute("role", "dialog");
    box.setAttribute("aria-modal", "true");
    localize(box, (b) => b.setAttribute("aria-label", t("cmdCommandPalette")));

    const input = el("input", "palette-input");
    input.type = "search";
    input.autocomplete = "off";
    input.spellcheck = false;
    localize(input, (i) => {
      i.placeholder = t("palettePlaceholder");
      i.setAttribute("aria-label", t("palettePlaceholder"));
    });

    const list = el("div", "palette-list");
    list.setAttribute("role", "listbox");

    box.append(input, list);
    backdrop.append(box);
    document.body.append(backdrop);

    this.backdrop = backdrop;
    this.input = input;
    this.list = list;

    backdrop.addEventListener("pointerdown", (e) => {
      if (e.target === backdrop) this.close();
    });
    input.addEventListener("input", () => {
      this.selected = 0;
      this.render();
    });
    input.addEventListener("keydown", (e) => this.key(e));

    this.render();
    input.focus();
  }

  close() {
    if (!this.backdrop) return;
    this.backdrop.remove();
    this.backdrop = undefined;
    this.input = undefined;
    this.list = undefined;
    this.items = [];
    this.shown = [];
    this.onDismiss();
  }

  private key(e: KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      this.close();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      if (this.shown.length > 0) {
        this.selected = (this.selected + 1) % this.shown.length;
        this.render();
      }
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      if (this.shown.length > 0) {
        this.selected = (this.selected - 1 + this.shown.length) % this.shown.length;
        this.render();
      }
    } else if (e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      const item = this.shown[this.selected];
      if (item) {
        this.close();
        item.run();
      }
    }
  }

  private render() {
    if (!this.list || !this.input) return;
    this.shown = filterPaletteItems(this.items, this.input.value);
    if (this.selected >= this.shown.length) this.selected = Math.max(0, this.shown.length - 1);
    this.list.replaceChildren();
    if (this.shown.length === 0) {
      this.list.append(el("div", "palette-empty", t("paletteNoMatches")));
      return;
    }
    this.shown.forEach((item, i) => {
      const row = el("button", "palette-row");
      row.type = "button";
      row.setAttribute("role", "option");
      const active = i === this.selected;
      row.setAttribute("aria-selected", active ? "true" : "false");
      if (active) row.classList.add("selected");

      const left = el("span", "palette-row-main");
      left.append(el("span", "palette-row-title", item.title));
      if (item.detail) left.append(el("span", "palette-row-detail", item.detail));
      row.append(left);
      if (item.keys) row.append(el("kbd", "settings-keys", item.keys));

      row.onmouseenter = () => {
        if (this.selected !== i) {
          this.selected = i;
          this.list?.querySelector(".palette-row.selected")?.classList.remove("selected");
          row.classList.add("selected");
        }
      };
      row.onclick = () => {
        this.close();
        item.run();
      };
      this.list!.append(row);
      if (active) row.scrollIntoView({ block: "nearest" });
    });
  }
}
