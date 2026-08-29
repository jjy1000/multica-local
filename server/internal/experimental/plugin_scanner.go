package experimental

// plugin_scanner.go — dynamic user plugin loading (0.3.60 Labs sandbox).
//
// Converts user_plugin DB rows and filesystem plugin directories into
// Flag structs that the Registry merges alongside the built-in Catalog.
// User plugin flag keys always carry the "user_" prefix to avoid
// collisions with built-in flags.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// UserPluginPrefix is the mandatory namespace prefix for all
// user-created plugin flag keys. IsKnownKey checks this prefix to
// route lookups to the user plugin layer.
const UserPluginPrefix = "user_"

// IsUserPluginKey reports whether key belongs to the user plugin
// namespace (starts with "user_").
func IsUserPluginKey(key string) bool {
	return strings.HasPrefix(key, UserPluginPrefix)
}

// UserPluginRow mirrors the user_plugin DB table. The handler layer
// converts sqlc-generated structs into this intermediate form so the
// experimental package stays DB-agnostic.
type UserPluginRow struct {
	Slug          string
	FlagKey       string
	TitleEn       string
	TitleZh       string
	DescriptionEn string
	DescriptionZh string
	ManifestJSON  string
	TriggerMode   string // "auto" | "issue_select"
	RuntimeKind   string // "none" | "inline" | "subprocess"
	Status        string // "active" | "disabled" | "deleted"
}

// UserPluginLeader extracts the lab's leader agent name from a plugin
// manifest's capabilities block. The leader is the (typically hidden)
// lab agent auto-assigned to issues bound to this lab, mirroring the
// built-in claude_science_lab→research dispatch. Returns ("", false)
// when the manifest is empty, malformed, or declares no leader.
//
// 0.5.88 P4: superseded by UserPluginLeaderAgent, which prefers the
// top-level leader_agent field of the interaction-model contract and
// falls back to this capabilities.leader block. Kept as the documented
// extractor for the legacy manifest shape.
func UserPluginLeader(manifestJSON []byte) (string, bool) {
	if len(manifestJSON) == 0 {
		return "", false
	}
	var doc struct {
		Capabilities struct {
			Leader string `json:"leader"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &doc); err != nil {
		return "", false
	}
	if doc.Capabilities.Leader == "" {
		return "", false
	}
	return doc.Capabilities.Leader, true
}

// userPluginContractDoc is the subset of a user-plugin manifest that
// carries the 0.5.88 interaction-model contract. The contract lives in
// the manifest JSONB (no migration) so external plugins declare the
// same taxonomy the built-in catalog carries in Flag.InteractionModel:
//
//	{
//	  "interaction_model": "assignee" | "auxiliary",
//	  "leader_agent": "some_agent_name",
//	  "capabilities": { "leader": "legacy_leader_name" }
//	}
//
// capabilities.leader is the pre-0.5.88 leader field; it stays as a
// read-side fallback so legacy plugins keep their 0.3.46 leader-rewrite.
type userPluginContractDoc struct {
	InteractionModel string `json:"interaction_model"`
	LeaderAgent      string `json:"leader_agent"`
	Capabilities     struct {
		Leader string `json:"leader"`
	} `json:"capabilities"`
}

// UserPluginContract is the resolved interaction-model contract of a
// user-plugin manifest (0.5.88 P4). InteractionModel is always a valid
// model literal (never empty); LeaderAgent is the effective leader
// agent name ("" when the manifest declares none).
type UserPluginContract struct {
	InteractionModel string
	LeaderAgent      string
}

// ParseUserPluginContract validates and resolves the interaction-model
// contract from a user-plugin manifest. Rules (0.5.88 P4):
//
//   - interaction_model absent → defaults to "auxiliary"
//     (behavior-preserving: unclassified user plugins never locked, and
//     auxiliary never locks either).
//   - interaction_model must be "assignee" or "auxiliary" — any other
//     value is a validation error (the plugin CRUD handlers surface it
//     as a 400 at create/update time).
//   - interaction_model == "assignee" REQUIRES a non-empty top-level
//     leader_agent (trimmed); assignee-without-leader is rejected so a
//     registered assignee-model plugin always has a lockable leader.
//   - LeaderAgent resolves from the top-level leader_agent field, with
//     the legacy capabilities.leader block as fallback.
//   - A malformed (non-JSON) manifest resolves to the auxiliary default
//     with no error — the storage layer already rejects invalid JSON,
//     so this only covers rows written before validation existed.
func ParseUserPluginContract(manifestJSON []byte) (UserPluginContract, error) {
	var doc userPluginContractDoc
	if len(manifestJSON) > 0 {
		if err := json.Unmarshal(manifestJSON, &doc); err != nil {
			return UserPluginContract{InteractionModel: InteractionModelAuxiliary}, nil
		}
	}
	switch doc.InteractionModel {
	case "":
		doc.InteractionModel = InteractionModelAuxiliary
	case InteractionModelAssignee, InteractionModelAuxiliary:
		// valid literals
	default:
		return UserPluginContract{}, fmt.Errorf("interaction_model must be %q or %q",
			InteractionModelAssignee, InteractionModelAuxiliary)
	}
	leader := strings.TrimSpace(doc.LeaderAgent)
	if doc.InteractionModel == InteractionModelAssignee && leader == "" {
		return UserPluginContract{}, fmt.Errorf(
			"leader_agent is required and must be non-empty when interaction_model is %q",
			InteractionModelAssignee)
	}
	if leader == "" {
		// Legacy fallback (pre-0.5.88): capabilities.leader carried the
		// dispatch agent before the top-level contract field existed.
		leader = strings.TrimSpace(doc.Capabilities.Leader)
	}
	return UserPluginContract{InteractionModel: doc.InteractionModel, LeaderAgent: leader}, nil
}

// UserPluginInteractionModel resolves just the interaction model a
// user-plugin manifest declares: "assignee" or "auxiliary", defaulting
// to "auxiliary" when absent. Validation errors (invalid model literal)
// and malformed manifests also resolve to auxiliary — this is the
// read-side default; write-time validation in the plugin CRUD handlers
// rejects invalid contracts before storage.
func UserPluginInteractionModel(manifestJSON []byte) string {
	c, err := ParseUserPluginContract(manifestJSON)
	if err != nil {
		return InteractionModelAuxiliary
	}
	return c.InteractionModel
}

// UserPluginLeaderAgent resolves the effective leader agent name from a
// user-plugin manifest: the top-level leader_agent contract field
// first, the legacy capabilities.leader block as fallback. Returns
// ("", false) when neither is declared. Callers: the issue-layer leader
// lookups (handler/issue.go + service/issue.go resolveLabLeader) — one
// source of truth for user-plugin leaders, mirroring the built-in
// defaultLabLeaderForKey / defaultLeaderAgentForLab tables.
func UserPluginLeaderAgent(manifestJSON []byte) (string, bool) {
	c, _ := ParseUserPluginContract(manifestJSON)
	if c.LeaderAgent == "" {
		return "", false
	}
	return c.LeaderAgent, true
}

// ---------------------------------------------------------------------------
// 0.5.89 provisioning + skill-visibility contract
// ---------------------------------------------------------------------------
//
// Three additive manifest blocks extend the capabilities contract:
//
//	{
//	  "capabilities": {
//	    "agents_inline":  [ { "name": "...", "description": "...",
//	                          "instructions": "...", "model": "..." } ],
//	    "skills_inline":  [ { "name": "...", "description": "...",
//	                          "content": "..." } ],
//	    "skills_visibility": "global" | "lab_scoped"
//	  }
//	}
//
// agents_inline / skills_inline ask the SERVER to create the resource at
// plugin create/update time (create-or-reuse, hidden via
// experimental_resource_visibility, recorded in the user_plugin_resource
// teardown ledger with origin='provisioned'). The legacy name-only lists
// (capabilities.agents/skills) keep their meaning: "declare a resource
// that already exists" — those ledger as origin='declared' and are never
// reclaimed on plugin delete.
//
// skills_visibility scopes the 0.3.63 global skill injection: "global"
// (default — behavior-preserving for existing plugins) keeps injecting the
// declared skills into EVERY agent's claim while the plugin is enabled;
// "lab_scoped" restricts them to claims whose issue.lab_source matches the
// plugin (the lab's own runs). task.go::LoadAgentSkillsForClaim is the
// consumer.

const (
	// PluginSkillsVisibilityGlobal keeps the pre-0.5.89 contract: skills
	// declared by an enabled plugin load for every agent in the workspace.
	PluginSkillsVisibilityGlobal = "global"
	// PluginSkillsVisibilityLabScoped injects the skills only for claims on
	// issues bound to this plugin (issue.lab_source == flag key).
	PluginSkillsVisibilityLabScoped = "lab_scoped"
)

// UserPluginSkillsVisibility resolves the capabilities.skills_visibility
// literal. Absent → "global" (append-only compat: plugins written before
// 0.5.89 keep their F-008-acknowledged global injection). An invalid
// literal also resolves to "global" on the read side — write-time
// validation in the plugin CRUD handlers rejects it with a 400 first.
func UserPluginSkillsVisibility(manifestJSON []byte) string {
	var doc struct {
		Capabilities struct {
			SkillsVisibility string `json:"skills_visibility"`
		} `json:"capabilities"`
	}
	if len(manifestJSON) > 0 {
		if err := json.Unmarshal(manifestJSON, &doc); err != nil {
			return PluginSkillsVisibilityGlobal
		}
	}
	switch doc.Capabilities.SkillsVisibility {
	case PluginSkillsVisibilityLabScoped:
		return PluginSkillsVisibilityLabScoped
	default:
		return PluginSkillsVisibilityGlobal
	}
}

// UserPluginRawSkillsVisibility returns the RAW capabilities.skills_visibility
// literal ("" when absent) — the write-side input for
// ValidateUserPluginSkillsVisibility, where absent must stay distinct from
// "global" so legacy manifests are never rewritten by validation.
func UserPluginRawSkillsVisibility(manifestJSON []byte) string {
	var doc struct {
		Capabilities struct {
			SkillsVisibility string `json:"skills_visibility"`
		} `json:"capabilities"`
	}
	if len(manifestJSON) > 0 {
		if err := json.Unmarshal(manifestJSON, &doc); err != nil {
			return ""
		}
	}
	return strings.TrimSpace(doc.Capabilities.SkillsVisibility)
}

// ValidateUserPluginSkillsVisibility is the write-side check behind the
// read-side default above: an explicit literal must be one of the two
// known values.
func ValidateUserPluginSkillsVisibility(literal string) error {
	switch literal {
	case "", PluginSkillsVisibilityGlobal, PluginSkillsVisibilityLabScoped:
		return nil
	default:
		return fmt.Errorf("capabilities.skills_visibility must be %q or %q",
			PluginSkillsVisibilityGlobal, PluginSkillsVisibilityLabScoped)
	}
}

// UserPluginAgentSpec is one inline agent definition from
// capabilities.agents_inline.
type UserPluginAgentSpec struct {
	Name         string
	Description  string
	Instructions string
	Model        string
}

// UserPluginSkillSpec is one inline skill definition from
// capabilities.skills_inline. Content is the SKILL.md body; files are not
// supported inline (declare a full workspace skill and reference it by
// name when file bundles are needed).
type UserPluginSkillSpec struct {
	Name        string
	Description string
	Content     string
}

// UserPluginInlineAgents extracts + validates capabilities.agents_inline.
// Names must be non-empty (trimmed, ≤64 chars — the agent.name column is
// TEXT but the pickers render raw names) and unique within the block.
// Malformed manifests yield an error so the CRUD handlers can 400 before
// anything is provisioned.
func UserPluginInlineAgents(manifestJSON []byte) ([]UserPluginAgentSpec, error) {
	if len(manifestJSON) == 0 {
		return nil, nil
	}
	var doc struct {
		Capabilities struct {
			AgentsInline []UserPluginAgentSpec `json:"agents_inline"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &doc); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON")
	}
	seen := make(map[string]struct{}, len(doc.Capabilities.AgentsInline))
	out := make([]UserPluginAgentSpec, 0, len(doc.Capabilities.AgentsInline))
	for _, a := range doc.Capabilities.AgentsInline {
		a.Name = strings.TrimSpace(a.Name)
		if a.Name == "" {
			return nil, fmt.Errorf("capabilities.agents_inline: every entry needs a non-empty name")
		}
		if len(a.Name) > 64 {
			return nil, fmt.Errorf("capabilities.agents_inline: name %q exceeds 64 chars", a.Name)
		}
		if _, dup := seen[a.Name]; dup {
			return nil, fmt.Errorf("capabilities.agents_inline: duplicate agent name %q", a.Name)
		}
		seen[a.Name] = struct{}{}
		out = append(out, a)
	}
	return out, nil
}

// UserPluginInlineSkills extracts + validates capabilities.skills_inline
// with the same non-empty/unique name rules (skill names must match the
// workspace skill UNIQUE(workspace_id, name) constraint).
func UserPluginInlineSkills(manifestJSON []byte) ([]UserPluginSkillSpec, error) {
	if len(manifestJSON) == 0 {
		return nil, nil
	}
	var doc struct {
		Capabilities struct {
			SkillsInline []UserPluginSkillSpec `json:"skills_inline"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(manifestJSON, &doc); err != nil {
		return nil, fmt.Errorf("manifest is not valid JSON")
	}
	seen := make(map[string]struct{}, len(doc.Capabilities.SkillsInline))
	out := make([]UserPluginSkillSpec, 0, len(doc.Capabilities.SkillsInline))
	for _, s := range doc.Capabilities.SkillsInline {
		s.Name = strings.TrimSpace(s.Name)
		if s.Name == "" {
			return nil, fmt.Errorf("capabilities.skills_inline: every entry needs a non-empty name")
		}
		if len(s.Name) > 128 {
			return nil, fmt.Errorf("capabilities.skills_inline: name %q exceeds 128 chars", s.Name)
		}
		if _, dup := seen[s.Name]; dup {
			return nil, fmt.Errorf("capabilities.skills_inline: duplicate skill name %q", s.Name)
		}
		seen[s.Name] = struct{}{}
		out = append(out, s)
	}
	return out, nil
}

// UserPluginToFlag converts a DB row into a catalog Flag. The
// resulting Flag can be merged into the Registry via MergeUserPlugins.
// 0.5.88 P4: the flag carries the manifest's interaction-model contract
// in InteractionModel so the registered flag space, InteractionModelOf,
// and FlagByKey resolve user plugins exactly like built-ins (absent →
// "auxiliary", which never locks — behavior-preserving).
func UserPluginToFlag(row UserPluginRow) Flag {
	hideFromPicker := row.TriggerMode == "auto"
	return Flag{
		Key:                    row.FlagKey,
		DefaultVal:             false,
		Title:                  LocalizedString{En: row.TitleEn, Zh: row.TitleZh},
		Description:            LocalizedString{En: row.DescriptionEn, Zh: row.DescriptionZh},
		RuntimeKind:            row.RuntimeKind,
		HideFromIssueLabPicker: hideFromPicker,
		InteractionModel:       UserPluginInteractionModel([]byte(row.ManifestJSON)),
	}
}

// UserPluginsToFlags converts a slice of DB rows, skipping any whose
// status is not "active".
func UserPluginsToFlags(rows []UserPluginRow) []Flag {
	out := make([]Flag, 0, len(rows))
	for _, row := range rows {
		if row.Status != "active" {
			continue
		}
		out = append(out, UserPluginToFlag(row))
	}
	return out
}

// ScanUserPluginDir scans dir for plugin directories. Each
// subdirectory containing a manifest.json is treated as a plugin.
// Returns Flag structs derived from the manifests. This is the
// filesystem-based plugin discovery path (Phase 1 of the sandbox
// roadmap); the DB path (ListActiveUserPlugins) is the primary
// source once the CRUD API is live.
func ScanUserPluginDir(dir string) []Flag {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil // directory missing or unreadable — soft skip
	}
	var out []Flag
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, e.Name(), "manifest.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			continue // no manifest — not a plugin
		}
		var doc struct {
			Metadata struct {
				Name        string            `json:"name"`
				Title       map[string]string `json:"title"`
				Description map[string]string `json:"description"`
			} `json:"metadata"`
			Spec struct {
				Runtime struct {
					Kind string `json:"kind"`
				} `json:"runtime"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(raw, &doc); err != nil {
			continue
		}
		slug := e.Name()
		flagKey := UserPluginPrefix + slug
		if doc.Metadata.Name != "" {
			slug = doc.Metadata.Name
			flagKey = UserPluginPrefix + slug
		}
		title := LocalizedString{
			En: doc.Metadata.Title["en"],
			Zh: doc.Metadata.Title["zh"],
		}
		desc := LocalizedString{
			En: doc.Metadata.Description["en"],
			Zh: doc.Metadata.Description["zh"],
		}
		rk := doc.Spec.Runtime.Kind
		if rk == "" {
			rk = "inline"
		}
		out = append(out, Flag{
			Key:         flagKey,
			DefaultVal:  false,
			Title:       title,
			Description: desc,
			RuntimeKind: rk,
		})
	}
	return out
}
