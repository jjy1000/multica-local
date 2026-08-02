import { useEffect, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { FlaskConical, Network, Sparkles, Users, BookOpen, AlertTriangle, Loader2 } from "lucide-react";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useT } from "@multica/views/i18n";
import { useCurrentWorkspace } from "@multica/core/paths";
import { useQuery, useMutation } from "@tanstack/react-query";
import { agentListOptions, skillListOptions } from "@multica/core/workspace/queries";
import { api } from "@multica/core/api";

// MythosView surfaces the Mythos Swarm surface.
//
// 0.3.16-patch.1 shipped the install handler (5 Mythos agents + 1 squad
// under the dedicated `mythos-swarm` workspace), the multica-mythos
// Skill, and the run form. The server-side POST /api/mythos/run is
// not yet implemented — running the RDT three-stage runner against a
// live workspace is the next slice. Until that lands, this view is
// intentionally a "manifest + state" page that:
//
//   1. Confirms the flag is on.
//   2. Lists the agents the install handler will provision.
//   3. Skips the live "submit run" form to avoid the previous fake-
//      data path (which rendered a synthetic convergence history
//      without ever calling the server).
//
// 0.3.29 round extension:
//   - Run form wires up `POST /api/experimental/mythos-swarm/run`
//     against the active workspace.
//   - "Extension agent list" multi-select lets the user add workspace
//     agents beyond the canonical 5-agent roster (the unique escape
//     hatch for mythos_swarm — other labs keep the picker locked).
//   - "Depth (max loop iters)" slider clamps 1..5 via the
//     server-side mythosMaxLoopHardCap.
//   - "Self-optimization" toggle wires the coda agent's per-iter
//     reflection. Rendered reflection count comes back in the
//     response so the UI can show "3 of 3 reflections written".
//   - "Skills to encourage" multi-select lets the user list skill
//     names the loop agent is encouraged to read via the SkillAdapter
//     (first 3 are baked into the sub-issue description).
//   - coda_conclusions JSONB is rendered alongside the free-text
//     summary so the structured takeaways are visible.

export function MythosView({ issueId: initialIssueId = null }: { issueId?: string | null } = {}) {
  const flagEnabled = useExperimentalFlag("mythos_swarm", false);

  return (
    <div className="flex h-full w-full flex-col overflow-y-auto bg-background text-foreground">
      <Header flagEnabled={flagEnabled} />
      <main className="mx-auto flex w-full max-w-3xl flex-col gap-8 px-6 py-10">
        <Intro />
        {!flagEnabled && <FlagOffNotice />}
        {flagEnabled && <EnabledStateCard />}
        {flagEnabled && <RosterCard />}
        {flagEnabled && <RunForm initialIssueId={initialIssueId} />}
      </main>
    </div>
  );
}

function Header({ flagEnabled }: { flagEnabled: boolean }) {
  const { t } = useT("layout");
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">{t(($) => $.sidebar.experimental_group)}</span>
        <span className="text-muted-foreground/60">/</span>
        <span>Mythos Swarm</span>
      </div>
      <span className="ml-auto inline-flex items-center gap-1.5 font-mono">
        <span
          className={
            "inline-block h-1.5 w-1.5 rounded-full " +
            (flagEnabled ? "bg-emerald-500" : "bg-muted-foreground/40")
          }
          aria-hidden
        />
        {flagEnabled ? "enabled" : "off"}
      </span>
    </header>
  );
}

function Intro() {
  const { t } = useT("mythos");
  return (
    <section className="flex flex-col gap-3">
      <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
        <Network className="size-6 text-emerald-600 dark:text-emerald-400" aria-hidden />
        {t(($) => $.title)}
      </h1>
      <p className="text-sm leading-relaxed text-muted-foreground">
        {t(($) => $.intro)}
      </p>
    </section>
  );
}

function FlagOffNotice() {
  const { t } = useT("mythos");
  return (
    <section className="rounded-md border border-border bg-muted/40 px-4 py-3 text-xs text-muted-foreground">
      {t(($) => $.flag_off_notice)}
    </section>
  );
}

function EnabledStateCard() {
  const { t } = useT("mythos");
  return (
    <section className="rounded-xl border border-emerald-200 bg-emerald-50/30 p-5 dark:border-emerald-800 dark:bg-emerald-950/20">
      <p className="flex items-center gap-2 text-sm font-medium text-emerald-800 dark:text-emerald-200">
        <Sparkles className="size-4" aria-hidden />
        {t(($) => $.flag_on_badge)}
      </p>
      <p className="mt-1 text-xs text-foreground">
        {t(($) => $.enabled_body)}
      </p>
      <ul className="mt-3 flex flex-col gap-1 text-xs text-foreground">
        <li>• {t(($) => $.bullet1)}</li>
        <li>• {t(($) => $.bullet2)}</li>
        <li>• {t(($) => $.bullet3)}</li>
      </ul>
    </section>
  );
}

function RosterCard() {
  const { t } = useT("mythos");
  const members = [
    { role: "prelude", name: "mythos_prelude", label: t(($) => $.role_prelude) },
    { role: "loop", name: "mythos_loop_researcher", label: t(($) => $.role_researcher) },
    { role: "loop", name: "mythos_loop_coder", label: t(($) => $.role_coder) },
    { role: "loop", name: "mythos_loop_analyst", label: t(($) => $.role_analyst) },
    { role: "coda", name: "mythos_coda", label: t(($) => $.role_coda) },
  ];
  return (
    <section
      aria-labelledby="roster-title"
      className="rounded-xl border border-border bg-card p-6"
    >
      <h2 id="roster-title" className="text-base font-semibold">
        {t(($) => $.roster_title)}
      </h2>
      <p className="mt-1 text-sm text-muted-foreground">
        {t(($) => $.roster_blurb)}
      </p>
      <ul className="mt-4 divide-y divide-border text-sm">
        {members.map((m) => (
          <li key={m.name} className="flex items-baseline justify-between gap-3 py-2">
            <div className="flex items-baseline gap-3">
              <span className="inline-flex size-5 items-center justify-center rounded-md bg-muted font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
                {m.role}
              </span>
              <span className="font-mono text-foreground">{m.name}</span>
            </div>
            <span className="text-muted-foreground">{m.label}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

type MythosRunResult = {
  run_id: string;
  coda_summary: string;
  iterations_run: number;
  convergence_history: number[];
  coda_conclusions?: Array<{ key: string; value: string; confidence?: number; actionable?: boolean }>;
  reflection_count?: number;
  root_issue_id?: string;
  final_issue_id?: string;
};

function RunForm({ initialIssueId = null }: { initialIssueId?: string | null } = {}) {
  const { t } = useT("mythos");
  const workspace = useCurrentWorkspace();
  const wsId = workspace?.id ?? "";
  const [problem, setProblem] = useState("");
  // When the parent passes an `issueId`, pre-bind the run to that issue
  // by sending it as `root_issue_id`. The server pins the run's planning
  // artifacts to that issue and the results land on the issue's lab
  // workspace via `IssueLabsSection` instead of getting lost in the
  // workspace-wide swarm. The user can clear the binding by re-running
  // without an issueId (the picker would let them pick a different one),
  // but for the per-issue case the binding is the whole point of having
  // a lab-tagged issue.
  const [searchParams] = useSearchParams();
  const urlIssueId = searchParams.get("issue");
  const [rootIssueId, setRootIssueId] = useState<string | null>(initialIssueId ?? urlIssueId);
  useEffect(() => {
    setRootIssueId(initialIssueId ?? urlIssueId);
  }, [initialIssueId, urlIssueId]);
  const [maxLoop, setMaxLoop] = useState(3);
  const [extensionAgentIDs, setExtensionAgentIDs] = useState<string[]>([]);
  const [selfOptimization, setSelfOptimization] = useState(true);
  const [skillsToEncourage, setSkillsToEncourage] = useState<string[]>([]);
  const [result, setResult] = useState<MythosRunResult | null>(null);

  // Fetch the workspace agents so the user can extend the roster.
  // We rely on agentListOptions (workspace/queries.ts:44), which
  // wraps `GET /api/agents?workspace_id=...&include_archived=true`.
  // include_archived=true so the user can see all lab-owned agents
  // when picking extensions (without it the picker hides the very
  // agents the install handler provisioned).
  const agentsQuery = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
  });

  // Fetch skills so the user can pick "skills to encourage" by
  // name. Same caveat: the SkillAdapter pulls skill content, here
  // we only use the name to populate the sub-issue description.
  const skillsQuery = useQuery({
    ...skillListOptions(wsId),
    enabled: !!wsId,
  });

  // Filter the agent picker so the canonical 5 Mythos agents are
  // always present (the loop rotation), and EXTENSION_AGENT_OPTIONS
  // can only add agents the workspace explicitly owns. We exclude
  // private visibility (sub-agent of another team) and the canonical
  // mythos_* roster (those are picked automatically by the server).
  const candidates = useMemo(() => {
    const list = (agentsQuery.data ?? []) as Array<{
      id: string;
      name: string;
      description?: string | null;
      visibility?: "workspace" | "private";
    }>;
    return list
      .filter((a) => a.visibility !== "private")
      .filter((a) => !a.name.startsWith("mythos_"))
      .map((a) => ({ id: a.id, name: a.name, description: a.description ?? "" }));
  }, [agentsQuery.data]);

  const runMut = useMutation<MythosRunResult, Error, {
    problem: string;
    max_loop_iters: number;
    extension_agent_ids: string[];
    self_optimization_enabled: boolean;
    skills_to_encourage: string[];
    root_issue_id: string | null;
  }>({
    mutationFn: async (body) => {
      const resp = await api.rawRequest(`/api/experimental/mythos-swarm/run`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      if (!resp.ok) {
        const message = await resp.text().catch(() => `HTTP ${resp.status}`);
        throw new Error(message || `HTTP ${resp.status}`);
      }
      return (await resp.json()) as MythosRunResult;
    },
    onSuccess: (data) => setResult(data),
  });

  const canSubmit = problem.trim().length > 0 && !runMut.isPending && !!wsId;

  const onSubmit = () => {
    if (!canSubmit) return;
    runMut.mutate({
      problem: problem.trim(),
      max_loop_iters: maxLoop,
      extension_agent_ids: extensionAgentIDs,
      self_optimization_enabled: selfOptimization,
      skills_to_encourage: skillsToEncourage,
      root_issue_id: rootIssueId,
    });
  };

  return (
    <section
      aria-labelledby="run-form-title"
      className="rounded-xl border border-border bg-card p-6"
    >
      <h2 id="run-form-title" className="text-base font-semibold">
        {t(($) => $.run_form_title)}
      </h2>
      <p className="mt-1 text-sm text-muted-foreground">
        {t(($) => $.run_form_blurb)}
      </p>

      {/* 0.3.54: a subtler hint when the page is opened without an
          issue binding. The Mythos RDT pipeline produces three
          comments + sub-issues that should land on the bound
          issue's timeline; without one the results surface on
          no issue at all. */}
      {!rootIssueId && (
        <p className="mt-2 rounded-md border border-border/60 bg-muted/40 px-3 py-2 text-[11px] text-muted-foreground">
          提示 · 这一页开启后,产物会落在「分配给 Mythos」的 issue 评论区。
          在任务列表的某个 issue 选 Mythos Swarm 后再点「打开实验室面板」,
          报告会按 issue 归档;不绑定也能跑(产物在工作区水平日志里)。
        </p>
      )}

      <div className="mt-4 flex flex-col gap-4">
        <label className="flex flex-col gap-1.5">
          <span className="text-xs font-medium text-foreground">
            {t(($) => $.field_problem)}
          </span>
          <textarea
            value={problem}
            onChange={(e) => setProblem(e.target.value)}
            placeholder={t(($) => $.field_problem_placeholder)}
            className="min-h-[80px] rounded-md border border-input bg-background px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          />
        </label>

        <label className="flex flex-col gap-1.5">
          <span className="flex items-center justify-between text-xs font-medium text-foreground">
            <span>{t(($) => $.field_max_iters)}</span>
            <span className="font-mono text-muted-foreground">{maxLoop}</span>
          </span>
          <input
            type="range"
            min={1}
            max={5}
            step={1}
            value={maxLoop}
            onChange={(e) => setMaxLoop(parseInt(e.target.value, 10))}
            className="accent-emerald-600"
          />
          <span className="text-[11px] text-muted-foreground">
            {t(($) => $.field_max_iters_help)}
          </span>
        </label>

        <fieldset className="flex flex-col gap-1.5 rounded-md border border-border p-3">
          <legend className="px-1 text-xs font-medium text-foreground">
            {t(($) => $.field_extension_agents)}
          </legend>
          <p className="text-[11px] text-muted-foreground">
            {t(($) => $.field_extension_agents_help)}
          </p>
          <div className="mt-2 flex max-h-40 flex-col gap-1 overflow-y-auto">
            {agentsQuery.isPending ? (
              <span className="text-xs text-muted-foreground">…</span>
            ) : agentsQuery.isError ? (
              <span className="text-xs text-destructive">
                {(agentsQuery.error as Error)?.message ?? "error"}
              </span>
            ) : candidates.length === 0 ? (
              <span className="text-xs text-muted-foreground">
                {t(($) => $.no_extension_candidates)}
              </span>
            ) : (
              candidates.map((a) => {
                const checked = extensionAgentIDs.includes(a.id);
                return (
                  <label
                    key={a.id}
                    className="flex cursor-pointer items-start gap-2 rounded-sm px-2 py-1 text-xs hover:bg-muted"
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => {
                        if (e.target.checked) {
                          setExtensionAgentIDs([...extensionAgentIDs, a.id]);
                        } else {
                          setExtensionAgentIDs(extensionAgentIDs.filter((x) => x !== a.id));
                        }
                      }}
                      className="mt-0.5"
                    />
                    <span className="flex flex-col">
                      <span className="font-mono text-foreground">{a.name}</span>
                      {a.description ? (
                        <span className="text-[10px] text-muted-foreground">
                          {a.description}
                        </span>
                      ) : null}
                    </span>
                  </label>
                );
              })
            )}
          </div>
        </fieldset>

        <label className="flex items-start gap-2 rounded-md border border-border p-3 text-xs">
          <input
            type="checkbox"
            checked={selfOptimization}
            onChange={(e) => setSelfOptimization(e.target.checked)}
            className="mt-0.5"
          />
          <span className="flex flex-col">
            <span className="font-medium text-foreground">
              {t(($) => $.field_self_optimization)}
            </span>
            <span className="text-[11px] text-muted-foreground">
              {t(($) => $.field_self_optimization_help)}
            </span>
          </span>
        </label>

        <fieldset className="flex flex-col gap-1.5 rounded-md border border-border p-3">
          <legend className="px-1 text-xs font-medium text-foreground">
            {t(($) => $.field_skills)}
          </legend>
          <p className="text-[11px] text-muted-foreground">
            {t(($) => $.field_skills_help)}
          </p>
          <div className="mt-2 flex max-h-40 flex-col gap-1 overflow-y-auto">
            {skillsQuery.isPending ? (
              <span className="text-xs text-muted-foreground">…</span>
            ) : skillsQuery.isError ? (
              <span className="text-xs text-destructive">
                {(skillsQuery.error as Error)?.message ?? "error"}
              </span>
            ) : (skillsQuery.data ?? []).length === 0 ? (
              <span className="text-xs text-muted-foreground">
                {t(($) => $.no_skills)}
              </span>
            ) : (
              (skillsQuery.data ?? []).map((s) => {
                const checked = skillsToEncourage.includes(s.name);
                return (
                  <label
                    key={s.id}
                    className="flex cursor-pointer items-start gap-2 rounded-sm px-2 py-1 text-xs hover:bg-muted"
                  >
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={(e) => {
                        if (e.target.checked) {
                          setSkillsToEncourage([...skillsToEncourage, s.name]);
                        } else {
                          setSkillsToEncourage(skillsToEncourage.filter((x) => x !== s.name));
                        }
                      }}
                      className="mt-0.5"
                    />
                    <span className="flex flex-col">
                      <span className="font-mono text-foreground">{s.name}</span>
                      {s.description ? (
                        <span className="text-[10px] text-muted-foreground">
                          {s.description}
                        </span>
                      ) : null}
                    </span>
                  </label>
                );
              })
            )}
          </div>
        </fieldset>

        <div className="flex items-center gap-3">
          <button
            type="button"
            disabled={!canSubmit}
            onClick={onSubmit}
            className="inline-flex items-center gap-2 rounded-md bg-emerald-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-emerald-700 disabled:cursor-not-allowed disabled:opacity-50"
          >
            {runMut.isPending ? (
              <Loader2 className="size-4 animate-spin" aria-hidden />
            ) : (
              <Network className="size-4" aria-hidden />
            )}
            {t(($) => $.submit_run)}
          </button>
          {runMut.isError ? (
            <span className="inline-flex items-center gap-1 text-xs text-destructive">
              <AlertTriangle className="size-3.5" aria-hidden />
              {runMut.error?.message ?? "error"}
            </span>
          ) : null}
        </div>

        {result ? <RunResultCard result={result} /> : null}

        {/* 0.3.55: finished runs for the bound issue. Pre-0.3.55 the
            coda result was only the in-session POST /run response, so
            a completed run vanished on unmount. The server now returns
            durable run rows (problem / iterations / coda_conclusions /
            final_issue_id) via GET /api/issues/{id}/mythos-runs; this
            panel lists them so the finished result stays visible. */}
        <PastRunsPanel rootIssueId={rootIssueId} wsId={wsId} />
      </div>
    </section>
  );
}

// MythosPastRun mirrors the 0.3.55-enriched server MythosRunSummary
// envelope (handler/mythos_supervise.go). coda_conclusions is the
// durable JSONB the coda phase writes; final_issue_id is where the
// run's conclusion issue landed.
type MythosPastRun = {
  run_id: string;
  status: string;
  mode: string;
  started_at: string;
  problem: string;
  iterations: number;
  completed_at?: string;
  final_issue_id?: string;
  coda_conclusions?: Array<{ key: string; value: string; confidence?: number; actionable?: boolean }>;
};

function PastRunsPanel({ rootIssueId, wsId }: { rootIssueId: string | null; wsId: string }) {
  const [runs, setRuns] = useState<MythosPastRun[]>([]);
  const [expandedId, setExpandedId] = useState<string | null>(null);

  useEffect(() => {
    if (!rootIssueId || !wsId) {
      setRuns([]);
      setExpandedId(null);
      return;
    }
    let cancelled = false;
    api
      .rawRequest(
        `/api/issues/${encodeURIComponent(rootIssueId)}/mythos-runs?workspace_id=${encodeURIComponent(wsId)}`,
        { method: "GET" },
      )
      .then(async (res) =>
        res.ok ? ((await res.json()) as MythosPastRun[]) : [],
      )
      .then((data) => {
        if (!cancelled) setRuns(Array.isArray(data) ? data : []);
      })
      .catch(() => {
        if (!cancelled) setRuns([]);
      });
    return () => {
      cancelled = true;
    };
  }, [rootIssueId, wsId]);

  if (!rootIssueId) return null;

  const expanded = runs.find((r) => r.run_id === expandedId) ?? null;

  return (
    <div className="mt-4 rounded-md border border-border bg-card/30 p-4">
      <div className="flex items-baseline justify-between gap-3">
        <h3 className="text-sm font-semibold text-foreground">历史运行</h3>
        <span className="text-[11px] text-muted-foreground">共 {runs.length} 次</span>
      </div>
      {runs.length === 0 ? (
        <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
          本 issue 还没有运行记录。在上面发起一次蜂群推演,完成后结果会存到这里,关掉页面也不会丢。
        </p>
      ) : (
        <div className="mt-3 flex flex-col gap-1.5">
          {runs.map((run) => (
            <button
              key={run.run_id}
              type="button"
              onClick={() =>
                setExpandedId((cur) => (cur === run.run_id ? null : run.run_id))
              }
              className={`flex items-center justify-between gap-2 rounded border px-2.5 py-1.5 text-left text-xs transition-colors ${
                expandedId === run.run_id
                  ? "border-primary/40 bg-primary/5"
                  : "border-border/60 hover:bg-accent/50"
              }`}
            >
              <span className="min-w-0 truncate text-foreground/90">
                {run.problem || run.run_id}
              </span>
              <span className="flex shrink-0 items-center gap-2 text-muted-foreground">
                <span className="rounded bg-muted px-1 py-0.5 text-[10px]">{run.mode}</span>
                <span className="font-mono">{run.iterations} 轮</span>
                <span className={run.status === "done" ? "text-emerald-600 dark:text-emerald-400" : ""}>
                  {run.status}
                </span>
              </span>
            </button>
          ))}
        </div>
      )}
      {expanded ? (
        <div className="mt-3 border-t border-border/60 pt-3">
          {Array.isArray(expanded.coda_conclusions) && expanded.coda_conclusions.length > 0 ? (
            <dl className="grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
              {expanded.coda_conclusions.map((c, i) => (
                <span key={`${c.key}-${i}`} className="contents">
                  <dt className="font-mono text-foreground">{c.key}:</dt>
                  <dd className="text-foreground">{c.value}</dd>
                </span>
              ))}
            </dl>
          ) : (
            <p className="text-xs text-muted-foreground">
              {expanded.status === "done" ? "该运行未写入结论。" : `运行状态:${expanded.status}`}
            </p>
          )}
          {expanded.final_issue_id ? (
            <p className="mt-2 font-mono text-[11px] text-muted-foreground">
              结论落在 issue {expanded.final_issue_id}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function RunResultCard({ result }: { result: MythosRunResult }) {
  const { t } = useT("mythos");
  return (
    <div className="mt-2 rounded-md border border-emerald-300 bg-emerald-50/40 p-4 text-xs dark:border-emerald-800 dark:bg-emerald-950/20">
      <div className="flex flex-wrap items-center gap-3 text-foreground">
        <span className="font-mono text-[10px] uppercase tracking-wide text-muted-foreground">
          {t(($) => $.result_run_id)}
        </span>
        <span className="font-mono">{result.run_id}</span>
        <span className="ml-auto inline-flex items-center gap-3 text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <Users className="size-3" aria-hidden />
            {t(($) => $.result_iterations)}
            <span className="font-mono">{result.iterations_run}</span>
          </span>
          <span className="inline-flex items-center gap-1">
            <Sparkles className="size-3" aria-hidden />
            {t(($) => $.result_reflections)}
            <span className="font-mono">{result.reflection_count ?? 0}</span>
          </span>
        </span>
      </div>
      <p className="mt-3 whitespace-pre-wrap text-sm leading-relaxed">
        {result.coda_summary}
      </p>
      {Array.isArray(result.coda_conclusions) && result.coda_conclusions.length > 0 ? (
        <div className="mt-3 border-t border-emerald-200 pt-3 dark:border-emerald-800">
          <h3 className="flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wide text-emerald-800 dark:text-emerald-300">
            <BookOpen className="size-3" aria-hidden />
            {t(($) => $.result_conclusions_title)}
          </h3>
          <dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-2 gap-y-1 text-xs">
            {result.coda_conclusions.map((c, i) => (
              <span key={`${c.key}-${i}`} className="contents">
                <dt className="font-mono text-foreground">{c.key}:</dt>
                <dd className="text-foreground">{c.value}</dd>
              </span>
            ))}
          </dl>
        </div>
      ) : null}
    </div>
  );
}
