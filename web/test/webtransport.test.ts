import assert from "node:assert/strict";
import test from "node:test";
import { parsePairingLink } from "../src/hosts";
import { parseCertHash } from "../src/webtransport";

test("parseCertHash decodes sha256-<base64url> to 32 bytes", () => {
  const raw = new Uint8Array(32);
  for (let i = 0; i < 32; i++) raw[i] = i;
  const b64url = Buffer.from(raw).toString("base64url");
  const buf = parseCertHash(`sha256-${b64url}`);
  assert.equal(buf.byteLength, 32);
  assert.deepEqual(new Uint8Array(buf), raw);
});

test("parseCertHash rejects non-32-byte digest", () => {
  assert.throws(() => parseCertHash("sha256-AQID"), /Invalid SHA-256/);
});

test("parsePairingLink extracts fragment host and wt hash", () => {
  const res = parsePairingLink("", "#host=https%3A%2F%2F192.168.1.50%3A7298&wt=sha256-abc");
  assert.deepEqual(res, { url: "https://192.168.1.50:7298", certHash: "sha256-abc" });
});

test("parsePairingLink supports legacy ?host= query param", () => {
  const res = parsePairingLink("?host=https%3A%2F%2Fexample.ts.net", "");
  assert.deepEqual(res, { url: "https://example.ts.net", certHash: undefined });
});
