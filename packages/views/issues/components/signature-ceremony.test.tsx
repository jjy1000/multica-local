/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import enSignature from "../../locales/en/signature.json";
import { SignatureHeaderPill } from "./signature-header-pill";
import { SignatureCeremonyDialog } from "./signature-ceremony-dialog";

const hookState = vi.hoisted(() => ({
  assets: [] as Array<{ id: string; name: string; retired_at: string | null; public_key_fingerprint: string; mime: string; algorithm: string; image_sha256: string; workspace_id: string; activated_at: string; created_at: string }>,
  active: null as { fingerprint: string; signed_by: string; signed_at: string; ops: string[]; asset_id: string; id: string; status: string } | null,
}));
const signIssueMock = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/signature/hooks", () => ({
  useSignatureAssets: () => ({ data: hookState.assets }),
  useIssueSignature: () => ({ data: { signatures: [], active: hookState.active } }),
  useInvalidateSignatures: () => vi.fn(),
  signatureKeys: { assets: (wsId: string) => ["signature-assets", wsId] },
}));
vi.mock("@multica/core/api", () => ({
  api: { signIssue: signIssueMock },
}));
vi.mock("../../navigation", () => ({
  useNavigation: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    pathname: "/issues/i1",
    searchParams: new URLSearchParams(),
  }),
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

describe("SignatureHeaderPill", () => {
  beforeEach(() => {
    cleanup();
    hookState.active = null;
  });

  it("renders the ghost unsigned affordance when no active signature exists", () => {
    const { container } = render(<SignatureHeaderPill wsId="ws" issueId="i1" onOpen={() => {}} />);
    const pill = screen.getByTestId("signature-header-pill");
    expect(pill.getAttribute("data-status")).toBe("unsigned");
    expect(pill.textContent).toContain(enSignature.pill_unsigned);
    expect(container.querySelector("svg")).toBeTruthy();
  });

  it("renders signed state with the short fingerprint", () => {
    hookState.active = {
      id: "sig-1",
      fingerprint: "9f3ac21e77b0d4e8" + "0".repeat(48),
      signed_by: "u",
      signed_at: "2026-10-04T00:00:00Z",
      ops: ["offensive_drill"],
      asset_id: "a",
      status: "active",
    };
    render(<SignatureHeaderPill wsId="ws" issueId="i1" onOpen={() => {}} />);
    const pill = screen.getByTestId("signature-header-pill");
    expect(pill.getAttribute("data-status")).toBe("signed");
    expect(pill.textContent).toContain("9f3a c21e");
  });

  it("opens the ceremony on click", () => {
    const onOpen = vi.fn();
    render(<SignatureHeaderPill wsId="ws" issueId="i1" onOpen={onOpen} />);
    fireEvent.click(screen.getByTestId("signature-header-pill"));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });
});

function renderDialog(open = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <SignatureCeremonyDialog open={open} onOpenChange={() => {}} wsId="ws" issueId="i1" issueTitle="红蓝对抗演练" />
    </QueryClientProvider>,
  );
}

describe("SignatureCeremonyDialog", () => {
  beforeEach(() => {
    cleanup();
    hookState.assets = [];
    hookState.active = null;
    signIssueMock.mockReset();
  });

  it("shows the no-asset empty state with a settings shortcut", () => {
    renderDialog();
    expect(screen.getByTestId("signature-ceremony-empty")).toBeTruthy();
    expect(screen.getByText(enSignature.ceremony_no_asset)).toBeTruthy();
  });

  it("renders scope chips, expiry choices, and the stamp button with an active asset", () => {
    hookState.assets = [
      {
        id: "asset-1",
        name: "primary",
        retired_at: null,
        public_key_fingerprint: "abcd1234abcd1234",
        mime: "image/png",
        algorithm: "ed25519",
        image_sha256: "",
        workspace_id: "ws",
        activated_at: "",
        created_at: "",
      },
    ];
    renderDialog();
    expect(screen.getByTestId("signature-ceremony-stamp")).toBeTruthy();
    const ops = screen.getByTestId("signature-op-choices");
    expect(ops.querySelectorAll("[data-op]").length).toBe(6);
    expect(ops.querySelector('[data-op="offensive_drill"]')?.getAttribute("data-selected")).toBe("true");
    // Keyframes + reduced-motion gate live in one mounted <style> block
    // inside the dialog's portal. Sonner also mounts a <style>, so match
    // by content rather than taking the first element.
    const style = Array.from(document.querySelectorAll("style")).find((el) =>
      el.textContent?.includes("sig-seal-drop"),
    );
    expect(style?.textContent).toContain("@keyframes sig-seal-drop");
    expect(style?.textContent).toContain("@media (prefers-reduced-motion: reduce)");
  });

  it("signs on stamp and lands in the signed phase with the fingerprint", async () => {
    hookState.assets = [
      {
        id: "asset-1",
        name: "primary",
        retired_at: null,
        public_key_fingerprint: "abcd1234abcd1234",
        mime: "image/png",
        algorithm: "ed25519",
        image_sha256: "",
        workspace_id: "ws",
        activated_at: "",
        created_at: "",
      },
    ];
    signIssueMock.mockResolvedValue({
      id: "sig-1",
      fingerprint: "9f3ac21e77b0d4e8" + "0".repeat(48),
      signed_by: "u",
      signed_at: "2026-10-04T00:00:00Z",
      ops: ["offensive_drill"],
      asset_id: "asset-1",
      status: "active",
    });
    renderDialog();
    fireEvent.click(screen.getByTestId("signature-ceremony-stamp"));
    await waitFor(
      () => expect(screen.getByTestId("signature-ceremony-verified")).toBeTruthy(),
      { timeout: 2000 },
    );
    expect(signIssueMock).toHaveBeenCalledWith(
      "i1",
      expect.objectContaining({ asset_id: "asset-1", ops: expect.arrayContaining(["offensive_drill"]) }),
    );
  });
});
