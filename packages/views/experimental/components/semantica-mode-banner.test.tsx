// SemanticaModeBanner.test.tsx (0.5.57 P5)
//
// Unit tests for the presentational ModeBanner. The wrapper
// deliberately supplies the full `en/experimental.json` resource
// block via I18nProvider so the arrow-expression selectors on
// `$.semantica.mode.{individual|team}` resolve to the actual
// translation strings — without that, vitest's default i18next
// stub returns the empty string and the assertion breaks.

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import enExperimental from "../../locales/en/experimental.json";
import { SemanticaModeBanner } from "./semantica-mode-banner";

function Wrapper({ children }: { children: React.ReactNode }) {
  // Minimal provider — the banner does not touch the network or any
  // store, so I18nProvider is the only wrapper we need. No
  // QueryClientProvider (no api calls); no WorkspaceProvider (no
  // workspace-bound queries).
  return (
    <I18nProvider locale="en" resources={{ en: { experimental: enExperimental } }}>
      {children}
    </I18nProvider>
  );
}

// Lazy import: keep the I18nProvider symbol out of the top-level
// import list so vitest's tree-shake doesn't choke on it.
import { I18nProvider } from "@multica/core/i18n/react";

describe("SemanticaModeBanner", () => {
  it("uses status role + polite aria-live for team mode", () => {
    const { container } = render(<SemanticaModeBanner mode="team" />, { wrapper: Wrapper });
    const banner = container.querySelector("[data-semantica-mode='team']");
    expect(banner).not.toBeNull();
    expect(banner?.getAttribute("role")).toBe("status");
    expect(banner?.getAttribute("aria-live")).toBe("polite");
  });

  it("uses note role + off aria-live for individual mode", () => {
    const { container } = render(<SemanticaModeBanner mode="individual" />, { wrapper: Wrapper });
    const banner = container.querySelector("[data-semantica-mode='individual']");
    expect(banner).not.toBeNull();
    expect(banner?.getAttribute("role")).toBe("note");
    expect(banner?.getAttribute("aria-live")).toBe("off");
  });

  it("renders the label + description in en (team workspace)", () => {
    render(<SemanticaModeBanner mode="team" />, { wrapper: Wrapper });
    expect(screen.getByText(/Team workspace/i)).toBeTruthy();
    expect(screen.getByText(/workspace members can see/i)).toBeTruthy();
  });

  it("renders the individual-mode label in en", () => {
    render(<SemanticaModeBanner mode="individual" />, { wrapper: Wrapper });
    expect(screen.getByText(/Individual workspace/i)).toBeTruthy();
  });

  it("forwards an optional className", () => {
    const { container } = render(
      <SemanticaModeBanner mode="individual" className="custom-class" />,
      { wrapper: Wrapper },
    );
    expect(container.querySelector(".custom-class")).not.toBeNull();
  });
});