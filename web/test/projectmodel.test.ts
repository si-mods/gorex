// Projects, spaces and tabs as the web client shows them (src/projectmodel.ts),
// and the calls of a host's connection (src/hostconn.ts) against a fake gateway.
import assert from "node:assert/strict";
import { test } from "node:test";
import type { HostConn as HostConnType } from "../src/hostconn";
import { dirName, featureVersion, groupLook, groupProjects, hostModel, Offline, parseProject, projectColor, ProjectRow, SpaceStore } from "../src/projectmodel";

// hostconn.ts imports the page's modules, which look at `window` as they load; a bare one is enough here.
(globalThis as any).window ??= { addEventListener() {}, matchMedia: () => ({ matches: false, addEventListener() {} }) };
const { HostConn } = (await import("../src/hostconn")) as { HostConn: typeof HostConnType };

const day = 86400000;
const now = Date.UTC(2026, 10, 18);

class Mem {
  data = new Map<string, string>();
  getItem(k: string) {
    return this.data.get(k) ?? null;
  }
  setItem(k: string, v: string) {
    this.data.set(k, v);
  }
}

const row = (id: string, space: string, ago: number, name = id): ProjectRow => ({ id, name, dir: `/work/${id}`, space, used: ago < 0 ? 0 : now - ago * day, tabs: [] });

test("parseProject reads {dir, space, used}, and {} as the tabs with no project", () => {
  assert.deepEqual(parseProject({ dir: "/work/a", space: "Work", used: "2026-11-18T00:00:00Z" }), { dir: "/work/a", space: "Work", used: now });
  assert.deepEqual(parseProject({}), { dir: "", space: "", used: 0 });
  assert.equal(parseProject(undefined), undefined);
  assert.equal(parseProject("x"), undefined);
  // A time of the zero value is none.
  assert.equal(parseProject({ dir: "/a", used: "0001-01-01T00:00:00Z" })!.used, 0);
});

test("featureVersion reads the welcome's versions in either shape", () => {
  assert.equal(featureVersion({ features: ["gorex.projects"], feature_versions: { "gorex.projects": 2 } }, "gorex.projects"), 2);
  assert.equal(featureVersion({ features: [{ name: "gorex.projects", version: 2 }] }, "gorex.projects"), 2);
  assert.equal(featureVersion({ features: ["gorex.projects"] }, "gorex.projects"), 0);
  assert.equal(featureVersion(undefined, "gorex.projects"), 0);
});

test("a host's model has its projects and its tabs with no project apart", () => {
  const view = (...ids: string[]) => ({ windows: ids.map((id) => ({ window_id: id })) });
  const m = hostModel(
    {
      host: { id: "h1", name: "office" },
      sessions: [
        { id: "s1", label: "windflow", project: { dir: "/work/windflow", space: "Work", used: now }, view: view("w1", "w2") },
        { id: "s2", label: "", project: { dir: "", space: "", used: 0 }, view: view("w3") },
        // No view yet, or a session of a host with no projects: not shown.
        { id: "s3", label: "x", project: { dir: "/x", space: "", used: 0 } },
        { id: "s4", label: "y", view: view("w4") },
      ],
    },
    (w) => `tab ${w.window_id}`,
  );
  assert.deepEqual(m.projects.map((p) => [p.id, p.name, p.tabs.map((t) => t.title)]), [["s1", "windflow", ["tab w1", "tab w2"]]]);
  assert.deepEqual(m.loose, { id: "s2", tabs: [{ id: "w3", title: "tab w3" }] });
});

test("projects of a space are in order of last use, and the ones unused for 30 days go under Older", () => {
  const ps = [row("old", "Work", 31), row("new", "Work", 1), row("mid", "Work", 20), row("life", "Life", 0), row("never", "Work", -1)];
  const g = groupProjects(ps, "Work", now);
  assert.deepEqual(g.recent.map((p) => p.id), ["new", "mid", "never"].sort((a, b) => ps.find((p) => p.id === b)!.used - ps.find((p) => p.id === a)!.used));
  assert.deepEqual(g.older.map((p) => p.id), ["old"]);
  // 30 days exactly is not older.
  assert.deepEqual(groupProjects([row("edge", "", 30)], "", now).older, []);
  assert.deepEqual(groupProjects(ps, "Nowhere", now), { recent: [], older: [] });
});

test("dirName is a folder's name on Unix and Windows", () => {
  assert.equal(dirName("/home/me/code/windflow/"), "windflow");
  assert.equal(dirName("C:\\work\\trainer"), "trainer");
  assert.equal(dirName("/"), "/");
});

test("projectColor maps preset IDs to hex and leaves unset colors muted", () => {
  assert.equal(projectColor("rose"), "#e54666");
  assert.equal(projectColor("blue"), "#0090ff");
  assert.equal(projectColor("#123456"), "#123456");
  assert.equal(projectColor(""), undefined);
  assert.equal(projectColor(undefined), undefined);
  assert.equal(projectColor("unknown"), undefined);
});

test("the picker lists the names the projects carry, Default, and the ones made here, in the user's order", () => {
  const s = new SpaceStore(new Mem());
  assert.deepEqual(s.names(["Work", "Life", "Work"]), ["", "Life", "Work"]);
  s.add("Ideas");
  assert.equal(s.current, "Ideas");
  assert.deepEqual(s.names(["Work"]), ["", "Ideas", "Work"]);
  s.order = ["Work", "Ideas"];
  assert.deepEqual(s.names(["Work", "Life"]), ["Work", "Ideas", "", "Life"]);
  // A space that vanished: the shown one falls back to Default.
  assert.equal(s.shown(["", "Work"]), "");
  s.setCurrent("Work");
  assert.equal(s.shown(["", "Work"]), "Work");
});

test("a space made here leaves the made list once a project carries it, and drops with its last project", () => {
  const s = new SpaceStore(new Mem());
  s.add("Ideas");
  s.prune(new Set(["Work"]));
  assert.deepEqual(s.names(["Work"]), ["", "Ideas", "Work"]);
  s.prune(new Set(["Ideas"]));
  assert.deepEqual(s.names(["Ideas"]), ["", "Ideas"]);
  assert.deepEqual(s.names([]), [""]);
});

test("the space store keeps its colors, order and renames, and reads them back", () => {
  const mem = new Mem();
  const a = new SpaceStore(mem);
  a.setColor("Work", "#e5484d");
  a.add("Ideas");
  a.setCurrent("Work");
  a.rename("Work", "Job", ["h-away"]);
  const b = new SpaceStore(mem);
  assert.equal(b.current, "Job");
  assert.deepEqual(b.colors, { Job: "#e5484d" });
  assert.deepEqual(b.made, ["Ideas"]);
  assert.deepEqual(b.pending, [{ host: "h-away", from: "Work", to: "Job" }]);
});

test("a rename waits for the host that is away, and maps its projects' space meanwhile", () => {
  const s = new SpaceStore(new Mem());
  s.rename("Work", "Job", ["away"]);
  s.rename("Job", "Office", ["away"]);
  assert.equal(s.mapSpace("away", "Work"), "Office");
  assert.equal(s.mapSpace("away", "Life"), "Life");
  // A host that took them shows its own names.
  assert.equal(s.mapSpace("here", "Work"), "Work");
  const w = s.waiting("away");
  assert.equal(w.length, 2);
  s.done(w[0]);
  s.done(w[1]);
  assert.equal(s.mapSpace("away", "Work"), "Work");
  assert.deepEqual(s.pending, []);
});

test("a storage that is missing, corrupt or throws does not stop the page", () => {
  const bad = { getItem: () => "not json", setItem: () => { throw new Error("quota"); } };
  const s = new SpaceStore(bad);
  s.add("Ideas");
  assert.equal(s.current, "Ideas");
  const o = new Offline(bad);
  o.put({ host: "h", name: "h", projects: [] });
  assert.ok(o.get("h"));
  assert.equal(new SpaceStore(undefined).current, "");
  assert.equal(new Offline(undefined).get("h"), undefined);
});

test("the offline copy keeps each host's projects, is replaced as the host is read, and reads back", () => {
  const mem = new Mem();
  const o = new Offline(mem);
  const m = { host: "h1", name: "office", offered: undefined, projects: [row("p", "Work", 1)], loose: { id: "l", tabs: [{ id: "w", title: "dev" }] } };
  o.put(m);
  const again = new Offline(mem);
  assert.deepEqual(again.get("h1"), m);
  o.put({ ...m, projects: [] });
  assert.deepEqual(new Offline(mem).get("h1")!.projects, []);
  o.drop("h1");
  assert.equal(new Offline(mem).get("h1"), undefined);
  // A damaged entry is skipped, not thrown.
  mem.setItem("gorex.web.projects-cache", JSON.stringify({ x: 5, y: { projects: [{ nope: 1 }, { id: "p" }] } }));
  assert.deepEqual(new Offline(mem).get("y")!.projects.map((p) => p.id), ["p"]);
});

/** A HostConn with a fake control connection, which records the calls. */
function fake(welcome: any, answers: Record<string, any> = {}) {
  const calls: [string, any][] = [];
  const c = new HostConn({ id: "h1", name: "office", url: "http://x", kind: "direct" } as any, {} as any, {} as any, {} as any);
  c.control = {
    features: new Set(welcome.features ?? []),
    welcome,
    call: async (method: string, payload: any) => {
      calls.push([method, payload]);
      if (answers[method] instanceof Error) throw answers[method];
      return { seq: 1, payload: answers[method] ?? {} };
    },
  } as any;
  return { c, calls };
}

test("a host has projects of version 2 only when its welcome says so", () => {
  assert.ok(fake({ features: ["gorex.projects"], feature_versions: { "gorex.projects": 2 } }).c.v2);
  assert.ok(!fake({ features: ["gorex.projects"] }).c.v2);
  assert.ok(!fake({ features: [] }).c.v2);
});

test("project create, move, update and space rename call the host's methods with the doc's arguments", async () => {
  const { c, calls } = fake(
    { features: ["gorex.projects"], feature_versions: { "gorex.projects": 2 } },
    { "gorex.project.create": { project_id: "session:1", tab_id: "window:1" } },
  );
  assert.deepEqual(await c.projectCreate("windflow", "/work/windflow", "Work"), { project_id: "session:1", tab_id: "window:1", exists: false });
  await c.projectMove("window:1", "");
  await c.projectUpdate("session:1", { name: "wf" });
  await c.spaceRename("Work", "Job");
  assert.deepEqual(calls, [
    ["gorex.project.create", { name: "windflow", dir: "/work/windflow", space: "Work" }],
    ["gorex.project.move", { tab_id: "window:1", project_id: "" }],
    ["gorex.project.update", { project_id: "session:1", name: "wf" }],
    ["gorex.space.rename", { from: "Work", to: "Job" }],
    ["gorex.space.list", undefined],
  ]);
});

test("a folder that is a project already is an exists error that names the project", async () => {
  const e = Object.assign(new Error("/work/x is a project already"), { code: "exists", projectID: "session:9" });
  const { c } = fake({ features: [] }, { "gorex.project.create": e });
  assert.deepEqual(await c.projectCreate("x", "/work/x", ""), { project_id: "session:9", exists: true });
});

test("gorex.space.list adds the names a host offers to the model, and the copy keeps them", async () => {
  const { c } = fake({ features: ["gorex.projects"], feature_versions: { "gorex.projects": 2 } }, { "gorex.space.list": { spaces: ["Work", "", "Old"] } });
  await c.readSpaces();
  assert.deepEqual(c.offered, ["Work", "", "Old"]);
  const m = hostModel({ host: { id: "h1", name: "x" }, sessions: [], offered: c.offered }, () => "");
  assert.deepEqual(m.offered, ["Work", "", "Old"]);
  const mem = new Mem();
  new Offline(mem).put(m);
  assert.deepEqual(new Offline(mem).get("h1")!.offered, ["Work", "", "Old"]);
});

test("a delete drops the space's color, order and made name, shows Default, and waits for the host that is away", () => {
  const mem = new Mem();
  const a = new SpaceStore(mem);
  a.setColor("Work", "#e5484d");
  a.add("Ideas");
  a.add("Work");
  a.order = ["Work", "Ideas"];
  a.delete("Work", ["away"]);
  const b = new SpaceStore(mem);
  assert.equal(b.current, "");
  assert.deepEqual(b.colors, {});
  assert.deepEqual(b.made, ["Ideas"]);
  assert.deepEqual(b.order, ["Ideas"]);
  assert.deepEqual(b.pending, [{ host: "away", from: "Work", to: "" }]);
  // The host away's projects of the space show in Default meanwhile, and the picker drops the name.
  assert.equal(b.mapSpace("away", "Work"), "");
  assert.ok(!b.names([b.mapSpace("away", "Work")]).includes("Work"));
  // Default is not deleted.
  b.delete("", ["away"]);
  assert.equal(b.pending.length, 1);
});

test("space delete calls gorex.space.delete, and a host of v0.41.0 a rename to Default", async () => {
  const v2 = { features: ["gorex.projects"], feature_versions: { "gorex.projects": 2 } };
  const { c, calls } = fake(v2, { "gorex.space.delete": { projects: 2 } });
  assert.equal(await c.spaceDelete("Work"), 2);
  assert.deepEqual(calls, [
    ["gorex.space.delete", { name: "Work" }],
    ["gorex.space.list", undefined],
  ]);
  const missing = Object.assign(new Error("no such method"), { code: "method_not_found" });
  const old = fake(v2, { "gorex.space.delete": missing, "gorex.space.rename": { projects: 1 } });
  assert.equal(await old.c.spaceDelete("Work"), 1);
  assert.deepEqual(old.calls.slice(0, 2), [
    ["gorex.space.delete", { name: "Work" }],
    ["gorex.space.rename", { from: "Work", to: "" }],
  ]);
  const other = Object.assign(new Error("denied"), { code: "forbidden" });
  await assert.rejects(fake(v2, { "gorex.space.delete": other }).c.spaceDelete("Work"), /denied/);
});

test("a group points its chevron by its state, and none with no tabs", () => {
  assert.equal(groupLook(true, false, 2).chevron, "open");
  assert.equal(groupLook(false, false, 2).chevron, "closed");
  // A project, Older or No Project with no tab has no chevron, open or not.
  assert.equal(groupLook(true, false, 0).chevron, "none");
  assert.equal(groupLook(false, true, 0).chevron, "none");
});

test("only the shown group, open with tabs, has the face; shut or empty, its row is filled instead", () => {
  assert.deepEqual(groupLook(true, true, 3), { chevron: "open", face: true, pill: false });
  assert.deepEqual(groupLook(false, true, 3), { chevron: "closed", face: false, pill: true });
  assert.deepEqual(groupLook(true, true, 0), { chevron: "none", face: false, pill: true });
  // A group the window does not show has neither, open or shut.
  assert.deepEqual(groupLook(true, false, 3), { chevron: "open", face: false, pill: false });
  assert.deepEqual(groupLook(false, false, 3), { chevron: "closed", face: false, pill: false });
});
