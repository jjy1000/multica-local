/**
 * @vitest-environment jsdom
 */
import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import enSignature from "../../locales/en/signature.json";
import type { TimelineEntry } from "@multica/core/types";
import { SignatureCommentCard } from "./signature-comment-card";

const hookState = vi.hoisted(() => ({
  wsId: "ws-1",
  signatures: [] as Array<{
    id: string;
    fingerprint: string;
    signed_by: string;
    signed_at: string;
    ops: string[];
    asset_id: string;
    status: string;
  }>,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => hookState.wsId,
}));
vi.mock("@multica/core/signature/hooks", () => ({
  useIssueSignature: () => ({ data: { signatures: hookState.signatures, active: null } }),
}));
vi.mock("@multica/core/api", () => ({
  // Watermark fetch is cosmetic; a non-ok response renders the card bare.
  api: { rawRequest: vi.fn().mockResolvedValue({ ok: false }) },
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

function signatureEntry(createdAt: string): TimelineEntry {
  return {
    type: "comment",
    id: "c1",
    actor_type: "member",
    actor_id: "u1",
    created_at: createdAt,
    comment_type: "signature",
    content: "签署授权 · 指纹 `sha256:9f3ac21e…`",
  };
}

describe("SignatureCommentCard", () => {
  beforeEach(() => {
    cleanup();
    hookState.signatures = [];
  });

  it("renders the certificate marker without normal comment affordances", () => {
    render(<SignatureCommentCard issueId="i1" entry={signatureEntry("2026-10-04T06:22:00Z")} />);
    const card = screen.getByTestId("signature-comment-card");
    expect(card.textContent).toContain(enSignature.marker_title);
    expect(card.textContent).toContain(enSignature.marker_verified);
    // No reaction/edit/delete controls — certificates are not conversation.
    expect(card.querySelector("button")).toBeNull();
  });

  it("matches the marker to the newest signature signed before it", () => {
    hookState.signatures = [
      {
        id: "older",
        fingerprint: "aaaa" + "0".repeat(60),
        signed_by: "11111111-1111-1111-1111-111111111111",
        signed_at: "2026-10-01T00:00:00Z",
        ops: ["create_skill"],
        asset_id: "a1",
        status: "revoked",
      },
      {
        id: "match",
        fingerprint: "9f3ac21e77b0d4e8" + "0".repeat(48),
        signed_by: "11111111-1111-1111-1111-111111111111",
        signed_at: "2026-10-04T06:22:00Z",
        ops: ["offensive_drill", "create_agent"],
        asset_id: "a1",
        status: "active",
      },
    ];
    render(<SignatureCommentCard issueId="i1" entry={signatureEntry("2026-10-04T07:00:00Z")} />);
    const card = screen.getByTestId("signature-comment-card");
    expect(card.textContent).toContain("sha256:9f3a c21e");
    expect(card.textContent).toContain(enSignature.op_offensive_drill);
    // Determinism: same inputs → identical structure (watermark absent on
    // failed fetch, no random ids/classes).
    const { container: container2 } = render(
      <SignatureCommentCard issueId="i1" entry={signatureEntry("2026-10-04T07:00:00Z")} />,
    );
    const card2 = container2.querySelector('[data-testid="signature-comment-card"]');
    expect(card2?.innerHTML).toBe(card.innerHTML);
  });
});
