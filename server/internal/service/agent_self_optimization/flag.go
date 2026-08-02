// Package agent_self_optimization — flag.go (0.3.45.1 + 0.3.45.2 + 0.5.5.1).
//
// 0.5.5.1: this file becomes a **compatibility shim**. The previous design
// gated the entire self-opt loop on a per-user row in
// `experimental_pref` for the `agent_self_optimization` key. The user
// had to enable the flag in the Labs tab to even start; the scheduler
// tick re-checked the gate every minute and silently no-op'd if the
// row was missing or `enabled=false`.
//
// That gate is now **decoupled**:
//   - The catalog `DefaultVal` for `agent_self_optimization` is
//     `true` (catalog.go). Users no longer see a Labs-tab toggle.
//   - The HTTP handlers (`/api/experimental/trust/*`, `/self-opt/edits/*`)
//     no longer call `experimentalFlagEnabled` — they return 200
//     unconditionally.
//   - The Service's scheduler + TriggerManualRun paths call
//     `flagOnForUser` (this file), which is now a stub that always
//     returns `true`. The file is kept so the rest of the service
//     compiles without churning call sites.
//   - The actual user control point is the **autopilot's own
//     `enabled` field**. The 2 self-opt autopilots
//     (SkillOpt-Multica daily + per-3-workday bulk) are normal
//     autopilot rows; users toggle them with
//     `multica autopilot update --disabled` (or the GUI), exactly
//     like every other autopilot in the system.
//
// This is the same product-level pattern 0.5.5 applied to
// `agent_creation_studio` (boot-provision the leader agent, drop
// from the LabPicker, control via the AssigneePicker). The self-opt
// service is a process-level automation, so the autopilot row is the
// natural control surface instead of the assignee picker.

package agent_self_optimization

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// PrefQuerier is kept to preserve the surface that the original
// `flagOnForUser` signature required. The 0.5.5.1 stub no longer
// reads from it, but the type stays so any future call site that
// imports the package does not get a type-resolution error.
type PrefQuerier interface {
	GetExperimentalPrefEnabled(ctx context.Context, arg db.GetExperimentalPrefEnabledParams) (bool, error)
}

// flagOnExperimental was the boot-time "is this build wired up"
// sanity check, reading `experimental.DefaultFor`. The catalog
// `DefaultVal` is now `true` (0.5.5.1), and the value is no longer
// consulted anywhere; the function is kept only so the original
// service.go import compiles without churn.
func flagOnExperimental() bool { return true }

// flagOnForUser was the per-tick gate. In 0.5.5.1 it always returns
// `true`: the per-user `experimental_pref` row for
// `agent_self_optimization` is no longer consulted. The user-facing
// toggle is the autopilot's own `enabled` field (autopilot.runOnly,
// autopilot.Disable, multica autopilot update --disabled).
func flagOnForUser(_ context.Context, _ PrefQuerier, _ pgtype.UUID) bool {
	return true
}
