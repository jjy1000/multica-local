// Tests for the generic manifest-driven subprocess manager (0.3.25).
//
// resolveGenericSubprocessManager reads
// resources/experiments/<flagKey>/manifest.json, pulls spec.runtime,
// and builds a BaseExperimentalManager so subprocess flags that need
// no bespoke env (e.g. code_canvas) spawn without per-flag TypeScript.
// These tests pin the manifest parsing + graceful-degradation contract
// without spawning any real process.

import { describe, expect, it, vi } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `app` from electron is read by resolveResourcePath. Stub the bits
// the manager uses so the test runs without an Electron runtime.
// isPackaged=false → resolveResourcePath uses app.getAppPath()+/resources.
vi.mock("electron", () => ({
  app: {
    isPackaged: false,
    getAppPath: () => (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__,
  },
}));

import { resolveGenericSubprocessManager } from "./subprocess-manager";

function stageManifest(appPath: string, flagKey: string, manifest: unknown): void {
  const dir = join(appPath, "resources", "experiments", flagKey);
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, "manifest.json"), JSON.stringify(manifest), "utf-8");
}

describe("resolveGenericSubprocessManager", () => {
  it("builds a manager from a valid subprocess manifest", () => {
    const tmp = mkdtempSync(join(tmpdir(), "generic-mgr-ok-"));
    (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
    try {
      stageManifest(tmp, "code_canvas_ok", {
        spec: {
          runtime: {
            kind: "subprocess",
            binary: "code-canvas/run.sh",
            args: [],
            health_path: "/health",
            ready_timeout_ms: 10000,
            stop_grace_ms: 5000,
          },
          surface: { loopback_service: "code_canvas_ok" },
        },
      });
      const m = resolveGenericSubprocessManager("code_canvas_ok");
      expect(m).not.toBeNull();
      expect(m?.name).toBe("code_canvas_ok");
      // Not started yet → idle, no URL.
      expect(m?.status()).toBe("idle");
      expect(m?.url()).toBeNull();
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("caches the manager instance per flag", () => {
    const tmp = mkdtempSync(join(tmpdir(), "generic-mgr-cache-"));
    (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
    try {
      stageManifest(tmp, "cache_flag", {
        spec: { runtime: { kind: "subprocess", binary: "x/run.sh" } },
      });
      const a = resolveGenericSubprocessManager("cache_flag");
      const b = resolveGenericSubprocessManager("cache_flag");
      expect(a).toBe(b);
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("returns null when the manifest is missing", () => {
    const tmp = mkdtempSync(join(tmpdir(), "generic-mgr-missing-"));
    (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
    try {
      expect(resolveGenericSubprocessManager("no_such_flag")).toBeNull();
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("returns null when runtime kind is not subprocess", () => {
    const tmp = mkdtempSync(join(tmpdir(), "generic-mgr-kind-"));
    (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
    try {
      stageManifest(tmp, "inline_flag", {
        spec: { runtime: { kind: "inline" } },
      });
      expect(resolveGenericSubprocessManager("inline_flag")).toBeNull();
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("returns null when the binary has no subdir prefix", () => {
    const tmp = mkdtempSync(join(tmpdir(), "generic-mgr-nobin-"));
    (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
    try {
      stageManifest(tmp, "flat_binary", {
        spec: { runtime: { kind: "subprocess", binary: "run.sh" } },
      });
      expect(resolveGenericSubprocessManager("flat_binary")).toBeNull();
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});
