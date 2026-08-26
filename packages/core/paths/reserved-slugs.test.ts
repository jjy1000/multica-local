// @vitest-environment node
import { describe, expect, it } from "vitest";
import { RESERVED_SLUGS, isReservedSlug } from "./reserved-slugs";

describe("reserved slugs", () => {
  it("returns true for a known reserved slug", () => {
    expect(isReservedSlug("login")).toBe(true);
  });

  it("returns false for an unreserved slug", () => {
    expect(isReservedSlug("my-cool-workspace")).toBe(false);
  });

  it("returns false for an empty slug", () => {
    expect(isReservedSlug("")).toBe(false);
  });

  it("exposes a non-empty reserved slug set", () => {
    expect(RESERVED_SLUGS.size).toBeGreaterThan(0);
  });

  it("keeps the set and predicate consistent", () => {
    for (const slug of RESERVED_SLUGS) {
      expect(isReservedSlug(slug)).toBe(true);
    }
  });

  it("matches slugs case-sensitively", () => {
    expect(isReservedSlug("Login")).toBe(false);
  });

  // 0.5.72 regression pin: pre-workspace lab route namespaces
  // (`/experimental/*`, `/experimental/plugin/:slug`) must be reserved so
  // extractWorkspaceSlug() returns null for those paths. Without this,
  // clicking an experimental sidebar item routes through
  // tryRouteToOtherWorkspace → switchWorkspace("experimental", path) → a
  // brand-new tab group keyed by "experimental" — sidebar context lost,
  // AppSidebar hidden, lab view renders full-window and collapses the
  // function bar.
  it("reserves the pre-workspace lab route namespaces", () => {
    expect(isReservedSlug("experimental")).toBe(true);
    expect(isReservedSlug("plugin")).toBe(true);
  });
});
