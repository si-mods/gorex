import assert from "node:assert/strict";
import { test } from "node:test";
import { cleanPath, fileURLPath, LinkGrid, linkAt, remotePath } from "../src/links";

/** A one-row grid printing text, whose columns from..to carry the hyperlink url. */
function grid(text: string, url = "", from = 0, to = 0): LinkGrid {
  const chars = [...text];
  return {
    cols: chars.length,
    rowText: () => ({ text: chars, cols: chars.map((_, i) => i) }),
    hyperlinkAt: (c) => (url !== "" && c >= from && c < to ? url : ""),
    scrolledBack: () => false,
  };
}

/** A grid printing label over the hyperlink url, after "see ". */
function hyperlinked(label: string, url: string): LinkGrid {
  return grid(`see ${label} now`, url, 4, 4 + [...label].length);
}

test("a hyperlink whose text is not its file asks first", () => {
  const l = linkAt(hyperlinked("chart.png", "file:///home/demo/.ssh/id_ed25519"), 5, 0, true);
  assert.equal(l?.path, "/home/demo/.ssh/id_ed25519");
  assert.equal(l?.hidden, true);
});

test("a hyperlink that prints its file, or the file's last parts, opens at once", () => {
  for (const label of ["file:///home/demo/out/chart.png", "/home/demo/out/chart.png", "chart.png", "out/chart.png", "./out/chart.png"]) {
    const l = linkAt(hyperlinked(label, "file:///home/demo/out/chart.png"), 5, 0, true);
    assert.equal(l?.path, "/home/demo/out/chart.png", label);
    assert.equal(l?.hidden, undefined, label);
  }
  // A name that only ends like the file's is not its last part.
  assert.equal(linkAt(hyperlinked("art.png", "file:///home/demo/out/chart.png"), 5, 0, true)?.hidden, true);
});

test("a path or file:// URL printed as text opens at once", () => {
  assert.equal(linkAt(grid("see ./out/chart.png now"), 6, 0, true)?.hidden, undefined);
  const u = linkAt(grid("see file:///home/demo/chart.png now"), 6, 0, true);
  assert.equal(u?.path, "/home/demo/chart.png");
  assert.equal(u?.hidden, undefined);
});

test("another machine's path is no file link", () => {
  for (const p of ["//attacker/share/x.png", "\\\\attacker\\share\\x.png", "/\\attacker\\share", "//?/UNC/a/x", "\\\\.\\pipe\\x", "\\??\\UNC\\a\\x", "/??/UNC/a/x"]) {
    assert.equal(remotePath(p), true, p);
  }
  for (const p of ["/tmp/a.png", "C:/dir/a.png", "./a//b.png", "a.png"]) assert.equal(remotePath(p), false, p);
  for (const u of ["file:////attacker/share/x.png", "file:///%2F%2Fattacker/share/x.png", "file:///%5C%5Cattacker%5Cshare%5Cx.png"]) {
    assert.equal(fileURLPath(u), undefined, u);
  }
  assert.equal(linkAt(grid("see //attacker/share/x.png now"), 8, 0, true), undefined);
  assert.equal(linkAt(hyperlinked("x.png", "file:////attacker/share/x.png"), 5, 0, true), undefined);
});

test("a file URL's path is cleaned as the host cleans it, and a path cleaning changes asks", () => {
  // %2F decodes after the URL's own dot segments are resolved.
  const key = "file:///home/demo/.ssh/id_ed25519%2Fx%2F..";
  for (const label of ["..", "x/.."]) {
    const l = linkAt(hyperlinked(label, key), 5, 0, true);
    assert.equal(l?.path, "/home/demo/.ssh/id_ed25519", label);
    assert.equal(l?.hidden, true, label);
  }
  const long = linkAt(hyperlinked("chart.png", "file:///home/demo/Pictures/chart.png%2F%2E%2F..%2F..%2F.ssh%2Fid_ed25519"), 5, 0, true);
  assert.equal(long?.path, "/home/demo/.ssh/id_ed25519");
  assert.equal(long?.hidden, true);
  const printed = linkAt(grid("see file:///home/demo/Pictures/a.png%2F..%2F..%2F.ssh%2Fid_ed25519 now"), 6, 0, true);
  assert.equal(printed?.path, "/home/demo/.ssh/id_ed25519");
  assert.equal(printed?.hidden, true);
  assert.equal(fileURLPath("file:///home/demo/a.png%5C..%5C..%5Cid_ed25519"), undefined);
  assert.equal(linkAt(hyperlinked("tmp", "file:///tmp%2F.."), 5, 0, true), undefined);
});

test("cleanPath cleans as Go's filepath.Clean", () => {
  const cases: [string, string][] = [
    ["/a/./b/", "/a/b"],
    ["/../a", "/a"],
    ["a/../../b", "../b"],
    ["C:/a/../..", "C:/"],
    ["./a//b", "a/b"],
    ["/home/demo/x", "/home/demo/x"],
  ];
  for (const [p, want] of cases) assert.equal(cleanPath(p), want, p);
});
