// Package handler — claude_science_skills.go
//
// Skill-catalogue + on-demand skill loading endpoints for the
// claude_science_lab Knowledge surface:
//
//	GET  /api/experimental/claude-science/skills?q=&category=&workspace_id=
//	GET  /api/experimental/claude-science/skills/{skillName}?workspace_id=
//	GET  /api/experimental/claude-science/skills/{skillName}/file?path=&workspace_id=
//
// 0.5.106 rework. Three defects fixed:
//
//  1. The list route used to sit OUTSIDE the authenticated group
//     (router.go, public section) — an unauthenticated caller could
//     enumerate the full catalogue. Same H1 class as the 0.5.105
//     semantica-decisions fix. All three routes now live behind
//     RequireExperimentalFlag("claude_science_lab"): flag off → 404,
//     indistinguishable from a nonexistent route.
//
//  2. The list read the claude-science RESERVED-slug workspace, but
//     installs since 0.3.25 land in the caller's active workspace —
//     fresh installs listed zero skills forever. Rows now come from
//     the caller's workspace (workspace_id param, membership-checked)
//     UNION the legacy reserved workspace for pre-0.3.25 installs.
//
//  3. ListVisibleSkillSummariesByWorkspace excludes lock-hidden rows,
//     but the installer ends with a blanket Hide(source) — every
//     lab-owned skill was hidden=true, so even the legacy workspace
//     path listed nothing. ListClaudeLabSkillSummariesByWorkspace
//     keeps rows hidden by OTHER labs out but lets this lab's own
//     rows through (the flag gate is the access control here).
//
// The catalogue carries name/category/description only; the detail
// endpoint serves the full SKILL.md body and the file endpoint serves
// supporting references/scripts, so agents can load a specific skill
// on demand instead of injecting all ~294 bodies into every claim.
package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const claudeScienceReservedSlug = "claude-science"

// ClaudeScienceSkillSummary is one row in the catalogue response.
type ClaudeScienceSkillSummary struct {
	Name        string `json:"name"`
	Category    string `json:"category"`
	Description string `json:"description,omitempty"`
}

type ClaudeScienceSkillsResponse struct {
	Skills     []ClaudeScienceSkillSummary `json:"skills"`
	Total      int                         `json:"total"`
	Categories []string                    `json:"categories"`
}

// ClaudeScienceSkillDetail is the full-body response. Content prefers
// the DB row (post-install, repairable via re-install) and falls back
// to the bundled manifest asset for rows installed before 0.5.106
// whose content is still empty. Files lists the supporting reference /
// script files available through the file endpoint.
type ClaudeScienceSkillDetail struct {
	Name        string                   `json:"name"`
	Category    string                   `json:"category"`
	Description string                   `json:"description,omitempty"`
	Content     string                   `json:"content"`
	Installed   bool                     `json:"installed"`
	Files       []ClaudeScienceSkillFile `json:"files"`
}

// ClaudeScienceSkillFile is one servable supporting file, path
// relative to the skill's directory.
type ClaudeScienceSkillFile struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

// claudeScienceManifestSkillEntry mirrors one manifest skill entry.
type claudeScienceManifestSkillEntry struct {
	Category string
	BodyPath string
}

// claudeScienceManifestFile mirrors the manifest shape emitted by
// apps/desktop/scripts/build-claude-science-manifest.mjs.
type claudeScienceManifestFile struct {
	Skills []struct {
		Category string `json:"category"`
		Name     string `json:"name"`
		BodyPath string `json:"body_path"`
	} `json:"skills"`
}

// loadClaudeScienceManifestSkillEntries reads the bundled manifest and
// returns name -> {category, body_path}. Returns an empty map when the
// manifest isn't available; the list then still serves DB-installed
// rows with an empty category. MULTICA_RESOURCES_DIR follows the same
// env-var contract as install_claude_science.go.
func loadClaudeScienceManifestSkillEntries(manifestRoot string) map[string]claudeScienceManifestSkillEntry {
	if manifestRoot == "" {
		return map[string]claudeScienceManifestSkillEntry{}
	}
	path := filepath.Join(manifestRoot, "claude-science", "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]claudeScienceManifestSkillEntry{}
	}
	var m claudeScienceManifestFile
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]claudeScienceManifestSkillEntry{}
	}
	out := make(map[string]claudeScienceManifestSkillEntry, len(m.Skills))
	for _, s := range m.Skills {
		if s.Name == "" {
			continue
		}
		out[s.Name] = claudeScienceManifestSkillEntry{
			Category: strings.ToLower(s.Category),
			BodyPath: s.BodyPath,
		}
	}
	return out
}

// RegisterClaudeScienceSkillRoutes wires the catalogue + on-demand
// loading endpoints. The caller (router.go) MUST wrap in
// RequireExperimentalFlag("claude_science_lab") — the routes
// physically disappear when the flag is off (0.3.6 hard rule #1).
func RegisterClaudeScienceSkillRoutes(r chi.Router, h *Handler) {
	r.Get("/api/experimental/claude-science/skills", h.ClaudeScienceSkills)
	r.Get("/api/experimental/claude-science/skills/{skillName}", h.ClaudeScienceSkillDetail)
	r.Get("/api/experimental/claude-science/skills/{skillName}/file", h.ClaudeScienceSkillFileBytes)
}

// claudeScienceSkillWorkspaces resolves the workspaces whose skill rows
// the claude_science surfaces read: the caller's workspace (workspace_id
// param, membership-enforced — where 0.3.25+ installs land) first, then
// the legacy reserved-slug workspace for installs that predate the
// reserved-workspace removal. The bool is false when a response has
// already been written (bad param / non-member).
func (h *Handler) claudeScienceSkillWorkspaces(w http.ResponseWriter, r *http.Request) ([]pgtype.UUID, bool) {
	wsIDs := make([]pgtype.UUID, 0, 2)
	if raw := r.URL.Query().Get("workspace_id"); raw != "" {
		id, err := util.ParseUUID(raw)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workspace_id is not a UUID"})
			return nil, false
		}
		if _, ok := h.workspaceMember(w, r, id.String()); !ok {
			return nil, false
		}
		wsIDs = append(wsIDs, id)
	}
	if ws, err := h.Queries.GetWorkspaceBySlug(r.Context(), claudeScienceReservedSlug); err == nil {
		wsIDs = append(wsIDs, ws.ID)
	}
	return wsIDs, true
}

// ClaudeScienceSkills returns the installed-skill catalogue for the
// Knowledge tab: caller workspace rows unioned with legacy-workspace
// rows, deduped by name, enriched with manifest categories.
func (h *Handler) ClaudeScienceSkills(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("q")))
	categoryFilter := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("category")))

	wsIDs, ok := h.claudeScienceSkillWorkspaces(w, r)
	if !ok {
		return
	}
	entries := loadClaudeScienceManifestSkillEntries(os.Getenv(ManifestResourceDirEnv))

	out := make([]ClaudeScienceSkillSummary, 0, 64)
	categorySet := map[string]struct{}{}
	seen := map[string]struct{}{}
	for _, wsID := range wsIDs {
		rows, err := h.Queries.ListClaudeLabSkillSummariesByWorkspace(r.Context(), wsID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list skills")
			return
		}
		for _, row := range rows {
			if _, dup := seen[row.Name]; dup {
				continue
			}
			seen[row.Name] = struct{}{}
			entry := entries[row.Name]
			if categoryFilter != "" && entry.Category != categoryFilter {
				continue
			}
			if q != "" && !strings.Contains(strings.ToLower(row.Name), q) &&
				!strings.Contains(entry.Category, q) {
				continue
			}
			out = append(out, ClaudeScienceSkillSummary{
				Name:        row.Name,
				Category:    entry.Category,
				Description: row.Description,
			})
			categorySet[entry.Category] = struct{}{}
		}
	}

	categories := make([]string, 0, len(categorySet))
	for c := range categorySet {
		categories = append(categories, c)
	}
	sort.Strings(categories)

	writeJSON(w, http.StatusOK, ClaudeScienceSkillsResponse{
		Skills:     out,
		Total:      len(out),
		Categories: categories,
	})
}

// ClaudeScienceSkillDetail serves one skill's full SKILL.md body plus
// the list of servable supporting files. Resolution order: DB rows in
// the resolved workspaces (caller's first), then the bundled manifest
// asset for not-yet-repaired installs. Unknown names get 404.
func (h *Handler) ClaudeScienceSkillDetail(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "skillName")
	if strings.TrimSpace(name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skillName is required"})
		return
	}
	wsIDs, ok := h.claudeScienceSkillWorkspaces(w, r)
	if !ok {
		return
	}
	entry := loadClaudeScienceManifestSkillEntries(os.Getenv(ManifestResourceDirEnv))[name]

	content := ""
	description := ""
	installed := false
	for _, wsID := range wsIDs {
		row, err := h.Queries.GetSkillByWorkspaceAndName(r.Context(), db.GetSkillByWorkspaceAndNameParams{
			WorkspaceID: wsID,
			Name:        name,
		})
		if err == nil {
			content = row.Content
			description = row.Description
			installed = true
			break
		}
	}
	if entry.BodyPath == "" && !installed {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	if strings.TrimSpace(content) == "" && entry.BodyPath != "" {
		// Pre-0.5.106 rows carry empty content (nested-SKILL.md packaging
		// bug). Serve the bundled asset so agents can use the skill
		// before a re-install backfills the DB.
		content = loadManifestAsset(claudeScienceReservedSlug, entry.BodyPath)
	}
	if strings.TrimSpace(description) == "" || strings.HasPrefix(description, "Imported from Claude Science manifest") {
		if parsed := skillFrontmatterDescription(content); parsed != "" {
			description = parsed
		}
	}
	if strings.TrimSpace(content) == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill body unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, ClaudeScienceSkillDetail{
		Name:        name,
		Category:    entry.Category,
		Description: description,
		Content:     content,
		Installed:   installed,
		Files:       listClaudeScienceSkillFiles(entry.BodyPath),
	})
}

// claudeScienceSkillFileExts bounds what the file endpoint will serve.
// Everything in the bundled tree is text except .pdf (binary, and not
// agent-consumable) — the allowlist skips it plus anything unknown.
var claudeScienceSkillFileExts = map[string]string{
	".md":   "text/markdown; charset=utf-8",
	".py":   "text/x-python; charset=utf-8",
	".txt":  "text/plain; charset=utf-8",
	".json": "application/json",
	".tex":  "text/x-tex; charset=utf-8",
	".sty":  "text/x-tex; charset=utf-8",
	".bst":  "text/x-tex; charset=utf-8",
	".bib":  "text/x-bibtex; charset=utf-8",
	".sh":   "text/x-shellscript; charset=utf-8",
	".yaml": "text/yaml; charset=utf-8",
	".yml":  "text/yaml; charset=utf-8",
	".csv":  "text/csv; charset=utf-8",
}

// maxSkillFileBytes caps a single served file (largest reference in the
// bundled tree is ~60 KB; the cap is abuse headroom, not a feature).
const maxSkillFileBytes = 256 * 1024

// ClaudeScienceSkillFileBytes serves one supporting file from the
// skill's bundled directory. The path query param is relative to the
// skill dir; traversal outside it is rejected, as are non-allowlisted
// extensions and oversized files.
func (h *Handler) ClaudeScienceSkillFileBytes(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "skillName")
	rel := r.URL.Query().Get("path")
	if strings.TrimSpace(name) == "" || strings.TrimSpace(rel) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skillName and path are required"})
		return
	}
	if _, ok := h.claudeScienceSkillWorkspaces(w, r); !ok {
		return
	}
	entry := loadClaudeScienceManifestSkillEntries(os.Getenv(ManifestResourceDirEnv))[name]
	if entry.BodyPath == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "skill not found"})
		return
	}
	if rel != filepath.ToSlash(filepath.Clean(rel)) || strings.HasPrefix(rel, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path must be a clean relative path"})
		return
	}
	mime, ok := claudeScienceSkillFileExts[strings.ToLower(filepath.Ext(rel))]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file extension not servable"})
		return
	}
	skillDir := claudeScienceSkillRoot(entry.BodyPath)
	full := filepath.Join(skillDir, filepath.FromSlash(rel))
	if !strings.HasPrefix(full, skillDir+string(filepath.Separator)) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes the skill directory"})
		return
	}
	data, err := os.ReadFile(full)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not found"})
		return
	}
	if len(data) > maxSkillFileBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "file exceeds size cap"})
		return
	}
	w.Header().Set("Content-Type", mime)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// claudeScienceSkillRoot resolves the directory whose files the detail
// listing + file endpoint serve for a skill with the given body_path.
// Canonical trees: <dir(bodyPath)> itself (SKILL.md is a regular file
// inside it). Pre-0.5.106 nested trees: SKILL.md is a directory and the
// real body + references/scripts live inside it, so the root is one
// level deeper.
func claudeScienceSkillRoot(bodyPath string) string {
	root := filepath.Join(os.Getenv(ManifestResourceDirEnv), "claude-science", filepath.Dir(filepath.FromSlash(bodyPath)))
	if st, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil && st.IsDir() {
		return filepath.Join(root, "SKILL.md")
	}
	return root
}

// listClaudeScienceSkillFiles walks the skill's bundled directory
// (depth ≤ 2) and returns servable supporting files relative to the
// skill root. Missing tree → empty list; allowlist excluded extensions
// (e.g. .pdf) never surface; the SKILL.md body itself is omitted (the
// detail response already carries it).
func listClaudeScienceSkillFiles(bodyPath string) []ClaudeScienceSkillFile {
	out := []ClaudeScienceSkillFile{}
	if bodyPath == "" || os.Getenv(ManifestResourceDirEnv) == "" {
		return out
	}
	skillDir := claudeScienceSkillRoot(bodyPath)
	if st, serr := os.Stat(skillDir); serr != nil || !st.IsDir() {
		return out
	}
	var walk func(rel string, d int)
	walk = func(rel string, d int) {
		abs := skillDir
		if rel != "" {
			abs = filepath.Join(skillDir, filepath.FromSlash(rel))
		}
		fileEntries, err := os.ReadDir(abs)
		if err != nil {
			return
		}
		for _, e := range fileEntries {
			childRel := e.Name()
			if rel != "" {
				childRel = rel + "/" + e.Name()
			}
			if e.IsDir() {
				if d < 2 {
					walk(childRel, d+1)
				}
				continue
			}
			if _, ok := claudeScienceSkillFileExts[strings.ToLower(filepath.Ext(e.Name()))]; !ok {
				continue
			}
			if childRel == "SKILL.md" {
				continue // body is served by the detail endpoint
			}
			if info, ierr := e.Info(); ierr == nil {
				out = append(out, ClaudeScienceSkillFile{Path: childRel, Bytes: int(info.Size())})
			}
		}
	}
	walk("", 0)
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
