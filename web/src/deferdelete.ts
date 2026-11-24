// Delete Space's Undo (docs/WEB-007-toast.md): the delete acts on the screen at once and is sent when the toast ends.
import type { ToastHost } from "./toast";

export interface DeferredDelete {
  toasts: ToastHost;
  /** The spaces deleted on screen and not sent yet (SpaceStore.hidden). */
  hidden: Set<string>;
  /** Draws the sidebar and the bar again. */
  redraw: () => void;
  /** Sends the delete. It runs once, when the toast ends and Undo was not pressed. */
  commit: (name: string) => void;
  /** The words of the toasts. */
  say: { deleted: (name: string) => string; restored: (name: string) => string; undo: string };
}

/** Hides the space and shows "Deleted <name>" with Undo; nothing is sent until the toast ends. Undo shows the space again and says so for a moment. */
export function deleteDeferred(name: string, o: DeferredDelete) {
  if (!name || o.hidden.has(name)) return;
  o.hidden.add(name);
  o.redraw();
  o.toasts.show({
    text: o.say.deleted(name),
    action: o.say.undo,
    onAction: () => {
      o.hidden.delete(name);
      o.redraw();
      o.toasts.show({ text: o.say.restored(name) });
    },
    onEnd: () => o.commit(name),
  });
}
