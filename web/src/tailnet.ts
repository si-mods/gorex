// Built-in Tailscale in the page (docs/web-tailscale.md): the tailnet
// engine (web/tailnet, Go compiled to WebAssembly) loaded only when the
// user turns Tailscale on, its node's state and sign-in, the GoRex hosts
// found in its network map, and the transport that reaches them.
//
// This version runs the node in the page's own origin, ephemeral and in
// memory ("This tab only"): reloading or closing the tab ends it, and
// Tailscale removes the device once it is offline. The node frame of a
// separate origin, which may keep a node longer, is phase 4.
import { Host, Problem, problem, untrusted } from "./hosts";
import { failure, Msg, msg, t } from "./i18n";
import { prefs, setPrefs } from "./prefs";
import { loader } from "./loader";
import type { Transport } from "./rex";

export interface BodyReader {
  read(): Promise<Uint8Array | null>;
  cancel(): void;
}

/** The node's calls (web/tailnet/main.go, api). */
export interface EngineNode {
  start(): void;
  login(): void;
  logout(): Promise<void>;
  stop(): void;
  rename(name: string): Promise<void>;
  netmap(): string;
  request(
    host: string, method: string, path: string, headers: string, body: Uint8Array | null, origin: string, timeoutMs: number,
  ): Promise<{ status: number; statusText: string; headers: string; body: BodyReader }>;
  socket(
    host: string, path: string, protocol: string, origin: string, emit: (kind: string, a?: any, b?: any) => void,
  ): { send(d: string | Uint8Array): void; close(code?: number, reason?: string): void };
}

/** The network map as the engine reports it (netmapJSON). */
export interface NetMap {
  state: string;
  self: { name: string; address: string; userID: number; login: string; displayName: string; keyExpiry?: number; machineStatus: string };
  tailnet: string;
  suffix: string;
  lockedOut: boolean;
  peers: Peer[] | null;
}

export interface Peer {
  id: string;
  name: string;
  hostname: string;
  app: string;
  os: string;
  userID: number;
  tagged: boolean;
  online?: boolean;
}

/** A GoRex host found on the tailnet: not saved, by its stable node ID. */
export interface TailnetHost extends Host {
  kind: "tailnet";
  /** Its MagicDNS name. */
  dns: string;
  state: "online" | "offline" | "refused";
  /** Why it refuses this browser. */
  problem?: Problem;
}

export type Load =
  | { state: "idle" }
  | { state: "loading"; phase: "download" | "start"; received: number; total: number }
  | { state: "failed"; message: string; noEngine?: boolean }
  | { state: "ready" };

export type Status =
  | "off" | "loading" | "load-failed" | "starting" | "needs-login" | "signing-in" | "needs-approval"
  | "running" | "expired" | "locked" | "error";

/** How long the login URL may take before the page says control is not reached. */
const loginWait = 25_000;
/** A probe's wait, and how many run at once (docs/web-tailscale.md, Finding hosts). */
const probeWait = 3_000;
const probesAtOnce = 4;
/** How often the network map is read again while running, besides its events. */
const pollEvery = 15_000;
/** How long a peer that did not answer waits before it is asked again. */
const askAgainAfter = 60_000;

/** A device name such as gorex-web-kqzt. */
function randomName(): string {
  const a = new Uint8Array(4);
  crypto.getRandomValues(a);
  return "gorex-web-" + [...a].map((x) => String.fromCharCode(97 + (x % 26))).join("");
}

/** internal/tailnode's ValidHostname rule. */
export const validDeviceName = (s: string) => /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(s);

/** The engine's module, loaded once per page. */
let engine: Promise<void> | undefined;
/** The code that runs only with the engine (tailengine.ts), a chunk of its own, in once the engine loads. */
let code: typeof import("./tailengine") | undefined;

/** A build without the engine (no Go): the guide says so, and Retry does not try again. */
export class NoEngine extends Error {
  get message() {
    return t("tsNoEngine");
  }
}

/** What a gateway's refusal (writeRefusal's message) means, and what to do. */
export function refusal(message: string, login: string): Problem {
  if (/does not trust web pages from/.test(message)) {
    return untrusted(() => t("tsUntrustedText"));
  }
  if (/lets in only the Tailscale user who signed it in/.test(message)) {
    return problem(
      () => t("tsOwnerTitle"),
      () => t("tsOwnerText", { account: login || t("tsAnotherAccount") }),
      "gorex rexapi tailscale on --allow-tailnet-members",
    );
  }
  if (/built-in Tailscale is off/.test(message)) {
    return problem(() => t("tsHostOffTitle"), () => t("tsRunOnHost"), "gorex rexapi tailscale on");
  }
  // The gateway's reason is its own words, in English.
  return problem(() => t("problemRefusedTitle"), () => message || t("tsRefusedText"));
}

/** A probe's answer: a gateway that lets this browser in, one that refuses it, or none. */
type Probe = { kind: "ok" } | { kind: "refused"; message: string } | { kind: "none"; why: Msg };

/**
 * Built-in Tailscale of this page: one node, its state, and the GoRex
 * hosts it finds. Everything that shows it listens with onChange.
 */
export class Tailnet {
  /** Whether the user turned it on in this tab. */
  on = false;
  load: Load = { state: "idle" };
  /** The node's backend state: NoState, NeedsLogin, NeedsMachineAuth, Starting, Running, or "" with no node. */
  backend = "";
  /** The login URL, once control sent it. */
  loginURL = "";
  /** A sign-in asked for and not finished. */
  signingIn = false;
  /** Control's message, or why the sign-in did not start, in the page's language as it is read. */
  get error(): string {
    return msg(this.errorText);
  }
  set error(m: Msg) {
    this.errorText = m;
  }
  private errorText: Msg = "";
  map?: NetMap;
  hosts: TailnetHost[] = [];
  /** Devices that look like GoRex hosts but did not answer as one, by name. */
  unanswered: string[] = [];
  /** Probes waiting or on their way. */
  searching = false;
  /** This device's name: the one chosen in Settings, else a new one per tab. */
  name = validDeviceName(prefs.tailscaleName) ? prefs.tailscaleName : randomName();

  private node?: EngineNode;
  /** The node ran once since it was signed in: a later NeedsLogin is an expiry, not a first sign-in. */
  private wasRunning = false;
  private listeners = new Set<() => void>();
  private pending = false;
  private loginTimer?: ReturnType<typeof setTimeout>;
  private poll?: ReturnType<typeof setInterval>;
  private refreshTimer?: ReturnType<typeof setTimeout>;
  /** Each peer's last probe, with whether it was online then and when. */
  private probes = new Map<string, { online: boolean; at: number; result: Probe }>();
  private queue: Peer[] = [];
  private running = 0;

  /** Calls f after every change, at most once a task. */
  onChange(f: () => void): () => void {
    this.listeners.add(f);
    return () => this.listeners.delete(f);
  }

  private changed() {
    if (this.pending) return;
    this.pending = true;
    queueMicrotask(() => {
      this.pending = false;
      for (const f of this.listeners) f();
    });
  }

  status(): Status {
    if (!this.on) return "off";
    if (this.load.state === "failed") return "load-failed";
    if (this.load.state !== "ready") return "loading";
    switch (this.backend) {
      case "Running":
        return this.map?.lockedOut ? "locked" : "running";
      case "NeedsMachineAuth":
        return "needs-approval";
      case "NeedsLogin":
        if (this.signingIn) return "signing-in";
        if (this.error) return "error";
        return this.wasRunning ? "expired" : "needs-login";
      default:
        return this.error ? "error" : "starting";
    }
  }

  /** The signed-in account, as control names it. */
  get account(): string {
    return this.map?.self.login || this.map?.self.displayName || "";
  }

  statusText(): string {
    switch (this.status()) {
      case "off":
        return t("tsStateOff");
      case "loading":
        return t("tsStatusLoading");
      case "load-failed":
        return t("tsStatusLoadFailed");
      case "starting":
        return t("bootStarting");
      case "needs-login":
        return t("tsStateNeedsLogin");
      case "signing-in":
        return t("tsStatusSigningIn");
      case "needs-approval":
        return t("tsStateApproval");
      case "running": {
        const account = this.account || t("tsYou");
        return this.map?.tailnet ? t("tsStatusRunningOn", { account, tailnet: this.map.tailnet }) : t("tsStatusRunning", { account });
      }
      case "expired":
        return t("tsStatusExpired");
      case "locked":
        return t("tsStatusLocked");
      case "error":
        return t("tsStateError");
    }
  }

  /** Turns it on: loads the engine once, then starts the node. */
  async turnOn() {
    this.on = true;
    this.error = "";
    this.changed();
    if (!(await this.ensureEngine())) return;
    if (!this.node && this.on) this.startNode();
  }

  /** Loads the engine, again after a failure; whether it is ready. */
  private async ensureEngine(): Promise<boolean> {
    if (this.load.state === "ready") return true;
    if (this.load.state === "failed" && !this.load.noEngine) engine = undefined;
    this.load = { state: "loading", phase: "download", received: 0, total: 0 };
    this.changed();
    engine ??= loader.module("js/tailengine", () => import("./tailengine")).then((m) => (code = m).loadEngine(
      (received, total) => {
        this.load = { state: "loading", phase: "download", received, total };
        this.changed();
      },
      () => {
        this.load = { state: "loading", phase: "start", received: 0, total: 0 };
        this.changed();
      },
    ));
    try {
      await engine;
      this.load = { state: "ready" };
      this.changed();
      return true;
    } catch (e) {
      const offline = !navigator.onLine;
      const why = offline ? () => t("tsDeviceOffline") : () => (e instanceof Error ? e.message : String(e));
      this.load = {
        state: "failed",
        noEngine: e instanceof NoEngine,
        get message() {
          return why();
        },
      };
      this.changed();
      return false;
    }
  }

  private startNode() {
    try {
      this.node = (globalThis as any).gorexTailnet({ hostname: this.name }, (kind: string, data: string) =>
        queueMicrotask(() => this.event(kind, data)),
      ) as EngineNode;
      this.node.start();
    } catch (e) {
      this.node = undefined;
      const error = e instanceof Error ? e.message : String(e);
      const why = () => t("tsStopped", { error });
      this.load = {
        state: "failed",
        get message() {
          return why();
        },
      };
      engine = Promise.reject(failure(why));
      engine.catch(() => {});
      this.changed();
    }
  }

  private event(kind: string, data: string) {
    switch (kind) {
      case "state":
        this.backend = data;
        if (data === "Running") {
          this.wasRunning = true;
          this.signingIn = false;
          this.loginURL = "";
          this.error = "";
          clearTimeout(this.loginTimer);
          this.poll ??= setInterval(() => this.refresh(), pollEvery);
        } else {
          clearInterval(this.poll);
          this.poll = undefined;
        }
        if (data === "NeedsLogin" && this.hosts.length) this.forgetHosts();
        this.refresh();
        break;
      case "browse":
        this.loginURL = data;
        clearTimeout(this.loginTimer);
        this.error = "";
        break;
      case "error":
        this.error = data;
        break;
      case "login":
        this.signingIn = false;
        break;
      case "netmap":
        clearTimeout(this.refreshTimer);
        this.refreshTimer = setTimeout(() => this.refresh(), 300);
        break;
    }
    this.changed();
  }

  /** Asks control for a login URL; the "browse" event brings it. */
  login() {
    if (!this.node) return;
    this.error = "";
    this.loginURL = "";
    this.signingIn = true;
    clearTimeout(this.loginTimer);
    this.loginTimer = setTimeout(() => {
      if (!this.signingIn || this.loginURL) return;
      this.signingIn = false;
      this.error = navigator.onLine ? () => t("tsControlTimeout") : () => t("tsOfflineConnect");
      this.changed();
    }, loginWait);
    this.node.login();
    this.changed();
  }

  /** Stops waiting for a sign-in; the node stays, signed out. */
  cancelLogin() {
    clearTimeout(this.loginTimer);
    this.signingIn = false;
    this.loginURL = "";
    this.changed();
  }

  /** Signs this browser out: the node's key expires at control, which removes the ephemeral device. */
  async signOut() {
    this.cancelLogin();
    this.wasRunning = false;
    this.forgetHosts();
    this.changed();
    try {
      await this.node?.logout();
    } catch (e) {
      console.warn("tailnet: logout", e);
    }
  }

  /** Turns it off: signs out, then stops the node. The engine stays loaded. */
  async turnOff() {
    this.on = false;
    const n = this.node;
    if (n && this.backend === "Running") await this.signOut();
    this.cancelLogin();
    this.node = undefined;
    n?.stop();
    this.backend = "";
    this.error = "";
    this.wasRunning = false;
    clearInterval(this.poll);
    this.poll = undefined;
    this.forgetHosts();
    if (this.load.state === "failed") this.load = { state: "idle" };
    this.changed();
  }

  /** Sets the device name, kept in this browser; the node takes it at once. */
  async rename(name: string) {
    this.name = name;
    setPrefs({ tailscaleName: name });
    this.changed();
    await this.node?.rename(name);
  }

  private forgetHosts() {
    this.hosts = [];
    this.unanswered = [];
    this.probes.clear();
    this.queue = [];
    this.map = undefined;
    this.searching = false;
  }

  /** Reads the network map again, and looks for hosts in it. */
  private refresh() {
    if (!this.node) return;
    try {
      this.map = JSON.parse(this.node.netmap());
    } catch {
      return;
    }
    if (this.backend === "Running" && !this.map?.lockedOut) this.discover();
    this.changed();
  }

  /** Looks for hosts again from the start, as the user asks. */
  searchAgain() {
    this.probes.clear();
    this.refresh();
  }

  /**
   * The peers that may be GoRex gateways (docs/web-tailscale.md, Finding
   * hosts): the signed-in user's own untagged devices, with Hostinfo.App
   * "gorex" or a name ending in -gorex. Online ones are asked for
   * /api/help, four at a time; one that answered stays listed, offline,
   * when it goes offline.
   */
  private discover() {
    const m = this.map!;
    const peers = (m.peers ?? []).filter(
      (p) => p.userID === m.self.userID && !p.tagged && (p.app === "gorex" || /-gorex$/i.test(p.hostname) || /-gorex$/i.test(p.name.split(".")[0])),
    );
    const now = Date.now();
    for (const p of peers) {
      const online = p.online !== false;
      const had = this.probes.get(p.id);
      const stale =
        !had ||
        (online && !had.online) ||
        (had.result.kind === "refused" && now - had.at >= pollEvery - 1000) ||
        (had.result.kind === "none" && now - had.at >= askAgainAfter);
      if (online && stale && !this.queue.some((q) => q.id === p.id)) this.queue.push(p);
      if (had) had.online = online;
    }
    this.hostsFrom(peers);
    this.pump();
  }

  /** The host list from the probes' answers. */
  private hostsFrom(peers: Peer[]) {
    const hosts: TailnetHost[] = [];
    const unanswered: string[] = [];
    for (const p of peers) {
      const pr = this.probes.get(p.id);
      if (!pr) continue;
      const online = p.online !== false;
      const label = p.name.split(".")[0] || p.hostname;
      const base: TailnetHost = { id: `ts:${p.id}`, name: label, url: `https://${p.name}`, icon: "server", kind: "tailnet", dns: p.name, state: "online" };
      if (pr.result.kind === "ok") hosts.push({ ...base, state: online ? "online" : "offline" });
      else if (pr.result.kind === "refused") hosts.push({ ...base, state: online ? "refused" : "offline", problem: refusal(pr.result.message, this.account) });
      else if (online) unanswered.push(p.name);
    }
    hosts.sort((a, b) => a.name.localeCompare(b.name));
    this.hosts = hosts;
    this.unanswered = unanswered;
    this.searching = this.queue.length + this.running > 0;
  }

  private pump() {
    while (this.running < probesAtOnce && this.queue.length) {
      const p = this.queue.shift()!;
      this.running++;
      this.probe(p.name, probeWait).then((result) => {
        this.running--;
        this.probes.set(p.id, { online: true, at: Date.now(), result });
        if (this.map && this.node) this.hostsFrom(this.candidates());
        this.pump();
        this.changed();
      });
    }
    this.searching = this.queue.length + this.running > 0;
  }

  private candidates(): Peer[] {
    const m = this.map!;
    const ids = new Set([...this.probes.keys()]);
    return (m.peers ?? []).filter((p) => ids.has(p.id));
  }

  /** Asks a peer for the gateway's help document, as this page (its Origin). */
  private async probe(host: string, wait: number): Promise<Probe> {
    const n = this.node;
    if (!n) return { kind: "none", why: () => t("tsProbeStopped") };
    try {
      const r = await n.request(host, "GET", "/api/help", "{}", null, location.origin, wait);
      if (r.status === 200) {
        r.body.cancel();
        return { kind: "ok" };
      }
      if (r.status === 403) {
        let text = "";
        for (let c; (c = await r.body.read()) && text.length < 4096; ) text += new TextDecoder().decode(c);
        r.body.cancel();
        let message = "";
        try {
          message = JSON.parse(text).error ?? "";
        } catch {}
        return { kind: "refused", message };
      }
      r.body.cancel();
      const status = String(r.status);
      return { kind: "none", why: () => t("tsAnswered", { status }) };
    } catch (e) {
      return { kind: "none", why: e instanceof Error ? e.message : String(e) };
    }
  }

  /** The transport to a host found here. */
  transport(h: TailnetHost): Transport {
    // Only the engine's node finds a tailnet host, so its code is in.
    return code!.tailnetTransport(() => this.node, h.dns);
  }

  /** Why a host's control connection did not open, as hosts.ts's diagnose tells a direct one's. */
  async diagnose(h: TailnetHost): Promise<Problem> {
    if (this.status() !== "running") {
      return problem(() => t("tsNotConnectedTitle"), () => t("tsNotConnectedText", { status: this.statusText(), host: h.name }));
    }
    const p = await this.probe(h.dns, 8000);
    if (p.kind === "ok") return problem(() => t("problemFailedTitle"), () => t("tsFailedText"));
    if (p.kind === "refused") return refusal(p.message, this.account);
    const why = p.why;
    return problem(() => t("tsUnreachableTitle"), () => t("tsUnreachableText", { address: h.dns, why: msg(why) }));
  }
}
