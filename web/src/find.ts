// Terminal scrollback find bar (find.ts), matching desktop's cmdFind / cmdFindNext / cmdFindPrevious.

import { FindMatch, findMatches, stepMatch, viewportHighlights } from "./findhelpers";
import { icon } from "./hosts";
import { localize, t } from "./i18n";
import type { Terminal } from "./terminal";

const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls: string) => {
  const e = document.createElement(tag);
  e.className = cls;
  return e;
};

export interface FindHost {
  term(): Terminal | undefined;
  redraw(force?: boolean): void;
  focus(): void;
}

export class FindBar {
  readonly el = el("div", "find-bar");
  private readonly input = el("input", "find-input");
  private readonly count = el("span", "find-count");
  private readonly prevBtn = el("button", "find-btn");
  private readonly nextBtn = el("button", "find-btn");
  private readonly closeBtn = el("button", "find-btn");

  private matches: FindMatch[] = [];
  private active = -1;

  constructor(private readonly host: FindHost) {
    this.el.hidden = true;
    this.input.type = "search";
    this.input.autocomplete = "off";
    this.input.spellcheck = false;
    localize(this.input, (i) => {
      i.placeholder = t("findPlaceholder");
      i.setAttribute("aria-label", t("findPlaceholder"));
    });

    this.prevBtn.type = "button";
    this.prevBtn.append(icon("chevron-up"));
    localize(this.prevBtn, (b) => {
      b.title = t("findPrevious");
      b.setAttribute("aria-label", t("findPrevious"));
    });

    this.nextBtn.type = "button";
    this.nextBtn.append(icon("chevron-down"));
    localize(this.nextBtn, (b) => {
      b.title = t("findNext");
      b.setAttribute("aria-label", t("findNext"));
    });

    this.closeBtn.type = "button";
    this.closeBtn.append(icon("x"));
    localize(this.closeBtn, (b) => {
      b.title = t("findClose");
      b.setAttribute("aria-label", t("findClose"));
    });

    this.el.append(this.input, this.count, this.prevBtn, this.nextBtn, this.closeBtn);

    this.input.addEventListener("input", () => this.search(true));
    this.input.addEventListener("keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        this.close();
      } else if (e.key === "Enter") {
        e.preventDefault();
        e.stopPropagation();
        if (e.shiftKey) this.prev();
        else this.next();
      }
    });
    this.prevBtn.onclick = () => this.prev();
    this.nextBtn.onclick = () => this.next();
    this.closeBtn.onclick = () => this.close();
  }

  isOpen(): boolean {
    return !this.el.hidden;
  }

  open() {
    const wasHidden = this.el.hidden;
    this.el.hidden = false;
    this.input.focus();
    this.input.select();
    if (wasHidden || this.input.value) this.search(false);
  }

  close() {
    if (this.el.hidden) return;
    this.el.hidden = true;
    this.matches = [];
    this.active = -1;
    this.updateCount();
    this.host.redraw(true);
    this.host.focus();
  }

  next() {
    this.step(1);
  }

  prev() {
    this.step(-1);
  }

  highlights(): { row: number; col: number; len: number; current: boolean }[] | undefined {
    if (this.el.hidden || this.matches.length === 0) return undefined;
    const term = this.host.term();
    if (!term) return undefined;
    return viewportHighlights(this.matches, this.active, term.viewportTop(), term.size().rows);
  }

  private step(delta: 1 | -1) {
    if (this.el.hidden) this.open();
    const term = this.host.term();
    if (!term) return;
    if (this.matches.length === 0 && this.input.value) {
      this.matches = findMatches(term.screenLines(), this.input.value);
    }
    this.active = stepMatch(this.active, this.matches.length, delta);
    this.reveal();
  }

  private search(jump: boolean) {
    const term = this.host.term();
    const q = this.input.value;
    if (!term || !q) {
      this.matches = [];
      this.active = -1;
      this.updateCount();
      this.host.redraw(true);
      return;
    }
    this.matches = findMatches(term.screenLines(), q);
    if (this.matches.length === 0) {
      this.active = -1;
    } else if (jump || this.active < 0 || this.active >= this.matches.length) {
      // Start at the most recent match (closest to bottom of scrollback).
      this.active = this.matches.length - 1;
    }
    this.reveal();
  }

  private reveal() {
    const term = this.host.term();
    if (term && this.active >= 0 && this.active < this.matches.length) {
      term.scrollToRow(this.matches[this.active].row);
    }
    this.updateCount();
    this.host.redraw(true);
  }

  private updateCount() {
    const q = this.input.value;
    if (!q) {
      this.count.textContent = "";
    } else if (this.matches.length === 0) {
      this.count.textContent = t("paletteNoMatches");
    } else {
      this.count.textContent = t("findCount", { selected: this.active + 1, total: this.matches.length });
    }
    const none = this.matches.length === 0;
    this.prevBtn.disabled = none;
    this.nextBtn.disabled = none;
  }
}
