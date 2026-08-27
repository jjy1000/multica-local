import { describe, expect, it, beforeEach } from "vitest";
import {
  suppressNextWorkspaceRelease,
  consumeWorkspaceReleaseSuppression,
  resetWorkspaceReleaseSuppression,
} from "./workspace-singleton-release-guard";

describe("workspace-singleton-release-guard", () => {
  beforeEach(() => {
    resetWorkspaceReleaseSuppression();
  });

  it("returns false when nothing was armed", () => {
    expect(consumeWorkspaceReleaseSuppression()).toBe(false);
  });

  it("consumes one armed suppression per call", () => {
    suppressNextWorkspaceRelease();
    expect(consumeWorkspaceReleaseSuppression()).toBe(true);
    // Second teardown in the same navigation batch must release normally.
    expect(consumeWorkspaceReleaseSuppression()).toBe(false);
  });

  it("stacks multiple armings so double dispatch cannot strand half a handshake", () => {
    suppressNextWorkspaceRelease();
    suppressNextWorkspaceRelease();
    expect(consumeWorkspaceReleaseSuppression()).toBe(true);
    expect(consumeWorkspaceReleaseSuppression()).toBe(true);
    expect(consumeWorkspaceReleaseSuppression()).toBe(false);
  });

  it("reset clears pending tokens", () => {
    suppressNextWorkspaceRelease();
    resetWorkspaceReleaseSuppression();
    expect(consumeWorkspaceReleaseSuppression()).toBe(false);
  });
});
