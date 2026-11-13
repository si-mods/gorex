#!/usr/bin/env node
// The web client's unit tests (npm test), for its modules with no DOM
// (docs/web-multihost.md, Decisions 8). The sources import without
// extensions, which Node does not load as they are, so this bundles each
// test/*.test.ts with esbuild into a directory of its own and runs the
// bundles with node --test.
//
// Usage: npm test   (exits as node --test does)
import { spawnSync } from "node:child_process";
import { mkdtempSync, readdirSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const web = dirname(fileURLToPath(import.meta.url));
const tests = readdirSync(join(web, "test")).filter((f) => f.endsWith(".test.ts")).sort();
if (!tests.length) {
  console.error("no test/*.test.ts");
  process.exit(1);
}
const out = mkdtempSync(join(tmpdir(), "gorex-web-test-"));
let status = 1;
try {
  await build({
    entryPoints: tests.map((f) => join(web, "test", f)),
    bundle: true,
    platform: "node",
    format: "esm",
    target: "node22",
    outdir: out,
    outExtension: { ".js": ".mjs" },
    logLevel: "warning",
  });
  const r = spawnSync(process.execPath, ["--test", ...tests.map((f) => join(out, f.replace(/\.ts$/, ".mjs")))], { stdio: "inherit" });
  status = r.status ?? 1;
} finally {
  rmSync(out, { recursive: true, force: true });
}
process.exit(status);
