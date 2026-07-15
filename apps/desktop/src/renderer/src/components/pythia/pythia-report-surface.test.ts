// PythiaReportSurface.test.tsx (0.3.29+)
//
// Light-weight unit coverage for the SSE-block parser that the
// report surface uses to convert /forecast/issue streaming output
// into the PythiaReportEnvelope shape. The component is lazy-loaded
// behind the pythia_oracle flag, so this test only exercises the
// pure parser — no DOM, no IPC, no manager.

import { describe, expect, it } from "vitest";
import { parsePredictionBlock } from "./pythia-report-surface";

// Minimal SSE envelope echoing the 0.3.29 shape with the new
// issue_id / lab_source / scenario_context fields populated by the
// /api/experimental/pythia-oracle/forecast/issue endpoint.
const sampleBlock =
  'event: prediction\n' +
  'data: {"id":"p_issue_1-ora_r1","issue_id":"11111111-1111-1111-1111-111111111111",' +
  '"scenario":"霍尔木兹海峡流量下降","narrative":"周环比下滑 15%",' +
  '"probability":0.42,"confidence":0.71,"horizon":"week","persona":"strategist",' +
  '"lab_source":"oracle","scenario_context":"霍尔木兹海峡流量下降\\n\\n过去 7 天...",' +
  '"createdAt":"2026-07-15T13:46:06Z"}\n\n';

describe("parsePredictionBlock", () => {
  it("extracts the bound envelope on a complete block", () => {
    const env = parsePredictionBlock(sampleBlock);
    expect(env).not.toBeNull();
    expect(env?.id).toBe("p_issue_1-ora_r1");
    expect(env?.issue_id).toBe("11111111-1111-1111-1111-111111111111");
    expect(env?.scenario).toBe("霍尔木兹海峡流量下降");
    expect(env?.probability).toBe(0.42);
    expect(env?.lab_source).toBe("oracle");
    expect(env?.scenario_context).toContain("霍尔木兹海峡流量下降");
  });

  it("returns null when the block hasn't closed yet", () => {
    // Truncate before the \n\n terminator.
    const partial = sampleBlock.slice(0, sampleBlock.length - 4);
    expect(parsePredictionBlock(partial)).toBeNull();
  });

  it("falls back to defaults when fields are missing", () => {
    const minimal =
      'event: prediction\n' +
      'data: {"id":"p_x","scenario":"x","probability":0.5}\n\n';
    const env = parsePredictionBlock(minimal);
    expect(env).not.toBeNull();
    expect(env?.horizon).toBe("week");
    expect(env?.persona).toBe("strategist");
    expect(env?.lab_source).toBe("synthetic");
  });

  it("tolerates trailing buffered data after the close", () => {
    const more = sampleBlock + ': keep-alive\n\n';
    const env = parsePredictionBlock(more);
    expect(env).not.toBeNull();
    expect(env?.id).toBe("p_issue_1-ora_r1");
  });
});
