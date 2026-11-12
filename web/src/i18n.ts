// The page's languages (docs/web-client.md, Languages): the messages of
// web/locales, which web/i18n.mjs checks and writes into locales.gen.ts
// with the type of each key's arguments, so that t() of an unknown key, or
// without an argument its message needs, fails tsc. Both catalogs are in
// the bundle: they are a few KB gzipped, less than a request for one.
//
// The language is the one chosen in Settings (prefs.language), or, for
// System (""), the first of the browser's languages the page has. It
// changes live: setLanguage tells the listeners (onLanguageChange), which
// draw what they show again, and an element given to localize() sets its
// words again by itself.
import { format, Locale, Node, parse } from "./icu";
import { Args, catalogs, Language, languages } from "./locales.gen";
import { prefs, setPrefs } from "./prefs";

export type { Language } from "./locales.gen";
export { languages };
export type Key = keyof Args;
/** The keys of the messages with no argument. */
export type PlainKey = { [K in Key]: [Args[K]] extends [undefined] ? K : never }[Key];

/** Words now, or words said again in the language of the moment: what the page keeps to show later. */
export type Msg = string | (() => string);
export const msg = (m: Msg): string => (typeof m === "function" ? m() : m);

/**
 * The language of a BCP 47 tag the page has, if any: zh, zh-CN, zh-SG and
 * zh-Hans are Simplified Chinese; zh-TW, zh-HK, zh-MO and zh-Hant are not
 * (Traditional Chinese has no catalog, and its readers get English, as the
 * desktop gives them: MyGo's zh-Hant falls back to the source language).
 */
export function supported(tag: string): Language | undefined {
  let l: Intl.Locale;
  try {
    l = new Intl.Locale(tag).maximize();
  } catch {
    return undefined;
  }
  if (l.language === "zh") return l.script === "Hans" ? "zh-Hans" : undefined;
  if (l.language === "en") return "en";
  return undefined;
}

/** System's language: the first of the browser's that the page has, else English. */
export function systemLanguage(tags: readonly string[] = navigator.languages ?? [navigator.language]): Language {
  for (const tag of tags) {
    const l = supported(tag);
    if (l) return l;
  }
  return "en";
}

/** The language a choice of Settings means: "" for System. */
const resolve = (choice: string): Language => (languages as readonly string[]).includes(choice) ? (choice as Language) : systemLanguage();

let current: Language = resolve(prefs.language);
const listeners = new Set<() => void>();

/** The language the page shows now. */
export const language = (): Language => current;

/** Chooses the page's language ("" for System), keeps it, and says everything again in it. */
export function setLanguage(choice: "" | Language) {
  setPrefs({ language: choice });
  apply();
}

function apply() {
  const next = resolve(prefs.language);
  document.documentElement.lang = next;
  if (next === current) return;
  current = next;
  for (const f of listeners) f();
  relocalize();
}

/** Calls f after each change of the language. */
export function onLanguageChange(f: () => void): () => void {
  listeners.add(f);
  return () => listeners.delete(f);
}

// System follows the browser's languages as they change.
window.addEventListener("languagechange", () => {
  if (!prefs.language) apply();
});
document.documentElement.lang = current;

/** Each language's formats, made once. */
const locales = new Map<Language, Locale>();
function localeOf(l: Language): Locale {
  let loc = locales.get(l);
  if (!loc) {
    const numbers = new Intl.NumberFormat(l, { maximumFractionDigits: 2 });
    const plurals = new Intl.PluralRules(l);
    loc = { number: (n) => numbers.format(n), plural: (n) => plurals.select(n) };
    locales.set(l, loc);
  }
  return loc;
}

/** The messages, parsed as they are first said, by language and key. */
const parsed = new Map<string, Node[]>();

type Rest<K extends Key> = [Args[K]] extends [undefined] ? [] : [args: Args[K]];

/** The message of key in the page's language (English where it has none), with its arguments. */
export function t<K extends Key>(key: K, ...args: Rest<K>): string {
  return tIn(current, key, ...args);
}

/** The message of key in language l. */
export function tIn<K extends Key>(l: Language, key: K, ...args: Rest<K>): string {
  const own = catalogs[l][key];
  const lang = own === undefined ? "en" : l;
  const id = `${lang}\0${key}`;
  let nodes = parsed.get(id);
  if (!nodes) {
    nodes = parse(catalogs[lang][key] ?? key);
    parsed.set(id, nodes);
  }
  return format(nodes, (args[0] ?? {}) as Record<string, unknown>, localeOf(lang));
}

/** A number in the page's language, as 1,234.5 or 1.234,5. */
export function formatNumber(n: number, options?: Intl.NumberFormatOptions): string {
  return new Intl.NumberFormat(current, options).format(n);
}

/** A list in the page's language: “a, b and c”, “a、b和c”. */
export function formatList(items: string[]): string {
  return new Intl.ListFormat(current, { style: "long", type: "conjunction" }).format(items);
}

/** A day (an ISO date, as 2026-11-12) in the page's language: Nov 12, 2026; 2026年11月12日. */
export function formatDate(iso: string): string {
  const d = new Date(`${iso}T00:00:00`);
  return isNaN(d.getTime()) ? iso : new Intl.DateTimeFormat(current, { dateStyle: "medium" }).format(d);
}

/**
 * Bytes as a short size, in units of 1000 (decimal, as a download's
 * progress: 412 KB, 6.1 MB) or 1024 (a file's size: 48 KB, 1.2 GB). A size
 * under ten units keeps one decimal.
 */
export function formatSize(n: number, base: 1000 | 1024 = 1024): string {
  if (n < base) return t("sizeBytes", { n });
  const keys = ["sizeKB", "sizeMB", "sizeGB"] as const;
  let v = n / base, u = 0;
  while (v >= base && u < keys.length - 1) {
    v /= base;
    u++;
  }
  const size = v < 10 ? formatNumber(v, { minimumFractionDigits: 1, maximumFractionDigits: 1 }) : formatNumber(Math.round(v));
  return t(keys[u], { size });
}

/**
 * Elements whose words localize() sets, with the function that sets them:
 * weakly, so that an element the page dropped goes with its function.
 */
const bound = new Set<WeakRef<Element>>();
const setters = new WeakMap<Element, ((e: Element) => void)[]>();

/** Runs set on e now, and again after each change of the language while e lives. Returns e. */
export function localize<E extends Element>(e: E, set: (e: E) => void): E {
  set(e);
  const list = setters.get(e);
  if (list) list.push(set as (e: Element) => void);
  else {
    setters.set(e, [set as (e: Element) => void]);
    bound.add(new WeakRef(e));
  }
  return e;
}

function relocalize() {
  for (const ref of bound) {
    const e = ref.deref();
    if (!e) {
      bound.delete(ref);
      continue;
    }
    for (const f of setters.get(e) ?? []) f(e);
  }
}

/** An Error whose message is said in the page's language each time it is read. */
export function failure(m: () => string): Error {
  return Object.defineProperty(new Error(), "message", { get: m });
}
