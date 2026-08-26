/**
 * @vitest-environment jsdom
 */
import type { ReactNode } from "react";
import { describe, it, expect, beforeEach, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enSettings from "../../locales/en/settings.json";

const mockFlags: { refetch: ReturnType<typeof vi.fn>; data: unknown[] } = vi.hoisted(() => ({
  refetch: vi.fn(() => Promise.resolve({ data: [] })),
  data: [
    {
      key: "pythia_oracle",
      enabled: true,
      default_enabled: true,
      title: { en: "Pythia Oracle", zh: "Pythia 神谕" },
      description: { en: "Forecast lab", zh: "预测实验室" },
      runtime_kind: "subprocess",
    },
  ],
}));
const invalidateSpy = vi.hoisted(() => vi.fn(() => Promise.resolve()));
const rawRequestSpy = vi.hoisted(() => vi.fn());

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: invalidateSpy }),
  useMutation: () => ({ mutate: vi.fn(), isPending: false, variables: undefined }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel?: (s: { user: { id: string } }) => unknown) =>
    sel ? sel({ user: { id: "user-1" } }) : { user: { id: "user-1" } },
  registerAuthStore: vi.fn(),
  createAuthStore: vi.fn(),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    rawRequest: (...args: unknown[]) => {
      rawRequestSpy(...args);
      return Promise.resolve(
        new Response(
          JSON.stringify({ attempted: 1, succeeded: 1, failed: 0 }),
          { status: 200, headers: { "content-type": "application/json" } },
        ),
      );
    },
  },
}));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlags: () => ({
    data: mockFlags.data,
    isLoading: false,
    error: null,
    refetch: mockFlags.refetch,
  }),
  useUpdateExperimentalFlag: () => ({ mutate: vi.fn(), isPending: false, variables: undefined }),
}));

vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (accessor: (dict: unknown) => string, params?: Record<string, unknown>) => {
      const template = accessor(enSettings);
      if (!params) return template;
      return template.replace(/\{\{(\w+)\}\}/g, (_, k: string) => String(params[k] ?? ""));
    },
    i18n: { language: "en" },
  }),
}));

vi.mock("./labs-flag-side-panel", () => ({
  LabsFlagSidePanel: () => null,
}));

vi.mock("./user-plugins-section", () => ({
  userPluginKeys: { all: ["user-plugins"] },
  UserPluginsSection: () => null,
}));

vi.mock("sonner", () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}));

import { LabsTab } from "./labs-tab";

const TEST_RESOURCES = {
  en: { common: enCommon, settings: enSettings },
};

function I18nWrapper({ children }: { children: ReactNode }) {
  return (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      {children}
    </I18nProvider>
  );
}

describe("LabsTab — install-all + per-flag cache invalidation", () => {
  beforeEach(() => {
    cleanup();
    vi.clearAllMocks();
    mockFlags.refetch.mockResolvedValue({ data: [] });
  });

  it("runInstallAll invalidates both experimental-flags (via refetch) AND user-plugins query keys", async () => {
    const user = userEvent.setup();
    render(<LabsTab />, { wrapper: I18nWrapper });

    const installAllButton = screen.getByRole("button", { name: /运行 install/i });
    await user.click(installAllButton);

    // refetch() handles the experimental-flags key; qc.invalidateQueries
    // handles the user-plugins key. Both must fire so UserPluginsSection
    // below the catalog re-renders with the post-install resource counts.
    await waitFor(() => {
      expect(mockFlags.refetch).toHaveBeenCalledTimes(1);
      expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["user-plugins"] });
    });
    // No accidental double-invalidation of the experimental-flags key.
    expect(invalidateSpy).not.toHaveBeenCalledWith({ queryKey: ["experimental-flags"] });
  });

  it("rawRequest targets /api/experimental-resources/install-all", async () => {
    const user = userEvent.setup();
    render(<LabsTab />, { wrapper: I18nWrapper });

    await user.click(screen.getByRole("button", { name: /运行 install/i }));

    await waitFor(() => {
      expect(rawRequestSpy).toHaveBeenCalledWith(
        "/api/experimental-resources/install-all",
        expect.objectContaining({ method: "POST" }),
      );
    });
  });
});
