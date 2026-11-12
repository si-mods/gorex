// The Tailscale sign-in guide: a dialog, as the Settings dialog is (a
// frosted panel over a backdrop that only dims, DESIGN.md), that walks the
// user from built-in Tailscale off to their GoRex hosts listed. Its steps
// follow the node (tailnet.ts): what happens, the engine's download, the
// sign-in in a tab of its own, and the hosts found, with what to do when
// none are. Escape or Close hides it; a sign-in on its way goes on.
import { allowCommand, commandBox, Host, icon } from "./hosts";
import { ProgressView } from "./progress";
import { Tailnet, TailnetHost } from "./tailnet";

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

function button(label: string, cls = "host-add", glyph?: string): HTMLButtonElement {
  const b = el("button", cls);
  b.type = "button";
  if (glyph) b.append(icon(glyph));
  b.append(el("span", "", label));
  return b;
}

/** A link styled as a button, to a page of its own tab. */
function linkButton(label: string, href: string, cls = "host-add", glyph = "square-arrow-out-up-right"): HTMLAnchorElement {
  const a = el("a", cls);
  a.href = href;
  a.target = "_blank";
  a.rel = "noopener noreferrer";
  a.append(icon(glyph), el("span", "", label));
  return a;
}

/** What the guide needs of the page. */
export interface GuideApp {
  /** Connects to a host found on the tailnet. */
  connect(h: Host): void;
  /** The guide closed: the keyboard goes back to the terminal. */
  closed(): void;
}

type Step = "explain" | "load" | "signin" | "hosts";
const stepNames: [Step, string][] = [
  ["explain", "About"],
  ["load", "Download"],
  ["signin", "Sign in"],
  ["hosts", "Hosts"],
];

export class Guide {
  private dialog = el("dialog", "settings ts-guide");
  private steps = el("ol", "ts-steps");
  private body = el("div", "ts-body");
  private foot = el("div", "ts-foot");
  /** The step the user is on before Tailscale starts: the explanation. */
  private explained = false;
  /** The tab opened for the sign-in, within the click that asked for it. */
  private popup: Window | null = null;
  /** Whether the browser refused to open it. */
  private blocked = false;
  private shownStep?: Step;
  private progress?: ProgressView;
  private shownKey = "";

  constructor(private tailnet: Tailnet, private app: GuideApp) {
    this.dialog.setAttribute("aria-label", "Add hosts from Tailscale");
    const close = el("button", "pane-action settings-close");
    close.type = "button";
    close.title = "Close";
    close.setAttribute("aria-label", "Close");
    close.append(icon("x"));
    close.onclick = () => this.close();
    const head = el("header", "ts-head");
    const title = el("h1", "settings-title", "Add Hosts from Tailscale");
    title.id = "ts-guide-title";
    this.dialog.setAttribute("aria-labelledby", title.id);
    head.append(title, close);
    this.steps.setAttribute("aria-label", "Steps");
    this.body.setAttribute("aria-live", "polite");
    this.dialog.append(head, this.steps, this.body, this.foot);
    this.dialog.addEventListener("click", (e) => {
      if (e.target !== this.dialog) return;
      const r = this.dialog.getBoundingClientRect();
      if (e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom) this.close();
    });
    this.dialog.addEventListener("close", () => this.app.closed());
    document.body.append(this.dialog);
    tailnet.onChange(() => this.update());
  }

  get isOpen() {
    return this.dialog.open;
  }

  open() {
    this.explained = this.tailnet.on;
    if (!this.dialog.open) this.dialog.showModal();
    this.shownKey = "";
    this.update();
    this.focusFirst();
  }

  close() {
    if (this.dialog.open) this.dialog.close();
  }

  /** The step the node is at. */
  private step(): Step {
    const s = this.tailnet.status();
    if (s === "off" || !this.explained) return "explain";
    if (s === "loading" || s === "load-failed") return "load";
    if (s === "running" || s === "locked") return "hosts";
    return "signin";
  }

  /** Draws the step again when what it shows changed; a focused control keeps its focus. */
  update() {
    if (!this.dialog.open) return;
    const t = this.tailnet;
    // The sign-in tab goes to the login URL as soon as it comes.
    if (t.loginURL && this.popup && !this.popup.closed) {
      try {
        if (this.popup.location.href === "about:blank") this.popup.location.href = t.loginURL;
      } catch {
        // Already at Tailscale's page: another origin.
      }
    }
    if (t.status() === "running" && this.popup) this.popup = null;
    const step = this.step();
    const key = JSON.stringify([
      step, t.status(), t.load, t.loginURL, t.error, t.signingIn, this.blocked, t.searching, t.account, t.map?.tailnet,
      t.hosts.map((h) => [h.id, h.state, h.problem?.title]), t.unanswered, t.name,
    ]);
    if (key === this.shownKey) return;
    this.shownKey = key;
    const hadFocus = (document.activeElement as HTMLElement | null)?.dataset?.key;
    this.drawSteps(step);
    this.body.replaceChildren();
    this.foot.replaceChildren();
    switch (step) {
      case "explain":
        this.explain();
        break;
      case "load":
        this.load();
        break;
      case "signin":
        this.signin();
        break;
      case "hosts":
        this.hosts();
        break;
    }
    const again = hadFocus && this.dialog.querySelector<HTMLElement>(`[data-key="${CSS.escape(hadFocus)}"]`);
    if (again) again.focus();
    else if (step !== this.shownStep) this.focusFirst();
    this.shownStep = step;
  }

  private focusFirst() {
    (this.foot.querySelector<HTMLElement>(".primary") ?? this.body.querySelector<HTMLElement>("button, a, input") ?? this.foot.querySelector<HTMLElement>("button"))?.focus();
  }

  private drawSteps(step: Step) {
    const at = stepNames.findIndex(([s]) => s === step);
    this.steps.replaceChildren(
      ...stepNames.map(([, label], i) => {
        const li = el("li", i < at ? "done" : i === at ? "now" : "");
        const n = el("span", "ts-step-n");
        if (i < at) n.append(icon("check"));
        else n.textContent = String(i + 1);
        li.append(n, el("span", "ts-step-label", label));
        if (i === at) li.setAttribute("aria-current", "step");
        return li;
      }),
    );
  }

  private hero(glyph: string, title: string, text?: string, kind = "") {
    const h = el("div", `ts-hero ${kind}`);
    const g = el("span", "ts-hero-icon");
    g.append(icon(glyph));
    const words = el("div", "ts-hero-text");
    words.append(el("h2", "", title));
    if (text) words.append(el("p", "", text));
    h.append(g, words);
    this.body.append(h);
    return h;
  }

  private point(glyph: string, text: string, parent: HTMLElement = this.body) {
    const p = el("div", "ts-point");
    p.append(icon(glyph), el("p", "", text));
    parent.append(p);
    return p;
  }

  private explain() {
    const t = this.tailnet;
    this.hero("network", "Reach your GoRex hosts through Tailscale", "No Tailscale app and no VPN on this device: this page signs in to Tailscale itself.");
    const list = el("div", "ts-points");
    this.point("shield-check", `This tab joins your tailnet as a device of its own, named ${t.name}. It reaches your GoRex hosts through Tailscale's encrypted relays.`, list);
    this.point("log-in", "You sign in on Tailscale's own page, in a new tab, with your tailnet's account. This page never sees your password.", list);
    this.point("cloud-download", "The page first downloads Tailscale's engine, about 6 MB, from where it is hosted.", list);
    this.point("lock", "For this tab only: reloading or closing it signs this browser out, and Tailscale removes the device soon after.", list);
    this.body.append(list);
    const cancel = button("Not Now");
    cancel.onclick = () => this.close();
    const go = button("Continue", "host-add primary");
    go.onclick = () => {
      this.explained = true;
      this.tailnet.turnOn();
      this.update();
    };
    this.foot.append(cancel, go);
  }

  private load() {
    const l = this.tailnet.load;
    if (l.state === "failed") {
      this.hero("circle-alert", l.noEngine ? "Tailscale isn't available here" : "Tailscale's engine didn't load", l.message, "bad");
      if (l.noEngine) {
        this.point("info", "Build the page with Go installed (npm run build in web/), which adds the engine, or use a copy of the page that has it.");
      } else {
        this.point("info", "Check this device's connection and try again. A content blocker may also stop the download.");
      }
      const cancel = button("Close");
      cancel.onclick = () => this.close();
      this.foot.append(cancel);
      if (!l.noEngine) {
        const retry = button("Try Again", "host-add primary", "rotate-ccw");
        retry.dataset.key = "retry";
        retry.onclick = () => this.tailnet.turnOn();
        this.foot.append(retry);
      }
      return;
    }
    const starting = l.state === "loading" && l.phase === "start";
    this.hero("cloud-download", starting ? "Starting Tailscale…" : "Downloading Tailscale…", "Once per version: your browser keeps the engine for the next visit.");
    // The page's one progress bar (progress.ts), kept across redraws so a screen reader follows it.
    this.progress ??= new ProgressView("Tailscale", { download: "Downloading", start: "Unpacking and starting…", failed: "Didn't load" });
    this.progress.update(l.state === "loading" && l.phase === "download" ? { phase: "download", received: l.received, total: l.total || undefined } : { phase: "start" });
    this.body.append(this.progress.el);
    const cancel = button("Cancel");
    cancel.onclick = () => {
      this.tailnet.turnOff();
      this.close();
    };
    this.foot.append(cancel);
  }

  /** Opens the sign-in tab within the click, and asks the node for the URL to put in it. */
  private startSignIn() {
    this.blocked = false;
    let w: Window | null = null;
    try {
      w = window.open("", "_blank");
    } catch {
      w = null;
    }
    if (w) {
      try {
        // Tailscale's page gets no handle on this one.
        w.opener = null;
        w.document.title = "Tailscale";
        w.document.body.style.cssText = "font: 15px system-ui, sans-serif; color: #666; display: grid; place-items: center; height: 100vh; margin: 0";
        w.document.body.textContent = "Opening Tailscale's sign-in page…";
      } catch {}
    }
    this.popup = w;
    this.blocked = !w;
    this.tailnet.login();
  }

  private cancelSignIn() {
    try {
      this.popup?.close();
    } catch {}
    this.popup = null;
    this.blocked = false;
    this.tailnet.cancelLogin();
  }

  private signin() {
    const t = this.tailnet;
    const s = t.status();
    const signIn = (label = "Sign in with Tailscale") => {
      const b = button(label, "host-add primary", "log-in");
      b.dataset.key = "signin";
      b.onclick = () => this.startSignIn();
      return b;
    };
    const close = button("Close");
    close.onclick = () => this.close();
    if (s === "starting") {
      this.hero("loader-circle", "Starting Tailscale…", undefined, "busy");
      this.foot.append(close);
      return;
    }
    if (s === "needs-approval") {
      this.hero("loader-circle", "Waiting for approval", `Your tailnet approves new devices by hand. An admin must approve ${t.name} in the admin console's Machines page; this page goes on by itself once it is.`, "busy");
      this.body.append(linkButton("Open the Admin Console", "https://login.tailscale.com/admin/machines"));
      this.foot.append(close);
      return;
    }
    if (s === "signing-in") {
      const blocked = this.blocked || (this.popup?.closed ?? false);
      if (!t.loginURL) {
        this.hero("loader-circle", "Opening Tailscale's sign-in page…", undefined, "busy");
      } else if (blocked) {
        this.hero("circle-alert", "Open the sign-in page", "Your browser didn't open a new tab, or it was closed. Open the page with the button below, then come back to this tab.", "warn");
      } else {
        this.hero("loader-circle", "Waiting for you to sign in…", "Finish signing in on the Tailscale tab, then come back here. This page notices by itself.", "busy");
      }
      if (t.loginURL) {
        const row = el("div", "ts-link-row");
        const open = linkButton(blocked ? "Open the Sign-in Page" : "Open It Again", t.loginURL, blocked ? "host-add primary" : "host-add");
        open.dataset.key = "open";
        row.append(open);
        this.body.append(row);
        const url = el("div", "ts-url-note", "Or copy the link into a browser where you are signed in:");
        this.body.append(url, commandBox(t.loginURL));
      }
      const cancel = button("Cancel");
      cancel.dataset.key = "cancel";
      cancel.onclick = () => this.cancelSignIn();
      this.foot.append(cancel);
      return;
    }
    if (s === "error") {
      this.hero("wifi-off", "Can't reach Tailscale", t.error, "bad");
      this.foot.append(close, signIn("Try Again"));
      return;
    }
    if (s === "expired") {
      this.hero("log-in", "Your sign-in expired", `Sign in again to reach your hosts. ${t.name} keeps its name.`);
      this.foot.append(close, signIn("Sign in Again"));
      return;
    }
    this.hero("log-in", "Sign in to your tailnet", "A new tab opens at login.tailscale.com. Sign in with the account your GoRex hosts use, then come back to this tab.");
    this.point("info", `This browser joins as ${t.name}, an ephemeral device: Tailscale removes it once this tab closes.`);
    this.foot.append(close, signIn());
  }

  private hosts() {
    const t = this.tailnet;
    if (t.status() === "locked") {
      this.hero(
        "lock", "Tailnet Lock isn't supported",
        `Your tailnet uses Tailnet Lock, and this version of the page can't join a locked tailnet: ${t.name} is not signed by a trusted node. Sign out here, and use the Tailscale app on this device instead.`,
        "bad",
      );
      const out = button("Sign Out", "host-add", "log-out");
      out.onclick = () => t.signOut();
      const close = button("Close", "host-add primary");
      close.onclick = () => this.close();
      this.foot.append(out, close);
      return;
    }
    const who = el("div", "ts-who");
    const g = el("span", "ts-hero-icon ok");
    g.append(icon("circle-check"));
    const words = el("div", "ts-hero-text");
    words.append(el("h2", "", `Signed in as ${t.account || "you"}`));
    const tail = [t.map?.tailnet && `on ${t.map.tailnet}`, `this device is ${t.name}${t.map?.self.address ? ` (${t.map.self.address})` : ""}`].filter(Boolean).join(" · ");
    words.append(el("p", "", tail));
    who.append(g, words);
    this.body.append(who);

    const head = el("div", "ts-section");
    head.append(el("h3", "", "GoRex hosts"));
    if (t.searching) {
      const s = el("span", "ts-searching");
      s.append(icon("loader-circle"), el("span", "", "Looking for GoRex hosts…"));
      head.append(s);
    }
    this.body.append(head);
    if (t.hosts.length) {
      const list = el("div", "ts-hosts");
      for (const h of t.hosts) list.append(this.hostRow(h));
      this.body.append(list);
    } else if (!t.searching) {
      this.empty();
    }
    const again = button("Search Again", "host-add", "rotate-ccw");
    again.dataset.key = "again";
    again.onclick = () => t.searchAgain();
    again.disabled = t.searching;
    const done = button("Done", "host-add primary");
    done.onclick = () => this.close();
    this.foot.append(again, done);
  }

  private hostRow(h: TailnetHost): HTMLElement {
    const row = el("div", `ts-host ${h.state}`);
    const top = el("div", "ts-host-top");
    const g = el("span", "settings-host-icon small");
    g.append(icon(h.icon));
    const text = el("span", "settings-host-text");
    const line = el("span", "settings-host-state");
    const dot = h.state === "online" ? "connected" : h.state === "refused" ? "unreachable" : "none";
    line.append(el("i", `state-dot ${dot}`), el("span", "", h.state === "online" ? h.dns : h.state === "refused" ? "Refuses this browser" : "Offline"));
    text.append(el("span", "settings-host-name", h.name), line);
    top.append(g, text);
    if (h.state === "online") {
      const c = button("Connect", "host-add primary");
      c.dataset.key = `connect-${h.id}`;
      c.setAttribute("aria-label", `Connect to ${h.name}`);
      c.onclick = () => {
        this.close();
        this.app.connect(h);
      };
      top.append(c);
    }
    row.append(top);
    if (h.problem) {
      const why = el("div", "ts-host-why");
      why.append(el("strong", "", h.problem.title), el("p", "", h.problem.text));
      if (h.problem.command) why.append(commandBox(h.problem.command));
      row.append(why);
    }
    return row;
  }

  /** No hosts: how to set one up, and the devices that looked like one. */
  private empty() {
    const t = this.tailnet;
    const box = el("div", "ts-empty");
    box.append(el("h3", "", "No GoRex hosts found"));
    box.append(el("p", "", `The page lists the devices of ${t.account || "your account"} that run GoRex with built-in Tailscale. To add one:`));
    const steps = el("ol", "ts-howto");
    const item = (text: string, command?: string) => {
      const li = el("li");
      li.append(el("p", "", text));
      if (command) li.append(commandBox(command));
      steps.append(li);
    };
    item("On the host, open GoRex, then turn on built-in Tailscale and sign it in with this same account:", "gorex rexapi tailscale on");
    item("Wait until its status shows https: available. MagicDNS and HTTPS must be on in the tailnet's admin console (DNS page).", "gorex rexapi tailscale status");
    item("Let the host trust this page:", allowCommand());
    box.append(steps);
    if (t.unanswered.length) {
      box.append(el("p", "ts-unanswered", `Found but not answering as GoRex on port 443: ${t.unanswered.join(", ")}. Check the steps above on ${t.unanswered.length > 1 ? "them" : "it"}.`));
    }
    box.append(el("p", "ts-note", "The page finds hosts named <machine>-gorex, built-in Tailscale's default name. A host renamed otherwise isn't found in this version."));
    this.body.append(box);
  }
}
