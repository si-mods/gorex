// Uploading dropped files, picked files, and pasted clipboard images to a
// remote Rex/GoRex host over com.superlogical.terminal.upload_begin +
// rex.data.bytes.v1: shows progress and supports cancellation.

import { Msg, t } from "./i18n";
import { formatBytes } from "./progress";
import { BytesStream, Control, describe, RexError } from "./rex";
import { RexProtocol } from "./rex-protocol.gen";
import { extractDroppedFiles, extractPastedImages, pastedImageName } from "./uploadhelpers";

export { extractDroppedFiles, extractPastedImages, pastedImageName };

export interface UploadTarget {
  control: Control;
  sessionID?: string;
  blockID: string;
  container: HTMLElement;
  paste(text: string): void;
  isAlive(): boolean;
  status(s: Msg): void;
}

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
    const chunkSize = RexProtocol.maxChunk;
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

/**
 * Uploads files to target's captured host/control and blockID, showing a
 * cancellable progress bar in target.container.
 */
export async function uploadFiles(target: UploadTarget, files: File[]): Promise<void> {
  if (!files.length) return;
  const { control, sessionID, blockID } = target;
  if (control.apiRevision < RexProtocol.uploadRevision || !sessionID) {
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
    let doneBytes = 0;
    const batch = files.length > 1 ? crypto.randomUUID().toLowerCase() : undefined;
    for (let i = 0; i < files.length; i++) {
      if (ac.signal.aborted || !target.isAlive()) {
        ac.abort();
        throw new DOMException("Aborted", "AbortError");
      }
      const f = files[i];
      update(i, f, doneBytes, 0);
      await sendOneBytes(control, sessionID, blockID, f, batch, i, files.length, ac.signal, (loaded) =>
        update(i, f, doneBytes, loaded),
      );
      doneBytes += f.size;
      update(i, f, doneBytes, 0);
    }
  } catch (e) {
    if (e instanceof DOMException && e.name === "AbortError") {
      target.status(() => t("uploadCancelled"));
    } else {
      target.status(() => t("uploadFailed", { error: describe(e) }));
    }
  } finally {
    box.remove();
  }
}
