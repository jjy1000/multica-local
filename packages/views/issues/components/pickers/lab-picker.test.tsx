import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../../locales/en/common.json";
import enIssues from "../../../locales/en/issues.json";
import { LabPicker } from "./lab-picker";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

// Mock the experimental flags query so the test controls which
// flags show up in the picker. The default for every test is one
// enabled lab (claude_science_lab); individual subtests override
// via `mockFlags.value`.
const mockFlags = vi.hoisted(() => ({
  value: [
    { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
    { key: "pythia_oracle", title: { zh: "Pythia 多视角预测", en: "Pythia Multi-Perspective Forecasting" }, enabled: true },
  ] as Array<{
    key: string;
    title: { zh: string; en: string };
    enabled: boolean;
    hide_from_issue_lab_picker?: boolean;
    always_show_in_lab_picker?: boolean;
  }>,
}));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlags: () => ({ data: mockFlags.value }),
}));

function renderPicker(props: Partial<React.ComponentProps<typeof LabPicker>> = {}) {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  const onUpdate = vi.fn();
  const onClearAssignee = vi.fn();
  const result = render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider resources={TEST_RESOURCES} locale="en">
        <LabPicker
          labSource={null}
          onUpdate={onUpdate}
          onClearAssignee={onClearAssignee}
          {...props}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return { ...result, onUpdate, onClearAssignee };
}

describe("LabPicker", () => {
  beforeEach(() => {
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
      { key: "pythia_oracle", title: { zh: "Pythia 多视角预测", en: "Pythia Multi-Perspective Forecasting" }, enabled: true },
    ];
  });

  it("renders 'None' as the first entry and one row per flag", () => {
    renderPicker();
    // PropertyPicker renders the trigger with no anchor by default;
    // open it via the controlled path. PropertyPicker wraps a
    // PopoverTrigger — to open the popover, fire click on the
    // trigger. 0.5.89: the default chrome shows the localized "None"
    // hint (the old empty aria-hidden span rendered an ~8×0px
    // invisible chip), so the trigger itself is assertable.
    const trigger = document.querySelector("button[aria-haspopup]")!;
    expect(trigger).toHaveTextContent("None");
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    expect(items.length).toBe(3); // None + 2 flags
    expect(items[0]).toHaveTextContent("None");
  });

  it("0.5.89: the trigger shows the bound lab's title when a lab is set", () => {
    // Title render order is `title.zh || title.en || key` — the Chinese
    // title is what the DOM carries. Before this fix the bound state
    // ALSO rendered a blank chip (the issue-detail row only looked
    // alive because of the sibling open-panel icon).
    renderPicker({ labSource: "claude_science_lab" });
    const trigger = document.querySelector("button[aria-haspopup]")!;
    expect(trigger).toHaveTextContent("Claude 实验室");
    // A bound-but-no-longer-cataloged key degrades to the raw key
    // instead of an empty chip.
    renderPicker({ labSource: "retired_lab_key" });
    const trigger2 = document.querySelectorAll("button[aria-haspopup]")[1]!;
    expect(trigger2).toHaveTextContent("retired_lab_key");
  });

  it("selecting a non-mutex lab keeps the assignee and still updates source+mode", () => {
    const { onUpdate, onClearAssignee } = renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    // The first non-None row is the claude_science_lab entry.
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[1]!);
    // 0.3.33 mutex narrowing (realigned 2026-07-28): only mythos_swarm
    // sole-mode and swarm_topology reserve the roster. claude_science_lab
    // coexists with a manual assignee — the picker must NOT clear it,
    // otherwise a user-pinned engineer silently disappears on lab pick.
    expect(onClearAssignee).not.toHaveBeenCalled();
    // 0.3.31: onUpdate carries lab_mode for non-mythos labs as well —
    // "sole" is the implicit default for any lab that doesn't expose
    // dual-mode tabs. The field is always present on the payload.
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: "claude_science_lab",
      lab_mode: "sole",
    });
  });

  it("selecting swarm_topology clears the assignee BEFORE onUpdate (mutex set)", () => {
    mockFlags.value = [
      ...mockFlags.value,
      { key: "swarm_topology", title: { zh: "群集拓扑", en: "Swarm Topology" }, enabled: true },
    ];
    const { onUpdate, onClearAssignee } = renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    // Items: None + claude_science_lab + pythia_oracle + swarm_topology.
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[3]!);
    // Order matters: the picker must clear the assignee BEFORE sending the
    // new lab_source, so the parent's single PATCH (if it batches) carries
    // both fields and the server's mutex gate never fires for a
    // user-initiated swap (Active Contract #5).
    const clearOrder = onClearAssignee.mock.invocationCallOrder[0]!;
    const updateOrder = onUpdate.mock.invocationCallOrder[0]!;
    expect(clearOrder).toBeLessThan(updateOrder);
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: "swarm_topology",
      lab_mode: "sole",
    });
  });

  it("mythos_swarm sole mode clears the assignee", () => {
    mockFlags.value = [
      ...mockFlags.value,
      { key: "mythos_swarm", title: { zh: "Mythos 群集", en: "Mythos Swarm" }, enabled: true },
    ];
    const sole = renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    // Items: None + claude_science_lab + pythia_oracle + mythos_swarm.
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[3]!);
    expect(sole.onClearAssignee).toHaveBeenCalledTimes(1);
    expect(sole.onUpdate).toHaveBeenCalledWith({
      lab_source: "mythos_swarm",
      lab_mode: "sole",
    });
  });

  it("mythos_swarm enhancer mode does not clear the assignee", () => {
    mockFlags.value = [
      ...mockFlags.value,
      { key: "mythos_swarm", title: { zh: "Mythos 群集", en: "Mythos Swarm" }, enabled: true },
    ];
    // enhancer reverses the mutex: the user kept an assignee by design.
    const enh = renderPicker({ labSource: null, labMode: "enhancer" });
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[3]!);
    expect(enh.onClearAssignee).not.toHaveBeenCalled();
    expect(enh.onUpdate).toHaveBeenCalledWith({
      lab_source: "mythos_swarm",
      lab_mode: "enhancer",
    });
  });

  it("selecting 'None' does NOT call onClearAssignee", () => {
    // The asymmetric semantics is intentional: clearing the lab
    // is a user-driven "I want a different actor" gesture, not
    // a lab-imposed "the lab owns the roster" handoff. The
    // caller's onClearAssignee is therefore skipped on the
    // clear path — the user explicitly chose None, they know
    // what they're doing.
    const { onUpdate, onClearAssignee } = renderPicker({ labSource: "pythia_oracle" });
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    // First item is the None row.
    fireEvent.click(items[0]!);
    expect(onClearAssignee).not.toHaveBeenCalled();
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: null,
      lab_mode: null,
    });
  });

  it("works safely when onClearAssignee is omitted", () => {
    // Defensive: a caller who doesn't pass onClearAssignee
    // (e.g. on web, where the issue detail doesn't have an
    // assignee picker wired to this callback) must not crash.
    // Selecting a non-empty lab should still call onUpdate.
    const onUpdate = vi.fn();
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={queryClient}>
        <I18nProvider resources={TEST_RESOURCES} locale="en">
          <LabPicker labSource={null} onUpdate={onUpdate} />
        </I18nProvider>
      </QueryClientProvider>,
    );
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[1]!);
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: "claude_science_lab",
      lab_mode: "sole",
    });
  });

  it("selecting the currently-selected lab short-circuits (no-op)", () => {
    // Picking the same lab the issue already has is a no-op:
    // the picker short-circuits at line 150-153 and neither
    // onClearAssignee nor onUpdate fires. This prevents a
    // redundant PATCH with `assignee_type: null,
    // assignee_id: null` even though the issue already has
    // the same lab.
    const { onUpdate, onClearAssignee } = renderPicker({ labSource: "claude_science_lab" });
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[1]!); // claude_science_lab row
    expect(onClearAssignee).not.toHaveBeenCalled();
    expect(onUpdate).not.toHaveBeenCalled();
  });

  it("0.3.45.8: hides flags whose hide_from_issue_lab_picker is true", () => {
    // llm_wiki_bridge and agent_self_optimization are infrastructure
    // / self-driven labs — enabled means global, not per-issue. The
    // picker must NOT offer them as a "实验插件" choice even when
    // enabled. They still appear in the Labs settings tab where the
    // user flips the toggle on.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
      { key: "pythia_oracle", title: { zh: "Pythia 多视角预测", en: "Pythia Multi-Perspective Forecasting" }, enabled: true },
      { key: "llm_wiki_bridge", title: { zh: "LLM Wiki 本地桥接", en: "LLM Wiki Bridge" }, enabled: true, hide_from_issue_lab_picker: true },
      { key: "agent_self_optimization", title: { zh: "智能体自优化循环", en: "Agent Self-Opt" }, enabled: true, hide_from_issue_lab_picker: true },
    ];
    renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    // None + claude_science_lab + pythia_oracle = 3 items.
    // The two hide_from_issue_lab_picker flags must NOT be present.
    expect(items.length).toBe(3);
    const labels = Array.from(items).map((el) => el.textContent ?? "");
    expect(labels.some((l) => l.includes("LLM Wiki"))).toBe(false);
    expect(labels.some((l) => l.includes("自优化"))).toBe(false);
  });

  it("0.5.6: the picker renders only catalog-returned opt-in flags (no product-level resources)", () => {
    // 0.5.6 contract: `agent_creation_studio` and
    // `agent_self_optimization` are no longer in the catalog. The
    // picker therefore lists only the remaining opt-in flags the
    // server returns (claude_science_lab here). The mock simulates
    // the 0.5.6 server shape: those two keys are absent from
    // `useExperimentalFlags`'s return. 0.5.5.2's HIDDEN_LAB_KEYS
    // client-side black-list is removed; the catalog itself is now
    // the source of truth.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
      { key: "mythos_swarm", title: { zh: "Mythos 蜂群", en: "Mythos Swarm" }, enabled: true },
    ];
    const { onUpdate, onClearAssignee } = renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    expect(document.querySelectorAll("button[data-picker-item]").length).toBe(3); // None + 2 flags
    const labels = Array.from(
      document.querySelectorAll("button[data-picker-item]"),
    ).map((el) => el.textContent ?? "");
    // None + claude_science_lab + mythos_swarm.
    expect(labels.some((l) => l.includes("Claude"))).toBe(true);
    expect(labels.some((l) => l.includes("Mythos"))).toBe(true);
    // Tapping a row still works as a normal lab bind; claude_science_lab
    // is non-mutex so the assignee stays put (0.3.33 narrowing).
    const claude = document.querySelectorAll("button[data-picker-item]")[1]!;
    fireEvent.click(claude);
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: "claude_science_lab",
      lab_mode: "sole",
    });
    expect(onClearAssignee).not.toHaveBeenCalled();
  });

  it("0.5.6: an empty catalog collapses the picker to a single None row", () => {
    // 0.5.6: when the server returns an empty flags list (which
    // happens in a single-user fork with only product-level
    // resources — no opt-in flags enabled), the picker renders
    // only the "None" row. The user picks the leader agent from
    // the AssigneePicker instead.
    mockFlags.value = [];
    renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    expect(document.querySelectorAll("button[data-picker-item]").length).toBe(1);
    expect(document.querySelector("button[data-picker-item]")).toHaveTextContent("None");
  });

  it("AlwaysShowBypass: hide_from_issue_lab_picker + disabled is reachable when always_show_in_lab_picker=true", () => {
    // The catalog.AlwaysShowInLabPicker DTO marker is an
    // escape hatch over the picker-hide filter (sibling of the
    // `lab_managed` marker — CLAUDE.md Active Contract #4).
    // A flag that is hide_from_picker=true AND enabled=false
    // but has always_show_in_lab_picker=true MUST still appear
    // in the picker — the catalog's intent is "reachable but
    // never auto-enabled", not "completely invisible".
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
      {
        key: "experimental_onboarding_lab",
        title: { zh: "试用新实验室", en: "Try New Lab" },
        enabled: false,
        hide_from_issue_lab_picker: true,
        always_show_in_lab_picker: true,
      },
    ];
    renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    // None + claude_science_lab + experimental_onboarding_lab = 3.
    // Title render order is `title.zh || title.en || key`, so the
    // Chinese title is what shows up in the DOM.
    const labels = Array.from(items).map((el) => el.textContent ?? "");
    expect(labels.some((l) => l.includes("试用新实验室"))).toBe(true);
    expect(items.length).toBe(3);
  });

  it("HideFromPicker: hide_from_issue_lab_picker + disabled stays out when always_show=false", () => {
    // Without the always_show escape hatch, the prior behavior
    // holds: a hide_from_picker=true flag is invisible in the
    // per-issue picker regardless of its enabled state. This
    // pins the asymmetric semantics — the escape hatch must be
    // EXPLICITLY opted into via always_show_in_lab_picker=true.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
      { key: "pythia_oracle", title: { zh: "Pythia 多视角预测", en: "Pythia Multi-Perspective Forecasting" }, enabled: true },
      {
        key: "infrastructure_invisible_lab",
        title: { zh: "基础设施实验室", en: "Infra Lab" },
        enabled: false,
        hide_from_issue_lab_picker: true,
      },
    ];
    renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    // None + claude_science_lab + pythia_oracle = 3; the
    // hide_from_picker flag must NOT appear.
    const labels = Array.from(items).map((el) => el.textContent ?? "");
    expect(labels.some((l) => l.includes("Infra Lab"))).toBe(false);
    expect(items.length).toBe(3);
  });
});
