// WhatIfPanel — text input + persona checkboxes + submit.
// POSTs to /whatif via experimentalAPI.pythia.proxy (Phase 3 IPC).
// The returned predictions are ephemeral — they render as a "WhatIf"
// row beneath the live globe for one cycle, then the SSE snapshot
// (if any) replaces them on the next event.
//
// Phase 4 polish:
//   - Busy state lives INSIDE the panel so the submit button can
//     reflect in-flight IPC without parent re-renders.
//   - All / None persona links for fast toggling.
//   - Trim textarea on submit (avoid leading/trailing whitespace).
//   - Keyboard: Cmd/Ctrl+Enter submits the form.

import { useMemo, useState } from "react";
import type { PythiaPrediction } from "./types";
import { useT } from "@multica/views/i18n";

// Persona ids stay English because they are wire identifiers — the
// Pythia engine returns the same id string ("Strategist" / "Economist"
// / "Naturalist" / "Skeptic") and it doubles as the key into the
// color map and the request body to /whatif. We translate only the
// user-visible label via useT below; the id stays a stable English
// literal so the engine contract doesn't shift.
type PersonaId = "Strategist" | "Economist" | "Naturalist" | "Skeptic";
const PERSONA_IDS: readonly PersonaId[] = ["Strategist", "Economist", "Naturalist", "Skeptic"];

export interface WhatIfResult {
  scenario: string;
  narrative: string;
  predictions: PythiaPrediction[];
}

export interface WhatIfPanelProps {
  onResult: (result: WhatIfResult) => void;
  /** Disable the form when the Pythia manager is not running. */
  disabled?: boolean;
}

export function WhatIfPanel({ onResult, disabled = false }: WhatIfPanelProps) {
  const { t } = useT("pythia");
  // Selector must be expression-form so i18next reads PATH_KEY off the
  // proxy return value. See packages/views/i18n/use-t.ts for the
  // block-body incident reference.
  const personaLabels = useMemo(
    () =>
      PERSONA_IDS.reduce(
        (acc, id) => {
          const key = `persona_${id.toLowerCase()}` as
            | "persona_strategist"
            | "persona_economist"
            | "persona_naturalist"
            | "persona_skeptic";
          acc[id] = (t as unknown as (k: string) => string)(key) || id;
          return acc;
        },
        {} as Record<PersonaId, string>,
      ),
    [t],
  );
  const personaOptions = useMemo(
    () => PERSONA_IDS.map((id) => ({ id, label: personaLabels[id] })),
    [personaLabels],
  );

  const [scenario, setScenario] = useState("");
  const [personas, setPersonas] = useState<Set<string>>(
    new Set<string>(["Strategist", "Skeptic"]),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function togglePersona(id: string) {
    setPersonas((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function selectAll() {
    setPersonas(new Set(PERSONA_IDS));
  }

  function selectNone() {
    setPersonas(new Set());
  }

  async function submit() {
    setError(null);
    const trimmed = scenario.trim();
    if (trimmed.length === 0) {
      setError("scenario is required");
      return;
    }
    if (busy) return;
    setBusy(true);
    try {
      const res = await window.experimentalAPI.pythia.proxy<{
        ok: boolean;
        status: number;
        body: unknown;
      }>("/whatif", {
        method: "POST",
        body: JSON.stringify({
          scenario: trimmed,
          personas: personas.size === 0 ? null : Array.from(personas),
        }),
        timeoutMs: 45_000,
      });
      if (!res.ok) {
        const errBody = res.body as { error?: string } | string | undefined;
        const msg =
          typeof errBody === "string"
            ? errBody
            : errBody?.error ?? `HTTP ${res.status}`;
        setError(msg);
        return;
      }
      const data = res.body as {
        scenario?: string;
        narrative?: string;
        predictions?: unknown[];
      };
      onResult({
        scenario: data.scenario ?? trimmed,
        narrative: data.narrative ?? "",
        predictions: normalizeWhatIfPredictions(data.predictions ?? []),
      });
      setScenario("");
    } finally {
      setBusy(false);
    }
  }

  return (
    <section
      className="flex flex-col gap-2 rounded-lg border border-border bg-card/40 p-3"
      aria-busy={busy}
    >
      <header className="flex items-baseline justify-between">
        <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t(($) => $.what_if)}
        </h3>
        <span className="text-[10px] text-muted-foreground">
          {t(($) => $.counterfactual_ephemeral)}
        </span>
      </header>

      <textarea
        value={scenario}
        onChange={(e) => setScenario(e.target.value)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
            e.preventDefault();
            void submit();
          }
        }}
        placeholder='e.g. "the Strait of Hormuz closes tonight"  (⌘⏎ to run)'
        disabled={disabled || busy}
        rows={2}
        className="w-full resize-none rounded border border-input bg-background px-2 py-1.5 text-xs leading-relaxed text-foreground placeholder:text-muted-foreground disabled:opacity-50"
      />

      <div className="flex items-center justify-between gap-2">
        <div className="flex flex-wrap items-center gap-1.5">
          {personaOptions.map((p) => {
            const checked = personas.has(p.id);
            return (
              <button
                key={p.id}
                type="button"
                onClick={() => togglePersona(p.id)}
                disabled={disabled}
                className={
                  checked
                    ? "rounded-full bg-primary/20 px-2 py-0.5 text-[10px] font-medium text-primary ring-1 ring-primary/40"
                    : "rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground ring-1 ring-transparent hover:text-foreground"
                }
              >
                {p.label}
              </button>
            );
          })}
        </div>
        <div className="flex shrink-0 gap-1 text-[10px]">
          <button
            type="button"
            onClick={selectAll}
            disabled={disabled}
            className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:opacity-50"
          >
            {t(($) => $.all)}
          </button>
          <span className="text-muted-foreground/50">·</span>
          <button
            type="button"
            onClick={selectNone}
            disabled={disabled}
            className="text-muted-foreground underline-offset-2 hover:text-foreground hover:underline disabled:opacity-50"
          >
            {t(($) => $.none)}
          </button>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2">
        <button
          type="button"
          onClick={() => void submit()}
          disabled={disabled || busy || scenario.trim().length === 0}
          className="flex items-center gap-1.5 rounded bg-primary px-3 py-1 text-xs font-medium text-primary-foreground disabled:cursor-not-allowed disabled:opacity-50"
        >
          {busy && (
            <span
              className="inline-block h-2.5 w-2.5 animate-spin rounded-full border border-primary-foreground border-t-transparent"
              aria-hidden
            />
          )}
          {busy ? t(($) => $.running_council) : t(($) => $.run_what_if)}
        </button>
        {error && (
          <span
            className="truncate text-[11px] text-rose-400"
            title={error}
            role="alert"
          >
            {error}
          </span>
        )}
      </div>
    </section>
  );
}

// normalizeWhatIfPredictions maps Pythia's wire shape (statement +
// brief_id, no base_probability / agents[]) onto our PythiaPrediction
// UI type. WhatIf predictions don't carry the swarm's deliberation
// (that's an optional second pass); when `agents` is empty we leave
// the field empty so the panel renders an "ephemeral" tag.
//
// Exported for unit tests — kept pure, no React state.
export function normalizeWhatIfPredictions(
  raw: ReadonlyArray<unknown>,
): PythiaPrediction[] {
  const out: PythiaPrediction[] = [];
  for (const item of raw) {
    if (!item || typeof item !== "object") continue;
    const r = item as Record<string, unknown>;
    const horizon = String(r.horizon ?? "week") as PythiaPrediction["horizon"];
    const probability =
      typeof r.probability === "number" ? r.probability : 0;
    const lat = typeof r.lat === "number" ? r.lat : null;
    const lng = typeof r.lng === "number" ? r.lng : null;
    const agents = Array.isArray(r.agents)
      ? (r.agents as PythiaPrediction["agents"])
      : [];
    out.push({
      id:
        typeof r.id === "string" && r.id.length > 0
          ? `whatif-${r.id}`
          : `whatif-${out.length}`,
      title: String(r.statement ?? r.title ?? ""),
      horizon,
      probability,
      reasoning: String(r.reasoning ?? ""),
      location: String(r.location ?? ""),
      lat,
      lng,
      agents,
      base_probability:
        typeof r.base_probability === "number" ? r.base_probability : null,
      prev_probability:
        typeof r.prev_probability === "number" ? r.prev_probability : null,
      split: Boolean(r.split),
    });
  }
  return out;
}