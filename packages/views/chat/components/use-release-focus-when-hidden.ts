import { useLayoutEffect, type RefObject } from "react";

/**
 * The floating chat window stays mounted while closed (opacity-only hide) so
 * drafts and the closing animation survive. If the user's focus was inside —
 * say, mid-sentence in the composer — closing the window would otherwise leave
 * keystrokes entering a hidden editor and page shortcuts dead. Blur on close,
 * but only when the user hasn't already moved focus to a dialog or page
 * control outside the window.
 *
 * Upstream pairs this with `inert` on the container; the explicit blur here is
 * the jsdom-testable half and covers engines that don't reset focus on inert
 * subtrees.
 */
export function useReleaseFocusWhenHidden(
  isOpen: boolean,
  windowRef: RefObject<HTMLElement | null>,
): void {
  useLayoutEffect(() => {
    if (isOpen) return;
    const container = windowRef.current;
    const active = container?.ownerDocument.activeElement;
    if (active instanceof HTMLElement && container?.contains(active)) active.blur();
  }, [isOpen, windowRef]);
}
