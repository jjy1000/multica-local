// Package experimental: source / resource-type enum + lock helpers.
//
// The lock package is the single seam between the Labs flag toggle and
// the underlying domain tables (skill / agent / squad / member /
// workspace / mcp_server). It exists so:
//
//   - Lab flag toggles can show/hide every resource attached to the lab
//     with a single UPDATE (Hide / Restore below), without each handler
//     needing to know which domain tables the lab touches.
//
//   - Write handlers (PR 2) can reject user-driven edits / deletes with
//     a uniform ErrLocked when the lab has claimed the resource. The
//     rejection works regardless of which user / role attempts the
//     write — the lab owns the resource for the duration of the claim.
//
// The lock is purely an overlay table; the underlying domain rows
// themselves are never moved or renamed. CLAUDE.md's "migrations are
// forward-only" rule is honored because the table is additive (PR 1
// ships only the table + helpers, no domain schema changes).
package experimental

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// LockQuerier is the minimal slice of sqlc's *db.Queries surface the
// lock package actually calls. Defining it here keeps the lock package
// decoupled from *db.Queries so unit tests can pass a fake without
// dragging in every other query the production code calls.
//
// Named LockQuerier (not Querier) to avoid colliding with the
// package-local Querier interface in provider.go — that one serves the
// featureflag.UserPrefProvider and has a different method set.
//
// The production caller in this fork pulls *db.Queries off the
// request-scoped transaction (see handler dependency wiring in
// server/internal/handler/experimental_resources.go), then passes it
// directly: *db.Queries satisfies LockQuerier structurally.
type LockQuerier interface {
	InsertExperimentalResourceLock(ctx context.Context, arg db.InsertExperimentalResourceLockParams) error
	HideExperimentalResourceLocksBySource(ctx context.Context, experimentalSource string) (int64, error)
	RestoreExperimentalResourceLocksBySource(ctx context.Context, experimentalSource string) (int64, error)
	RestoreExperimentalResourceLockByID(ctx context.Context, arg db.RestoreExperimentalResourceLockByIDParams) (int64, error)
	IsExperimentalResourceHidden(ctx context.Context, arg db.IsExperimentalResourceHiddenParams) (bool, error)
	GetExperimentalResourceLock(ctx context.Context, arg db.GetExperimentalResourceLockParams) (db.ExperimentalResourceLock, error)
	CountExperimentalResourceLocksByType(ctx context.Context, experimentalSource string) ([]db.CountExperimentalResourceLocksByTypeRow, error)
}

// Source is the closed enum of experimental surfaces that can attach
// resources via this package. Adding a second surface (e.g. pythia_oracle)
// means appending a constant here AND extending the CHECK constraint on
// experimental_resource_lock.experimental_source in a future migration.
//
// Constants live alongside the type so package users do not have to
// chase a registry file. Same single source of truth as the SQL enum.
type Source string

const (
	// SourceClaudeScience is the 0.3.20 lab source. Deprecated as of
	// 0.3.22 — new lab installs use SourceClaudeScienceLab. Existing
	// rows are kept so prior lock claims remain valid; the catalog no
	// longer registers a flag for this source.
	SourceClaudeScience Source = "claude_science"
	// SourceClaudeScienceLab is the 0.3.22+ lab source. Used by the
	// consolidated Claude Research Lab flag (`claude_science_lab`),
	// which folds in the 0.3.20 `claude_science` + `claude_science_runtime`
	// pair.
	SourceClaudeScienceLab Source = "claude_science_lab"
	// SourceMythosSwarm is the Mythos Swarm RDT topology lab. Added
	// in 0.3.16-patch.1; the install handler provisions the dedicated
	// mythos-swarm workspace + 5 Mythos agents + 1 squad under this
	// source.
	SourceMythosSwarm Source = "mythos_swarm"
	// 0.3.27 B4: source for the agent_self_optimization lab. Same
	// rationale as the deprecated SourceConstitutionAgent (retired in
	// 0.3.57, migration 165). 0.5.6: the catalog literal is removed
	// but the source string is preserved so historical
	// `experimental_resource_lock` rows still match by string, and
	// migration 237 can clean them up.
	SourceAgentSelfOptimization Source = "agent_self_optimization"
	// 0.3.54: source for the pythia_oracle lab. Used by
	// install_pythia.go which provisions a `pythia_runtime` leader
	// agent bound to the multica-pythia skill. The Python process
	// itself is started by the desktop manager-factory, not the
	// server — the source here is the agent / skill / lock rows
	// attached to the lab.
	SourcePythiaOracle Source = "pythia_oracle"
	// 0.3.54: source for the code_canvas internal pilot lab. Used
	// by install_code_canvas.go which provisions a `code_canvas_worker`
	// agent + the bundled run.sh /health stub subprocess.
	SourceCodeCanvas Source = "code_canvas"
	// 0.5.3: source for the agent_creation_studio lab (0.3.45 action
	// entry, upgraded to an issue-bound lab). 0.5.6: catalog literal
	// removed; the source string stays so historical lock rows still
	// match by string, and migration 237 can clean them up.
	SourceAgentCreationStudio Source = "agent_creation_studio"
	// SourceSwarmTopology is the 0.5.21 swarm topology feature —
	// top-level task mode (parallel to claude_science_lab), NOT a
	// LabPicker sub-plugin. Self-organising multi-agent system:
	// the orchestrator authors N role-agents + M skills + 1
	// coordinating squad on bootstrap, runs a 5-phase machine, and
	// tears everything down on terminal status. See
	// server/internal/service/swarm/orchestrator.go.
	SourceSwarmTopology Source = "swarm_topology"
	// SourceSemantica is the 0.5.22 Semantica × Multica integration lab.
	// Owns the `semantica_decision_advisor` leader agent (the only
	// installable resource) so the P0#4 leader-rewrite path can land
	// `multica lab delegate semantica "<task>"` jobs on the right agent.
	// The Semantica FastAPI subprocess lifecycle itself is owned by the
	// desktop manager-factory; the install handler only writes the DB
	// rows that the daemon auto-dispatch path lands on.
	SourceSemantica Source = "semantica"
	// SourceTimesfm is the 0.5.82 WL2 TimesFM forecasting lab. Owns the
	// `timesfm_oracle` leader agent (the only installable resource).
	// The vendored torch-stack subprocess lifecycle is owned by the
	// desktop manager-factory; install_timesfm.go only writes the DB
	// rows (lock + visibility) that the issue-driven dispatch path
	// lands on. NOTE the flag-key string duplication law: the literal
	// "timesfm" is VERBATIM across experimental/handler/desktop
	// packages (import cycles forbid sharing a constant) — pinned by
	// TestCatalogAutoDispatchContract + the migration 275 static test.
	SourceTimesfm Source = "timesfm"
)

// AllSources is the developer-facing read-only list of every known
// Source. Used for validation in the install / rollback handlers (PR 3)
// and for documentation in the labs UI. 0.3.26: SourceClaudeScienceLab
// added so the consolidated flag (the one currently in `Catalog`) is
// recognized by every helper that enumerates sources. The legacy
// `claude_science` source remains because prior 0.3.20 lock claims
// are still valid rows in experimental_resource_lock.
var AllSources = []Source{
	SourceClaudeScience,
	SourceClaudeScienceLab,
	SourceMythosSwarm,
	SourceAgentSelfOptimization,
	SourcePythiaOracle,
	SourceCodeCanvas,
	SourceAgentCreationStudio,
	SourceSwarmTopology,
	SourceSemantica,
	SourceTimesfm,
}

// Valid reports whether s is in AllSources.
func (s Source) Valid() bool {
	for _, v := range AllSources {
		if v == s {
			return true
		}
	}
	return false
}

// ResourceType enumerates the kinds of domain rows a lock can attach
// to. Each value MUST match a CHECK constraint on
// experimental_resource_lock.resource_type.
type ResourceType string

const (
	LockWorkspace  ResourceType = "workspace"
	LockSkill      ResourceType = "skill"
	LockAgent      ResourceType = "agent"
	LockSquad      ResourceType = "squad"
	LockMember     ResourceType = "member"
	LockMCPServer  ResourceType = "mcp_server"
	// 0.5.22 (audit fix 2026-08-16): swarm_run is the per-row
	// lock target for the swarm topology orchestrator. The
	// handler/swarm_run.go::PostSwarmRun path claims one lock per
	// swarm_run row so the GC can release it on archive; the SQL
	// CHECK was widened in mig 244 to admit this value.
	LockSwarmRun ResourceType = "swarm_run"
)

// ErrLocked is the public error returned by handleLockedWrite when a
// write attempt targets a resource the lab has claimed. Handlers
// translate this to 423 Locked or 409 Conflict depending on the
// verb (PR 2).
type ErrLocked struct {
	Source Source
	Type   ResourceType
	ResID  pgtype.UUID
}

func (e ErrLocked) Error() string {
	return fmt.Sprintf("experimental lock: resource %s/%s is owned by lab %q and cannot be edited, optimized, or deleted",
		e.Type, e.ResID, e.Source)
}

// MarkerIDNamespace is the byte prefix used by LifecycleMarker to
// derive a stable pgtype.UUID from a flag key. The 0.3.15
// pgtypeUUIDZero marker used the all-zero UUID; that collides with
// the "real" lock row that the install dispatcher inserts after
// provisioning the workspace (the marker and the real row are
// distinguished only by resource_type). 0.3.19 P6 derives the marker
// id from the flag key so each lab has its own deterministic
// marker UUID — easier to query, impossible to collide.
const MarkerIDNamespace byte = 0xEC

// LifecycleMarker returns a stable pgtype.UUID for use as the
// "marker" lock row that a source inserts when its install runs
// without a heavy installer. The id is derived by
// SHA-256(flagKey)[0..16] XOR MarkerIDNamespace so:
//   - each flag has a unique, reproducible marker;
//   - marker ids are not the all-zero UUID (which previously
//     collided with "no row at all" semantics);
//   - the derivation is one-way enough that a future contributor
//     cannot construct a marker by hand without re-running this
//     function.
func LifecycleMarker(flagKey string) pgtype.UUID {
	var out [16]byte
	sum := sha256.Sum256([]byte("multica-labs-marker:" + flagKey))
	copy(out[:], sum[:16])
	out[0] = out[0] ^ MarkerIDNamespace
	return pgtype.UUID{Bytes: out, Valid: true}
}

// ErrUnknownSource is returned by Claim / Hide / Restore when a caller
// passes a Source outside AllSources. The HTTP layer maps this to a
// 400 Bad Request.
var ErrUnknownSource = errors.New("experimental lock: unknown source")

// ErrUnknownResourceType is returned by Claim when a caller passes a
// ResourceType the SQL enum does not allow. Mapped to 400 Bad Request.
var ErrUnknownResourceType = errors.New("experimental lock: unknown resource type")

// Lock-release contract by resource type (labs audit 2026-08-24 residual):
//
//   - agent / squad / skill / member / workspace rows left behind after a
//     hard delete are swept by the periodic orphan GC
//     (lock_gc.SweepOrphanedExperimentalResources, wired into the swarm_gc
//     6h tick; migration 274 did the one-shot backfill).
//   - swarm_run locks are released by the swarm runner itself
//     (swarm_gc.go DeleteExperimentalResourceLockByID).
//   - **mcp_server has NO GC fallback, on purpose.** The schema CHECK allows
//     it but neither migration 274 nor the sweep covers resource_type
//     'mcp_server' (no writer produces those rows today). Any future lab
//     that claims an mcp_server lock MUST release it through its own
//     lifecycle (install/rollback or runtime teardown) — orphaned rows will
//     linger silently. Extend the sweep BEFORE the first such writer lands.

// Claim attaches a lock to (source, type, id). Idempotent: re-claiming
// the same triple is a no-op (the SQL ON CONFLICT DO NOTHING swallows
// the duplicate). The new row is created with hidden=false; use Hide
// below to flip visibility.
//
// Returns ErrUnknownSource when src ∉ AllSources, ErrUnknownResourceType
// when rt is not one of the Lock* constants. Either error is programmer
// error and should be impossible to trigger from the runtime.
func Claim(ctx context.Context, q LockQuerier, src Source, rt ResourceType, id pgtype.UUID) error {
	if !src.Valid() {
		return ErrUnknownSource
	}
	switch rt {
	case LockWorkspace, LockSkill, LockAgent, LockSquad, LockMember, LockMCPServer, LockSwarmRun:
	default:
		return ErrUnknownResourceType
	}
	if err := q.InsertExperimentalResourceLock(ctx, db.InsertExperimentalResourceLockParams{
		ExperimentalSource: string(src),
		ResourceType:       string(rt),
		ResourceID:         id,
	}); err != nil {
		return err
	}
	return nil
}

// Hide flips every row attached to src to hidden=true. Returns the
// number of rows touched so the rollback handler can report "hidden N
// rows" without a second round-trip.
func Hide(ctx context.Context, q LockQuerier, src Source) (int, error) {
	if !src.Valid() {
		return 0, ErrUnknownSource
	}
	rows, err := q.HideExperimentalResourceLocksBySource(ctx, string(src))
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// Restore flips every hidden row attached to src back to visible.
// Returns the number of rows touched. Used by install() in PR 4 when a
// user re-toggles the lab on after a previous rollback.
func Restore(ctx context.Context, q LockQuerier, src Source) (int, error) {
	if !src.Valid() {
		return 0, ErrUnknownSource
	}
	rows, err := q.RestoreExperimentalResourceLocksBySource(ctx, string(src))
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// RestoreOne flips a single (src, rt, id) lock back to visible, leaving
// every other row attached to src untouched. It is the by-row companion
// to Restore (which un-hides the whole source). The install path uses it
// to keep a workspace lifecycle marker visible after the blanket
// Hide(src) that suppresses lab resources from the main pickers, so the
// marker's Visible count drives "installed" status without leaking any
// real resource back into the picker.
//
// Returns the number of rows touched (0 when the lock is absent or
// already visible). Same source / resource-type validation as Claim.
func RestoreOne(ctx context.Context, q LockQuerier, src Source, rt ResourceType, id pgtype.UUID) (int, error) {
	if !src.Valid() {
		return 0, ErrUnknownSource
	}
	switch rt {
	case LockWorkspace, LockSkill, LockAgent, LockSquad, LockMember, LockMCPServer, LockSwarmRun:
	default:
		return 0, ErrUnknownResourceType
	}
	rows, err := q.RestoreExperimentalResourceLockByID(ctx, db.RestoreExperimentalResourceLockByIDParams{
		ExperimentalSource: string(src),
		ResourceType:       string(rt),
		ResourceID:         id,
	})
	if err != nil {
		return 0, err
	}
	return int(rows), nil
}

// IsHidden reports whether a lock exists for (src, rt, id) AND that
// lock is currently hidden. The handler helper layer (PR 2) wraps
// this to return false on both "no lock" and "lock visible".
//
// Use this in the user-facing read path; it is the cheap read the
// skill / agent picker hits on every render.
func IsHidden(ctx context.Context, q LockQuerier, src Source, rt ResourceType, id pgtype.UUID) (bool, error) {
	if !src.Valid() {
		return false, ErrUnknownSource
	}
	row, err := q.IsExperimentalResourceHidden(ctx, db.IsExperimentalResourceHiddenParams{
		ExperimentalSource: string(src),
		ResourceType:       string(rt),
		ResourceID:         id,
	})
	if err != nil {
		return false, err
	}
	return row, nil
}

// Lookup returns the full lock row for (src, rt, id). The zero-value
// (with sql.ErrNoRows underneath) means "no claim". The caller checks
// .Hidden to decide between "lab-owned but visible" (allow writes) and
// "lab-owned and hidden" (reject writes with ErrLocked).
//
// Use this in write handlers — IsHidden above is the read-path
// variant. Lookup exists so write handlers can also surface the
// lock metadata (when claimed, by what) in the 423 response.
func Lookup(ctx context.Context, q LockQuerier, src Source, rt ResourceType, id pgtype.UUID) (db.ExperimentalResourceLock, error) {
	if !src.Valid() {
		return db.ExperimentalResourceLock{}, ErrUnknownSource
	}
	return q.GetExperimentalResourceLock(ctx, db.GetExperimentalResourceLockParams{
		ExperimentalSource: string(src),
		ResourceType:       string(rt),
		ResourceID:         id,
	})
}

// CountByType returns per-resource_type counts for src, both total and
// visible. Used by the install manifest endpoint (PR 3) to answer
// "how many resources did the lab import?".
type LockCounts struct {
	Type    ResourceType
	Total   int
	Visible int
}

func CountByType(ctx context.Context, q LockQuerier, src Source) ([]LockCounts, error) {
	if !src.Valid() {
		return nil, ErrUnknownSource
	}
	rows, err := q.CountExperimentalResourceLocksByType(ctx, string(src))
	if err != nil {
		return nil, err
	}
	out := make([]LockCounts, 0, len(rows))
	for _, r := range rows {
		out = append(out, LockCounts{
			Type:    ResourceType(r.ResourceType),
			Total:   int(r.Total),
			Visible: int(r.Visible),
		})
	}
	return out, nil
}
