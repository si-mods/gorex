// File upload helpers: paths, clipboard image extraction, directory skip, and response parsing (src/upload.ts).
import assert from "node:assert/strict";
import { test } from "node:test";
import { extractDroppedFiles, extractPastedImages, parseUploadResponse, pastedImageName, uploadPath } from "../src/uploadhelpers";

test("uploadPath encodes block_id, name, and optional size", () => {
  assert.equal(uploadPath("block:1", "my file.png"), "/api/upload?block_id=block%3A1&name=my+file.png");
  assert.equal(uploadPath("block:1", "a.txt", 42), "/api/upload?block_id=block%3A1&name=a.txt&size=42");
});

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

test("parseUploadResponse returns word or throws on error / unsupported shell path", () => {
  assert.deepEqual(parseUploadResponse(200, JSON.stringify({ path: "/tmp/a b.png", word: "'/tmp/a b.png'" })), {
    path: "/tmp/a b.png",
    word: "'/tmp/a b.png'",
    reason: undefined,
  });
  assert.throws(() => parseUploadResponse(200, JSON.stringify({ path: "/tmp/a", word: "", reason: "bad shell" })), /bad shell/);
  assert.throws(() => parseUploadResponse(413, JSON.stringify({ error: "file too large" })), /file too large/);
});
