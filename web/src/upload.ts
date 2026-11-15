// Uploading dropped files, picked files, and pasted clipboard images to a
// remote GoRex host (/api/upload, internal/rexapi/upload.go): shows progress,
// supports cancellation, and pastes the shell-escaped remote path(s) into the
// target pane only once the upload succeeds.

import { Msg, t } from "./i18n";
import { formatBytes } from "./progress";
import { BytesStream, Control, describe, RexError } from "./rex";
import { extractDroppedFiles, extractPastedImages, parseUploadResponse, pastedImageName, UploadError, uploadPath, UploadResult } from "./uploadhelpers";

export { extractDroppedFiles, extractPastedImages, parseUploadResponse, pastedImageName, uploadPath };
export type { UploadResult };

export interface UploadTarget {
  control: Control;
  sessionID?: string;
  blockID: string;
  container: HTMLElement;
  paste(text: string): void;
  isAlive(): boolean;
  status(s: Msg): void;
}

async function compressDeflateRaw(file: File): Promise<Uint8Array | undefined> {
  if (typeof CompressionStream === "undefined" || file.size === 0 || file.size > 32 << 20) return undefined;
  try {
    const stream = file.stream().pipeThrough(new CompressionStream("deflate-raw"));
    const buf = new Uint8Array(await new Response(stream).arrayBuffer());
    return buf.length < file.size ? buf : undefined;
  } catch {
    return undefined;
  }
}

function sendXHR(
  url: string,
  file: File,
  signal: AbortSignal,
  onProgress: (loaded: number) => void,
): Promise<{ status: number; text: string }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    const onAbort = () => xhr.abort();
    signal.addEventListener("abort", onAbort, { once: true });
    xhr.open("POST", url);
    xhr.setRequestHeader("Content-Type", "application/octet-stream");
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress(e.loaded);
    };
    xhr.onload = () => {
      signal.removeEventListener("abort", onAbort);
      resolve({ status: xhr.status, text: xhr.responseText });
    };
    xhr.onerror = () => {
      signal.removeEventListener("abort", onAbort);
      reject(new Error(t("errorOffline")));
    };
    xhr.onabort = () => {
      signal.removeEventListener("abort", onAbort);
      reject(new DOMException("Aborted", "AbortError"));
    };
    xhr.send(file);
  });
}

async function sendOne(
  control: Control,
  blockID: string,
  file: File,
  signal: AbortSignal,
  onProgress: (loaded: number) => void,
): Promise<UploadResult> {
  if (control.transport.url) {
    const path = uploadPath(blockID, file.name);
    const r = await sendXHR(control.transport.url(path), file, signal, onProgress);
    return parseUploadResponse(r.status, r.text);
  }
  let body: BodyInit = file;
  let path = uploadPath(blockID, file.name);
  const headers: Record<string, string> = { "Content-Type": "application/octet-stream" };
  if (control.features.has("gorex.upload.deflate-raw")) {
    const gz = await compressDeflateRaw(file);
    if (gz) {
      body = gz as unknown as BodyInit;
      headers["Content-Encoding"] = "deflate-raw";
      path = uploadPath(blockID, file.name, file.size);
    }
  }
  const res = await control.transport.fetch(path, { method: "POST", headers, body, signal });
  onProgress(file.size);
  return parseUploadResponse(res.status, await res.text());
}

/**
 * Uploads files to target's captured host/control and blockID, showing a
 * cancellable progress bar in target.container, and pastes the escaped
 * remote path(s) into target only after all files succeed.
 */
async function sendOneBytes(
  control: Control,
  sessionID: string,
  blockID: string,
  file: File,
  batch: string | undefined,
  index: number,
  count: number,
  signal: AbortSignal,
  onProgress: (loaded: number) => void,
): Promise<void> {
  const args: Record<string, unknown> = {
    ref: `upload:${crypto.randomUUID().toLowerCase()}`,
    name: file.name || "file",
    size: file.size,
  };
  if (batch !== undefined) {
    args.batch = batch;
    args.index = index;
    args.count = count;
  }
  const res = await control.callBlock("upload_begin", sessionID, blockID, args);
  const ticket = String(res?.ticket ?? "");
  if (!ticket) throw new RexError("protocol", () => t("errorProtocol"));
  const stream = new BytesStream(control.transport);
  const closed = new Promise<void>((resolve, reject) => {
    stream.onClose = (code, reason) => {
      if (code === 1000) resolve();
      else reject(new RexError("closed", () => reason || t("errorDataClosed", { code: String(code) })));
    };
  });
  const onAbort = () => stream.close();
  signal.addEventListener("abort", onAbort, { once: true });
  try {
    await stream.open(ticket);
    const reader = file.stream().getReader();
    let sent = 0;
    const chunkSize = 64 << 10;
    while (true) {
      if (signal.aborted) throw new DOMException("Aborted", "AbortError");
      const { done, value } = await reader.read();
      if (done) break;
      for (let off = 0; off < value.byteLength; off += chunkSize) {
        if (signal.aborted) throw new DOMException("Aborted", "AbortError");
        stream.send(value.subarray(off, Math.min(value.byteLength, off + chunkSize)));
      }
      sent += value.byteLength;
      onProgress(sent);
    }
    stream.halfClose();
    await closed;
  } finally {
    signal.removeEventListener("abort", onAbort);
  }
}

export async function uploadFiles(target: UploadTarget, files: File[]): Promise<void> {
  if (!files.length) return;
  const { control, sessionID, blockID } = target;
  const useGorexHTTP = control.features.has("gorex.upload");
  const useBytesStream = !useGorexHTTP && control.apiRevision >= 11 && !!sessionID;
  if (!useGorexHTTP && !useBytesStream) {
    target.status(() => t("uploadUnsupported"));
    return;
  }

  const ac = new AbortController();
  const totalBytes = files.reduce((sum, f) => sum + f.size, 0);

  const box = document.createElement("div");
  box.className = "pane-upload";
  box.setAttribute("role", "status");
  const nameEl = document.createElement("span");
  nameEl.className = "pane-upload-name";
  const countEl = document.createElement("span");
  countEl.className = "pane-upload-count";
  const cancelBtn = document.createElement("button");
  cancelBtn.type = "button";
  cancelBtn.className = "pane-upload-cancel";
  cancelBtn.textContent = t("commonCancel");
  cancelBtn.onclick = () => ac.abort();
  const top = document.createElement("div");
  top.className = "pane-upload-top";
  top.append(nameEl, countEl, cancelBtn);
  const bar = document.createElement("div");
  bar.className = "pane-upload-bar";
  const fill = document.createElement("div");
  fill.className = "pane-upload-fill";
  bar.append(fill);
  box.append(top, bar);
  target.container.append(box);

  const update = (index: number, file: File, doneBefore: number, loadedInFile: number) => {
    nameEl.textContent = files.length > 1 ? `${file.name} (${index + 1}/${files.length})` : file.name;
    const sent = Math.min(totalBytes, doneBefore + loadedInFile);
    countEl.textContent = totalBytes > 0 ? `${formatBytes(sent)} / ${formatBytes(totalBytes)}` : "";
    const pct = totalBytes > 0 ? Math.round((sent / totalBytes) * 100) : 0;
    fill.style.width = `${pct}%`;
  };

  try {
    const words: string[] = [];
    let doneBytes = 0;
    const batch = useBytesStream && files.length > 1 ? crypto.randomUUID().toLowerCase() : undefined;
    for (let i = 0; i < files.length; i++) {
      if (ac.signal.aborted || !target.isAlive()) {
        ac.abort();
        throw new DOMException("Aborted", "AbortError");
      }
      const f = files[i];
      update(i, f, doneBytes, 0);
      if (useBytesStream) {
        await sendOneBytes(control, sessionID!, blockID, f, batch, i, files.length, ac.signal, (loaded) =>
          update(i, f, doneBytes, loaded),
        );
      } else {
        const res = await sendOne(control, blockID, f, ac.signal, (loaded) => update(i, f, doneBytes, loaded));
        words.push(res.word);
      }
      doneBytes += f.size;
      update(i, f, doneBytes, 0);
    }
    if (!useBytesStream && target.isAlive() && words.length > 0) {
      target.paste(words.join(" "));
    }
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      target.status(() => t("uploadCancelled"));
    } else {
      const err = e instanceof UploadError ? new RexError(e.code, e.message) : e;
      target.status(() => t("uploadFailed", { error: describe(err) }));
    }
  } finally {
    box.remove();
  }
}
