// Package handler — claude_science_skills.go
//
// /api/experimental/claude-science/skills returns the lab-installed skill
// catalogue that the Multica-native skills-browser renders at
// /experimental/claude-science. The browser is the 0.3.16 replacement
// for the retired OpenScience SolidJS renderer (which used to render the
// same 291 skills but in a separate process).
//
// Contract:
//
//   GET /api/experimental/claude-science/skills?q=<substring>&category=<slug>
//   → {"skills":[{"name":"anndata","category":"biology"}], "total": N, "categories":["biology","chemistry",...]}
//
// Auth: any signed-in user; no workspace membership required because
// the lab workspace is reserved-slug and the data is global per source.
//
// Visibility: rows are filtered through ListVisibleSkillSummariesByWorkspace,
// which already excludes hidden rows via experimental_resource_lock.
// Toggling the flag off makes rows disappear on the next refetch.
//
// Note on category: the skill table does NOT carry a category column
// (the OpenScience taxonomy is conceptual). We read categories from
// the bundled manifest.json and join by skill name. Skills present
// in the lab workspace but absent from the manifest get an empty
// category — they were created by the user manually, not by the
// claude_science installer.

package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/util"
)

const claudeScienceReservedSlug = "claude-science"

// ClaudeScienceSkillSummary is one row in the browser response. We
// deliberately do NOT include the SKILL.md content here — the browser
// fetches the full body separately via the existing skill-detail
// surface (handler.skill.go) so this endpoint stays cheap to call
// from a list page that scrolls 291 rows.
type ClaudeScienceSkillSummary struct {
	Name     string `json:"name"`
	Category string `json:"category"`
}

type ClaudeScienceSkillsResponse struct {
	Skills     []ClaudeScienceSkillSummary `json:"skills"`
	Total      int                         `json:"total"`
	Categories []string                    `json:"categories"`
}

// claudeScienceManifestFile mirrors the manifest shape emitted by
// apps/desktop/scripts/build-claude-science-manifest.mjs. We only care
// about the (name -> category) map; everything else is ignored.
type claudeScienceManifestFile struct {
	Skills []struct {
		Category string `json:"category"`
		Name     string `json:"name"`
	} `json:"skills"`
}

// loadClaudeScienceManifestCategories reads the bundled manifest and
// returns a map of skill name -> category. Returns an empty map when
// the manifest isn't available; the caller treats that as "no
// categories, but skills still listable". We pull MULTICA_RESOURCES_DIR
// the same way install_claude_science.go does — same env var, same
// precedence rules.
func loadClaudeScienceManifestCategories(manifestRoot string) map[string]string {
	if manifestRoot == "" {
		return map[string]string{}
	}
	path := filepath.Join(manifestRoot, "claude-science", "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	var m claudeScienceManifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(m.Skills))
	for _, s := range m.Skills {
		if s.Name == "" {
			continue
		}
		out[s.Name] = s.Category
	}
	return out
}

// ClaudeScienceSkills returns the installed-skill catalogue.
//
// Implementation: look up the reserved lab workspace by slug, then ask
// sqlc for ListVisibleSkillSummariesByWorkspace (which honours
// experimental_resource_lock hidden flags). If the lab hasn't been
// installed yet, the workspace row simply doesn't exist and we return
// an empty catalogue — that's the right answer for the UI, which
// renders an empty-state placeholder until install completes.
func (h *Handler) ClaudeScienceSkills(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	categoryFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("category")))

	// Read categories from the bundled manifest (same source the
	// installer wrote into skill rows). MULTICA_RESOURCES_DIR is set
	// by the desktop main process at server spawn — see install
	// handler for the env-var contract.
	categoriesByName := loadClaudeScienceManifestCategories(os.Getenv(ManifestResourceDirEnv))

	ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), claudeScienceReservedSlug)
	if err != nil {
		// sqlc returns sql.ErrNoRows when the slug is unclaimed.
		// Empty catalogue is the right answer.
		writeJSON(w, http.StatusOK, ClaudeScienceSkillsResponse{
			Skills:     []ClaudeScienceSkillSummary{},
			Total:      0,
			Categories: []string{},
		})
		return
	}

	rows, err := h.Queries.ListVisibleSkillSummariesByWorkspace(r.Context(), ws.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list skills")
		return
	}

	out := make([]ClaudeScienceSkillSummary, 0, len(rows))
	categorySet := map[string]struct{}{}
	for _, row := range rows {
		cat := strings.ToLower(categoriesByName[row.Name])
		name := strings.ToLower(row.Name)
		if categoryFilter != "" && cat != categoryFilter {
			continue
		}
		if q != "" && !strings.Contains(name, q) && !strings.Contains(cat, q) {
			continue
		}
		out = append(out, ClaudeScienceSkillSummary{
			Name:     row.Name,
			Category: cat,
		})
		categorySet[cat] = struct{}{}
	}

	categories := make([]string, 0, len(categorySet))
	for c := range categorySet {
		categories = append(categories, c)
	}
	sortStrings(categories)

	writeJSON(w, http.StatusOK, ClaudeScienceSkillsResponse{
		Skills:     out,
		Total:      len(out),
		Categories: categories,
	})
}

// sortStrings is an inlined insertion sort for the category list (N ≤
// ~20). Pulling in the sort package just for one call site isn't
// worth the import noise.
func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j-1] > in[j]; j-- {
			in[j-1], in[j] = in[j], in[j-1]
		}
	}
}

// keep the util import alive even though we don't currently use it
// — left as a forward anchor for the upcoming workspace-id helper
// path that experimental_sources with reserved slugs will share.
var _ = util.UUIDToString