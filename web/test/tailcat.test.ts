// The tailcat address the host form reads (docs/WEB-006-tailcat.md): the
// rexgo://tailcat link the QR dialog encodes, or the bare tc… string;
// anything else is a gateway URL or an error.
import assert from "node:assert/strict";
import { test } from "node:test";
import { parseTailcatAddress } from "../src/hosts";

const addr = "tcAB12cd34-ef56_gh78ij90";

test("a bare tc… address parses", () => {
  assert.deepEqual(parseTailcatAddress(addr), { address: addr });
  assert.deepEqual(parseTailcatAddress("  " + addr + "\n"), { address: addr });
});

test("a rexgo://tailcat link parses, with its name", () => {
  const link = "rexgo://tailcat?a=" + encodeURIComponent(addr) + "&n=alice-laptop";
  assert.deepEqual(parseTailcatAddress(link), { address: addr, name: "alice-laptop" });
});

test("a link's encoded name decodes", () => {
  const link = "rexgo://tailcat?a=" + encodeURIComponent(addr) + "&n=" + encodeURIComponent("studio mac");
  assert.deepEqual(parseTailcatAddress(link), { address: addr, name: "studio mac" });
});

test("a link without a name gives none", () => {
  assert.deepEqual(parseTailcatAddress("rexgo://tailcat?a=" + addr), { address: addr });
});

test("a gateway address is not a tailcat address", () => {
  assert.equal(parseTailcatAddress("192.168.1.20:7297"), undefined);
  assert.equal(parseTailcatAddress("https://alice-laptop:7297"), undefined);
  assert.equal(parseTailcatAddress(""), undefined);
});

test("a tailcat-looking string that is not one is not parsed", () => {
  assert.equal(parseTailcatAddress("tc"), undefined);
  assert.equal(parseTailcatAddress("tc123"), undefined);
  assert.equal(parseTailcatAddress("rexgo://tailcat"), undefined);
  assert.equal(parseTailcatAddress("rexgo://tailcat?a=bad"), undefined);
  assert.equal(parseTailcatAddress("rexgo://other?a=" + addr), undefined);
});
