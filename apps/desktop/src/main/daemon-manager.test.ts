// @vitest-environment node
import { describe, expect, it } from "vitest";
import { isAllowedTargetApiUrl, shouldAcceptCachedPat } from "./daemon-manager";

// 2026-07-03 self-heal regression test
//
// Symptom (before this fix): config.json had a cached `mul_…` PAT for a
// user that no longer existed in the database (PG restore after the
// v0.3.0 destructive-migration incident wiped the secondary user
// `e25b96e9` but kept the primary user `jyf`; config.json still
// referenced the dead user_id + dead PAT).
//
// The pre-fix syncToken() only re-minted when previousUserId mismatched
// the JWT's `sub`. After the restore, both happened to be `3f2d577f`
// (jyf), so no re-mint was triggered, and the daemon was handed the
// dead `e25b96e9` PAT. The user-visible result was the
// "agent's runtime is offline" banner with daemon log spam
// (`auth token rejected by server`).
//
// Fix: re-mint whenever the cached PAT is rejected by /api/me, even if
// user_id matches. The decision is captured in shouldAcceptCachedPat —
// tested here so a future refactor cannot regress this path silently.
describe("shouldAcceptCachedPat — 2026-07-03 self-heal", () => {
  it("accepts cached PAT when user unchanged and server says 'ok'", () => {
    expect(
      shouldAcceptCachedPat({
        cachedPatLooksValid: true,
        userChanged: false,
        probe: "ok",
      }),
    ).toBe(true);
  });

  it("REJECTS cached PAT when server says 'auth_expired' even if user unchanged (the 2026-07-03 bug)", () => {
    expect(
      shouldAcceptCachedPat({
        cachedPatLooksValid: true,
        userChanged: false,
        probe: "auth_expired",
      }),
    ).toBe(false);
  });

  it("REJECTS cached PAT when probe is 'unknown' (network error) — fail safe", () => {
    expect(
      shouldAcceptCachedPat({
        cachedPatLooksValid: true,
        userChanged: false,
        probe: "unknown",
      }),
    ).toBe(false);
  });

  it("REJECTS cached PAT when userChanged=true (always re-mint on user switch)", () => {
    expect(
      shouldAcceptCachedPat({
        cachedPatLooksValid: true,
        userChanged: true,
        probe: "ok",
      }),
    ).toBe(false);
  });

  it("REJECTS when no cached PAT exists", () => {
    expect(
      shouldAcceptCachedPat({
        cachedPatLooksValid: false,
        userChanged: false,
        probe: "ok",
      }),
    ).toBe(false);
  });
});

// F-027: daemon:set-target-api-url allowlist. The target API URL drives
// daemon auth + token minting, so only loopback / private LAN http(s) URLs
// may be set; everything else is rejected.
describe("isAllowedTargetApiUrl — F-027 allowlist", () => {
  it("allows loopback + private LAN http(s) URLs", () => {
    expect(isAllowedTargetApiUrl("http://127.0.0.1:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("http://localhost:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("http://[::1]:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("http://10.0.0.5:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("http://172.16.3.4:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("http://192.168.1.20:8090")).toBe(true);
    expect(isAllowedTargetApiUrl("https://127.0.0.1:8443")).toBe(true);
    expect(isAllowedTargetApiUrl("http://[::ffff:127.0.0.1]:8090")).toBe(true);
  });

  it("allows null / empty to clear the override", () => {
    expect(isAllowedTargetApiUrl(null)).toBe(true);
    expect(isAllowedTargetApiUrl("")).toBe(true);
    expect(isAllowedTargetApiUrl(undefined)).toBe(true);
  });

  it("rejects public hosts, wildcard binds, and non-http schemes", () => {
    expect(isAllowedTargetApiUrl("http://example.com")).toBe(false);
    expect(isAllowedTargetApiUrl("http://8.8.8.8")).toBe(false);
    expect(isAllowedTargetApiUrl("http://0.0.0.0:8090")).toBe(false);
    expect(isAllowedTargetApiUrl("http://169.254.169.254")).toBe(false);
    expect(isAllowedTargetApiUrl("file:///etc/passwd")).toBe(false);
    expect(isAllowedTargetApiUrl("ftp://127.0.0.1")).toBe(false);
    expect(isAllowedTargetApiUrl("not a url")).toBe(false);
    expect(isAllowedTargetApiUrl("http://")).toBe(false);
  });
});
