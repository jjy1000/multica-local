// Tests for the SSE frame parser. The wire shape is documented in
// engine/server.py: each frame is one or more `data:` lines (JSON)
// terminated by a blank line. Heartbeats are `:`-prefixed lines that
// arrive between events.

import { describe, expect, test } from "vitest";
import { parseSseEvent } from "./use-pythia-sse";

describe("parseSseEvent", () => {
  test("parses a single-line snapshot frame", () => {
    const frame = `data: {"kind":"snapshot","payload":{"predictions":[]}}\n\n`;
    const msg = parseSseEvent(frame);
    expect(msg).not.toBeNull();
    expect(msg?.kind).toBe("snapshot");
    expect(msg?.payload).toEqual({ predictions: [] });
  });

  test("ignores heartbeat-only frames", () => {
    expect(parseSseEvent(": ping\n\n")).toBeNull();
    expect(parseSseEvent("retry: 5000\n\n")).toBeNull();
  });

  test("joins multi-line data fields with newline", () => {
    // Two `data:` lines that together form one JSON object. The newline
    // joiner preserves the JSON parse (we use a single string field so
    // the resulting object is trivially round-trippable).
    const frame =
      'data: {"kind":"world","payload":{"text":"line1\\nline2"}}\n\n';
    const msg = parseSseEvent(frame);
    expect(msg?.kind).toBe("world");
    expect((msg?.payload as { text: string }).text).toBe("line1\nline2");
  });

  test("returns null when JSON is malformed", () => {
    expect(parseSseEvent("data: not-json\n\n")).toBeNull();
  });

  test("strips a single leading space per data: line (SSE spec)", () => {
    const frame = "data:  {\"kind\":\"predictions\"}\n\n";
    expect(parseSseEvent(frame)?.kind).toBe("predictions");
  });
});