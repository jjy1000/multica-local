import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, render, waitFor } from "@testing-library/react";

vi.mock("../i18n", async () => {
  const editor = (await import("../locales/en/editor.json")).default;
  return {
    useT: () => ({
      t: (accessor: (dict: unknown) => string) => accessor(editor),
    }),
  };
});

const mermaidRenderMock = vi.hoisted(() => vi.fn());
const mermaidInitializeMock = vi.hoisted(() => vi.fn());
vi.mock("mermaid", () => ({
  default: { initialize: mermaidInitializeMock, render: mermaidRenderMock },
}));

const MOCK_SVG = '<svg viewBox="0 0 1000 500"><g><text>mock diagram</text></g></svg>';

import { MermaidDiagram } from "./mermaid-diagram";

const CHART = "graph LR\n  A[Start] --> B[Done]";

// jsdom reports 0x0 for every rect; the layout pass needs a real viewport.
beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    bottom: 400,
    height: 400,
    left: 0,
    right: 800,
    top: 0,
    width: 800,
    x: 0,
    y: 0,
    toJSON: () => ({}),
  });
  mermaidRenderMock.mockReset();
  mermaidRenderMock.mockResolvedValue({ svg: MOCK_SVG });
  mermaidInitializeMock.mockClear();
  Object.defineProperty(HTMLCanvasElement.prototype, "getContext", {
    configurable: true,
    value: () => ({
      fillStyle: "#000",
      fillRect: vi.fn(),
      getImageData: () => ({ data: new Uint8ClampedArray([12, 34, 56, 255]) }),
    }),
  });
});

afterEach(() => {
  vi.restoreAllMocks();
  document.documentElement.className = "";
  document.documentElement.removeAttribute("style");
  document.body.removeAttribute("style");
});

// The dialog scroll lock writes `overflow` into body style on every open and
// close. Taken for a theme switch, that re-rendered every diagram on the page
// as the viewer closed — a long task that stalled the dialog's exit and made
// the page flash (MUL-7760, upstream 32a396fd5). The observer must bump only
// when the resolved theme signature actually moved.
describe("MermaidDiagram theme observation", () => {
  it("does not re-render when a dialog's scroll lock rewrites the page style", async () => {
    render(<MermaidDiagram chart={CHART} />);
    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalledTimes(1);
    });

    await act(async () => {
      document.body.style.overflow = "hidden";
      await Promise.resolve();
    });
    await act(async () => {
      document.body.style.removeProperty("overflow");
      await Promise.resolve();
    });

    // A real theme switch still re-renders, and it is the only extra render:
    // any from the scroll lock would already have pushed the count past two.
    await act(async () => {
      document.documentElement.classList.add("dark");
      await Promise.resolve();
    });
    await waitFor(() => {
      expect(mermaidRenderMock).toHaveBeenCalledTimes(2);
    });
  });
});
