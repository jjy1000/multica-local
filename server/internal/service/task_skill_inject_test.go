package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// newSkillInjectPool mirrors the pool helper in task_claim_race_test.go.
func newSkillInjectPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// skillInjectFixture holds the IDs created for one test scenario.
type skillInjectFixture struct {
	userID      string
	workspaceID string
	agentID     string
	skillID     string
	pluginID    string
	flagKey     string
}

// createSkillInjectFixture creates a user, workspace, agent, workspace skill,
// and a user_plugin whose manifest declares capabilities.skills = [skillName].
// The plugin's flag is toggled on/off via the `enabled` parameter.
func createSkillInjectFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, skillName string, enabled bool) skillInjectFixture {
	t.Helper()
	suffix := time.Now().UnixNano()
	slug := fmt.Sprintf("skill-inject-%d", suffix)
	email := fmt.Sprintf("skill-inject-%d@multica.ai", suffix)
	flagKey := fmt.Sprintf("user_%s", slug)

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, "Skill Inject Test", email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, 'skill inject test', 'SIT') RETURNING id
	`, "Skill Inject Test", slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, $2, 'local', 'test', 'online', 'test runtime', '{}'::jsonb, now(), 'private', $3)
		RETURNING id
	`, workspaceID, fmt.Sprintf("skill-inject-runtime-%d", suffix), userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	var agentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, status, owner_id)
		VALUES ($1, $2, 'local', $3, 'idle', $4)
		RETURNING id
	`, workspaceID, fmt.Sprintf("skill-inject-agent-%d", suffix), runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	var skillID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content)
		VALUES ($1, $2, 'skill inject test skill', 'test content')
		RETURNING id
	`, workspaceID, skillName).Scan(&skillID); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID)
	})

	manifest, _ := json.Marshal(map[string]any{
		"capabilities": map[string]any{
			"skills": []string{skillName},
		},
	})
	var pluginID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO user_plugin (slug, flag_key, title_en, title_zh, description_en, description_zh, manifest_json, trigger_mode, runtime_kind, status, created_by)
		VALUES ($1, $2, 'Skill Inject Plugin', '技能注入插件', 'test', 'test', $3, 'auto', 'none', 'active', $4)
		RETURNING id
	`, slug, flagKey, manifest, userID).Scan(&pluginID); err != nil {
		t.Fatalf("create user_plugin: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM user_plugin WHERE id = $1`, pluginID)
	})

	if _, err := pool.Exec(ctx, `
		INSERT INTO experimental_pref (user_id, flag_key, enabled)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, flag_key) DO UPDATE SET enabled = $3
	`, userID, flagKey, enabled); err != nil {
		t.Fatalf("set experimental_pref: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE user_id = $1 AND flag_key = $2`, userID, flagKey)
	})

	return skillInjectFixture{
		userID:      userID,
		workspaceID: workspaceID,
		agentID:     agentID,
		skillID:     skillID,
		pluginID:    pluginID,
		flagKey:     flagKey,
	}
}

func TestSkillInject_EnabledPluginInjectsSkill(t *testing.T) {
	ctx := context.Background()
	pool := newSkillInjectPool(t)
	queries := db.New(pool)
	f := createSkillInjectFixture(t, ctx, pool, "injected-skill", true)

	svc := NewTaskService(queries, pool, nil, events.New())
	agentUUID := util.MustParseUUID(f.agentID)
	skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, "")

	found := false
	for _, sk := range skills {
		if sk.Name == "injected-skill" {
			found = true
			if sk.Source != "workspace" {
				t.Errorf("injected skill Source = %q, want %q", sk.Source, "workspace")
			}
			break
		}
	}
	if !found {
		t.Errorf("LoadAgentSkillsForClaim did not inject the enabled plugin skill; got %d skills", len(skills))
	}
}

func TestSkillInject_DisabledPluginDoesNotInject(t *testing.T) {
	ctx := context.Background()
	pool := newSkillInjectPool(t)
	queries := db.New(pool)
	f := createSkillInjectFixture(t, ctx, pool, "disabled-skill", false)

	svc := NewTaskService(queries, pool, nil, events.New())
	agentUUID := util.MustParseUUID(f.agentID)
	skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, "")

	for _, sk := range skills {
		if sk.Name == "disabled-skill" {
			t.Errorf("LoadAgentSkillsForClaim injected a disabled plugin skill")
		}
	}
}

func TestSkillInject_MissingSkillSkippedSilently(t *testing.T) {
	ctx := context.Background()
	pool := newSkillInjectPool(t)
	queries := db.New(pool)
	// The fixture declares capabilities.skills = ["ghost-skill"] but we
	// create the workspace skill under a different name, so the lookup
	// returns no row and the injection must skip silently.
	f := createSkillInjectFixture(t, ctx, pool, "ghost-skill", true)
	// Overwrite the skill name so it no longer matches the manifest.
	pool.Exec(ctx, `UPDATE skill SET name = 'renamed-skill' WHERE id = $1`, f.skillID)

	svc := NewTaskService(queries, pool, nil, events.New())
	agentUUID := util.MustParseUUID(f.agentID)
	// Must not panic or error — best-effort degradation.
	skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, "")
	for _, sk := range skills {
		if sk.Name == "ghost-skill" {
			t.Errorf("LoadAgentSkillsForClaim injected a skill that doesn't exist in the workspace")
		}
	}
}

func TestSkillInject_DeduplicatesAgainstExistingAgentSkills(t *testing.T) {
	ctx := context.Background()
	pool := newSkillInjectPool(t)
	queries := db.New(pool)
	f := createSkillInjectFixture(t, ctx, pool, "dedup-skill", true)

	// Bind the skill to the agent directly (agent_skill row), so the
	// plugin injection must NOT add a second copy.
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_skill (agent_id, skill_id) VALUES ($1, $2)
	`, f.agentID, f.skillID); err != nil {
		t.Fatalf("create agent_skill: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM agent_skill WHERE agent_id = $1 AND skill_id = $2`, f.agentID, f.skillID)
	})

	svc := NewTaskService(queries, pool, nil, events.New())
	agentUUID := util.MustParseUUID(f.agentID)
	skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, "")

	count := 0
	for _, sk := range skills {
		if sk.Name == "dedup-skill" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("LoadAgentSkillsForClaim returned %d copies of dedup-skill, want 1", count)
	}
}

// TestSkillInject_LabScopedPluginScopesToOwnIssues pins the 0.5.89
// skills_visibility contract: a plugin whose manifest sets
// capabilities.skills_visibility="lab_scoped" injects its skills ONLY when
// the claimed issue's lab_source matches the plugin's flag key. Issue-less
// claims (labSource "") and unrelated labs get nothing — the lab's skills
// ride the lab's own runs, not the whole workspace.
func TestSkillInject_LabScopedPluginScopesToOwnIssues(t *testing.T) {
	ctx := context.Background()
	pool := newSkillInjectPool(t)
	queries := db.New(pool)
	suffix := time.Now().UnixNano()
	slug := fmt.Sprintf("lab-scoped-inject-%d", suffix)
	email := fmt.Sprintf("lab-scoped-inject-%d@multica.ai", suffix)
	flagKey := fmt.Sprintf("user_%s", slug)
	skillName := "lab-scoped-skill"

	var userID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id`,
		"Lab Scoped Test", email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })

	var workspaceID string
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug, description, issue_prefix) VALUES ($1, $2, 'lab scoped test', 'LSI') RETURNING id`,
		"Lab Scoped Test", slug).Scan(&workspaceID); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID) })

	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatalf("create member: %v", err)
	}

	var runtimeID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at, visibility, owner_id)
		VALUES ($1, $2, 'local', 'test', 'online', 'test runtime', '{}'::jsonb, now(), 'private', $3)
		RETURNING id`, workspaceID, "lab-scoped-runtime-"+slug, userID).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID) })

	var agentID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, status, owner_id)
		VALUES ($1, $2, 'local', $3, 'idle', $4) RETURNING id`,
		workspaceID, "lab-scoped-agent-"+slug, runtimeID, userID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

	var skillID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO skill (workspace_id, name, description, content)
		VALUES ($1, $2, 'lab scoped skill', 'content') RETURNING id`,
		workspaceID, skillName).Scan(&skillID); err != nil {
		t.Fatalf("create skill: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, skillID) })

	manifest, _ := json.Marshal(map[string]any{
		"capabilities": map[string]any{
			"skills":            []string{skillName},
			"skills_visibility": "lab_scoped",
		},
	})
	var pluginID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO user_plugin (slug, flag_key, title_en, manifest_json, trigger_mode, runtime_kind, status, created_by)
		VALUES ($1, $2, 'Lab Scoped Plugin', $3, 'auto', 'none', 'active', $4) RETURNING id`,
		slug, flagKey, manifest, userID).Scan(&pluginID); err != nil {
		t.Fatalf("create user_plugin: %v", err)
	}
	t.Cleanup(func() { pool.Exec(context.Background(), `DELETE FROM user_plugin WHERE id = $1`, pluginID) })

	if _, err := pool.Exec(ctx, `
		INSERT INTO experimental_pref (user_id, flag_key, enabled) VALUES ($1, $2, true)
		ON CONFLICT (user_id, flag_key) DO UPDATE SET enabled = true`,
		userID, flagKey); err != nil {
		t.Fatalf("set experimental_pref: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM experimental_pref WHERE user_id = $1 AND flag_key = $2`, userID, flagKey)
	})

	svc := NewTaskService(queries, pool, nil, events.New())
	agentUUID := util.MustParseUUID(agentID)

	// Issue-less / unrelated-lab claims get NO injection.
	for _, labSource := range []string{"", "user_some-other-lab", "claude_science_lab"} {
		skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, labSource)
		for _, sk := range skills {
			if sk.Name == skillName {
				t.Errorf("lab_scoped skill injected for labSource=%q", labSource)
			}
		}
	}

	// The lab's own claim gets the skill.
	skills := svc.LoadAgentSkillsForClaim(ctx, agentUUID, flagKey)
	found := false
	for _, sk := range skills {
		if sk.Name == skillName {
			found = true
		}
	}
	if !found {
		t.Errorf("lab_scoped skill NOT injected on the lab's own claim (labSource=%q)", flagKey)
	}
}
