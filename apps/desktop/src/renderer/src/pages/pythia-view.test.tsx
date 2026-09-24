/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, beforeEach, vi } from "vitest";
import { render } from "@testing-library/react";

const rawRequestSpy = vi.hoisted(() =>
  vi.fn(async (_url: string) => new Response("[]", { status: 200 })),
);
const useQuerySpy = vi.hoisted(() => vi.fn());
const wsIdRef = vi.hoisted(() => ({ value: "11111111-2222-3333-4444-555555555555" }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: (opts: unknown) => {
    useQuerySpy(opts);
    return { data: [], isLoading: false };
  },
}));

vi.mock("@multica/core/api", () => ({
  api: { rawRequest: (url: string) => rawRequestSpy(url) },
  parseWithFallback: <T,>(raw: unknown): T => raw as T,
}));

vi.mock("@multica/core/platform", () => ({
  getCurrentWsId: () => wsIdRef.value,
  getCurrentSlug: () => "test-ws",
}));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlag: () => true,
}));

vi.mock("@multica/views/i18n", () => ({
  useT: () => ({ t: (accessor: unknown) => String(accessor) }),
}));

vi.mock("@multica/views/navigation", () => ({
  useNavigation: () => ({ push: vi.fn(), back: vi.fn() }),
}));

vi.mock("motion/react", () => ({
  motion: new Proxy({}, { get: () => () => null }),
  AnimatePresence: ({ children }: { children?: React.ReactNode }) => children,
  useReducedMotion: () => false,
}));

beforeEach(() => {
  useQuerySpy.mockClear();
  rawRequestSpy.mockClear();
  (window as { experimentalAPI?: unknown }).experimentalAPI = {
    pythia: {
      ensureUp: vi.fn(async () => undefined),
      getStatus: vi.fn(async () => "ready"),
      getURL: vi.fn(async () => "http://127.0.0.1:8765"),
      proxy: vi.fn(async () => ({ ok: true, body: { engine: true, osiris: true, oracle: true } })),
    },
  };
});

import { PythiaView } from "./pythia-view";

describe("PythiaView monitor query — explicit workspace_id (0.5.115)", () => {
  it("carries workspace_id as a query param (rawRequest never sends X-Workspace-ID)", async () => {
    render(<PythiaView />);
    expect(useQuerySpy).toHaveBeenCalledTimes(1);
    const opts = useQuerySpy.mock.calls[0][0] as {
      enabled: boolean;
      queryFn: () => Promise<unknown>;
    };
    expect(opts.enabled).toBe(true);
    await opts.queryFn();
    expect(rawRequestSpy).toHaveBeenCalledTimes(1);
    const url = rawRequestSpy.mock.calls[0][0] as string;
    expect(url).toContain("/api/experimental/pythia-oracle/forecast/monitor?limit=30");
    expect(url).toContain("workspace_id=11111111-2222-3333-4444-555555555555");
  });

  it("disables the query when no workspace id is known", () => {
    wsIdRef.value = "";
    render(<PythiaView />);
    const opts = useQuerySpy.mock.calls[0][0] as { enabled: boolean };
    expect(opts.enabled).toBe(false);
    expect(rawRequestSpy).not.toHaveBeenCalled();
    wsIdRef.value = "11111111-2222-3333-4444-555555555555";
  });
});
