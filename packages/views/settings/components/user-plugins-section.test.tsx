import type { ReactNode } from "react";
import { describe, expect, it, beforeEach, vi } from "vitest";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enAuth from "../../locales/en/auth.json";
import enExperimental from "../../locales/en/experimental.json";
import jaExperimental from "../../locales/ja/experimental.json";

const mockListUserPlugins = vi.hoisted(() => vi.fn());
const mockDeleteUserPlugin = vi.hoisted(() => vi.fn());
const mockMutate = vi.hoisted(() => vi.fn());
const mockToastSuccess = vi.hoisted(() => vi.fn());
const mockToastError = vi.hoisted(() => vi.fn());
// 0.5.107 (audit P-1): the section reads the Labs preference list to drive
// each Switch, so the test controls it explicitly instead of letting the
// real hook call api.listExperimentalFlags (unmocked here).
const mockFlags = vi.hoisted(() => ({
  current: [] as Array<{ key: string; enabled: boolean }>,
}));

vi.mock("@multica/core/api", () => ({
  api: {
    listUserPlugins: mockListUserPlugins,
    deleteUserPlugin: mockDeleteUserPlugin,
  },
}));

vi.mock("@multica/core/experimental", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/experimental")>(
      "@multica/core/experimental",
    );
  return {
    ...actual,
    useUpdateExperimentalFlag: () => ({
      mutate: mockMutate,
      isPending: false,
    }),
    useExperimentalFlags: () => ({ data: mockFlags.current }),
  };
});

vi.mock("sonner", () => ({
  toast: { success: mockToastSuccess, error: mockToastError },
}));

vi.mock("../../experimental/components/user-plugin-form-dialog", () => ({
  UserPluginFormDialog: () => null,
}));

import { pluginInjectedSkillNames, UserPluginsSection } from "./user-plugins-section";

function makeI18nProvider(locale: "en" | "ja", experimental: Record<string, unknown>) {
  return function I18nWrapper({ children }: { children: ReactNode }) {
    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    return (
      <QueryClientProvider client={qc}>
        <I18nProvider
          locale={locale}
          resources={{
            en: { common: enCommon, auth: enAuth, experimental: enExperimental },
            ja: { common: enCommon, auth: enAuth, experimental: experimental as typeof enExperimental },
          }}
        >
          {children}
        </I18nProvider>
      </QueryClientProvider>
    );
  };
}

// F-008: the global skill-injection ack gate keys off a plugin manifest's
// capabilities.skills. The helper must return the declared skill names only
// when the manifest actually carries them — a plugin with no skills block (or
// no manifest at all) is NOT a global-injection lab and needs no ack.
describe("pluginInjectedSkillNames — F-008 ack gate", () => {
  it("returns declared skills for a tool-lab manifest", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { skills: ["kb-search", "kb-graph"], agents: [], leader: "" },
      }),
    ).toEqual(["kb-search", "kb-graph"]);
  });

  it("returns [] when the manifest declares no skills", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { agents: ["my-agent"], leader: "my-agent" },
      }),
    ).toEqual([]);
    expect(pluginInjectedSkillNames({ runtime: { kind: "inline" } })).toEqual([]);
  });

  it("returns [] for undefined / non-object manifests", () => {
    expect(pluginInjectedSkillNames(undefined)).toEqual([]);
    expect(pluginInjectedSkillNames(null as unknown as Record<string, unknown>)).toEqual([]);
  });

  it("filters out non-string entries defensively", () => {
    expect(
      pluginInjectedSkillNames({
        capabilities: { skills: ["real", 42, "", null] },
      }),
    ).toEqual(["real"]);
  });
});

// PR-5 i18n regression pins: the section must not emit hardcoded Chinese
// strings — all visible labels must come from the experimental namespace.
// The tests below render UserPluginsSection with real locale bundles and
// assert the localized strings are present (and Chinese absent) in en + ja.
describe("UserPluginsSection — i18n label rendering", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const basePlugin = {
    id: "p1",
    slug: "demo-plugin",
    flag_key: "user_demo_plugin",
    trigger_mode: "auto" as const,
    runtime_kind: "inline" as const,
    status: "active" as const,
    title: { en: "Demo Plugin" },
    description: { en: "Demo description" },
    manifest: null,
  };

  it("renders localized labels (en) and emits no Chinese strings", async () => {
    mockListUserPlugins.mockResolvedValue([basePlugin]);
    const I18nWrapper = makeI18nProvider("en", enExperimental);
    render(<UserPluginsSection />, { wrapper: I18nWrapper });

    await waitFor(() => {
      expect(screen.getByText("Demo Plugin")).toBeTruthy();
    });
    expect(screen.getByText("Self-driven")).toBeTruthy();
    expect(screen.getByText("Inline")).toBeTruthy();
    expect(screen.getByText("Active")).toBeTruthy();

    const root = document.body.textContent ?? "";
    expect(root).not.toMatch(/自驱|任务绑定|无运行时|内联|子进程|启用|停用|已删除|切换失败|删除失败|插件已删除/);
  });

  it("renders localized labels (ja) and emits no Chinese strings", async () => {
    mockListUserPlugins.mockResolvedValue([basePlugin]);
    const I18nWrapper = makeI18nProvider("ja", jaExperimental);
    render(<UserPluginsSection />, { wrapper: I18nWrapper });

    await waitFor(() => {
      expect(screen.getByText("Demo Plugin")).toBeTruthy();
    });
    expect(screen.getByText("自走化")).toBeTruthy();
    expect(screen.getByText("インライン")).toBeTruthy();
    expect(screen.getByText("有効")).toBeTruthy();

    const root = document.body.textContent ?? "";
    expect(root).not.toMatch(/自驱|任务绑定|无运行时|内联|子进程|启用|停用|已删除|切换失败|删除失败|插件已删除/);
  });

  it("resolves the delete-failure toast string with interpolation from the i18n namespace", () => {
    // Direct namespace assertion: the template `"Delete failed: {{msg}}"`
    // must come from experimental.user_plugins.toast.delete_failed so the
    // interpolation contract is pinned to the i18n key, not the legacy
    // template-literal concatenation that PR-5 replaced.
    const toastNs = (enExperimental as { user_plugins: { toast: Record<string, string> } })
      .user_plugins.toast;
    expect(toastNs.delete_failed).toBe("Delete failed: {{msg}}");
    // 0.5.89: the success toast reports how many resources the teardown
    // ledger reclaimed.
    expect(toastNs.delete_success).toBe("Plugin deleted ({{count}} resources reclaimed)");
    expect(toastNs.toggle_failed).toBe("Toggle failed");
  });

  it("delete_failed interpolation template renders Delete failed: network unreachable", async () => {
    // i18next interpolation contract for {{msg}} — the renderer produces
    // the same exact string the toast call must use. Pins both the key
    // path AND the interpolation placeholder in one assertion.
    const i18n = (await import("i18next")).default;
    await i18n.init({
      lng: "en",
      resources: { en: { experimental: enExperimental } },
      defaultNS: "experimental",
      interpolation: { prefix: "{{", suffix: "}}" },
    });
    // Cast: the I18nResources augmentation only fires through views'
    // t(($) => $.foo) selector API; direct string keys are runtime-valid
    // because we initialised the instance above with the experimental NS.
    const rendered = (i18n.t as (k: string, opts?: Record<string, string>) => string)(
      "user_plugins.toast.delete_failed",
      { msg: "network unreachable" },
    );
    expect(rendered).toBe("Delete failed: network unreachable");
  });
});

// 0.5.107 (audit P-1): the Switch used to read `plugin.status`, a field the
// toggle never writes — so every click snapped back on the next refetch and
// a lab that was actually OFF rendered as ON. It now reads the Labs
// preference the mutation writes. Server-side, RunUserPlugin gained the
// matching gate, so the switch and the executable surface agree.
describe("UserPluginsSection — Labs toggle binds the preference, not the row status", () => {
  const plugin = {
    id: "p1",
    slug: "demo-plugin",
    flag_key: "user_demo_plugin",
    trigger_mode: "auto" as const,
    runtime_kind: "inline" as const,
    // The row is active in every case below: that is precisely the state the
    // old binding mistook for "lab enabled".
    status: "active" as const,
    title: { en: "Demo Plugin" },
    description: { en: "Demo description" },
    manifest: null,
  };

  beforeEach(() => {
    vi.clearAllMocks();
    mockListUserPlugins.mockResolvedValue([plugin]);
  });

  // Base UI marks the root with aria-checked and/or data-checked; accept
  // either so the assertion cannot pass vacuously on an attribute rename.
  function switchIsOn(el: HTMLElement): boolean {
    return el.getAttribute("aria-checked") === "true" || el.hasAttribute("data-checked");
  }

  async function renderSwitch(): Promise<HTMLElement> {
    const I18nWrapper = makeI18nProvider("en", enExperimental);
    render(<UserPluginsSection />, { wrapper: I18nWrapper });
    await waitFor(() => expect(screen.getByText("Demo Plugin")).toBeTruthy());
    return screen.getByRole("switch") as HTMLElement;
  }

  it("renders OFF while the preference is off even though the row is active", async () => {
    mockFlags.current = [{ key: plugin.flag_key, enabled: false }];
    expect(switchIsOn(await renderSwitch())).toBe(false);
  });

  it("renders ON from the preference and writes the preference on click", async () => {
    mockFlags.current = [{ key: plugin.flag_key, enabled: true }];
    const sw = await renderSwitch();
    // Guards against a vacuous pass: if the component rendered no state at
    // all, the OFF assertion above could not tell a fix from a regression.
    expect(switchIsOn(sw)).toBe(true);

    fireEvent.click(sw);
    await waitFor(() =>
      expect(mockMutate).toHaveBeenCalledWith(
        { key: plugin.flag_key, enabled: false },
        expect.anything(),
      ),
    );
  });

  it("reads a missing flag row as OFF, never ON", async () => {
    mockFlags.current = [];
    expect(switchIsOn(await renderSwitch())).toBe(false);
  });
});
