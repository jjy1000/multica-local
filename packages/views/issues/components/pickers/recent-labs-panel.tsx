"use client";

// RecentLabsPanel — 0.5.4 info panel rendered inside the LabPicker
// popover when the user clicks the `agent_creation_studio` entry.
//
// Purpose: surface the workspace's recent agent / skill / squad authoring
// history so the user can see what the Agent Creation expert has already
// produced, plus a one-line hint about what this lab does. The panel is
// READ-ONLY — clicking a row is a no-op. Creating a new resource is a
// separate flow: the user picks the lab on an issue (or uses the issue
// create modal) and dispatches a task to `agent_creation_expert`, which
// then invokes `multica-creating-agents` / `multica-lab-builder` skills.
//
// Why the data is client-sorted: the workspace list endpoints already
// return every non-hidden row in a single payload (per the 0.3.30+ list
// filter contract). Adding `limit` / `order_by` query params would force
// a server-wide schema migration for the sake of a 5-item view; the
// client `.sort().slice(0, 5)` is cheap and stays cache-friendly (the
// query hooks are the same ones the sidebar / settings use, so the data
// is already warm).

import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Agent, Squad, SkillSummary } from "@multica/core/types";
import {
  agentListOptions,
  skillListOptions,
  squadListOptions,
} from "@multica/core/workspace/queries";
import { useT } from "../../../i18n";
import {
  PickerSection,
  PickerItem,
  PickerEmpty,
} from "./property-picker";

const TOP_N = 5;

interface RecentLabsPanelProps {
  /** Current workspace id; drives listAgents / listSkills / listSquads. */
  wsId: string;
  /** Optional close handler. LabPicker wires this to its "back to main list"
   *  affordance when present. Popover-level close is handled by the parent
   *  via PropertyPicker.handleOpenChange, not by this component. */
  onClose?: () => void;
}

function pickRecent<T extends { created_at: string }>(rows: T[] | undefined): T[] {
  if (!rows || rows.length === 0) return [];
  // Stable sort: most recent first. `Date` parse is fine for ISO strings
  // the server ships (always UTC, always `Z`-suffixed in Go's time.Time
  // JSON encoding).
  return [...rows]
    .sort((a, b) => +new Date(b.created_at) - +new Date(a.created_at))
    .slice(0, TOP_N);
}

function formatCreatedAt(iso: string, locale: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  // Use the user's browser locale; on desktop this matches the i18n
  // locale for the main product, on web the navigator language. The
  // resource was just created in this workspace so a date-only format
  // keeps the row compact.
  try {
    return new Intl.DateTimeFormat(locale ?? undefined, {
      year: "numeric",
      month: "short",
      day: "numeric",
    }).format(d);
  } catch {
    return iso;
  }
}

export function RecentLabsPanel({ wsId, onClose }: RecentLabsPanelProps) {
  const { t, i18n } = useT("issues");
  const locale = i18n.language;

  // Three parallel workspace-scoped queries. Each one independently
  // populates from the cache key already shared with sidebar / settings,
  // so opening this panel is effectively free on a warm workspace.
  const agentsQ = useQuery({
    ...agentListOptions(wsId),
    enabled: !!wsId,
    staleTime: 60 * 1000,
  });
  const skillsQ = useQuery({
    ...skillListOptions(wsId),
    enabled: !!wsId,
    staleTime: 60 * 1000,
  });
  const squadsQ = useQuery({
    ...squadListOptions(wsId),
    enabled: !!wsId,
    staleTime: 60 * 1000,
  });

  const recentAgents = useMemo(() => pickRecent(agentsQ.data as Agent[] | undefined), [agentsQ.data]);
  const recentSkills = useMemo(() => pickRecent(skillsQ.data as SkillSummary[] | undefined), [skillsQ.data]);
  const recentSquads = useMemo(() => pickRecent(squadsQ.data as Squad[] | undefined), [squadsQ.data]);

  const isAllEmpty =
    recentAgents.length === 0 &&
    recentSkills.length === 0 &&
    recentSquads.length === 0;

  const createdAtTemplate =
    t(($) => $.pickers.lab.recent_item_created_at) ?? "Created {date}";

  return (
    <div
      className="space-y-1.5"
      data-recent-labs-panel
      // data-lab-source lets the parent (LabPicker) tag this panel for
      // any future per-lab styling / test hooks without coupling to a
      // hard-coded component name.
      data-lab-source="agent_creation_studio"
    >
      <p className="px-1.5 pb-0.5 text-[11px] leading-snug text-muted-foreground">
        {t(($) => $.pickers.lab.recent_panel_hint) ??
          "View your recently created agents, skills, and squads. Selecting this lab dispatches new tasks to the Agent Creation expert."}
      </p>

      {isAllEmpty ? (
        <PickerEmpty />
      ) : (
        <>
          {recentAgents.length > 0 && (
            <PickerSection
              label={
                t(($) => $.pickers.lab.recent_section_agents) ?? "Agents"
              }
            >
              {recentAgents.map((agent) => (
                <PickerItem
                  key={`agent:${agent.id}`}
                  selected={false}
                  onClick={() => {
                    // Info-only row: no-op on click. The user creates
                    // new agents by dispatching a task to this lab, not
                    // by selecting an existing one here.
                  }}
                >
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate font-medium text-foreground">
                      {agent.name}
                    </span>
                    {agent.description && (
                      <span className="truncate text-[11px] text-muted-foreground">
                        {agent.description}
                      </span>
                    )}
                    <span className="text-[10px] text-muted-foreground/70">
                      {createdAtTemplate.replace(
                        "{date}",
                        formatCreatedAt(agent.created_at, locale),
                      )}
                    </span>
                  </span>
                </PickerItem>
              ))}
            </PickerSection>
          )}

          {recentSkills.length > 0 && (
            <PickerSection
              label={
                t(($) => $.pickers.lab.recent_section_skills) ?? "Skills"
              }
            >
              {recentSkills.map((skill) => (
                <PickerItem
                  key={`skill:${skill.id}`}
                  selected={false}
                  onClick={() => {
                    // Info-only.
                  }}
                >
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate font-medium text-foreground">
                      {skill.name}
                    </span>
                    {skill.description && (
                      <span className="truncate text-[11px] text-muted-foreground">
                        {skill.description}
                      </span>
                    )}
                    <span className="text-[10px] text-muted-foreground/70">
                      {createdAtTemplate.replace(
                        "{date}",
                        formatCreatedAt(skill.created_at, locale),
                      )}
                    </span>
                  </span>
                </PickerItem>
              ))}
            </PickerSection>
          )}

          {recentSquads.length > 0 && (
            <PickerSection
              label={
                t(($) => $.pickers.lab.recent_section_squads) ?? "Squads"
              }
            >
              {recentSquads.map((squad) => (
                <PickerItem
                  key={`squad:${squad.id}`}
                  selected={false}
                  onClick={() => {
                    // Info-only.
                  }}
                >
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="truncate font-medium text-foreground">
                      {squad.name}
                    </span>
                    {squad.description && (
                      <span className="truncate text-[11px] text-muted-foreground">
                        {squad.description}
                      </span>
                    )}
                    <span className="text-[10px] text-muted-foreground/70">
                      {createdAtTemplate.replace(
                        "{date}",
                        formatCreatedAt(squad.created_at, locale),
                      )}
                    </span>
                  </span>
                </PickerItem>
              ))}
            </PickerSection>
          )}
        </>
      )}

      {/* Optional back-to-list affordance. LabPicker passes onClose only
          when it wants this footer button; the popover-level X is the
          default escape hatch. */}
      {onClose && (
        <div className="border-t border-border/60 pt-1">
          <button
            type="button"
            data-recent-labs-panel-back
            onClick={onClose}
            className="flex w-full items-center justify-center rounded-md px-2 py-1 text-[11px] text-muted-foreground hover:bg-accent/60 hover:text-foreground transition-colors"
          >
            ← {t(($) => $.pickers.lab.recent_panel_back) ?? "← 返回实验插件"}
          </button>
        </div>
      )}
    </div>
  );
}