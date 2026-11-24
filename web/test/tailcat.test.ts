// The tailcat address the host form reads (docs/WEB-006-tailcat.md): the
// rexgo://tailcat link the QR dialog encodes, or the bare tc… string;
// anything else is a gateway URL or an error.
import assert from "node:assert/strict";
import { test } from "node:test";
import { parseTailcatAddress } from "../src/hosts";
import { relayDenies } from "../src/tailcat";

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

// The relay's admit probe (docs/NET-004-public-relay.md): only an ok
// answer whose body says allow:false means the relay refused the key.
test("the probe asks the relay about the key", async () => {
  let got = "";
  const yes = await relayDenies(async (url) => {
    got = String(url);
    return new Response(JSON.stringify({ allow: false }));
  }, "https://derp.example", "nodekey:a=b");
  assert.equal(yes, true);
  assert.equal(got, "https://derp.example/derp/admit?key=" + encodeURIComponent("nodekey:a=b"));
});

test("a key the relay admits is not refused", async () => {
  const no = await relayDenies(async () => new Response(JSON.stringify({ allow: true })), "https://derp.example", "k");
  assert.equal(no, false);
});

test("a relay without the probe is not a refusal", async () => {
  const no = await relayDenies(async () => new Response("not found", { status: 404 }), "https://derp.example", "k");
  assert.equal(no, false);
});

test("a relay that does not answer is not a refusal", async () => {
  const no = await relayDenies(async () => Promise.reject(new Error("offline")), "https://derp.example", "k");
  assert.equal(no, false);
});

test("an answer that is not JSON is not a refusal", async () => {
  const no = await relayDenies(async () => new Response("<html>", { status: 200 }), "https://derp.example", "k");
  assert.equal(no, false);
});
