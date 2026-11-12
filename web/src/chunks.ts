// The parts of the page's code loaded when first used (docs/web-client.md,
// Loading on demand): esbuild cuts each dynamic import() into a chunk, and
// a Chunk loads it through the loader (loader.ts) the first time the page
// needs it, as the Settings dialog when it first opens. Once in, it runs
// at once. A load that takes more than 150 ms (a slow network) shows a
// small panel in the corner with a busy bar, and its failure the reason
// and Retry, which runs what waited on it.
import { localize, msg, Msg, t } from "./i18n";
import { icon } from "./hosts";
import { loader } from "./loader";
import { ProgressView } from "./progress";

/** How long a chunk loads before its panel shows: a load from the cache shows none. */
const showAfter = 150;

/** The panels' column in the corner, made with the first. */
let column: HTMLElement | undefined;

export class Chunk<T> {
  private module?: T;

  /**
   * name is the chunk's manifest entry without js/ (build.mjs); what names
   * it in the panel, mid-sentence ("Loading {name}…").
   */
  constructor(private name: string, private what: Msg, private importer: () => Promise<T>) {}

  /** The module, once it is in. */
  get loaded(): T | undefined {
    return this.module;
  }

  /** The module, loaded once; no panel. */
  load(): Promise<T> {
    return loader.module(`js/${this.name}`, this.importer).then((m) => (this.module = m));
  }

  /**
   * Runs f with the module: at once when it is in, else once it has
   * loaded, with the panel after showAfter, or the failure and Retry.
   */
  use(f: (m: T) => void) {
    if (this.module) return f(this.module);
    let panel: Panel | undefined;
    const timer = setTimeout(() => (panel = new Panel(this.what, () => this.use(f))), showAfter);
    this.load().then(
      (m) => {
        clearTimeout(timer);
        panel?.remove();
        f(m);
      },
      (e) => {
        clearTimeout(timer);
        (panel ??= new Panel(this.what, () => this.use(f))).fail(e);
      },
    );
  }
}

/** One chunk's panel: its busy bar, then its failure with Retry and a close button. */
class Panel {
  private el = document.createElement("div");
  private view: ProgressView;

  constructor(what: Msg, retry: () => void) {
    this.el.className = "chunk-load";
    this.el.setAttribute("role", "status");
    this.view = new ProgressView(() => t("chunkLoading", { name: msg(what) }), {
      download: () => t("chunkLoading", { name: msg(what) }),
      start: () => t("chunkLoading", { name: msg(what) }),
      failed: () => t("chunkFailed", { name: msg(what) }),
    }, () => {
      this.remove();
      retry();
    });
    this.view.update({ phase: "download", received: 0 });
    const close = document.createElement("button");
    close.type = "button";
    close.className = "icon chunk-close";
    close.hidden = true;
    localize(close, (b) => {
      b.title = t("commonClose");
      b.setAttribute("aria-label", b.title);
    });
    close.append(icon("x"));
    close.onclick = () => this.remove();
    this.el.append(this.view.el, close);
    if (!column?.isConnected) {
      column = document.createElement("div");
      column.className = "chunk-loads";
      document.body.append(column);
    }
    column.append(this.el);
  }

  fail(e: unknown) {
    const error = e instanceof Error ? e.message : String(e);
    this.view.update({ phase: "failed", message: () => t("chunkFailedText", { error }) });
    this.el.querySelector<HTMLElement>(".chunk-close")!.hidden = false;
    this.view.focusRetry();
  }

  remove() {
    this.el.remove();
  }
}
