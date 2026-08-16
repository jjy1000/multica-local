// Package handler — agent_permission.go (0.5.22 MUL-3963 port).
//
// Agent invocation permission model (MUL-3963 PR #4844, upstream commit
// 9c876d7a0). Splits "who may TRIGGER/INVOKE an agent" out of the
// overloaded `visibility` field into an explicit model:
//
//   - agent.permission_mode TEXT NOT NULL DEFAULT 'private' CHECK
//     ('private', 'public_to')
//   - agent_invocation_target — the per-agent allow-list (workspace /
//     member / team; team is reserved/inert in V1)
//
// This file ports the upstream `agent_permission.go` + the core gate
// from upstream `agent_access.go` into the fork's handler package
// (fork has a documented decision to keep admission/permission types in
// handler/ — see admission.go::DispatchReasonCode doc-comment). The
// visibility column stays as a derived legacy field so old clients
// never see a permission widening.
//
// What's in this port:
//   - AgentInvocationTargetDTO wire shape
//   - permissionMode constants
//   - resolvePermission + parsePermissionInput (legacy/new normaliser)
//   - replaceInvocationTargets + enrichAgentResponseWithTargets +
//     enrichAgentResponseWithTargetsHTTP (handler methods)
//   - canInvokeAgent (the gate) + invokeOriginatorFromRequest (originator
//     resolver)
//
// What's NOT in this commit (next-session work):
//   - rewrite canAccessPrivateAgent call sites in autopilot.go, chat.go,
//     comment.go, squad.go, issue_child_done.go, issue_trigger.go,
//     runtime.go, runtime_profile.go, workspace_revoke.go to use
//     canInvokeAgent instead
//   - update ListAgents / GetAgent / CreateAgent / UpdateAgent to
//     populate PermissionMode + InvocationTargets and call
//     enrichAgentResponseWithTargets
//   - the 616-LOC agent_permission_test.go + agent_access_test.go +
//     autopilot_private_leader_test.go updates
//   - frontend access-picker.tsx (279 LOC) + permissions/rules.ts +
//     types/agent.ts + api/schemas.ts + 4 locales
//   - MUL-3963 follow-ons: MUL-4010/4015/4063/4305/4857/5548

package handler

import (
	"context"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentInvocationTargetDTO is the wire shape of one invocation allow-list
// entry (MUL-3963). target_id is null for team placeholders that a client
// omitted, but is always present for workspace (the workspace id) and
// member (the user id) rows persisted by the backend.
type AgentInvocationTargetDTO struct {
	TargetType string  `json:"target_type"`
	TargetID   *string `json:"target_id"`
}

const (
	permissionModePrivate  = "private"
	permissionModePublicTo = "public_to"

	invocationTargetWorkspace = "workspace"
	invocationTargetMember    = "member"
	invocationTargetTeam      = "team"
)

// deriveLegacyVisibility maps the permission model back onto the legacy
// two-value visibility field so old clients never observe a widening:
//   - public_to WITH a workspace target -> "workspace" (everyone can invoke)
//   - everything else (private, or public_to limited to member/team) -> "private"
func deriveLegacyVisibility(permissionMode string, targets []db.AgentInvocationTarget) string {
	if permissionMode == permissionModePublicTo {
		for _, t := range targets {
			if t.TargetType == invocationTargetWorkspace {
				return "workspace"
			}
		}
	}
	return "private"
}

// applyInvocationTargetsToResponse fills InvocationTargets and recomputes
// the derived legacy Visibility from the loaded targets, keeping both
// views of the permission consistent in a single place.
func applyInvocationTargetsToResponse(resp *AgentResponse, targets []db.AgentInvocationTarget) {
	dto := make([]AgentInvocationTargetDTO, 0, len(targets))
	for _, t := range targets {
		var idPtr *string
		if t.TargetID.Valid {
			s := uuidToString(t.TargetID)
			idPtr = &s
		}
		dto = append(dto, AgentInvocationTargetDTO{TargetType: t.TargetType, TargetID: idPtr})
	}
	resp.InvocationTargets = dto
	resp.Visibility = deriveLegacyVisibility(resp.PermissionMode, targets)
}

// resolvedPermission is the normalised outcome of parsing the permission
// fields (or legacy visibility) off a create/update request.
type resolvedPermission struct {
	mode    string
	targets []targetSpec
}

type targetSpec struct {
	targetType string
	targetID   pgtype.UUID // invalid for team placeholders
}

// legacyVisibility is what this permission maps to for the visibility
// column we keep in sync for backwards compatibility. public_to with a
// workspace target round-trips to "workspace"; everything else (private
// or member-only public_to) is "private". Mirrors the upstream
// resolvedPermission.legacyVisibility() (MUL-4010 #4897 keeps the
// visibility mirror column in sync with permission_mode).
func (p resolvedPermission) legacyVisibility() string {
	if p.mode == permissionModePublicTo {
		for _, t := range p.targets {
			if t.targetType == invocationTargetWorkspace {
				return "workspace"
			}
		}
	}
	return "private"
}

// parsePermissionInput normalises a permission_mode + invocation_targets
// pair, falling back to a legacy visibility value when the new fields are
// absent. See upstream agent_permission.go::parsePermissionInput for the
// exact rules (legacy visibility "workspace" -> public_to + workspace
// target; empty public_to is normalised to a single workspace target).
//
// The fork port drops the parsePermissionInput signature's separate
// hasPermissionMode / hasTargets booleans and the legacyVisibility pointer;
// callers pass nil values for absence, matching the API layer's
// normalisation (agent.go::agentCreateRequestBody / agentUpdateRequestBody
// wire shapes).
func parsePermissionInput(
	workspaceID pgtype.UUID,
	permissionMode *string,
	targets []AgentInvocationTargetDTO,
	legacyVisibility *string,
) (resolvedPermission, bool, error) {
	hasPermissionMode := permissionMode != nil
	hasTargets := len(targets) > 0

	if !hasPermissionMode && legacyVisibility == nil {
		return resolvedPermission{}, false, nil
	}

	// Legacy-only path: map visibility onto the new model.
	if !hasPermissionMode {
		switch *legacyVisibility {
		case "workspace":
			return resolvedPermission{
				mode:    permissionModePublicTo,
				targets: []targetSpec{{targetType: invocationTargetWorkspace, targetID: workspaceID}},
			}, true, nil
		case "private", "":
			return resolvedPermission{mode: permissionModePrivate}, true, nil
		default:
			return resolvedPermission{}, false, fmt.Errorf("visibility must be 'private' or 'workspace'")
		}
	}

	mode := permissionModePrivate
	if *permissionMode != "" {
		mode = *permissionMode
	}
	if mode != permissionModePrivate && mode != permissionModePublicTo {
		return resolvedPermission{}, false, fmt.Errorf("permission_mode must be 'private' or 'public_to'")
	}

	res := resolvedPermission{mode: mode}
	if mode == permissionModePrivate {
		return res, true, nil
	}

	// public_to: normalise the target list, de-duping and validating.
	if hasTargets {
		seen := map[string]struct{}{}
		for _, t := range targets {
			switch t.TargetType {
			case invocationTargetWorkspace:
				key := "workspace"
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				res.targets = append(res.targets, targetSpec{targetType: invocationTargetWorkspace, targetID: workspaceID})
			case invocationTargetMember:
				if t.TargetID == nil || *t.TargetID == "" {
					return resolvedPermission{}, false, fmt.Errorf("member invocation target requires target_id")
				}
				uid, err := parseUUIDLoose(*t.TargetID)
				if err != nil {
					return resolvedPermission{}, false, fmt.Errorf("member invocation target_id is not a valid uuid")
				}
				key := "member:" + *t.TargetID
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				res.targets = append(res.targets, targetSpec{targetType: invocationTargetMember, targetID: uid})
			case invocationTargetTeam:
				if t.TargetID == nil || *t.TargetID == "" {
					return resolvedPermission{}, false, fmt.Errorf("team invocation target requires target_id")
				}
				tid, err := parseUUIDLoose(*t.TargetID)
				if err != nil {
					return resolvedPermission{}, false, fmt.Errorf("team invocation target_id is not a valid uuid")
				}
				key := "team:" + *t.TargetID
				if _, dup := seen[key]; dup {
					continue
				}
				seen[key] = struct{}{}
				res.targets = append(res.targets, targetSpec{targetType: invocationTargetTeam, targetID: tid})
			default:
				return resolvedPermission{}, false, fmt.Errorf("invocation target_type must be 'workspace', 'member', or 'team'")
			}
		}
	}
	// An empty public_to is a phantom: "shared with nobody" that the
	// front-end would render as workspace-shared while the backend admits
	// no one. Per the MUL-3963 ruling, normalise to a single workspace
	// target — also makes `--permission-mode public_to` with no targets
	// mean "public to workspace".
	if len(res.targets) == 0 {
		res.targets = append(res.targets, targetSpec{targetType: invocationTargetWorkspace, targetID: workspaceID})
	}
	return res, true, nil
}

// replaceInvocationTargets rewrites an agent's invocation allow-list
// wholesale: clear then re-insert. Called inside create/update after the
// agent row exists.
func (h *Handler) replaceInvocationTargets(ctx context.Context, agentID pgtype.UUID, createdBy pgtype.UUID, targets []targetSpec) error {
	return replaceInvocationTargetsWithQueries(ctx, h.Queries, agentID, createdBy, targets)
}

// replaceInvocationTargetsWithQueries is the tx-friendly variant: callers
// that hold a `qtx := h.Queries.WithTx(tx)` can pass it here so the
// invocation target rows are written inside the same transaction as the
// agent row (the template create path in agent_template.go depends on
// this — a fresh agent row must not observe a state where the row
// exists but its targets are missing; MUL-4010 #4897 closed this gap).
func replaceInvocationTargetsWithQueries(ctx context.Context, q *db.Queries, agentID pgtype.UUID, createdBy pgtype.UUID, targets []targetSpec) error {
	if err := q.DeleteAgentInvocationTargets(ctx, agentID); err != nil {
		return err
	}
	for _, t := range targets {
		if err := q.CreateAgentInvocationTarget(ctx, db.CreateAgentInvocationTargetParams{
			AgentID:    agentID,
			TargetType: t.targetType,
			TargetID:   t.targetID,
			CreatedBy:  createdBy,
		}); err != nil {
			return err
		}
	}
	return nil
}

// enrichAgentResponseWithTargets loads an agent's invocation targets and
// applies them to the response (InvocationTargets + derived Visibility).
// Used by the single-agent detail / create / update responses.
func (h *Handler) enrichAgentResponseWithTargets(ctx context.Context, resp *AgentResponse, agentID pgtype.UUID) error {
	targets, err := h.Queries.ListAgentInvocationTargets(ctx, agentID)
	if err != nil {
		return err
	}
	applyInvocationTargetsToResponse(resp, targets)
	return nil
}

// enrichAgentResponseWithTargetsHTTP is the HTTP-boundary wrapper that
// writes a 500 and returns false on failure.
func (h *Handler) enrichAgentResponseWithTargetsHTTP(w http.ResponseWriter, r *http.Request, resp *AgentResponse, agentID pgtype.UUID) bool {
	if err := h.enrichAgentResponseWithTargets(r.Context(), resp, agentID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent invocation targets")
		return false
	}
	return true
}

// memberHitsInvocationTargets is the pure predicate deciding whether a
// regular member is on a public_to agent's allow-list, used by both the
// single-agent view gate and the ListAgents batch filter. A workspace
// target admits any member; a member target admits the matching user;
// team targets are inert (reserved for future team membership).
func memberHitsInvocationTargets(targets []db.AgentInvocationTarget, userID string) bool {
	for _, t := range targets {
		switch t.TargetType {
		case invocationTargetWorkspace:
			return true
		case invocationTargetMember:
			if uuidToString(t.TargetID) == userID {
				return true
			}
		}
	}
	return false
}

// canInvokeAgent is the trigger gate (MUL-3963 PR #4844). It replaces the
// fork's older visibility-based gate for INVOCATION decisions. Rules:
//
//   - agent actor  -> the top-of-chain human originator (originatorUserID)
//   - system actor -> the originator when one was resolved, else no user
//   - the agent owner may always invoke their own agent
//   - permission_mode == 'private' (or unknown): deny-by-default, no admin
//     bypass, no A2A bypass
//   - permission_mode == 'public_to': evaluate the per-agent allow-list
//     against the effective user
//
// Originator semantics: when the actor is not a member, only the
// resolved human originator is trusted (the immediate agent/system
// principal cannot self-grant). Workspaces target admits agent/system
// principals even when no human originator resolved (deliberate
// MUL-3963 exception for webhook/system automation); member/team
// targets always require a resolved human.
func (h *Handler) canInvokeAgent(ctx context.Context, agent db.Agent, actorType, actorID, originatorUserID, workspaceID string) bool {
	effectiveUser := actorID
	if actorType != "member" {
		effectiveUser = originatorUserID
	}

	if effectiveUser != "" && uuidToString(agent.OwnerID) == effectiveUser {
		return true
	}

	if agent.PermissionMode != permissionModePublicTo {
		// private (or any unknown mode) is deny-by-default: no admin
		// bypass, no A2A bypass.
		return false
	}

	targets, err := h.Queries.ListAgentInvocationTargets(ctx, agent.ID)
	if err != nil {
		return false
	}

	workspaceBroad := actorType == "agent" || actorType == "system"
	isWorkspaceMember := false
	if effectiveUser != "" {
		if _, err := h.getWorkspaceMember(ctx, effectiveUser, workspaceID); err == nil {
			isWorkspaceMember = true
		}
	}

	for _, t := range targets {
		switch t.TargetType {
		case invocationTargetWorkspace:
			if isWorkspaceMember || workspaceBroad {
				return true
			}
		case invocationTargetMember:
			if effectiveUser != "" && uuidToString(t.TargetID) == effectiveUser {
				return true
			}
		case invocationTargetTeam:
			// Reserved: team membership does not exist in V1, so team
			// targets never admit anyone.
		}
	}
	return false
}

// invokeOriginatorFromRequest resolves the top-of-chain human user id
// for an invocation initiated over HTTP. Members are their own
// originator; agent actors inherit the originator from the task named by
// the X-Task-ID header (set by the CLI on every request). Returns ""
// when no human can be attributed — canInvokeAgent then fails closed for
// member/team targets.
//
// The fork port uses parseUUID (the fork's internal helper) instead of
// util.ParseUUID — agent_access.go:177 in the upstream used util.ParseUUID;
// the fork's handler package does not import util, so parseUUID is the
// canonical alternative.
func (h *Handler) invokeOriginatorFromRequest(r *http.Request, actorType, actorID string) string {
	if actorType == "member" {
		return actorID
	}
	if actorType == "agent" {
		if taskIDHeader := r.Header.Get("X-Task-ID"); taskIDHeader != "" {
			if taskUUID, err := parseUUIDLoose(taskIDHeader); err == nil {
				if task, err := h.Queries.GetAgentTask(r.Context(), taskUUID); err == nil {
					return uuidToString(task.OriginatorUserID)
				}
			}
		}
	}
	return ""
}