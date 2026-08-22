// Package experimental — acl_test.go (0.5.56 P4)
//
// Pure-function tests for VisibilityFor / Mode / ActorIDFor. The DB
// helpers (WorkspaceMemberCount, UpsertSemanticaDecisionACL,
// ListSemanticaDecisionsForViewer) are tested at the integration level
// via the handler tests — this file exercises the deterministic
// helpers only so it can run without a live PG.
package experimental

import "testing"

func TestMode(t *testing.T) {
	cases := []struct {
		count int64
		want  string
	}{
		{0, ModeIndividual}, // empty workspace -> individual (no team can form)
		{1, ModeIndividual}, // single member
		{2, ModeTeam},
		{42, ModeTeam},
	}
	for _, c := range cases {
		if got := Mode(c.count); got != c.want {
			t.Errorf("Mode(%d) = %q, want %q", c.count, got, c.want)
		}
	}
}

func TestVisibilityFor(t *testing.T) {
	cases := []struct {
		mode, actor string
		want        string
	}{
		// individual mode — every actor type yields private visibility
		{ModeIndividual, ActorTypeSystem, VisibilityIndividualPrivate},
		{ModeIndividual, ActorTypeUser, VisibilityIndividualPrivate},
		{ModeIndividual, ActorTypeAgent, VisibilityIndividualPrivate},
		{ModeIndividual, ActorTypeTeam, VisibilityIndividualPrivate},

		// team mode — system-written team records stay "team";
		// everything else becomes "shared_team"
		{ModeTeam, ActorTypeTeam, VisibilityTeam},
		{ModeTeam, ActorTypeSystem, VisibilitySharedTeam},
		{ModeTeam, ActorTypeUser, VisibilitySharedTeam},
		{ModeTeam, ActorTypeAgent, VisibilitySharedTeam},

		// unknown mode falls through to individual_private (defensive)
		{"weird", ActorTypeUser, VisibilityIndividualPrivate},
	}
	for _, c := range cases {
		if got := VisibilityFor(c.mode, c.actor); got != c.want {
			t.Errorf("VisibilityFor(%q,%q) = %q, want %q", c.mode, c.actor, got, c.want)
		}
	}
}

func TestActorIDFor_TeamUsesWorkspaceLiteral(t *testing.T) {
	// The actor_id for team-mode records is "workspace:<uuid>" so the
	// NOT NULL constraint is satisfied; the visibility SQL keys off
	// workspace_id, not this literal, so the value is otherwise inert.
	if got := ActorIDFor(ActorTypeTeam, pgtypeUUIDEmpty(), pgtypeUUIDFromString("11111111-2222-3333-4444-555555555555")); got != "workspace:11111111-2222-3333-4444-555555555555" {
		t.Errorf("ActorIDFor(team) = %q", got)
	}
}

func TestActorIDFor_UserPassesThrough(t *testing.T) {
	if got := ActorIDFor(ActorTypeUser, pgtypeUUIDFromString("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"), pgtypeUUIDEmpty()); got != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" {
		t.Errorf("ActorIDFor(user) = %q", got)
	}
}