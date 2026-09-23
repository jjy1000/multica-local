import { describe, expect, it } from "vitest";
import {
  IDLE_PYTHIA_STREAM,
  parsePythiaSSEBlock,
  reducePythiaStreamEvent,
  type PythiaStreamEvent,
  type PythiaStreamState,
} from "./use-pythia-issue-lab";
import type { PythiaForecastEnvelope } from "@multica/core/types/api";

function envelope(i: number, probability = 0.5): PythiaForecastEnvelope {
  return {
    id: `env-${i}`,
    scenario: `scenario ${i}`,
    narrative: `narrative ${i}`,
    probability,
    confidence: 0.55,
    horizon: "week",
    persona: "strategist",
    lab_source: "oracle",
  };
}

function snapshot(id: string): PythiaStreamEvent {
  return {
    kind: "snapshot",
    run: {
      id,
      rounds: 6,
      status: "running",
      runKind: "continuation",
      variables: "把汇率冲击调到 20%",
      report: "",
    },
    envelopes: [envelope(0)],
  };
}

describe("reducePythiaStreamEvent", () => {
  it("snapshot seeds the state from run meta + persisted envelopes", () => {
    const s = reducePythiaStreamEvent(IDLE_PYTHIA_STREAM, snapshot("run-1"));
    expect(s.runId).toBe("run-1");
    expect(s.status).toBe("running");
    expect(s.runKind).toBe("continuation");
    expect(s.variables).toBe("把汇率冲击调到 20%");
    expect(s.totalRounds).toBe(6);
    expect(s.envelopes).toHaveLength(1);
  });

  it("round appends a new envelope in order", () => {
    let s: PythiaStreamState = reducePythiaStreamEvent(IDLE_PYTHIA_STREAM, snapshot("run-1"));
    s = reducePythiaStreamEvent(s, { kind: "round", index: 1, envelope: envelope(1, 0.6) });
    expect(s.envelopes).toHaveLength(2);
    expect(s.envelopes[1]?.probability).toBe(0.6);
  });

  it("round is idempotent by index — a replayed frame replaces, not duplicates", () => {
    // The server subscribes the stream BEFORE reading the snapshot, so a
    // round published in that window can arrive in BOTH the snapshot and
    // the channel. The reducer keys by index; this pins that contract.
    let s: PythiaStreamState = reducePythiaStreamEvent(IDLE_PYTHIA_STREAM, snapshot("run-1"));
    s = reducePythiaStreamEvent(s, { kind: "round", index: 0, envelope: envelope(0, 0.9) });
    expect(s.envelopes).toHaveLength(1);
    expect(s.envelopes[0]?.probability).toBe(0.9);
  });

  it("report + status fold in without touching envelopes", () => {
    let s: PythiaStreamState = reducePythiaStreamEvent(IDLE_PYTHIA_STREAM, snapshot("run-1"));
    s = reducePythiaStreamEvent(s, { kind: "report", report: "## 共识结论" });
    expect(s.report).toBe("## 共识结论");
    expect(s.envelopes).toHaveLength(1);
    s = reducePythiaStreamEvent(s, { kind: "status", status: "completed" });
    expect(s.status).toBe("completed");
    expect(s.report).toBe("## 共识结论");
  });
});

describe("parsePythiaSSEBlock", () => {
  it("parses a round frame", () => {
    const block = 'event: round\ndata: {"index":2,"id":"x","scenario":"s","narrative":"n","probability":0.7,"confidence":0.5,"horizon":"week","persona":"strategist","lab_source":"oracle"}';
    const ev = parsePythiaSSEBlock(block);
    expect(ev?.kind).toBe("round");
    if (ev?.kind === "round") {
      expect(ev.index).toBe(2);
      expect(ev.envelope.probability).toBe(0.7);
    }
  });

  it("parses snapshot with meta + envelopes", () => {
    const block =
      'event: snapshot\ndata: {"run":{"id":"r1","rounds":3,"status":"running","run_kind":"initial","variables":"","report":""},"envelopes":[]}';
    const ev = parsePythiaSSEBlock(block);
    expect(ev?.kind).toBe("snapshot");
    if (ev?.kind === "snapshot") {
      expect(ev.run.id).toBe("r1");
      expect(ev.run.runKind).toBe("initial");
      expect(ev.envelopes).toEqual([]);
    }
  });

  it("parses status and report frames", () => {
    expect(parsePythiaSSEBlock('event: status\ndata: {"status":"aborted"}')).toEqual({
      kind: "status",
      status: "aborted",
    });
    expect(parsePythiaSSEBlock('event: report\ndata: {"report":"# R"}')).toEqual({
      kind: "report",
      report: "# R",
    });
  });

  it("returns null for keep-alive comments and unknown events", () => {
    expect(parsePythiaSSEBlock(": ping")).toBeNull();
    expect(parsePythiaSSEBlock('event: wat\ndata: {"a":1}')).toBeNull();
    expect(parsePythiaSSEBlock("event: round\ndata: not-json")).toBeNull();
  });
});
