// Package swarm — orchestrator_test.go (0.5.21).
//
// Unit tests for the orchestrator's pure-function helpers + the
// tickRole state machine. The orchestrator's DB-touching paths are
// exercised by integration tests (DB-backed; not in this file).

package swarm

import (
	"encoding/json"
	"testing"
)

func TestNextPhase_ResearchToDesign(t *testing.T) {
	got := NextPhase(PhaseResearch)
	if got != PhaseDesign {
		t.Fatalf("NextPhase(research) = %q, want %q", got, PhaseDesign)
	}
}

func TestNextPhase_DesignToImplement(t *testing.T) {
	got := NextPhase(PhaseDesign)
	if got != PhaseImplement {
		t.Fatalf("NextPhase(design) = %q, want %q", got, PhaseImplement)
	}
}

func TestNextPhase_ImplementToReview(t *testing.T) {
	got := NextPhase(PhaseImplement)
	if got != PhaseReview {
		t.Fatalf("NextPhase(implement) = %q, want %q", got, PhaseReview)
	}
}

func TestNextPhase_ReviewToDone(t *testing.T) {
	got := NextPhase(PhaseReview)
	if got != PhaseDone {
		t.Fatalf("NextPhase(review) = %q, want %q", got, PhaseDone)
	}
}

func TestNextPhase_DoneIsTerminal(t *testing.T) {
	got := NextPhase(PhaseDone)
	if got != PhaseDone {
		t.Fatalf("NextPhase(done) = %q, want %q (must stay terminal)", got, PhaseDone)
	}
}

func TestNextPhase_UnknownStaysAtDone(t *testing.T) {
	// Defensive: an unknown phase value shouldn't crash; the
	// fallback is PhaseDone (terminal).
	got := NextPhase(SwarmPhase("not-a-real-phase"))
	if got != PhaseDone {
		t.Fatalf("NextPhase(unknown) = %q, want %q", got, PhaseDone)
	}
}

func TestPhaseOrder_HasFiveEntries(t *testing.T) {
	// The phase machine must have exactly 5 entries — research,
	// design, implement, review, done. Adding a new phase is a
	// deliberate schema decision (CHECK widening migration).
	if len(PhaseOrder) != 5 {
		t.Fatalf("PhaseOrder has %d entries, want 5", len(PhaseOrder))
	}
	want := []SwarmPhase{PhaseResearch, PhaseDesign, PhaseImplement, PhaseReview, PhaseDone}
	for i, p := range want {
		if PhaseOrder[i] != p {
			t.Errorf("PhaseOrder[%d] = %q, want %q", i, PhaseOrder[i], p)
		}
	}
}

func TestMaxSwarmRoles_CapEnforced(t *testing.T) {
	// MaxSwarmRoles must stay ≤ 6 per the Anthropic Jun 2025
	// spawn-50 anti-pattern finding. The validator enforces this;
	// the constant is the contract.
	if MaxSwarmRoles > 6 {
		t.Fatalf("MaxSwarmRoles = %d, must be ≤ 6 (Anthropic Jun 2025 anti-pattern)", MaxSwarmRoles)
	}
	if MaxSwarmRoles < 2 {
		t.Fatalf("MaxSwarmRoles = %d, must be ≥ 2 (swarm needs ≥ 1 coordinator + 1 worker)", MaxSwarmRoles)
	}
}

func TestValidateTopologySpec_EmptyRejected(t *testing.T) {
	spec := TopologySpec{}
	err := ValidateTopologySpec(spec)
	if err == nil {
		t.Fatal("empty topology spec accepted, want error")
	}
}

func TestValidateTopologySpec_DuplicateRoleName(t *testing.T) {
	spec := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "..."},
			{Name: "researcher", Instructions: "..."},
		},
	}
	err := ValidateTopologySpec(spec)
	if err == nil {
		t.Fatal("duplicate role name accepted, want error")
	}
}

func TestValidateTopologySpec_OverCap(t *testing.T) {
	roles := make([]RoleSpec, MaxSwarmRoles+1)
	for i := range roles {
		roles[i] = RoleSpec{Name: roleNameForIndex(i), Instructions: "..."}
	}
	spec := TopologySpec{Roles: roles}
	err := ValidateTopologySpec(spec)
	if err == nil {
		t.Fatal("topology > MaxSwarmRoles accepted, want error")
	}
}

func TestValidateTopologySpec_MissingParent(t *testing.T) {
	spec := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "..."},
			{Name: "coder", Instructions: "...", ParentRoleName: "ghost"},
		},
	}
	err := ValidateTopologySpec(spec)
	if err == nil {
		t.Fatal("undeclared parent accepted, want error")
	}
}

func TestValidateTopologySpec_SelfDependency(t *testing.T) {
	spec := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "...", DependsOn: []string{"researcher"}},
		},
	}
	err := ValidateTopologySpec(spec)
	if err == nil {
		t.Fatal("self-dependency accepted, want error")
	}
}

func TestValidateTopologySpec_Valid(t *testing.T) {
	spec := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "research", ParentRoleName: ""},
			{Name: "coder", Instructions: "code", ParentRoleName: "researcher"},
			{Name: "reviewer", Instructions: "review", ParentRoleName: "coder"},
		},
	}
	if err := ValidateTopologySpec(spec); err != nil {
		t.Fatalf("valid topology rejected: %v", err)
	}
}

func TestTopologySpecFromJSON_Empty(t *testing.T) {
	spec, err := TopologySpecFromJSON(nil)
	if err != nil {
		t.Fatalf("nil raw json: %v", err)
	}
	if len(spec.Roles) != 0 {
		t.Fatalf("empty spec has %d roles, want 0", len(spec.Roles))
	}
}

func TestTopologySpecFromJSON_RoundTrip(t *testing.T) {
	src := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "research", ParentRoleName: ""},
			{Name: "coder", Instructions: "code", ParentRoleName: "researcher"},
		},
		Notes: "two-role DAG",
	}
	raw, err := json.Marshal(src)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := TopologySpecFromJSON(raw)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Roles) != len(src.Roles) {
		t.Fatalf("round-trip roles = %d, want %d", len(got.Roles), len(src.Roles))
	}
	if got.Notes != src.Notes {
		t.Errorf("round-trip notes = %q, want %q", got.Notes, src.Notes)
	}
}

func TestIsTerminal_TrueForCompletedAbortedFailed(t *testing.T) {
	for _, s := range []string{"completed", "aborted", "failed"} {
		if !isTerminal(s) {
			t.Errorf("isTerminal(%q) = false, want true", s)
		}
	}
}

func TestIsTerminal_FalseForActiveStates(t *testing.T) {
	for _, s := range []string{"preparing", "planning", "running", "monitoring", ""} {
		if isTerminal(s) {
			t.Errorf("isTerminal(%q) = true, want false", s)
		}
	}
}

func roleNameForIndex(i int) string {
	// Produces names like r0, r1, ... to keep the over-cap test
	// self-contained (no name collisions within the test slice).
	return "r" + string(rune('0'+i))
}