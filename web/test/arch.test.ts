// Architectural anti-regression checks for web/:
// 1. Zero runtime circular dependencies across all web/src/*.ts modules.
// 2. Cycle & import-edge parser self-checks on synthetic tricky inputs.
// 3. i18n.mjs "--ci" gate failure checks on "..." and unused locale keys.
import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { pathToFileURL } from "node:url";
import { hex } from "../src/termtheme";

function findRuntimeCycles(graph: Map<string, string[]>): string[][] {
  const state = new Map<string, number>();
  const stack: string[] = [];
  const cycles: string[][] = [];

  const dfs = (u: string) => {
    state.set(u, 1);
    stack.push(u);
    for (const v of graph.get(u) ?? []) {
      if (!graph.has(v)) continue;
      const s = state.get(v) ?? 0;
      if (s === 1) {
        cycles.push([...stack.slice(stack.indexOf(v)), v]);
      } else if (s === 0) {
        dfs(v);
      }
    }
    stack.pop();
    state.set(u, 2);
  };

  for (const node of [...graph.keys()].sort()) {
    if ((state.get(node) ?? 0) === 0) dfs(node);
  }
  return cycles;
}

export function parseRuntimeEdges(source: string): string[] {
  const edges: string[] = [];
  // Strip block and line comments before matching static import/export-from clauses.
  const stripped = source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/^\s*\/\/.*$/gm, "");
  const stmtRe = /(?:^|;|\n)\s*(?:import|export)\s+(?!type\b)([^;()=]*?)from\s+["'](\.[^"']+)["']/g;
  let m: RegExpExecArray | null;
  while ((m = stmtRe.exec(stripped)) !== null) {
    const clause = m[1].trim();
    // An import whose specifiers are all `type Foo` has no runtime edge.
    if (clause.startsWith("{") && clause.endsWith("}")) {
      const specs = clause
        .slice(1, -1)
        .split(",")
        .map((s) => s.trim())
        .filter(Boolean);
      if (specs.length > 0 && specs.every((s) => /^type\s/.test(s))) continue;
    }
    edges.push(m[2].replace(/^\.\//, "").replace(/\.(?:ts|js)$/, ""));
  }
  const sideEffectRe = /(?:^|;|\n)\s*import\s+["'](\.[^"']+)["']/g;
  while ((m = sideEffectRe.exec(stripped)) !== null) {
    edges.push(m[1].replace(/^\.\//, "").replace(/\.(?:ts|js)$/, ""));
  }
  return edges;
}

test("findRuntimeCycles and parseRuntimeEdges handle synthetic cycles, same-line imports, and type-only clauses", () => {
  assert.deepEqual(
    parseRuntimeEdges('export const x = 1;\nimport type { A } from "./a";'),
    [],
  );
  assert.deepEqual(
    parseRuntimeEdges('import { x } from "ext";\nimport { type A } from "./a";'),
    [],
  );
  assert.deepEqual(
    parseRuntimeEdges('export function f() { return \'from "./fake"\'; }'),
    [],
  );
  assert.deepEqual(
    parseRuntimeEdges('import type { X } from "./a"; import { Y } from "./b";'),
    ["b"],
  );
  assert.deepEqual(
    parseRuntimeEdges('import {\n  type\n  X,\n} from "./a"; import "./side";'),
    ["side"],
  );

  const synthetic = new Map<string, string[]>([
    ["a", ["b"]],
    ["b", ["c"]],
    ["c", ["a"]],
  ]);
  const cycles = findRuntimeCycles(synthetic);
  assert.equal(cycles.length, 1);
  assert.deepEqual(cycles[0], ["a", "b", "c", "a"]);
});

test("web/src has zero runtime circular dependencies", () => {
  const srcDir = join(process.cwd(), "src");
  const files = readdirSync(srcDir).filter((f) => f.endsWith(".ts")).sort();
  const graph = new Map<string, string[]>();
  for (const f of files) {
    const mod = f.replace(/\.ts$/, "");
    graph.set(mod, parseRuntimeEdges(readFileSync(join(srcDir, f), "utf8")));
  }
  const cycles = findRuntimeCycles(graph);
  assert.deepEqual(
    cycles,
    [],
    `runtime import cycles in web/src:\n${cycles.map((c) => "  " + c.join(" -> ")).join("\n")}`,
  );
});

test("i18n.mjs --ci rejects three-dot '...' strings and unused locale keys", async () => {
  const mod = (await import(pathToFileURL(join(process.cwd(), "i18n.mjs")).href)) as {
    generate(opts: { ci?: boolean; localesDir?: string; srcDir?: string; write?: boolean }): Promise<{ errors: string[] }>;
  };
  const tmp = mkdtempSync(join(tmpdir(), "gorex-i18n-test-"));
  try {
    const localesDir = join(tmp, "locales");
    const srcDir = join(tmp, "src");
    mkdirSync(localesDir);
    mkdirSync(srcDir);
    writeFileSync(
      join(localesDir, "en.json"),
      JSON.stringify({
        "@@locale": "en",
        usedEllipsis: "Loading...",
        "@usedEllipsis": { description: "Shown while loading." },
        deadKey: "Never referenced",
        "@deadKey": { description: "Unused message." },
      }),
    );
    writeFileSync(
      join(localesDir, "zh-Hans.json"),
      JSON.stringify({
        "@@locale": "zh-Hans",
        usedEllipsis: "正在加载...",
        deadKey: "未使用",
      }),
    );
    writeFileSync(
      join(srcDir, "sample.ts"),
      '// "deadKey" in a comment does not count as used\nconst s = "usedEllipsis";\nvoid s;\n',
    );
    const res = await mod.generate({ ci: true, localesDir, srcDir, write: false });
    assert.ok(res.errors.some((e) => e.includes('en.json: usedEllipsis: use ellipsis')), res.errors.join("\n"));
    assert.ok(res.errors.some((e) => e.includes('zh-Hans.json: usedEllipsis: use ellipsis')), res.errors.join("\n"));
    assert.ok(res.errors.some((e) => e.includes('unused key(s): deadKey')), res.errors.join("\n"));
  } finally {
    rmSync(tmp, { recursive: true, force: true });
  }
});

test("termtheme hex parses both 3-digit and 6-digit hex colors", () => {
  assert.deepEqual(hex("#fff"), [255, 255, 255]);
  assert.deepEqual(hex("abc"), [0xaa, 0xbb, 0xcc]);
  assert.deepEqual(hex("#1e1e22"), [0x1e, 0x1e, 0x22]);
  assert.equal(hex("#1234"), undefined);
  assert.equal(hex("not-hex"), undefined);
});

test("windowTitle resolves custom window name -> pane name -> OSC title -> project -> program -> Tab, never raw Window <id>", async () => {
  const prevWin = (globalThis as any).window;
  (globalThis as any).window = prevWin ?? { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) };
  try {
    const { paneNameIn, windowName, windowTitle } = await import("../src/sidebar");
    const mkWin = (label: string, blockLabel = "shell") => ({
      window_id: "win-12345678",
      label,
      focused_block_id: "b1",
      layers: [{ state: "normal", layout: { block_id: "b1" }, blocks: [{ block_id: "b1", label: blockLabel }] }],
    });
    const baseConn = (): any => ({
      sessions: new Map(),
      titles: new Map<string, string>(),
      programs: new Map<string, string | undefined>(),
    });

    // Default gateway label "Window 12345678" is ignored, falling back through project -> program -> "Tab".
    const wDefault = mkWin("Window 12345678");
    assert.equal(windowName(wDefault), undefined);
    assert.equal(paneNameIn(wDefault, "b1"), undefined);
    assert.equal(windowTitle(baseConn(), wDefault), "Tab");

    const withProg = baseConn();
    withProg.programs.set("b1", "claude");
    assert.equal(windowTitle(withProg, wDefault), "claude");

    const withProj = baseConn();
    withProj.programs.set("b1", "claude");
    withProj.sessions.set("s1", { id: "s1", label: "", project: { dir: "/work/gorex", space: "", used: 0 }, view: { windows: [wDefault] } });
    assert.equal(windowTitle(withProj, wDefault), "gorex");

    // Focused pane OSC title beats project fallback; custom pane label beats OSC title; custom window name beats all.
    withProj.titles.set("b1", "vim main.go");
    assert.equal(windowTitle(withProj, wDefault), "vim main.go");
    assert.equal(windowTitle(withProj, mkWin("Window 12345678", "server logs")), "server logs");
    assert.equal(windowTitle(withProj, mkWin("Release Prep", "server logs")), "Release Prep");
  } finally {
    if (prevWin === undefined) delete (globalThis as any).window;
    else (globalThis as any).window = prevWin;
  }
});
