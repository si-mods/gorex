// File upload helpers: clipboard image extraction and directory skip (src/upload.ts).
import assert from "node:assert/strict";
import { test } from "node:test";
import { extractDroppedFiles, extractPastedImages, pastedImageName } from "../src/uploadhelpers";

test("pastedImageName formats timestamp and extension", () => {
  const d = new Date(2026, 10, 13, 9, 5, 7);
  assert.equal(pastedImageName("image/png", d), "pasted-image-20261113-090507.png");
  assert.equal(pastedImageName("image/jpeg", d), "pasted-image-20261113-090507.jpg");
  assert.equal(pastedImageName("image/webp; charset=binary", d), "pasted-image-20261113-090507.webp");
});

test("extractDroppedFiles skips directories and keeps files", () => {
  const f1 = new File(["hello"], "notes.txt", { type: "text/plain" });
  const dt = {
    files: [f1],
    items: [
      { kind: "file", webkitGetAsEntry: () => ({ isDirectory: true }), getAsFile: () => new File([], "folder") },
      { kind: "file", webkitGetAsEntry: () => ({ isDirectory: false }), getAsFile: () => f1 },
    ],
  } as unknown as DataTransfer;
  const r = extractDroppedFiles(dt);
  assert.equal(r.skippedDir, true);
  assert.equal(r.files.length, 1);
  assert.equal(r.files[0].name, "notes.txt");
});

test("extractPastedImages ignores images when plain text is present and renames generic image.png", () => {
  const img = new File(["png"], "image.png", { type: "image/png" });
  const named = new File(["png"], "shot.png", { type: "image/png" });
  const withText = {
    getData: () => "hello world",
    files: [img],
    items: [],
  } as unknown as DataTransfer;
  assert.equal(extractPastedImages(withText).length, 0);

  const d = new Date(2026, 10, 13, 21, 0, 0);
  const withoutText = {
    getData: () => "   ",
    files: [img, named],
    items: [],
  } as unknown as DataTransfer;
  const out = extractPastedImages(withoutText, d);
  assert.equal(out.length, 2);
  assert.equal(out[0].name, "pasted-image-20261113-210000.png");
  assert.equal(out[1].name, "shot.png");
});
