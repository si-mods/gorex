// Opening a host's file, as the desktop opens a path printed in a host's
// pane (upload.go download), over the gateway (docs/web-open-files.md):
// download_begin on the control connection gives a ticket for one file, and
// GET /api/download?ticket=… sends it. An image the page can show, and
// whose first bytes are that image's, opens in the viewer (viewer.ts) from
// a blob of that image type only; anything else is the browser's own
// download, a hidden link navigated to the file, so no blob of the host's
// data other than a checked image exists on the page. Over the tailnet the
// browser cannot navigate to the host, so a file downloads into a blob
// that is saved and let go (docs/web-tailscale.md, Downloads).
import { apiURL, Control, hostTransport, RexError } from "./rex";
import { Viewer } from "./viewer";

/** The images the viewer shows, by the MIME type their blob takes. */
export const imageTypes = {
  png: "image/png",
  jpeg: "image/jpeg",
  gif: "image/gif",
  webp: "image/webp",
  avif: "image/avif",
  bmp: "image/bmp",
  ico: "image/x-icon",
  svg: "image/svg+xml",
} as const;
export type ImageKind = keyof typeof imageTypes;

/** The types the gateway may name an image by (Go's mime.TypeByExtension reads the system's table). */
const typeKinds: Record<string, ImageKind> = {
  "image/png": "png",
  "image/jpeg": "jpeg",
  "image/jpg": "jpeg",
  "image/pjpeg": "jpeg",
  "image/gif": "gif",
  "image/webp": "webp",
  "image/avif": "avif",
  "image/bmp": "bmp",
  "image/x-bmp": "bmp",
  "image/x-ms-bmp": "bmp",
  "image/x-icon": "ico",
  "image/vnd.microsoft.icon": "ico",
  "image/svg+xml": "svg",
};
const extKinds: Record<string, ImageKind> = {
  png: "png", jpg: "jpeg", jpeg: "jpeg", gif: "gif", webp: "webp", avif: "avif", bmp: "bmp", ico: "ico", svg: "svg",
};

/** The largest image the page fetches to show; a larger one downloads. */
export const maxViewBytes = 50 << 20;
/** The largest file a tailnet host's download holds in memory, as a blob. */
export const maxBlobBytes = 256 << 20;
/** How many of the first bytes the signature check reads. */
const sniffBytes = 4096;

/**
 * The image a file is, as the gateway's type names it, or as its name's
 * extension does when the gateway knew no type; undefined for any other
 * file. Only a guess: sniff checks the bytes.
 */
export function imageKind(type: string, name: string): ImageKind | undefined {
  const t = type.split(";")[0].trim().toLowerCase();
  if (t && t !== "application/octet-stream") return typeKinds[t];
  const dot = name.lastIndexOf(".");
  return dot >= 0 ? extKinds[name.slice(dot + 1).toLowerCase()] : undefined;
}

const ascii = (b: Uint8Array, at: number, s: string) => s.length + at <= b.length && [...s].every((c, i) => b[at + i] === c.charCodeAt(0));

/**
 * Whether a file's first bytes are an image of kind: its signature, and for
 * SVG text whose first element is <svg, after a byte order mark, white
 * space and the XML prolog (the declaration, comments, processing
 * instructions and a doctype).
 */
export function sniff(kind: ImageKind, b: Uint8Array): boolean {
  switch (kind) {
    case "png":
      return [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a].every((x, i) => b[i] === x);
    case "jpeg":
      return b[0] === 0xff && b[1] === 0xd8 && b[2] === 0xff;
    case "gif":
      return ascii(b, 0, "GIF87a") || ascii(b, 0, "GIF89a");
    case "webp":
      return ascii(b, 0, "RIFF") && ascii(b, 8, "WEBP");
    case "avif": {
      // An ISO BMFF ftyp box whose major or a compatible brand is avif or avis.
      if (b.length < 16 || !ascii(b, 4, "ftyp")) return false;
      const size = Math.min(((b[0] << 24) | (b[1] << 16) | (b[2] << 8) | b[3]) >>> 0, b.length);
      for (let at = 8; at + 4 <= size; at += at === 8 ? 8 : 4) if (ascii(b, at, "avif") || ascii(b, at, "avis")) return true;
      return false;
    }
    case "bmp":
      return ascii(b, 0, "BM");
    case "ico":
      return b[0] === 0 && b[1] === 0 && b[2] === 1 && b[3] === 0;
    case "svg":
      return svgText(new TextDecoder().decode(b));
  }
}

/** Whether text, an SVG file's start, opens with its <svg element after the prolog. */
function svgText(s: string): boolean {
  let i = s.charCodeAt(0) === 0xfeff ? 1 : 0;
  for (;;) {
    while (i < s.length && " \t\r\n".includes(s[i])) i++;
    const skip = (open: string, close: string) => {
      if (!s.startsWith(open, i)) return false;
      const j = s.indexOf(close, i + open.length);
      i = j < 0 ? s.length : j + close.length;
      return true;
    };
    if (skip("<?", "?>") || skip("<!--", "-->")) continue;
    if (s.startsWith("<!DOCTYPE", i)) {
      // A doctype may hold an internal subset in brackets.
      const gt = s.indexOf(">", i), br = s.indexOf("[", i);
      if (br >= 0 && (gt < 0 || br < gt)) skip("<!DOCTYPE", "]>");
      else skip("<!DOCTYPE", ">");
      continue;
    }
    return /^<svg[\s/>]/.test(s.slice(i, i + 5));
  }
}

/** What download_begin answers. */
interface Begun {
  ticket: string;
  name: string;
  size: number;
  type: string;
}

/** The status line's words for download_begin's errors (docs/web-open-files.md). */
const errorText: Record<string, string> = {
  no_file: "No such file",
  is_folder: "A folder is not downloaded",
  not_regular: "Not a regular file",
  denied: "Permission denied",
  too_large: "Too large",
  not_supported: "The host does not send files",
};

/** An error's words for the status line. */
export function errorMessage(e: unknown): string {
  if (e instanceof RexError && errorText[e.code]) return errorText[e.code];
  if (e instanceof RexError) return e.message || e.code;
  return e instanceof Error ? e.message : String(e);
}

/** What opening a file needs of its pane. */
export interface FileHost {
  control(): Control | undefined;
  status(s: string): void;
  /** The viewer closed: the keyboard goes back to the pane. */
  back(): void;
}

/** The files being opened, by block and path: a second click on one does nothing. */
const opening = new Set<string>();
let viewer: Viewer | undefined;

async function begin(c: Control | undefined, session: string, block: string, path: string): Promise<Begun> {
  if (!c) throw new RexError("offline", "disconnected");
  const r = await c.callBlock("download_begin", session, block, { path });
  return { ticket: String(r.ticket ?? ""), name: String(r.name ?? ""), size: Number(r.size ?? 0), type: String(r.type ?? "") };
}

const downloadPath = (ticket: string) => `/api/download?ticket=${encodeURIComponent(ticket)}`;

/**
 * Hands a ticket's file to the browser's own download: a hidden frame
 * navigated to it. The answer is an attachment, which the browser saves;
 * an error (a lapsed ticket's 404) lands in the frame, never in the page.
 * A tailnet host's file goes through a blob instead (saveBlob).
 */
async function navigate(f: Begun, host: FileHost) {
  if (hostTransport().kind !== "direct") return saveBlob(f, host);
  const frame = document.createElement("iframe");
  frame.hidden = true;
  frame.src = apiURL(downloadPath(f.ticket));
  document.body.append(frame);
  // The download goes on without the frame once it has started.
  setTimeout(() => frame.remove(), 60_000);
}

/**
 * Downloads a ticket's file through the transport into a blob, at most
 * maxBlobBytes, and saves it with a link's download: the blob is never
 * shown or navigated to, and its URL goes once the save has started.
 */
async function saveBlob(f: Begun, host: FileHost) {
  const cap = `over Tailscale the page downloads files up to ${maxBlobBytes >> 20} MB`;
  if (f.size > maxBlobBytes) throw new Error(`Too large: ${cap}`);
  const name = f.name || "download";
  const r = await hostTransport().fetch(downloadPath(f.ticket), { cache: "no-store" });
  if (!r.ok || !r.body) throw new Error(`the host answered ${r.status}`);
  const reader = r.body.getReader();
  const chunks: Uint8Array[] = [];
  let n = 0, shown = -1;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      chunks.push(value);
      n += value.length;
      if (n > maxBlobBytes) throw new Error(`the file grew too large: ${cap}`);
      const pct = f.size ? Math.floor((100 * n) / f.size) : -1;
      if (pct !== shown && pct >= 0) {
        shown = pct;
        host.status(`Downloading ${name}… ${Math.min(pct, 100)}%`);
      }
    }
  } catch (e) {
    reader.cancel().catch(() => {});
    throw e;
  }
  const url = URL.createObjectURL(new Blob(chunks as BlobPart[], { type: "application/octet-stream" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.hidden = true;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 30_000);
  host.status(`Downloaded ${name}`);
}

/**
 * Fetches a ticket's image of kind, at most maxViewBytes, and returns it as
 * a blob of that image's type; undefined when its first bytes are not that
 * image's, the rest then left unread.
 */
async function fetchImage(ticket: string, kind: ImageKind): Promise<Blob | undefined> {
  const r = await hostTransport().fetch(downloadPath(ticket), { mode: "cors", credentials: "omit", cache: "no-store", referrerPolicy: "no-referrer" });
  if (!r.ok || !r.body) throw new Error(`the host answered ${r.status}`);
  const reader = r.body.getReader();
  const chunks: Uint8Array[] = [];
  let n = 0, checked = false;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (value) {
        chunks.push(value);
        n += value.length;
        if (n > maxViewBytes) throw new Error("the file grew past what the page shows");
      }
      if (!checked && (done || n >= sniffBytes)) {
        const head = new Uint8Array(Math.min(n, sniffBytes));
        let at = 0;
        for (const c of chunks) {
          if (at >= head.length) break;
          head.set(c.subarray(0, head.length - at), at);
          at += Math.min(c.length, head.length - at);
        }
        if (!sniff(kind, head)) {
          reader.cancel().catch(() => {});
          return undefined;
        }
        checked = true;
      }
      if (done) break;
    }
  } catch (e) {
    reader.cancel().catch(() => {});
    throw e;
  }
  return new Blob(chunks as BlobPart[], { type: imageTypes[kind] });
}

/** Downloads a file of a block with the browser, with a ticket of its own. */
export async function downloadFile(session: string, block: string, path: string, host: FileHost) {
  const key = `${session}\n${block}\n${path}`;
  if (opening.has(key)) return;
  opening.add(key);
  try {
    const f = await begin(host.control(), session, block, path);
    await navigate(f, host);
  } catch (e) {
    host.status(`Could not download ${path}: ${errorMessage(e)}`);
  } finally {
    opening.delete(key);
  }
}

/**
 * Opens a file a block's pane printed, a link's path: an image the page can
 * show in the viewer, any other file with the browser's download.
 */
export async function openFile(session: string, block: string, path: string, host: FileHost) {
  const key = `${session}\n${block}\n${path}`;
  if (opening.has(key)) return;
  opening.add(key);
  try {
    const f = await begin(host.control(), session, block, path);
    const kind = imageKind(f.type, f.name);
    if (kind && f.size <= maxViewBytes) {
      const blob = await fetchImage(f.ticket, kind);
      if (blob) {
        viewer ??= new Viewer();
        viewer.show(blob, f.name || path, {
          download: () => downloadFile(session, block, path, host),
          closed: () => host.back(),
        });
        return;
      }
      // Not the image it was named: the browser downloads it, with a new ticket.
      const again = await begin(host.control(), session, block, path);
      await navigate(again, host);
      return;
    }
    await navigate(f, host);
  } catch (e) {
    host.status(`Could not open ${path}: ${errorMessage(e)}`);
  } finally {
    opening.delete(key);
  }
}
