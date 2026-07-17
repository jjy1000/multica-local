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
  ],
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
    // open it via the controlled path. The triggerRender is not
    // supplied so the default chrome shows. PropertyPicker wraps a
    // PopoverTrigger — to open the popover, fire click on the
    // trigger. The trigger text is empty (default chrome is a
    // blank span because triggerRender is undefined), so we look
    // for the data-picker-item buttons once the popover is open.
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    const items = document.querySelectorAll("button[data-picker-item]");
    expect(items.length).toBe(3); // None + 2 flags
    expect(items[0]).toHaveTextContent("None");
  });

  it("selecting a non-empty lab calls onClearAssignee THEN onUpdate", () => {
    const { onUpdate, onClearAssignee } = renderPicker();
    const trigger = document.querySelector("button[aria-haspopup]")!;
    fireEvent.click(trigger);
    // The first non-None row is the claude_science_lab entry.
    const items = document.querySelectorAll("button[data-picker-item]");
    fireEvent.click(items[1]!);
    // Order matters: the picker must clear the assignee BEFORE
    // sending the new lab_source, so the parent's single PATCH
    // (if it batches) carries both fields and the server's mutex
    // gate never fires for a user-initiated swap.
    const clearOrder = onClearAssignee.mock.invocationCallOrder[0]!;
    const updateOrder = onUpdate.mock.invocationCallOrder[0]!;
    expect(clearOrder).toBeLessThan(updateOrder);
    // 0.3.31: onUpdate now carries lab_mode for non-mythos labs
    // as well — "sole" is the implicit default for any lab that
    // doesn't expose dual-mode tabs. The field is always present
    // on the payload.
    expect(onUpdate).toHaveBeenCalledWith({
      lab_source: "claude_science_lab",
      lab_mode: "sole",
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
});
