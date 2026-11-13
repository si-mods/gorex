import assert from "node:assert/strict";
import { test } from "node:test";
import { filterPaletteItems, PaletteItem } from "../src/palettehelpers";

test("filterPaletteItems matches title and detail tokens and prioritizes title prefix", () => {
  const items: PaletteItem[] = [
    { id: "1", title: "Split Right", detail: "Pane", run: () => {} },
    { id: "2", title: "Find…", detail: "Search terminal", run: () => {} },
    { id: "3", title: "Find Next", detail: "Terminal", run: () => {} },
  ];
  assert.equal(filterPaletteItems(items, "").length, 3);
  const res = filterPaletteItems(items, "find");
  assert.deepEqual(res.map((i) => i.id), ["2", "3"]);
  assert.deepEqual(filterPaletteItems(items, "terminal").map((i) => i.id), ["2", "3"]);
  assert.deepEqual(filterPaletteItems(items, "missing"), []);
});
