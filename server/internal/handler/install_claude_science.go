// install_claude_science.go — 0.3.15 install handler for the claude_science
// Labs lab.
//
// Lifecycle:
//
//   1. Resolve the caller's active workspace (0.3.25: the lab no
//      longer creates a dedicated "claude-science" reserved-slug
//      workspace — resources land in the user's own workspace and are
//      isolated by the experimental_resource_lock rows below).
//
//   2. Read the manifest emitted by
//      apps/desktop/scripts/build-claude-science-manifest.mjs at bundle
//      time. The manifest is on disk under
//      apps/desktop/resources/claude-science/manifest.json, resolved
//      for both dev (`$REPO/apps/desktop/resources/...`) and packaged
//      modes via the MULTICA_RESOURCES_DIR env var (set by the desktop
//      main process at spawn time).
//
//   3. For every skill / agent / squad / member row declared in the
//      manifest, write the row to its domain table. Idempotent —
//      existing rows are skipped using (workspace_id, name) uniqueness
//      from migrations 008 (skill), 046 (agent), 084 (squad).
//
//   4. After every row lands, attach a lock row via experimental.Claim
//      so the renderer / list filters treat the row as lab-owned.
//
// The handler is called from PostExperimentalResourcesInstall (PR 3)
// and from UpdateExperimentalFlag (PR 4) — both feed src + already-
// restored visibility, so all Claim calls below go in straight through.
//
// Why not chunk / parallelize: install is a one-shot bootstrap
// triggered by user click. A few seconds of latency is acceptable; not
// worth the extra surface for parallel-write retry semantics.

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ManifestResourceDirEnv is the env var the desktop main process sets
// when it spawns the bundled server. It points at the unpacked
// resources/ directory inside the packaged .app (or the dev repo's
// apps/desktop/resources/ in non-bundled dev). Keeping the resolution
// here rather than in the desktop binary lets the install path run
// identically in `go test` (env-set to a fixture dir) and in production.
const ManifestResourceDirEnv = "MULTICA_RESOURCES_DIR"

// DefaultManifestPath is the manifest's well-known location relative
// to the resource dir. bundle-cli.mjs stages both the manifest and the
// supporting skill / agent asset trees under this prefix.
const DefaultManifestPath = "claude-science/manifest.json"

// claudeScienceManifest mirrors the JSON shape emitted by
// build-claude-science-manifest.mjs. Field names mirror the manifest
// directly so a future OpenScience version can grow without breaking
// this loader.
type claudeScienceManifest struct {
	SchemaVersion      int               `json:"schema_version"`
	ExperimentalSource string            `json:"experimental_source"`
	Workspace          manifestWorkspace `json:"workspace"`
	Skills             []manifestSkill   `json:"skills"`
	Agents             []manifestAgent   `json:"agents"`
	Squads             []manifestSquad   `json:"squads"`
	InstalledAtBuild   bool              `json:"installed_at_build"`
	BuiltFrom          string            `json:"built_from"`
	Counts             map[string]int    `json:"counts"`
}

type manifestWorkspace struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
}

type manifestSkill struct {
	Category string `json:"category"`
	Name     string `json:"name"`
	BodyPath string `json:"body_path"`
}

type manifestAgent struct {
	Name       string `json:"name"`
	PromptPath string `json:"prompt_path"`
	Category   string `json:"category"`
}

type manifestSquad struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Members     []string `json:"members"`
	LeaderAgent string   `json:"leader_agent"`
}

// InstallClaudeScience is the PR 6 entry point. The Handler keeps it
// exposed (capital I) so the tests in this package can call it
// directly without bouncing through HTTP.
//
// The manifest loader is the single point of failure for a fresh
// install: if the manifest is not staged under the resource dir, the
// install returns ErrManifestUnavailable so the HTTP layer can
// surface a 503 with a clear hint to run the build script.
//
// The userID parameter is the authenticated caller — we add it as
// the workspace's owner member so downstream NOT NULL constraints
// (squad.creator_id, member.user_id) can resolve. Passing the empty
// UUID is allowed only when the workspace already exists with at
// least one member from a prior install; otherwise the squad step
// fails fast with a clear "workspace has no members" error.
//
// 0.3.25 reserved-workspace removal: the lab no longer creates a
// dedicated "claude-science" reserved-slug workspace. Resources land
// on the caller's currently active workspace (passed as workspaceID)
// and are isolated purely by the experimental_resource_lock rows
// (experimental_source column) + the visibility table — the same
// mechanism every other lab already relies on. This honors the
// CLAUDE.md hard constraint ("no reserved workspace for new labs")
// and matches the upstream OpenScience model, which has no workspace
// / tenant concept at all. When workspaceID is empty (unauthenticated
// fixture callers) we fall back to the caller's first workspace.
func (h *Handler) InstallClaudeScience(ctx context.Context, src experimental.Source, userID, workspaceID string) error {
	if h == nil || h.Queries == nil {
		return errors.New("installClaudeScience: handler not initialized")
	}
	if src != experimental.SourceClaudeScienceLab && src != experimental.SourceClaudeScience {
		return fmt.Errorf("installClaudeScience: source %q not supported", src)
	}

	manifest, err := loadClaudeScienceManifest()
	if err != nil {
		return fmt.Errorf("manifest load: %w", err)
	}
	if !manifest.InstalledAtBuild {
		return ErrManifestUnavailable
	}

	// 1. Resolve the target workspace: the caller's active workspace.
	// No reserved workspace is created; the lab writes into the
	// user's own workspace and is scoped by the lock + visibility
	// tables. We do NOT Claim the workspace itself — locking a user's
	// own workspace as lab-owned would let a rollback hide it.
	workspaceUUID, err := resolveLabWorkspace(ctx, h, workspaceID, userID)
	if err != nil {
		return fmt.Errorf("resolve workspace: %w", err)
	}

	// 1a. Workspace owner. Migration 084 makes squad.creator_id NOT
	// NULL, and the cleanest way to satisfy it without fabricating
	// users is to add the calling user as the workspace's owner
	// (idempotent — re-install reuses the existing row). When the
	// install runs unauthenticated (test fixtures), we fall back to
	// the workspace's first existing member so the squad step still
	// resolves.
	if userID != "" {
		if err := ensureWorkspaceOwner(ctx, h, workspaceUUID, userID); err != nil {
			return fmt.Errorf("ensure workspace owner: %w", err)
		}
	}

	// 1b. Lab runtime. Migration 004 made agent.runtime_id NOT NULL
	// with FK to agent_runtime. We provision one synthetic runtime
	// per lab install keyed by daemon_id='claude-science'. This
	// lets the runtime system track lab-owned agents under one
	// logical runtime without an actual daemon registering.
	runtimeID, err := upsertClaudeScienceRuntime(ctx, h, workspaceUUID)
	if err != nil {
		return fmt.Errorf("lab runtime: %w", err)
	}

	// 2. Skills. Existing rows with the same (workspace_id, name) are
	// skipped; new ones are inserted. The Claim attaches a lock row
	// for each inserted skill so the renderer can filter.
	//
	// 0.3.33: every lab-owned resource stays hidden=true from the
	// start so it never surfaces in the main agent / skill picker.
	// The lab's own runtime (Claude Lab tabs, Mythos supervisor,
	// Pythia report) reads through the dedicated IPC / WS path and
	// doesn't depend on the catalog list endpoints. The visible
	// override for flag-on is opt-in via `?include_all=true`,
	// exercised by the install handler itself when seeding the
	// internal roster. Hidden is sticky — there's no catalog
	// "show lab rows in picker" mode.
	for _, sk := range manifest.Skills {
		skillID, err := upsertClaudeScienceSkill(ctx, h, workspaceUUID, sk, manifest.Workspace.Slug)
		if err != nil {
			return fmt.Errorf("skill %s: %w", sk.Name, err)
		}
		if err := experimental.Claim(ctx, h.Queries, src, experimental.LockSkill, skillID); err != nil {
			return fmt.Errorf("skill lock %s: %w", sk.Name, err)
		}
	}

	// 3. Agents. Same shape as skills.
	agentsByName := make(map[string]pgtype.UUID, len(manifest.Agents))
	for _, ag := range manifest.Agents {
		agentID, err := upsertClaudeScienceAgent(ctx, h, workspaceUUID, runtimeID, ag, manifest.Workspace.Slug)
		if err != nil {
			return fmt.Errorf("agent %s: %w", ag.Name, err)
		}
		agentsByName[ag.Name] = agentID
		if err := experimental.Claim(ctx, h.Queries, src, experimental.LockAgent, agentID); err != nil {
			return fmt.Errorf("agent lock %s: %w", ag.Name, err)
		}
	}

	// 4. Squads. Each squad declares its members by agent name; we
	// resolve names to ids and write squad_member rows. The squad row
	// itself is the entity our lock covers.
	for _, sq := range manifest.Squads {
		squadID, err := upsertClaudeScienceSquad(ctx, h, workspaceUUID, sq, agentsByName)
		if err != nil {
			return fmt.Errorf("squad %s: %w", sq.Name, err)
		}
		if err := experimental.Claim(ctx, h.Queries, src, experimental.LockSquad, squadID); err != nil {
			return fmt.Errorf("squad lock %s: %w", sq.Name, err)
		}
		if err := attachSquadMembers(ctx, h, squadID, sq.Members, agentsByName, src); err != nil {
			return fmt.Errorf("squad members %s: %w", sq.Name, err)
		}
	}

	// 0.3.53: seed experimental_resource_visibility rows for every
	// Claude Science agent + squad the install just created. The mythos
	// install path has had this since 0.3.31; claude_science did not,
	// which meant `filterLabsHiddenByDefault(flagKey, HideAgent)` was
	// silently a no-op for claude_science_lab when the flag was OFF
	// (no visibility rows → empty hidden set → all rows passed
	// through). Today the user-facing picker filters via the lock
	// table's `hidden=true` rows (ListVisibleAgentsByWorkspace), but
	// the visibility table is the canonical source-of-truth for any
	// future flag-gated UI surface. Without this loop a future
	// migration that adds a flag-driven filter would silently leave
	// Claude Science agents visible — exactly the failure mode this
	// PR fixes.
	//
	// Idempotent: InsertExperimentalResourceVisibility uses ON CONFLICT
	// DO NOTHING, so re-running the install is a no-op.
	if err := upsertClaudeScienceVisibility(ctx, h, workspaceUUID, agentsByName, manifest); err != nil {
		return fmt.Errorf("claude_science visibility: %w", err)
	}

	// 0.3.33: every lab-installed row is now claimed — flip them
	// hidden=TRUE so they never surface in the main user-facing
	// pickers (agent / autopilot / squad / skill lists). The lab
	// surfaces its own roster through /experimental/<suffix> and
	// via dedicated IPC channels, NOT through the shared
	// catalog list endpoints. Rollback handler will call Hide()
	// again — idempotent.
	if _, err := experimental.Hide(ctx, h.Queries, src); err != nil {
		return fmt.Errorf("hide lab resources: %w", err)
	}

	// 0.3.44: the blanket Hide() above also hides this source's
	// workspace lifecycle marker, which breaks install/rollback status
	// semantics — with every lock row hidden, isManifestHidden() can no
	// longer tell "installed" apart from "rolled back". Restore a single
	// visible marker row (workspace-typed, keyed off LifecycleMarker so
	// it never matches a real workspace/agent/skill and cannot leak into
	// the pickers) to carry the "installed & visible" signal. Claim first
	// so the marker exists on a fresh install; RestoreOne then un-hides
	// it on re-install (Claim is a no-op once the row already exists).
	// Rollback's Hide() re-hides the marker along with everything else,
	// so Hidden flips back to true. Both calls are idempotent.
	marker := experimental.LifecycleMarker(string(src))
	if err := experimental.Claim(ctx, h.Queries, src, experimental.LockWorkspace, marker); err != nil {
		return fmt.Errorf("claim lifecycle marker: %w", err)
	}
	if _, err := experimental.RestoreOne(ctx, h.Queries, src, experimental.LockWorkspace, marker); err != nil {
		return fmt.Errorf("restore lifecycle marker: %w", err)
	}

	// 0.3.35: heal agent.runtime_id for agents installed by earlier
	// versions that pointed at the synthetic offline stub. The
	// upsert* helpers above short-circuit on existing rows, so this
	// is the only place that repairs the FK without an uninstall +
	// reinstall round-trip.
	rebindLabAgentsToOnlineRuntime(ctx, h, workspaceUUID)

	return nil
}

// ErrManifestUnavailable surfaces a clean 503 in the HTTP layer when
// the manifest has not been staged by bundle-cli yet. We deliberately
// distinguish it from generic errors so the renderer can show
// "manifest not bundled — run apps/desktop/scripts/build-claude-science-manifest.mjs"
// without parsing Go error strings.
var ErrManifestUnavailable = errors.New("claude_science manifest not staged under resources/")

// resolveLabWorkspace resolves the workspace a lab install writes
// into. Preference order:
//
//  1. The caller's active workspace (workspaceID, from the request's
//     X-Workspace-ID header via resolveWorkspaceID). This is the
//     normal path — resources land where the user is working.
//  2. When workspaceID is empty (unauthenticated fixture callers) OR
//     is not a valid UUID, fall back to the caller's first workspace
//     (ListWorkspaces returns the workspaces a user belongs to).
//
// Returns an error only when neither path yields a workspace — an
// install genuinely cannot proceed without one. No workspace is
// created here: the lab is isolated by lock + visibility rows, not by
// a dedicated reserved workspace (0.3.25 reserved-workspace removal).
func resolveLabWorkspace(
	ctx context.Context, h *Handler, workspaceID, userID string,
) (pgtype.UUID, error) {
	if workspaceID != "" {
		if id := parseUUID(workspaceID); id.Valid {
			return id, nil
		}
	}
	uid := parseUUID(userID)
	if uid.Valid {
		wss, err := h.Queries.ListWorkspaces(ctx, uid)
		if err == nil && len(wss) > 0 {
			return wss[0].ID, nil
		}
	}
	return pgtype.UUID{}, errors.New(
		"no active workspace to install into (pass X-Workspace-ID or ensure the user has a workspace)")
}

// upsertClaudeScienceSkill writes one skill row per manifest entry.
// The (workspace_id, name) uniqueness on the skill table (migration
// 008) makes the call idempotent — re-install on a populated lab is a
// no-op for skills that already exist.
//
// We hydrate the SKILL.md body from disk so the agent runtime has the
// full prompt content available. The body is short (< 50 KB typically)
// so reading it synchronously inside the install HTTP handler is
// acceptable; PR 11 (deferred) might push this to a background job if
// the body sizes ever grow.
func upsertClaudeScienceSkill(
	ctx context.Context, h *Handler,
	workspaceID pgtype.UUID, sk manifestSkill, slug string,
) (pgtype.UUID, error) {
	id, err := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        sk.Name,
	})
	if err == nil {
		return id.ID, nil
	}
	body := loadManifestAsset(slug, sk.BodyPath)
	created, cerr := h.Queries.CreateSkill(ctx, db.CreateSkillParams{
		WorkspaceID: workspaceID,
		Name:        sk.Name,
		Description: fmt.Sprintf("Imported from Claude Science manifest (%s)", sk.Category),
		Content:     body,
		Config:      []byte(`{}`),
		CreatedBy:   pgtype.UUID{},
	})
	if cerr != nil {
		// Race / already-exists path — re-read.
		if re, rerr := h.Queries.GetSkillByWorkspaceAndName(ctx, db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        sk.Name,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, fmt.Errorf("create: %w", cerr)
	}
	return created.ID, nil
}

// upsertClaudeScienceAgent handles the agent row. Migration 046 added
// UNIQUE(workspace_id, name) so the find-or-create pattern is safe.
//
// CreateAgent takes 16 columns. The lab's agents run in 'local' runtime
// mode (no cloud runtime), default visibility, no owner, no MCP
// overlay, no model pin — the agent-runtime sees them as plain local
// agents that happen to be lock-protected.
func upsertClaudeScienceAgent(
	ctx context.Context, h *Handler,
	workspaceID pgtype.UUID, runtimeID pgtype.UUID, ag manifestAgent, slug string,
) (pgtype.UUID, error) {
	id, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        ag.Name,
	})
	if err == nil {
		return id.ID, nil
	}
	prompt := loadManifestAsset(slug, ag.PromptPath)
	instructions := prompt
	if instructions == "" {
		// Fallback so the agent has something to anchor on when the
		// prompt file is missing. The renderer's "试验功能" badge
		// already flags the row as lab-owned so the agent runtime
		// treats this as expected, not user-edit-broken.
		instructions = fmt.Sprintf("%s agent imported from Claude Science manifest", ag.Category)
	}
	created, cerr := h.Queries.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        workspaceID,
		Name:               ag.Name,
		Description:        fmt.Sprintf("%s agent imported from Claude Science manifest", ag.Category),
		AvatarUrl:          pgtype.Text{},
		RuntimeMode:        "local",
		RuntimeConfig:      []byte(`{}`),
		RuntimeID:          runtimeID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		OwnerID:            pgtype.UUID{},
		Instructions:       instructions,
		CustomEnv:          []byte(`{}`),
		CustomArgs:         []byte(`{}`),
		McpConfig:          []byte(`{}`),
		Model:              pgtype.Text{},
		ThinkingLevel:      pgtype.Text{},
	})
	if cerr != nil {
		// Race / already-exists path.
		if re, rerr := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        ag.Name,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, fmt.Errorf("create: %w", cerr)
	}
	return created.ID, nil
}

// upsertClaudeScienceSquad creates the squad row. Migration 084 added
// UNIQUE(workspace_id, name) so the find-or-create pattern is safe.
//
// CreateSquad takes 6 columns: workspace_id, name, description,
// leader_id, creator_id, avatar_url. creator_id is NOT NULL, so we
// resolve it to the workspace's first member (the workspace's owner)
// — the lab install runs without auth context, but every workspace
// must have at least one member by the time we run, since this path
// is gated behind user click (which implies an authenticated user).
func upsertClaudeScienceSquad(
	ctx context.Context, h *Handler,
	workspaceID pgtype.UUID, sq manifestSquad,
	agentsByName map[string]pgtype.UUID,
) (pgtype.UUID, error) {
	id, err := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        sq.Name,
	})
	if err == nil {
		return id.ID, nil
	}
	creatorID, err := firstWorkspaceMember(ctx, h, workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("squad %q creator: %w", sq.Name, err)
	}
	// 0.3.15 fix: when the manifest's leader_agent points at an agent
	// that wasn't ported (e.g. OpenScience source dropped the prompt
	// file), don't fail the entire install. leader_id is NOT NULL
	// with an FK to agent.id (migration 084), so we must pick an
	// existing agent — fall back to the first agent that DID port
	// over. If agents is empty, fall back to the first member (which
	// won't satisfy the FK; the caller should not invoke install with
	// an empty manifest in that case). The squad row still gets
	// created, and the renderer shows the squad with a placeholder
	// leader rather than the whole install blowing up.
	leader, ok := agentsByName[sq.LeaderAgent]
	if !ok {
		for _, id := range agentsByName {
			leader = id
			break
		}
	}
	created, cerr := h.Queries.CreateSquad(ctx, db.CreateSquadParams{
		WorkspaceID: workspaceID,
		Name:        sq.Name,
		Description: sq.Description,
		LeaderID:    leader,
		CreatorID:   creatorID,
		AvatarUrl:   pgtype.Text{},
	})
	if cerr != nil {
		// Race / already-exists path.
		if re, rerr := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        sq.Name,
		}); rerr == nil {
			return re.ID, nil
		}
		return pgtype.UUID{}, fmt.Errorf("create: %w", cerr)
	}
	return created.ID, nil
}

// firstWorkspaceMember returns the user_id of the workspace's oldest
// member. The lab install runs without auth context but every
// install path is triggered by an authenticated user click, so the
// workspace is guaranteed to have at least one member. We pick the
// earliest one (created_at ASC) so the choice is deterministic.
//
// If the workspace is somehow empty we return an error rather than
// fabricating a creator — fabricating would let a no-member
// workspace satisfy the NOT NULL constraint and then break every
// downstream query that joins squad → user.
func firstWorkspaceMember(
	ctx context.Context, h *Handler, workspaceID pgtype.UUID,
) (pgtype.UUID, error) {
	members, err := h.Queries.ListMembers(ctx, workspaceID)
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("list members: %w", err)
	}
	if len(members) == 0 {
		return pgtype.UUID{}, errors.New("workspace has no members — cannot resolve squad creator")
	}
	return members[0].UserID, nil
}

// ensureWorkspaceOwner idempotently adds the calling user as the
// workspace's owner member. Uses GetMemberByUserAndWorkspace to
// short-circuit when the membership already exists, avoiding the
// unique-constraint error path on re-install.
//
// When the caller is the empty UUID (test fixtures without auth),
// we skip the insert and rely on firstWorkspaceMember to fall back
// to any prior member — this keeps the test path deterministic
// while still erroring if the workspace is genuinely empty.
func ensureWorkspaceOwner(
	ctx context.Context, h *Handler,
	workspaceID pgtype.UUID, userID string,
) error {
	uid := parseUUID(userID)
	if !uid.Valid {
		return nil
	}
	existing, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
		WorkspaceID: workspaceID,
		UserID:      uid,
	})
	if err == nil && existing.ID.Valid {
		return nil
	}
	if _, cerr := h.Queries.CreateMember(ctx, db.CreateMemberParams{
		WorkspaceID: workspaceID,
		UserID:      uid,
		Role:        "owner",
	}); cerr != nil {
		// Duplicate membership is fine — race with another install.
		if !strings.Contains(cerr.Error(), "duplicate") &&
			!strings.Contains(cerr.Error(), "unique") {
			return cerr
		}
	}
	return nil
}

// rebindLabAgentsToOnlineRuntime walks the workspace's lab-installed
// leader agents (`research`, `宪法智能体`, `智能体优化专家`) and
// rewrites their runtime_id to the workspace's online local runtime
// when (a) the agent currently points at a synthetic offline stub,
// or (b) the runtime_id is invalid and a daemon is now online.
//
// This is the 0.3.35 post-install healing step: existing lab
// installs from earlier versions ship the synthetic offline
// runtime baked into agent.runtime_id, which silently blocks the
// auto-dispatch path. Reinstalling the lab hits the upsert
// short-circuit (agent row already exists, returns early) and
// would never repair the FK, so we run this explicitly.
//
// Called at the end of every install_*_lab handler. Best-effort:
// errors are logged and swallowed so an offline workspace (no
// daemon) still leaves the agents at their existing runtime_id
// rather than corrupting it.
func rebindLabAgentsToOnlineRuntime(
	ctx context.Context, h *Handler, workspaceID pgtype.UUID,
) {
	runtimeID := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID)
	if !runtimeID.Valid {
		return
	}
	for _, name := range labLeaderAgentNames {
		agent, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
			WorkspaceID: workspaceID,
			Name:        name,
		})
		if err != nil {
			continue
		}
		if agent.RuntimeID == runtimeID {
			continue
		}
		if _, err := h.Queries.UpdateAgent(ctx, db.UpdateAgentParams{
			ID:        agent.ID,
			RuntimeID: runtimeID,
		}); err != nil {
			slog.Warn("rebindLabAgentsToOnlineRuntime: update failed",
				"agent_name", name,
				"workspace_id", util.UUIDToString(workspaceID),
				"error", err)
			continue
		}
		slog.Info("rebindLabAgentsToOnlineRuntime: agent rebound to online runtime",
			"agent_name", name,
			"workspace_id", util.UUIDToString(workspaceID))
	}
}

// labLeaderAgentNames lists every lab-installed agent whose
// runtime_id is auto-rebound to the workspace's online local
// runtime by rebindLabAgentsToOnlineRuntime. Keep this in sync
// with defaultLeaderAgentForLab in service/issue.go.
var labLeaderAgentNames = []string{
	"research",
	"宪法智能体",
	"智能体优化专家",
}

// resolveWorkspaceOnlineRuntime returns the workspace's primary
// online local agent_runtime row, or pgtype.UUID{} when no daemon
// is currently online. Lab installs use this to bind leader agents
// (`research`, `宪法智能体`, `智能体优化专家`) to a real daemon
// instead of a synthetic offline runtime — without it, the
// issue-creation auto-dispatch path silently enqueues into a
// runtime the daemon never polls (CLAUDE.md Known Stability
// Surface #2; see 0.3.35 audit). Returns the FIRST online local
// runtime ordered by created_at; agents share that daemon.
func resolveWorkspaceOnlineRuntime(
	ctx context.Context, h *Handler, workspaceID pgtype.UUID,
) pgtype.UUID {
	runtimes, err := h.Queries.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		slog.Info("resolveWorkspaceOnlineRuntime: list runtimes failed",
			"workspace_id", util.UUIDToString(workspaceID),
			"error", err)
		return pgtype.UUID{}
	}
	for _, rt := range runtimes {
		if rt.Status == "online" && rt.RuntimeMode == "local" {
			return rt.ID
		}
	}
	return pgtype.UUID{}
}

// upsertClaudeScienceRuntime provisions one synthetic agent_runtime
// row per claude_science lab install. The runtime exists to satisfy
// the agent.runtime_id NOT NULL FK (migration 004) without an actual
// daemon registering. We use a stable daemon_id ('claude-science')
// and provider ('claude_science') so re-install / re-toggle hits the
// unique constraint and reuses the row instead of inserting duplicates.
//
// 0.3.35: when the workspace already has an online local daemon,
// reuse its runtime id instead of inserting the synthetic offline
// stub. The synthetic stub was correct for the 0.3.15 install path
// (no daemon lived at runtime_id, so isAgentAssigneeReady had to
// refuse enqueue) but the 0.3.30 auto-dispatch contract requires
// the leader agent to be enqueueable, which means a real daemon
// must be polling the runtime's queue. The fallback path
// (no online daemon) keeps the synthetic offline stub so the
// install still succeeds — agents sit in the table but never
// run until a daemon comes online, which is the right behaviour
// for an offline workspace.
//
// On re-install of an existing lab the agent rows already point
// at the synthetic stub from a previous install; we still upsert
// the stub (idempotent) so existing rows keep their FK, but the
// leader assignment path below will fall through to the existing
// runtime id the agent already has.
func upsertClaudeScienceRuntime(
	ctx context.Context, h *Handler, workspaceID pgtype.UUID,
) (pgtype.UUID, error) {
	if online := resolveWorkspaceOnlineRuntime(ctx, h, workspaceID); online.Valid {
		return online, nil
	}
	row, err := h.Queries.UpsertAgentRuntime(ctx, db.UpsertAgentRuntimeParams{
		WorkspaceID: workspaceID,
		DaemonID:    pgtype.Text{String: "claude-science", Valid: true},
		Name:        "Claude Research Lab Runtime",
		RuntimeMode: "local",
		Provider:    "claude_science_lab",
		Status:      "offline",
		DeviceInfo:  "synthetic lab runtime — no live daemon",
		Metadata:    []byte(`{"synthetic":true,"source":"experimental.claude_science"}`),
		OwnerID:     pgtype.UUID{},
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("upsert runtime: %w", err)
	}
	return row.ID, nil
}

// attachSquadMembers converts the manifest's string-list of agent
// names into squad_member rows. The lookup table agentsByName is
// pre-populated from the install loop so all member ids resolve.
//
// MemberType is 'agent' per migration 084's CHECK constraint; the
// underlying schema is (squad_id, member_type, member_id, role). The
// member lock covers the agent row (since that's the entity the lab
// owns), not the join row — re-installs see the join cleared but the
// underlying agent lock preserved.
func attachSquadMembers(
	ctx context.Context, h *Handler,
	squadID pgtype.UUID, memberNames []string,
	agentsByName map[string]pgtype.UUID, src experimental.Source,
) error {
	for _, name := range memberNames {
		agentID, ok := agentsByName[name]
		if !ok {
			// 0.3.15 fix: skip squad_member references that point at
			// agents we did not port over. Log to stderr for visibility
			// but keep going — the squad row stays (with whatever
			// subset did port), the install completes.
			fmt.Fprintf(os.Stderr,
				"[install_claude_science] squad member %q skipped: not in install set\n",
				name)
			continue
		}
		if _, err := h.Queries.AddSquadMember(ctx, db.AddSquadMemberParams{
			SquadID:    squadID,
			MemberType: "agent",
			MemberID:   agentID,
			Role:       "member",
		}); err != nil {
			// Duplicate is fine (idempotent re-install); any other
			// error bubbles up.
			if !strings.Contains(err.Error(), "duplicate") {
				return err
			}
		}
		if err := experimental.Claim(ctx, h.Queries, src,
			experimental.LockMember, agentID); err != nil {
			return err
		}
	}
	return nil
}

// upsertClaudeScienceVisibility (0.3.53) writes visibility rows so
// every Claude Science agent + squad is hidden from the regular
// pickers when the claude_science_lab flag is OFF. Mirrors
// install_mythos.go::upsertMythosVisibility. The user-facing picker
// filter (`ListVisibleAgentsByWorkspace`) currently reads the
// experimental_resource_lock table's `hidden=true` rows; this loop
// keeps the visibility table in sync so any future flag-gated UI
// surface (filterLabsHiddenByDefault) sees a complete set without
// having to backfill.
//
// Idempotent via the visibility table's UNIQUE(flag_key, resource_type,
// resource_id) constraint + ON CONFLICT DO NOTHING.
func upsertClaudeScienceVisibility(
	ctx context.Context,
	h *Handler,
	workspaceUUID pgtype.UUID,
	agentsByName map[string]pgtype.UUID,
	manifest claudeScienceManifest,
) error {
	if h == nil || h.Queries == nil {
		return errors.New("upsertClaudeScienceVisibility: handler not initialized")
	}
	src := experimental.SourceClaudeScienceLab
	flagKey := string(src)
	// Every agent the install just created gets a visibility row keyed
	// to the lab's experimental_source.
	for _, name := range []string{
		"biology", "physics", "ml", "research", "write",
	} {
		id, ok := agentsByName[name]
		if !ok {
			continue
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      flagKey,
			ResourceType: string(experimental.HideAgent),
			ResourceID:   id,
		}); err != nil {
			return fmt.Errorf("agent %s visibility: %w", name, err)
		}
	}
	// Every squad the install just created gets a visibility row too.
	// Reuse the same (workspace_id, name) lookup the install loop
	// above uses — squads are uniquely identified by that pair.
	for _, sq := range manifest.Squads {
		squadID, err := h.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
			WorkspaceID: workspaceUUID,
			Name:        sq.Name,
		})
		if err != nil {
			return fmt.Errorf("squad %s lookup: %w", sq.Name, err)
		}
		if err := h.Queries.InsertExperimentalResourceVisibility(ctx, db.InsertExperimentalResourceVisibilityParams{
			FlagKey:      flagKey,
			ResourceType: string(experimental.HideSquad),
			ResourceID:   squadID.ID,
		}); err != nil {
			return fmt.Errorf("squad %s visibility: %w", sq.Name, err)
		}
	}
	return nil
}

// loadClaudeScienceManifest decodes the on-disk manifest. The
// resource path comes from MULTICA_RESOURCES_DIR (set by the desktop
// main process when it spawns the bundled server) or, in `go test`
// runs, from the env var the test harness sets.
//
// When the env var is unset we fall back to a stub manifest with
// InstalledAtBuild=false so callers see a clean ErrManifestUnavailable
// rather than a panic. The HTTP layer translates that into a 503.
//
// This function deliberately returns a typed error (not os.IsNotExist)
// so the install path can show "manifest not bundled" vs. "DB error"
// without string parsing.
func loadClaudeScienceManifest() (claudeScienceManifest, error) {
	dir := os.Getenv(ManifestResourceDirEnv)
	if dir == "" {
		return claudeScienceManifest{}, fmt.Errorf("%s not set: %w",
			ManifestResourceDirEnv, ErrManifestUnavailable)
	}
	path := filepath.Join(dir, DefaultManifestPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return claudeScienceManifest{}, ErrManifestUnavailable
		}
		return claudeScienceManifest{}, fmt.Errorf("read %s: %w", path, err)
	}
	var m claudeScienceManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return claudeScienceManifest{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return m, nil
}

// loadManifestAsset reads a small text asset (SKILL.md body, agent
// .txt prompt) from the resource dir. Missing assets return "" —
// the install continues with an empty body, and the runtime flags
// the resource as "asset not bundled" via a separate code path that
// is out of scope for PR 6.
//
// Sized-bound: we cap at 256 KB so a runaway symlink cannot wedge the
// install. Most SKILL.md files are under 30 KB.
func loadManifestAsset(slug string, relPath string) string {
	if relPath == "" {
		return ""
	}
	dir := os.Getenv(ManifestResourceDirEnv)
	if dir == "" {
		return ""
	}
	full := filepath.Join(dir, "claude-science", relPath)
	f, err := os.Open(full)
	if err != nil {
		return ""
	}
	defer f.Close()
	const cap = 256 * 1024
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 4096)
	read := 0
	for {
		n, err := f.Read(tmp)
		if n > 0 {
			read += n
			if read > cap {
				// This chunk crosses the cap. Copy only the bytes up
				// to the cap boundary. `n-(read-cap)` is the count of
				// in-bounds bytes in tmp; read-cap is the overshoot.
				// (The old `tmp[:cap-read]` went negative here and
				// panicked with slice bounds out of range.)
				keep := n - (read - cap)
				if keep > 0 {
					buf = append(buf, tmp[:keep]...)
				}
				break
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			break
		}
	}
	return string(buf)
}
