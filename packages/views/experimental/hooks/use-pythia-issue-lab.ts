"use client";

// use-pythia-issue-lab — client state for the issue-bound Pythia lab
// (0.5.111). Owns three things:
//
//   1. the per-issue runs list (TanStack Query; 5s poll while a run is
//      live, 60s idle — Active Contract #1 cadence);
//   2. the live stream: when a run is running (freshly started, or
//      discovered from the runs list so a reload mid-run still follows),
//      the hook subscribes to GET .../runs/{id}/stream via
//      api.rawRequest + ReadableStream and folds every SSE frame through
//      ONE reducer (reducePythiaStreamEvent — the SocialSim
//      "single-switch state convergence" pattern, exported pure for
//      tests). Duplicate `round` frames are idempotent: the reducer keys
//      by envelope index, which is what makes the server's
//      subscribe-before-snapshot ordering safe.
//   3. start / cancel, wiring through the shared trigger util.

import { useCallback, useEffect, useRef, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, parseWithFallback } from "@multica/core/api";
import {
  PythiaForecastEnvelopeListSchema,
  PythiaForecastEnvelopeSchema,
  PythiaForecastRunListSchema,
} from "@multica/core/api/schemas";
import type {
  PythiaForecastEnvelope,
  PythiaForecastRun,
} from "@multica/core/types/api";
import {
  cancelPythiaIssueForecast,
  startPythiaIssueForecast,
} from "../../issues/utils/pythia-forecast-trigger";

export interface PythiaStreamState {
  runId: string | null;
  status: "idle" | "running" | "completed" | "aborted" | "failed";
  runKind: string;
  variables: string;
  /** Planned rounds (from the start payload or the snapshot meta). */
  totalRounds: number;
  envelopes: PythiaForecastEnvelope[];
  report: string;
}

export const IDLE_PYTHIA_STREAM: PythiaStreamState = {
  runId: null,
  status: "idle",
  runKind: "initial",
  variables: "",
  totalRounds: 0,
  envelopes: [],
  report: "",
};

export type PythiaStreamEvent =
  | {
      kind: "snapshot";
      run: {
        id: string;
        rounds: number;
        status: string;
        runKind: string;
        variables: string;
        report: string;
      };
      envelopes: PythiaForecastEnvelope[];
    }
  | { kind: "round"; index: number; envelope: PythiaForecastEnvelope }
  | { kind: "report"; report: string }
  | { kind: "status"; status: PythiaStreamState["status"] };

export function isPythiaTerminalStatus(status: string): boolean {
  return status === "completed" || status === "aborted" || status === "failed";
}

/** Fold one SSE frame into the stream state. PURE — unit-pinned. */
export function reducePythiaStreamEvent(
  state: PythiaStreamState,
  event: PythiaStreamEvent,
): PythiaStreamState {
  switch (event.kind) {
    case "snapshot":
      return {
        runId: event.run.id,
        status: (event.run.status as PythiaStreamState["status"]) || "running",
        runKind: event.run.runKind,
        variables: event.run.variables,
        totalRounds: event.run.rounds,
        envelopes: event.envelopes,
        report: event.run.report,
      };
    case "round": {
      // Idempotent by index: a frame the snapshot already replayed is a
      // replace, not a duplicate append.
      const envelopes = [...state.envelopes];
      if (event.index >= 0 && event.index < envelopes.length) {
        envelopes[event.index] = event.envelope;
      } else {
        envelopes.push(event.envelope);
      }
      return { ...state, envelopes };
    }
    case "report":
      return { ...state, report: event.report };
    case "status":
      return { ...state, status: event.status };
    default:
      return state;
  }
}

/** Parse one SSE block ("event: X\ndata: {...}") into a typed stream
 *  event. Returns null for keep-alive comments / unknown events. Pure. */
export function parsePythiaSSEBlock(block: string): PythiaStreamEvent | null {
  let event = "";
  let data = "";
  for (const line of block.split("\n")) {
    if (line.startsWith("event:")) event = line.slice(6).trim();
    else if (line.startsWith("data:")) data += line.slice(5).trim();
  }
  if (!event || !data) return null;
  try {
    const raw: unknown = JSON.parse(data);
    switch (event) {
      case "snapshot": {
        const obj = raw as Record<string, unknown>;
        const run = (obj.run ?? {}) as Record<string, unknown>;
        const envelopes = parseWithFallback<PythiaForecastEnvelope[]>(
          obj.envelopes ?? [],
          zArrayEnvelope,
          [],
          { endpoint: "pythia run stream snapshot" },
        );
        return {
          kind: "snapshot",
          run: {
            id: String(run.id ?? ""),
            rounds: Number(run.rounds ?? 0),
            status: String(run.status ?? "running"),
            runKind: String(run.run_kind ?? "initial"),
            variables: String(run.variables ?? ""),
            report: String(run.report ?? ""),
          },
          envelopes,
        };
      }
      case "round": {
        const envelope = parseWithFallback<PythiaForecastEnvelope>(
          raw,
          PythiaForecastEnvelopeSchema,
          {
            id: "",
            scenario: "",
            narrative: "",
            probability: 0,
            confidence: 0,
            horizon: "",
            persona: "",
            lab_source: "",
          },
          { endpoint: "pythia run stream round" },
        );
        const idx = Number((raw as Record<string, unknown>).index ?? -1);
        return { kind: "round", index: Number.isFinite(idx) ? idx : -1, envelope };
      }
      case "report":
        return { kind: "report", report: String((raw as Record<string, unknown>).report ?? "") };
      case "status":
        return {
          kind: "status",
          status: String((raw as Record<string, unknown>).status ?? "") as PythiaStreamState["status"],
        };
      default:
        return null;
    }
  } catch {
    return null;
  }
}

// Parser for the snapshot's envelopes array (loose, defaulted — same
// discipline as the runs list).
const zArrayEnvelope = PythiaForecastEnvelopeListSchema;

export interface PythiaIssueLab {
  runs: PythiaForecastRun[];
  isLoading: boolean;
  isError: boolean;
  refetch: () => void;
  stream: PythiaStreamState;
  /** True while the hook believes a run is executing server-side. */
  hasLiveRun: boolean;
  start: (input: {
    rounds?: number;
    variables?: string;
    parentRunId?: string;
  }) => Promise<{ run_id: string; run_kind: string } | null>;
  cancel: () => void;
}

export function usePythiaIssueLab(wsId: string, issueId: string): PythiaIssueLab {
  const qc = useQueryClient();
  const runsKey = ["pythia-issue-lab-runs", wsId, issueId] as const;

  const runsQuery = useQuery({
    queryKey: runsKey,
    queryFn: async (): Promise<PythiaForecastRun[]> => {
      const r = await api.rawRequest(
        `/api/experimental/pythia-oracle/forecast/issue/runs?issue_id=${encodeURIComponent(issueId)}&limit=20`,
      );
      if (r.status === 404) return [];
      if (!r.ok) throw new Error(`pythia forecast runs ${r.status}`);
      const raw: unknown = await r.json();
      return parseWithFallback<PythiaForecastRun[]>(
        raw,
        PythiaForecastRunListSchema,
        [],
        { endpoint: "GET /api/experimental/pythia-oracle/forecast/issue/runs" },
      );
    },
    refetchInterval: (query) => {
      const runs = query.state.data ?? [];
      return runs.some((run) => run.status === "running") ? 5_000 : 60_000;
    },
  });

  const runs = runsQuery.data ?? [];
  const [stream, setStream] = useState<PythiaStreamState>(IDLE_PYTHIA_STREAM);
  const [followRunId, setFollowRunId] = useState<string | null>(null);
  // Bumped by the reconnect scheduler to re-arm the subscription effect
  // without touching followRunId (a same-value set would be a no-op).
  const [streamEpoch, setStreamEpoch] = useState(0);
  // Exponential-backoff bookkeeping for transport retries. Reset when a
  // frame parses (transport proven) or when the follow switches runs.
  const reconnectAttemptRef = useRef(0);
  const lastFollowRef = useRef<string | null>(null);
  const followingRef = useRef<string | null>(null);
  followingRef.current = followRunId;

  // Auto-follow: a run discovered as 'running' in the list (fresh page,
  // reload mid-run, or another tab's start) takes over the stream. The
  // stream replays persistence in its snapshot, so a late join loses
  // nothing.
  const runningRunId = runs.find((run) => run.status === "running")?.id ?? null;
  useEffect(() => {
    if (!followRunId && runningRunId) {
      setFollowRunId(runningRunId);
    }
  }, [followRunId, runningRunId]);

  // The live subscription. api.rawRequest returns the raw Response; SSE
  // frames are parsed off the ReadableStream with a block buffer.
  // 0.5.131: a transport failure mid-run (reader error / server restart /
  // proxy timeout) no longer strands the UI — the subscription re-arms
  // with exponential backoff (1s→15s) and the server's snapshot frame
  // replays persistence, so a reconnect loses nothing. A 404/410 run row
  // drops the follow instead of retrying.
  useEffect(() => {
    if (!followRunId) return;
    if (lastFollowRef.current !== followRunId) {
      reconnectAttemptRef.current = 0;
      lastFollowRef.current = followRunId;
    }
    let cancelled = false;
    const controller = new AbortController();
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    const scheduleReconnect = () => {
      if (cancelled) return;
      const attempt = reconnectAttemptRef.current;
      reconnectAttemptRef.current = Math.min(attempt + 1, 5);
      const delay = Math.min(1000 * 2 ** attempt, 15_000);
      retryTimer = setTimeout(() => setStreamEpoch((e) => e + 1), delay);
    };
    void (async () => {
      try {
        const r = await api.rawRequest(
          `/api/experimental/pythia-oracle/forecast/issue/runs/${encodeURIComponent(followRunId)}/stream`,
          { signal: controller.signal },
        );
        if (!r.ok || !r.body) {
          if (r.status === 404 || r.status === 410) {
            if (!cancelled) {
              setStream((s) => (s.runId === followRunId ? s : IDLE_PYTHIA_STREAM));
              setFollowRunId(null);
            }
            return;
          }
          scheduleReconnect();
          return;
        }
        const reader = r.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";
        for (;;) {
          const { done, value } = await reader.read();
          if (cancelled) return;
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          let idx = buffer.indexOf("\n\n");
          while (idx !== -1) {
            const block = buffer.slice(0, idx);
            buffer = buffer.slice(idx + 2);
            const event = parsePythiaSSEBlock(block);
            if (event) {
              reconnectAttemptRef.current = 0;
              setStream((s) => reducePythiaStreamEvent(s, event));
            }
            idx = buffer.indexOf("\n\n");
          }
        }
        // Natural EOF: the server closes after the terminal status frame,
        // and the terminal effect below drops the follow. If EOF arrives
        // without one, treat it as a transport drop and re-arm.
        scheduleReconnect();
      } catch {
        if (!cancelled) scheduleReconnect();
      }
    })();
    return () => {
      cancelled = true;
      if (retryTimer) clearTimeout(retryTimer);
      controller.abort();
    };
  }, [followRunId, streamEpoch]);

  // Terminal → refresh the list (the row's status/report just changed
  // server-side) and drop the follow so the effect doesn't re-arm.
  useEffect(() => {
    if (!isPythiaTerminalStatus(stream.status)) return;
    void qc.invalidateQueries({ queryKey: runsKey });
    if (followingRef.current === stream.runId) {
      setFollowRunId(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [stream.status, stream.runId]);

  const start = useCallback(
    async (input: { rounds?: number; variables?: string; parentRunId?: string }) => {
      const res = await startPythiaIssueForecast({ wsId, issueId, ...input });
      if (res?.run_id) {
        setStream({
          runId: res.run_id,
          status: "running",
          runKind: res.run_kind,
          variables: input.variables ?? "",
          totalRounds: res.rounds,
          envelopes: [],
          report: "",
        });
        setFollowRunId(res.run_id);
        void qc.invalidateQueries({ queryKey: runsKey });
      }
      return res;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [wsId, issueId],
  );

  const cancel = useCallback(() => {
    cancelPythiaIssueForecast(wsId, issueId, stream.runId ?? followRunId);
    setStream((s) => (s.status === "running" ? { ...s, status: "aborted" } : s));
  }, [wsId, issueId, stream.runId, followRunId]);

  const hasLiveRun =
    stream.status === "running" || runs.some((run) => run.status === "running");

  return {
    runs,
    isLoading: runsQuery.isLoading,
    isError: runsQuery.isError,
    refetch: () => void runsQuery.refetch(),
    stream,
    hasLiveRun,
    start,
    cancel,
  };
}
