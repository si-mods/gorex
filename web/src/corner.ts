// The onboarding screen's top-right corner: the language and Settings, which
// the sidebar's footer and Settings' Appearance page hold once a host exists.
// The language is the same setting as Settings › Appearance › Language
// (setLanguage), so the two always show the same choice.
import { icon } from "./hosts";
import { Language, language, languages, localize, setLanguage, t, tIn } from "./i18n";
import { prefs } from "./prefs";

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  e.className = cls;
  return e;
}

/** Fills mount with the language menu and the Settings button. */
export function mountCorner(mount: HTMLElement, openSettings: () => void) {
  const button = el("button", "corner-button corner-lang");
  button.type = "button";
  button.setAttribute("aria-haspopup", "menu");
  button.setAttribute("aria-expanded", "false");
  const name = el("span", "");
  button.append(icon("languages"), name);
  const menu = el("div", "corner-menu");
  menu.setAttribute("role", "menu");
  menu.hidden = true;

  const items = (): HTMLButtonElement[] => [...menu.querySelectorAll<HTMLButtonElement>("button")];
  const close = (focus = false) => {
    if (menu.hidden) return;
    menu.hidden = true;
    button.setAttribute("aria-expanded", "false");
    if (focus) button.focus();
  };
  const draw = () => {
    // Each language by its own name, as Settings does; "" follows the browser.
    const choices: [string, string][] = [["", t("settingsLanguageSystem")], ...languages.map((l): [string, string] => [l, tIn(l as Language, "languageName")])];
    const now = (languages as readonly string[]).includes(prefs.language) ? prefs.language : "";
    menu.replaceChildren(
      ...choices.map(([v, text]) => {
        const b = el("button", "corner-item");
        b.type = "button";
        b.tabIndex = -1;
        b.setAttribute("role", "menuitemradio");
        b.setAttribute("aria-checked", String(v === now));
        const label = el("span", "");
        label.textContent = text;
        b.append(label);
        if (v === now) b.append(icon("check"));
        b.onclick = () => {
          close(true);
          setLanguage(v as "" | Language);
        };
        return b;
      }),
    );
  };
  const open = () => {
    draw();
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    const list = items();
    (list.find((b) => b.getAttribute("aria-checked") === "true") ?? list[0]).focus();
  };
  button.onclick = () => (menu.hidden ? open() : close());
  button.onkeydown = (e) => {
    if (e.key === "ArrowDown" && menu.hidden) {
      e.preventDefault();
      open();
    }
  };
  menu.onkeydown = (e) => {
    const list = items();
    const i = list.indexOf(document.activeElement as HTMLButtonElement);
    const to = e.key === "ArrowDown" ? (i + 1) % list.length : e.key === "ArrowUp" ? (i - 1 + list.length) % list.length : e.key === "Home" ? 0 : e.key === "End" ? list.length - 1 : -1;
    if (to >= 0) {
      e.preventDefault();
      list[to].focus();
    } else if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      close(true);
    } else if (e.key === "Tab") close();
  };
  document.addEventListener("pointerdown", (e) => {
    if (!mount.contains(e.target as Node)) close();
  });
  localize(button, () => {
    name.textContent = tIn(language(), "languageName");
    button.setAttribute("aria-label", t("cornerLanguage"));
    button.title = t("cornerLanguage");
    if (!menu.hidden) draw();
  });

  const settings = el("button", "corner-button corner-icon");
  settings.type = "button";
  settings.append(icon("settings-2"));
  localize(settings, () => {
    settings.setAttribute("aria-label", t("sidebarSettings"));
    settings.title = t("sidebarSettings");
  });
  settings.onclick = openSettings;

  const lang = el("div", "corner-lang-box");
  lang.append(button, menu);
  mount.append(lang, settings);
}
