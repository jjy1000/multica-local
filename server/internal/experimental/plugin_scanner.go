package experimental

// plugin_scanner.go — dynamic user plugin loading (0.3.60 Labs sandbox).
//
// Converts user_plugin DB rows and filesystem plugin directories into
// Flag structs that the Registry merges alongside the built-in Catalog.
// User plugin flag keys always carry the "user_" prefix to avoid
// collisions with built-in flags.

import (
	"encoding/json"
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

// UserPluginToFlag converts a DB row into a catalog Flag. The
// resulting Flag can be merged into the Registry via MergeUserPlugins.
func UserPluginToFlag(row UserPluginRow) Flag {
	hideFromPicker := row.TriggerMode == "auto"
	return Flag{
		Key:                    row.FlagKey,
		DefaultVal:             false,
		Title:                  LocalizedString{En: row.TitleEn, Zh: row.TitleZh},
		Description:            LocalizedString{En: row.DescriptionEn, Zh: row.DescriptionZh},
		RuntimeKind:            row.RuntimeKind,
		HideFromIssueLabPicker: hideFromPicker,
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
