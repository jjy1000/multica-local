"use client";

// useDeepLinkRun — shared hook for the ICP-3 deep-link scroll/highlight
// contract (0.5.81). Lab views that expose a list of runs read
// `?run=<id>` from the route's search params; this hook returns a
// callback ref factory + an active-run-id flag so the list-row code
// can mark the matching row and scroll it into view on mount.
//
// Why a shared hook instead of copy-paste in each view:
//   - Identical scroll/highlight semantics across the 4 surface families
//     (pythia, mythos, code-canvas, plugin-shell) — only the row markup
//     differs.
//   - Single audit point when the ICP-3 contract changes (e.g. adding
//     a smooth-scroll behavior or an aria-live announcement).
//   - Tested once via the hook, re-used via prop forwarding.
//
// Behaviour:
//   - On mount or when ?run= changes, the matching row's ref triggers
//     `scrollIntoView({ block: "center", behavior: "smooth" })`.
//   - Highlight is applied via `data-deep-linked="true"` on the row
//     so each view's existing class-based styling can hook in (or the
//     caller can read the `isDeepLinked` flag).
//   - Resilient: run lists usually resolve AFTER first paint
//     (react-query / rawRequest), so the scroll polles via
//     requestAnimationFrame until the target row actually mounts
//     (~2s budget) instead of assuming it existed on the first render.
//   - Host-safe: reads ?run= through the NavigationAdapter searchParams
//     mirror (useOptionalNavigation) like IssueBreadcrumb does — under a
//     host with no NavigationProvider the hook degrades to plain rows
//     instead of throwing.
//
// Out of scope for this hook: keyboard focus management (a follow-up
// can extend it without breaking callers).

import { useCallback, useEffect, useRef } from "react";
import { useOptionalNavigation } from "../../navigation";

export interface UseDeepLinkRunResult<T extends HTMLElement> {
  /** Active run id (the value of ?run=), or null when unset / empty. */
  activeRunId: string | null;
  /**
   * Returns a ref callback for a list-row element. Attach to the row's
   * `ref` prop. When the row's data-run-id matches activeRunId, the
   * hook scrolls it into view on mount.
   */
  rowRef: (runId: string) => (el: T | null) => void;
  /** True for the row matching `?run=`. Use for highlight className. */
  isDeepLinked: (runId: string) => boolean;
}

export function useDeepLinkRun<T extends HTMLElement = HTMLDivElement>(): UseDeepLinkRunResult<T> {
  const searchParams = useOptionalNavigation()?.searchParams;
  const activeRunId = searchParams?.get("run") || null;
  // Map of runId → element ref. Held outside state because we never
  // need to re-render when a row mounts — the scroll trigger reads it
  // directly.
  const refs = useRef(new Map<string, T | null>());
  const scrolled = useRef<string | null>(null);

  const rowRef = useCallback(
    (runId: string) => (el: T | null) => {
      if (el) {
        refs.current.set(runId, el);
      } else {
        refs.current.delete(runId);
      }
    },
    [],
  );

  // Scroll when activeRunId matches a mounted row. Rows typically mount
  // async (fetch resolves post-paint), so poll via requestAnimationFrame
  // until the target appears (~2s wall-clock budget — frame-count caps
  // assume 60fps and halve on 120Hz displays), then stop quietly —
  // the highlight class still lands because isDeepLinked derives from
  // state, not DOM presence.
  useEffect(() => {
    if (!activeRunId) {
      scrolled.current = null;
      return;
    }
    const startMs = performance.now();
    let raf = 0;
    const tick = () => {
      const el = refs.current.get(activeRunId);
      if (el && scrolled.current !== activeRunId) {
        scrolled.current = activeRunId;
        el.scrollIntoView({ block: "center", behavior: "smooth" });
        return;
      }
      if (!el && performance.now() - startMs < 2000)
        raf = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(raf);
  }, [activeRunId, refs]);

  const isDeepLinked = useCallback(
    (runId: string) => activeRunId === runId,
    [activeRunId],
  );

  return { activeRunId, rowRef, isDeepLinked };
}