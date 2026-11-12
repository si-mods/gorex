// Loading a large file of the page's own with its progress: the terminal's
// wasm as the page starts, and built-in Tailscale's engine (tailnet.ts).
// The bytes come through a stream reader, counted as they arrive; the
// total is the size build.mjs knew, else Content-Length when the server
// sent the file unencoded (with Content-Encoding, as Cloudflare's brotli,
// Content-Length is the compressed size or none), else unknown and the bar
// is indeterminate. ProgressView shows it, the same in both places.

/** Bytes as a short size: 412 KB, 6.1 MB. */
export function formatBytes(n: number): string {
  return n < 1e6 ? `${Math.max(1, Math.round(n / 1e3))} KB` : `${(n / 1e6).toFixed(1)} MB`;
}

type Progress = (received: number, total?: number) => void;

/** A response's total: size when known, else an unencoded Content-Length, else unknown. */
function totalOf(res: Response, size: number): number | undefined {
  const length = Number(res.headers.get("content-length"));
  return size > 0 ? size : !res.headers.get("content-encoding") && length > 0 ? length : undefined;
}

/** Reads a response's body to its end, reporting the bytes; keeps them when keep. */
async function drain(res: Response, size: number, progress: Progress, keep: boolean): Promise<Uint8Array> {
  if (!res.body) throw new Error("the response has no body");
  const total = totalOf(res, size);
  const reader = res.body.getReader();
  const chunks: Uint8Array[] = [];
  let received = 0;
  progress(0, total);
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    if (keep) chunks.push(value);
    received += value.length;
    progress(received, total);
  }
  const out = new Uint8Array(keep ? received : 0);
  let at = 0;
  for (const c of chunks) {
    out.set(c, at);
    at += c.length;
  }
  return out;
}

/** A response's bytes, read with their progress. */
export const readBody = (res: Response, size: number, progress: Progress) => drain(res, size, progress, true);

/** Counts a response's bytes (a clone's, while another reader takes the original), keeping none. */
export const countBody = (res: Response, size: number, progress: Progress) => drain(res, size, progress, false).then(() => {});

/** Fetches url with the browser's usual caching, and reads it with its progress. */
export async function fetchBytes(url: string, size: number, progress: Progress): Promise<Uint8Array> {
  const res = await fetch(url);
  if (!res.ok || !res.body) throw new Error(`${url}: the server answered ${res.status}`);
  return readBody(res, size, progress);
}

/** A load's state, as ProgressView shows it. */
export type LoadState =
  | { phase: "download"; received: number; total?: number }
  | { phase: "start" }
  | { phase: "failed"; message: string };

function el<K extends keyof HTMLElementTagNameMap>(tag: K, cls?: string, text?: string): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

/**
 * A progress bar with its title and count, which shows a download, then
 * "Starting…" while the module compiles, or the failure with a Retry
 * button. It updates in place, so a screen reader follows one bar.
 */
export class ProgressView {
  readonly el = el("div", "load");
  private title = el("span", "load-title");
  private count = el("span", "load-count");
  private bar = el("div", "load-bar");
  private fill = el("div", "load-fill");
  private error = el("p", "load-error");
  private retryButton = el("button", "host-add primary load-retry", "Retry");

  /** what names the file, as "the terminal"; retry runs again after a failure. */
  constructor(private what: string, private titles: { download: string; start: string; failed: string }, retry?: () => void) {
    const head = el("div", "load-head");
    head.append(this.title, this.count);
    this.bar.setAttribute("role", "progressbar");
    this.bar.setAttribute("aria-label", `Loading ${what}`);
    this.bar.setAttribute("aria-valuemin", "0");
    this.bar.setAttribute("aria-valuemax", "100");
    this.bar.append(this.fill);
    this.error.setAttribute("role", "alert");
    this.retryButton.type = "button";
    this.retryButton.onclick = () => retry?.();
    this.el.append(head, this.bar, this.error, this.retryButton);
    this.update({ phase: "download", received: 0 });
  }

  update(s: LoadState) {
    this.el.dataset.phase = s.phase;
    this.error.hidden = this.retryButton.hidden = s.phase !== "failed";
    this.bar.hidden = s.phase === "failed";
    this.bar.classList.toggle("busy", s.phase === "start" || (s.phase === "download" && !s.total));
    if (s.phase === "download") {
      this.title.textContent = this.titles.download;
      if (s.total) {
        const pct = Math.min(100, Math.floor((100 * s.received) / s.total));
        this.fill.style.width = `${pct}%`;
        this.bar.setAttribute("aria-valuenow", String(pct));
        this.count.textContent = `${formatBytes(Math.min(s.received, s.total))} of ${formatBytes(s.total)}`;
      } else {
        this.fill.style.width = "";
        this.bar.removeAttribute("aria-valuenow");
        this.count.textContent = s.received ? formatBytes(s.received) : "";
      }
      this.bar.setAttribute("aria-valuetext", this.count.textContent || "Connecting");
    } else if (s.phase === "start") {
      this.title.textContent = this.titles.start;
      this.count.textContent = "";
      this.fill.style.width = "";
      this.bar.removeAttribute("aria-valuenow");
      this.bar.setAttribute("aria-valuetext", "Starting");
    } else {
      this.title.textContent = this.titles.failed;
      this.count.textContent = "";
      this.error.textContent = s.message;
    }
  }

  focusRetry() {
    this.retryButton.focus();
  }
}
