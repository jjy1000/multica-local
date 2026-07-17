// Experimental flag (Labs) TanStack Query hooks.
//
// Per-user opt-in flags. The server returns the catalog merged with the
// caller's stored preference, so the UI does not need to manage two
// parallel data sources — `enabled` is the authoritative state and
// `default_enabled` is a hint shown next to the toggle.
//
// Cache strategy: staleTime 60s keeps the catalog responsive without
// re-hitting the server on every navigation. The mutation invalidates on
// settle so the next read pulls the server-side truth and catches any
// edge case where the optimistic patch did not match.

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import { createLogger } from "../logger";
import type { ExperimentalFlag } from "../types";

const logger = createLogger("experimental.queries");

export const experimentalFlagKeys = {
  all: ["experimental-flags"] as const,
};

/**
 * Fetch every catalog flag with the caller's current preference applied.
 * 60s staleTime matches the GitHub flags pattern — users flip a toggle
 * at most every few minutes, so a stale-while-revalidate window keeps
 * the Labs tab responsive without pinging the server on every render.
 */
export function useExperimentalFlags() {
  return useQuery({
    queryKey: experimentalFlagKeys.all,
    queryFn: () => {
      logger.info("listExperimentalFlags.start");
      return api.listExperimentalFlags();
    },
    staleTime: 60_000,
  });
}

/**
 * Returns the effective state of a single flag, or `defaultEnabled`
 * when the catalog hasn't been fetched yet. Callers should treat the
 * false default as "off until proven on" — never as a writable value.
 */
export function useExperimentalFlag(key: string, defaultEnabled = false): boolean {
  const { data } = useExperimentalFlags();
  if (!data) return defaultEnabled;
  const found = data.find((f) => f.key === key);
  return found ? found.enabled : defaultEnabled;
}

interface UpdateInput {
  key: string;
  enabled: boolean;
}

/**
 * Optimistic toggle. Patches the cached list immediately, rolls back on
 * server error, and invalidates on settle so the response from the
 * server replaces the optimistic value (handles the edge case where the
 * server adjusted the value, e.g. when a flag becomes read-only).
 */
export function useUpdateExperimentalFlag() {
  const qc = useQueryClient();

  return useMutation({
    mutationFn: (input: UpdateInput) => {
      logger.info("updateExperimentalFlag.start", { key: input.key, enabled: input.enabled });
      return api.updateExperimentalFlag(input.key, input.enabled);
    },
    onMutate: async (input) => {
      await qc.cancelQueries({ queryKey: experimentalFlagKeys.all });
      const previous = qc.getQueryData<ExperimentalFlag[]>(experimentalFlagKeys.all);
      qc.setQueryData<ExperimentalFlag[]>(experimentalFlagKeys.all, (old) =>
        old?.map((f) => (f.key === input.key ? { ...f, enabled: input.enabled } : f)),
      );
      return { previous };
    },
    onError: (err, _input, context) => {
      logger.error("updateExperimentalFlag.error", err);
      if (context?.previous) {
        qc.setQueryData(experimentalFlagKeys.all, context.previous);
      }
    },
    onSettled: () => {
      qc.invalidateQueries({ queryKey: experimentalFlagKeys.all });
    },
  });
}