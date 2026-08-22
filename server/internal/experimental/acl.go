// Package experimental — acl.go (0.5.56 P4 semantica port-and-localize)
//
// Mode detection (individual vs team) and per-decision ACL helpers
// for the semantica flag. The mode is derived from
// `COUNT(member WHERE workspace_id = ?)`: single-member workspaces are
// "individual" (per-actor decisions stay private), multi-member
// workspaces are "team" (shared visibility for cross-member decisions).
//
// Storage separation lives upstream at
// apps/desktop/vendor/semantica-src/semantica/ (one graph per
// workspace, 0.5.29 P0-2); this file provides the FORK-SIDE access
// layer that decides what each viewer is allowed to read, plus the
// Visibility enum that gets stamped on every outgoing /api/decisions
// POST.
//
// ACL data flow:
//   - Write path: terminal-issue listener -> postDecisionSync computes
//     Visibility from Mode + actor_type -> UpsertSemanticaDecisionACL.
//   - Read path: GET /api/experimental/semantica/decisions filters by
//     ListSemanticaDecisionsForViewer at query time (the SQL itself
//     encodes the per-actor visibility rules — see semantica_acl.sql).
package experimental

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Actor types — mirrors the actor_type CHECK on
// semantica_local_decision_acl and on the upstream semantica
// DecisionRecord Provenance envelope (system | user | agent | team).
const (
	ActorTypeSystem = "system"
	ActorTypeUser   = "user"
	ActorTypeAgent  = "agent"
	ActorTypeTeam   = "team"
)

// Visibility values for a single decision record. Pinned to the
// CHECK in migration 273. The fork only writes these three values:
//   - "team"               — system-written team-level record (only
//                            install_semantica seeds today; P5 may add
//                            per-workspace summary records).
//   - "individual_private" — terminal-issue decision in an
//                            individual workspace (only the
//                            originating member sees it).
//   - "shared_team"        — terminal-issue decision in a team
//                            workspace (visible to all members).
const (
	VisibilityTeam             = "team"
	VisibilityIndividualPrivate = "individual_private"
	VisibilitySharedTeam        = "shared_team"
)

// Mode detection (individual | team).
const (
	ModeIndividual = "individual"
	ModeTeam       = "team"
)

// ErrACLStoreUnavailable is returned by WorkspaceMemberCount when the
// queries handle is nil — surfaces as slog.Debug at every call site
// rather than aborting the goroutine.
var ErrACLStoreUnavailable = errors.New("experimental: queries handle unavailable")

// WorkspaceMemberCount returns COUNT(*) of non-deleted member rows for
// the workspace. Single source of truth for the mode detector (1 ->
// individual, >=2 -> team). Cheap query (member rows are workspace-
// indexed); called once per terminal-issue sync, never on the hot
// path. Returns ErrACLStoreUnavailable if the queries handle is nil
// (test fixtures, startup-before-store-bound).
func WorkspaceMemberCount(ctx context.Context, queries *db.Queries, workspaceID pgtype.UUID) (int64, error) {
	if queries == nil {
		return 0, ErrACLStoreUnavailable
	}
	return queries.CountWorkspaceMembers(ctx, workspaceID)
}

// Mode returns "individual" when count == 1, "team" when count >= 2.
// count == 0 is treated as individual (a fresh workspace with no
// members yet cannot produce a team-shaped decision anyway; the
// listener will skip until at least one member exists).
func Mode(count int64) string {
	if count >= 2 {
		return ModeTeam
	}
	return ModeIndividual
}

// VisibilityFor picks the Visibility for a single outgoing decision
// POST. The matrix:
//   - team mode + actor_type=team                -> "team"
//   - team mode + actor_type=user|agent|system    -> "shared_team"
//   - individual mode + ANY actor_type           -> "individual_private"
// The function is pure: no DB access, no clock, no randomness. Pure
// so it can be unit-tested without fixtures and is safe to call from
// inside the postDecisionSync goroutine.
func VisibilityFor(mode, actorType string) string {
	switch mode {
	case ModeTeam:
		if actorType == ActorTypeTeam {
			return VisibilityTeam
		}
		return VisibilitySharedTeam
	default:
		return VisibilityIndividualPrivate
	}
}

// ActorIDFor builds the actor_id field for the ACL row at write time:
//   - actor_type=team -> literal "workspace:<uuid>" (placeholder so the
//     NOT NULL column is satisfied; the visibility rules in the
//     SELECT query key off workspace_id, not this literal, so the
//     value is otherwise inert).
//   - anything else -> the actor's UUID-as-string (member.user_id or
//     agent.id).
func ActorIDFor(actorType string, actorID pgtype.UUID, workspaceID pgtype.UUID) string {
	switch actorType {
	case ActorTypeTeam:
		return "workspace:" + uuidString(workspaceID)
	default:
		return uuidString(actorID)
	}
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x",
		id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}