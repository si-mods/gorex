import assert from "node:assert/strict";
import test from "node:test";
import { joinHostURL, parseSharedHosts } from "../src/share";

test("joinHostURL preserves path prefix on shared host URLs", () => {
  assert.equal(joinHostURL("http://172.20.10.1:7300/h/abc", "/api/control"), "http://172.20.10.1:7300/h/abc/api/control");
  assert.equal(joinHostURL("http://172.20.10.1:7300/h/abc/", "/api/help"), "http://172.20.10.1:7300/h/abc/api/help");
  assert.equal(joinHostURL("http://127.0.0.1:8080", "/api/download?ticket=1"), "http://127.0.0.1:8080/api/download?ticket=1");
});

test("parseSharedHosts builds share-prefixed hosts with /h/<id> URLs", () => {
  const g = parseSharedHosts(
    {
      phone: "Yibo's iPhone",
      hosts: [
        { id: "mac-1", name: "MacBook", icon: "laptop" },
        { id: "a/b", name: "Linux" },
        { id: "", name: "Bad" },
      ],
    },
    "http://172.20.10.1:7300",
  );
  assert.deepEqual(g, {
    phone: "Yibo's iPhone",
    hosts: [
      { id: "share:mac-1", name: "MacBook", url: "http://172.20.10.1:7300/h/mac-1", icon: "laptop", kind: "share" },
      { id: "share:a/b", name: "Linux", url: "http://172.20.10.1:7300/h/a%2Fb", icon: "laptop", kind: "share" },
    ],
  });
  assert.equal(parseSharedHosts(null, "http://172.20.10.1:7300"), undefined);
});
