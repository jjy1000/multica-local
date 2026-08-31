import { queryOptions, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type { McpSyncSnapshot, McpSyncServer } from "../types";

/**
 * MCP 管理 (0.5.92) — the read-only mirror of the user's Claude Code MCP
 * servers. The server-side worker keeps the mirror fresh (60s tick + boot
 * sync); the UI can force a pass via the refresh mutation but has no edit
 * or delete path — removals only ever come from the source file itself.
 */

export const mcpSyncKeys = {
  all: (wsId: string) => ["mcp-sync", wsId] as const,
  snapshot: (wsId: string) => [...mcpSyncKeys.all(wsId), "snapshot"] as const,
};

export function mcpSyncSnapshotOptions(wsId: string) {
  return queryOptions({
    queryKey: mcpSyncKeys.snapshot(wsId),
    queryFn: () => api.getMcpSync(),
    enabled: !!wsId,
    // The server worker refreshes independently of this cache; a moderate
    // stale time keeps the settings tab from polling on every focus.
    staleTime: 30_000,
  });
}

export function useMcpSyncSnapshot(wsId: string) {
  return useQuery(mcpSyncSnapshotOptions(wsId));
}

export function useRefreshMcpSync(wsId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () => api.refreshMcpSync(),
    onSuccess: (snapshot: McpSyncSnapshot) => {
      // Seed the cache with the refresh response so the list doesn't flash
      // stale content between invalidation and refetch.
      queryClient.setQueryData(mcpSyncKeys.snapshot(wsId), snapshot);
      void queryClient.invalidateQueries({ queryKey: mcpSyncKeys.all(wsId) });
    },
  });
}

/** Live (merge-eligible) servers — what agents actually see at claim time. */
export function syncedServers(snapshot: McpSyncSnapshot | undefined): McpSyncServer[] {
  return (snapshot?.servers ?? []).filter((s) => s.status === "synced");
}
