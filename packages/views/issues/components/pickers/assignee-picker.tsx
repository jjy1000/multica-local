"use client";

import { useCallback, useMemo, useState } from "react";
import { FlaskConical, Lock, UserMinus } from "lucide-react";
import type { Agent, IssueAssigneeType, UpdateIssueRequest } from "@multica/core/types";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "@multica/core/auth";
import { canAssignAgentToIssue } from "@multica/core/permissions";
import { useActorName } from "@multica/core/workspace/hooks";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberListOptions, agentListOptions, squadListOptions, assigneeFrequencyOptions } from "@multica/core/workspace/queries";
import { ActorAvatar } from "../../../common/actor-avatar";
import {
  PropertyPicker,
  PickerItem,
  PickerSection,
  PickerEmpty,
} from "./property-picker";
import { useT } from "../../../i18n";
import { matchesPinyin } from "../../../editor/extensions/pinyin-match";

/**
 * Legacy boolean shape kept around for callers (e.g. `use-issue-actions.ts`)
 * that haven't migrated to the new `canAssignAgentToIssue` Decision API yet.
 * Internally redirects to the canonical rule so behaviour stays in sync.
 */
export function canAssignAgent(
  agent: Agent,
  userId: string | undefined,
  memberRole: string | undefined,
): boolean {
  return canAssignAgentToIssue(agent, {
    userId: userId ?? null,
    role: memberRole === "owner" || memberRole === "admin" || memberRole === "member"
      ? memberRole
      : null,
  }).allowed;
}

export function AssigneePicker({
  assigneeType,
  assigneeId,
  mixed = false,
  onUpdate,
  trigger: customTrigger,
  triggerRender,
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  align,
  /**
   * Lab mutex: when a `lab_source` is set on the issue, the lab owns the
   * agent roster and the user MUST NOT pick a separate actor. Disabled
   * the entire picker surface (no click, no popover) and surfaces the
   * reason via tooltip. Set by create-issue + issue-detail when the
   * parallel LabPicker is non-empty. Caller is responsible for clearing
   * the lab first if they want to re-pick an actor.
   */
  lockedReason,
}: {
  assigneeType: IssueAssigneeType | null;
  assigneeId: string | null;
  /**
   * `true` when a batch selection spans different assignees ("mixed"): no row
   * is checked, including the unassigned row. Distinct from `assigneeType` /
   * `assigneeId` both being `null`, which means every selected issue is
   * genuinely unassigned and the unassigned row should be checked.
   */
  mixed?: boolean;
  onUpdate: (updates: Partial<UpdateIssueRequest>) => void;
  trigger?: React.ReactNode;
  triggerRender?: React.ReactElement;
  open?: boolean;
  onOpenChange?: (v: boolean) => void;
  align?: "start" | "center" | "end";
  lockedReason?: string;
}) {
  const { t } = useT("issues");
  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = controlledOnOpenChange ?? setInternalOpen;
  const [filter, setFilter] = useState("");
  const user = useAuthStore((s) => s.user);
  const wsId = useWorkspaceId();
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));
  const { data: squads = [] } = useQuery(squadListOptions(wsId));
  const { data: frequency = [] } = useQuery(assigneeFrequencyOptions(wsId));
  const { getActorName } = useActorName();

  const currentMember = members.find((m) => m.user_id === user?.id);
  const memberRole = currentMember?.role;

  // Build a lookup map from frequency data for sorting.
  const freqMap = useMemo(() => {
    const map = new Map<string, number>();
    for (const entry of frequency) {
      map.set(`${entry.assignee_type}:${entry.assignee_id}`, entry.frequency);
    }
    return map;
  }, [frequency]);

  const getFreq = (type: string, id: string) => freqMap.get(`${type}:${id}`) ?? 0;

  const query = filter.trim().toLowerCase();
  const filteredMembers = members
    .filter((m) => m.name.toLowerCase().includes(query) || matchesPinyin(m.name, query))
    .sort((a, b) => getFreq("member", b.user_id) - getFreq("member", a.user_id));
  const filteredAgents = agents
    .filter((a) => !a.archived_at && (a.name.toLowerCase().includes(query) || matchesPinyin(a.name, query)))
    .sort((a, b) => getFreq("agent", b.id) - getFreq("agent", a.id));
  const filteredSquads = squads
    .filter((s) => !s.archived_at && (s.name.toLowerCase().includes(query) || matchesPinyin(s.name, query)))
    .sort((a, b) => getFreq("squad", b.id) - getFreq("squad", a.id));

  const isSelected = (type: string, id: string) =>
    assigneeType === type && assigneeId === id;

  const triggerLabel =
    assigneeType && assigneeId
      ? getActorName(assigneeType, assigneeId)
      : t(($) => $.pickers.assignee.trigger_unassigned);

  // Lab mutex — when locked, refuse to open the popover entirely.
  // The trigger still renders so the assignee value stays visible
  // (showing the actor that the lab promoted), but the click path
  // is short-circuited and the trigger is grayed out with a design-
  // system tooltip explaining how to re-enable picks (clear the
  // lab). The disabled cursor + opacity is applied on the wrapper
  // span below so it works for ALL three trigger modes
  // (customTrigger / triggerRender / default chrome) — earlier
  // versions only layered the lock styling onto the triggerRender
  // clone path, which silently dropped the affordance on the
  // inline PropRow (default chrome) and on customTrigger callers.
  const isLocked = !!lockedReason;
  // Chain, don't overwrite: a future caller may attach their own
  // onClick to the trigger (e.g. a "view assignee profile" affordance).
  // Earlier versions OVERWROTE prev.onClick inside the clone, which
  // silently swallowed any parent intent. The wrapper below calls
  // the parent's handler first, then preventDefault / stopPropagation
  // to keep the popover closed. If the parent already called
  // preventDefault we honor that and skip the lock override.
  const wrapLocked = useCallback(
    (parent?: (e: React.MouseEvent) => void) =>
      (e: React.MouseEvent) => {
        if (parent) {
          parent(e);
          if (e.defaultPrevented) return;
        }
        e.preventDefault();
        e.stopPropagation();
      },
    [],
  );

  return (
    <PropertyPicker
      open={isLocked ? false : open}
      onOpenChange={(v: boolean) => {
        if (isLocked) return;
        setOpen(v);
        if (!v) setFilter("");
      }}
      width="w-64"
      align={align}
      searchable
      searchPlaceholder={t(($) => $.pickers.assignee.search_placeholder)}
      onSearchChange={setFilter}
      tooltip={lockedReason}
      triggerRender={
        isLocked && triggerRender
          ? // Reuse the caller's triggerRender but layer on the lock
            // treatment: gray-out + cursor + disabled. We CHAIN the
            // parent's onClick (don't overwrite) via wrapLocked so
            // a future caller-supplied handler keeps running. We do
            // NOT set the native `title` attribute here — the design-
            // system Tooltip on the surrounding PropertyPicker already
            // surfaces the same message, and a stacked native + DS
            // tooltip renders both at once.
            (() => {
              const el = triggerRender as React.ReactElement<{
                onClick?: (e: React.MouseEvent) => void;
                className?: string;
                disabled?: boolean;
              }> | undefined;
              if (!el) return undefined;
              const prev = el.props ?? {};
              return {
                ...el,
                props: {
                  ...prev,
                  onClick: wrapLocked(prev.onClick),
                  disabled: true,
                  className: [
                    prev.className,
                    "opacity-50 cursor-not-allowed",
                  ]
                    .filter(Boolean)
                    .join(" "),
                },
              };
            })()
          : triggerRender
      }
      trigger={
        // The wrapper span exists for ONE purpose: to carry the
        // locked-treatment visual (opacity / cursor) on the
        // default-chrome path. Earlier versions relied on the
        // triggerRender clone to add these, but PropRow and any
        // customTrigger caller never went through that clone, so
        // the locked state had no visual affordance at all. The
        // span is only mounted when isLocked; the un-locked
        // path returns the original chrome so the un-locked
        // visual is byte-for-byte unchanged.
        isLocked ? (
          <span
            aria-disabled
            className="flex items-center gap-1.5 cursor-not-allowed opacity-60"
            onClick={wrapLocked(
              // customTrigger is a ReactNode — it may be an element
              // carrying an onClick (rare). We don't try to deep-
              // extract that handler; we just defensively guard
              // against a click bubbling up to the popover anchor.
              typeof customTrigger === "object" &&
                customTrigger !== null &&
                "props" in customTrigger
                ? (customTrigger as { props?: { onClick?: (e: React.MouseEvent) => void } })
                    .props?.onClick
                : undefined,
            )}
          >
            {assigneeType && assigneeId ? (
              <ActorAvatar
                actorType={assigneeType}
                actorId={assigneeId}
                size={18}
                enableHoverCard
                showStatusDot
              />
            ) : null}
            <span className="truncate text-muted-foreground">{triggerLabel}</span>
          </span>
        ) : customTrigger ? customTrigger : assigneeType && assigneeId ? (
          <>
            <ActorAvatar actorType={assigneeType} actorId={assigneeId} size={18} enableHoverCard showStatusDot />
            <span className="truncate">{triggerLabel}</span>
          </>
        ) : (
          <span className="text-muted-foreground">{t(($) => $.pickers.assignee.trigger_unassigned)}</span>
        )
      }
    >
      {/* Unassigned option — hidden when search is active */}
      {!query && (
        <PickerItem
          selected={!mixed && !assigneeType && !assigneeId}
          onClick={() => {
            onUpdate({ assignee_type: null, assignee_id: null });
            setOpen(false);
          }}
        >
          <UserMinus className="h-3.5 w-3.5 text-muted-foreground" />
          <span className="text-muted-foreground">{t(($) => $.pickers.assignee.trigger_unassigned)}</span>
        </PickerItem>
      )}

      {/* Members */}
      {filteredMembers.length > 0 && (
        <PickerSection label={t(($) => $.pickers.assignee.members_group)}>
          {filteredMembers.map((m) => (
            <PickerItem
              key={m.user_id}
              selected={isSelected("member", m.user_id)}
              onClick={() => {
                onUpdate({
                  assignee_type: "member",
                  assignee_id: m.user_id,
                });
                setOpen(false);
              }}
            >
              <ActorAvatar actorType="member" actorId={m.user_id} size={18} />
              <span className="truncate">{m.name}</span>
            </PickerItem>
          ))}
        </PickerSection>
      )}

      {/* Agents */}
      {filteredAgents.length > 0 && (
        <PickerSection label={t(($) => $.pickers.assignee.agents_group)}>
          {filteredAgents.map((a) => {
            const decision = canAssignAgentToIssue(a, {
              userId: user?.id ?? null,
              role:
                memberRole === "owner" ||
                memberRole === "admin" ||
                memberRole === "member"
                  ? memberRole
                  : null,
            });
            const allowed = decision.allowed;
            // 0.3.56: lab-managed agents are auto-dispatched by their lab and
            // must not be picked standalone — grey + disable + explain, same
            // affordance as the permission gate above. The row still renders
            // (not filtered out) so the lab/assignee model stays visible.
            const labLocked = a.lab_managed === true;
            const disabled = !allowed || labLocked;
            const tooltip = labLocked
              ? t(($) => $.pickers.assignee.lab_managed_tooltip)
              : !allowed
                ? decision.message
                : undefined;
            return (
              <PickerItem
                key={a.id}
                selected={isSelected("agent", a.id)}
                disabled={disabled}
                tooltip={tooltip}
                onClick={() => {
                  if (disabled) return;
                  onUpdate({
                    assignee_type: "agent",
                    assignee_id: a.id,
                  });
                  setOpen(false);
                }}
              >
                <ActorAvatar actorType="agent" actorId={a.id} size={18} showStatusDot />
                <span className={`truncate ${disabled ? "text-muted-foreground" : ""}`}>{a.name}</span>
                {labLocked ? (
                  <FlaskConical className="ml-auto h-3 w-3 text-muted-foreground" />
                ) : a.visibility === "private" ? (
                  <Lock className="ml-auto h-3 w-3 text-muted-foreground" />
                ) : null}
              </PickerItem>
            );
          })}
        </PickerSection>
      )}

      {/* Squads — group ownership; assigning to a squad routes the issue to
          its leader agent on the backend. */}
      {filteredSquads.length > 0 && (
        <PickerSection label={t(($) => $.pickers.assignee.squads_group)}>
          {filteredSquads.map((s) => {
            // 0.3.56: lab-owned squads (e.g. the Claude Science / Mythos
            // rosters) are reachable only by selecting the lab on an issue —
            // grey + disable them here so they can't be assigned standalone.
            const labLocked = s.lab_managed === true;
            return (
              <PickerItem
                key={s.id}
                selected={isSelected("squad", s.id)}
                disabled={labLocked}
                tooltip={
                  labLocked
                    ? t(($) => $.pickers.assignee.lab_managed_tooltip)
                    : undefined
                }
                onClick={() => {
                  if (labLocked) return;
                  onUpdate({
                    assignee_type: "squad",
                    assignee_id: s.id,
                  });
                  setOpen(false);
                }}
              >
                <ActorAvatar actorType="squad" actorId={s.id} size={18} />
                <span className={`truncate ${labLocked ? "text-muted-foreground" : ""}`}>{s.name}</span>
                {labLocked && (
                  <FlaskConical className="ml-auto h-3 w-3 text-muted-foreground" />
                )}
              </PickerItem>
            );
          })}
        </PickerSection>
      )}

      {filteredMembers.length === 0 &&
        filteredAgents.length === 0 &&
        filteredSquads.length === 0 &&
        filter && <PickerEmpty />}
    </PropertyPicker>
  );
}
