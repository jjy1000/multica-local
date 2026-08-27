"use client";

// useDeepLinkRun regression suite (0.5.81 C2 closure tails).
//
// The hook reads ?run= through the NavigationAdapter searchParams mirror
// (not react-router-dom directly), so every case here mounts under a
// NavigationProvider stub — or none at all for the host-safe degradation
// contract. The scroll path polls via requestAnimationFrame so rows that
// mount from async-fetched lists are reached; the frame queue below is
// drained manually to keep scheduling deterministic.

import { fireEvent, render } from "@testing-library/react";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import { useDeepLinkRun } from "./use-deep-link-run";

// Frame-schedule emulation: real ids into a handle map so effect-cleanup
// cancels actually drop pending frames (a noop cancel would leak stale
// callbacks across cases), and a bounded drain lets late-mount tests keep
// polling budget instead of burning all ~120 tries in one synchronous pass.
let frameHandles = new Map<number, FrameRequestCallback>();
let frameSeq = 0;

function drainFrames(maxPasses = 30) {
  act(() => {
    let passes = 0;
    while (frameHandles.size > 0 && passes < maxPasses) {
      const pending = [...frameHandles.values()];
      frameHandles.clear();
      pending.forEach((cb) => cb(passes));
      passes++;
    }
  });
}

function navStub(search?: string): NavigationAdapter {
  return {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/experimental/pythia",
    searchParams: new URLSearchParams(search ?? ""),
    getShareableUrl: (p: string) => p,
  };
}

function Host({
  showRow,
  runId = "r1",
}: {
  showRow: boolean;
  runId?: string;
}) {
  const { rowRef, isDeepLinked } = useDeepLinkRun<HTMLButtonElement>();
  return (
    <>
      {showRow && (
        <button
          ref={rowRef(runId)}
          onClick={() => {}}
          data-testid="run-row"
          data-deep-linked={isDeepLinked(runId)}
        />
      )}
      {!showRow && <span data-testid="flag-holder">{isDeepLinked(runId)}</span>}
    </>
  );
}

function hostUi(showRow: boolean, search: string | undefined) {
  return (
    <NavigationProvider value={navStub(search)}>
      <Host showRow={showRow} />
    </NavigationProvider>
  );
}

describe("useDeepLinkRun", () => {
  beforeEach(() => {
    frameHandles = new Map();
    frameSeq = 0;
    vi.stubGlobal(
      "requestAnimationFrame",
      (cb: FrameRequestCallback) => {
        frameSeq += 1;
        frameHandles.set(frameSeq, cb);
        return frameSeq;
      },
    );
    vi.stubGlobal("cancelAnimationFrame", (id: number) => {
      frameHandles.delete(id);
    });
    Element.prototype.scrollIntoView = vi.fn();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders plain rows without ?run= and never scrolls", () => {
    const { getByTestId } = render(hostUi(true, undefined));
    drainFrames();
    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();
    // React stringifies data-* values: absent ?run= makes the flag "false".
    expect(getByTestId("run-row").dataset.deepLinked).toBe("false");
  });

  it("scrolls an already-mounted matching row exactly once and flags it", () => {
    const { getByTestId } = render(hostUi(true, "?issue=i1&run=r1"));
    drainFrames();
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledTimes(1);
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledWith({
      block: "center",
      behavior: "smooth",
    });
    expect(getByTestId("run-row").dataset.deepLinked).toBe("true");
    // Extra drained frames must not re-scroll (one-shot per run id), and a
    // click on the row leaves the deep-link flag alone.
    drainFrames();
    fireEvent.click(getByTestId("run-row"));
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledTimes(1);
    expect(getByTestId("run-row").dataset.deepLinked).toBe("true");
  });

  it("reaches rows mounted later from async-fetched lists (rAF polling)", () => {
    const view = render(hostUi(false, "?run=r1"));
    // Pre-mount window: active id resolves but no row exists yet → NO
    // scroll attempted. A bounded drain keeps most of the hook's ~2s
    // wall-clock polling budget alive for the post-list-load window below
    // (mirrors real-world pacing where frames arrive at display rate).
    drainFrames();
    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();

    // The list payload lands and renders the row — the next drained frame
    // performs the single center-view scroll.
    view.rerender(hostUi(true, "?run=r1"));
    drainFrames();
    expect(Element.prototype.scrollIntoView).toHaveBeenCalledTimes(1);
    expect(view.getByTestId("run-row").dataset.deepLinked).toBe("true");
  });

  it("degrades gracefully with no NavigationProvider (host-safety)", () => {
    expect(() => render(<Host showRow />)).not.toThrow();
    drainFrames();
    expect(Element.prototype.scrollIntoView).not.toHaveBeenCalled();
  });
});
