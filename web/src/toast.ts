// The page's toast host (docs/WEB-007-toast.md): one small panel at the bottom centre that says what just
// happened and offers one action, such as Undo. One host per page, made at start and kept in the DOM, with
// one persistent live region whose text changes for each toast. One toast at a time: a new one replaces the
// old one, which ends first. It never takes focus.

/** Why a toast ended. The action button ends a toast with no reason: its handler has run instead. */
export type ToastEnd = "timeout" | "close" | "replaced" | "hide";

export interface Toast {
  text: string;
  /** The button's label; with none, the toast is a note: text only, and it has no close button. */
  action?: string;
  /** The button was pressed. The toast is gone already, so this may show another one. */
  onAction?: () => void;
  /** The toast ended some other way; called once. */
  onEnd?: (why: ToastEnd) => void;
  /** How long it stays, in ms; an action toast stays at least minAction. */
  ms?: number;
}

/** The least an action toast stays, in ms. */
export const minAction = 6000;
/** How long a note stays by default, in ms. */
export const noteMs = 1500;

export interface ToastLabels {
  /** The close button's name. */
  close: () => string;
  /** The close button's icon. */
  icon: () => Node;
  /** Gives focus back to the page when the toast goes while focus is in it. */
  restoreFocus?: () => void;
}

interface Current {
  toast: Toast;
  left: number;
  since: number;
  timer: ReturnType<typeof setTimeout> | undefined;
}

export class ToastHost {
  readonly root: HTMLElement;
  private readonly live: HTMLElement;
  private readonly panel: HTMLElement;
  private readonly text: HTMLElement;
  private ring: HTMLElement;
  private readonly act: HTMLButtonElement;
  private readonly close: HTMLButtonElement;
  private current: Current | undefined;
  private pointer = false;
  private focus = false;

  constructor(private readonly doc: Document, labels: ToastLabels) {
    const el = <K extends keyof HTMLElementTagNameMap>(tag: K, cls: string) => {
      const e = doc.createElement(tag);
      e.className = cls;
      return e;
    };
    this.root = el("div", "toast-host");
    // The one live region: it stays, and its text changes. It is visually hidden; the panel below is what people see.
    this.live = el("div", "toast-live");
    this.live.setAttribute("role", "status");
    this.live.setAttribute("aria-live", "polite");
    this.live.setAttribute("aria-atomic", "true");
    this.panel = el("div", "toast");
    this.panel.hidden = true;
    this.text = el("span", "toast-text");
    this.text.setAttribute("aria-hidden", "true");
    this.ring = el("span", "toast-ring");
    this.ring.setAttribute("aria-hidden", "true");
    this.act = el("button", "host-add toast-action");
    this.act.type = "button";
    this.close = el("button", "icon toast-close");
    this.close.type = "button";
    this.close.append(labels.icon());
    this.panel.append(this.text, this.ring, this.act, this.close);
    this.root.append(this.live, this.panel);

    this.act.addEventListener("click", () => {
      const c = this.current;
      if (!c) return;
      this.finish();
      c.toast.onAction?.();
    });
    this.close.addEventListener("click", () => this.end("close"));
    this.panel.addEventListener("keydown", (e) => {
      if ((e as KeyboardEvent).key !== "Escape" || !this.current) return;
      e.stopPropagation();
      this.end("close");
    });
    // Time stands still while the pointer is over the panel or focus is inside it.
    this.panel.addEventListener("pointerenter", () => this.hold({ pointer: true }));
    this.panel.addEventListener("pointerleave", () => this.hold({ pointer: false }));
    this.panel.addEventListener("focusin", () => this.hold({ focus: true }));
    this.panel.addEventListener("focusout", (e) => {
      const to = (e as FocusEvent).relatedTarget as Node | null;
      if (!to || !this.panel.contains(to)) this.hold({ focus: false });
    });
    this.close.setAttribute("aria-label", labels.close());
    this.close.title = labels.close();
    this.labels = labels;
    doc.body.append(this.root);
  }

  private readonly labels: ToastLabels;

  /** Shows a toast; the one on show, if any, ends first as "replaced". */
  show(toast: Toast): void {
    this.end("replaced");
    const ms = toast.action ? Math.max(toast.ms ?? minAction, minAction) : (toast.ms ?? noteMs);
    this.current = { toast, left: ms, since: 0, timer: undefined };
    this.text.textContent = toast.text;
    this.act.hidden = !toast.action;
    this.act.textContent = toast.action ?? "";
    this.close.hidden = !toast.action;
    this.close.setAttribute("aria-label", this.labels.close());
    this.close.title = this.labels.close();
    // A new ring each time, so that its animation starts again.
    const ring = this.doc.createElement("span");
    ring.className = "toast-ring";
    ring.setAttribute("aria-hidden", "true");
    ring.style.setProperty("--toast-ms", ms + "ms");
    ring.hidden = !toast.action;
    this.ring.replaceWith(ring);
    this.ring = ring;
    this.panel.hidden = false;
    // The panel was hidden: the pointer may have left it with no event, so ask the browser where it is.
    this.pointer = this.panel.matches?.(":hover") ?? this.pointer;
    this.live.textContent = toast.text;
    this.run();
  }

  /** Ends the toast on show, if any, with a reason; its onEnd runs once. */
  end(why: ToastEnd): void {
    const c = this.finish();
    c?.toast.onEnd?.(why);
  }

  /** Whether a toast is on show. */
  get showing(): boolean {
    return !!this.current;
  }

  /** Takes the toast down and returns it, with no callback. */
  private finish(): Current | undefined {
    const c = this.current;
    if (!c) return undefined;
    clearTimeout(c.timer);
    this.current = undefined;
    // Focus in a panel that goes would fall to the body: the page takes it back.
    const had = this.focus || this.panel.contains(this.doc.activeElement);
    this.panel.hidden = true;
    this.live.textContent = "";
    this.focus = false;
    if (had) this.labels.restoreFocus?.();
    return c;
  }

  private hold(h: { pointer?: boolean; focus?: boolean }) {
    if (h.pointer !== undefined) this.pointer = h.pointer;
    if (h.focus !== undefined) this.focus = h.focus;
    const c = this.current;
    if (!c) return;
    const held = this.pointer || this.focus;
    if (held && c.timer !== undefined) {
      clearTimeout(c.timer);
      c.timer = undefined;
      c.left = Math.max(0, c.left - (Date.now() - c.since));
      this.ring.setAttribute("data-held", "");
    } else if (!held && c.timer === undefined) {
      this.run();
    }
  }

  /** Starts the countdown for what is left. */
  private run() {
    const c = this.current;
    if (!c) return;
    // A pointer already on the panel holds a new toast too; pointerleave starts it.
    if (this.pointer || this.focus) {
      this.ring.setAttribute("data-held", "");
      return;
    }
    this.ring.removeAttribute("data-held");
    c.since = Date.now();
    c.timer = setTimeout(() => this.end("timeout"), c.left);
  }
}

/**
 * Makes the page's host, once, and ends the toast when the page hides or unloads, so that what waits for the
 * end (a deferred delete) is sent while the page can still send. Best effort: a page may go with no event.
 */
let host: ToastHost | undefined;

export function startToasts(labels: ToastLabels): ToastHost {
  if (host) return host;
  const h = new ToastHost(document, labels);
  const flush = () => h.end("hide");
  addEventListener("pagehide", flush);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "hidden") flush();
  });
  host = h;
  return h;
}

/** The page's host; throws before startToasts. */
export function toasts(): ToastHost {
  if (!host) throw new Error("toasts not started");
  return host;
}
