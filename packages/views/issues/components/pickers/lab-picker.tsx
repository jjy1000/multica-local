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
//
// 0.5.6: the lab picker is now a thin wrapper over the remaining
// opt-in catalog flags. The two product-level flags
// (`agent_creation_studio`, `agent_self_optimization`) that 0.5.5
// lifted out of the Labs tier no longer appear here, the
// `RecentLabsPanel` and its read-only history view are deleted
// (the leader agents are reachable through the AssigneePicker
// instead), and the inline info panel state machine is removed
// (no second view to swap into).
//
// 0.5.5: HIDDEN_LAB_KEYS hard-coded the two product-level flags
// as a defense in depth. 0.5.6 removes the set: the catalog
// itself no longer returns those keys (catalog.go removed the Flag
// literals), so a client-side black-list is no longer needed.
//
// 0.5.4.x / 0.5.5.2 / 0.5.5.3: a series of UI tweaks (click-through
// dispatch, "open panel" removal, ProductLevelBanner) layered on
// top of the original 0.3.45 action-type lab. 0.5.6 subsumes all
// of them by removing the relevant flags from the picker entirely.
//
// This picker writes both `issue.lab_source` and `issue.lab_mode`
// through the same `onUpdate` callback. The caller is responsible
// for routing both fields into the underlying mutation.

import { useMemo, useState } from "react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { useT } from "../../../i18n";
import { PropertyPicker, PickerItem } from "./property-picker";

export type LabMode = "sole" | "enhancer";

// Labs that reserve the issue roster via the lab ↔ assignee mutex.
// Narrowed in 0.3.33 and realigned 2026-07-28: mythos_swarm sole-mode
// (enhancer reverses it) and swarm_topology are the only two members.
// Every other lab coexists with a manual assignee — the server only
// auto-rewrites the assignee to the lab leader when NO explicit
// assignee is carried (0.3.47), so wiping it here would discard a
// user choice the contract promises to keep.
const ASSIGNEE_MUTEX_LABS = new Set<string>(["mythos_swarm", "swarm_topology"]);

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
  //
  // 0.3.45.8: also drop flags whose catalog.HideFromIssueLabPicker is
  // true. Those are infrastructure / self-driven labs (llm_wiki_bridge,
  // code_canvas) that take effect globally once enabled; picking
  // them per-issue is a UX trap because the issue-level lab_source
  // value would never be consulted by the runtime. The user still
  // flips these flags on in the Labs settings tab — only the
  // per-issue picker omits them.
  //
  // 0.5.6: the catalog no longer returns the
  // `agent_creation_studio` / `agent_self_optimization` keys at
  // all, so the prior 0.5.5 / 0.5.5.2 / 0.5.5.3 client-side
  // `HIDDEN_LAB_KEYS` black-list is removed. Any future
  // product-level flag should be added to the catalog's
  // `HideFromIssueLabPicker` instead of duplicating the
  // black-list here.
  //
  // catalog.AlwaysShowInLabPicker DTO marker (sibling of the
  // `lab_managed` DTO marker — see CLAUDE.md Active Contract #4):
  // an `always_show=true` flag must remain reachable in the picker
  // even when the catalog says "hide from issue lab picker" or the
  // flag is currently disabled. Without this escape hatch the
  // catalog's HideFromIssueLabPicker cannot coexist with an opt-in
  // onboarding surface (e.g. a flag should be reachable but never
  // auto-enabled). The escape hatch is checked FIRST so a
  // hide_from_picker=true flag still surfaces when always_show is
  // set; an enabled=false flag still surfaces when always_show is
  // set (the Enabled field on the picker row is honest about state).
  const entries = useMemo(() => {
    const out: { id: string; title: string; enabled: boolean }[] = [
      { id: "", title: t(($) => $.pickers.lab.picker_none) ?? "None", enabled: true },
    ];
    for (const flag of flags ?? []) {
      // Hide-from-picker respects always_show escape hatch: an
      // always-shown flag must be reachable in the picker even if
      // catalog says hide-from-picker.
      if (flag.hide_from_issue_lab_picker && !flag.always_show_in_lab_picker) continue;
      // Enabled filter: must be enabled UNLESS always_show is true.
      if (!flag.enabled && !flag.always_show_in_lab_picker) continue;
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
                    // Only mutex labs (swarm_topology here — mythos_swarm
                    // is handled above with its sole/enhancer nuance)
                    // clear the assignee. Non-mutex labs keep the
                    // current assignee: lab + assignee coexist by
                    // contract, and the server's leader rewrite only
                    // fills an EMPTY assignee field.
                    if (ASSIGNEE_MUTEX_LABS.has(nextLab) && onClearAssignee) {
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