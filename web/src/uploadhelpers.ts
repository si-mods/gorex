// Pure file-upload helpers (no DOM or window globals) so web/test/upload.test.ts
// can run them directly in Node.

export interface UploadResult {
  path: string;
  word: string;
  reason?: string;
}

export class UploadError extends Error {
  constructor(public code: string, message: string) {
    super(message);
  }
}

/** Builds the /api/upload path for a block, file name, and optional uncompressed size (when deflate-raw). */
export function uploadPath(blockID: string, name: string, rawSize?: number): string {
  const q = new URLSearchParams({ block_id: blockID, name });
  if (rawSize !== undefined) q.set("size", String(rawSize));
  return `/api/upload?${q.toString()}`;
}

/** Generates a timestamped file name for a pasted clipboard image, matching desktop paste.go. */
export function pastedImageName(mime: string, now = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp =
    `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-` +
    `${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  const raw = mime.split(";")[0].trim().toLowerCase().replace(/^image\//, "");
  const ext = raw === "jpeg" || raw === "pjpeg" ? "jpg" : /^[a-z0-9]+$/.test(raw) ? raw : "png";
  return `pasted-image-${stamp}.${ext}`;
}

/** Extracts regular files from a drop event, flagging directories so they are not uploaded as 0-byte files. */
export function extractDroppedFiles(dt: DataTransfer | null): { files: File[]; skippedDir: boolean } {
  if (!dt) return { files: [], skippedDir: false };
  const files: File[] = [];
  let skippedDir = false;
  if (dt.items && dt.items.length > 0) {
    for (const item of [...dt.items]) {
      if (item.kind !== "file") continue;
      const entry = (item as DataTransferItem & { webkitGetAsEntry?: () => { isDirectory?: boolean } | null }).webkitGetAsEntry?.();
      if (entry?.isDirectory) {
        skippedDir = true;
        continue;
      }
      const f = item.getAsFile();
      if (f) files.push(f);
    }
    return { files, skippedDir };
  }
  return { files: [...dt.files], skippedDir: false };
}

/**
 * Extracts pasted clipboard images when there is no non-whitespace plain text,
 * matching desktop pasteUpload (paste.go).
 */
export function extractPastedImages(dt: DataTransfer | null, now = new Date()): File[] {
  if (!dt) return [];
  const text = dt.getData("text/plain") ?? "";
  if (text.trim().length > 0) return [];
  const out: File[] = [];
  const seen = new Set<File>();
  const add = (f: File | null) => {
    if (!f || seen.has(f) || !f.type.startsWith("image/")) return;
    seen.add(f);
    const generic = !f.name || /^image\.\w+$/i.test(f.name);
    out.push(generic ? new File([f], pastedImageName(f.type, now), { type: f.type }) : f);
  };
  for (const f of [...dt.files]) add(f);
  for (const item of [...(dt.items ?? [])]) {
    if (item.kind === "file") add(item.getAsFile());
  }
  return out;
}

/** Parses the JSON answer from POST /api/upload. */
export function parseUploadResponse(status: number, bodyText: string): UploadResult {
  let j: any = {};
  try {
    j = JSON.parse(bodyText || "{}");
  } catch {
    if (status < 200 || status >= 300) throw new Error(bodyText || `HTTP ${status}`);
    throw new Error("Invalid upload response");
  }
  if (status < 200 || status >= 300) {
    throw new UploadError(typeof j.code === "string" ? j.code : "upload_failed", j.error || `HTTP ${status}`);
  }
  if (!j.word) {
    throw new Error(j.reason || "Shell cannot accept this uploaded path");
  }
  return { path: String(j.path ?? ""), word: String(j.word), reason: j.reason };
}
