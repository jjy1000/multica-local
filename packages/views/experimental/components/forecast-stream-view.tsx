"use client";

import { useEffect, useState } from "react";
import { FlaskConical, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { useT } from "../../i18n";

/**
 * ForecastStreamView — renders Claude Lab forecast SSE envelopes
 * (the 0.3.24 wire shape: a flat `{id, scenario, narrative,
 * probability, confidence, horizon, persona, createdAt}` per frame).
 *
 * 0.3.27 B1: the previous implementation mounted the `<PythiaDashboard>`
 * wrapper expecting the Pythia `{kind, payload}` envelope and
 * `/state/stream` path. Both wrong for the Claude Lab forecast endpoint
 * — its wire shape is flat and its URL does NOT carry
 * `/state/stream`. The dashboard silently fell back to
 * `SAMPLE_PREDICTIONS` (the bug the 5-line audit caught).
 *
 * This component parses the lab envelope directly via `fetch + ReadableStream`
 * and renders a plain ticker. Keeps the renderer honest about what
 * the server actually emits; full visualization (SVG globe / Recharts)
 * is a 0.3.27-deferred UI enhancement.
 */

export interface ForecastEnvelope {
  id: string;
  scenario: string;
  narrative: string;
  probability: number;
  confidence: number;
  horizon: string;
  persona: string;
  createdAt: string;
}

export function ForecastStreamView({ url }: { url: string }) {
  const { t } = useT("experimental");
  const [frames, setFrames] = useState<ForecastEnvelope[]>([]);
  const [err, setErr] = useState<string | null>(null);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!url) return;
    const controller = new AbortController();
    let cancelled = false;

    const connect = async () => {
      try {
        setErr(null);
        const resp = await api.rawRequest(url, {
          signal: controller.signal,
          cache: "no-store",
          headers: { Accept: "text/event-stream" },
        });
        if (!resp.ok || !resp.body) {
          throw new Error(`forecast stream not available: ${resp.status}`);
        }
        if (cancelled) return;
        setConnected(true);

        const reader = resp.body.getReader();
        const decoder = new TextDecoder();
        let buf = "";
        while (!cancelled) {
          const { done, value } = await reader.read();
          if (done) break;
          buf += decoder.decode(value, { stream: true });
          // Parse SSE: events separated by "\n\n", each event has
          // "event:" and "data:" lines. We only care about the
          // `prediction` event from this endpoint.
          let idx;
          while ((idx = buf.indexOf("\n\n")) !== -1) {
            const block = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const lines = block.split("\n");
            let data: string | null = null;
            for (const line of lines) {
              if (line.startsWith("data:")) {
                data = line.slice(5).trim();
                break;
              }
            }
            if (data === null) continue;
            try {
              const envelope = JSON.parse(data) as ForecastEnvelope;
              if (typeof envelope?.id === "string") {
                setFrames((prev) => [envelope, ...prev].slice(0, 50));
              }
            } catch {
              // Skip non-JSON lines (e.g. ": ping" keep-alives).
            }
          }
        }
      } catch (e) {
        if (cancelled) return;
        const msg = e instanceof Error ? e.message : String(e);
        setErr(msg);
        setConnected(false);
      }
    };

    void connect();
    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [url]);

  return (
    <div className="flex h-full w-full flex-col gap-2 overflow-y-auto p-3">
      <header className="flex items-center justify-between text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <FlaskConical className="size-3.5" />
          {t(($) => $.forecast_stream.title)}
        </span>
        {connected ? (
          <span className="inline-flex items-center gap-1">
            <span className="size-1.5 animate-pulse rounded-full bg-emerald-500" />
            {t(($) => $.forecast_stream.connected)}
          </span>
        ) : err ? (
          <span className="text-rose-500">
            {t(($) => $.forecast_stream.disconnected, { error: err })}
          </span>
        ) : (
          <span className="inline-flex items-center gap-1">
            <Loader2 className="size-3 animate-spin" />
            {t(($) => $.forecast_stream.connecting)}
          </span>
        )}
      </header>
      <div className="flex flex-col gap-2">
        {frames.length === 0 ? (
          <p className="rounded-md border border-dashed border-border p-4 text-xs text-muted-foreground">
            {t(($) => $.forecast_stream.waiting)}
          </p>
        ) : (
          frames.map((f) => (
            <article
              key={f.id}
              className="rounded-md border border-border bg-background p-3"
            >
              <div className="mb-1 flex items-center gap-2 text-[10px] uppercase tracking-wide text-muted-foreground">
                <span className="rounded bg-secondary px-1.5 py-0.5 font-mono">
                  {f.horizon}
                </span>
                <span className="rounded bg-secondary px-1.5 py-0.5 font-mono">
                  {f.persona}
                </span>
                <span className="ml-auto font-mono">
                  {(f.probability * 100).toFixed(0)}%
                </span>
                <span className="font-mono">
                  {t(($) => $.forecast_stream.confidence, {
                    value: (f.confidence * 100).toFixed(0),
                  })}
                </span>
              </div>
              <h3 className="text-sm font-medium">{f.scenario}</h3>
              {f.narrative ? (
                <p className="mt-1 text-xs leading-relaxed text-muted-foreground">
                  {f.narrative}
                </p>
              ) : null}
            </article>
          ))
        )}
      </div>
    </div>
  );
}
