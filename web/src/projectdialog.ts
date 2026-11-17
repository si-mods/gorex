// The New Project sheet and Change Folder (docs/projects.md, Desktop): a
// host, a folder browsed through gorex.dir.list, a name. The state is
// projects.ts's FolderBrowser; this draws it, as manage.ts's confirmDanger
// draws its question (the same frosted dialog).
import type { HostConn } from "./hostconn";
import { icon } from "./hosts";
import { describe } from "./pane";
import { t } from "./i18n";
import { defaultHost, FolderBrowser, folderName, hostState, HostState } from "./projects";

export interface Chosen {
  conn: HostConn;
  dir: string;
  /** The project's name; empty when the sheet asked for none. */
  name: string;
}

export interface SheetOptions {
  title: string;
  action: string;
  /** Every host's connection, in the sidebar's order. */
  hosts: HostConn[];
  /** The host to start on, when it can be picked. */
  host?: string;
  /** The folder to start in, on that host. */
  dir?: string;
  /** Ask for the project's name, which is the folder's by default. */
  named: boolean;
}

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function why(s: HostState, name: string): string {
  return s === "offline" ? t("projectHostOffline", { host: name }) : s === "old" ? t("projectHostOld", { host: name }) : t("projectHostNoMachine", { host: name });
}

/** Opens the sheet; the choice, or none when it is cancelled. */
export function chooseFolder(o: SheetOptions): Promise<Chosen | undefined> {
  return new Promise((resolve) => {
    const states = o.hosts.map((c) => ({ host: c, state: hostState({ connected: c.ref.connected, listsDirs: c.listsDirs, keepsProjects: c.keepsProjects, machine: c.machine }) }));
    const first = defaultHost(states.map((s) => ({ id: s.host.host.id, state: s.state })), o.host);
    let conn = o.hosts.find((c) => c.host.id === first);
    let named = false;
    let shownPath = "";
    let browser: FolderBrowser;

    const d = el("dialog", "confirm project-sheet");
    d.setAttribute("aria-label", o.title);
    const h = el("h2", undefined, o.title);

    const hostLabel = el("label", "sheet-label", t("projectHost"));
    const select = el("select", "sheet-field");
    for (const s of states) {
      const opt = el("option", undefined, s.state === "ok" ? s.host.host.name : why(s.state, s.host.host.name));
      opt.value = s.host.host.id;
      opt.disabled = s.state !== "ok";
      select.append(opt);
    }
    if (conn) select.value = conn.host.id;
    hostLabel.append(select);

    const bar = el("div", "sheet-bar");
    const home = el("button", "icon");
    home.type = "button";
    home.append(icon("house"));
    home.title = t("projectHome");
    home.setAttribute("aria-label", home.title);
    const up = el("button", "icon");
    up.type = "button";
    up.append(icon("arrow-up"));
    up.title = t("projectUp");
    up.setAttribute("aria-label", up.title);
    const path = el("input", "sheet-field");
    path.type = "text";
    path.spellcheck = false;
    path.autocomplete = "off";
    path.setAttribute("aria-label", t("projectPath"));
    bar.append(home, up, path);

    const list = el("ul", "sheet-list");
    const note = el("p", "sheet-note");
    const nameLabel = el("label", "sheet-label", t("projectName"));
    const name = el("input", "sheet-field");
    name.type = "text";
    name.autocomplete = "off";
    nameLabel.append(name);
    nameLabel.hidden = !o.named;

    const row = el("div", "confirm-buttons");
    const cancel = el("button", "host-add", t("commonCancel"));
    cancel.type = "button";
    const ok = el("button", "host-add primary", o.action);
    ok.type = "button";
    row.append(cancel, ok);
    d.append(h, hostLabel, bar, list, note, nameLabel, row);

    function draw() {
      const b = browser;
      // The field shows the folder listed; a path being typed stays until another folder is listed.
      if (b.path !== shownPath) {
        shownPath = b.path;
        path.value = b.path;
      }
      up.disabled = !b.canUp;
      home.disabled = !conn || b.loading;
      if (o.named && !named) name.value = folderName(b.path) || conn?.host.name || "";
      ok.disabled = !conn || !b.canChoose || (o.named && !name.value.trim());
      list.replaceChildren(
        ...b.dirs.map((dir) => {
          const li = el("li");
          const btn = el("button", "sheet-dir");
          btn.type = "button";
          btn.append(icon("folder"), el("span", undefined, dir));
          btn.onclick = () => void b.open(dir);
          li.append(btn);
          return li;
        }),
      );
      const host = conn?.host.name ?? "";
      note.textContent = !conn
        ? t("projectNoHosts")
        : b.error
          ? t("projectListFailed", { error: b.error })
          : b.code === "not_found"
            ? t("projectNotFound", { host })
            : b.code === "denied"
              ? t("projectDenied", { host })
              : b.more
                ? t("projectMore")
                : !b.loading && !b.dirs.length && b.path
                  ? t("projectNoFolders")
                  : "";
    }

    function start() {
      const c = conn;
      browser = new FolderBrowser(async (p) => {
        if (!c) throw new Error(t("projectNoHosts"));
        try {
          return await c.dirList(p);
        } catch (e) {
          throw new Error(describe(e));
        }
      }, draw);
      draw();
      if (c) void browser.go(o.dir && c.host.id === o.host ? o.dir : "");
    }

    select.onchange = () => {
      conn = o.hosts.find((c) => c.host.id === select.value);
      named = false;
      start();
    };
    home.onclick = () => void browser.goHome();
    up.onclick = () => void browser.up();
    path.onkeydown = (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        void browser.go(path.value.trim());
      }
    };
    name.oninput = () => {
      named = true;
      draw();
    };
    name.onkeydown = (e) => {
      if (e.key === "Enter" && !ok.disabled) ok.click();
    };

    let answer: Chosen | undefined;
    cancel.onclick = () => d.close();
    ok.onclick = () => {
      if (conn && browser.canChoose) answer = { conn, dir: browser.path, name: o.named ? name.value.trim() : "" };
      d.close();
    };
    d.addEventListener("close", () => {
      d.remove();
      resolve(answer);
    });
    document.body.append(d);
    d.showModal();
    start();
    (o.named ? name : path).focus();
  });
}
