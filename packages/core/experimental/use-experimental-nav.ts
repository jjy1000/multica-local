// useExperimentalNav (0.3.19 P3 → 0.3.20 manifest-driven): render the
// sidebar's "Experimental" group from the catalog payload itself.
//
// 0.3.19 hard-coded STATIC_NAV (4 rows) — every new flag needed a
// code edit here. 0.3.20 reads sidebar_entries from the
// GET /api/experimental-flags response, so a catalog-only edit
// lights up a new sidebar row without touching this file.
//
// The hook is a pure projection: TanStack Query owns the cache, the
// projection maps the wire shape into the renderer's nav row shape.
// Tests cover the projection (empty → [], all-off → [], mixed → filter)
// without touching the network.

import { useMemo } from "react";
import type { ExperimentalFlag } from "../types";
import { useExperimentalFlags } from "./queries";

export interface ExperimentalNavItem {
  key: string;
  flagKey: string;
  /** i18n key the renderer resolves with its locale table. */
  labelKey: string;
  route: string;
}

export function useExperimentalNav(): ExperimentalNavItem[] {
  const { data } = useExperimentalFlags();
  return useMemo(() => {
    if (!data) return [];
    const out: ExperimentalNavItem[] = [];
    for (const f of data as ExperimentalFlag[]) {
      if (!f.enabled) continue;
      const entries = f.sidebar_entries ?? [];
      for (const e of entries) {
        out.push({
          key: e.key,
          flagKey: e.flag_key ?? f.key,
          labelKey: e.label_key,
          route: e.route,
        });
      }
    }
    return out;
  }, [data]);
}