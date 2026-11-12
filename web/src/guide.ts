// The Tailscale sign-in guide: a dialog, as the Settings dialog is (a
// frosted panel over a backdrop that only dims, DESIGN.md), that walks the
// user from built-in Tailscale off to their GoRex hosts listed. Its steps
// follow the node (tailnet.ts): what happens, the engine's download, the
// sign-in in a tab of its own, and the hosts found, with what to do when
// none are. Escape or Close hides it; a sign-in on its way goes on.
import { allowCommand, commandBox, Host, icon } from "./hosts";
import { formatList, onLanguageChange, PlainKey, t } from "./i18n";
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
const stepNames: [Step, PlainKey][] = [
  ["explain", "guideStepAbout"],
  ["load", "tsDownload"],
  ["signin", "guideStepSignIn"],
  ["hosts", "guideStepHosts"],
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
    const close = el("button", "pane-action settings-close");
    close.type = "button";
    close.append(icon("x"));
    close.onclick = () => this.close();
    const head = el("header", "ts-head");
    const title = el("h1", "settings-title");
    title.id = "ts-guide-title";
    this.dialog.setAttribute("aria-labelledby", title.id);
    head.append(title, close);
    const label = () => {
      title.textContent = t("hostTsAdd");
      close.title = t("commonClose");
      close.setAttribute("aria-label", close.title);
      this.steps.setAttribute("aria-label", t("guideSteps"));
    };
    label();
    // A change of language draws the step again in it.
    onLanguageChange(() => {
      label();
      this.shownKey = "";
      this.update();
    });
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
    const ts = this.tailnet;
    // The sign-in tab goes to the login URL as soon as it comes.
    if (ts.loginURL && this.popup && !this.popup.closed) {
      try {
        if (this.popup.location.href === "about:blank") this.popup.location.href = ts.loginURL;
      } catch {
        // Already at Tailscale's page: another origin.
      }
    }
    if (ts.status() === "running" && this.popup) this.popup = null;
    const step = this.step();
    const key = JSON.stringify([
      step, ts.status(), ts.load, ts.loginURL, ts.error, ts.signingIn, this.blocked, ts.searching, ts.account, ts.map?.tailnet,
      ts.hosts.map((h) => [h.id, h.state, h.problem?.title]), ts.unanswered, ts.name,
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
        li.append(n, el("span", "ts-step-label", t(label)));
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
    const ts = this.tailnet;
    this.hero("brand:tailscale", t("guideExplainTitle"), t("guideExplainText"));
    const list = el("div", "ts-points");
    this.point("shield-check", t("guideExplainDevice", { name: ts.name }), list);
    this.point("log-in", t("guideExplainSignIn"), list);
    this.point("cloud-download", t("guideExplainDownload"), list);
    this.point("lock", t("guideExplainTab"), list);
    this.body.append(list);
    const cancel = button(t("guideNotNow"));
    cancel.onclick = () => this.close();
    const go = button(t("guideContinue"), "host-add primary");
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
      this.hero("circle-alert", l.noEngine ? t("guideNoEngineTitle") : t("guideLoadFailedTitle"), l.message, "bad");
      if (l.noEngine) {
        this.point("info", t("guideNoEngineHint"));
      } else {
        this.point("info", t("guideLoadFailedHint"));
      }
      const cancel = button(t("commonClose"));
      cancel.onclick = () => this.close();
      this.foot.append(cancel);
      if (!l.noEngine) {
        const retry = button(t("commonTryAgain"), "host-add primary", "rotate-ccw");
        retry.dataset.key = "retry";
        retry.onclick = () => this.tailnet.turnOn();
        this.foot.append(retry);
      }
      return;
    }
    const starting = l.state === "loading" && l.phase === "start";
    this.hero("cloud-download", starting ? t("guideStarting") : t("guideDownloading"), t("guideOncePerVersion"));
    // The page's one progress bar (progress.ts), kept across redraws so a screen reader follows it.
    this.progress ??= new ProgressView(() => t("progressTailscale"), {
      download: () => t("guideProgressDownload"),
      start: () => t("guideProgressStart"),
      failed: () => t("tsStateLoadFailed"),
    });
    this.progress.update(l.state === "loading" && l.phase === "download" ? { phase: "download", received: l.received, total: l.total || undefined } : { phase: "start" });
    this.body.append(this.progress.el);
    const cancel = button(t("commonCancel"));
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
        w.document.documentElement.lang = document.documentElement.lang;
        w.document.body.style.cssText = "font: 15px system-ui, sans-serif; color: #666; display: grid; place-items: center; height: 100vh; margin: 0";
        w.document.body.textContent = t("guideOpening");
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
    const ts = this.tailnet;
    const s = ts.status();
    const signIn = (label = t("noticeSignInTailscale")) => {
      const b = button(label, "host-add primary", "log-in");
      b.dataset.key = "signin";
      b.onclick = () => this.startSignIn();
      return b;
    };
    const close = button(t("commonClose"));
    close.onclick = () => this.close();
    if (s === "starting") {
      this.hero("loader-circle", t("guideStarting"), undefined, "busy");
      this.foot.append(close);
      return;
    }
    if (s === "needs-approval") {
      this.hero("loader-circle", t("tsStateApproval"), t("guideApprovalText", { name: ts.name }), "busy");
      this.body.append(linkButton(t("guideOpenAdmin"), "https://login.tailscale.com/admin/machines"));
      this.foot.append(close);
      return;
    }
    if (s === "signing-in") {
      const blocked = this.blocked || (this.popup?.closed ?? false);
      if (!ts.loginURL) {
        this.hero("loader-circle", t("guideOpening"), undefined, "busy");
      } else if (blocked) {
        this.hero("circle-alert", t("guideOpenSignInTitle"), t("guideBlockedText"), "warn");
      } else {
        this.hero("loader-circle", t("guideWaitingTitle"), t("guideWaitingText"), "busy");
      }
      if (ts.loginURL) {
        const row = el("div", "ts-link-row");
        const open = linkButton(blocked ? t("guideOpenSignIn") : t("guideOpenAgain"), ts.loginURL, blocked ? "host-add primary" : "host-add");
        open.dataset.key = "open";
        row.append(open);
        this.body.append(row);
        const url = el("div", "ts-url-note", t("guideCopyLink"));
        this.body.append(url, commandBox(ts.loginURL));
      }
      const cancel = button(t("commonCancel"));
      cancel.dataset.key = "cancel";
      cancel.onclick = () => this.cancelSignIn();
      this.foot.append(cancel);
      return;
    }
    if (s === "error") {
      this.hero("wifi-off", t("guideCantReach"), ts.error, "bad");
      this.foot.append(close, signIn(t("commonTryAgain")));
      return;
    }
    if (s === "expired") {
      this.hero("log-in", t("guideExpiredTitle"), t("guideExpiredText", { name: ts.name }));
      this.foot.append(close, signIn(t("guideSignInAgain")));
      return;
    }
    this.hero("log-in", t("guideSignInTitle"), t("guideSignInText"));
    this.point("info", t("guideEphemeral", { name: ts.name }));
    this.foot.append(close, signIn());
  }

  private hosts() {
    const ts = this.tailnet;
    if (ts.status() === "locked") {
      this.hero(
        "lock", t("guideLockedTitle"), t("guideLockedText", { name: ts.name }), "bad",
      );
      const out = button(t("tsSignOut"), "host-add", "log-out");
      out.onclick = () => ts.signOut();
      const close = button(t("commonClose"), "host-add primary");
      close.onclick = () => this.close();
      this.foot.append(out, close);
      return;
    }
    const who = el("div", "ts-who");
    const g = el("span", "ts-hero-icon ok");
    g.append(icon("circle-check"));
    const words = el("div", "ts-hero-text");
    words.append(el("h2", "", t("guideSignedInAs", { account: ts.account || t("tsYou") })));
    const address = ts.map?.self.address;
    const tail = [
      ts.map?.tailnet && t("guideOnTailnet", { tailnet: ts.map.tailnet }),
      address ? t("guideThisDeviceAt", { name: ts.name, address }) : t("guideThisDevice", { name: ts.name }),
    ].filter(Boolean).join(t("listSeparator"));
    words.append(el("p", "", tail));
    who.append(g, words);
    this.body.append(who);

    const head = el("div", "ts-section");
    head.append(el("h3", "", t("tsHosts")));
    if (ts.searching) {
      const s = el("span", "ts-searching");
      s.append(icon("loader-circle"), el("span", "", t("hostSearching")));
      head.append(s);
    }
    this.body.append(head);
    if (ts.hosts.length) {
      const list = el("div", "ts-hosts");
      for (const h of ts.hosts) list.append(this.hostRow(h));
      this.body.append(list);
    } else if (!ts.searching) {
      this.empty();
    }
    const again = button(t("guideSearchAgain"), "host-add", "rotate-ccw");
    again.dataset.key = "again";
    again.onclick = () => ts.searchAgain();
    again.disabled = ts.searching;
    const done = button(t("commonDone"), "host-add primary");
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
    line.append(el("i", `state-dot ${dot}`), el("span", "", h.state === "online" ? h.dns : h.state === "refused" ? t("hostTailnetRefused") : t("hostTailnetOffline")));
    text.append(el("span", "settings-host-name", h.name), line);
    top.append(g, text);
    if (h.state === "online") {
      const c = button(t("guideConnect"), "host-add primary");
      c.dataset.key = `connect-${h.id}`;
      c.setAttribute("aria-label", t("settingsHostConnect", { name: h.name }));
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
    const ts = this.tailnet;
    const box = el("div", "ts-empty");
    box.append(el("h3", "", t("guideEmptyTitle")));
    box.append(el("p", "", t("guideEmptyText", { account: ts.account || t("tsYourAccount") })));
    const steps = el("ol", "ts-howto");
    const item = (text: string, command?: string) => {
      const li = el("li");
      li.append(el("p", "", text));
      if (command) li.append(commandBox(command));
      steps.append(li);
    };
    item(t("guideEmptyStep1"), "gorex rexapi tailscale on");
    item(t("guideEmptyStep2"), "gorex rexapi tailscale status");
    item(t("guideEmptyStep3"), allowCommand());
    box.append(steps);
    if (ts.unanswered.length) {
      box.append(el("p", "ts-unanswered", t("guideUnanswered", { names: formatList(ts.unanswered), n: ts.unanswered.length })));
    }
    box.append(el("p", "ts-note", t("guideNote")));
    this.body.append(box);
  }
}
