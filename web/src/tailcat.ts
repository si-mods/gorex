// The web client as a tailcat device (docs/WEB-006-tailcat.md): the tailcat
// engine (web/tailcat, Go compiled to WebAssembly) loaded only when a
// tailcat host needs it, the device key the browser keeps, the pairing
// ask on the host's RemotePort, and the transport that reaches a paired
// host's gateway.
//
// In the default mode the key is kept in IndexedDB and a Web Lock +
// BroadcastChannel coordinates one holder across tabs; in "This tab
// only" mode the key lives in memory and a reload pairs again.
import { Host, Problem, problem } from "./hosts";
import { msg, Msg, t } from "./i18n";
import { prefs, setPrefs } from "./prefs";
import { loader } from "./loader";
import type { Transport } from "./rex";
import { channelNamed, TabCoordinator, TabHandlers } from "./tailstore";
import { clearKey, loadKey, saveKey } from "./catstore";

export interface BodyReader {
  read(): Promise<Uint8Array | null>;
  cancel(): void;
}

/** The engine's calls (web/tailcat/main.go, api). */
export interface EngineCat {
  key(): string;
  privkey(): string;
  check(address: string): { ok?: boolean; ownRelay?: boolean; error?: string };
  pair(address: string, name: string): Promise<string>;
  request(
    address: string, method: string, path: string, headers: string, body: Uint8Array | null, timeoutMs: number,
  ): Promise<{ status: number; statusText: string; headers: string; body: BodyReader }>;
  socket(
    address: string, path: string, protocol: string, emit: (kind: string, a?: any, b?: any) => void,
  ): { send(d: string | Uint8Array): void; close(code?: number, reason?: string): void };
  close(address: string): void;
}

/**
 * A host paired over tailcat (hosts.ts): address is the tailcat address,
 * "tc…", a secret; url only labels it ("tailcat").
 */
export interface TailcatHost extends Host {
  kind: "tailcat";
  address: string;
}

export type Load =
  | { state: "idle" }
  | { state: "loading"; phase: "download" | "start"; received: number; total: number }
  | { state: "failed"; message: string; noEngine?: boolean }
  | { state: "ready" };

export type Status = "idle" | "loading" | "load-failed" | "in-use-other-tab" | "ready";

/** A build without the engine (no Go): the host's problem says so. */
export class NoCatEngine extends Error {
  get message() {
    return t("tsNoEngine");
  }
}

/** A device name such as web-rex-kqzt, for remote.pair. */
function randomName(): string {
  const a = new Uint8Array(4);
  crypto.getRandomValues(a);
  return "web-" + [...a].map((x) => String.fromCharCode(97 + (x % 26))).join("");
}

/**
 * This page's tailcat device: one key, one engine, and each host's
 * pairing state. Everything that shows it listens with onChange.
 */
export class Tailcat {
  load: Load = { state: "idle" };
  /** Another tab holds the key. */
  inUseOtherTab = false;
  /** This device's name, as a pairing request asks with it. */
  name = prefs.tailcatName || randomName();
  /** This device's public key, once the engine runs. */
  publicKey = "";

  private node?: EngineCat;
  /** The addresses this tab's node has a client for, closed on takeover. */
  private readonly addresses = new Set<string>();
  private tabs = new TabCoordinator(undefined, channelNamed("gorex-tailcat"), "gorex-tailcat-key");
  private listeners = new Set<() => void>();
  private pending = false;
  /** Each host's pairing: asked once, then the fingerprint the host shows while the user decides, the refusal, or a relay a browser cannot use. */
  private readonly pairing = new Map<string, { asked?: boolean; ownRelay?: boolean; fingerprint?: string; error?: Msg; paired?: boolean }>();
  /** The engine's module, loaded once per page. */
  private engine?: Promise<void>;
  /** The code that runs only with the engine (catengine.ts). */
  private code?: typeof import("./catengine");

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

  get persist(): boolean {
    return prefs.tailcatPersist;
  }

  status(): Status {
    if (this.inUseOtherTab) return "in-use-other-tab";
    if (this.load.state === "failed") return "load-failed";
    if (this.load.state !== "ready") return this.load.state === "idle" ? "idle" : "loading";
    return "ready";
  }

  /** The load in flight: two callers share it, or the second one's acquire loses the ifAvailable lock to the first and wrongly marks this tab another tab's. */
  private ensuring?: Promise<EngineCat | undefined>;

  /** The engine, loaded and running with this tab's key; undefined while it cannot. */
  private ensure(): Promise<EngineCat | undefined> {
    this.ensuring ??= this.ensureInner().finally(() => (this.ensuring = undefined));
    return this.ensuring;
  }

  private async ensureInner(): Promise<EngineCat | undefined> {
    if (this.node) return this.node;
    if (!(await this.ensureEngine())) return undefined;
    if (!this.persist) {
      this.node = this.makeNode("");
      return this.node;
    }
    const got = await this.tabs.acquire(this.tabHandlers());
    if (!got) {
      this.inUseOtherTab = true;
      this.changed();
      return undefined;
    }
    this.inUseOtherTab = false;
    const key = (await loadKey()) ?? "";
    this.node = this.makeNode(key);
    if (!key) await saveKey(this.node.privkey());
    return this.node;
  }

  private makeNode(key: string): EngineCat {
    const n = (globalThis as any).gorexTailcat({ key }) as EngineCat;
    this.publicKey = n.key();
    if (!prefs.tailcatName) setPrefs({ tailcatName: this.name });
    return n;
  }

  private async ensureEngine(): Promise<boolean> {
    if (this.load.state === "ready") return true;
    if (this.load.state === "failed" && !this.load.noEngine) this.engine = undefined;
    this.load = { state: "loading", phase: "download", received: 0, total: 0 };
    this.changed();
    this.engine ??= loader.module("js/catengine", () => import("./catengine")).then((m) => (this.code = m).loadEngine(
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
      await this.engine;
      this.load = { state: "ready" };
      this.changed();
      return true;
    } catch (e) {
      const offline = !navigator.onLine;
      const why = offline ? () => t("tsDeviceOffline") : () => (e instanceof Error ? e.message : String(e));
      this.load = {
        state: "failed",
        noEngine: e instanceof NoCatEngine,
        get message() {
          return why();
        },
      };
      this.changed();
      return false;
    }
  }

  private tabHandlers(): TabHandlers {
    return {
      onTakeover: () => {
        // This key's clients keep their DERP streams until closed; the
        // other tab's node takes them over only once these are gone.
        const n = this.node;
        this.node = undefined;
        if (n) for (const a of this.addresses) n.close(a);
        this.inUseOtherTab = true;
        this.changed();
      },
      onAvailable: () => {
        if (this.inUseOtherTab && !this.node) {
          this.inUseOtherTab = false;
          this.changed();
        }
      },
      onSignedOut: () => {},
    };
  }

  /** Takes the key over from the tab that holds it. */
  async takeOver() {
    if (!this.persist) return;
    await this.tabs.takeOver(this.tabHandlers());
    this.inUseOtherTab = false;
    this.changed();
  }

  /** Releases the tab lock when the page unloads so another waiting tab can take over. */
  dispose() {
    this.tabs.release();
  }

  /** Switches between keeping the key and this-tab-only: a new key pairs again. */
  async setPersist(persist: boolean) {
    if (prefs.tailcatPersist === persist) return;
    setPrefs({ tailcatPersist: persist });
    this.node = undefined;
    this.publicKey = "";
    this.pairing.clear();
    if (persist) {
      // The key the next ensure() makes or finds is saved then.
    } else {
      await clearKey();
      this.tabs.release();
      this.inUseOtherTab = false;
    }
    this.changed();
  }

  /** Forgets the device key and starts over, for Reset. */
  async reset() {
    this.node = undefined;
    this.publicKey = "";
    this.pairing.clear();
    await clearKey();
    this.changed();
  }

  /** Sets the device name pairing asks with, kept in this browser. */
  rename(name: string) {
    this.name = name;
    setPrefs({ tailcatName: name });
    this.changed();
  }

  /**
   * Asks the host to pair (remote.pair), and keeps what the host answered
   * for the problem lines: the fingerprint to compare, or the refusal.
   */
  private async pair(h: TailcatHost): Promise<void> {
    const n = await this.ensure();
    // An engine that cannot run or a key held elsewhere is said by diagnose's own checks.
    if (!n) return;
    // An address homed on the machine's own relay cannot work here: a
    // browser cannot pin its certificate (docs/WEB-006-tailcat.md).
    this.addresses.add(h.address);
    if (n.check(h.address).ownRelay) {
      this.pairing.set(h.id, { asked: true, ownRelay: true });
      this.changed();
      return;
    }
    try {
      const fingerprint = await n.pair(h.address, this.name);
      this.pairing.set(h.id, { fingerprint });
    } catch (e) {
      const m = e instanceof Error ? e.message : String(e);
      if (/allowed there already/.test(m)) {
        // Approved in an earlier ask: the host is paired, and a connect
        // that still fails is unreachable, not refused.
        this.pairing.set(h.id, { asked: true, paired: true });
      } else {
        const err: Msg = /not pairing/.test(m)
          ? () => t("tcNotPairing", { host: h.name })
          : () => m;
        this.pairing.set(h.id, { error: err });
      }
    }
    this.changed();
  }

  /** Pairs a host again, as its problem's action asks: after a refusal, or a new pairing window. */
  async pairNow(h: TailcatHost) {
    this.pairing.delete(h.id);
    await this.pair(h);
  }

  /** The pairing's fingerprint, to compare with what the host shows. */
  fingerprint(h: TailcatHost): string | undefined {
    return this.pairing.get(h.id)?.fingerprint;
  }

  /**
   * Why a tailcat host did not connect, as hosts.ts's diagnose tells a
   * direct one's. An unpaired key pairs here: the problem carries the
   * fingerprint while the host waits for approval.
   */
  async diagnose(h: TailcatHost): Promise<Problem> {
    if (this.inUseOtherTab) {
      return problem(() => t("tcOtherTabTitle"), () => t("tcOtherTabText"));
    }
    if (this.load.state === "failed") {
      const m = this.load.message;
      return problem(() => t("problemFailedTitle"), () => m);
    }
    const st = this.pairing.get(h.id) ?? {};
    if (st.fingerprint) {
      const fp = st.fingerprint;
      return problem(() => t("tcApproveTitle", { host: h.name }), () => t("tcApproveText", { host: h.name, name: this.name, fingerprint: fp }));
    }
    if (st.ownRelay) {
      return problem(() => t("tcOwnRelayTitle"), () => t("tcOwnRelayText"));
    }
    if (st.paired) {
      return problem(() => t("problemUnreachableTitle"), () => t("tcUnreachableText"));
    }
    if (st.error) {
      const err = st.error;
      return problem(() => t("problemRefusedTitle"), () => msg(err));
    }
    // Pairing is asked once a host; a retry sees its state, a refusal's Pair asks again (pairNow).
    if (!st.asked) {
      st.asked = true;
      this.pairing.set(h.id, st);
      void this.pair(h);
    }
    return problem(() => t("tcPairingTitle", { host: h.name }), () => t("tcPairingText", { name: this.name }));
  }

  /** The transport to a paired host. */
  transport(h: TailcatHost): Transport {
    this.addresses.add(h.address);
    if (!this.code) void this.ensure();
    // A HostConn keeps the transport it is made with, so the engine's is
    // resolved a call at a time: made while the engine still loads, this
    // transport comes alive once it has, instead of failing every retry.
    return {
      kind: "tailcat",
      socket: (path, protocol) => {
        const tr = this.code?.tailcatTransport(() => this.node, h.address);
        if (!tr) throw new TypeError(t("tcIsOff"));
        return tr.socket(path, protocol);
      },
      fetch: (path, init) => {
        const tr = this.code?.tailcatTransport(() => this.node, h.address);
        if (!tr) return Promise.reject(new TypeError(t("tcIsOff")));
        return tr.fetch(path, init);
      },
    };
  }

  /** Drops a removed host's client. */
  removed(h: TailcatHost) {
    this.pairing.delete(h.id);
    this.addresses.delete(h.address);
    this.node?.close(h.address);
  }
}
