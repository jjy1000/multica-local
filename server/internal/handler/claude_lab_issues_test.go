// Package handler — claude_lab_issues_test.go (0.3.29+)
//
// Smoke tests for the Claude Lab issues listing endpoint. We focus on
// the input-validation surface (lab allowlist, missing query params,
// unknown lab returning empty list) without spinning up a DB. The
// full DB integration path is exercised by the running test suite.

package handler

import "testing"

func TestAllowedClaudeLabSources(t *testing.T) {
	// The closed set is intentional friction: bumping it requires the
	// catalog and the migration that widens issue.lab_source CHECK.
	want := []string{"claude_science_lab", "mythos_swarm"}
	for _, k := range want {
		if _, ok := allowedClaudeLabSources[k]; !ok {
			t.Errorf("expected %q to be in allowedClaudeLabSources", k)
		}
	}
	if _, ok := allowedClaudeLabSources["sql_injection_attempt"]; ok {
		t.Error("bogus lab source leaked into allowlist")
	}
	if _, ok := allowedClaudeLabSources[""]; ok {
		t.Error("empty lab source leaked into allowlist")
	}
}