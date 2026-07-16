"use client";

// LabPicker — 0.3.29 Lab flag picker for the issue detail Property row.
//
// Mirrors the shape of the other pickers in this folder (status /
// priority / assignee / label) so the Lab PropRow in issue-detail.tsx
// can drop in `<LabPicker ... />` without bespoke wiring.
//
// Lab selection is intentionally a flag-only field (no second-level
// "what lab agent runs this issue" — the lab itself owns that
// assignment). Each lab maps to a single experimental flag key in
// the catalog (server/internal/experimental/catalog.go). The picker
// is read-only on the right-hand-side label (the chrome is fixed to
// "Lab") and shows the active flag's localized title.
//
// The hard constraint from the original 0.3.29 spec is:
//
//   - Once a lab is selected, the user CANNOT pick a different agent
//     for that issue (the lab owns the agent roster).
//   - mythos_swarm is the unique exception: it lets the user add extra
//     agents beyond the canonical 5-agent roster, on top of the lab's
//     locked baseline roster.
//
// This picker only writes `issue.lab_source` — the agent lock is
// enforced elsewhere (the create-issue modal disables the assignee
// picker when a lab_source is set, and the issue detail enforces
// it on update).

import { useMemo, useState } from "react";
import { useExperimentalFlags } from "@multica/core/experimental";
import { useT } from "../../../i18n";
import { PropertyPicker, PickerItem } from "./property-picker";

interface LabPickerProps {
  /** Current lab_source value on the issue. null/undefined = no lab. */
  labSource: string | null | undefined;
  /** Called when the user picks a lab (or clears it). */
  onUpdate: (next: { lab_source: string | null }) => void;
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
 */
export function LabPicker({
  labSource,
  onUpdate,
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
  // flag the catalog exposes so users can flip back and forth freely;
  // a separate "no lab" entry is always first (id="") so clearing is
  // a single click.
  const entries = useMemo(() => {
    const out: { id: string; title: string }[] = [
      { id: "", title: t(($) => $.lab.picker_none) ?? "None" },
    ];
    for (const flag of flags ?? []) {
      out.push({
        id: flag.key,
        title: flag.title.zh || flag.title.en || flag.key,
      });
    }
    return out;
  }, [flags, t]);

  const currentEntry = useMemo(() => {
    if (!labSource) return null;
    return entries.find((e) => e.id === labSource) ?? null;
  }, [labSource, entries]);
  // currentEntry is exposed for callers that want to render their own
  // trigger chrome; we don't use it inside the picker because the
  // triggerRender prop (when provided) replaces the default chrome.
  void currentEntry;

  return (
    <PropertyPicker
      open={open}
      onOpenChange={setOpen}
      align={align}
      triggerRender={triggerRender}
      trigger={<span aria-hidden />}
    >
      {entries.map((entry) => (
        <PickerItem
          key={entry.id}
          selected={entry.id === (labSource ?? "")}
          onClick={() => {
            onUpdate({ lab_source: entry.id === "" ? null : entry.id });
            setOpen(false);
          }}
        >
          {entry.title}
        </PickerItem>
      ))}
    </PropertyPicker>
  );
}