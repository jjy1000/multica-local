import { describe, expect, test, beforeEach, afterEach } from "vitest";
import * as fs from "node:fs/promises";
import * as os from "node:os";
import * as path from "node:path";
import {
  loadBlacklist,
  saveBlacklist,
  isBroken,
  clearBroken,
  loadBrokenFlagKeys,
  setBlacklistPath,
  type Blacklist,
} from "./experimental-safety";

let tmpDir = "";
let blacklistPath = "";

beforeEach(async () => {
  tmpDir = await fs.mkdtemp(path.join(os.tmpdir(), "multica-safety-"));
  blacklistPath = path.join(tmpDir, "experimental-blacklist.json");
  setBlacklistPath(blacklistPath);
});

afterEach(async () => {
  await fs.rm(tmpDir, { recursive: true, force: true });
});

describe("loadBlacklist", () => {
  test("returns empty blacklist when file missing", async () => {
    const bl = await loadBlacklist();
    expect(bl).not.toBeNull();
    expect(bl!.entries).toEqual([]);
  });

  test("returns null on schema version mismatch", async () => {
    await fs.writeFile(
      blacklistPath,
      JSON.stringify({ version: 99, entries: [] }),
    );
    expect(await loadBlacklist()).toBeNull();
  });

  test("returns null on malformed JSON", async () => {
    await fs.writeFile(blacklistPath, "not json");
    expect(await loadBlacklist()).toBeNull();
  });

  test("roundtrips a saved blacklist", async () => {
    const inBl: Blacklist = {
      version: 1,
      entries: [
        {
          flag_key: "mythos_swarm",
          reason: "panic",
          broken_at: "2026-07-14T10:00:00Z",
          context: "main.go:142",
        },
      ],
    };
    await saveBlacklist(inBl);
    const out = await loadBlacklist();
    expect(out).toEqual(inBl);
  });
});

describe("isBroken + clearBroken", () => {
  test("isBroken returns entry when present", async () => {
    const bl: Blacklist = {
      version: 1,
      entries: [
        {
          flag_key: "claude_science_lab",
          reason: "5xx_burst",
          broken_at: "2026-07-14T10:00:00Z",
        },
      ],
    };
    await saveBlacklist(bl);
    const e = await isBroken("claude_science_lab");
    expect(e).not.toBeNull();
    expect(e!.reason).toBe("5xx_burst");
  });

  test("isBroken returns null when flag not in list", async () => {
    expect(await isBroken("nope")).toBeNull();
  });

  test("clearBroken removes entry", async () => {
    const bl: Blacklist = {
      version: 1,
      entries: [
        { flag_key: "a", reason: "panic", broken_at: "2026-07-14T10:00:00Z" },
        { flag_key: "b", reason: "init_timeout", broken_at: "2026-07-14T10:00:00Z" },
      ],
    };
    await saveBlacklist(bl);
    await clearBroken("a");
    const out = await loadBlacklist();
    expect(out!.entries.map((e) => e.flag_key)).toEqual(["b"]);
  });

  test("clearBroken is idempotent", async () => {
    await clearBroken("absent");
    expect(await loadBlacklist()).toEqual({ version: 1, entries: [] });
  });
});

describe("loadBrokenFlagKeys", () => {
  test("returns a Set of broken keys", async () => {
    const bl: Blacklist = {
      version: 1,
      entries: [
        { flag_key: "x", reason: "panic", broken_at: "2026-07-14T10:00:00Z" },
        { flag_key: "y", reason: "5xx_burst", broken_at: "2026-07-14T10:00:00Z" },
      ],
    };
    await saveBlacklist(bl);
    const keys = await loadBrokenFlagKeys();
    expect(keys.has("x")).toBe(true);
    expect(keys.has("y")).toBe(true);
    expect(keys.has("z")).toBe(false);
  });
});
