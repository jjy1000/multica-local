package handler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Two tests pin distinct contracts for the Claude Science lab:
//
//   - TestClaudeScienceLabManifestSkillReferences_AllExist is the
//     "manifest claims a skill we never shipped" guard: every name
//     in capabilities.skills must resolve to a real builtin skill
//     directory under server/internal/service/builtin_skills/.
//
//   - TestInstallClaudeScience_SeedsVisibility exercises the 0.3.53
//     visibility-seed contract: a fresh install must write
//     experimental_resource_visibility rows for every squad it
//     creates so filterLabsHiddenByDefault(flagKey, HideSquad) hides
//     them from the regular squad picker when the claude_science_lab
//     flag is OFF.
//
// 2026-08-11 incident (manifest test): claude_science_lab/manifest.json
// referenced "multica-claude-literature" and "multica-claude-reviewer"
// — neither directory existed under server/internal/service/builtin_skills/.
// The dead references were dropped from the manifest; the test fails
// loudly if any future contributor re-adds a name that does not have
// a matching directory on disk.
//
// The manifest test does NOT touch the database and runs in any
// environment where the manifest + builtin_skills tree are present.
// The visibility test requires a running PostgreSQL and is skipped
// by handler_test.go's TestMain when DATABASE_URL is unreachable.
func TestClaudeScienceLabManifestSkillReferences_AllExist(t *testing.T) {
	// Resolve the lab manifest relative to the package directory
	// (server/internal/handler/ → ../../../ → repo root).
	manifestPath, err := filepath.Abs(filepath.Join(
		"..", "..", "..",
		"apps", "desktop", "resources",
		"experiments", "claude_science_lab", "manifest.json",
	))
	if err != nil {
		t.Fatalf("abs manifest path: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Skipf("lab manifest not present at %s: %v", manifestPath, err)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read lab manifest: %v", err)
	}
	var doc struct {
		Spec struct {
			Capabilities struct {
				Skills []string `json:"skills"`
			} `json:"capabilities"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse lab manifest: %v", err)
	}
	if len(doc.Spec.Capabilities.Skills) == 0 {
		t.Fatal("lab manifest declares no skills — install would have nothing to inject")
	}

	// Resolve the builtin_skills root similarly.
	// server/internal/handler/ → ../../internal/service/builtin_skills
	skillsRoot, err := filepath.Abs(filepath.Join(
		"..", "..", "internal", "service", "builtin_skills",
	))
	if err != nil {
		t.Fatalf("abs builtin_skills path: %v", err)
	}
	for _, name := range doc.Spec.Capabilities.Skills {
		if _, err := os.Stat(filepath.Join(skillsRoot, name)); err != nil {
			t.Errorf("dead skill reference %q: no directory at %s (%v) — "+
				"either port the skill into server/internal/service/builtin_skills/ "+
				"or drop it from the manifest's capabilities.skills array",
				name, filepath.Join(skillsRoot, name), err)
		}
	}
}

// TestInstallClaudeScience_SeedsVisibility is the 0.3.53 visibility
// contract for the Claude Science lab: the install must seed
// experimental_resource_visibility rows for every squad it creates so
// filterLabsHiddenByDefault(flagKey, HideSquad) hides them from the
// regular squad picker when the claude_science_lab flag is OFF. The
// visibility table has UNIQUE(flag_key, resource_type, resource_id)
// + ON CONFLICT DO NOTHING at the SQL layer, so a re-install on a
// populated workspace is a no-op for visibility.
//
// The fixture manifest (testdata/claude-science-fixture) declares one
// squad (fixture-research-squad); the agent leg of
// upsertClaudeScienceVisibility is hardcoded to the production
// roster (biology/physics/ml/research/write), so the fixture install
// only exercises the squad leg. Both legs share the same ON CONFLICT
// DO NOTHING idempotency path, so the squad assertion is
// representative for the agent leg too — what differs is the input
// agent set, not the seeding loop.
//
// Why this test exists: pre-0.3.53 the install path created only
// experimental_resource_lock rows. The lock-driven picker filter
// (ListVisibleAgentsByWorkspace) stayed happy, but
// filterLabsHiddenByDefault — the canonical visibility source — was
// silently empty for claude_science_lab. Any UI surface that
// switched to the visibility-driven filter (or relied on the
// lab_managed DTO stamp, which is keyed on visibility-row existence)
// would re-leak the Claude Science squads. These tests pin the
// 0.3.53 seed loop in place.
//
// The test is NOT t.Parallel() — withTestManifestEnv mutates a
// process-global env var, and the round-trip install test in
// experimental_resources_test.go also relies on the same env. This
// matches TestExperimentalResourcesRoundTrip_InstalledThenHidden's
// concurrency profile.
func TestInstallClaudeScience_SeedsVisibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	userID, workspaceID := installCodeCanvasFresh(t, ctx, "cs-vis")
	cleanupVisibilityRows(t, workspaceID)
	defer withTestManifestEnv(t)()

	const fixtureSquadName = "fixture-research-squad"
	flagKey := string(experimental.SourceClaudeScienceLab)

	if err := testHandler.InstallClaudeScience(ctx, experimental.SourceClaudeScience, userID, workspaceID); err != nil {
		t.Fatalf("InstallClaudeScience: %v", err)
	}

	squadRow, err := testHandler.Queries.GetSquadByWorkspaceAndName(ctx, db.GetSquadByWorkspaceAndNameParams{
		WorkspaceID: uuidToPgtype(workspaceID),
		Name:        fixtureSquadName,
	})
	if err != nil {
		t.Fatalf("fixture squad %q not created: %v", fixtureSquadName, err)
	}

	var visCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'squad'
		  AND resource_id = $2 AND hidden = TRUE
	`, flagKey, pgtypeToString(squadRow.ID)).Scan(&visCount); err != nil {
		t.Fatalf("squad visibility count: %v", err)
	}
	if visCount != 1 {
		t.Fatalf("squad visibility rows = %d, want 1", visCount)
	}

	// Idempotent re-install: still exactly one row for the squad,
	// proving ON CONFLICT DO NOTHING holds for visibility even when
	// every other resource (skills, agents, squad_members) also gets
	// re-walked.
	if err := testHandler.InstallClaudeScience(ctx, experimental.SourceClaudeScience, userID, workspaceID); err != nil {
		t.Fatalf("InstallClaudeScience (re-install): %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM experimental_resource_visibility
		WHERE flag_key = $1 AND resource_type = 'squad'
		  AND resource_id = $2 AND hidden = TRUE
	`, flagKey, pgtypeToString(squadRow.ID)).Scan(&visCount); err != nil {
		t.Fatalf("squad visibility count (re-install): %v", err)
	}
	if visCount != 1 {
		t.Fatalf("squad visibility rows after re-install = %d, want 1 (idempotency broken)", visCount)
	}
}