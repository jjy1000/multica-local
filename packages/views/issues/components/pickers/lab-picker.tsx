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
// 0.3.45 action sub-menu (agent_creation_studio):
//   - Labs Platform action-type entry point. Distinct from the
//     issue-bound lab_source flow above: the action callback
//     navigates AWAY from the issue picker, INTO a pre-workspace
//     /experimental/<route> route, and does NOT mutate
//     issue.lab_source. The lab/assignee mutex + IssueLabsSection
//     are intentionally oblivious — the studio is orthogonal.
//
// This picker writes both `issue.lab_source` and `issue.lab_mode`
// through the same `onUpdate` callback. The caller is responsible
// for routing both fields into the underlying mutation.

import { useMemo, useState } from "react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { useT } from "../../../i18n";
import { PropertyPicker, PickerItem } from "./property-picker";

export type LabMode = "sole" | "enhancer";

interface LabPickerProps {
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
  /**
   * 0.3.45: action-type lab entry. Renders a second-tier sub-menu
   * at the bottom of the popover when supplied. Triggering an
   * action invokes `onAction(actionKey)` — the caller is expected
   * to navigate to the corresponding pre-workspace route. Actions
   * do NOT mutate `issue.lab_source` and do NOT touch the
   * lab/assignee mutex.
   *
   * Currently the only registered action key is
   * `"agent_creation_studio"` (defined by the
   * `agent_creation_studio` manifest's `entry_points.issue_panel_action`).
   * The string union is kept open so future action-type labs can
   * register without changing this picker's signature.
   */
  onAction?: (actionKey: string) => void;
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
  labSource,
  labMode,
  onUpdate,
  onClearAssignee,
  onAction,
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
  const entries = useMemo(() => {
    const out: { id: string; title: string }[] = [
      { id: "", title: t(($) => $.pickers.lab.picker_none) ?? "None" },
    ];
    for (const flag of flags ?? []) {
      if (!flag.enabled) continue;
      out.push({
        id: flag.key,
        title: flag.title.zh || flag.title.en || flag.key,
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
      footer={
        // 0.3.45 action sub-menu. Rendered in the built-in footer
        // slot (NOT as a child PickerItem) so arrow-key navigation
        // skips it — the action isn't a list option, it's an
        // outbound link. onAction is optional; omitting it
        // suppresses this footer entirely.
        onAction ? (
          <div className="space-y-1" data-lab-picker-action-group>
            <div className="px-1.5 pb-0.5 text-[10px] uppercase tracking-wide text-muted-foreground">
              {t(($) => $.pickers.lab.action_group_label) ?? "智能体创建"}
            </div>
            <PickerItem
              selected={false}
              onClick={() => {
                onAction("agent_creation_studio");
                setOpen(false);
              }}
            >
              <span className="flex items-center gap-1.5">
                <span aria-hidden>+</span>
                <span>
                  {t(($) => $.pickers.lab.action_create) ?? "创建智能体 / 技能 / 团队"}
                </span>
              </span>
            </PickerItem>
          </div>
        ) : null
      }
    >
      <div className="space-y-1.5 p-1.5">
        {entries.map((entry) => (
          <PickerItem
            key={entry.id}
            selected={entry.id === (labSource ?? "")}
            onClick={() => {
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
                // Any non-mythos lab clears the assignee (lab owns
                // the roster). Mode is conceptually irrelevant for
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
      </div>
    </PropertyPicker>
  );
}