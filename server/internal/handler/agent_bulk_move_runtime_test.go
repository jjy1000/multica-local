package handler

// DB-backed pins for POST /api/agents/bulk-move-runtime (0.5.127) — the
// companion to the default-runtime setting: the setting only seeds NEW
// agents (CreateAgent requires an explicit runtime_id and the form seeds it
// from the default), so the existing fleet stays wherever it was created
// until moved explicitly. Per-agent semantics must match UpdateAgent's
// runtime-switch path: known provider-incompatible models cleared
// (MUL-3341), thinking_level handled. The one deliberate divergence from
// UpdateAgent — a literal-invalid thinking_level is CLEARED instead of
// 400-ing — is pinned here, because a bulk migration that halted at agent
// #37 would be useless as the "switch the fleet over / switch it back"
// escape hatch.
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// createOpencodeProviderRuntime mirrors createCodexProviderRuntime with the
// provider the 0.5.127 default-CLI switch actually targets. Distinct name
// from the thinking-test runtimes so parallel fixtures never collide.
func createOpencodeProviderRuntime(t *testing.T) string {
	t.Helper()
	var runtimeID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at, owner_id
		)
		VALUES ($1, NULL, $2, 'cloud', 'opencode', 'online', $3, '{}'::jsonb, now(), $4)
		RETURNING id
	`, testWorkspaceID, "Opencode Bulk Move Runtime", "Opencode bulk-move test runtime", testUserID).Scan(&runtimeID)
	if err != nil {
		t.Fatalf("create opencode runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

// createBulkMoveAgentWithModelAndThinking — the two existing fixture helpers
// each set only one of (model, thinking_level); the bulk endpoint's contract
// covers both fields on the SAME agent, so seed them together.
func createBulkMoveAgentWithModelAndThinking(t *testing.T, name, runtimeID, model, level string) string {
	t.Helper()
	var modelArg, levelArg any
	if model != "" {
		modelArg = model
	}
	if level != "" {
		levelArg = level
	}
	var agentID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id,
			instructions, custom_env, custom_args, model, thinking_level
		)
		VALUES ($1, $2, '', 'cloud', '{}'::jsonb, $3, 'private', 1, $4, '', '{}'::jsonb, '[]'::jsonb, $5, $6)
		RETURNING id
	`, testWorkspaceID, name, runtimeID, testUserID, modelArg, levelArg).Scan(&agentID)
	if err != nil {
		t.Fatalf("create bulk-move agent %s: %v", name, err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

func agentRuntimeAndFields(t *testing.T, agentID string) (runtimeID, model string, thinking *string) {
	t.Helper()
	err := testPool.QueryRow(context.Background(),
		`SELECT runtime_id::text, COALESCE(model, ''), thinking_level FROM agent WHERE id = $1`, agentID,
	).Scan(&runtimeID, &model, &thinking)
	if err != nil {
		t.Fatalf("load agent %s: %v", agentID, err)
	}
	return runtimeID, model, thinking
}

func postBulkMoveRuntime(t *testing.T, body map[string]any) (BulkMoveAgentRuntimeResponse, int) {
	t.Helper()
	w := httptest.NewRecorder()
	testHandler.BulkMoveAgentRuntime(w, newRequest(http.MethodPost, "/api/agents/bulk-move-runtime", body))
	var resp BulkMoveAgentRuntimeResponse
	_ = json.NewDecoder(w.Body).Decode(&resp)
	return resp, w.Code
}

// createBulkMovePlainMember stands up a non-admin workspace member with
// unique emails (the privateAgentTestFixture owns its own, and both clean
// up by email).
func createBulkMovePlainMember(t *testing.T) string {
	t.Helper()
	var userID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email)
		VALUES ('Bulk Move Member', 'bulk-move-member@multica.test')
		RETURNING id
	`).Scan(&userID)
	if err != nil {
		t.Fatalf("create plain member user: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE email = 'bulk-move-member@multica.test'`)
	})
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, userID); err != nil {
		t.Fatalf("add plain member: %v", err)
	}
	return userID
}

func TestBulkMoveAgentRuntime_MovesFleetAndResetsProviderFields(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	claudeRT := createClaudeProviderRuntime(t)
	codexRT := createCodexProviderRuntime(t)
	opencodeRT := createOpencodeProviderRuntime(t)

	t.Run("moves agents and clears codex-incompatible provider fields", func(t *testing.T) {
		withModel := createBulkMoveAgentWithModelAndThinking(t, "bulk-move-a1", claudeRT, "claude-sonnet-4-6", "max")
		compatible := createBulkMoveAgentWithModelAndThinking(t, "bulk-move-a2", claudeRT, "", "high")

		resp, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   codexRT,
		})
		if code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", code, "body omitted")
		}
		if resp.MovedCount != 2 {
			t.Errorf("moved_count = %d, want 2", resp.MovedCount)
		}
		// `claude-sonnet-4-6` is a known runtime-specific id not accepted by
		// codex (MUL-3341 rule) → cleared. `max` is Claude-only → cleared
		// (the bulk divergence from UpdateAgent's 400).
		if resp.ClearedModelCount != 1 || resp.ClearedThinkingCount != 1 {
			t.Errorf("cleared counts = (model %d, thinking %d), want (1, 1)", resp.ClearedModelCount, resp.ClearedThinkingCount)
		}
		if len(resp.AgentIDs) != 2 {
			t.Errorf("agent_ids = %v, want both agents", resp.AgentIDs)
		}

		rt, model, thinking := agentRuntimeAndFields(t, withModel)
		if rt != codexRT || model != "" || thinking != nil {
			t.Errorf("incompatible agent after move: runtime=%s model=%q thinking=%v, want codex, cleared, NULL", rt, model, thinking)
		}
		rt, model, thinking = agentRuntimeAndFields(t, compatible)
		if rt != codexRT || thinking == nil || *thinking != "high" {
			t.Errorf("compatible agent after move: runtime=%s thinking=%v, want codex + `high` preserved", rt, thinking)
		}

		// Switch back: the reverse move restores the binding. The cleared
		// fields stay cleared — a reset, not a round-trip snapshot.
		back, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": codexRT,
			"to_runtime_id":   claudeRT,
		})
		if code != http.StatusOK || back.MovedCount != 2 {
			t.Fatalf("switch back: expected 200/2, got %d/%d", code, back.MovedCount)
		}
		if rt, _, _ := agentRuntimeAndFields(t, withModel); rt != claudeRT {
			t.Errorf("switch back did not restore claude binding, runtime=%s", rt)
		}
	})

	t.Run("opencode is an unknown model family so models are preserved", func(t *testing.T) {
		// acceptedModelIDsForProvider knows claude + codex only; opencode
		// resolves (nil, false) and ModelKnownIncompatibleWithProvider
		// deliberately preserves manual/unknown model strings. The move
		// must still happen.
		agent := createBulkMoveAgentWithModelAndThinking(t, "bulk-move-a3", claudeRT, "claude-sonnet-4-6", "")

		resp, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   opencodeRT,
		})
		if code != http.StatusOK || resp.MovedCount != 1 || resp.ClearedModelCount != 0 {
			t.Fatalf("expected 200/(1 moved, 0 cleared), got %d/(%d moved, %d cleared)", code, resp.MovedCount, resp.ClearedModelCount)
		}
		if rt, model, _ := agentRuntimeAndFields(t, agent); rt != opencodeRT || model != "claude-sonnet-4-6" {
			t.Errorf("opencode move: runtime=%s model=%q, want opencode + model preserved", rt, model)
		}
	})

	t.Run("archived agents move by default and can be excluded", func(t *testing.T) {
		archived := createBulkMoveAgentWithModelAndThinking(t, "bulk-move-a4", claudeRT, "", "")
		if _, err := testPool.Exec(context.Background(),
			`UPDATE agent SET archived_at = now() WHERE id = $1`, archived); err != nil {
			t.Fatalf("archive fixture agent: %v", err)
		}

		resp, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   codexRT,
		})
		if code != http.StatusOK {
			t.Fatalf("default move: expected 200, got %d", code)
		}
		found := false
		for _, id := range resp.AgentIDs {
			if id == archived {
				found = true
			}
		}
		if !found {
			t.Errorf("archived agent must move by default (switch-back completeness), ids=%v", resp.AgentIDs)
		}

		// Move it back to claude, archive again, exclude it this time.
		_, code = postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": codexRT,
			"to_runtime_id":   claudeRT,
		})
		if code != http.StatusOK {
			t.Fatalf("reverse move: expected 200, got %d", code)
		}
		exclude := false
		resp, code = postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id":  claudeRT,
			"to_runtime_id":    codexRT,
			"include_archived": &exclude,
		})
		if code != http.StatusOK {
			t.Fatalf("excluded move: expected 200, got %d", code)
		}
		for _, id := range resp.AgentIDs {
			if id == archived {
				t.Errorf("include_archived=false must leave archived agents alone, ids=%v", resp.AgentIDs)
			}
		}
	})
}

func TestBulkMoveAgentRuntime_RequestGates(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}

	claudeRT := createClaudeProviderRuntime(t)
	opencodeRT := createOpencodeProviderRuntime(t)

	t.Run("from equals to is rejected", func(t *testing.T) {
		_, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   claudeRT,
		})
		if code != http.StatusBadRequest {
			t.Errorf("from == to must 400, got %d", code)
		}
	})

	t.Run("unknown target runtime is rejected", func(t *testing.T) {
		_, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   "00000000-0000-0000-0000-000000000000",
		})
		if code != http.StatusBadRequest {
			t.Errorf("unknown to_runtime_id must 400, got %d", code)
		}
	})

	t.Run("no agents on source is a zero-count no-op", func(t *testing.T) {
		resp, code := postBulkMoveRuntime(t, map[string]any{
			"from_runtime_id": opencodeRT,
			"to_runtime_id":   claudeRT,
		})
		if code != http.StatusOK || resp.MovedCount != 0 {
			t.Errorf("empty source must 200/0, got %d/%d", code, resp.MovedCount)
		}
	})

	t.Run("plain member is forbidden", func(t *testing.T) {
		memberID := createBulkMovePlainMember(t)
		w := httptest.NewRecorder()
		body := map[string]any{
			"from_runtime_id": claudeRT,
			"to_runtime_id":   opencodeRT,
		}
		testHandler.BulkMoveAgentRuntime(w, newRequestAs(memberID, http.MethodPost, "/api/agents/bulk-move-runtime", body))
		if w.Code != http.StatusForbidden {
			t.Errorf("plain member must 403, got %d: %s", w.Code, w.Body.String())
		}
	})
}
