/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import enSignature from "../../locales/en/signature.json";
import { SignatureTab } from "./signature-tab";

const testState = vi.hoisted(() => ({
  workspace: {
    id: "ws-1",
    name: "Test WS",
    slug: "test-ws",
    settings: {} as Record<string, unknown> | undefined,
  },
  updateWorkspace: vi.fn(),
  assets: [] as Array<Record<string, unknown>>,
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => testState.workspace,
}));
vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => testState.workspace.id,
}));
vi.mock("@multica/core/workspace/queries", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces"] }),
}));
vi.mock("@multica/core/signature/hooks", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/signature/hooks")>();
  return {
    ...actual,
    useInvalidateSignatures: () => vi.fn(),
    signatureKeys: {
      assets: (wsId: string) => ["signature-assets", wsId],
      workspaceHistory: (wsId: string) => ["signature-history", wsId],
      issue: (wsId: string, issueId: string) => ["issue-signature", wsId, issueId],
    },
  };
});
vi.mock("@multica/core/api", () => ({
  api: {
    listSignatureAssets: vi.fn(async () => testState.assets),
    listWorkspaceSignatures: vi.fn(async () => []),
    uploadSignatureAsset: vi.fn(),
    retireSignatureAsset: vi.fn(),
    revokeSignature: vi.fn(),
    updateWorkspace: testState.updateWorkspace,
  },
}));
vi.mock("../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown, opts?: Record<string, unknown>) => {
      const v = sel(enSignature);
      if (typeof v !== "string") return undefined;
      return opts ? v.replace(/\{\{(\w+)\}\}/g, (_, k) => String(opts[k] ?? `{{${k}}}`)) : v;
    },
  }),
}));

function renderTab() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SignatureTab />
    </QueryClientProvider>,
  );
}

describe("SignatureTab arm switch", () => {
  beforeEach(() => {
    cleanup();
    testState.workspace.settings = undefined;
    testState.updateWorkspace.mockReset();
    testState.assets = [];
  });

  it("defaults to the declaration gate with enable blocked until acknowledged", () => {
    const { container } = renderTab();
    expect(container.firstElementChild?.getAttribute("data-armed")).toBe("false");
    expect(screen.getByText(enSignature.enable_purpose)).toBeTruthy();
    expect(screen.getByText(enSignature.enable_risk)).toBeTruthy();
    const enable = screen.getByTestId("signature-enable-button") as HTMLButtonElement;
    expect(enable.disabled).toBe(true);

    fireEvent.click(screen.getByTestId("signature-ack-checkbox"));
    expect((screen.getByTestId("signature-enable-button") as HTMLButtonElement).disabled).toBe(false);
  });

  it("enables only after acknowledgment and writes the merged settings key", async () => {
    testState.workspace.settings = { default_runtime_id: "rt-1" };
    testState.updateWorkspace.mockResolvedValue({ ...testState.workspace });
    renderTab();
    fireEvent.click(screen.getByTestId("signature-ack-checkbox"));
    fireEvent.click(screen.getByTestId("signature-enable-button"));

    await vi.waitFor(() => expect(testState.updateWorkspace).toHaveBeenCalledTimes(1));
    const [, body] = testState.updateWorkspace.mock.calls[0] as [string, { settings: Record<string, unknown> }];
    expect(body.settings.signature_authorization_enabled).toBe(true);
    expect(body.settings.default_runtime_id).toBe("rt-1");
  });

  it("renders the manager surface once armed, with a two-click disable", async () => {
    testState.workspace.settings = { signature_authorization_enabled: true };
    renderTab();
    const tab = screen.getByTestId("settings-signature-tab");
    expect(tab.getAttribute("data-armed")).toBe("true");
    expect(screen.getByTestId("signature-upload-button")).toBeTruthy();

    fireEvent.click(screen.getByTestId("signature-disable-button"));
    expect(testState.updateWorkspace).not.toHaveBeenCalled();
    fireEvent.click(screen.getByTestId("signature-disable-button"));
    await vi.waitFor(() => expect(testState.updateWorkspace).toHaveBeenCalledTimes(1));
    const [, body] = testState.updateWorkspace.mock.calls[0] as [string, { settings: Record<string, unknown> }];
    // Disable writes an explicit false (auditability), mirroring the server.
    expect(body.settings.signature_authorization_enabled).toBe(false);
  });
});
