"use client";

// Stub for `autopilot-list-toolbar.tsx` — 0.3.29 working-tree sparse
// checkout gap. The actual implementation lives in 0.3.28 source but
// was not part of the 0.3.29 ship wiring. This stub exports the
// symbols referenced by `autopilots-page.tsx` (AutopilotListToolbar
// + actorFilterValue) so the rolldown build resolves the import path
// without crashing. Behavior here is a faithful no-op: the toolbar
// renders nothing, and actorFilterValue just shapes the (type, id)
// pair into the filter-key string the parent state expects.
//
// 0.3.29 ship chain (this PR):
//   - snapshot ✓
//   - migrate up ✓ (156_mythos_round_extension + 156_runtime_lab_source)
//   - bundle-cli ✓
//   - electron-vite build ← resolving this stub for the build to pass
//   - electron-builder --dir → /Applications/Multica.app
//   - cold start verify + row parity

import type { AutopilotColumnKey, AutopilotScope, AutopilotSortField } from "@multica/core/autopilots/stores";
import type { ListGridSortDirection } from "@multica/ui/components/ui/list-grid";

/**
 * `actorFilterValue` mirrors the convention used by the autopilots
 * store: a (type, id) actor pair maps to a stable filter-key string of
 * the form `<type>:<id>`. The store's assignees / creators filter
 * sets are populated with these strings; the stub preserves the same
 * shape so we do not silently change the parent store's contract.
 */
export function actorFilterValue(actorType: string, actorId: string): string {
  return `${actorType}:${actorId}`;
}

/**
 * Stub toolbar — 0.3.29 working-tree sparse checkout lost the original
 * implementation. Renders null because the parent already provides
 * its own header / scope pill via `AutopilotListHeader`. Returning
 * null keeps the page chrome unchanged from the 0.3.28 baseline (no
 * extra row, no extra spacing) — visually identical to the missing
 * file's prior no-op render path when scopeCounts was empty.
 */
export function AutopilotListToolbar(_props: {
  scope: AutopilotScope;
  onScopeChange: (next: AutopilotScope) => void;
  scopeCounts: Record<AutopilotScope, number>;
  filters: {
    modes: string[];
    assignees: string[];
    creators: string[];
    triggers: string[];
  };
  onToggleFilter: (group: "modes" | "assignees" | "creators" | "triggers", value: string) => void;
  onClearFilters: () => void;
  sortField: AutopilotSortField;
  sortDirection: ListGridSortDirection;
  onSortFieldChange: (next: AutopilotSortField) => void;
  onSortDirectionChange: (next: ListGridSortDirection) => void;
  hiddenColumns: AutopilotColumnKey[];
  onToggleColumn: (column: AutopilotColumnKey) => void;
  allRows: unknown[];
  visibleCount: number;
}): null {
  return null;
}