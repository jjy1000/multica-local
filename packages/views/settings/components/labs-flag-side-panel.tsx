"use client";

import { FlaskConical } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { useT } from "../../i18n";

// Renders the PR 7 side panel that appears under each installable
// Labs flag. Shows the lab's current state (installed / hidden / off),
// the workspace slug the install bound to, and a per-resource-type
// (visible/total) count breakdown.
//
// The panel is intentionally a pure projection of the install manifest
// shape returned by GET /api/experimental-flags → flag.installation.
// It does NOT fetch additional data, does NOT mutate state, and does
// NOT render when `installation` is undefined (e.g. for non-
// installable flags like chat_pin_ui). This keeps the side panel a
// thin decoration on the existing flag toggle and avoids a second
// round-trip per Labs tab render.

interface LabsFlagSidePanelProps {
  // Loosely-typed to match zod's `.loose()` parse result, which carries
  // an index signature so unknown future server fields don't break the
  // renderer. The flag.installation field comes straight off
  // useExperimentalFlags() → listExperimentalFlags() → parseWithFallback.
  installation: { source: string; installed: boolean; hidden: boolean; counts: Array<{ resource_type: string; total: number; visible: number }>; recent_activity?: { installed_workspace_slug?: string; tasks_last_24h: number; agent_runs_last_24h: number; last_activity_at?: string } } | undefined;
  flagEnabled: boolean;
}

const RESOURCE_TYPE_LABEL: Record<string, string> = {
  workspace: "Workspace",
  skill: "Skills",
  agent: "Agents",
  squad: "Squads",
  member: "Members",
  mcp_server: "MCP",
};

function labelForResourceType(key: string): string {
  // Translate known labels; fall back to the raw key for unknown
  // resource types so future catalog growth never blanks the panel.
  return RESOURCE_TYPE_LABEL[key] ?? key;
}

export function LabsFlagSidePanel({ installation, flagEnabled }: LabsFlagSidePanelProps) {
  const { t } = useT("settings");

  // Render nothing for non-installable flags (e.g. chat_pin_ui).
  // The toggle above still works — this component just adds context
  // for labs that ship with backing resources.
  if (!installation) {
    return null;
  }

  // No-install state: the flag is either off or has never been
  // installed. Surface a single hint line so the user understands
  // what opening the toggle will do.
  if (!installation.installed || !flagEnabled) {
    const statusKey = installation.installed && !flagEnabled
      ? "side_panel_status_hidden"
      : "side_panel_status_off";
    return (
      <div className="mt-2 ml-1 flex items-center gap-2 text-xs text-muted-foreground">
        <FlaskConical className="h-3 w-3" />
        <span>{t(($) => $.labs[statusKey])}</span>
      </div>
    );
  }

  return (
    <div className="mt-3 space-y-2 border-l-2 border-muted pl-3">
      {/* Status row */}
      <div className="flex items-center gap-2 text-xs">
        <span className="rounded bg-emerald-100 px-1.5 py-0.5 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300">
          {t(($) => $.labs.side_panel_status_installed)}
        </span>
        {installation.hidden ? (
          <span className="rounded bg-amber-100 px-1.5 py-0.5 text-amber-700 dark:bg-amber-900/30 dark:text-amber-300">
            {t(($) => $.labs.side_panel_status_hidden)}
          </span>
        ) : null}
      </div>

      {/* Workspace row */}
      {installation.recent_activity?.installed_workspace_slug ? (
        <div className="text-xs text-muted-foreground">
          <span className="font-mono">{installation.recent_activity.installed_workspace_slug}</span>
        </div>
      ) : null}

      {/* Resource counts */}
      {installation.counts.length > 0 ? (
        <Card className="bg-muted/30">
          <CardContent className="space-y-1 p-3">
            <div className="text-[10px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.labs.side_panel_resources_label)}
            </div>
            {installation.counts.map((c) => (
              <div
                key={c.resource_type}
                className="flex items-baseline justify-between text-xs"
              >
                <span className="text-muted-foreground">
                  {labelForResourceType(c.resource_type)}
                </span>
                <span className="font-mono">
                  {t(($) => $.labs.side_panel_count_label)
                    .replace("{{visible}}", String(c.visible))
                    .replace("{{total}}", String(c.total))}
                </span>
              </div>
            ))}
          </CardContent>
        </Card>
      ) : (
        <Skeleton className="h-12 w-full" />
      )}
    </div>
  );
}