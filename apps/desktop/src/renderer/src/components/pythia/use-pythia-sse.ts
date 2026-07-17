// usePythiaSse — subscribes to Pythia's /state/stream endpoint and
// returns the current snapshot of predictions + connection status.
//
// Wire compatibility (engine/server.py):
//   - Media type: text/event-stream
//   - First message: { kind: "snapshot", payload: EngineState.snapshot() }
//   - Updates:      { kind: "predictions"|"world"|"run"|"deliberation"
//                         |"generating"|"loop", ts, payload }
//   - Heartbeats:   ": ping\n\n" (every ~15s) — ignored here
//   - Reconnect:    Pythia does not auto-reconnect; the client is
//                   responsible for re-opening the stream.
//
// The hook is defensive: it does not throw on transient errors, it
// surfaces them via `connection` so the dashboard can decide whether
// to fall back to sample data or show an empty-state. Errors that do
// happen upstream (e.g. Pythia not running) keep the LAST good
// snapshot so the dashboard never blinks to empty mid-flight.

import { useEffect, useReducer, useRef } from "react";
import type { PythiaPrediction, PythiaSnapshot } from "./types";

export type PythiaConnection = "idle" | "connecting" | "open" | "error" | "closed";

export interface PythiaLiveState {
  snapshot: PythiaSnapshot | null;
  connection: PythiaConnection;
  lastEventKind: string | null;
}

type Action =
  | { type: "connect" }
  | { type: "open" }
  | { type: "snapshot"; payload: PythiaSnapshot }
  | { type: "predictions"; payload: PythiaPrediction[] }
  | { type: "event"; kind: string }
  | { type: "error" }
  | { type: "closed" };

function reducer(state: PythiaLiveState, action: Action): PythiaLiveState {
  switch (action.type) {
    case "connect":
      return { ...state, connection: "connecting" };
    case "open":
      return { ...state, connection: "open" };
    case "snapshot":
      return {
        ...state,
        snapshot: action.payload,
        connection: "open",
      };
    case "predictions": {
      if (!state.snapshot) {
        return {
          ...state,
          snapshot: {
            generating: false,
            loop_enabled: false,
            last_run_ms: null,
            world: null,
            predictions: action.payload,
          },
        };
      }
      return {
        ...state,
        snapshot: {
          ...state.snapshot,
          predictions: action.payload,
          last_run_ms: Date.now(),
        },
      };
    }
    case "event":
      return { ...state, lastEventKind: action.kind };
    case "error":
      return { ...state, connection: "error" };
    case "closed":
      return { ...state, connection: "closed" };
    default:
      return state;
  }
}

const INITIAL: PythiaLiveState = {
  snapshot: null,
  connection: "idle",
  lastEventKind: null,
};

interface PythiaSseMessage {
  kind: string;
  ts?: number;
  payload?: unknown;
}

/**
 * Parses a single SSE event block (everything between two blank lines
 * in the stream). Multi-line `data:` fields are joined with newlines.
 * Returns null for heartbeat-only frames (`: ping`) so callers can
 * ignore them.
 *
 * Exported for unit tests — kept pure, no I/O.
 */
export function parseSseEvent(raw: string): PythiaSseMessage | null {
  const lines = raw.split(/\r?\n/);
  const dataLines: string[] = [];
  for (const line of lines) {
    if (line.startsWith("data:")) {
      dataLines.push(line.slice(5).trimStart());
    }
  }
  if (dataLines.length === 0) return null;
  try {
    return JSON.parse(dataLines.join("\n")) as PythiaSseMessage;
  } catch {
    return null;
  }
}

export interface UsePythiaSseOptions {
  /** When false, the hook stays idle and does not open a stream. */
  enabled: boolean;
  /** Optional reconnect backoff in ms; defaults to 2000. */
  reconnectMs?: number;
}

export function usePythiaSse(
  url: string | null,
  { enabled, reconnectMs = 2_000 }: UsePythiaSseOptions,
): PythiaLiveState {
  const [state, dispatch] = useReducer(reducer, INITIAL);
  const cancelledRef = useRef(false);

  useEffect(() => {
    cancelledRef.current = false;
    if (!enabled || !url) {
      return;
    }

    let abort: AbortController | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

    async function connect() {
      if (cancelledRef.current) return;
      abort = new AbortController();
      dispatch({ type: "connect" });

      try {
        const res = await fetch(`${url}/state/stream`, {
          signal: abort.signal,
          // Disable Next/Electron caching for streaming endpoints.
          cache: "no-store",
          headers: { Accept: "text/event-stream" },
        });
        if (!res.ok || !res.body) {
          dispatch({ type: "error" });
          scheduleReconnect();
          return;
        }
        dispatch({ type: "open" });

        const reader = res.body.getReader();
        const decoder = new TextDecoder("utf-8");
        let buffer = "";

        // Read stream until the server closes or the caller unmounts.
        // The buffer accumulates partial frames; we flush on a blank
        // line which marks the end of an SSE event.
        while (!cancelledRef.current) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });

          let sep: number;
          while ((sep = buffer.indexOf("\n\n")) !== -1) {
            const frame = buffer.slice(0, sep);
            buffer = buffer.slice(sep + 2);
            const message = parseSseEvent(frame);
            if (!message) continue;
            dispatch({ type: "event", kind: message.kind });
            switch (message.kind) {
              case "snapshot":
                if (message.payload) {
                  dispatch({
                    type: "snapshot",
                    payload: message.payload as PythiaSnapshot,
                  });
                }
                break;
              case "predictions":
                if (Array.isArray(message.payload)) {
                  dispatch({
                    type: "predictions",
                    payload: message.payload as PythiaPrediction[],
                  });
                }
                break;
              // world / run / deliberation / generating / loop events
              // are surfaced via lastEventKind; Phase 2 keeps the
              // dashboard focused on predictions only. Future hooks
              // (world brief banner, run history panel) will pick
              // these up.
              default:
                break;
            }
          }
        }

        dispatch({ type: "closed" });
        if (!cancelledRef.current) scheduleReconnect();
      } catch (err) {
        if (cancelledRef.current) return;
        // AbortError on unmount is expected — do not surface as error.
        if (err instanceof DOMException && err.name === "AbortError") {
          return;
        }
        dispatch({ type: "error" });
        scheduleReconnect();
      }
    }

    function scheduleReconnect() {
      if (cancelledRef.current) return;
      reconnectTimer = setTimeout(connect, reconnectMs);
    }

    void connect();

    return () => {
      cancelledRef.current = true;
      abort?.abort();
      if (reconnectTimer) clearTimeout(reconnectTimer);
    };
  }, [enabled, url, reconnectMs]);

  return state;
}