// The subset of ICU MessageFormat that the catalogs (web/locales) use, as
// MyGo's i18n.Message reads the desktop's (locales/*.json):
//
//   Hello, {name}!
//   {count, number}
//   {count, plural, =0 {No files} one {# file} other {# files}}
//   {kind, select, light {Light} other {Dark}}
//
// An argument {name} writes its value, a number formatted for the language
// as {name, number} does. A plural picks the case of an exact value (=0),
// else of the number's plural category in the language (one, few, other...);
// # in its cases writes the number. A select picks the case that is the
// value, else other; plural and select need an other case, and cases nest.
// An apostrophe quotes as ICU's does: '{' writes a brace, '#' a #, and two
// apostrophes write one; any other apostrophe is one ("don't").
//
// i18n.ts formats with it at runtime; web/i18n.mjs parses the catalogs with
// it at build time, to check them and to type their arguments.

export type Node =
  | string
  | { pound: true }
  | { arg: string; kind: "" | "number" }
  | { arg: string; kind: "plural" | "select"; cases: Record<string, Node[]> };

/** An argument of a message: its name, and how it is used ("" for {name}). */
export interface Arg {
  name: string;
  kind: "" | "number" | "plural" | "select";
}

export class MessageError extends Error {}

/** Parses a message; throws a MessageError when it is not one. */
export function parse(s: string): Node[] {
  let i = 0;
  const fail = (why: string): never => {
    throw new MessageError(`${why} at ${i} in ${JSON.stringify(s)}`);
  };
  const space = () => {
    while (i < s.length && /\s/.test(s[i])) i++;
  };
  const word = () => {
    space();
    const start = i;
    while (i < s.length && /[\w=-]/.test(s[i])) i++;
    if (i === start) fail("expected a name");
    return s.slice(start, i);
  };
  // Text and arguments up to a closing brace (nested) or the end.
  const message = (nested: boolean, plural: boolean): Node[] => {
    const out: Node[] = [];
    let text = "";
    const flush = () => {
      if (text) out.push(text);
      text = "";
    };
    while (i < s.length) {
      const c = s[i];
      if (c === "'") {
        if (s[i + 1] === "'") {
          text += "'";
          i += 2;
        } else if (s[i + 1] === "{" || s[i + 1] === "}" || (plural && s[i + 1] === "#")) {
          const end = s.indexOf("'", i + 1);
          if (end < 0) fail("unclosed quote");
          text += s.slice(i + 1, end);
          i = end + 1;
        } else {
          text += c;
          i++;
        }
      } else if (c === "{") {
        flush();
        i++;
        out.push(argument());
      } else if (c === "}") {
        if (!nested) fail("unmatched }");
        break;
      } else if (c === "#" && plural) {
        flush();
        out.push({ pound: true });
        i++;
      } else {
        text += c;
        i++;
      }
    }
    if (nested && s[i] !== "}") fail("unclosed {");
    flush();
    return out;
  };
  const argument = (): Node => {
    const name = word();
    space();
    if (s[i] === "}") {
      i++;
      return { arg: name, kind: "" };
    }
    if (s[i] !== ",") fail("expected , or }");
    i++;
    const kind = word();
    space();
    if (kind === "number") {
      if (s[i] !== "}") fail("a number takes no style here");
      i++;
      return { arg: name, kind: "number" };
    }
    if (kind !== "plural" && kind !== "select") fail(`unknown argument type ${kind}`);
    if (s[i] !== ",") fail("expected ,");
    i++;
    const cases: Record<string, Node[]> = {};
    for (;;) {
      space();
      if (s[i] === "}") {
        i++;
        break;
      }
      const key = word();
      if (kind === "select" && key.startsWith("=")) fail("a select has no exact cases");
      space();
      if (s[i] !== "{") fail("expected {");
      i++;
      cases[key] = message(true, kind === "plural");
      i++;
    }
    if (!cases.other) fail(`${name}'s ${kind} has no other case`);
    return { arg: name, kind: kind as "plural" | "select", cases };
  };
  return message(false, false);
}

/** The arguments of a message, nested ones too, each once, with its first kind other than "". */
export function args(nodes: Node[]): Arg[] {
  const out: Arg[] = [];
  const walk = (list: Node[]) => {
    for (const n of list) {
      if (typeof n === "string" || "pound" in n) continue;
      const had = out.find((a) => a.name === n.arg);
      if (!had) out.push({ name: n.arg, kind: n.kind });
      else if (!had.kind) had.kind = n.kind;
      if ("cases" in n) for (const c of Object.values(n.cases)) walk(c);
    }
  };
  walk(nodes);
  return out;
}

/** How a language writes numbers and picks plural cases. */
export interface Locale {
  number(n: number): string;
  plural(n: number): string;
}

/** Writes a message with its arguments' values. */
export function format(nodes: Node[], values: Record<string, unknown>, locale: Locale, pound?: number): string {
  let out = "";
  for (const n of nodes) {
    if (typeof n === "string") out += n;
    else if ("pound" in n) out += pound === undefined ? "#" : locale.number(pound);
    else {
      const v = values[n.arg];
      if (n.kind === "plural") {
        const x = Number(v);
        const c = n.cases[`=${x}`] ?? n.cases[locale.plural(x)] ?? n.cases.other;
        out += format(c, values, locale, x);
      } else if (n.kind === "select") {
        out += format(n.cases[String(v)] ?? n.cases.other, values, locale, pound);
      } else {
        out += typeof v === "number" ? locale.number(v) : String(v ?? `{${n.arg}}`);
      }
    }
  }
  return out;
}
