import assert from "node:assert/strict";
import { test } from "node:test";
import { floatingLayersOf, layoutOf, leavesOf } from "../src/layerhelpers";

test("floatingLayersOf extracts normal and minimized floating layers back to front", () => {
  const win = {
    layers: [
      { kind: "tiled", layer_id: "t1", layout: { block_id: "b1" }, blocks: [{ block_id: "b1" }] },
      { kind: "floating", layer_id: "f1", bounds: [0.1, 0.2, 0.5, 0.4], blocks: [{ block_id: "b2" }] },
      { kind: "floating", layer_id: "f2", bounds: [0.3, 0.3, 0.4, 0.4], state: "minimized", blocks: [{ block_id: "b3" }] },
      { kind: "unknown-future-kind", layer_id: "f3", blocks: [{ block_id: "b4" }] },
    ],
  };
  assert.deepEqual(leavesOf(layoutOf(win)), ["b1"]);
  const floats = floatingLayersOf(win);
  assert.equal(floats.length, 3);
  assert.deepEqual(floats[0], {
    layerId: "f1",
    bounds: [0.1, 0.2, 0.5, 0.4],
    minimized: false,
    blocks: ["b2"],
    zIndex: 10,
  });
  assert.equal(floats[1].layerId, "f2");
  assert.equal(floats[1].minimized, true);
  assert.equal(floats[1].zIndex, 11);
  assert.equal(floats[2].layerId, "f3");
  assert.deepEqual(floats[2].bounds, [0.15, 0.15, 0.7, 0.7]);
  assert.equal(floats[2].minimized, false);
  assert.equal(floats[2].zIndex, 12);
});
