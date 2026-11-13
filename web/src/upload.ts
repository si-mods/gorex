// Uploading dropped files, picked files, and pasted clipboard images to a
// remote GoRex host (/api/upload, internal/rexapi/upload.go): shows progress,
// supports cancellation, and pastes the shell-escaped remote path(s) into the
// target pane only once the upload succeeds.

import { Msg, t } from "./i18n";
import { formatBytes } from "./progress";
import { Control, describe, RexError } from "./rex";
import { extractDroppedFiles, extractPastedImages, parseUploadResponse, pastedImageName, UploadError, uploadPath, UploadResult } from "./uploadhelpers";

export { extractDroppedFiles, extractPastedImages, parseUploadResponse, pastedImageName, uploadPath };
export type { UploadResult };

export interface UploadTarget {
  control: Control;
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
export async function uploadFiles(target: UploadTarget, files: File[]): Promise<void> {
  if (!files.length) return;
  const { control, blockID } = target;
  if (!control.features.has("gorex.upload")) {
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
    for (let i = 0; i < files.length; i++) {
      if (ac.signal.aborted || !target.isAlive()) {
        ac.abort();
        throw new DOMException("Aborted", "AbortError");
      }
      const f = files[i];
      update(i, f, doneBytes, 0);
      const res = await sendOne(control, blockID, f, ac.signal, (loaded) => update(i, f, doneBytes, loaded));
      doneBytes += f.size;
      update(i, f, doneBytes, 0);
      words.push(res.word);
    }
    if (target.isAlive() && words.length > 0) {
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
