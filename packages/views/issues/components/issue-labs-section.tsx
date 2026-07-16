"use client";

import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, ExternalLink, FlaskConical, Loader2 } from "lucide-react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { agentTaskSnapshotOptions } from "@multica/core/agents";
import { useWorkspaceId } from "@multica/core/hooks";
import { AppLink } from "../../navigation";
import { useT } from "../../i18n";

// Hard-coded mapping from experimental flag key to its experimental view
// route suffix. Mirrors the `path: "experimental/<suffix>"` entries in
// apps/desktop/src/renderer/src/routes.tsx. Kept local because the wire
// shape (`ExperimentalFlag`) intentionally doesn't carry the route —
// sidebar entries already hold it, but the flag object's surface is
// trimmed for the LabPicker / settings surface. Update this map when
// adding a new lab view.
const FLAG_ROUTE_SUFFIX: Record<string, string> = {
  claude_science_lab: "claude-lab",
  pythia_oracle: "pythia",
  mythos_swarm: "mythos",
  llm_wiki_bridge: "llm-wiki",
  code_canvas: "code-canvas",
  agent_self_optimization: "agent-self-optimization",
  constitution_agent: "constitution-agent",
  chat_pin_ui: "chat-pin",
};

/**
 * Sidebar "Labs" section for issues that were tagged with a lab source.
 *
 * 0.3.29 — gives a tagged issue its own affordance to surface what
 * experimental surface it is associated with and a one-click route into
 * that lab's experimental view. Renders nothing when the issue has no
 * `lab_source` (the existing `Lab` PropRow in Properties already covers
 * the picker flow in that case).
 *
 * Three observable states:
 *
 * 1. Flag still enabled + an agent task is running on this issue:
 *    renders a "Running" dot + the flag's localized title + an
 *    `Open lab panel` AppLink.
 * 2. Flag still enabled + queued but not running:
 *    same chrome, dot downgrades to "Queued".
 * 3. Flag was disabled after tagging (the issue is keeping a leftover
 *    lab source): swaps the link for a "Lab no longer enabled"
 *    hint and an explanatory note to enable from Settings → Labs.
 */
export function IssueLabsSection({ issueId, labSource }: { issueId: string; labSource: string }) {
  const { t } = useT("issues");
  const wsId = useWorkspaceId();
  const { data: flags } = useExperimentalFlags();
  const { data: snapshot = [] } = useQuery(agentTaskSnapshotOptions(wsId));

  const [open, setOpen] = useState(true);

  // Look up the flag title once. The picker already does this dance but
  // the layout here is dense — keep it inline.
  const flagTitle = useMemo(() => {
    const f = (flags ?? []).find((flag) => flag.key === labSource);
    if (!f) return null;
    return f.title.zh || f.title.en;
  }, [flags, labSource]);

  const suffix = FLAG_ROUTE_SUFFIX[labSource];
  const labEnabled = (flags ?? []).some((f) => f.key === labSource && f.enabled);

  // Drive the running indicator off the same agent-task snapshot the
  // header chip + IssueAgentActivityIndicator use. This is the live
  // workspace-wide stream, so opening a different issue does not need
  // any extra wiring — WS invalidation handles refresh transparently.
  const live = useMemo(() => {
    let running = false;
    let queued = false;
    for (const task of snapshot) {
      if (task.issue_id !== issueId) continue;
      if (task.status === "running") running = true;
      else if (
        task.status === "queued" ||
        task.status === "dispatched" ||
        task.status === "waiting_local_directory"
      ) {
        queued = true;
      }
    }
    return { running, queued };
  }, [snapshot, issueId]);

  const indicator = live.running
    ? {
        tone: "running" as const,
        label: t(($) => $.lab_section.running_indicator),
        title: t(($) => $.lab_section.running_tooltip),
      }
    : live.queued
      ? {
          tone: "queued" as const,
          label: t(($) => $.lab_section.queued_indicator),
          title: t(($) => $.lab_section.queued_tooltip),
        }
      : null;

  return (
    <div>
      <button
        type="button"
        aria-expanded={open}
        className={`flex w-full items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors mb-2 hover:bg-accent/70 ${open ? "" : "text-muted-foreground hover:text-foreground"}`}
        onClick={() => setOpen((v) => !v)}
      >
        <FlaskConical className="h-3 w-3 shrink-0 text-purple-500" />
        <span>{t(($) => $.lab_section.section_title)}</span>
        <ChevronRight className={`!size-3 shrink-0 stroke-[2.5] text-muted-foreground transition-transform ${open ? "rotate-90" : ""}`} />
      </button>
      {open && (
        <div className="space-y-1.5 pl-2">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-xs text-foreground/90">
              {flagTitle ?? labSource}
            </span>
            {indicator && (
              <span
                className="inline-flex shrink-0 items-center gap-1 rounded-full bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-medium text-emerald-700 dark:text-emerald-300"
                title={indicator.title}
              >
                {indicator.tone === "running" ? (
                  <Loader2 className="size-2.5 animate-spin" aria-hidden />
                ) : (
                  <span className="size-1.5 rounded-full bg-emerald-500" aria-hidden />
                )}
                {indicator.label}
              </span>
            )}
          </div>
          {suffix && labEnabled ? (
            <AppLink
              href={`/experimental/${suffix}`}
              className="inline-flex items-center gap-1 text-xs text-purple-600 hover:text-purple-700 dark:text-purple-400 dark:hover:text-purple-300 transition-colors"
            >
              <ExternalLink className="size-3" />
              {t(($) => $.lab_section.open_panel)}
            </AppLink>
          ) : (
            <div className="rounded-md border border-dashed border-border/60 px-2 py-1.5 text-[11px] text-muted-foreground">
              <p className="font-medium text-foreground/80">
                {t(($) => $.lab_section.no_flag_title)}
              </p>
              <p className="mt-0.5 leading-snug">{t(($) => $.lab_section.no_flag_hint)}</p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
