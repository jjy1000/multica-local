// @vitest-environment node
// 0.5.67 audit regression pin — F-027 extension to upstream-registry.
//
// Pre-fix: apiBaseURL() in upstream-registry.ts trusted any URL from
// ~/.multica/desktop.json or MULTICA_API_URL env. Combined with the
// Bearer header attach in registerExperimentalUpstream /
// unregisterExperimentalUpstream (0.5.61 fix fd296ad8e), a user
// pointing desktop.json at `https://attacker.example.com` would
// silently POST their full Multica auth token to the attacker.
// F-027 (closed 0.5.18) already enforced the allowlist on
// `daemon:set-target-api-url` IPC; this test pins that the same
// gate is applied to the upstream-registry IPC.
//
// The allowlist accepts only loopback (localhost / 127.0.0.1 / ::1)
// + private LAN IPv4 (10/8, 172.16/12, 192.168/16) over http(s).
// file://, 0.0.0.0, and public IPs are rejected.

import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

// Stub electron `app` to satisfy the module-level reference.
// The registry's app.isPackaged is the only field read; we don't
// hit resolveResourcePath from these tests because we mock fetch.
vi.mock("electron", () => ({
  app: { isPackaged: false },
}));

let homeDir = "";
let savedEnv: string | undefined;

// Stub node:os and node:fs/promises BEFORE importing the SUT.
// The SUT calls `await import("node:os")` and `await import("node:fs/promises")`
// dynamically — but the resolution happens once per process. We can't
// intercept the dynamic import path without module-level mocking, so
// instead we set HOME + MULTICA_API_URL via env, write a real desktop.json,
// and let the production paths run end-to-end. That's a heavier test
// but it's the only way to exercise the production code without
// pulling the SUT apart.
beforeEach(async () => {
  homeDir = mkdtempSync(join(tmpdir(), "upstream-registry-test-"));
  mkdirSync(join(homeDir, ".multica"), { recursive: true });
  savedEnv = process.env["MULTICA_API_URL"];
  process.env["MULTICA_API_URL"] = "";
  // No desktop.json in this tmp HOME → apiBaseURL falls through to
  // the safe default. We write one per-test as needed.
});

afterEach(() => {
  process.env["MULTICA_API_URL"] = savedEnv;
  rmSync(homeDir, { recursive: true, force: true });
});

describe("upstream-registry URL allowlist (F-027 extension)", () => {
  it("falls back to http://localhost:8090 when desktop.json is absent", async () => {
    delete process.env["MULTICA_API_URL"];
    // Re-import the SUT module to reset its cached state.
    vi.resetModules();
    const mod = await import("./upstream-registry");
    // Stub fetch — we only care that apiBaseURL resolved correctly.
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await mod.registerExperimentalUpstream("pythia_oracle", "http://127.0.0.1:9999");
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const calledUrl = (fetchMock.mock.calls[0] as unknown as [string])[0];
    expect(calledUrl.startsWith("http://localhost:8090/")).toBe(true);
    vi.unstubAllGlobals();
  });

  it("rejects a public https URL in desktop.json and falls back to localhost", async () => {
    writeFileSync(
      join(homeDir, ".multica", "desktop.json"),
      JSON.stringify({ apiUrl: "https://attacker.example.com" }),
    );
    const origHome = process.env["HOME"];
    process.env["HOME"] = homeDir;
    vi.resetModules();
    try {
      const mod = await import("./upstream-registry");
      const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
      vi.stubGlobal("fetch", fetchMock);
      await mod.registerExperimentalUpstream("pythia_oracle", "http://127.0.0.1:9999");
      const calledUrl = (fetchMock.mock.calls[0] as unknown as [string])[0];
      expect(calledUrl.startsWith("https://attacker.example.com")).toBe(false);
      expect(calledUrl.startsWith("http://localhost:8090/")).toBe(true);
    } finally {
      process.env["HOME"] = origHome;
      vi.unstubAllGlobals();
    }
  });

  it("accepts a private LAN URL in desktop.json", async () => {
    writeFileSync(
      join(homeDir, ".multica", "desktop.json"),
      JSON.stringify({ apiUrl: "http://10.0.1.42:8090" }),
    );
    const origHome = process.env["HOME"];
    process.env["HOME"] = homeDir;
    vi.resetModules();
    try {
      const mod = await import("./upstream-registry");
      const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
      vi.stubGlobal("fetch", fetchMock);
      await mod.registerExperimentalUpstream("pythia_oracle", "http://127.0.0.1:9999");
      const calledUrl = (fetchMock.mock.calls[0] as unknown as [string])[0];
      expect(calledUrl.startsWith("http://10.0.1.42:8090/")).toBe(true);
    } finally {
      process.env["HOME"] = origHome;
      vi.unstubAllGlobals();
    }
  });

  it("rejects MULTICA_API_URL when it points at a public host", async () => {
    process.env["MULTICA_API_URL"] = "https://attacker.example.com";
    vi.resetModules();
    const mod = await import("./upstream-registry");
    const fetchMock = vi.fn(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    await mod.registerExperimentalUpstream("pythia_oracle", "http://127.0.0.1:9999");
    const calledUrl = (fetchMock.mock.calls[0] as unknown as [string])[0];
    expect(calledUrl.startsWith("https://attacker.example.com")).toBe(false);
    expect(calledUrl.startsWith("http://localhost:8090/")).toBe(true);
    vi.unstubAllGlobals();
  });
});