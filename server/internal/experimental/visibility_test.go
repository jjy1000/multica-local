package experimental

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestAgentSelfOptimizationIDsAreValid exercises the constants in
// visibility.go. The flags are deployment-fixed UUIDs; if any of them
// is malformed the catalog helper will panic on init, but a unit test
// gives a clearer failure mode and documents the intent.
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

// TestCatalogHasAgentSelfOptimizationFlag locks in the catalog
// presence so a future cleanup that drops the flag without removing
// the visibility constants will fail loudly here.
func TestCatalogHasAgentSelfOptimizationFlag(t *testing.T) {
	t.Parallel()

	for _, f := range Catalog {
		if f.Key == "agent_self_optimization" {
			if f.DefaultVal {
				t.Fatal("agent_self_optimization must default to false per Labs constraint")
			}
			return
		}
	}
	t.Fatal("agent_self_optimization missing from experimental.Catalog")
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

// TestAgentSelfOptimizationDescriptions renders the bilingual title
// + description so a translator dropping one of the locales surfaces
// as a test failure rather than an empty UI card.
func TestAgentSelfOptimizationDescriptions(t *testing.T) {
	t.Parallel()

	var flag *Flag
	for i, f := range Catalog {
		if f.Key == "agent_self_optimization" {
			flag = &Catalog[i]
			break
		}
	}
	if flag == nil {
		t.Fatal("agent_self_optimization flag missing from catalog")
	}
	if strings.TrimSpace(flag.Title.En) == "" {
		t.Error("agent_self_optimization title.en is empty")
	}
	if strings.TrimSpace(flag.Title.Zh) == "" {
		t.Error("agent_self_optimization title.zh is empty")
	}
	if strings.TrimSpace(flag.Description.En) == "" {
		t.Error("agent_self_optimization description.en is empty")
	}
	if strings.TrimSpace(flag.Description.Zh) == "" {
		t.Error("agent_self_optimization description.zh is empty")
	}
}

// TestConstitutionAgentIDsAreValid mirrors the
// agent_self_optimization counterpart for the 0.3.20
// constitution_agent flag. The agent id + 3 autopilot ids must all be
// non-zero UUIDs; if any of them is malformed the catalog helper will
// panic on init, but a unit test gives a clearer failure mode.
func TestConstitutionAgentIDsAreValid(t *testing.T) {
	t.Parallel()

	if ConstitutionAgentAgentID() == uuid.Nil {
		t.Fatal("constitution_agent agent id is zero UUID")
	}
	autopilotIDs := ConstitutionAgentAutopilotIDs()
	if len(autopilotIDs) != 3 {
		t.Fatalf("constitution_agent autopilot ids: got %d, want 3 (CTR + CSIL + TAOL)", len(autopilotIDs))
	}
	for _, id := range autopilotIDs {
		if id == uuid.Nil {
			t.Fatal("constitution_agent autopilot id is zero UUID")
		}
	}
	// The constitution_agent Skill content ships via the experiment
	// boot loader (not in the workspace skill table), so there is no
	// skill UUID to assert here. The flag-off bypass contract is
	// verified end-to-end by the list-handler integration tests.
}

// TestCatalogHasConstitutionAgentFlag locks in the catalog presence
// so a future cleanup that drops the flag without removing the
// visibility constants will fail loudly here.
func TestCatalogHasConstitutionAgentFlag(t *testing.T) {
	t.Parallel()

	var flag *Flag
	for i, f := range Catalog {
		if f.Key == "constitution_agent" {
			flag = &Catalog[i]
			break
		}
	}
	if flag == nil {
		t.Fatal("constitution_agent missing from experimental.Catalog")
	}
	if flag.DefaultVal {
		t.Fatal("constitution_agent must default to false per Labs constraint")
	}
	if strings.TrimSpace(flag.Title.En) == "" {
		t.Error("constitution_agent title.en is empty")
	}
	if strings.TrimSpace(flag.Title.Zh) == "" {
		t.Error("constitution_agent title.zh is empty")
	}
	if strings.TrimSpace(flag.Description.En) == "" {
		t.Error("constitution_agent description.en is empty")
	}
	if strings.TrimSpace(flag.Description.Zh) == "" {
		t.Error("constitution_agent description.zh is empty")
	}
	if flag.ManifestPath == "" {
		t.Error("constitution_agent must carry a manifest path (0.3.19 contract)")
	}
	if flag.RuntimeKind != "inline" {
		t.Errorf("constitution_agent runtime_kind = %q, want inline", flag.RuntimeKind)
	}
}
