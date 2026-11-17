import assert from "node:assert/strict";
import test from "node:test";
import { parsePairingLink } from "../src/hosts";
import { directTransport } from "../src/rex";
import { withToken } from "../src/share";
import { parseCertHash, webTransport, webTransportProblem } from "../src/webtransport";

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

function stubPermission(state: string | undefined) {
  const had = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  Reflect.defineProperty(globalThis, "navigator", {
    value: state === undefined ? {} : { permissions: { query: async () => ({ state }) } },
    configurable: true,
  });
  return () => {
    if (had) Reflect.defineProperty(globalThis, "navigator", had);
    else Reflect.deleteProperty(globalThis, "navigator");
  };
}

function stubWebTransport() {
  const had = Object.getOwnPropertyDescriptor(globalThis, "WebTransport");
  Reflect.set(globalThis, "WebTransport", class {});
  return () => {
    if (had) Reflect.defineProperty(globalThis, "WebTransport", had);
    else Reflect.deleteProperty(globalThis, "WebTransport");
  };
}

test("a browser without WebTransport is told what it lacks, not that the host is down", async () => {
  const restoreWT = stubWebTransport();
  Reflect.deleteProperty(globalThis, "WebTransport");
  const restoreNav = stubPermission("prompt");
  try {
    const p = await webTransportProblem("https://192.168.1.50:7298", new Error("Opening handshake failed."));
    assert.equal(p.title, "This browser can't reach a host directly");
    assert.match(p.text, /Safari 26\.4/);
    assert.doesNotMatch(p.text, /192\.168\.1\.50/);
  } finally {
    restoreNav();
    restoreWT();
  }
});

test("a denied local network permission is named as such", async () => {
  const restoreWT = stubWebTransport();
  const restoreNav = stubPermission("denied");
  try {
    const p = await webTransportProblem("https://192.168.1.50:7298", new Error("Opening handshake failed."));
    assert.equal(p.title, "This site isn't allowed on your local network");
    assert.match(p.text, /Site settings, Additional permissions/);
    assert.match(String(p.details), /Opening handshake failed\./);
  } finally {
    restoreNav();
    restoreWT();
  }
});

test("a permission never decided sends the user to allow it", async () => {
  const restoreWT = stubWebTransport();
  const restoreNav = stubPermission("prompt");
  try {
    const p = await webTransportProblem("https://192.168.1.50:7298");
    assert.equal(p.title, "Let this page reach your local network");
    assert.match(p.text, /192\.168\.1\.50:7298/);
    assert.equal(p.details, undefined);
  } finally {
    restoreNav();
    restoreWT();
  }
});

test("a granted permission leaves the causes a browser never names", async () => {
  const restoreWT = stubWebTransport();
  const restoreNav = stubPermission("granted");
  try {
    const p = await webTransportProblem("https://192.168.1.50:7298", new Error("Opening handshake failed."));
    assert.match(p.title, /local network/);
    assert.match(p.text, /gorex rexapi lan status/);
  } finally {
    restoreNav();
    restoreWT();
  }
});

test("a browser with no such permission still gets the causes", async () => {
  const restoreWT = stubWebTransport();
  const restoreNav = stubPermission(undefined);
  try {
    const p = await webTransportProblem("https://192.168.1.50:7298");
    assert.match(p.text, /gorex rexapi lan status/);
  } finally {
    restoreNav();
    restoreWT();
  }
});

const tok = "test-token-AAAA";
const lan = "https://192.0.2.10:7298";

test("parsePairingLink reads k from the fragment, and from a legacy query", () => {
  const hash = `#host=${encodeURIComponent(lan)}&wt=sha256-abc&k=${tok}`;
  assert.deepEqual(parsePairingLink("", hash), { url: lan, certHash: "sha256-abc", token: tok });
  assert.equal(parsePairingLink(`?host=${encodeURIComponent(lan)}&k=${tok}`, "")?.token, tok);
  assert.equal(parsePairingLink("", `#host=${encodeURIComponent(lan)}`)?.token, undefined);
});

test("withToken adds k with ? or &, and nothing without a token", () => {
  assert.equal(withToken("https://192.0.2.10:7298/api/wt", tok), `https://192.0.2.10:7298/api/wt?k=${tok}`);
  assert.equal(withToken("http://192.0.2.10:7297/api/download?ticket=1", tok), `http://192.0.2.10:7297/api/download?ticket=1&k=${tok}`);
  assert.equal(withToken("http://192.0.2.10:7297/api/control"), "http://192.0.2.10:7297/api/control");
  assert.equal(withToken("http://192.0.2.10:7297/api/control", ""), "http://192.0.2.10:7297/api/control");
});

test("directTransport puts k on its WebSocket and fetch URLs only with a token", async () => {
  const urls: string[] = [];
  const hadWS = Object.getOwnPropertyDescriptor(globalThis, "WebSocket");
  const hadFetch = Object.getOwnPropertyDescriptor(globalThis, "fetch");
  Reflect.set(globalThis, "WebSocket", class { constructor(u: string) { urls.push(u); } });
  Reflect.set(globalThis, "fetch", async (u: string) => { urls.push(u); return new Response(""); });
  try {
    const withK = directTransport("http://192.0.2.10:7297", tok);
    withK.socket("/api/control", "p");
    withK.socket("/api/data", "p");
    await withK.fetch("/api/download?ticket=1");
    await withK.fetch("/api/spaces");
    assert.equal(withK.url?.("/api/upload"), `http://192.0.2.10:7297/api/upload?k=${tok}`);
    const plain = directTransport("http://192.0.2.10:7297");
    plain.socket("/api/control", "p");
    await plain.fetch("/api/spaces");
    assert.deepEqual(urls, [
      `ws://192.0.2.10:7297/api/control?k=${tok}`,
      `ws://192.0.2.10:7297/api/data?k=${tok}`,
      `http://192.0.2.10:7297/api/download?ticket=1&k=${tok}`,
      `http://192.0.2.10:7297/api/spaces?k=${tok}`,
      "ws://192.0.2.10:7297/api/control",
      "http://192.0.2.10:7297/api/spaces",
    ]);
  } finally {
    if (hadWS) Reflect.defineProperty(globalThis, "WebSocket", hadWS); else Reflect.deleteProperty(globalThis, "WebSocket");
    if (hadFetch) Reflect.defineProperty(globalThis, "fetch", hadFetch);
  }
});

test("webTransport puts k on the CONNECT URL only with a token", async () => {
  const urls: string[] = [];
  const had = Object.getOwnPropertyDescriptor(globalThis, "WebTransport");
  Reflect.set(globalThis, "WebTransport", class {
    ready = Promise.reject(new Error("stop"));
    constructor(u: string) { urls.push(u); this.ready.catch(() => {}); }
  });
  const hash = "sha256-" + Buffer.alloc(32).toString("base64url");
  try {
    await assert.rejects(webTransport(lan, hash, tok).fetch("/api/spaces"), /stop/);
    await assert.rejects(webTransport(lan, hash).fetch("/api/spaces"), /stop/);
    assert.deepEqual(urls, [`${lan}/api/wt?k=${tok}`, `${lan}/api/wt`]);
  } finally {
    if (had) Reflect.defineProperty(globalThis, "WebTransport", had); else Reflect.deleteProperty(globalThis, "WebTransport");
  }
});
