import { describe, it, expect, vi, beforeEach } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enIssues from "../../../locales/en/issues.json";
import { RecentLabsPanel } from "./recent-labs-panel";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

// Mock the workspace queries used by the panel so each test can
// supply its own fixtures. The default returns empty arrays so the
// empty-state copy is exercised unless a test overrides it.
const mockAgents = vi.hoisted(() => ({
  value: [] as Array<Record<string, unknown> & { id: string; name: string; description?: string; created_at: string }>,
}));
const mockSkills = vi.hoisted(() => ({
  value: [] as Array<Record<string, unknown> & { id: string; name: string; description?: string; created_at: string }>,
}));
const mockSquads = vi.hoisted(() => ({
  value: [] as Array<Record<string, unknown> & { id: string; name: string; description?: string; created_at: string }>,
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({
    queryKey: ["test", "agents"],
    queryFn: () => Promise.resolve(mockAgents.value),
  }),
  skillListOptions: () => ({
    queryKey: ["test", "skills"],
    queryFn: () => Promise.resolve(mockSkills.value),
  }),
  squadListOptions: () => ({
    queryKey: ["test", "squads"],
    queryFn: () => Promise.resolve(mockSquads.value),
  }),
}));

function renderPanel(props: Partial<React.ComponentProps<typeof RecentLabsPanel>> = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <RecentLabsPanel wsId="test-ws" {...props} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function makeItem<T extends { id: string; name: string; created_at: string }>(
  partial: Partial<T> & { id: string; name: string; created_at: string },
): T {
  return partial as T;
}

beforeEach(() => {
  mockAgents.value = [];
  mockSkills.value = [];
  mockSquads.value = [];
});

describe("RecentLabsPanel", () => {
  it("renders the hint copy and an empty state when every list is empty", () => {
    const { container } = renderPanel();
    expect(
      container.querySelector("[data-recent-labs-panel]"),
    ).not.toBeNull();
    // Hint text always renders (the explainer line at the top of the
    // panel); the empty-state body uses the picker's built-in
    // PickerEmpty ("No results") so we just verify the panel mounts
    // cleanly without data.
    expect(container.textContent ?? "").toContain(
      "View your recently created agents, skills, and squads",
    );
    expect(container.textContent ?? "").toContain("No results");
  });

  it("renders up to top-5 entries per section, sorted by created_at DESC", async () => {
    mockAgents.value = Array.from({ length: 7 }, (_, i) =>
      makeItem({
        id: `a${i}`,
        name: `Agent ${i}`,
        description: `desc ${i}`,
        created_at: new Date(2026, 6, 1 + i).toISOString(),
      }),
    );
    const { findAllByText } = renderPanel();
    // The picker renders 5 of the 7 supplied (top-5 by created_at).
    // The newest (i = 6) shows up as the first Agent row.
    const agentRows = await findAllByText(/Agent [0-9]+/);
    const labels = agentRows.map((el) => el.textContent ?? "");
    expect(labels.some((l) => l.includes("Agent 6"))).toBe(true);
    expect(labels.some((l) => l.includes("Agent 5"))).toBe(true);
    expect(labels.some((l) => l.includes("Agent 2"))).toBe(true);
    // Older rows (i = 0 / 1) are clipped from the top-5 window.
    expect(labels.some((l) => l.includes("Agent 0"))).toBe(false);
    expect(labels.some((l) => l.includes("Agent 1"))).toBe(false);
  });

  it("renders all three sections when their respective lists are populated", async () => {
    mockAgents.value = [
      makeItem({ id: "a", name: "Agent A", created_at: "2026-07-01T00:00:00Z" }),
    ];
    mockSkills.value = [
      makeItem({ id: "s", name: "Skill A", created_at: "2026-07-02T00:00:00Z" }),
    ];
    mockSquads.value = [
      makeItem({ id: "q", name: "Squad A", created_at: "2026-07-03T00:00:00Z" }),
    ];
    const { findAllByText } = renderPanel();
    expect(await findAllByText("Agent A")).toBeTruthy();
    expect(await findAllByText("Skill A")).toBeTruthy();
    expect(await findAllByText("Squad A")).toBeTruthy();
  });

  it("renders only the populated section headers when other lists are empty", async () => {
    mockAgents.value = [
      makeItem({ id: "a", name: "Lone Agent", created_at: "2026-07-01T00:00:00Z" }),
    ];
    const { findByText, queryByText } = renderPanel();
    expect(await findByText("Lone Agent")).toBeTruthy();
    // Section labels for the populated kinds render, others don't.
    expect(queryByText("Skills")).toBeNull();
    expect(queryByText("Squads")).toBeNull();
  });
});