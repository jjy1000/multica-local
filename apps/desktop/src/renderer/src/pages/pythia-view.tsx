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
  // When mounted inline from an issue detail (via renderLabInline
  // on IssueDetailPage), the parent passes an `issueId` so the
  // report surface opens already-scoped to that issue instead of
  // showing a "no issue bound" empty state. The user can still
  // re-pick via the issue panel inside the surface.
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

  if (!url) {
    return (
      <div className="flex h-full w-full items-center justify-center text-sm text-muted-foreground">
        <span>
          {t(($) => $.starting_pythia)} (status: {status})…
        </span>
      </div>
    );
  }

  return <PythiaStatusPanel url={url} />;
}

function PythiaStatusPanel({ url }: { url: string }) {
  const { t } = useT("pythia");
  return (
    <div className="flex h-full w-full flex-col gap-6 overflow-auto p-8 text-sm">
      <header className="flex flex-col gap-2">
        <h2 className="text-xl font-semibold">Pythia Prediction Oracle</h2>
        <p className="text-muted-foreground">
          Headless Python service. Multica agents reach it via the
          <code className="mx-1 rounded bg-muted px-1.5 py-0.5">multica-pythia</code>
          Skill; this page is a status surface for the local subprocess.
        </p>
      </header>

      <dl className="grid grid-cols-[120px_1fr] gap-y-2 text-sm">
        <dt className="text-muted-foreground">{t(($) => $.loopback_url)}</dt>
        <dd className="font-mono">{url}</dd>
        <dt className="text-muted-foreground">{t(($) => $.health_probe)}</dt>
        <dd className="font-mono">{`${url}/health`}</dd>
      </dl>

      <section className="flex flex-col gap-2">
        <h3 className="font-semibold">{t(($) => $.try_from_agent)}</h3>
        <pre className="overflow-auto rounded border bg-muted p-3 text-xs leading-relaxed">
{`multica --json pythia status
multica pythia brief --url ${url}
multica pythia predict --url ${url} --scenario "…" --horizon week
multica pythia whatif --url ${url} --intervention "…"`}
        </pre>
      </section>
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
