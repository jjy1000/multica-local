"use client";

// claude-issue-embed — the issue-main-pane Claude Lab embed (0.5.114),
// mounted by issue-detail.tsx below the comment stream and above the
// composer, mirroring the PythiaIssueEmbed slot. Shows the live/last
// experiment status plus the sandbox artifact stream as it lands
// (figures inline for the newest few, rows for the rest). Collapses to
// null when the issue has no lab tasks AND no artifacts — same
// 0-runs-ready law as pythia.
//
// Deliberately NO deliverable markdown: the research run's report
// arrives through the standard agent-task comment path (and
// HidesDeliverableInIssueTimeline keeps it out of the way) — rendering
// it here would duplicate the timeline, the exact double-delivery the
// pythia embed removed in b69c24364.

import { useEffect, useState } from "react";
import {
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
import { useClaudeLabIssue } from "../../hooks/use-claude-lab-issue";
import { useT } from "../../../i18n";

const INLINE_PREVIEW_KINDS = new Set(["png", "svg", "jpg", "jpeg"]);
const MAX_INLINE_PREVIEWS = 4;
const MAX_ROWS = 20;

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
  const { tasks, status, hasLive, artifacts } = useClaudeLabIssue(wsId, issueId);

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

  // 0-runs-ready law (pythia analogue): nothing to show → no card.
  if (!tasks.length && artifacts.length === 0) return null;

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
          <span className="ml-1 text-[10px] text-muted-foreground">
            {t(($) => $.claude_lab.embed_status_done)}
          </span>
        )}
      </div>
      <div className="space-y-2 px-3 py-2">
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
              return (
                <li key={a.id} className="flex items-center gap-2 text-xs">
                  <Icon className="size-3.5 shrink-0 text-sky-600 dark:text-sky-300" aria-hidden />
                  <span className="min-w-0 flex-1 truncate text-foreground">{a.name}</span>
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
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="text-xs text-muted-foreground">
            {t(($) => $.claude_lab.embed_artifacts_empty)}
          </p>
        )}
      </div>
    </div>
  );
}
