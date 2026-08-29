"use client";

import { useCallback, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import type { Agent, MemberWithUser, Squad, Workspace } from "../types";
import { useWorkspaceId } from "../hooks";
import { api } from "../api";
import {
  memberListOptions,
  agentListOptions,
  squadListOptions,
  workspaceListOptions,
} from "./queries";
import { resolvePublicFileUrl } from "./avatar-url";

// Stable empty array for the still-loading workspace list query. A fresh
// `= []` default allocates a new array on every render while `data` is
// undefined, which causes downstream consumers to churn the React Query
// cache key. Sharing one reference keeps the loading snapshot referentially
// stable so memo deps like `workspaces.length` don't fire on every render.
const EMPTY_WORKSPACES: Workspace[] = [];
// MUL-4985: the member/agent/squad directory queries are `undefined` while
// loading. A fresh `= []` default allocates a new array on every render, so
// `getActorName` (memoized on those arrays) changes identity every render and
// consumers that list it in their own memo deps churn a new value each render.
// Sharing one reference keeps the loading snapshot referentially stable.
const EMPTY_MEMBERS: MemberWithUser[] = [];
const EMPTY_AGENTS: Agent[] = [];
const EMPTY_SQUADS: Squad[] = [];

/**
 * Shared authoritative-state contract for the workspace list.
 *
 * TanStack Query's `isFetched` also becomes true after an initial failure, so
 * it cannot distinguish "the account has no workspaces" from "the first
 * request failed before any list arrived". Data presence can: a successful
 * empty response is `[]`, while an initial failure remains `undefined`.
 * Background failures retain cached data and therefore remain ready.
 */
export function useWorkspaceList({ enabled = true }: { enabled?: boolean } = {}) {
  const query = useQuery({
    ...workspaceListOptions(),
    enabled,
  });
  const ready = query.data !== undefined;

  return {
    workspaces: query.data ?? EMPTY_WORKSPACES,
    ready,
    unavailable: enabled && !ready && query.isLoadingError,
    isFetching: query.isFetching,
    refetch: query.refetch,
  };
}

export function useActorName() {
  const wsId = useWorkspaceId();
  const { data: members = EMPTY_MEMBERS } = useQuery(memberListOptions(wsId));
  const { data: agents = EMPTY_AGENTS } = useQuery(agentListOptions(wsId));
  const { data: squads = EMPTY_SQUADS } = useQuery(squadListOptions(wsId));

  const getMemberName = useCallback((userId: string) => {
    const m = members.find((m) => m.user_id === userId);
    return m?.name ?? "Unknown";
  }, [members]);

  // 0.5.89: an agent id missing from the workspace list — the cold-load
  // window (data still EMPTY_AGENTS) or a hidden lab leader row the
  // cached list predates — used to fall straight to "Unknown Agent" and
  // stay there after a lab leader-rewrite. Each miss now resolves ONCE
  // via GET /api/agents/:id (the by-id route serves lab-managed leaders;
  // only selection surfaces filter them) and caches the name — including
  // the failure, so an unresolvable id costs a single request.
  const [extraAgentNames, setExtraAgentNames] = useState<Record<string, string>>({});
  const inflightAgentNames = useRef<Set<string>>(new Set());

  const getAgentName = useCallback(
    (agentId: string) => {
      const a = agents.find((a) => a.id === agentId);
      if (a) return a.name;
      const fetched = extraAgentNames[agentId];
      if (fetched !== undefined) return fetched;
      if (!inflightAgentNames.current.has(agentId)) {
        inflightAgentNames.current.add(agentId);
        void Promise.resolve()
          .then(() => api.getAgent(agentId))
          .then((agent) => {
            setExtraAgentNames((prev) => ({ ...prev, [agentId]: agent.name }));
          })
          .catch(() => {
            setExtraAgentNames((prev) => ({ ...prev, [agentId]: "Unknown Agent" }));
          })
          .finally(() => {
            inflightAgentNames.current.delete(agentId);
          });
      }
      return "Unknown Agent";
    },
    [agents, extraAgentNames],
  );

  const getSquadName = useCallback((squadId: string) => {
    const s = squads.find((s) => s.id === squadId);
    return s?.name ?? "Unknown Squad";
  }, [squads]);

  const getActorName = useCallback((type: string, id: string) => {
    if (type === "member") return getMemberName(id);
    if (type === "agent") return getAgentName(id);
    if (type === "squad") return getSquadName(id);
    if (type === "system") return "Multica";
    return "System";
  }, [getAgentName, getMemberName, getSquadName]);

  const getActorInitials = useCallback((type: string, id: string) => {
    const name = getActorName(type, id);
    return name
      .split(" ")
      .map((w) => w[0])
      .join("")
      .toUpperCase()
      .slice(0, 2);
  }, [getActorName]);

  const getActorAvatarUrl = useCallback((type: string, id: string): string | null => {
    if (type === "member") return resolvePublicFileUrl(members.find((m) => m.user_id === id)?.avatar_url);
    if (type === "agent") return resolvePublicFileUrl(agents.find((a) => a.id === id)?.avatar_url);
    if (type === "squad") return resolvePublicFileUrl(squads.find((s) => s.id === id)?.avatar_url);
    return null;
  }, [agents, members, squads]);

  return useMemo(
    () => ({
      getMemberName,
      getAgentName,
      getSquadName,
      getActorName,
      getActorInitials,
      getActorAvatarUrl,
    }),
    [
      getActorAvatarUrl,
      getActorInitials,
      getActorName,
      getAgentName,
      getMemberName,
      getSquadName,
    ],
  );
}
