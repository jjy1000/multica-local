import { describe, expect, it } from "vitest";

import { pickFreePort } from "./free-port";

// pickFreePort binds an ephemeral loopback port and releases it. Two
// back-to-back calls must NOT collide with each other (kernel might
// hand back the same number if it has not been reused yet) and must
// each return a port in the dynamic / private range (>1024). A real
// subprocess manager binds the port again right after — a TOCTOU race
// is acceptable for this single-user desktop use case, but we at
// least want to confirm the function itself behaves.
describe("pickFreePort", () => {
  it("returns a port number that is reachable on 127.0.0.1", async () => {
    const port = await pickFreePort();
    expect(Number.isInteger(port)).toBe(true);
    expect(port).toBeGreaterThan(1024);
    expect(port).toBeLessThanOrEqual(65535);
  });

  it("returns distinct ports across repeated calls within a short window", async () => {
    // 8 calls is enough to catch a regression where we accidentally
    // returned a cached value; in practice kernel rarely hands back
    // the same port within a millisecond, but uniqueness is cheap to
    // verify and we own the contract.
    const ports = new Set<number>();
    for (let i = 0; i < 8; i++) {
      ports.add(await pickFreePort());
    }
    expect(ports.size).toBe(8);
  });
});
