import { type RefObject, type CSSProperties, useEffect, useState, useCallback } from "react";

/**
 * Returns a dynamic maskImage style based on scroll position.
 * - At top/start → fade end only
 * - At bottom/end → fade start only
 * - In middle → fade both
 * - No overflow → undefined (no mask)
 *
 * The `axis` parameter selects between vertical (default, for chat /
 * sidebar / dropdown lists) and horizontal (for tab bars and other
 * row-scrolling surfaces). When the caller does not pass `axis`, the
 * hook falls back to vertical to preserve the pre-0.3.58 contract.
 */
export type ScrollFadeAxis = "vertical" | "horizontal";

export function useScrollFade(
  ref: RefObject<HTMLElement | null>,
  fadeSize = 32,
  axis: ScrollFadeAxis = "vertical"
): CSSProperties | undefined {
  const [fade, setFade] = useState<"none" | "top" | "bottom" | "start" | "end" | "both">("none");

  const update = useCallback(() => {
    const el = ref.current;
    if (!el) return;

    if (axis === "horizontal") {
      const { scrollLeft, scrollWidth, clientWidth } = el;
      const scrollable = scrollWidth - clientWidth;

      if (scrollable <= 0) {
        setFade("none");
        return;
      }

      const atStart = scrollLeft <= 1;
      const atEnd = scrollLeft >= scrollable - 1;

      if (atStart && atEnd) setFade("none");
      else if (atStart) setFade("end");
      else if (atEnd) setFade("start");
      else setFade("both");
      return;
    }

    const { scrollTop, scrollHeight, clientHeight } = el;
    const scrollable = scrollHeight - clientHeight;

    if (scrollable <= 0) {
      setFade("none");
      return;
    }

    const atTop = scrollTop <= 1;
    const atBottom = scrollTop >= scrollable - 1;

    if (atTop && atBottom) setFade("none");
    else if (atTop) setFade("bottom");
    else if (atBottom) setFade("top");
    else setFade("both");
  }, [ref, axis]);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    const frame = requestAnimationFrame(update);

    el.addEventListener("scroll", update, { passive: true });
    const ro = new ResizeObserver(update);
    ro.observe(el);
    // ResizeObserver only fires on the container's own box. When children
    // grow inside a flex/auto-height parent (e.g. async-loaded list items,
    // collapsibles), scrollHeight changes but clientHeight does not — the
    // mask would stay "none" until the user scrolls. MutationObserver on
    // childList catches those content insertions.
    const mo = new MutationObserver(update);
    mo.observe(el, { childList: true, subtree: true });

    return () => {
      cancelAnimationFrame(frame);
      el.removeEventListener("scroll", update);
      ro.disconnect();
      mo.disconnect();
    };
  }, [ref, update]);

  if (fade === "none") return undefined;

  if (axis === "horizontal") {
    // start = left edge, end = right edge
    const start =
      fade === "start" || fade === "both" ? `transparent 0%, black ${fadeSize}px` : "black 0%";
    const end =
      fade === "end" || fade === "both"
        ? `black calc(100% - ${fadeSize}px), transparent 100%`
        : "black 100%";

    const gradient = `linear-gradient(to right, ${start}, ${end})`;

    return {
      maskImage: gradient,
      WebkitMaskImage: gradient,
    };
  }

  // axis === "vertical" (default)
  const top = fade === "top" || fade === "both" ? `transparent 0%, black ${fadeSize}px` : "black 0%";
  const bottom =
    fade === "bottom" || fade === "both"
      ? `black calc(100% - ${fadeSize}px), transparent 100%`
      : "black 100%";

  const gradient = `linear-gradient(to bottom, ${top}, ${bottom})`;

  return {
    maskImage: gradient,
    WebkitMaskImage: gradient,
  };
}
