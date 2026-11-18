// The project rules (src/projects.ts) and the wording of the catalogs.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { test } from "node:test";
import { browserCrumbs, childPath, collapseCrumbs, crumbs, defaultHost, DirAnswer, FolderBrowser, folderName, HostRef, hostState, isFolderMissing, parse, parseDirs, planNewWindow, projectLine } from "../src/projects";

const office: HostRef = { id: "h-office", name: "office", machine: "M-office", connected: true };
const laptop: HostRef = { id: "h-laptop", name: "laptop", machine: "M-laptop", connected: true };
const proj = (machine: string, dir = "/home/me/windflow") => parse({ home_machine: machine, dir });

test("parse reads a session's project, and none without a folder", () => {
  assert.deepEqual(parse({ home_machine: "M", dir: "/a" }), { home_machine: "M", dir: "/a" });
  assert.equal(parse(undefined), undefined);
  assert.equal(parse({ home_machine: "M", dir: "" }), undefined);
  assert.deepEqual(parse({ dir: "/a" }), { home_machine: "", dir: "/a" });
});

test("New Window without a project is a plain window", () => {
  assert.deepEqual(planNewWindow(undefined, office.id, [office]), { kind: "plain" });
});

test("New Window opens on the home host with the folder", () => {
  assert.deepEqual(planNewWindow(proj("M-office"), office.id, [office, laptop]), { kind: "open", host: office, cwd: "/home/me/windflow", here: true });
  // The session is on the laptop; the home is the office.
  assert.deepEqual(planNewWindow(proj("M-office"), laptop.id, [laptop, office]), { kind: "open", host: office, cwd: "/home/me/windflow", here: false });
});

test("New Window is refused when the home host is not connected, and opens nowhere else", () => {
  const away = { ...office, connected: false };
  assert.deepEqual(planNewWindow(proj("M-office"), laptop.id, [laptop, away]), { kind: "offline", host: away });
});

test("New Window is refused when no host is the home's machine", () => {
  assert.deepEqual(planNewWindow(proj("M-gone"), laptop.id, [laptop, office]), { kind: "unknown" });
  assert.deepEqual(planNewWindow(proj(""), laptop.id, [laptop]), { kind: "unknown" });
  // A host whose machine is not known yet is no match.
  assert.deepEqual(planNewWindow(proj("M-office"), laptop.id, [laptop, { ...office, machine: undefined }]), { kind: "unknown" });
});

test("of two hosts of one machine, the connected one is the home", () => {
  const lan = { ...office, id: "lan", connected: false };
  const tail = { ...office, id: "tail" };
  const plan = planNewWindow(proj("M-office"), laptop.id, [laptop, lan, tail]);
  assert.equal(plan.kind === "open" && plan.host.id, "tail");
});

test("the line under a project's name names the host when it is another machine", () => {
  assert.deepEqual(projectLine(proj("M-office")!, office, [office, laptop]), { unknown: false, dir: "/home/me/windflow" });
  assert.deepEqual(projectLine(proj("M-office")!, laptop, [office, laptop]), { host: "office", unknown: false, dir: "/home/me/windflow" });
  assert.deepEqual(projectLine(proj("M-gone")!, laptop, [laptop]), { unknown: true, dir: "/home/me/windflow" });
  assert.deepEqual(projectLine(proj("M-laptop")!, laptop, [laptop]), { unknown: false, dir: "/home/me/windflow" });
});

test("folder_missing is a conflict that names the code", () => {
  assert.ok(isFolderMissing({ code: "conflict", message: "folder_missing: /x is gone" }));
  assert.ok(!isFolderMissing({ code: "conflict", message: "busy" }));
  assert.ok(!isFolderMissing(new Error("folder_missing")));
});

test("a host can be picked when connected, able and its machine known", () => {
  const ok = { connected: true, listsDirs: true, keepsProjects: true, machine: "M" };
  assert.equal(hostState(ok), "ok");
  assert.equal(hostState({ ...ok, connected: false }), "offline");
  assert.equal(hostState({ ...ok, listsDirs: false }), "old");
  assert.equal(hostState({ ...ok, keepsProjects: false }), "old");
  assert.equal(hostState({ ...ok, machine: undefined }), "nomachine");
});

test("New Project starts on the preferred host if it can be picked, else the first that can", () => {
  const hs = [
    { id: "a", state: "offline" as const },
    { id: "b", state: "ok" as const },
    { id: "c", state: "ok" as const },
  ];
  assert.equal(defaultHost(hs, "c"), "c");
  assert.equal(defaultHost(hs, "a"), "b");
  assert.equal(defaultHost([{ id: "a", state: "old" }], "a"), undefined);
});

test("paths: join, crumbs and the folder's name, on Unix and Windows", () => {
  assert.equal(childPath("/home/me", "src"), "/home/me/src");
  assert.equal(childPath("/", "usr"), "/usr");
  assert.equal(childPath("C:\\Users", "me"), "C:\\Users\\me");
  assert.equal(childPath("C:\\", "Users"), "C:\\Users");
  assert.equal(childPath("/", "C:\\"), "C:\\"); // Windows lists its drives at "/"
  assert.deepEqual(crumbs("/home/me"), [
    { name: "/", path: "/" },
    { name: "home", path: "/home" },
    { name: "me", path: "/home/me" },
  ]);
  assert.deepEqual(crumbs("C:\\Users\\me").map((c) => c.path), ["C:\\", "C:\\Users", "C:\\Users\\me"]);
  assert.equal(folderName("/home/me/windflow"), "windflow");
  assert.equal(folderName("C:\\Users\\me\\windflow"), "windflow");
  assert.equal(folderName("C:\\"), "C:");
  assert.equal(folderName("/"), "");
});

const answer = (path: string, dirs: string[], extra: Partial<DirAnswer> = {}): DirAnswer => ({
  path,
  parent: path === "/" ? "" : path.slice(0, path.lastIndexOf("/")) || "/",
  home: "/home/me",
  dirs,
  more: false,
  ...extra,
});

test("parseDirs reads the host's answer", () => {
  const a = parseDirs({ path: "/a", parent: "/", home: "/h", dirs: ["x", 3, "y"], more: true, code: "denied" });
  assert.deepEqual(a, { path: "/a", parent: "/", home: "/h", dirs: ["x", "y"], more: true, code: "denied" });
  assert.throws(() => parseDirs({}));
});

test("the folder browser goes Home, down, Up and to a typed path", async () => {
  const tree: Record<string, DirAnswer> = {
    "": answer("/home/me", ["code", "docs"]),
    "/home/me": answer("/home/me", ["code", "docs"]),
    "/home/me/code": answer("/home/me/code", ["windflow"]),
    "/srv": answer("/srv", []),
  };
  const asked: string[] = [];
  const b = new FolderBrowser(async (p) => (asked.push(p), tree[p] ?? answer(p, [], { code: "not_found" })));
  await b.go("");
  assert.equal(b.path, "/home/me");
  assert.ok(b.canChoose);
  await b.open("code");
  assert.equal(b.path, "/home/me/code");
  assert.deepEqual(b.dirs, ["windflow"]);
  await b.up();
  assert.equal(b.path, "/home/me");
  await b.go("/srv");
  assert.equal(b.path, "/srv");
  await b.goHome();
  assert.equal(b.path, "/home/me");
  assert.deepEqual(asked, ["", "/home/me/code", "/home/me", "/srv", "/home/me"]);
});

test("the folder browser cannot choose a missing folder, and keeps the listing when a request fails", async () => {
  let fail = false;
  const b = new FolderBrowser(async (p) => {
    if (fail) throw new Error("No response");
    return p === "/nope" ? answer(p, ["x"], { code: "not_found" }) : answer(p || "/home/me", ["a"]);
  });
  await b.go("");
  assert.ok(b.canChoose);
  await b.go("/nope");
  assert.equal(b.code, "not_found");
  assert.deepEqual(b.dirs, []);
  assert.ok(!b.canChoose);
  await b.go("");
  fail = true;
  await b.go("/x");
  assert.equal(b.error, "No response");
  assert.equal(b.path, "/home/me");
  assert.ok(!b.canChoose);
  fail = false;
  await b.go("");
  assert.equal(b.error, undefined);
  assert.ok(b.canChoose);
});

test("of two requests the later wins, whichever answers last", async () => {
  const waits: Record<string, () => void> = {};
  const b = new FolderBrowser((p) => new Promise((r) => (waits[p] = () => r(answer(p, [])))));
  const first = b.go("/a");
  const second = b.go("/b");
  waits["/b"]();
  await second;
  waits["/a"]();
  await first;
  assert.equal(b.path, "/b");
  assert.ok(!b.loading);
});

test("Up at a root does nothing", async () => {
  const b = new FolderBrowser(async () => answer("/", ["usr"]));
  await b.go("/");
  assert.ok(!b.canUp);
  await b.up();
  assert.equal(b.path, "/");
});

test("no message of the page says Space where a project is meant", () => {
  const reads = (lang: string) => JSON.parse(readFileSync(join(process.cwd(), "locales", `${lang}.json`), "utf8")) as Record<string, any>;
  const en = reads("en");
  for (const [k, v] of Object.entries(en)) {
    if (k.startsWith("@") || typeof v !== "string") continue;
    assert.ok(!/\bspaces?\b/i.test(v), `en ${k}: ${v}`);
  }
  assert.equal(en.cmdNewSpace, "New Project…");
  assert.equal(en.cmdCloseSpace, "Close Project");
  const zh = reads("zh-Hans");
  for (const [k, v] of Object.entries(zh)) {
    if (typeof v === "string") assert.ok(!v.includes("空间"), `zh ${k}: ${v}`);
  }
});

const drives = () => answer("/", ["C:\\", "D:\\"]);

test("the Windows drives listing cannot be chosen, and is one crumb", async () => {
  const b = new FolderBrowser(async () => drives());
  await b.go("/");
  assert.ok(b.atDrives);
  assert.ok(!b.canChoose);
  assert.deepEqual(browserCrumbs(b.path, b.atDrives, "This PC"), [{ name: "This PC", path: "/" }]);
  // A Unix root is a folder: it can be chosen.
  const u = new FolderBrowser(async () => answer("/", ["usr", "home"]));
  await u.go("/");
  assert.ok(!u.atDrives);
  assert.ok(u.canChoose);
});

test("a Windows path's crumbs start at This PC, a Unix path's at its root", () => {
  assert.deepEqual(browserCrumbs("C:\\a\\b", false, "PC").map((c) => c.name), ["PC", "C:\\", "a", "b"]);
  assert.deepEqual(browserCrumbs("/a/b", false, "PC").map((c) => c.name), ["/", "a", "b"]);
});

test("collapseCrumbs keeps the first and the last, and hides the ones after the first", () => {
  const cs = ["/", "a", "b", "c", "d"];
  assert.deepEqual(collapseCrumbs(cs, 0), { head: ["/"], gone: [], tail: ["a", "b", "c", "d"] });
  assert.deepEqual(collapseCrumbs(cs, 2), { head: ["/"], gone: ["a", "b"], tail: ["c", "d"] });
  assert.deepEqual(collapseCrumbs(cs, 99), { head: ["/"], gone: ["a", "b", "c"], tail: ["d"] });
  assert.deepEqual(collapseCrumbs(["x"], 3), { head: ["x"], gone: [], tail: [] });
});

test("goNearest lists the nearest folder that exists above a missing one", async () => {
  const tree: Record<string, DirAnswer> = {
    "/home/me": answer("/home/me", ["code"]),
    "": answer("/home/me", ["code"]),
  };
  const b = new FolderBrowser(async (p) => tree[p] ?? answer(p, [], { code: "not_found" }));
  await b.goNearest("/home/me/gone/deeper");
  assert.equal(b.path, "/home/me");
  assert.equal(b.missing, "/home/me/gone/deeper");
  assert.equal(b.code, undefined);
  assert.ok(b.canChoose);
  await b.go("/home/me");
  assert.equal(b.missing, undefined);
  // Nothing above exists: the home directory.
  await b.goNearest("/zzz/y");
  assert.equal(b.path, "/home/me");
  assert.equal(b.missing, "/zzz/y");
  // A folder that is there is listed as it is.
  await b.goNearest("/home/me");
  assert.equal(b.missing, undefined);
});
