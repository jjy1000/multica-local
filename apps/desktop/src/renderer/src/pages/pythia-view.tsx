import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";

// PythiaView (0.3.18+)
//
// Two surfaces live here, picked at runtime via the `pythia_oracle`
// Labs flag (server/internal/experimental/catalog.go):
//
//   flag = off  → original informational shell: loopback URL, status,
//                 CLI usage example. No forecast UI, no SSE, no imports
//                 from components/pythia/. Hard constraint from the
//                 Labs framework (0.3.6 memo): flag-off completely
//                 bypass — no module init side effects.
//
//   flag = on   → <PythiaReportSurface /> (lazy-loaded so the ~5KB of
//                 report shell + interactive controls is fetched only
//                 when the flag is actually on). 0.3.29 default is 1
//                 round; user can extend to 3 via the horizon/persona
//                 controls.
//
// The actual forecasting still happens through the multica-pythia Skill
// (server/internal/service/builtin_skills/multica-pythia/SKILL.md).
const PythiaReportSurface = lazy(() =>
  import("../components/pythia/pythia-report-surface").then((m) => ({
    default: m.PythiaReportSurface,
  })),
);

type Persona = "strategist" | "analyst" | "critic";
type Horizon = "day" | "week" | "month" | "year";

export function PythiaView({ issueId: initialIssueId = null }: { issueId?: string | null } = {}) {
  const pythiaOracleEnabled = useExperimentalFlag("pythia_oracle", false);
  const { t } = useT("pythia");
  const { t: tExp } = useT("experimental");
  const [url, setUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<string>("idle");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!pythiaOracleEnabled) return;
    let cancelled = false;

    async function bootAndPoll() {
      try {
        await window.experimentalAPI.pythia.ensureUp();
      } catch (err) {
        if (!cancelled) {
          setError(humanizeBootError(err));
        }
        return;
      }
      while (!cancelled) {
        const [nextStatus, nextUrl] = await Promise.all([
          window.experimentalAPI.pythia.getStatus(),
          window.experimentalAPI.pythia.getURL(),
        ]);
        setStatus(nextStatus);
        if (nextUrl) {
          setUrl(nextUrl);
          break;
        }
        if (nextStatus === "error") {
          setError(
            "Pythia manager reported error — verify python3 is on PATH and that resources/pythia/engine/ is staged",
          );
          return;
        }
        await new Promise((r) => setTimeout(r, 1_000));
      }
    }

    void bootAndPoll();
    return () => {
      cancelled = true;
    };
  }, [pythiaOracleEnabled]);

  // Interactive state — owned at the page level so the lazy-loaded
  // report surface receives stable props. The horizon + persona
  // choices feed into /forecast/issue and the synthetic generator.
  // When the parent passes an `issueId`, the report surface opens
  // already-scoped to that issue instead of showing a "no issue
  // bound" empty state. The user can still re-pick via the issue
  // panel inside the surface.
  const [searchParams] = useSearchParams();
  const urlIssueId = searchParams.get("issue");
  const [issueId, setIssueId] = useState<string | null>(initialIssueId ?? urlIssueId);
  const [horizon, setHorizon] = useState<Horizon>("week");
  const [persona, setPersona] = useState<Persona>("strategist");
  const [subscribed, setSubscribed] = useState(true);
  const [rounds, setRounds] = useState(1);

  const handleRegenerate = useCallback(() => {
    // Bumping rounds forces the report surface to re-mount its
    // SSE subscription; the surface filters out frames beyond
    // `rounds` so this is just a refresh signal.
    setRounds((r) => (r >= 3 ? 1 : r + 1));
  }, []);

  const handleSubmitScenario = useCallback((scenario: string) => {
    // 0.3.29 close-the-loop: when the user submits a new scenario
    // from the report surface, we bump the round count and feed
    // it as `scenario_context` to the next /forecast/issue call.
    // Phase 2 can persist this as a new Issue row; Phase 1 just
    // rerenders with the new context.
    if (scenario.trim().length === 0) return;
    setRounds((r) => r + 1);
  }, []);

  if (error) {
    return (
      <div className="flex h-full w-full flex-col items-center justify-center gap-3 p-6 text-center text-muted-foreground">
        <h2 className="text-lg font-semibold text-foreground">
          {t(($) => $.pythia_unavailable)}
        </h2>
        <p className="max-w-md text-sm">{error}</p>
      </div>
    );
  }

  if (pythiaOracleEnabled) {
    return (
      <div className="flex h-full w-full flex-col">
        {/* 0.3.54: when the user lands on the Pythia page without
            pre-binding an issue, surface a one-line onboarding hint
            that explains the issue-scoped prediction contract. The
            hint shows above the report surface (which still renders
            with its own issue picker below) so the user does not
            think the page is empty when the report surface
            "completes" without producing per-issue frames. */}
        {!issueId && (
          <div className="border-b border-border bg-muted/40 px-6 py-2 text-xs text-muted-foreground">
            <span className="font-medium text-foreground/90">提示 · </span>
            从「分配给」绑定的问题进入时,这里的报告会按 issue 出 10 轮多视角预测。
            还没绑定?先到任务列表里给某个 issue 选 Pythia,再点上方「打开实验室面板」回来。
          </div>
        )}
        {issueId ? (
          <div className="flex items-center justify-end gap-1.5 border-b border-border/60 bg-muted/30 px-6 py-1.5 text-[10px] text-muted-foreground">
            <span className="rounded border border-border bg-background/40 px-1.5 py-0.5 font-mono text-foreground/80">
              {issueId.slice(0, 8)}…
            </span>
            <button
              type="button"
              onClick={() => setIssueId(null)}
              aria-label={tExp(($) => $.back)}
              className="inline-flex size-4 items-center justify-center rounded text-xs hover:bg-muted hover:text-foreground"
            >
              ×
            </button>
          </div>
        ) : null}
        <Suspense
          fallback={
            <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
              <span>
                {t(($) => $.loading_dashboard)} (manager: {status})…
              </span>
            </div>
          }
        >
          <PythiaReportSurface
            issueId={issueId}
            horizon={horizon}
            persona={persona}
            subscribed={subscribed}
            rounds={rounds}
            managerUrl={url}
            onSelectIssue={setIssueId}
            onChangeHorizon={setHorizon}
            onChangePersona={setPersona}
            onToggleSubscribe={() => setSubscribed((v) => !v)}
            onRegenerate={handleRegenerate}
            onSubmitScenario={handleSubmitScenario}
          />
        </Suspense>
      </div>
    );
  }

  // Flag-off: bare placeholder. The Pythia prediction surface is
  // gated behind `pythia_oracle`; without the flag there's nothing
  // to render here.
  return (
    <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
      <span>{t(($) => $.starting_pythia)} (status: {status})…</span>
    </div>
  );
}

// humanizeBootError mirrors the Claude Science helper. The main
// process surfaces a structured error with `code = "BINARY_NOT_BUNDLED"`
// when the bundled Python engine / uvicorn launcher is missing under
// resources/pythia/. The renderer copy points the user at the right
// vendor path so the fix path is one command away.
function humanizeBootError(err: unknown): string {
  if (err instanceof Error) {
    if ((err as Error & { code?: string }).code === "BINARY_NOT_BUNDLED") {
      return (
        "Pythia 的 Python 引擎还没有被打包进桌面 app。请把 Pythia 源码放到 " +
        "apps/desktop/vendor/pythia-src/engine/,然后跑 " +
        "`pnpm --filter @multica/desktop bundle-cli && pnpm --filter @multica/desktop package` " +
        "重新打包。当前 Labs flag 已启用,但 service not bundled。"
      );
    }
    return err.message;
  }
  return "failed to start Pythia manager";
}

// silence unused-warning for hooks that Phase 1 leaves unwired.
const _useRefHook = useRef;
void _useRefHook;
