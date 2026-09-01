// @vitest-environment node
// Regression test for the P1 main-process crash fix (0.3.9).
//
// Before this fix, `BaseExperimentalManager.start()` called `spawn()`
// without first checking that the bundled binary exists. spawn() does
// not throw on ENOENT — it emits an asynchronous `'error'` event on
// the returned child object — so the error escaped to Node's default
// unhandled-rejection path. In Electron's main process that surfaced
// as a modal NSAlert and a "spawn ... ENOENT" stack trace that the
// user could not act on.
//
// This test pins the fix: when the binary path does not exist,
// `start()` must reject synchronously with a structured error whose
// `code === "BINARY_NOT_BUNDLED"`, BEFORE any IPC round-trip cost.
//
// We run against the real `BaseExperimentalManager` so the test catches
// any future regression in the spawn() ordering. We do NOT need a real
// binary on disk — the manager should refuse to spawn at all.

import { describe, expect, it, vi } from "vitest";
import { chmodSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// `app` from electron is read by `resolveResourcePath`. We stub the
// bits the manager uses so the test does not require an Electron
// runtime. `app.isPackaged` is false so `resolveResourcePath` falls
// through to `app.getAppPath() + /resources/...` — that is the dev
// path. We then point getAppPath() at a temp dir that holds no binary.
vi.mock("electron", () => ({
  app: {
    isPackaged: false,
    getAppPath: () => (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__,
  },
}));

import { BaseExperimentalManager } from "./manager-template";

describe("BaseExperimentalManager BINARY_NOT_BUNDLED pre-check", () => {
  it("REGRESSION GUARD: rejects with structured error when binary path is absent", async () => {
    const tmp = mkdtempSync(join(tmpdir(), "experimental-mgr-test-"));
    try {
      (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
      // No binary inside resources/openscience/ on purpose.

      const manager = new BaseExperimentalManager({
        name: "test-claude-science",
        resourceSubdir: "openscience",
        binName: "openscience",
        args: ["web", "--port"],
        readyTimeoutMs: 5_000,
      });

      let caught: Error & { code?: string } | null = null;
      try {
        await manager.start();
      } catch (err) {
        caught = err as Error & { code?: string };
      }

      expect(caught).not.toBeNull();
      expect(caught?.code).toBe("BINARY_NOT_BUNDLED");
      expect(caught?.message).toMatch(/openscience/);
      expect(caught?.message).toMatch(/not bundled/i);
      expect(manager.status()).toBe("error");
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("REGRESSION GUARD: does not call pickFreePort when binary is missing", async () => {
    const tmp = mkdtempSync(join(tmpdir(), "experimental-mgr-no-port-"));
    try {
      (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;

      const manager = new BaseExperimentalManager({
        name: "test-pythia",
        resourceSubdir: "pythia",
        binName: "run.sh",
        args: [],
        readyTimeoutMs: 5_000,
      });

      await expect(manager.start()).rejects.toMatchObject({ code: "BINARY_NOT_BUNDLED" });

      // If the pre-check fired, url() must still return null — never
      // an allocated port that was never bound.
      expect(manager.url()).toBeNull();
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });

  it("passes the existence check when binary IS staged (real readiness still depends on child)", async () => {
    const tmp = mkdtempSync(join(tmpdir(), "experimental-mgr-present-"));
    try {
      // Stage a binary so existsSync returns true. We don't let the
      // test go past the pre-check — the manager would try to actually
      // spawn this binary and start a health-probe loop, which is not
      // what we want in a unit test. We assert only the pre-check pass.
      const binDir = join(tmp, "resources", "openscience");
      mkdirSync(binDir, { recursive: true });
      writeFileSync(join(binDir, "openscience"), "#!/bin/sh\necho hi\n");
      chmodSync(join(binDir, "openscience"), 0o755);

      (globalThis as { __TEST_APP_PATH__?: string }).__TEST_APP_PATH__ = tmp;
      const manager = new BaseExperimentalManager({
        name: "test-present",
        resourceSubdir: "openscience",
        binName: "openscience",
        args: ["web", "--port"],
        readyTimeoutMs: 200,
      });

      // The pre-check should let start() through, then the manager
      // should fail at the health probe (the dummy binary does not
      // listen). The structured error code is "SERVICE_NEEDS_SETUP"
      // — the manager's contract is: spawn OK + health timeout →
      // the underlying service needs external setup. NOT "not bundled".
      // After the catch block calls stop(), status settles to "stopped".
      await expect(manager.start()).rejects.toMatchObject({
        code: "SERVICE_NEEDS_SETUP",
      });
      expect(manager.status()).toBe("stopped");
    } finally {
      rmSync(tmp, { recursive: true, force: true });
    }
  });
});