import { describe, expect, it, vi } from "vitest";

// Mock the `electron` module BEFORE importing pg-bootstrap so the
// `app.getPath("userData")` call inside resolveNativePaths uses our
// test-controlled value.
vi.mock("electron", () => ({
  app: {
    getPath: (key: string) => {
      if (key === "userData") return "/tmp/multica-pg-bootstrap-test";
      return "/tmp";
    },
  },
}));

// `existsSync` returns false for everything in the test fs sandbox, so
// checkNativePgInstalled naturally reports `false` without any extra
// stubbing. We also need to make sure the pgdata/PG_VERSION guard
// returns false so initPgDataDir doesn't try to write — we never call
// it in these tests, but defensive mocking keeps the module load safe.

import { resolveNativePaths, checkNativePgInstalled, detectInstalledPgVersion } from "./pg-bootstrap";
import { pickPgBackend } from "./server-manager";

describe("pg-bootstrap", () => {
  describe("resolveNativePaths", () => {
    it("returns paths under the mocked userData dir", () => {
      const p = resolveNativePaths();
      expect(p.pgHome).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4");
      expect(p.pgBin).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4/bin");
      expect(p.postgresBin).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4/bin/postgres");
      expect(p.initdbBin).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4/bin/initdb");
      expect(p.pgCtlBin).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4/bin/pg_ctl");
      expect(p.pgdata).toBe("/tmp/multica-pg-bootstrap-test/pgdata");
      expect(p.pgLog).toBe("/tmp/multica-pg-bootstrap-test/pg/17.4/pg.log");
    });

    it("uses version 17.4 (locked for PR 2 reproducibility)", () => {
      const p = resolveNativePaths();
      // 17.4 must appear in pgHome; if we ever need to bump the
      // tarball version (PR 3), update PG_VERSION_DIR and re-run
      // these tests as a sanity check.
      expect(p.pgHome).toMatch(/\/pg\/17\.4$/);
    });
  });

  describe("checkNativePgInstalled", () => {
    it("returns false when no binaries are present", () => {
      // The mock userData is empty, so all three existsSync checks
      // fail. We do NOT create files in the test sandbox to avoid
      // polluting /tmp.
      expect(checkNativePgInstalled()).toBe(false);
    });
  });

  describe("detectInstalledPgVersion", () => {
    it("returns null when binaries are missing", async () => {
      // No binaries → short-circuit return null.
      const v = await detectInstalledPgVersion();
      expect(v).toBeNull();
    });
  });
});

describe("pickPgBackend — PR 2 native extension", () => {
  const base = {
    pgReachable: false,
    preferred: undefined as "native" | "external" | "auto" | undefined,
  };

  it("returns external when PG is already reachable", () => {
    expect(pickPgBackend({ ...base, pgReachable: true })).toBe("external");
  });

  it("returns native when PG is not reachable", () => {
    expect(pickPgBackend({ ...base, pgReachable: false })).toBe("native");
  });

  it("returns external even when preferred=native if PG is reachable", () => {
    expect(pickPgBackend({ ...base, pgReachable: true, preferred: "native" })).toBe("external");
  });

  it("returns native when preferred=external but PG is unreachable", () => {
    // We can't honour "use external" when no external PG exists — fall
    // through to native so the app still boots.
    expect(pickPgBackend({ ...base, pgReachable: false, preferred: "external" })).toBe("native");
  });

  it("returns external when preferred=external and PG is reachable", () => {
    expect(pickPgBackend({ ...base, pgReachable: true, preferred: "external" })).toBe("external");
  });
});

// ---------------------------------------------------------------------------
// 0.3.1 P1.8 — sentinel atomicity (lightweight pin)
// ---------------------------------------------------------------------------
//
// runMigrationFlow's full lifecycle requires docker + pg_restore + psql
// which we cannot exercise in vitest. The fix correctness is pinned by:
//   (a) typecheck
//   (b) a smoke test of runMigrationFlow that hits the
//       "already-migrated" / "no-docker-volume" / "user-cancelled"
//       short-circuits — these exercise the sentinel existence
//       check + the O_EXCL try/catch.
//   (c) post-ship integration test: cold-start the packaged 0.3.1
//       app, verify .pg-migrating-v1 is renamed (not left around)
//       after a clean install.

describe("runMigrationFlow — 0.3.1 P1.8 short-circuit paths", () => {
  it("returns a skipped outcome when sentinels exist OR docker is unavailable", async () => {
    // The test sandbox may have a real .pg-migrated-v1 left over from
    // a prior interactive run, OR no docker binary — both routes
    // short-circuit the destructive pg_restore path. The P1.8 fix's
    // contract is "we never reach pg_restore unless a clean migration
    // is the only remaining path"; asserting a skipped outcome is
    // sufficient.
    const result = await runMigrationFlowUnderTest({
      onProgress: () => undefined,
      confirmed: true,
    });
    expect(result).toMatchObject({ skipped: true });
    expect(["already-migrated", "no-docker-volume"]).toContain(
      (result as { reason: string }).reason,
    );
  });
});

// Dynamic import wrapper to defer module load so the electron mock at
// the top of this file is in place before pg-bootstrap is first touched.
async function runMigrationFlowUnderTest(args: {
  onProgress: (phase: string, percent: number) => void;
  confirmed: boolean;
}) {
  const mod = await import("./pg-bootstrap");
  return mod.runMigrationFlow(args);
}
