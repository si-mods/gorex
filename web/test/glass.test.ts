// The liquid glass's math (src/glassmath.ts, docs/WEB-005-liquid-glass.md): the reading of the
// terminal under a surface, the ink and its hysteresis, the tint's density, the contrast of
// text at the default tint, and the rim lens's displacement map.
import assert from "node:assert/strict";
import { test } from "node:test";
import {
  contrast, density, displacementMap, edgeContrast, filterAmount, glassColor, Ink, inkOn, lift, luminance, materialOf, materials, maxOffset,
  nextInk, outlineAlpha, readBackdrop, RGB, rimOffset, rimWidth, saturationKept, Scheme, tintAlpha, trimmedMean,
} from "../src/glassmath";
import { glassPrefs } from "../src/prefs";
import { glassRedrawn, glassRunning } from "../src/glass";

const near = (a: number, b: number, eps = 1e-6) => assert.ok(Math.abs(a - b) <= eps, `${a} is not ${b}`);
/** 15 samples, as the 5 by 3 grid: n of the text's colour, the rest the background's. */
const grid = (bg: RGB, fg: RGB = bg, n = 0): RGB[] => Array.from({ length: 15 }, (_, i) => (i < n ? fg : bg));

test("luminance weighs the channels 0.299, 0.587 and 0.114", () => {
  near(luminance([0, 0, 0]), 0);
  near(luminance([255, 255, 255]), 1);
  near(luminance([255, 0, 0]), 0.299);
  near(luminance([0, 255, 0]), 0.587);
});

test("trimmedMean drops the 2 darkest and the 2 brightest samples", () => {
  const s: RGB[] = [[0, 0, 0], [0, 0, 0], [255, 255, 255], [255, 255, 255], ...grid([100, 100, 100]).slice(0, 11)];
  assert.deepEqual(trimmedMean(s), [100, 100, 100]);
  // Fewer than 5 samples: nothing to drop.
  assert.deepEqual(trimmedMean([[0, 0, 0], [100, 100, 100]]), [50, 50, 50]);
});

test("readBackdrop: texture is the spread of all samples, chroma the mean's", () => {
  const flat = readBackdrop(grid([40, 40, 40]));
  near(flat.texture, 0);
  near(flat.chroma, 0);
  const busy = readBackdrop(grid([0, 0, 0], [255, 255, 255], 5));
  near(busy.texture, 1);
  const red = readBackdrop(grid([255, 0, 0]));
  near(red.chroma, 1);
});

test("nextInk: the dark scheme always takes light ink", () => {
  for (const l of [0, 0.5, 1]) assert.equal(nextInk("dark", l), "light");
});

test("nextInk: light ink under 0.45, dark over 0.55, the ink kept between", () => {
  assert.equal(nextInk("light", 0.44, "dark"), "light");
  assert.equal(nextInk("light", 0.56, "light"), "dark");
  assert.equal(nextInk("light", 0.47, "dark"), "dark");
  assert.equal(nextInk("light", 0.53, "light"), "light");
  // No ink yet: the side of 0.5.
  assert.equal(nextInk("light", 0.49), "light");
  assert.equal(nextInk("light", 0.51), "dark");
});

test("density: closeness to the ink's brightness, the lift, texture and chroma, clamped to 0..1", () => {
  // White under light glass: far from the dark ink, but the glass's own white (lift 1).
  near(density(readBackdrop(grid([255, 255, 255])), "dark"), 0.35);
  near(density(readBackdrop(grid([255, 255, 255])), "light"), 0.55);
  near(density(readBackdrop(grid([128, 128, 128])), "dark"), 0.55 * (1 - 128 / 255));
  near(density(readBackdrop(grid([0, 0, 0], [255, 255, 255], 5)), "dark"), 1);
  const red = readBackdrop(grid([255, 0, 0]));
  near(density(red, "light"), Math.min(1, 0.55 * (1 - Math.abs(red.luminance - 1)) + 0.3));
});

test("saturationKept falls with chroma to 0.45", () => {
  near(saturationKept(0), 1);
  near(saturationKept(0.4), 0.7);
  near(saturationKept(1), 0.45);
});

test("the tint scale thins the tint and, under 1, the brightness lift", () => {
  const m = materials.lightGlass;
  near(tintAlpha(m, 0, 1), m.thin);
  near(tintAlpha(m, 1, 1), m.dense);
  near(tintAlpha(m, 1, 0), 0);
  near(tintAlpha(m, 1, 1.5), m.dense * 1.5);
  near(filterAmount(1.06, 1), 1.06);
  near(filterAmount(1.06, 1.5), 1.06);
  near(filterAmount(1.06, 0.5), 1.03);
  near(filterAmount(0.8, 0), 1);
});

/**
 * The backgrounds a terminal can show under the glass, as the glass reads
 * them: the darkest, flat ones, busy ones (a fifth to a half of the grid on
 * text), coloured ones, and the light scheme's band around 0.5 where either
 * ink may be in use. The dark scheme over white is a light theme with
 * Window set to Dark.
 */
const backdrops: [string, Scheme, RGB[]][] = [
  ["black", "dark", grid([0, 0, 0])],
  ["Gruvbox Dark", "dark", grid([40, 40, 40])],
  ["Gruvbox Dark, busy", "dark", grid([40, 40, 40], [235, 219, 178], 7)],
  ["white, Window Dark", "dark", grid([255, 255, 255])],
  ["Gruvbox Light, busy, Window Dark", "dark", grid([251, 241, 199], [60, 56, 54], 6)],
  ["red", "dark", grid([200, 40, 40])],
  ["white", "light", grid([255, 255, 255])],
  ["Gruvbox Light, busy", "light", grid([251, 241, 199], [60, 56, 54], 6)],
  ["black, Window Light", "light", grid([0, 0, 0])],
  ["Gruvbox Dark, busy, Window Light", "light", grid([40, 40, 40], [235, 219, 178], 7)],
  ["grey 0.45", "light", grid([115, 115, 115])],
  ["grey 0.5", "light", grid([128, 128, 128])],
  ["grey 0.55", "light", grid([140, 140, 140])],
  ["red", "light", grid([220, 50, 50])],
];

test("text in the primary ink keeps 4.5:1 at the default tint over every backdrop", () => {
  for (const [name, scheme, s] of backdrops) {
    const b = readBackdrop(s);
    // In the band either ink may be shown: both must read.
    const ink = nextInk(scheme, b.luminance);
    const inks: Ink[] = scheme === "light" && Math.abs(b.luminance - 0.5) <= 0.05 ? ["light", "dark"] : [ink];
    for (const k of inks) {
      const glass = glassColor(b.mean, materialOf(scheme, k), density(b, k), 1, saturationKept(b.chroma));
      const ratio = contrast(inkOn(glass, k, "primary"), glass);
      assert.ok(ratio >= 4.5, `${name}, ${k} ink: ${ratio.toFixed(2)}:1`);
    }
  }
});

/** Light terminal backgrounds, flat (a prompt's empty screen), as the glass reads them. */
const lightThemes: [string, RGB][] = [
  ["white (Builtin Light)", [255, 255, 255]],
  ["GoRex's light terminal", [251, 251, 251]],
  ["Gruvbox Light", [251, 241, 199]],
  ["iTerm2 Solarized Light", [253, 246, 227]],
];

test("lift: none to 0.55 and from light ink, full from 0.9", () => {
  near(lift(readBackdrop(grid([140, 140, 140])), "dark"), 0);
  near(lift(readBackdrop(grid([255, 255, 255])), "dark"), 1);
  near(lift(readBackdrop(grid([184, 184, 184])), "dark"), (184 / 255 - 0.55) / 0.35);
  for (const c of [[0, 0, 0], [40, 40, 40], [255, 255, 255]] as RGB[]) near(lift(readBackdrop(grid(c)), "light"), 0);
});

test("light glass over a light terminal shows its edge: outline 1.4:1 or more, and no grey wash", () => {
  for (const [name, bg] of lightThemes) {
    const b = readBackdrop(grid(bg));
    const ink = nextInk("light", b.luminance);
    assert.equal(ink, "dark", name);
    const m = materialOf("light", ink);
    const l = lift(b, ink);
    const ratio = edgeContrast(b.mean, m, l);
    assert.ok(ratio >= 1.4, `${name}: edge ${ratio.toFixed(2)}:1`);
    // The face (away from the inner light) is not darker than the terminal around it: white glass, not grey.
    const face = glassColor(b.mean, { ...m, light: 0 }, density(b, ink), 1, saturationKept(b.chroma));
    assert.ok(contrast(face, [0, 0, 0]) >= contrast(b.mean, [0, 0, 0]) - 1e-9, `${name}: face ${face.map(Math.round)} is darker than ${bg}`);
    // And not dark enough to read as a grey line: the outline stays under a fifth of black.
    assert.ok(outlineAlpha(m, l) < 0.2, `${name}: outline alpha ${outlineAlpha(m, l)}`);
  }
});

test("the lift leaves the dark materials as they were", () => {
  for (const [scheme, bg] of [["light", [40, 40, 40]], ["light", [0, 0, 0]], ["dark", [40, 40, 40]], ["dark", [251, 241, 199]], ["dark", [255, 255, 255]]] as [Scheme, RGB][]) {
    const b = readBackdrop(grid(bg));
    const ink = nextInk(scheme, b.luminance);
    const m = materialOf(scheme, ink);
    near(lift(b, ink), 0);
    near(outlineAlpha(m, 0), m.outline);
    near(density(b, ink), Math.min(1, 0.55 * (1 - Math.abs(b.luminance - 1)) + 0.7 * b.texture + 0.3 * b.chroma));
  }
});

test("rimWidth is a 3.5th of the short side, 4 to 44 px", () => {
  near(rimWidth(300, 35), 10);
  assert.equal(rimWidth(1000, 1000), 44);
  assert.equal(rimWidth(10, 10), 4);
});

test("rimOffset: 1.1 rim at the edge, (1 - d/rim)^2.6 inward, 0 from the rim on", () => {
  near(rimOffset(0, 10), 11);
  near(rimOffset(5, 10), 11 * 0.5 ** 2.6);
  near(rimOffset(10, 10), 0);
  near(rimOffset(20, 10), 0);
  near(maxOffset(10), 11);
});

test("displacementMap bends only the rim, inward, and keeps the corners outside neutral", () => {
  const w = 120;
  const h = 40;
  const rim = 10;
  const m = displacementMap(w, h, 12, rim);
  const at = (x: number, y: number) => [m[(y * w + x) * 4], m[(y * w + x) * 4 + 1]];
  // The middle: no bend.
  assert.deepEqual(at(60, 20), [128, 128]);
  // The left edge looks right (R up), the right edge left, the top down (G up), the bottom up.
  assert.ok(at(0, 20)[0] > 230 && at(0, 20)[1] === 128);
  assert.ok(at(w - 1, 20)[0] < 26);
  assert.ok(at(60, 0)[1] > 230 && at(60, 0)[0] === 128);
  assert.ok(at(60, h - 1)[1] < 26);
  // Past the rim: none.
  assert.deepEqual(at(60, rim + 1), [128, 128]);
  // Outside the rounded corner: none. On the arc's diagonal: both ways, inward.
  assert.deepEqual(at(0, 0), [128, 128]);
  const [r, g] = at(5, 5);
  assert.ok(r > 128 && g > 128 && Math.abs(r - g) <= 1);
  // The value is the offset: 128 + 127 rimOffset / maxOffset.
  const d = 3;
  assert.equal(at(60, d)[1], Math.round(128 + (127 * rimOffset(d + 0.5, rim)) / maxOffset(rim)));
});


test("glassPrefs: the Glass setting is off until set to true; a tint only in 0..1.5", () => {
  // Unset: off, as the web's default (the desktop's is on).
  assert.deepEqual(glassPrefs({}), {});
  assert.deepEqual(glassPrefs({ glass: true }), { glass: true });
  for (const v of [false, "true", 1, null]) assert.deepEqual(glassPrefs({ glass: v }), {});
  assert.deepEqual(glassPrefs({ glassLight: 0, glassDark: 1.5 }), { glassLight: 0, glassDark: 1.5 });
  assert.deepEqual(glassPrefs({ glassLight: -0.1, glassDark: 1.6 }), {});
  assert.deepEqual(glassPrefs({ glassLight: "1" }), {});
});

test("the glass does not run until applyGlass(true), and a redraw starts nothing", () => {
  assert.equal(glassRunning(), false);
  // With no window here, a timer it tried to start would throw.
  assert.doesNotThrow(() => glassRedrawn());
  assert.equal(glassRunning(), false);
});