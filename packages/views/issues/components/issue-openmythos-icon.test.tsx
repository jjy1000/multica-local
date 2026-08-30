// IssueOpenMythosIcon.test.tsx (0.5.90)
//
// ICP-5 smoke pins for the OpenMythos outer-loop affordance on the
// issue header:
//   - issue NOT bound to mythos_swarm → renders NOTHING (passive law);
//   - bound → renders the indicator trigger with the OpenMythos label;
//   - bound without an agent/squad assignee → popover surfaces the
//     target-required hint and the start control stays disabled
//     (enhancer-only contract: no target, no outer loop).
// The run pipeline itself is covered by the Go handler/service tests
// plus the live loop; here only the affordance contract is pinned.

import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";

import enIssues from "../../locales/en/issues.json";
import { IssueOpenMythosIcon } from "./issue-openmythos-icon";

const mockState = vi.hoisted(() => ({
  wsId: "ws-1",
  rawRequest: vi.fn(),
}));

vi.mock("@multica/core/hooks", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/hooks")>("@multica/core/hooks");
  return {
    ...actual,
    useWorkspaceId: () => mockState.wsId,
  };
});

vi.mock("@multica/core/api", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/api")>("@multica/core/api");
  return {
    ...actual,
    api: {
      ...actual.api,
      rawRequest: (...args: unknown[]) => mockState.rawRequest(...args),
    },
  };
});

function renderIcon(props: Partial<Parameters<typeof IssueOpenMythosIcon>[0]> = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <IssueOpenMythosIcon
          issueId="issue-1"
          labSource={null}
          issueTitle="Ship the thing"
          issueDescription={null}
          assigneeType={null}
          {...props}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  mockState.rawRequest.mockReset();
  // default: no mythos runs for the issue (404 → empty list)
  mockState.rawRequest.mockResolvedValue(new Response(JSON.stringify([]), { status: 200 }));
});

describe("IssueOpenMythosIcon", () => {
  it("renders nothing when the issue is not bound to mythos_swarm (ICP-5)", () => {
    const { container } = renderIcon({ labSource: null });
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing for other labs even when bound to one", () => {
    const { container } = renderIcon({ labSource: "pythia_oracle" });
    expect(container).toBeEmptyDOMElement();
  });

  it("bound issue renders the OpenMythos trigger", () => {
    renderIcon({ labSource: "mythos_swarm", assigneeType: "agent" });
    const trigger = screen.getByRole("button", { name: "OpenMythos outer loop" });
    expect(trigger).toBeDefined();
  });

  it("popover without an agent/squad assignee shows the target hint and disables start", () => {
    renderIcon({ labSource: "mythos_swarm", assigneeType: null });
    fireEvent.click(screen.getByRole("button", { name: "OpenMythos outer loop" }));
    const hint = screen.getByText("Assign this issue to an agent or squad first");
    expect(hint).toBeDefined();
    const start = screen.getByRole("button", { name: "Start outer loop" });
    expect((start as HTMLButtonElement).disabled).toBe(true);
    // and it must NOT have attempted to start anything
    expect(mockState.rawRequest).not.toHaveBeenCalledWith(
      expect.stringContaining("/mythos-swarm/run"),
      expect.anything(),
    );
  });

  it("popover with an agent assignee enables the start control", () => {
    renderIcon({ labSource: "mythos_swarm", assigneeType: "agent" });
    fireEvent.click(screen.getByRole("button", { name: "OpenMythos outer loop" }));
    const start = screen.getByRole("button", { name: "Start outer loop" });
    expect((start as HTMLButtonElement).disabled).toBe(false);
  });
});
