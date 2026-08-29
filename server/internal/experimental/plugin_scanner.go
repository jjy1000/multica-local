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
