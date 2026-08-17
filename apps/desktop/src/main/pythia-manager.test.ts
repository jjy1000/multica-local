// @vitest-environment node
// Tests for managerBootHint — the renderer-facing error mapper that
// turns PythiaManager.start() failures into actionable Chinese hints.

import { describe, expect, test } from "vitest";
import { managerBootHint } from "./pythia-manager";

describe("managerBootHint", () => {
  test("maps BINARY_NOT_BUNDLED to the vendor-path hint", () => {
    const err = Object.assign(new Error("nope"), {
      code: "BINARY_NOT_BUNDLED",
    });
    const hint = managerBootHint(err);
    expect(hint).toContain("apps/desktop/vendor/pythia-src/engine");
    expect(hint).toContain("bundle-cli");
  });

  test("maps ENOENT (missing python3) to a brew-install hint", () => {
    const err = Object.assign(new Error("spawn python3 ENOENT"), {
      code: "ENOENT",
    });
    const hint = managerBootHint(err);
    expect(hint).toContain("python3");
    expect(hint.toLowerCase()).toContain("brew");
  });

  test("maps EADDRINUSE to a port-collision hint", () => {
    const err = Object.assign(new Error("EADDRINUSE"), {
      code: "EADDRINUSE",
    });
    const hint = managerBootHint(err);
    expect(hint).toContain("端口");
  });

  test("falls back to err.message when code is unknown", () => {
    const hint = managerBootHint(new Error("boom"));
    expect(hint).toBe("boom");
  });

  test("returns generic fallback for non-Error values", () => {
    expect(managerBootHint(undefined)).toBe(
      "Pythia manager failed to start",
    );
    expect(managerBootHint("string only")).toBe(
      "Pythia manager failed to start",
    );
    expect(managerBootHint(null)).toBe("Pythia manager failed to start");
  });
});