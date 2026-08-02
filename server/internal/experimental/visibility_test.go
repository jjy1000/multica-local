package experimental

import (
	"testing"

	"github.com/google/uuid"
)

// TestAgentSelfOptimizationIDsAreValid exercises the constants in
// visibility.go. The flags are deployment-fixed UUIDs; if any of them
// is malformed the catalog helper will panic on init, but a unit test
// gives a clearer failure mode and documents the intent.
//
// 0.5.6: the `agent_self_optimization` catalog literal is removed.
// The visibility constants are still referenced by migration 237
// (which cleans up the corresponding lock / visibility rows), so
// the UUIDs must stay valid.
func TestAgentSelfOptimizationIDsAreValid(t *testing.T) {
	t.Parallel()

	if AgentSelfOptimizationAgentID() == uuid.Nil {
		t.Fatal("agent_self_optimization agent id is zero UUID")
	}
	autopilotIDs := AgentSelfOptimizationAutopilotIDs()
	if len(autopilotIDs) != 2 {
		t.Fatalf("agent_self_optimization autopilot ids: got %d, want 2", len(autopilotIDs))
	}
	for _, id := range autopilotIDs {
		if id == uuid.Nil {
			t.Fatal("agent_self_optimization autopilot id is zero UUID")
		}
	}
	if AgentSelfOptimizationSkillID() == uuid.Nil {
		t.Fatal("agent_self_optimization skill id is zero UUID")
	}
}

// TestAgentSelfOptimizationFlagRemovedFromCatalog pins the 0.5.6
// contract: the `agent_self_optimization` flag is a product-level
// resource, not a Labs tier flag, so the catalog literal must be
// gone. (Visibility constants in visibility.go are kept — they
// are still referenced by migration 237 to identify the rows to
// delete — but the catalog entry that would have driven the
// 0.3.17 per-flag gate is removed.) A future cleanup that
// silently re-adds the catalog literal will fail loudly here.
func TestAgentSelfOptimizationFlagRemovedFromCatalog(t *testing.T) {
	t.Parallel()

	for _, f := range Catalog {
		if f.Key == "agent_self_optimization" {
			t.Fatal("agent_self_optimization must NOT be in catalog (0.5.6: product-level resource)")
		}
	}
}

// TestIsKnownHideableResource guards the resource type enum against
// drift. If someone adds a new resource_type to the migration CHECK
// without updating visibility.go, ListHiddenResourceIDs will reject
// it at runtime; this test makes the supported set explicit.
func TestIsKnownHideableResource(t *testing.T) {
	t.Parallel()

	for _, want := range []HideableResource{HideAgent, HideAutopilot, HideSkill} {
		if !IsKnownHideableResource(want) {
			t.Errorf("IsKnownHideableResource(%q) = false, want true", want)
		}
	}
	for _, bad := range []HideableResource{"", "user", "workspace", "AGENT"} {
		if IsKnownHideableResource(bad) {
			t.Errorf("IsKnownHideableResource(%q) = true, want false", bad)
		}
	}
}

// TestUUIDsToPgtype checks the helper used by List queries to embed
// the hidden-id set into a `WHERE id NOT IN ($1::uuid[])` clause.
// Empty input must still produce a non-nil slice so the placeholder
// binds; sqlc-generated pgtype.UUID comparisons panic on nil slices.
func TestUUIDsToPgtype(t *testing.T) {
	t.Parallel()

	if got := UUIDsToPgtype(nil); got == nil || len(got) != 0 {
		t.Fatalf("UUIDsToPgtype(nil) = %v, want non-nil empty slice", got)
	}
	if got := UUIDsToPgtype([]uuid.UUID{}); got == nil || len(got) != 0 {
		t.Fatalf("UUIDsToPgtype([]) = %v, want non-nil empty slice", got)
	}
	ids := []uuid.UUID{
		uuid.MustParse("6a647967-f56e-4661-ad39-774420b870d4"),
		uuid.MustParse("f788217e-ef6a-4af0-a5a1-cbf85d8dbb8e"),
	}
	got := UUIDsToPgtype(ids)
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	for i, want := range ids {
		if !got[i].Valid {
			t.Errorf("got[%d] = invalid, want valid", i)
		}
		if got[i].Bytes != want {
			t.Errorf("got[%d] = %v, want %v", i, got[i].Bytes, want)
		}
	}
}

// (0.5.6: the `agent_self_optimization` description rendering test
// was removed alongside the catalog literal. The agent / autopilot
// / skill still carry their own descriptions in the DB; that
// coverage is not lost — it is exercised by the
// `agent_self_optimization` work-in-progress view (0.5.5.x kept
// the view; 0.5.6 removed it). The title / description i18n keys
// are kept in packages/views/locales for any future re-introduction.)
//
// (no constitution_agent visibility tests remain — the flag was
// retired in 0.3.57 with migration 165. Visibility constants were
// removed alongside the catalog entry; nothing here to assert.)
