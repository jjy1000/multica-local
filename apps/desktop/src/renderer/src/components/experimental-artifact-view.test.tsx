import { describe, expect, it, vi } from "vitest";
import { render } from "@testing-library/react";
import enClaudeLab from "@multica/views/locales/en/claude-lab.json";

// Mock the experimental flags hook so the tests can pin the flag
// without going through the catalog HTTP call.
const flagState = vi.hoisted(() => ({ on: false as boolean }));
vi.mock("@multica/core/experimental", () => ({
  useExperimentalFlag: (_key: string, _dflt = false) => flagState.on,
}));

// Mock @tanstack/react-query's useQueryClient so the component
// doesn't crash on import.
vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: () => undefined }),
  useQuery: () => ({ data: undefined, isLoading: false, isError: false }),
  useMutation: () => ({ mutate: () => undefined, isPending: false }),
}));

// 0.5.131: the view's strings moved from hardcoded Chinese to the
// claude-lab i18n namespace — pin the header against the en locale
// instead of the old literal.
vi.mock("@multica/views/i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown) => {
      const v = sel(enClaudeLab);
      return typeof v === "string" ? v : undefined;
    },
  }),
}));

import { ExperimentalArtifactView } from "./experimental-artifact-view";

describe("ExperimentalArtifactView", () => {
  it("renders nothing when the claude_science_lab flag is off", () => {
    flagState.on = false;
    const { container } = render(
      <ExperimentalArtifactView workspaceId="00000000-0000-0000-0000-000000000000" />,
    );
    expect(container.firstChild).toBeNull();
  });

  it("renders the localized header when the flag is on but no sessions exist", () => {
    flagState.on = true;
    const { container } = render(
      <ExperimentalArtifactView workspaceId="00000000-0000-0000-0000-000000000000" />,
    );
    // With useQuery mocked to return undefined, sessions.isLoading is
    // false and sessions.data is undefined; the component falls
    // through to the "loading…" branch and renders nothing visible
    // besides the header. We assert the localized header is present.
    expect(container.querySelector("h2")).toBeTruthy();
    expect(container.querySelector("h2")?.textContent).toBe(enClaudeLab.runtime_header);
  });
});
