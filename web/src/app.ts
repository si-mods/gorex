// The page as an app (docs/web-client.md, App): the Service Worker that
// keeps it offline (sw.js) and the prompt when a new version waits, the
// browser's install prompt for Settings, Keyboard Lock in the app's own
// window, and downloads streamed through the worker (files.ts).
import { icon } from "./hosts";
import { localize, t } from "./i18n";

/** Whether this build registers sw.js: build.mjs's yes, the dev server's no (dev.mjs). */
declare const __GOREX_SW__: boolean;
const workerOn = typeof __GOREX_SW__ !== "undefined" && __GOREX_SW__;

/** The browser's install prompt (Chrome's and Edge's), not in TypeScript's DOM types. */
interface InstallPrompt extends Event {
  prompt(): Promise<void>;
  userChoice: Promise<{ outcome: "accepted" | "dismissed" }>;
}

let deferred: InstallPrompt | undefined;
const listeners = new Set<() => void>();
const changed = () => listeners.forEach((f) => f());

/** Calls f when the install state changes, for the Settings dialog's About section. */
export function onAppChange(f: () => void) {
  listeners.add(f);
}

/** Whether the page runs as the installed app, in a window of its own. */
export function standalone(): boolean {
  return matchMedia("(display-mode: standalone)").matches || (navigator as { standalone?: boolean }).standalone === true;
}

const ios = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

/**
 * How the page can be installed: "prompt" when the browser offered its
 * install prompt, "ios" in a browser of iOS or iPadOS (Add to Home
 * Screen, by hand), else "none": installed already, or a browser that
 * offers none.
 */
export function installKind(): "prompt" | "ios" | "none" {
  if (standalone()) return "none";
  if (deferred) return "prompt";
  return ios ? "ios" : "none";
}

/** Shows the browser's install prompt; its answer, either way, uses it up. */
export async function install() {
  const p = deferred;
  if (!p) return;
  deferred = undefined;
  await p.prompt();
  await p.userChoice.catch(() => undefined);
  changed();
}

// The browser offers to install the page: Settings shows Install App in
// place of its own banner.
window.addEventListener("beforeinstallprompt", (e) => {
  e.preventDefault();
  deferred = e as InstallPrompt;
  changed();
});
window.addEventListener("appinstalled", () => {
  deferred = undefined;
  changed();
});

/**
 * Every key the keyboard has but Escape: in the app's window, Keyboard Lock
 * sends the browser's own chords (Ctrl+W, Ctrl+T, Ctrl+N, Ctrl+Tab and the
 * rest) to the page, and so to the terminal, where the browser lets it.
 * Escape is left out: a browser holds the keys only in fullscreen, where a
 * locked Escape would take a long press to leave it.
 */
const lockKeys = [
  ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ".split("").map((c) => `Key${c}`),
  ..."0123456789".split("").map((d) => `Digit${d}`),
  ...Array.from({ length: 12 }, (_, i) => `F${i + 1}`),
  "Backquote", "Minus", "Equal", "BracketLeft", "BracketRight", "Backslash", "Semicolon", "Quote", "Comma", "Period",
  "Slash", "Tab", "Enter", "Space", "Backspace", "Delete", "Insert", "Home", "End", "PageUp", "PageDown",
  "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown",
];

/** Keyboard Lock where it is (Chrome, Edge), in the app's window only; a tab keeps the browser's keys. */
function lockKeyboard() {
  const kb = (navigator as { keyboard?: { lock(keys?: string[]): Promise<void>; unlock(): void } }).keyboard;
  if (!kb?.lock) return;
  const apply = () => {
    if (standalone()) kb.lock(lockKeys).catch(() => {});
    else kb.unlock();
  };
  apply();
  matchMedia("(display-mode: standalone)").addEventListener("change", apply);
}

let updateBox: HTMLElement | undefined;

/**
 * A new version is installed and waits: a panel in the corner offers to
 * reload into it. Nothing reloads until the user clicks Reload, as a
 * reload ends the terminals' connections and this tab's Tailscale sign-in.
 */
function offerUpdate(reg: ServiceWorkerRegistration) {
  if (updateBox) {
    updateBox.hidden = false;
    return;
  }
  const box = document.createElement("div");
  box.className = "update";
  box.setAttribute("role", "status");
  const text = document.createElement("span");
  text.className = "update-text";
  localize(text, (e) => (e.textContent = t("updateReady")));
  const reload = document.createElement("button");
  reload.type = "button";
  reload.className = "host-add primary";
  const label = document.createElement("span");
  reload.append(icon("rotate-ccw"), label);
  localize(reload, () => {
    label.textContent = t("updateReload");
    reload.title = t("updateReloadTip");
  });
  const later = document.createElement("button");
  later.type = "button";
  later.className = "icon";
  localize(later, (b) => {
    b.title = t("updateLater");
    b.setAttribute("aria-label", b.title);
  });
  later.append(icon("x"));
  later.onclick = () => (box.hidden = true);
  reload.onclick = () => {
    reload.disabled = true;
    reloading = true;
    const w = reg.waiting;
    // No worker waits (another tab took it): reload into the one that runs.
    if (!w) return location.reload();
    w.postMessage({ type: "skip-waiting" });
    // The new worker takes the page (controllerchange), which reloads it; this, if that never comes.
    setTimeout(() => location.reload(), 5000);
  };
  box.append(text, reload, later);
  document.body.append(box);
  updateBox = box;
}

/** Whether the user clicked Reload: only then does a change of worker reload the page, once. */
let reloading = false;

/**
 * Starts the page's app parts: Keyboard Lock in the app's window, and the
 * Service Worker (not on the dev server), which is checked for a new
 * version at each load, when the tab comes back, and hourly.
 */
export async function startApp() {
  lockKeyboard();
  if (!workerOn || !("serviceWorker" in navigator)) return;
  let reg: ServiceWorkerRegistration;
  try {
    reg = await navigator.serviceWorker.register("sw.js", { scope: "./", updateViaCache: "none" });
  } catch (e) {
    console.warn("GoRex: no Service Worker:", e);
    return;
  }
  // A worker waiting while one runs is a new version; the first one installed is not.
  const offer = () => {
    if (reg.waiting && reg.active) offerUpdate(reg);
  };
  offer();
  reg.addEventListener("updatefound", () => {
    const w = reg.installing;
    w?.addEventListener("statechange", () => {
      if (w.state === "installed") offer();
    });
  });
  navigator.serviceWorker.addEventListener("controllerchange", () => {
    if (!reloading) return;
    reloading = false;
    location.reload();
  });
  const check = () => reg.update().catch(() => {});
  setInterval(check, 60 * 60_000);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") check();
  });
}

/** A download streamed through the worker to the browser's own download (streamSave). */
export interface SaveStream {
  /**
   * Hands the reader's bytes to the browser's download as it asks for
   * them, counting each chunk; answers "done" when all went, "cancelled"
   * when the user cancelled it in the browser.
   */
  pipe(reader: ReadableStreamDefaultReader<Uint8Array>, count: (bytes: number) => void): Promise<"done" | "cancelled">;
  /** Gives up before pipe: the worker lets the download go. */
  abort(why: string): void;
}

/**
 * Sets up a streamed download of a file named name: the worker that
 * controls the page makes a stream of save/<id>, which a hidden frame then
 * opens as an attachment, while the page sends the chunks one at a time as
 * the browser asks for them. Undefined without a controlling worker (the
 * dev server, the first load, a browser without one): files.ts then holds
 * the file in a blob instead.
 */
export async function streamSave(name: string): Promise<SaveStream | undefined> {
  const sw = workerOn ? navigator.serviceWorker?.controller : null;
  if (!sw) return undefined;
  const id = crypto.randomUUID();
  const ch = new MessageChannel();
  const port = ch.port1;
  // The worker asks for the first chunk as soon as it made the stream, maybe before pipe: kept here.
  let asked = 0, cancelled = false;
  let onPull: (() => void) | undefined, onCancel: (() => void) | undefined;
  const ready = new Promise<boolean>((resolve) => {
    port.onmessage = ({ data }) => {
      if (data?.ready) resolve(true);
      else if (data?.pull) onPull ? onPull() : asked++;
      else if (data?.cancel) onCancel ? onCancel() : (cancelled = true);
    };
    setTimeout(() => resolve(false), 3000);
  });
  sw.postMessage({ type: "save", id, name }, [ch.port2]);
  if (!(await ready)) {
    port.close();
    return undefined;
  }
  return {
    abort(why) {
      port.postMessage({ error: why });
      port.close();
    },
    pipe(reader, count) {
      return new Promise((resolve, reject) => {
        // Messages keep the worker awake while the download lasts (sw.js).
        const ping = setInterval(() => sw.postMessage({ type: "ping" }), 10_000);
        const end = () => {
          clearInterval(ping);
          port.close();
        };
        let busy = false;
        onCancel = () => {
          end();
          reader.cancel().catch(() => {});
          resolve("cancelled");
        };
        onPull = async () => {
          if (busy) return void asked++;
          busy = true;
          try {
            const { done, value } = await reader.read();
            if (done) {
              port.postMessage({ end: true });
              end();
              return resolve("done");
            }
            count(value.length);
            const b = value.slice().buffer as ArrayBuffer;
            port.postMessage({ chunk: b }, [b]);
          } catch (e) {
            port.postMessage({ error: e instanceof Error ? e.message : String(e) });
            end();
            reader.cancel().catch(() => {});
            return reject(e);
          } finally {
            busy = false;
          }
          if (asked > 0) {
            asked--;
            onPull!();
          }
        };
        if (cancelled) return onCancel();
        const frame = document.createElement("iframe");
        frame.hidden = true;
        frame.src = `save/${id}`;
        document.body.append(frame);
        // The download goes on without the frame once it has started.
        setTimeout(() => frame.remove(), 60_000);
        if (asked > 0) {
          asked--;
          onPull();
        }
      });
    },
  };
}
