// Package handler — boot_provision_product_labs.go (0.5.5)
//
// Boot-time product-level resource provisioning.
//
// 0.5.5 promotes `agent_creation_studio` and `agent_self_optimization`
// from opt-in Labs to product-level resources. The user no longer
// enables a flag, no longer picks them from a "实验插件" sub-menu, and
// no longer manually runs `multica experimental install ...`. The
// leader agents (`agent_creation_expert` + `智能体优化专家`) and
// the self-opt autopilots are **always available**:
//
//   - Self-opt's `智能体优化专家` agent + 2 autopilots are already
//     boot-wired via the agent_self_optimization service (0.3.45.1,
//     `cmd/server/router.go:679`). That block scans every workspace
//     for zombie runs and starts the per-workspace scheduler ticker;
//     service.Start() no-ops when the lab flag is OFF, so the
//     cron-driven optimization is gated by the flag gate but the
//     Service struct itself is always live. **No additional wiring
//     needed for self-opt here.**
//
//   - `agent_creation_studio` had no Service — its leader agent
//     (`agent_creation_expert`) was only ever upserted by the
//     `install_agent_creation_studio` HTTP handler (now legacy).
//     With the catalog's DefaultVal flipped to true, the 0.3.46 P0#4
//     leader-rewrite path will look for that agent on every issue
//     creation/update and 404 silently if it's missing. To keep the
//     product contract ("pick 智能体创建 in AssigneePicker → it just
//     works"), we provision the leader here at boot, mirroring the
//     mythos / self-opt resume block in router.go:649-700.
//
// We do NOT claim the resource under `experimental.Claim` (no
// `experimental_resource_lock` row) and we do NOT write any
// `experimental_resource_visibility` rows. The studio is a
// product-level agent now, not a lab asset, so it shows up in the
// regular agent list unconditionally and is never hidden behind a
// flag.
//
// Errors are logged at warn level and never block startup. The
// non-fatal contract is identical to mythos / self-opt resume: a
// transient DB failure on boot must not bring the server down. A
// user-triggered path (the assignDefaultLabAgentOnUpdate leader
// rewrite) will still try to upsert the agent at first dispatch as
// a final safety net, so a missed boot provision self-heals on the
// next issue created in that workspace.

package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// resolveOrSynthesizeProductRuntime is the product-lab equivalent of
// install_claude_science.go's `upsertClaudeScienceRuntime`:
//
//   1. If a local online runtime already exists (the daemon is
//      running), return its id directly.
//   2. Otherwise, upsert a synthetic offline stub runtime with a
//      stable daemon_id ("agent-creation-studio") so the studio
//      leader agent row can satisfy the `agent.runtime_id` NOT NULL
//      FK on cold boot. Once the user later starts the local daemon,
//      `rebindLabAgentsToOnlineRuntime` (which already lists
//      `agent_creation_expert` in `labLeaderAgentNames`) re-points
//      the agent's runtime_id at the live daemon.
//
// Returning (pgtype.UUID{}, nil) is treated as a hard fail by the
// caller (no online runtime AND the synthetic upsert returned no row);
// a non-nil error indicates the call itself errored.
func resolveOrSynthesizeProductRuntime(
	ctx context.Context, h *Handler, workspaceID pgtype.UUID,
) (pgtype.UUID, error) {
	if online := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID); online.Valid {
		return online, nil
	}
	row, err := h.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: "agent-creation-studio", Valid: true},
		Name:        "Agent Creation Studio Runtime",
		RuntimeMode: "local",
		Provider:    "agent_creation_studio",
		Status:      "offline",
		DeviceInfo:  "synthetic product runtime — no live daemon yet",
		Metadata:    []byte(`{"synthetic":true,"source":"product.agent_creation_studio"}`),
		OwnerID:     pgtype.UUID{},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("upsert synthetic runtime: %w", err)
	}
	if !row.ID.Valid {
		return pgtype.UUID{}, errors.New("synthetic runtime upsert returned invalid id")
	}
	slog.Info("resolveOrSynthesizeProductRuntime: provisioned synthetic offline stub",
		"workspace_id", util.UUIDToString(workspaceID),
		"runtime_id", util.UUIDToString(row.ID))
	return row.ID, nil
}

// BootProvisionProductLabs walks every workspace and upserts the
// `agent_creation_studio` leader agent (`agent_creation_expert`).
//
// 30 s budget covers a few thousand workspaces on a real DB. Failures
// on individual workspaces are logged and skipped — one broken
// workspace must not abort the others. A nil pool is treated as
// "tests that only exercise routing"; we skip the entire walk so the
// test fixtures that pass `queries.New(nil)` (and thus a non-nil
// Queries struct with a nil inner DBTX) do not panic.
func (h *Handler) BootProvisionProductLabs(ctx context.Context) {
	if h == nil || h.Queries == nil {
		return
	}
	bootCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	ids, err := h.Queries.ListAllWorkspaceIDs(bootCtx)
	if err != nil {
		slog.Warn("boot provision product labs: list workspace ids failed",
			"err", err)
		return
	}
	var provisioned, skipped int
	for _, id := range ids {
		if _, err := upsertAgentCreationExpert(bootCtx, h, id); err != nil {
			slog.Warn("boot provision product labs: upsert agent failed",
				"workspace_id", util.UUIDToString(id),
				"agent", AgentCreationExpertName,
				"err", err)
			skipped++
			continue
		}
		// 0.5.22: swarm_topology leader is a sibling product-level
		// resource. Provision failure on it does NOT mark the
		// workspace as skipped — the studio leader already succeeded.
		// Logged separately so the swarm_topology path is observable
		// in production logs.
		if _, err := upsertSwarmCoordinator(bootCtx, h, id); err != nil {
			slog.Warn("boot provision product labs: upsert swarm coordinator failed",
				"workspace_id", util.UUIDToString(id),
				"agent", SwarmCoordinatorName,
				"err", err)
		}
		provisioned++
	}
	slog.Info("boot provision product labs complete",
		"agent", AgentCreationExpertName,
		"workspaces_provisioned", provisioned,
		"workspaces_skipped", skipped,
		"total_workspaces", len(ids))
}

// EnsureProductAgentForWorkspace is the lazy fallback used by the
// 0.3.46 P0#4 leader-rewrite path when a workspace was skipped
// during boot (e.g. cold start, transient DB failure). The caller
// passes the leader's expected display name; if the row already
// exists the call is a no-op, otherwise it is created with the
// online-runtime binding (matching boot_provision_product_labs.go).
//
// This is the same shape as `upsertAgentCreationExpert`; it is
// exposed at the package level (not just lowercase) so that future
// product-level agents can share the lazy-provision contract.
func (h *Handler) EnsureProductAgentForWorkspace(
	ctx context.Context, workspaceID pgtype.UUID, agentName string,
) (pgtype.UUID, error) {
	if h == nil || h.Queries == nil {
		return pgtype.UUID{}, fmt.Errorf("ensureProductAgentForWorkspace: handler not initialized")
	}
	if !workspaceID.Valid {
		return pgtype.UUID{}, fmt.Errorf("ensureProductAgentForWorkspace: invalid workspace id")
	}
	switch agentName {
	case AgentCreationExpertName:
		return upsertAgentCreationExpert(ctx, h, workspaceID)
	case SwarmCoordinatorName:
		// 0.5.22: swarm_topology leader. Lazy fallback for the
		// 0.3.46 P0#4 leader-rewrite path when the workspace was
		// skipped during boot. Mirrors the studio helper shape.
		return upsertSwarmCoordinator(ctx, h, workspaceID)
	default:
		return pgtype.UUID{}, fmt.Errorf("ensureProductAgentForWorkspace: unknown product agent %q", agentName)
	}
}
