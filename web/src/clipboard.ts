/**
 * Writes text to the clipboard: starts the Clipboard API where the page has it
 * and, synchronously in the same user gesture, copies via a hidden textarea with
 * execCommand so a rejected Clipboard API promise (e.g. Safari on pointerup)
 * does not lose the gesture before the fallback runs. The keyboard goes back
 * where it was.
 */
export async function writeClipboard(text: string): Promise<boolean> {
  const api = window.isSecureContext && navigator.clipboard?.writeText
    ? navigator.clipboard.writeText(text).then(() => true, () => false)
    : undefined;
  const fallback = copyExec(text);
  if (api && await api) return true;
  return fallback;
}

function copyExec(text: string): boolean {
  const was = typeof HTMLElement !== "undefined" && document.activeElement instanceof HTMLElement
    ? document.activeElement
    : undefined;
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.readOnly = true;
  ta.setAttribute("aria-hidden", "true");
  ta.style.cssText = "position:fixed;left:0;top:0;width:1px;height:1px;padding:0;border:0;opacity:0;pointer-events:none";
  document.body.append(ta);
  ta.focus({ preventScroll: true });
  ta.select();
  ta.setSelectionRange(0, text.length);
  let ok = false;
  try {
    ok = document.execCommand("copy");
  } catch {
    ok = false;
  }
  ta.remove();
  was?.focus({ preventScroll: true });
  return ok;
}
