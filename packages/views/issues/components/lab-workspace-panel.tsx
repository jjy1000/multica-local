"use client";

// LabWorkspacePanel — issue-detail sidebar indicator that the bound
// lab is live for this issue. 0.3.33.
//
// History:
//   - 0.3.31: this component mounted the lab's full inline view
//     (recharts / pythia SSE / claude-lab tabs) inside the issue
//     detail right column. The user feedback was that it crowded the
//     issue detail chrome (the inline view grew to ~600px tall and
//     blocked the description / activity panel).
//
//   - 0.3.33 hard constraint: keep the chrome here to a single,
//     compact "lab bound for issue N" card with a `打开实验室面板 →`
//     jump link to `/experimental/<suffix>`. The detailed view
//     (Plan / Forecast / Council / Artifact / Knowledge tabs) lives
//     exclusively on the workspace-scoped `/experimental/<suffix>`
//     route. Clicking the link takes the user to the right place
//     with the issue pre-scoped; we don't duplicate the chrome here.
//
// We still keep `renderInline` as an opt-in render-prop because
// desktop might want a one-line teaser (council verdict / forecast
// probability) inside the card. When omitted, we render a plain
// "bound, click to open" card and nothing else.

import { useExperimentalFlags } from "@multica/core/experimental";
import { useT } from "../../i18n";
import { FlaskConical, ExternalLink } from "lucide-react";
import type { ReactNode } from "react";
import { AppLink } from "../../navigation";

export interface LabWorkspacePanelProps {
  issueId: string;
  labSource: string;
  /** Optional one-line teaser rendered inside the compact card.
   *  Receives `issueId`; the heavy lab visualization lives at
   *  `/experimental/<suffix>`, not here. Keep this small
   *  (single line / a probability meter at most) — anything more
   *  defeats the 0.3.33 "compact chrome" goal. */
  renderInline?: (issueId: string) => ReactNode;
  /** Workspace-scoped lab route suffix (e.g. "claude-lab"). When
   *  present, the card surfaces a "打开实验室面板 →" jump link
   *  that takes the user to the full lab view with the bound
   *  issue pre-scoped. Resolved from `IssueLabsSection`'s
   *  `labSourceRouteSuffix` table so both views stay in sync. */
  routeSuffix?: string;
}

export function LabWorkspacePanel({
  issueId,
  labSource,
  renderInline,
  routeSuffix,
}: LabWorkspacePanelProps) {
  const { t } = useT("issues");
  const { data: flags } = useExperimentalFlags();

  // Flag-off is a complete bypass — no chrome, no children. The lab's
  // own view module short-circuits inside its own component, but
  // gating here too keeps the issue detail surface clean when the
  // user disables the lab after tagging an issue.
  const flag = (flags ?? []).find((f) => f.key === labSource && f.enabled);
  if (!flag) return null;

  // Localized flag title — prefer zh (since most users run zh-Hans
  // as their primary locale and the catalog is bilingual), fall
  // back to en, then the raw key. Avoids leaking the internal
  // flag identifier (e.g. "claude_science_lab") into the chrome
  // when a friendly title is available.
  const flagTitle = flag.title.zh || flag.title.en || labSource;
  const fullRoute = routeSuffix ? `/experimental/${routeSuffix}` : null;

  return (
    <section
      aria-labelledby="lab-workspace-title"
      data-issue-id={issueId}
      data-lab-source={labSource}
      className="rounded-lg border border-purple-500/20 bg-purple-500/5 px-3 py-2.5"
    >
      <div className="flex items-center justify-between gap-2">
        <div className="flex min-w-0 items-center gap-1.5">
          <FlaskConical className="size-3.5 shrink-0 text-purple-500" />
          <h3
            id="lab-workspace-title"
            className="truncate text-xs font-medium text-purple-700 dark:text-purple-300"
          >
            {flagTitle}
          </h3>
          <span className="shrink-0 text-[10px] uppercase tracking-wide text-muted-foreground">
            {t(($) => $.lab_section.bound_for_issue)}
          </span>
        </div>
        {fullRoute ? (
          <AppLink
            href={fullRoute}
            aria-label={t(($) => $.lab_section.open_panel)}
            className="inline-flex shrink-0 items-center gap-1 rounded-md border border-purple-500/30 px-2 py-0.5 text-[11px] font-medium text-purple-700 hover:bg-purple-500/10 dark:text-purple-300"
          >
            {t(($) => $.lab_section.open_panel)}
            <ExternalLink className="size-3" aria-hidden />
          </AppLink>
        ) : null}
      </div>
      {renderInline ? (
        <div className="mt-2 text-[11px] leading-snug text-muted-foreground">
          {renderInline(issueId)}
        </div>
      ) : null}
    </section>
  );
}

