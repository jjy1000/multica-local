import { afterEach, describe, expect, it } from "vitest";
import { cleanup, renderHook } from "@testing-library/react";
import { useReleaseFocusWhenHidden } from "./use-release-focus-when-hidden";

describe("useReleaseFocusWhenHidden", () => {
  afterEach(cleanup);

  it("releases focus trapped inside the window when it closes", () => {
    const container = document.createElement("div");
    const input = document.createElement("input");
    container.appendChild(input);
    document.body.appendChild(container);

    const { rerender } = renderHook(
      ({ open }: { open: boolean }) => useReleaseFocusWhenHidden(open, { current: container }),
      { initialProps: { open: true } },
    );

    input.focus();
    expect(document.activeElement).toBe(input);

    rerender({ open: false });
    expect(document.activeElement).not.toBe(input);

    container.remove();
  });

  it("leaves focus alone when the user already moved it outside the window", () => {
    const container = document.createElement("div");
    const outside = document.createElement("input");
    document.body.appendChild(outside);

    const { rerender } = renderHook(
      ({ open }: { open: boolean }) => useReleaseFocusWhenHidden(open, { current: container }),
      { initialProps: { open: true } },
    );

    outside.focus();
    rerender({ open: false });
    expect(document.activeElement).toBe(outside);

    outside.remove();
  });

  it("does not blur while the window is open", () => {
    const container = document.createElement("div");
    const input = document.createElement("input");
    container.appendChild(input);
    document.body.appendChild(container);

    renderHook(({ open }: { open: boolean }) => useReleaseFocusWhenHidden(open, { current: container }), {
      initialProps: { open: true },
    });

    input.focus();
    expect(document.activeElement).toBe(input);

    container.remove();
  });
});
