#!/usr/bin/env node
// Checks the web client's catalogs (web/locales/*.json, docs/web-client.md,
// Languages) and writes src/locales.gen.ts from them: each language's
// messages, without the metadata, and the type of every key's arguments,
// so that t() of a key en.json does not have, or without an argument its
// message needs, fails tsc. build.mjs runs it before tsc, and dev.mjs again
// when a catalog changes. src/locales.gen.ts is not kept in git.
//
// The catalogs are ARB-style JSON, as the desktop's (locales/): "@@locale",
// the messages by key, in ICU MessageFormat (src/icu.ts), and in en.json,
// the source, an "@key" for each with its description and its
// placeholders, each with its type ("String", "int" or "num"). It checks:
//
// - every message parses, and every en message has its "@key" with a
//   description and exactly the placeholders it uses (errors);
// - every other language has no key en.json has not (an error), the same
//   arguments in each message as en's (an error), and every key of en's: a
//   missing one is a warning, and the page says it in English, but with
//   --ci (or $CI set) it is an error.
//
// Usage: node web/i18n.mjs [--ci]   (exits 1 on an error)
import { readdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const web = dirname(fileURLToPath(import.meta.url));
const dir = join(web, "locales");
const out = join(web, "src", "locales.gen.ts");
const source = "en";

/** src/icu.ts, the page's own parser, through esbuild: the build and the page read messages alike. */
async function icu() {
  const esbuild = await import(pathToFileURL(join(web, "node_modules", "esbuild", "lib", "main.js")).href);
  const { code } = await esbuild.transform(readFileSync(join(web, "src", "icu.ts"), "utf8"), { loader: "ts", format: "esm" });
  return import("data:text/javascript;base64," + Buffer.from(code).toString("base64"));
}

const typeOf = { String: "string", int: "number", num: "number", double: "number" };

/**
 * Checks the catalogs and writes src/locales.gen.ts when it changes.
 * Returns the errors and warnings; with ci, a missing translation is an error.
 */
export async function generate({ ci = !!process.env.CI } = {}) {
  const { parse, args } = await icu();
  const errors = [], warnings = [];
  const files = readdirSync(dir).filter((f) => f.endsWith(".json")).sort();
  const catalogs = {};
  for (const f of files) {
    const lang = f.slice(0, -5);
    let j;
    try {
      j = JSON.parse(readFileSync(join(dir, f), "utf8"));
    } catch (e) {
      errors.push(`${f}: ${e.message}`);
      continue;
    }
    if (j["@@locale"] !== lang) errors.push(`${f}: "@@locale" is ${JSON.stringify(j["@@locale"])}, not "${lang}"`);
    catalogs[lang] = j;
  }
  const en = catalogs[source];
  if (!en) throw new Error(`web/locales has no ${source}.json`);
  const keys = Object.keys(en).filter((k) => !k.startsWith("@"));
  // The source's arguments, by key: name -> TypeScript type.
  const types = {};
  const argsOf = (lang, key) => {
    try {
      return args(parse(catalogs[lang][key]));
    } catch (e) {
      errors.push(`${lang}.json: ${key}: ${e.message}`);
      return undefined;
    }
  };
  for (const k of keys) {
    if (typeof en[k] !== "string") {
      errors.push(`en.json: ${k} is not a string`);
      continue;
    }
    if (!/^[a-z][A-Za-z0-9]*$/.test(k)) errors.push(`en.json: ${k}: keys are camelCase`);
    const meta = en[`@${k}`];
    if (!meta?.description) errors.push(`en.json: ${k} has no "@${k}" with a description of where it shows`);
    const used = argsOf(source, k);
    if (!used) continue;
    const declared = meta?.placeholders ?? {};
    const t = {};
    for (const a of used) {
      const p = declared[a.name];
      if (!p) {
        errors.push(`en.json: ${k} uses {${a.name}}, which "@${k}".placeholders does not declare`);
        continue;
      }
      const ts = typeOf[p.type];
      if (!ts) errors.push(`en.json: "@${k}".placeholders.${a.name}: type ${JSON.stringify(p.type)} is not String, int or num`);
      if ((a.kind === "plural" || a.kind === "number") && ts !== "number") errors.push(`en.json: ${k}: {${a.name}} is a ${a.kind}, so its type is int or num`);
      t[a.name] = ts ?? "string";
    }
    for (const name of Object.keys(declared)) {
      if (!used.some((a) => a.name === name)) errors.push(`en.json: "@${k}".placeholders.${name} is not in the message`);
    }
    types[k] = t;
  }
  for (const k of Object.keys(en)) {
    if (k.startsWith("@") && k !== "@@locale" && !(k.slice(1) in en)) errors.push(`en.json: ${k} describes no message`);
  }
  for (const [lang, c] of Object.entries(catalogs)) {
    if (lang === source) continue;
    for (const k of Object.keys(c)) {
      if (k === "@@locale") continue;
      if (k.startsWith("@")) {
        errors.push(`${lang}.json: ${k}: metadata belongs in en.json only`);
        continue;
      }
      if (!(k in en) || k.startsWith("@")) {
        errors.push(`${lang}.json: ${k} is not a key of en.json`);
        continue;
      }
      if (typeof c[k] !== "string") {
        errors.push(`${lang}.json: ${k} is not a string`);
        continue;
      }
      const got = argsOf(lang, k);
      if (!got || !types[k]) continue;
      const want = Object.keys(types[k]).sort().join(", ");
      const have = got.map((a) => a.name).sort().join(", ");
      if (want !== have) errors.push(`${lang}.json: ${k} has the placeholders {${have}}, en.json's has {${want}}`);
    }
    const missing = keys.filter((k) => !(k in c));
    if (missing.length) {
      const msg = `${lang}.json: ${missing.length} key(s) not translated, shown in English: ${missing.join(", ")}`;
      (ci ? errors : warnings).push(msg);
    }
  }

  // The messages without metadata, and the arguments' types.
  const messages = {};
  for (const [lang, c] of Object.entries(catalogs)) {
    messages[lang] = Object.fromEntries(Object.entries(c).filter(([k, v]) => !k.startsWith("@") && typeof v === "string" && k in en));
  }
  const langs = [source, ...Object.keys(catalogs).filter((l) => l !== source)];
  const lines = [
    "// Generated by web/i18n.mjs from web/locales/*.json. Do not edit; it is not kept in git.",
    "",
    "/** The languages of the catalogs, the source first. */",
    `export const languages = ${JSON.stringify(langs)} as const;`,
    "export type Language = (typeof languages)[number];",
    "",
    "/** Each key's arguments, by name; undefined for a message with none. */",
    "export interface Args {",
    ...keys.filter((k) => types[k]).map((k) => {
      const t = Object.entries(types[k]);
      return `  ${k}: ${t.length ? `{ ${t.map(([n, ty]) => `${n}: ${ty}`).join("; ")} }` : "undefined"};`;
    }),
    "}",
    "",
    "/** The messages, by language and key; a key missing from a language is said in en. */",
    `export const catalogs: Record<Language, Partial<Record<keyof Args, string>>> = ${JSON.stringify(Object.fromEntries(langs.map((l) => [l, messages[l]])), null, 1)};`,
    "",
  ];
  const text = lines.join("\n");
  // A catalog with errors leaves the last good file, so that a dev server keeps building.
  if (!errors.length && (!existsSync(out) || readFileSync(out, "utf8") !== text)) writeFileSync(out, text);
  return { errors, warnings, keys: keys.length, languages: langs };
}

/** Runs generate, printing what it found; throws when the catalogs have errors. */
export async function generateOrFail(opts) {
  const r = await generate(opts);
  for (const w of r.warnings) console.warn(`i18n: warning: ${w}`);
  for (const e of r.errors) console.error(`i18n: error: ${e}`);
  if (r.errors.length) throw new Error(`i18n: ${r.errors.length} error(s) in web/locales`);
  console.log(`i18n: ${r.keys} keys in ${r.languages.join(", ")}; wrote src/locales.gen.ts`);
  return r;
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  try {
    await generateOrFail({ ci: process.argv.includes("--ci") || !!process.env.CI });
  } catch (e) {
    console.error(e.message);
    process.exit(1);
  }
}
