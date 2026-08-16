// Package swarm — orchestrator_integration_test.go (0.5.22).
//
// Hermetic tests for the orchestrator's DB-touching decision paths
// (tick / bootstrapFromSpec / advancePhase / enqueueReadyRole). They use
// a fake OrchestratorQuerier so no real PostgreSQL is required — the fake
// embeds the interface and overrides only the methods each path touches;
// any unoverridden method called during a test panics (fail loudly rather
// than silently pass on a broken code path).
//
// The live IO paths are covered by the DB-backed integration suite under
// server/internal/handler/; this file pins the orchestrator's state
// transitions without a database.

package swarm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeQuerier implements OrchestratorQuerier by embedding the interface
// (so unoverridden methods panic) and overriding only the methods the
// tests below exercise. `calls` records every overridden method name for
// assertions; `run` / `roles` / `completedCount` / `hasRuntime` are the
// canned return values.
type fakeQuerier struct {
	OrchestratorQuerier

	run            db.SwarmRun
	roles          []db.SwarmRole
	completedCount int64
	hasRuntime     bool

	calls []string
}

func (f *fakeQuerier) GetSwarmRun(_ context.Context, _ pgtype.UUID) (db.SwarmRun, error) {
	f.calls = append(f.calls, "GetSwarmRun")
	return f.run, nil
}

func (f *fakeQuerier) ListSwarmRolesByRun(_ context.Context, _ pgtype.UUID) ([]db.SwarmRole, error) {
	f.calls = append(f.calls, "ListSwarmRolesByRun")
	return f.roles, nil
}

func (f *fakeQuerier) CountCompletedRolesByRun(_ context.Context, _ pgtype.UUID) (int64, error) {
	f.calls = append(f.calls, "CountCompletedRolesByRun")
	return f.completedCount, nil
}

func (f *fakeQuerier) SetSwarmRunPhase(_ context.Context, arg db.SetSwarmRunPhaseParams) (db.SwarmRun, error) {
	f.calls = append(f.calls, "SetSwarmRunPhase:"+arg.CurrentPhase)
	return f.run, nil
}

func (f *fakeQuerier) SetSwarmRunStatus(_ context.Context, arg db.SetSwarmRunStatusParams) (db.SwarmRun, error) {
	f.calls = append(f.calls, "SetSwarmRunStatus:"+arg.Status)
	return f.run, nil
}

func (f *fakeQuerier) SetSwarmRoleStatus(_ context.Context, arg db.SetSwarmRoleStatusParams) (db.SwarmRole, error) {
	f.calls = append(f.calls, "SetSwarmRoleStatus:"+arg.Status)
	return db.SwarmRole{}, nil
}

func (f *fakeQuerier) CreateAgent(_ context.Context, arg db.CreateAgentParams) (db.Agent, error) {
	f.calls = append(f.calls, "CreateAgent:"+arg.Name)
	return db.Agent{ID: newSwarmTestUUID()}, nil
}

func (f *fakeQuerier) CreateSwarmRole(_ context.Context, arg db.CreateSwarmRoleParams) (db.SwarmRole, error) {
	f.calls = append(f.calls, "CreateSwarmRole:"+arg.RoleName)
	return db.SwarmRole{ID: newSwarmTestUUID(), RoleName: arg.RoleName, Status: string(RoleCreated)}, nil
}

func (f *fakeQuerier) AgentHasOnlineRuntime(_ context.Context, _ pgtype.UUID) (bool, error) {
	f.calls = append(f.calls, "AgentHasOnlineRuntime")
	return f.hasRuntime, nil
}

// 0.5.22 P0 fix: bootstrapFromSpec now binds runtime_id via
// GetOnlineRuntimeByWorkspace + UpsertAgentRuntime. fakeQuerier
// returns a stable synthetic id so tests can pin the runtime_id
// propagation end-to-end.
func (f *fakeQuerier) GetOnlineRuntimeByWorkspace(_ context.Context, _ pgtype.UUID) (pgtype.UUID, error) {
	f.calls = append(f.calls, "GetOnlineRuntimeByWorkspace")
	if f.hasRuntime {
		return newSwarmTestUUID(), nil
	}
	return pgtype.UUID{}, nil
}

func (f *fakeQuerier) UpsertAgentRuntime(_ context.Context, arg db.UpsertAgentRuntimeParams) (db.UpsertAgentRuntimeRow, error) {
	f.calls = append(f.calls, "UpsertAgentRuntime:"+arg.Provider)
	return db.UpsertAgentRuntimeRow{ID: newSwarmTestUUID()}, nil
}

// 0.5.22 P1-12 fix: bootstrapFromSpec now writes a visibility row
// for every role-agent so it hides from regular pickers (mirrors
// product_swarm_coordinator.go:121-130). fakeQuerier records the
// call so tests can pin that the wiring fires.
func (f *fakeQuerier) InsertExperimentalResourceVisibility(_ context.Context, arg db.InsertExperimentalResourceVisibilityParams) error {
	f.calls = append(f.calls, "InsertExperimentalResourceVisibility:"+arg.ResourceType)
	return nil
}

// 0.5.22 P1-4 fix: enqueueReadyRole now uses EnqueueSwarmRoleTask
// (atomic INSERT + UPDATE) instead of CreateAgentTask +
// SetSwarmRoleStatus. fakeQuerier records the call so tests can pin
// the new atomic path.
func (f *fakeQuerier) EnqueueSwarmRoleTask(_ context.Context, arg db.EnqueueSwarmRoleTaskParams) error {
	f.calls = append(f.calls, "EnqueueSwarmRoleTask")
	_ = arg
	return nil
}

func (f *fakeQuerier) CreateSwarmRoleMessage(_ context.Context, arg db.CreateSwarmRoleMessageParams) (db.SwarmRoleMessage, error) {
	f.calls = append(f.calls, "CreateSwarmRoleMessage:"+arg.Type)
	return db.SwarmRoleMessage{}, nil
}

// called reports whether any recorded call starts with prefix.
func (f *fakeQuerier) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// countCalls returns the number of recorded calls starting with prefix.
func (f *fakeQuerier) countCalls(prefix string) int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func newSwarmTestUUID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

// TestTickPausedSkipsAdvance: while is_paused is true, tick must read the
// run row and return without bootstrapping, ticking roles, enqueueing, or
// advancing the phase. Roles stay in their current state and no new work
// is written.
func TestTickPausedSkipsAdvance(t *testing.T) {
	runID := newSwarmTestUUID()
	f := &fakeQuerier{
		run: db.SwarmRun{
			ID:           runID,
			Status:       string(StatusRunning),
			CurrentPhase: string(PhaseResearch),
			IsPaused:     true,
		},
	}
	svc := NewService(f, nil)

	if err := svc.tick(context.Background(), runID); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := len(f.calls); got != 1 {
		t.Fatalf("expected exactly 1 query call while paused, got %d: %v", got, f.calls)
	}
	if f.calls[0] != "GetSwarmRun" {
		t.Fatalf("expected first call to be GetSwarmRun, got %q", f.calls[0])
	}
}

// TestTickAdvancePhaseWhenAllCompleted: with every role 'completed', the
// tick's advancePhase gate flips current_phase to the next phase and
// resets each role to 'ready' for the next phase.
func TestTickAdvancePhaseWhenAllCompleted(t *testing.T) {
	runID := newSwarmTestUUID()
	f := &fakeQuerier{
		run: db.SwarmRun{
			ID:           runID,
			Status:       string(StatusRunning),
			CurrentPhase: string(PhaseResearch),
		},
		roles: []db.SwarmRole{
			{ID: newSwarmTestUUID(), RoleName: "researcher", Status: string(RoleCompleted)},
			{ID: newSwarmTestUUID(), RoleName: "coder", Status: string(RoleCompleted)},
		},
		completedCount: 2,
	}
	svc := NewService(f, nil)

	if err := svc.tick(context.Background(), runID); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if !f.called("SetSwarmRunPhase:design") {
		t.Fatalf("expected phase advance to design, got calls: %v", f.calls)
	}
	if got := f.countCalls("SetSwarmRoleStatus:ready"); got != 2 {
		t.Fatalf("expected 2 role resets to ready, got %d: %v", got, f.calls)
	}
}

// TestTickBootstrapFromSpec: a run carrying a 3-role topology_spec must
// create 3 role-agents + 3 swarm_role rows and flip the run from
// 'planning' to 'running'.
func TestTickBootstrapFromSpec(t *testing.T) {
	runID := newSwarmTestUUID()
	spec := TopologySpec{
		Roles: []RoleSpec{
			{Name: "researcher", Instructions: "research"},
			{Name: "coder", Instructions: "code", ParentRoleName: "researcher"},
			{Name: "reviewer", Instructions: "review", ParentRoleName: "coder"},
		},
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	run := db.SwarmRun{
		ID:           runID,
		Status:       string(StatusPlanning),
		CurrentPhase: string(PhaseResearch),
		TopologySpec: specJSON,
	}
	f := &fakeQuerier{
		run: run,
	}
	svc := NewService(f, nil)

	// 0.5.22 P2-16 fix: bootstrapFromSpec now takes the run row,
	// not the run id (the caller already loaded it).
	if err := svc.bootstrapFromSpec(context.Background(), run); err != nil {
		t.Fatalf("bootstrapFromSpec: %v", err)
	}

	if got := f.countCalls("CreateSwarmRole:"); got != 3 {
		t.Fatalf("expected 3 swarm_role rows, got %d: %v", got, f.calls)
	}
	if got := f.countCalls("CreateAgent:"); got != 3 {
		t.Fatalf("expected 3 role-agents, got %d: %v", got, f.calls)
	}
	// 0.5.22 audit fix (P1-12): every role-agent must get an
	// experimental_resource_visibility row so ListAgents/GetAgent
	// stamps lab_managed=true and the role stays hidden from regular
	// pickers (CLAUDE.md Active Contract #4). Without this assertion
	// the visibility wiring could silently regress — the role-agent
	// would surface in the AssigneePicker despite the swarm_topology
	// mutex forbidding manual assignee.
	if got := f.countCalls("InsertExperimentalResourceVisibility:agent"); got != 3 {
		t.Fatalf("expected 3 visibility rows for role-agents, got %d: %v", got, f.calls)
	}
	if !f.called("SetSwarmRunStatus:running") {
		t.Fatalf("expected planning → running flip, got calls: %v", f.calls)
	}
}

// TestEnqueueReadyRoleSkipsWithoutRuntime: a ready role whose agent has no
// online runtime must NOT be enqueued (the daemon could never claim it);
// instead an error message is written and the role stays 'ready'.
func TestEnqueueReadyRoleSkipsWithoutRuntime(t *testing.T) {
	runID := newSwarmTestUUID()
	role := db.SwarmRole{
		ID:       newSwarmTestUUID(),
		AgentID:  newSwarmTestUUID(),
		RoleName: "coder",
		Status:   string(RoleReady),
	}
	f := &fakeQuerier{
		run: db.SwarmRun{
			ID:           runID,
			Status:       string(StatusRunning),
			CurrentPhase: string(PhaseResearch),
		},
		hasRuntime: false,
	}
	svc := NewService(f, nil)

	if err := svc.enqueueReadyRole(context.Background(), f.run, role); err != nil {
		t.Fatalf("enqueueReadyRole: %v", err)
	}

	if f.called("CreateAgentTask:") {
		t.Fatalf("must NOT enqueue without a live runtime, calls: %v", f.calls)
	}
	if !f.called("CreateSwarmRoleMessage:error") {
		t.Fatalf("expected an error message for missing runtime, calls: %v", f.calls)
	}
}
