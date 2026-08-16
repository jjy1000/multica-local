package handler

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// canAccessPrivateAgent gates the four protected surfaces for private
// agents: chat / @-mention dispatch, viewing the agent's history, editing
// configuration, and deletion.
//
// 0.5.22 MUL-3963 port: the gate now keys off agent.permission_mode
// (the new MUL-3963 column) instead of the legacy visibility field.
// permission_mode defaults to 'private' for pre-existing rows that the
// backfill in migration 245 did not retag (the backfill only maps
// visibility='workspace' -> 'public_to'; visibility='private' is
// already the default).
//
// Public agents (permission_mode='public_to') are still unrestricted
// at the surface layer; the trigger gate canInvokeAgent (in
// agent_permission.go) is what actually evaluates the per-agent allow-list.
// The split mirrors the upstream MUL-3963 design where canAccessPrivateAgent
// governs view/edit surfaces and canInvokeAgent governs trigger surfaces.
//
// Agent-to-agent traffic is always allowed (actorType == "agent"); this is
// what preserves A2A collaboration even with private agents. The trust
// boundary is at member↔agent, not agent↔agent.
//
// For members, the implicit allowed_principals set is computed inline as:
// {agent.owner_id} ∪ workspace owner/admin members. Manual configuration of
// allowed_principals is not exposed in v1; future work can extend this set
// without changing call sites.
func (h *Handler) canAccessPrivateAgent(ctx context.Context, agent db.Agent, actorType, actorID, workspaceID string) bool {
	// 0.5.22 MUL-3963: read permission_mode first. The fork's pre-port
	// code checked visibility, which is now a DERIVED field maintained
	// by applyPermissionToResponse / deriveLegacyVisibility. The agent
	// row's permission_mode is authoritative for invocation + view gates.
	if agent.PermissionMode != "private" {
		return true
	}
	if actorType == "agent" {
		return true
	}
	if uuidToString(agent.OwnerID) == actorID {
		return true
	}
	member, err := h.getWorkspaceMember(ctx, actorID, workspaceID)
	if err != nil {
		return false
	}
	return roleAllowed(member.Role, "owner", "admin")
}

// memberAllowedForPrivateAgent is the pure predicate used by both
// canAccessPrivateAgent and the ListAgents filter loop. Caller must have
// already confirmed agent.PermissionMode == "private" (the MUL-3963
// equivalent of the pre-port visibility=='private' check).
func memberAllowedForPrivateAgent(agent db.Agent, userID, role string) bool {
	if roleAllowed(role, "owner", "admin") {
		return true
	}
	return uuidToString(agent.OwnerID) == userID
}

// accessibleAgentIDs returns the set of agent IDs in the workspace the actor
// is allowed to see, for use by workspace-wide aggregation endpoints
// (run counts, activity histograms, task snapshots) that need to filter out
// private agents the member can't access. Returns nil and false on error.
func (h *Handler) accessibleAgentIDs(ctx context.Context, workspaceID, actorType, actorID, role string) (map[string]struct{}, bool) {
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return nil, false
	}
	agents, err := h.Queries.ListAllAgents(ctx, wsUUID)
	if err != nil {
		return nil, false
	}
	allowed := make(map[string]struct{}, len(agents))
	for _, a := range agents {
		// 0.5.22 MUL-3963: permission_mode replaces visibility as the
		// authoritative gate key (visibility is now derived).
		if a.PermissionMode == "private" && actorType == "member" {
			if !memberAllowedForPrivateAgent(a, actorID, role) {
				continue
			}
		}
		allowed[uuidToString(a.ID)] = struct{}{}
	}
	return allowed, true
}

// canEnqueueSquadLeader returns true when the given actor is allowed to
// trigger the squad's private leader. It loads the leader agent and delegates
// to canAccessPrivateAgent. Non-private leaders always pass. System-initiated
// triggers (e.g. github webhooks) pass by treating "system" like "agent".
func (h *Handler) canEnqueueSquadLeader(ctx context.Context, leaderID pgtype.UUID, actorType, actorID, workspaceID string) bool {
	agent, err := h.Queries.GetAgent(ctx, leaderID)
	if err != nil {
		return false
	}
	if actorType == "system" {
		actorType = "agent"
	}
	return h.canAccessPrivateAgent(ctx, agent, actorType, actorID, workspaceID)
}

// canTriggerAgent is the MUL-3963 trigger gate (fork port of upstream
// canInvokeAgent with fork's existing 4-arg signature preserved for
// compatibility with the comment/autopilot call sites that pass
// (ctx, agent, actorType, actorID, workspaceID) only). It evaluates the
// permission_mode + per-agent allow-list; a workspace target also
// admits agent/system actors even without a resolved human originator
// (deliberate MUL-3963 exception for webhook / system automation).
//
// For call sites that have a resolved human originator (comment mention,
// chat send, manual rerun, autopilot run-now), pass it as the
// triggerOriginatorID arg. Pass "" if no human could be attributed —
// member/team targets then fail closed (see agent_permission.go for the
// full rule set).
//
// Fork port note: this is the unified trigger gate that replaces the
// fork's previous canAccessPrivateAgent + canEnqueueSquadLeader pattern
// for trigger surfaces. View / edit / delete surfaces still go through
// canAccessPrivateAgent (different concern: visibility, not allow-list).
func (h *Handler) canTriggerAgent(ctx context.Context, agent db.Agent, actorType, actorID, triggerOriginatorID, workspaceID string) bool {
	// 0.5.22 MUL-3963: route to the full invocation gate (with
	// allow-list evaluation) when the agent is public_to; for private
	// agents canInvokeAgent returns false (deny-by-default) which
	// matches the fork's previous canAccessPrivateAgent behavior for
	// private agents when the actor is not the owner / admin.
	//
	// For private agents, canInvokeAgent also short-circuits to true
	// when effectiveUser == OwnerID, so the fork's owner-bypass is
	// preserved (verified: canInvokeAgent has the same owner branch
	// at line 56 of the upstream port).
	return h.canInvokeAgent(ctx, agent, actorType, actorID, triggerOriginatorID, workspaceID)
}