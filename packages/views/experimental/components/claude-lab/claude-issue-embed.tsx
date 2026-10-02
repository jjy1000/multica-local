"use client";

// claude-issue-embed — the issue-main-pane Claude Lab embed (0.5.114),
// mounted by issue-detail.tsx below the comment stream and above the
// composer, mirroring the PythiaIssueEmbed slot. Shows the live/last
// experiment status plus the sandbox artifact stream as it lands
// (figures inline for the newest few, rows for the rest). Collapses to
// null when the issue has no lab tasks AND no artifacts — same
// 0-runs-ready law as pythia.
//
// Deliberately NO deliverable markdown *comment*: the research run's
// report arrives as a TOP-LEVEL agent-task comment and renders in the
// timeline (0.5.124 narrowed hides_deliverable_in_issue_timeline to
// replies only, because 0.3.49.1's filter was also hiding the
// deliverable itself). Re-rendering it here would duplicate the
// timeline — the exact double-delivery the pythia embed removed in
// b69c24364.
//
// 0.5.126: what the embed DID lack was the other half of the delivery
// — the artifact payloads. `report.md` was a filename plus a download
// button and `results.csv` was "12 B", so reading a result meant
// leaving the issue. ArtifactInlineView now expands markdown reports,
// CSV tables and JSON in place (AIPOCH open-science reviews its
// generated reports "beside the conversation"); the report comment
// above and the payloads below are one delivery, not a duplicate.

import { useEffect, useState } from "react";
import {
  CheckCircle2,
  ChevronRight,
  Download,
  File as FileIcon,
  FileCode2,
  FileImage,
  FileJson,
  FileText,
  Loader2,
  TestTubes,
} from "lucide-react";
import { api } from "@multica/core/api";
import type { LabArtifactStub } from "@multica/core/api/schemas";
import { formatElapsedSecs } from "../../../chat/lib/format";
import { useClaudeLabIssue, type ClaudeLabRunStatus } from "../../hooks/use-claude-lab-issue";
import { ArtifactInlineView, hasInlineView } from "./artifact-inline-view";
import { useT } from "../../../i18n";

const INLINE_PREVIEW_KINDS = new Set(["png", "svg", "jpg", "jpeg"]);
const MAX_INLINE_PREVIEWS = 4;
const MAX_ROWS = 20;

// RunStatusStrip (0.5.131, open-science session-card pattern): a thin
// state bar under the embed header — shimmer sweep while running,
// amber pulse while queued, emerald fill on completion, amber/red on
// failure. CSS-only keyframes (media-query reduced-motion gate covers
// every consumer without per-component hooks); the <style> tag mounts
// only while a state that animates is showing.
const RUN_STRIP_STYLE = `
@keyframes claude-strip-shimmer{from{transform:translateX(-100%)}to{transform:translateX(100%)}}
.claude-strip-shimmer{animation:claude-strip-shimmer 1.4s linear infinite}
@keyframes claude-strip-pop{from{transform:scale(0.4);opacity:0}to{transform:scale(1);opacity:1}}
.claude-strip-pop{animation:claude-strip-pop 0.35s ease-out}
@media (prefers-reduced-motion: reduce){
  .claude-strip-shimmer,.claude-strip-pop{animation:none}
}`;

function RunStatusStrip({ status }: { status: ClaudeLabRunStatus }) {
  if (status === "idle") return null;
  const animate = status === "running" || status === "queued" || status === "completed";
  const fill =
    status === "running"
      ? "bg-sky-500/40"
      : status === "queued"
        ? "bg-amber-500/60 animate-pulse"
        : status === "completed"
          ? "bg-emerald-500/80"
          : "bg-amber-600/80";
  return (
    <div data-testid="claude-run-status-strip" data-status={status}>
      {animate ? <style>{RUN_STRIP_STYLE}</style> : null}
      <div className="relative h-0.5 w-full overflow-hidden">
        <div className={`h-full w-full ${fill}`} />
        {status === "running" && (
          <div className="absolute inset-0 overflow-hidden">
            <div className="claude-strip-shimmer h-full w-1/2 bg-gradient-to-r from-transparent via-sky-400/80 to-transparent" />
          </div>
        )}
      </div>
    </div>
  );
}

function artifactIcon(kind: string) {
  const k = kind.toLowerCase();
  if (INLINE_PREVIEW_KINDS.has(k)) return FileImage;
  if (k === "json") return FileJson;
  if (k === "csv" || k === "txt" || k === "md") return FileText;
  if (k === "py" || k === "html") return FileCode2;
  return FileIcon;
}

async function fetchArtifactBlobUrl(id: string): Promise<string> {
  const res = await api.rawRequest(
    `/api/experimental/claude-science-runtime/artifacts/${encodeURIComponent(id)}`,
  );
  if (!res.ok) throw new Error(`artifact ${res.status}`);
  const blob = await res.blob();
  return URL.createObjectURL(blob);
}

export function ClaudeIssueEmbed({
  wsId,
  issueId,
}: {
  wsId: string;
  issueId: string;
}) {
  const { t } = useT("experimental");
  const { tasks, latest, liveTask, status, hasLive, artifacts, isError, refetch } =
    useClaudeLabIssue(wsId, issueId);

  // Ticking clock for the live progress row — 1s cadence so the elapsed
  // counter visibly moves while a run is in flight; interval mounted only
  // while live (the AgentTaskSnapshot 5s poll supplies the state changes).
  const [now, setNow] = useState(() => Date.now());
  // 0.5.126: which artifact's CONTENT is expanded inline. Single-open on
  // purpose — a results bundle is usually report.md + results.csv +
  // figure.png, and opening all three at once turns the embed into a
  // page. Binary kinds (png/svg/html) never land here; they keep the
  // download row.
  const [expandedId, setExpandedId] = useState<string | null>(null);
  useEffect(() => {
    if (!hasLive) return;
    setNow(Date.now());
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, [hasLive]);

  const startedMs = Date.parse(liveTask?.started_at ?? liveTask?.created_at ?? "") || 0;
  const elapsedSecs =
    hasLive && startedMs ? Math.max(0, Math.round((now - startedMs) / 1000)) : 0;
  const attempt = liveTask?.attempt ?? 1;
  const liveLabel = !liveTask
    ? ""
    : liveTask.status !== "running" && liveTask.status !== "waiting_local_directory"
      ? t(($) => $.claude_lab.embed_queued)
      : latest?.status === "failed"
        ? t(($) => $.claude_lab.embed_retrying, { n: attempt })
        : attempt > 1
          ? t(($) => $.claude_lab.embed_attempt, { n: attempt })
          : t(($) => $.claude_lab.embed_working);

  // Inline previews: newest image-kind artifacts only, fetched through
  // rawRequest (auth headers; a bare <img src> would 401). Object URLs
  // are revoked on unmount / refresh.
  const imageIds = artifacts
    .filter((a) => INLINE_PREVIEW_KINDS.has(a.kind.toLowerCase()))
    .slice(0, MAX_INLINE_PREVIEWS);
  const [previews, setPreviews] = useState<Record<string, string>>({});
  useEffect(() => {
    let cancelled = false;
    const acquired: string[] = [];
    (async () => {
      const next: Record<string, string> = {};
      for (const a of imageIds) {
        try {
          const url = await fetchArtifactBlobUrl(a.id);
          if (cancelled) {
            URL.revokeObjectURL(url);
            return;
          }
          acquired.push(url);
          next[a.id] = url;
        } catch {
          // preview is decorative — a failed fetch just drops the image
        }
      }
      setPreviews(next);
    })();
    return () => {
      cancelled = true;
      for (const url of acquired) URL.revokeObjectURL(url);
    };
    // re-acquire when the image set changes (new artifacts land)
  }, [imageIds.map((a) => a.id).join(",")]); // eslint-disable-line react-hooks/exhaustive-deps

  async function download(a: LabArtifactStub) {
    const url = await fetchArtifactBlobUrl(a.id);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = a.name;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 5_000);
  }

  // 0-runs-ready law (pythia analogue): nothing to show → no card. A
  // failed fetch is NOT "nothing to show" — without this branch the
  // outage rendered exactly like the empty state (0.5.131).
  if (!tasks.length && artifacts.length === 0) {
    if (!isError) return null;
    return (
      <div
        className="mt-4 flex items-center justify-between gap-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-1.5"
        data-testid="claude-issue-embed-error"
      >
        <span className="text-[11px] text-destructive">
          {t(($) => $.claude_lab.embed_error)}
        </span>
        <button
          type="button"
          onClick={refetch}
          className="shrink-0 rounded-md border border-input bg-background px-2 py-0.5 text-[11px] font-medium text-foreground hover:bg-muted"
        >
          {t(($) => $.lab_output_panel.retry)}
        </button>
      </div>
    );
  }

  const rows = artifacts.slice(0, MAX_ROWS);

  return (
    <div
      className="mt-4 overflow-hidden rounded-lg border border-sky-500/30 bg-sky-500/5"
      data-testid="claude-issue-embed"
    >
      <div className="flex items-center gap-2 border-b border-sky-500/20 bg-sky-500/10 px-3 py-1.5">
        <TestTubes className="size-3.5 shrink-0 text-sky-600 dark:text-sky-300" aria-hidden />
        <span className="text-xs font-medium text-sky-700 dark:text-sky-300">
          {t(($) => $.claude_lab.embed_title)}
        </span>
        {hasLive && (
          <span className="ml-1 flex items-center gap-1 text-[10px] font-medium text-sky-700 dark:text-sky-300">
            <Loader2 className="size-2.5 animate-spin" aria-hidden />
            {t(($) => $.claude_lab.embed_running)}
          </span>
        )}
        {!hasLive && status === "completed" && (
          <span className="ml-1 flex items-center gap-0.5 text-[10px] text-muted-foreground">
            <CheckCircle2 className="claude-strip-pop size-2.5 text-emerald-500" aria-hidden />
            {t(($) => $.claude_lab.embed_status_done)}
          </span>
        )}
      </div>
      <RunStatusStrip status={status} />
      <div className="space-y-2 px-3 py-2">
        {hasLive && liveLabel && (
          <div className="flex items-center gap-2" data-testid="claude-embed-live-progress">
            <Loader2
              className="size-3 shrink-0 animate-spin text-sky-600 dark:text-sky-300"
              aria-hidden
            />
            <span className="animate-pulse truncate text-xs font-medium text-sky-700 dark:text-sky-300">
              {liveLabel}
            </span>
            {elapsedSecs > 0 && (
              <span className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground">
                {formatElapsedSecs(elapsedSecs)}
              </span>
            )}
          </div>
        )}
        {imageIds.length > 0 && (
          <div className="flex flex-wrap gap-2">
            {imageIds.map((a) => (
              <img
                key={a.id}
                src={previews[a.id]}
                alt={a.name}
                className="h-24 w-auto max-w-[40%] rounded border border-sky-500/20 bg-background object-contain"
                data-testid="claude-embed-preview"
              />
            ))}
          </div>
        )}
        {rows.length > 0 ? (
          <ul className="space-y-1" data-testid="claude-embed-artifacts">
            {rows.map((a) => {
              const Icon = artifactIcon(a.kind);
              const viewable = hasInlineView(a);
              const open = expandedId === a.id;
              return (
                <li key={a.id} className="text-xs">
                  <div className="flex items-center gap-2">
                    <Icon className="size-3.5 shrink-0 text-sky-600 dark:text-sky-300" aria-hidden />
                    {viewable ? (
                      <button
                        type="button"
                        onClick={() => setExpandedId(open ? null : a.id)}
                        aria-expanded={open}
                        data-testid="claude-embed-toggle"
                        className="flex min-w-0 flex-1 items-center gap-1 text-left hover:text-sky-700 dark:hover:text-sky-300"
                      >
                        <ChevronRight
                          className={`size-3 shrink-0 transition-transform ${open ? "rotate-90" : ""}`}
                          aria-hidden
                        />
                        <span className="truncate text-foreground">{a.name}</span>
                      </button>
                    ) : (
                      <span className="min-w-0 flex-1 truncate text-foreground">{a.name}</span>
                    )}
                    <span className="shrink-0 text-[10px] text-muted-foreground">
                      {a.bytes > 1024 ? `${(a.bytes / 1024).toFixed(1)} KB` : `${a.bytes} B`}
                    </span>
                    <button
                      type="button"
                      onClick={() => void download(a)}
                      className="inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] text-sky-700 hover:bg-sky-500/10 dark:text-sky-300"
                      aria-label={`${t(($) => $.claude_lab.embed_download)}: ${a.name}`}
                    >
                      <Download className="size-3" aria-hidden />
                      {t(($) => $.claude_lab.embed_download)}
                    </button>
                  </div>
                  {open && (
                    <div className="mt-1 pl-4.5">
                      <ArtifactInlineView artifact={a} />
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">
            {hasLive
              ? t(($) => $.claude_lab.embed_artifacts_pending)
              : t(($) => $.claude_lab.embed_artifacts_empty)}
          </p>
        )}
      </div>
    </div>
  );
}
