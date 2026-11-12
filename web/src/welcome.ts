// The first screen, while no host is chosen (docs/web-client.md, Hosts):
// what the page is, and two ways to reach a GoRex computer. Sign in with
// Tailscale opens the guide (guide.ts); Connect by address shows the host
// form (hosts.ts) in place of the choices. How a host trusts the page is
// not said here: the page says it when a host refuses it (diagnose).
import type { HostList } from "./hosts";
import { icon } from "./hosts";
import { localize, PlainKey, t } from "./i18n";
import type { Tailnet } from "./tailnet";

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  return e;
}

/** An element whose text is key's message, said again after a change of language. */
function said<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string, key: PlainKey | (() => string)): HTMLElementTagNameMap[K] {
  return localize(el(tag, cls), (e) => (e.textContent = typeof key === "function" ? key() : t(key)));
}

export interface WelcomeApp {
  hosts: HostList;
  tailnet: Tailnet;
  /** Opens the Tailscale sign-in guide. */
  guide(): void;
}

/**
 * The welcome: the app's icon, what it is, and a card for each way in.
 * update() follows Tailscale's state without leaving the form.
 */
export class Welcome {
  readonly el = el("section", "welcome");
  private choices = el("div", "welcome-choices");
  private tsTitle = el("span", "welcome-choice-title");
  private tsLine = el("span", "welcome-choice-line");
  private home = el("div", "welcome-home");

  constructor(private app: WelcomeApp) {
    const logo = el("img", "welcome-icon");
    logo.src = "pwa/icon-192.png";
    logo.alt = "";
    logo.width = logo.height = 72;
    const title = said("h1", "welcome-title", "welcomeTitle");
    title.id = "welcome-title";
    this.el.setAttribute("aria-labelledby", title.id);
    const ts = this.choice("tailscale", this.tsTitle, this.tsLine, () => app.guide(), true);
    const address = this.choice(
      "monitor",
      said("span", "welcome-choice-title", "welcomeAddressTitle"),
      said("span", "welcome-choice-line", "welcomeAddressLine"),
      () => this.form(),
    );
    address.dataset.choice = "address";
    ts.dataset.choice = "tailscale";
    this.choices.append(ts, address);
    this.home.append(logo, title, said("p", "welcome-text", "welcomeText"), this.choices, this.needs());
    this.el.append(this.home);
    localize(this.tsTitle, () => this.update());
  }

  /** Says the Tailscale card for its state: sign in, finish signing in, or choose a computer found. */
  update() {
    const s = this.app.tailnet.status();
    this.tsTitle.replaceChildren(
      t(s === "running" ? "welcomeTsChoose" : s === "off" ? "noticeSignInTailscale" : "noticeContinueTailscale"),
    );
    if (s === "off") this.tsTitle.append(" ", said("span", "welcome-badge", "welcomeRecommended"));
    this.tsLine.textContent = t(s === "running" ? "welcomeTsLineRunning" : s === "off" ? "welcomeTsLine" : "welcomeTsLineContinue");
  }

  /** A way in: a large button with an icon, a title and a line, Enter and Space as a click. */
  private choice(glyph: string, title: HTMLElement, line: HTMLElement, run: () => void, primary = false): HTMLButtonElement {
    const b = el("button", primary ? "welcome-choice primary" : "welcome-choice");
    b.type = "button";
    const mark = el("span", glyph === "tailscale" ? "welcome-choice-icon ts-tile" : "welcome-choice-icon");
    mark.append(icon(glyph));
    const text = el("span", "welcome-choice-text");
    text.append(title, line);
    const go = el("span", "welcome-choice-go");
    go.append(icon("arrow-right"));
    b.append(mark, text, go);
    b.onclick = run;
    return b;
  }

  /** What a computer needs, in a disclosure under the choices. */
  private needs(): HTMLElement {
    const d = el("details", "welcome-needs");
    const s = el("summary");
    s.append(icon("info"), said("span", "", "welcomeNeedsTitle"));
    const list = el("ul");
    for (const k of ["welcomeNeeds1", "welcomeNeeds2", "welcomeNeeds3"] as const) list.append(said("li", "", k));
    d.append(s, list);
    return d;
  }

  /** Connect by address: the host form in place of the choices; Cancel comes back. */
  private form() {
    const box = el("div", "welcome-form");
    const head = el("div", "welcome-form-head");
    const mark = el("span", "welcome-choice-icon");
    mark.append(icon("monitor"));
    const text = el("div", "welcome-choice-text");
    text.append(said("h1", "welcome-form-title", "welcomeAddressTitle"), said("p", "welcome-choice-line", "welcomeAddressLine"));
    head.append(mark, text);
    const form = this.app.hosts.form(undefined, (how) => {
      if (how !== "cancel") return;
      box.replaceWith(this.home);
      this.choices.querySelector<HTMLElement>('[data-choice="address"]')?.focus();
    });
    box.append(head, form);
    this.home.replaceWith(box);
    form.querySelector("input")?.focus();
  }
}
