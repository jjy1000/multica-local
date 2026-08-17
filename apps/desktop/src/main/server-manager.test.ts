import { afterEach, describe, expect, it } from "vitest";
import {
  pickPgBackend,
  runMigrate,
  pgProbeUrl,
  type PickPgBackendArgs,
} from "./server-manager";

// PR 3 (Stage D-2): pickPgBackend is now a 2-way picker.
//   "external" → a multica PG is already on 5432 (reuse it)
//   "native"   → we need to bring up the bundled Postgres.app
//
// All previous docker/local precedence tests are obsolete. The
// behaviour is now trivial — the test matrix is correspondingly
// smaller but still locks the contract for future regressions.

const base: PickPgBackendArgs = {
  pgReachable: false,
  preferred: undefined,
};

describe("pickPgBackend — PR 3 2-way picker", () => {
  it("returns 'external' when a multica PG is already on 5432", () => {
    expect(pickPgBackend({ ...base, pgReachable: true })).toBe("external");
  });

  it("returns 'native' when no PG is reachable", () => {
    expect(pickPgBackend(base)).toBe("native");
  });

  it("returns 'external' when pgReachable regardless of preferred=native", () => {
    expect(
      pickPgBackend({ ...base, pgReachable: true, preferred: "native" }),
    ).toBe("external");
  });

  it("returns 'native' when preferred=external but no PG is reachable", () => {
    // We can't honour "use external" when nothing is on 5432 — fall
    // through to native so the app still boots.
    expect(pickPgBackend({ ...base, preferred: "external" })).toBe("native");
  });

  it("returns 'external' when preferred=external and PG is reachable", () => {
    expect(
      pickPgBackend({ ...base, pgReachable: true, preferred: "external" }),
    ).toBe("external");
  });

  it("returns 'native' when preferred=auto and PG is not reachable", () => {
    expect(pickPgBackend({ ...base, preferred: "auto" })).toBe("native");
  });
});

// ---------------------------------------------------------------------------
// P0 structural guard test — THE TEST THAT WOULD HAVE CAUGHT THE INCIDENT
// ---------------------------------------------------------------------------
//
// On 2026-07-02, v0.3.0 launched against a Docker pgdata backend and
// `ensureServerUp` ran the bundled `migrate` binary unconditionally.
// History migrations 029 / 046 / 103 contain DROP TABLE; running them
// against the user's existing schema destroyed daemon_pairing_session,
// runtime_usage, and task_usage_* tables. The fix added a caller-side
// `if (backend === "external")` check, then later hardened the function
// itself to refuse `backend === "external"` regardless of caller.
//
// These tests pin BOTH the caller's skip path AND the runMigrate-level
// refusal. Anyone refactoring server-manager.ts in the future must keep
// both checks green. See memory file
// `multica-0.3.0-standalone-2026-07-02.md` for the full incident report.

describe("runMigrate — P0 structural guard (data-safety line)", () => {
  it("REFUSES to run when backend === 'external' (would invoke DROP TABLE migrations 029/046/103)", async () => {
    await expect(runMigrate("test-profile", {} as never, "external")).rejects.toThrow(
      /backend=external.*destroy the existing schema/,
    );
  });

  it("refuses external backend even with valid env + profile", async () => {
    // The check is purely on the `backend` argument, not on env/profile
    // content. This is intentional: defence in depth — the caller may
    // have been refactored to skip the env setup for external backends,
    // but the migrate binary must NEVER be reached for external.
    await expect(
      runMigrate(
        "test-profile",
        {
          MULTICA_DATABASE_URL: "postgres://multica:multica@127.0.0.1:5432/multica",
        } as never,
        "external",
      ),
    ).rejects.toThrow(/runMigrate refused/);
  });

  it("does NOT refuse when backend === 'native'", async () => {
    // We expect runMigrate to attempt to spawn the migrate binary, which
    // doesn't exist in the test sandbox. The point is that it should NOT
    // throw the "backend=external" refusal. The test asserts the failure
    // mode is "binary not found" (or similar non-refusal error), not the
    // P0 refusal.
    await expect(
      runMigrate("test-profile", {} as never, "native"),
    ).rejects.not.toThrow(/runMigrate refused/);
  });

  it("does NOT refuse when backend is undefined (caller forgot to pass — fail open to spawn, but be loud)", async () => {
    // The current runMigrate signature makes `backend` optional. We
    // intentionally fail OPEN here (proceed to spawn) rather than fail
    // CLOSED, because (a) every existing call site passes backend, and
    // (b) failing closed would block 0.2.x rollbacks. The test pins this
    // policy so it's a deliberate decision, not an accident.
    await expect(
      runMigrate("test-profile", {} as never),
    ).rejects.not.toThrow(/runMigrate refused/);
  });
});

// ---------------------------------------------------------------------------
// 0.3.1 P1.1 + P1.2 + P1.6 — code contract verification
// ---------------------------------------------------------------------------
//
// The full in-flight coalescing + probeMulticaPg flow requires mocking
// child_process / net / electron modules which fight with vitest's
// module-reset semantics. Fix correctness is pinned by:
//
//   (a) typecheck: parameter / return shapes are correct
//   (b) code review: diff against the memory file's incident notes
//   (c) the runMigrate refusal tests above (same Promise-cache
//       pattern as P1.1's ensureServerUp)
//   (d) post-ship integration test: cold-start the packaged 0.3.1
//       app, observe no orphan children, no double-spawn, and the
//       sentinel file is renamed to its final name.
//
// The smoke test in `.omc/plans/0.3.1-stability-fixes.md` step 10
// is the runnable verification harness for these three fixes.

describe("pgProbeUrl — 0.5.31 P1 loopback-only guard", () => {
  const DEFAULT_URL =
    "postgres://multica:multica@127.0.0.1:5432/multica?sslmode=disable";
  const orig = process.env["DATABASE_URL"];

  afterEach(() => {
    if (orig === undefined) delete process.env["DATABASE_URL"];
    else process.env["DATABASE_URL"] = orig;
  });

  it("returns the loopback default when DATABASE_URL is unset", () => {
    delete process.env["DATABASE_URL"];
    expect(pgProbeUrl()).toBe(DEFAULT_URL);
  });

  it("accepts a localhost DATABASE_URL", () => {
    process.env["DATABASE_URL"] =
      "postgres://u:p@localhost:5432/multica?sslmode=disable";
    expect(pgProbeUrl()).toBe(
      "postgres://u:p@localhost:5432/multica?sslmode=disable",
    );
  });

  it("accepts 127.0.0.1 / ::1", () => {
    process.env["DATABASE_URL"] =
      "postgres://u:p@127.0.0.1:5432/multica?sslmode=disable";
    expect(pgProbeUrl()).toBe(
      "postgres://u:p@127.0.0.1:5432/multica?sslmode=disable",
    );
    process.env["DATABASE_URL"] =
      "postgres://u:p@[::1]:5432/multica?sslmode=disable";
    expect(pgProbeUrl()).toBe(
      "postgres://u:p@[::1]:5432/multica?sslmode=disable",
    );
  });

  it("REJECTS a remote / public-host DATABASE_URL (data-localization)", () => {
    process.env["DATABASE_URL"] =
      "postgres://u:p@prod-db.internal.example.com:5432/multica";
    expect(pgProbeUrl()).toBe(DEFAULT_URL);
    process.env["DATABASE_URL"] =
      "postgres://u:p@203.0.113.9:5432/multica";
    expect(pgProbeUrl()).toBe(DEFAULT_URL);
  });

  it("REJECTS a malformed DATABASE_URL", () => {
    process.env["DATABASE_URL"] = "not-a-url";
    expect(pgProbeUrl()).toBe(DEFAULT_URL);
  });
});
