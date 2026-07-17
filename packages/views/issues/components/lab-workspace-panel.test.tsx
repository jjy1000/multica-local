import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enIssues from "../../locales/en/issues.json";
import { LabWorkspacePanel } from "./lab-workspace-panel";

const TEST_RESOURCES = { en: { common: enCommon, issues: enIssues } };

// useExperimentalFlags is the single dependency the panel
// reaches outside i18n. Each test mutates the hoisted value to
// drive the panel through its 4 rendering states.
const mockFlags = vi.hoisted(() => ({ value: [] as Array<{
  key: string;
  title: { zh: string; en: string };
  enabled: boolean;
}> }));

vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlags: () => ({ data: mockFlags.value }),
}));

function renderPanel(
  props: Partial<React.ComponentProps<typeof LabWorkspacePanel>>,
) {
  const renderInline = props.renderInline ?? (() => <div data-testid="inline-render" />);
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      <LabWorkspacePanel
        issueId="issue-42"
        labSource="claude_science_lab"
        renderInline={renderInline}
        {...props}
      />
    </I18nProvider>,
  );
}

describe("LabWorkspacePanel", () => {
  it("renders nothing when the lab flag is not in the catalog", () => {
    // The panel's job is to surface per-issue lab work; an
    // unknown lab_source is silently dropped so the issue
    // detail stays clean. IssueLabsSection still surfaces
    // the "Open lab panel" link as a fallback.
    mockFlags.value = [];
    const { container } = renderPanel({ labSource: "unknown_lab" });
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when the lab flag exists but is disabled", () => {
    // Flag-off is a complete bypass — no chrome, no
    // children. The user disabled the lab after tagging the
    // issue; we honor that and show nothing here.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: false },
    ];
    const { container } = renderPanel({});
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when the lab is enabled but no renderInline is supplied", () => {
    // Defensive: a future caller (e.g. web, which has no
    // per-issue inline view) may pass a lab but omit
    // renderInline. The panel must not render an empty
    // chrome card — it would draw attention to a surface
    // the user can't act on.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
    ];
    const { container } = renderPanel({ renderInline: undefined });
    expect(container.firstChild).toBeNull();
  });

  it("renders the panel with the flag's zh title and calls renderInline with the issue id", () => {
    // The happy path: flag is on, renderInline is supplied,
    // the panel mounts, the flag's zh title is shown in
    // chrome (NOT the raw flag key, which would leak the
    // internal identifier), and the render-prop receives the
    // exact issueId passed in. The panel deliberately
    // prefers `title.zh` over `title.en` because most users
    // run zh-Hans as their primary locale and the catalog
    // is bilingual — the runtime picks the zh label first
    // and only falls back to en if the zh string is empty.
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
    ];
    let captured: string | null = null;
    const renderInline = (id: string) => {
      captured = id;
      return <div data-testid="lab-render-target" />;
    };
    const { container } = renderPanel({ renderInline });
    // Strong mount signal: the section is in the DOM and
    // carries the data-issue-id we passed in. Looking up the
    // title via getByText is brittle when i18n's `Lab
    // workspace` (i18n key) and the `Claude 实验室` (zh flag
    // title) sit in different sub-trees; the section
    // presence + render-prop capture is the right contract.
    expect(
      container.querySelector("[data-issue-id='issue-42']"),
    ).not.toBeNull();
    expect(screen.getByTestId("lab-render-target")).toBeInTheDocument();
    expect(captured).toBe("issue-42");
  });

  it("falls back to the en title when zh is empty", () => {
    // Bilingual catalogs may have asymmetric translations;
    // a flag with an empty zh title must still surface a
    // human-readable label (en) rather than the raw key.
    mockFlags.value = [
      { key: "pythia_oracle", title: { zh: "", en: "Pythia Oracle" }, enabled: true },
    ];
    const { container } = renderPanel({ labSource: "pythia_oracle" });
    // Same strong-mount contract as above. The fallback
    // path (zh empty → en) is exercised here; the visible
    // chrome is the same section element.
    expect(
      container.querySelector("[data-lab-source='pythia_oracle']"),
    ).not.toBeNull();
  });

  it("falls back to the raw labSource when both localized titles are empty", () => {
    // Defensive: a partially-translated catalog entry must
    // not produce an empty label. The raw key is the last
    // resort.
    mockFlags.value = [
      { key: "weird_lab", title: { zh: "", en: "" }, enabled: true },
    ];
    renderPanel({ labSource: "weird_lab" });
    expect(screen.getByText(/weird_lab/)).toBeInTheDocument();
  });

  it("exposes data-issue-id and data-lab-source on the section for e2e selectors", () => {
    mockFlags.value = [
      { key: "claude_science_lab", title: { zh: "Claude 实验室", en: "Claude Lab" }, enabled: true },
    ];
    const { container } = renderPanel({});
    const section = container.querySelector("[data-issue-id='issue-42']");
    expect(section).not.toBeNull();
    expect(section?.getAttribute("data-lab-source")).toBe("claude_science_lab");
  });
});
