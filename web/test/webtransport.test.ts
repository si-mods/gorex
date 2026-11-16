import assert from "node:assert/strict";
import test from "node:test";
import { parsePairingLink } from "../src/hosts";
import { parseCertHash, webTransportProblem } from "../src/webtransport";

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

test("a browser without WebTransport is told what it lacks, not that the host is down", () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, "WebTransport");
  Reflect.deleteProperty(globalThis, "WebTransport");
  try {
    const p = webTransportProblem("https://192.168.1.50:7298", new Error("Opening handshake failed."));
    assert.equal(p.title, "This browser can't reach a host directly");
    assert.match(p.text, /Safari 26\.4/);
    assert.doesNotMatch(p.text, /192\.168\.1\.50/);
  } finally {
    if (had) Object.defineProperty(globalThis, "WebTransport", had);
  }
});

test("a WebTransport handshake failure names the causes a browser never does", () => {
  const had = Object.getOwnPropertyDescriptor(globalThis, "WebTransport");
  Reflect.set(globalThis, "WebTransport", class {});
  try {
    const p = webTransportProblem("https://192.168.1.50:7298", new Error("Opening handshake failed."));
    assert.match(p.title, /local network/);
    assert.match(p.text, /192\.168\.1\.50:7298/);
    assert.match(p.text, /gorex rexapi lan status/);
    assert.match(String(p.details), /Opening handshake failed\./);
  } finally {
    if (had) Object.defineProperty(globalThis, "WebTransport", had);
    else Reflect.deleteProperty(globalThis, "WebTransport");
  }
});
