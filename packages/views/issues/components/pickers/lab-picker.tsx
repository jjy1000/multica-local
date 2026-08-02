"use client";

// LabPicker — 0.3.29 Lab flag picker for the issue detail Property row,
// extended in 0.3.31 with the mythos_swarm dual-mode tabs.
//
// Mirrors the shape of the other pickers in this folder (status /
// priority / assignee / label) so the Lab PropRow in issue-detail.tsx
// can drop in `<LabPicker ... />` without bespoke wiring.
//
// 0.3.29 hard constraint:
//   - Once a lab is selected, the user CANNOT pick a different agent
//     for that issue (the lab owns the agent roster).
//   - mythos_swarm is the unique exception: it lets the user add
//     extra agents beyond the canonical 5-agent roster, on top of
//     the lab's locked baseline roster.
//
// 0.3.31 dual-mode:
//   - When the picked lab is mythos_swarm, the user picks a *mode*
//     in addition to the lab. "sole" preserves the 0.3.30 behaviour
//     (mythos owns the issue end-to-end). "enhancer" lets the user
//     keep a manual assignee on the issue; mythos preludes +
//     supervises, the assignee executes.
//   - The mode picker is a second-level TabsList rendered BELOW
//     the lab list when labSource === "mythos_swarm" + non-null.
//   - The clear-assignee side effect is gated: sole mode triggers
//     it (lab owns the roster), enhancer mode does NOT (the user
//     must keep their assignee).
//
// 0.3.45 hoist / 0.5.4 inline panel / 0.5.4.x click-through:
//
//   - 0.3.45: the `agent_creation_studio` lab shipped as an action-type
//     lab with an `onAction` footer callback that routed the user to a
//     pre-workspace `/experimental/agent-creation-studio` creator page.
//   - 0.5.4 (creator page deleted): the studio is an issue-bound lab —
//     selecting it in the LabPicker main list writes
//     `issue.lab_source='agent_creation_studio'` and the leader
//     (`agent_creation_expert`, provisioned by
//     `install_agent_creation_studio.go`) is auto-assigned via the
//     0.3.46 P0#4 leader-rewrite contract. The dedicated creator page
//     was removed; the studio now authors resources by dispatching a
//     task to the leader.
//   - 0.5.4.x (this revision): when the flag IS enabled, tapping
//     `智能体创建` binds the lab directly — same as any other
//     issue-bound lab. The user gets the "click once, start now"
//     experience: bind → server PATCH → 0.3.46 P0#4 rewrites the
//     assignee to `agent_creation_expert` → task is queued.
//
//     The inline `RecentLabsPanel` is now only shown when the flag is
//     OFF. The panel explains what the lab is (because the leader is
//     not yet installed and direct dispatch would fail on the server)
//     and gives the user a single-click escape hatch back to the main
//     list. Once the user enables the flag in Labs settings, every
//     subsequent picker tap binds the lab directly.
//
//     The panel is a read-only history view (`RecentLabsPanel.tsx`),
//     not a creation surface — creating new agents/skills/squads is
//     always done by dispatching a task to the leader, which calls
//     `multica-creating-agents` / `multica-lab-builder` skills.
//
// This picker writes both `issue.lab_source` and `issue.lab_mode`
// through the same `onUpdate` callback. The caller is responsible
// for routing both fields into the underlying mutation.

import { useEffect, useMemo, useState } from "react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { useT } from "../../../i18n";
import { PropertyPicker, PickerItem } from "./property-picker";
import { RecentLabsPanel } from "./recent-labs-panel";

export type LabMode = "sole" | "enhancer";

interface LabPickerProps {
  /** Current workspace id. Required for the inline info panel
   *  (`RecentLabsPanel` reads agents/skills/squads lists scoped to this
   *  workspace). Callers should pass `useWorkspaceId()`. */
  wsId: string;
  /** Current lab_source value on the issue. null/undefined = no lab. */
  labSource: string | null | undefined;
  /** Current lab_mode value on the issue. null/undefined = no mode.
   *  Only meaningful when labSource === "mythos_swarm"; the picker
   *  ignores it for any other lab (and for "no lab"). */
  labMode?: LabMode | null;
  /** Called when the user picks a lab (or clears it), or changes
   *  the mythos mode. The next `lab_source` and (if changed)
   *  `lab_mode` are populated; the picker is responsible for
   *  deciding what to do with the existing assignee (typically
   *  the caller wants `assignee_type` and `assignee_id` cleared
   *  whenever the new value is non-null AND mode !== "enhancer"). */
  onUpdate: (next: {
    lab_source: string | null;
    lab_mode?: LabMode | null;
  }) => void;
  /**
   * Called by the picker whenever the user selects a NON-EMPTY lab
   * in sole mode (or sets the first one). The caller is expected
   * to clear `assignee_type` / `assignee_id` in response. The
   * picker never mutates the issue directly — it forwards the
   * intent via this callback so the parent can keep its optimistic
   * update layer in sync.
   *
   * Enhancer mode does NOT call this callback: the user keeps
   * their assignee.
   */
  onClearAssignee?: () => void;
  /** Popover alignment — same contract as the other pickers. */
  align?: "start" | "center" | "end";
  /** Optional controlled open state (for tests / cmd+k integration). */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** Custom trigger element. When provided, the default chrome is
   *  skipped and this element drives the popover anchor (the
   *  issue-detail PropRow uses this when the picker sits inline in
   *  the property list). */
  triggerRender?: React.ReactElement;
}

/**
 * Picker surface — visually matches status / priority / label picker.
 * Renders the picker trigger as a small chip showing the localized
 * lab title when a lab is set, and a "None" hint when it is not.
 *
 * When the selected lab is mythos_swarm, the popover body appends
 * a second-level TabsList for sole / enhancer. The current mode
 * defaults to 'sole' if the caller never set one explicitly.
 */
export function LabPicker({
  wsId,
  labSource,
  labMode,
  onUpdate,
  onClearAssignee,
  align = "start",
  open: controlledOpen,
  onOpenChange,
  triggerRender,
}: LabPickerProps) {
  const { t } = useT("issues");
  const { data: flags } = useExperimentalFlags();

  const [internalOpen, setInternalOpen] = useState(false);
  const open = controlledOpen ?? internalOpen;
  const setOpen = onOpenChange ?? setInternalOpen;

  // 0.5.4 inline-panel state machine.
  //
  //   "main"   — the default lab-list view (entries + mythos mode tabs).
  //   "recent" — the read-only info panel for `agent_creation_studio`,
  //              showing recent agents / skills / squads + a one-line
  //              hint. Picking this entry never binds `lab_source`;
  //              it only flips the local view so the popover swaps
  //              content while staying open.
  //
  // The view resets to "main" whenever the popover closes — the
  // `useEffect` below handles that. The "back to list" button the
  // panel renders on its own footer calls the same setter directly.
  const [view, setView] = useState<"main" | "recent">("main");
  useEffect(() => {
    if (!open) setView("main");
  }, [open]);

  // Build the picker entries once per flag list change. We surface every
  // ENABLED flag the catalog exposes so users can flip back and forth
  // freely; a separate "no lab" entry is always first (id="") so
  // clearing is a single click.
  //
  // 0.3.33 hardening: we used to list every flag from the catalog,
  // including disabled ones. That was misleading — clicking a
  // disabled flag would silently 400 on the server ("lab_source
  // must match a known experimental flag key" / "this flag is not
  // enabled for this workspace"). Filtering by `.enabled` here
  // guarantees the menu only offers what the server would accept.
  //
  // 0.3.45.8: also drop flags whose catalog.HideFromIssueLabPicker is
  // true. Those are infrastructure / self-driven labs (llm_wiki_bridge,
  // agent_self_optimization) that take effect globally once enabled;
  // picking them per-issue is a UX trap because the issue-level
  // lab_source value would never be consulted by the runtime. The
  // user still flips these flags on in the Labs settings tab — only
  // the per-issue picker omits them.
  //
  // 0.5.4.x: `agent_creation_studio` is still listed when `enabled=false`
  // thanks to `always_show_in_lab_picker` — flag-off users must be able
  // to discover the lab from the picker (the inline `RecentLabsPanel`
  // shown in that case explains what the lab is and what enabling the
  // flag would unlock). When the flag IS enabled, the same entry behaves
  // like any other issue-bound lab: selecting it writes
  // `lab_source='agent_creation_studio'` and the server auto-rewrites
  // the assignee to `agent_creation_expert` via the 0.3.46 P0#4
  // contract. See the `onClick` branch below for the split.
  const entries = useMemo(() => {
    const out: { id: string; title: string; enabled: boolean }[] = [
      { id: "", title: t(($) => $.pickers.lab.picker_none) ?? "None", enabled: true },
    ];
    for (const flag of flags ?? []) {
      if (!flag.enabled && !flag.always_show_in_lab_picker) continue;
      if (flag.hide_from_issue_lab_picker) continue;
      out.push({
        id: flag.key,
        title: flag.title.zh || flag.title.en || flag.key,
        enabled: flag.enabled,
      });
    }
    return out;
  }, [flags, t]);

  // The picked lab + mode determine what the popover body shows.
  // For non-mythos labs, we hide the mode tabs entirely (the
  // renderer falls back to a single tab, "sole", which the backend
  // treats as the implicit default).
  const showModeTabs = labSource === "mythos_swarm";
  // Stable default for the mode tabs when the caller never set one.
  const effectiveMode: LabMode = labMode ?? "sole";

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      align={align}
      triggerRender={triggerRender}
      trigger={<span aria-hidden />}
    >
      <div className="space-y-1.5 p-1.5">
        {view === "recent" ? (
          // Inline info panel for `agent_creation_studio`. Renders
          // recent agents/skills/squads + a hint line. Picking the
          // entry never bound `lab_source` — the issue stays in
          // whatever state it was in before the popover opened. The
          // panel renders its own back-to-list footer; popover close
          // also resets the view via the `useEffect` above.
          <RecentLabsPanel wsId={wsId} onClose={() => setView("main")} />
        ) : (
          <>
            {entries.map((entry) => (
              <PickerItem
                key={entry.id}
                selected={entry.id === (labSource ?? "")}
                onClick={() => {
                  // 0.5.4.x split for `agent_creation_studio`:
                  //
                  //   - flag ON  → bind `lab_source` directly (same
                  //     path as every other issue-bound lab). The
                  //     server's 0.3.46 P0#4 contract rewrites the
                  //     assignee to `agent_creation_expert` so the
                  //     user gets a single click that "just starts".
                  //     The popover closes; the user sees the lab
                  //     chip + a queued task.
                  //   - flag OFF → the leader isn't installed and
                  //     direct dispatch would 400 on the server, so
                  //     we open the read-only `RecentLabsPanel` (a
                  //     "here's what the lab is + a single escape
                  //     back" info surface) instead of binding.
                  //     The user can still close the popover, go to
                  //     the Labs settings tab, enable the flag, and
                  //     come back to bind the lab.
                  if (entry.id === "agent_creation_studio" && !entry.enabled) {
                    setView("recent");
                    setOpen(true);
                    return;
                  }
                  const nextLab = entry.id === "" ? null : entry.id;
                  // Clear-or-set: if the user picked the currently selected
                  // lab (no-op on the source side), do nothing. Otherwise
                  // dispatch the update with a sensible default mode.
                  if (nextLab === labSource) {
                    setOpen(false);
                    return;
                  }
                  if (nextLab === null) {
                    // Clearing the lab also clears the mode so the next
                    // render doesn't carry a stale 'sole'/'enhancer'.
                    onUpdate({ lab_source: null, lab_mode: null });
                    setOpen(false);
                    return;
                  }
                  // Switching from any lab → mythos_swarm, or between
                  // mythos_swarm and another lab: reset the mode to the
                  // stored value (or 'sole' default) so the new context
                  // is internally consistent.
                  if (nextLab === "mythos_swarm") {
                    // Don't auto-clear the assignee on enhancer; default
                    // is sole which DOES auto-clear.
                    if (effectiveMode === "sole" && onClearAssignee) {
                      onClearAssignee();
                    }
                    onUpdate({ lab_source: "mythos_swarm", lab_mode: effectiveMode });
                  } else {
                    // Any non-mythos lab (including `agent_creation_studio`
                    // when the flag is enabled) clears the assignee — the
                    // lab owns the roster, and the 0.3.46 P0#4 contract
                    // will rewrite it to the lab's leader if no assignee
                    // was carried. Mode is conceptually irrelevant for
                    // non-mythos labs; the backend accepts "sole" by
                    // default and the renderer doesn't show it.
                    if (onClearAssignee) {
                      onClearAssignee();
                    }
                    onUpdate({ lab_source: nextLab, lab_mode: "sole" });
                  }
                  setOpen(false);
                }}
              >
                {entry.title}
              </PickerItem>
            ))}

            {/* 0.3.31: mode tabs for mythos_swarm. Renders only when the
                current source is mythos_swarm so the other 7 labs keep
                the old single-list UX. The TabsList is a simple div +
                button pair to avoid pulling in @radix-ui/react-tabs for
                one tiny picker. */}
            {showModeTabs && (
              <div
                role="tablist"
                aria-label={t(($) => $.pickers.lab.mode_group_label) ?? "Mythos mode"}
                className="mt-1.5 border-t border-border/60 pt-1.5"
              >
                <div className="px-1.5 pb-1 text-[10px] uppercase tracking-wide text-muted-foreground">
                  {t(($) => $.pickers.lab.mode_group_label) ?? "Mode"}
                </div>
                <div className="flex gap-1">
                  {(["sole", "enhancer"] as const).map((m) => {
                    const selected = effectiveMode === m;
                    return (
                      <button
                        key={m}
                        type="button"
                        role="tab"
                        aria-selected={selected}
                        data-mode={m}
                        onClick={() => {
                          if (m === effectiveMode) {
                            setOpen(false);
                            return;
                          }
                          // Switching sole ↔ enhancer: enhancer mode
                          // must NOT clear the assignee (user keeps it).
                          // sole mode still clears.
                          if (m === "sole" && onClearAssignee) {
                            onClearAssignee();
                          }
                          onUpdate({ lab_source: "mythos_swarm", lab_mode: m });
                          setOpen(false);
                        }}
                        className={`flex-1 rounded-md px-2 py-1 text-xs transition-colors ${
                          selected
                            ? "bg-purple-500/15 text-purple-700 dark:text-purple-300"
                            : "text-muted-foreground hover:bg-accent/60 hover:text-foreground"
                        }`}
                      >
                        <div className="font-medium">
                          {m === "sole"
                            ? (t(($) => $.pickers.lab.mode_sole) ?? "蜂群独立")
                            : (t(($) => $.pickers.lab.mode_enhancer) ?? "蜂群增强")}
                        </div>
                        <div className="text-[10px] leading-tight text-muted-foreground">
                          {m === "sole"
                            ? (t(($) => $.pickers.lab.mode_sole_hint) ?? "Mythos 自己完成")
                            : (t(($) => $.pickers.lab.mode_enhancer_hint) ?? "先开会,再交给你的 agent/squad")}
                        </div>
                      </button>
                    );
                  })}
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </PropertyPicker>
  );
}