package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

// ArtifactMeta is the wire shape for a single plugin artifact. File-backed
// artifacts (image/html/file) carry a URL pointing at the raw-serve endpoint;
// inline artifacts (chart/table/code/text) carry Data directly.
type ArtifactMeta struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"` // "image" | "chart" | "table" | "html" | "code" | "file" | "text"
	Title     string    `json:"title"`
	MimeType  string    `json:"mime_type,omitempty"`
	Size      int64     `json:"size,omitempty"`
	Data      any       `json:"data,omitempty"` // for chart/table/code/text: inline data
	URL       string    `json:"url,omitempty"`  // for image/file/html: download URL
	CreatedAt time.Time `json:"created_at"`
}

// storedArtifact extends ArtifactMeta with the on-disk filename so the
// index can locate the backing file. FileName is persisted in index.json
// but omitted from API responses (callers use URL instead).
type storedArtifact struct {
	ArtifactMeta
	FileName string `json:"file_name,omitempty"`
}

// validArtifactTypes enumerates the accepted artifact type values.
var validArtifactTypes = map[string]bool{
	"image": true,
	"chart": true,
	"table": true,
	"html":  true,
	"code":  true,
	"file":  true,
	"text":  true,
}

// inlineArtifactTypes are stored as JSON data in the index (no file on disk).
var inlineArtifactTypes = map[string]bool{
	"chart": true,
	"table": true,
	"code":  true,
	"text":  true,
}

// pluginArtifactDir resolves the artifact storage directory for a plugin:
// ~/.multica/plugins/<slug>/artifacts/. The directory is NOT created here;
// callers create it lazily on first write.
func pluginArtifactDir(slug string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".multica", "plugins", slug, "artifacts"), nil
}

// sanitizeArtifactTitle cleans a user-supplied artifact title / multipart
// filename so it can never carry path separators, traversal components, or
// control characters into the index or the Content-Disposition header
// (F-006). Returns "" when the input contains nothing usable.
func sanitizeArtifactTitle(raw string) string {
	s := strings.ReplaceAll(raw, "\\", "/")
	s = filepath.Base(s)
	s = strings.TrimSpace(s)
	if s == "." || s == ".." || s == "/" {
		return ""
	}
	// Strip control characters (keeps display/metadata safe for headers).
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	const maxTitleLen = 200
	if len(s) > maxTitleLen {
		s = s[:maxTitleLen]
	}
	return s
}

// artifactExtPattern constrains the on-disk extension derived from an upload
// filename: a single dot followed by letters/digits only, at most 12 chars.
var artifactExtPattern = regexp.MustCompile(`^\.[A-Za-z0-9]{1,12}$`)

// sanitizeArtifactExt returns the extension only when it matches the safe
// pattern; anything else (path separators, traversal, control chars, weird
// encodings) degrades to "" so the on-disk name stays id + safe ext.
func sanitizeArtifactExt(ext string) string {
	if artifactExtPattern.MatchString(ext) {
		return strings.ToLower(ext)
	}
	return ""
}

// allowedArtifactMimePrefixes whitelists the mime base types accepted for
// uploaded artifact files. Anything else degrades to application/octet-stream
// — still stored, but served with attachment disposition so it can never be
// rendered inline in the renderer origin.
var allowedArtifactMimePrefixes = []string{
	"image/", "text/", "application/json", "application/xml",
	"application/svg+xml", "application/pdf", "application/octet-stream",
	"application/zip", "font/", "audio/", "video/",
}

// sanitizeArtifactMime returns mimeType unchanged when its base type is
// whitelisted, else the inert application/octet-stream fallback.
func sanitizeArtifactMime(mimeType string) string {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0]))
	for _, p := range allowedArtifactMimePrefixes {
		if strings.HasPrefix(base, p) {
			return mimeType
		}
	}
	return "application/octet-stream"
}

// loadArtifactIndex reads and parses the index.json for a plugin. A missing
// file returns an empty slice (no artifacts yet).
func loadArtifactIndex(dir string) ([]storedArtifact, error) {
	data, err := os.ReadFile(filepath.Join(dir, "index.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read index: %w", err)
	}
	var items []storedArtifact
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("parse index: %w", err)
	}
	return items, nil
}

// saveArtifactIndex writes the index atomically (write tmp + rename) so a
// crash mid-write cannot corrupt the existing index.
func saveArtifactIndex(dir string, items []storedArtifact) error {
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	tmp := filepath.Join(dir, "index.json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp index: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "index.json")); err != nil {
		return fmt.Errorf("rename index: %w", err)
	}
	return nil
}

// generateArtifactID produces a unique-enough ID for single-user use:
// hex-encoded UnixNano timestamp (16 chars).
func generateArtifactID() string {
	return fmt.Sprintf("%x", time.Now().UnixNano())
}

// ListPluginArtifacts returns the artifact index for a user plugin.
// GET /api/user-plugins/{slug}/artifacts
func (h *Handler) ListPluginArtifacts(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	if !validateUserPluginSlug(slug) {
		writeError(w, http.StatusBadRequest, "invalid plugin slug")
		return
	}

	dir, err := pluginArtifactDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve artifact directory")
		return
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: failed to load index", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}

	// Strip internal fields for the wire response.
	resp := make([]ArtifactMeta, 0, len(items))
	for _, item := range items {
		resp = append(resp, item.ArtifactMeta)
	}
	writeJSON(w, http.StatusOK, resp)
}

// uploadArtifactJSON is the JSON body shape for inline artifact uploads.
type uploadArtifactJSON struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	Data  any    `json:"data"`
}

// UploadPluginArtifact accepts a multipart file upload or a JSON body.
// POST /api/user-plugins/{slug}/artifacts
//
// Multipart: file field + type + title → saves file to disk, returns
// metadata with URL.
// JSON: {type, title, data} → for chart/table/code/text artifacts,
// data is stored inline in the index.
func (h *Handler) UploadPluginArtifact(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	if !validateUserPluginSlug(slug) {
		writeError(w, http.StatusBadRequest, "invalid plugin slug")
		return
	}

	dir, err := pluginArtifactDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve artifact directory")
		return
	}

	contentType := r.Header.Get("Content-Type")

	if strings.HasPrefix(contentType, "multipart/form-data") {
		h.uploadArtifactMultipart(w, r, slug, dir)
		return
	}

	// Default: JSON inline artifact.
	h.uploadArtifactInline(w, r, slug, dir)
}

// uploadArtifactMultipart handles a multipart/form-data upload with a file
// field plus type and title form values.
func (h *Handler) uploadArtifactMultipart(w http.ResponseWriter, r *http.Request, slug, dir string) {
	// 32 MB max memory; larger files spill to temp files on disk.
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "failed to parse multipart form")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field in multipart form")
		return
	}
	defer file.Close()

	artifactType := r.FormValue("type")
	if artifactType == "" {
		artifactType = "file"
	}
	if !validArtifactTypes[artifactType] {
		writeError(w, http.StatusBadRequest, "type must be one of: image, chart, table, html, code, file, text")
		return
	}

	// F-006: sanitize the multipart filename before it can influence the
	// index title, the on-disk name, or the serve Content-Disposition.
	title := sanitizeArtifactTitle(r.FormValue("title"))
	if title == "" {
		title = sanitizeArtifactTitle(header.Filename)
	}
	if title == "" {
		title = "artifact"
	}

	// Detect mime type from the sanitized extension, whitelisted.
	ext := sanitizeArtifactExt(filepath.Ext(header.Filename))
	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	mimeType = sanitizeArtifactMime(mimeType)

	id := generateArtifactID()
	fileName := id + ext

	// Lazily create the artifact directory.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("plugin artifacts: mkdir failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create artifact directory")
		return
	}

	dst, err := os.Create(filepath.Join(dir, fileName))
	if err != nil {
		slog.Error("plugin artifacts: create file failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to save artifact file")
		return
	}
	defer dst.Close()

	size, err := io.Copy(dst, file)
	if err != nil {
		slog.Error("plugin artifacts: write file failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to write artifact file")
		return
	}

	meta := storedArtifact{
		ArtifactMeta: ArtifactMeta{
			ID:        id,
			Type:      artifactType,
			Title:     title,
			MimeType:  mimeType,
			Size:      size,
			URL:       fmt.Sprintf("/api/user-plugins/%s/artifacts/%s/raw", slug, id),
			CreatedAt: time.Now().UTC(),
		},
		FileName: fileName,
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: load index for append", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}
	items = append(items, meta)
	if err := saveArtifactIndex(dir, items); err != nil {
		slog.Error("plugin artifacts: save index", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update artifact index")
		return
	}

	writeJSON(w, http.StatusCreated, meta.ArtifactMeta)
}

// uploadArtifactInline handles a JSON body upload for inline data artifacts
// (chart, table, code, text).
func (h *Handler) uploadArtifactInline(w http.ResponseWriter, r *http.Request, slug, dir string) {
	var body uploadArtifactJSON
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	if body.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if !validArtifactTypes[body.Type] {
		writeError(w, http.StatusBadRequest, "type must be one of: image, chart, table, html, code, file, text")
		return
	}
	if body.Title == "" {
		body.Title = body.Type + " artifact"
	}
	if body.Data == nil {
		writeError(w, http.StatusBadRequest, "data is required for inline artifacts")
		return
	}

	id := generateArtifactID()

	meta := storedArtifact{
		ArtifactMeta: ArtifactMeta{
			ID:        id,
			Type:      body.Type,
			Title:     body.Title,
			Data:      body.Data,
			CreatedAt: time.Now().UTC(),
		},
	}

	// Lazily create the directory even for inline artifacts so the index
	// file has a home.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		slog.Error("plugin artifacts: mkdir failed", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create artifact directory")
		return
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: load index for append", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}
	items = append(items, meta)
	if err := saveArtifactIndex(dir, items); err != nil {
		slog.Error("plugin artifacts: save index", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update artifact index")
		return
	}

	writeJSON(w, http.StatusCreated, meta.ArtifactMeta)
}

// ServePluginArtifactRaw serves the raw file content for a file-backed
// artifact. Sets Content-Type from the stored mime_type.
//
// Two auth paths:
//   - signed (?sig=...): an <img>/<iframe>/<a download> element cannot send
//     the Bearer header, so the short-lived HMAC signature (minted by the
//     /sign endpoint) proves access instead. Served inline (no attachment
//     disposition) with X-Content-Type-Options: nosniff; the security
//     boundary is the consumer's sandbox.
//   - unsigned: Bearer auth + Content-Disposition: attachment (F-006) so an
//     uploaded HTML/JS file can never render inline in the app's origin.
//
// GET /api/user-plugins/{slug}/artifacts/{artifactID}/raw
func (h *Handler) ServePluginArtifactRaw(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	artifactID := chi.URLParam(r, "artifactID")
	if !validateUserPluginSlug(slug) || artifactID == "" {
		writeError(w, http.StatusBadRequest, "invalid plugin slug or artifact ID")
		return
	}

	signed := r.URL.Query().Get("sig") != ""
	if signed {
		// Signed path: verify the HMAC instead of requiring Bearer auth
		// (iframe/img elements cannot attach the Authorization header).
		uid := r.URL.Query().Get("uid")
		expRaw := r.URL.Query().Get("exp")
		exp, err := strconv.ParseInt(expRaw, 10, 64)
		if err != nil || !verifyPluginArtifactSignature(uid, slug, artifactID, r.URL.Query().Get("sig"), exp) {
			writeError(w, http.StatusForbidden, "invalid or expired signature")
			return
		}
	} else if _, ok := requireUserID(w, r); !ok {
		return
	}

	dir, err := pluginArtifactDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve artifact directory")
		return
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: load index for serve", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}

	var target *storedArtifact
	for i := range items {
		if items[i].ID == artifactID {
			target = &items[i]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}
	if target.FileName == "" {
		writeError(w, http.StatusBadRequest, "artifact has no backing file (inline data artifact)")
		return
	}

	filePath := filepath.Join(dir, target.FileName)

	f, err := os.Open(filePath)
	if err != nil {
		slog.Error("plugin artifacts: open backing file", "slug", slug, "artifact", artifactID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to open artifact file")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to stat artifact file")
		return
	}

	contentType := target.MimeType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)

	serveName := filepath.Base(target.FileName)
	if signed {
		// Inline render (img/iframe): do NOT force a download, and pin
		// nosniff so the browser cannot re-interpret the bytes as a
		// different MIME type. The consumer's sandbox is the boundary.
		w.Header().Set("X-Content-Type-Options", "nosniff")
	} else {
		// F-006: always serve as a download — a stored artifact (e.g. an
		// uploaded HTML/JS file whose mime whitelisted as text/html) must
		// never render inline with the app's origin. The filename param is
		// the already-safe on-disk name (id + whitelisted ext).
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", serveName))
	}
	http.ServeContent(w, r, serveName, info.ModTime(), f)
}

// DeletePluginArtifact removes an artifact from the index and deletes its
// backing file (if any).
// DELETE /api/user-plugins/{slug}/artifacts/{artifactID}
func (h *Handler) DeletePluginArtifact(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}

	slug := chi.URLParam(r, "slug")
	artifactID := chi.URLParam(r, "artifactID")
	if !validateUserPluginSlug(slug) || artifactID == "" {
		writeError(w, http.StatusBadRequest, "invalid plugin slug or artifact ID")
		return
	}

	dir, err := pluginArtifactDir(slug)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve artifact directory")
		return
	}

	items, err := loadArtifactIndex(dir)
	if err != nil {
		slog.Error("plugin artifacts: load index for delete", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to read artifact index")
		return
	}

	found := -1
	for i := range items {
		if items[i].ID == artifactID {
			found = i
			break
		}
	}
	if found == -1 {
		writeError(w, http.StatusNotFound, "artifact not found")
		return
	}

	// Remove the backing file if present.
	if items[found].FileName != "" {
		filePath := filepath.Join(dir, items[found].FileName)
		if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
			slog.Warn("plugin artifacts: failed to remove backing file",
				"slug", slug, "artifact", artifactID, "error", err)
		}
	}

	// Remove from the index.
	items = append(items[:found], items[found+1:]...)
	if err := saveArtifactIndex(dir, items); err != nil {
		slog.Error("plugin artifacts: save index after delete", "slug", slug, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update artifact index")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
