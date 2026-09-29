import { describe, expect, it } from "vitest";
import {
  DEFAULT_RUNTIME_SETTINGS_KEY,
  defaultRuntimeIdFromSettings,
  withDefaultRuntimeId,
  workspaceDefaultRuntimeId,
} from "./default";

describe("defaultRuntimeIdFromSettings", () => {
  it("reads a stored id", () => {
    expect(
      defaultRuntimeIdFromSettings({ default_runtime_id: "rt-1" }),
    ).toBe("rt-1");
  });

  it("returns null when unset, empty, or non-string", () => {
    expect(defaultRuntimeIdFromSettings({})).toBeNull();
    expect(defaultRuntimeIdFromSettings({ default_runtime_id: "" })).toBeNull();
    expect(
      defaultRuntimeIdFromSettings({ default_runtime_id: 42 }),
    ).toBeNull();
    expect(defaultRuntimeIdFromSettings(null)).toBeNull();
    expect(defaultRuntimeIdFromSettings(undefined)).toBeNull();
  });

  it("survives settings drift (other keys present, weird shapes)", () => {
    expect(
      defaultRuntimeIdFromSettings({
        theme: "dark",
        default_runtime_id: "rt-2",
        nested: { default_runtime_id: "nope" },
      }),
    ).toBe("rt-2");
    expect(defaultRuntimeIdFromSettings([] as never)).toBeNull();
  });
});

describe("withDefaultRuntimeId", () => {
  it("sets the id and preserves unknown keys", () => {
    const next = withDefaultRuntimeId({ theme: "dark" }, "rt-1");
    expect(next).toEqual({
      theme: "dark",
      [DEFAULT_RUNTIME_SETTINGS_KEY]: "rt-1",
    });
  });

  it("clearing removes the key instead of storing an empty string", () => {
    const next = withDefaultRuntimeId(
      { theme: "dark", [DEFAULT_RUNTIME_SETTINGS_KEY]: "rt-1" },
      null,
    );
    expect(next).toEqual({ theme: "dark" });
  });

  it("does not mutate the input and tolerates null settings", () => {
    const original = { [DEFAULT_RUNTIME_SETTINGS_KEY]: "rt-1" };
    const cleared = withDefaultRuntimeId(original, null);
    expect(original[DEFAULT_RUNTIME_SETTINGS_KEY]).toBe("rt-1");
    expect(cleared).toEqual({});
    expect(withDefaultRuntimeId(null, "rt-2")).toEqual({
      [DEFAULT_RUNTIME_SETTINGS_KEY]: "rt-2",
    });
  });
});

describe("workspaceDefaultRuntimeId", () => {
  it("reads through a workspace object", () => {
    expect(
      workspaceDefaultRuntimeId({
        settings: { default_runtime_id: "rt-3" },
      }),
    ).toBe("rt-3");
    expect(workspaceDefaultRuntimeId(null)).toBeNull();
  });
});
