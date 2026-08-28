// issue-labs-section.test.tsx (0.5.84 P0 #1)
//
// Regression pin for the FLAG_ROUTE_SUFFIX map: every flag key with a
// dedicated /experimental/<suffix> view MUST appear here. The 0.5.81
// (semantica) and 0.5.83 (causal_graph) silent-link-death rounds both
// came from this dict silently missing a row — labSourceRouteSuffix()
// returned undefined and PropRow / create-issue redirect / IssueLabsSection
// trail links no-op'd without complaint. This test pins every flag key
// that ships a view today; a new lab without a row is caught here
// instead of in post-ship verification.

import { describe, it, expect, vi } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { I18nProvider } from "@multica/core/i18n/react";
import type { NavigationAdapter } from "../../navigation";
import { NavigationProvider } from "../../navigation";
import enIssues from "../../locales/en/issues.json";

import { IssueLabsSection, labSourceRouteSuffix } from "./issue-labs-section";

// ── 0.5.86: LabProgressCard wiring pin ────────────────────────────────────
//
// The progress card must receive the section's bound labSource, the live
// flag state and the snapshot-derived `live` flags. The heavy child surfaces
// (LabOutputPanel / LabLastResultChip) are stubbed out — this suite pins the
// IssueLabsSection → LabProgressCard contract, not the card internals (those
// live in lab-progress-card.test.tsx).

const progressCardCaptured = vi.hoisted(() => ({
  props: [] as Array<Record<string, unknown>>,
}));

vi.mock("./lab-progress-card", () => ({
  LabProgressCard: (props: Record<string, unknown>) => {
    progressCardCaptured.props.push(props);
    return null;
  },
}));

vi.mock("../../experimental/components/lab-output-panel", () => ({
  LabOutputPanel: () => null,
}));

vi.mock("./lab-last-result-chip", () => ({
  LabLastResultChip: () => null,
}));

const mockFlags = vi.hoisted(() => ({ value: [] as Array<unknown> }));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlags: () => ({ data: mockFlags.value }),
}));

vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: (wsId: string) => ({
    queryKey: ["test-agent-task-snapshot", wsId],
    queryFn: () => [],
  }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function makeFlag(key: string, enabled = true) {
  return {
    key,
    enabled,
    title: { zh: key, en: key },
    hides_deliverable_in_issue_timeline: false,
  };
}

function SectionWrapper({ children }: { children: ReactNode }) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const nav: NavigationAdapter = {
    push: vi.fn(),
    replace: vi.fn(),
    back: vi.fn(),
    pathname: "/issues/issue-1",
    searchParams: new URLSearchParams(),
    getShareableUrl: (p: string) => p,
  };
  return (
    <QueryClientProvider client={client}>
      <I18nProvider locale="en" resources={{ en: { issues: enIssues } }}>
        <NavigationProvider value={nav}>{children}</NavigationProvider>
      </I18nProvider>
    </QueryClientProvider>
  );
}

describe("IssueLabsSection → LabProgressCard wiring", () => {
  it("mounts the progress card with the bound lab, flag state and live flags", () => {
    mockFlags.value = [makeFlag("pythia_oracle", true)];
    progressCardCaptured.props = [];
    render(
      <IssueLabsSection issueId="issue-1" labSource="pythia_oracle" />,
      { wrapper: SectionWrapper },
    );
    expect(progressCardCaptured.props).toHaveLength(1);
    expect(progressCardCaptured.props[0]).toMatchObject({
      issueId: "issue-1",
      workspaceId: "ws-1",
      labSource: "pythia_oracle",
      flagEnabled: true,
      live: { running: false, queued: false, failed: false, cancelled: false },
    });
  });

  it("passes the auxiliary lab source through so the card can render the muted no-run row", () => {
    mockFlags.value = [makeFlag("causal_graph", true)];
    progressCardCaptured.props = [];
    render(
      <IssueLabsSection issueId="issue-2" labSource="causal_graph" />,
      { wrapper: SectionWrapper },
    );
    expect(progressCardCaptured.props[0]).toMatchObject({
      issueId: "issue-2",
      labSource: "causal_graph",
      flagEnabled: true,
    });
  });

  it("reports flagEnabled=false for a bound-but-disabled lab", () => {
    mockFlags.value = [makeFlag("timesfm", false)];
    progressCardCaptured.props = [];
    render(
      <IssueLabsSection issueId="issue-3" labSource="timesfm" />,
      { wrapper: SectionWrapper },
    );
    expect(progressCardCaptured.props[0]).toMatchObject({
      labSource: "timesfm",
      flagEnabled: false,
    });
  });
});

// Expected suffix mapping mirrors routes.tsx (apps/desktop/.../routes.tsx):
//   claude_science_lab → claude-lab
//   pythia_oracle      → pythia
//   mythos_swarm       → mythos
//   llm_wiki_bridge    → llm-wiki
//   code_canvas        → code-canvas
//   swarm_topology     → swarm-topology
//   semantica          → semantica-explorer
//   timesfm            → timesfm-lab
//   causal_graph       → causal-graph  (0.5.83 — the regression pin)
describe("labSourceRouteSuffix — FLAG_ROUTE_SUFFIX rows", () => {
  it("resolves every built-in lab flag to its /experimental/<suffix> view", () => {
    const expected: Array<[string, string]> = [
      ["claude_science_lab", "claude-lab"],
      ["pythia_oracle", "pythia"],
      ["mythos_swarm", "mythos"],
      ["llm_wiki_bridge", "llm-wiki"],
      ["code_canvas", "code-canvas"],
      ["swarm_topology", "swarm-topology"],
      ["semantica", "semantica-explorer"],
      ["timesfm", "timesfm-lab"],
      ["causal_graph", "causal-graph"],
    ];
    for (const [flagKey, suffix] of expected) {
      expect(labSourceRouteSuffix(flagKey), flagKey).toBe(suffix);
    }
  });

  it("returns undefined for unknown / retired flag keys (silent no-op trap)", () => {
    // The audit's whole point: labSourceRouteSuffix('causal_graph') used
    // to return undefined, killing every external-link affordance.
    // Unknown keys SHOULD return undefined — this asserts the negative
    // case so the positive case above cannot drift to also-undefined.
    expect(labSourceRouteSuffix("not_a_real_flag")).toBeUndefined();
    expect(labSourceRouteSuffix("constitution_agent")).toBeUndefined(); // retired 0.3.57
    expect(labSourceRouteSuffix("chat_pin_ui")).toBeUndefined(); // removed 0.3.68
  });

  it("returns undefined for null / empty / whitespace inputs", () => {
    expect(labSourceRouteSuffix(null)).toBeUndefined();
    expect(labSourceRouteSuffix(undefined)).toBeUndefined();
    expect(labSourceRouteSuffix("")).toBeUndefined();
  });
});