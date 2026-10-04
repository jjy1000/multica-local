package handler

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/signing"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Signature authorization endpoints (mig 294, Phase 1 observation layer).
//
// Surfaces:
//   - POST   /api/signature-assets                        upload watermark + generate keypair
//   - GET    /api/signature-assets                        list assets
//   - GET    /api/signature-assets/{assetID}/image        serve watermark bytes (authed)
//   - POST   /api/signature-assets/{assetID}/retire       retire asset (tombstone)
//   - GET    /api/signatures                              workspace signature history
//   - POST   /api/signatures/{signatureID}/revoke         revoke one authorization
//   - GET    /api/signatures/{signatureID}/verify         recompute + verify
//   - GET    /api/signatures/by-fingerprint/{fp}/verify   CLI verify entry
//   - GET/POST /api/issues/{issueID}/signatures           per-issue list / signing ceremony
//
// The signing ceremony posts one top-level `type='signature'` comment and
// publishes comment:created so the timeline renders the watermark card live.

const (
	maxSignatureImageBytes = 2 << 20 // 2 MiB — watermarks are small; the handler rejects larger uploads
	maxSignatureHistory    = 50
	maxSignatureExpiryDays = 365

	// signatureDisabledError is the message EVERY gate path returns. It is
	// user-facing (Settings deep-link guidance) and asserted by tests.
	signatureDisabledError = "signature authorization is not enabled; open Settings → Signatures, read the purpose and risk notice, and enable it yourself"
)

// requireSignatureArmed is the server-side enforcement of the feature's
// default-OFF posture: every signature surface (upload, ceremony, verify,
// claim-time attestation) refuses while the workspace has not armed the
// switch via Settings. The FE hiding the UI is cosmetic only — this is the
// actual gate.
func (h *Handler) requireSignatureArmed(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return false
	}
	if !signing.EnabledFromSettings(ws.Settings) {
		writeError(w, http.StatusForbidden, signatureDisabledError)
		return false
	}
	return true
}

type SignatureAssetResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Name        string  `json:"name"`
	Mime        string  `json:"mime"`
	ImageSha256 string  `json:"image_sha256"`
	Algorithm   string  `json:"algorithm"`
	PublicKeyFP string  `json:"public_key_fingerprint"` // sha256(public key) — display identity of the key
	ActivatedAt string  `json:"activated_at"`
	RetiredAt   *string `json:"retired_at"`
	CreatedAt   string  `json:"created_at"`
}

type RiskSignatureResponse struct {
	ID            string          `json:"id"`
	WorkspaceID   string          `json:"workspace_id"`
	IssueID       *string         `json:"issue_id"`
	AssetID       string          `json:"asset_id"`
	SignedBy      string          `json:"signed_by"`
	Ops           []string        `json:"ops"`
	Scope         json.RawMessage `json:"scope"`
	ContentSha256 string          `json:"content_sha256"`
	Fingerprint   string          `json:"fingerprint"`
	Signature     string          `json:"signature"`
	SignedAt      string          `json:"signed_at"`
	ExpiresAt     *string         `json:"expires_at"`
	RevokedAt     *string         `json:"revoked_at"`
	Status        string          `json:"status"` // active | revoked | expired
}

func signatureAssetToResponse(a db.SignatureAsset) SignatureAssetResponse {
	pubFP := sha256.Sum256(a.PublicKey)
	return SignatureAssetResponse{
		ID:          uuidToString(a.ID),
		WorkspaceID: uuidToString(a.WorkspaceID),
		Name:        a.Name,
		Mime:        a.Mime,
		ImageSha256: a.ImageSha256,
		Algorithm:   a.Algorithm,
		PublicKeyFP: hex.EncodeToString(pubFP[:])[:16],
		ActivatedAt: timestampToString(a.ActivatedAt),
		RetiredAt:   timestampToPtr(a.RetiredAt),
		CreatedAt:   timestampToString(a.CreatedAt),
	}
}

func riskSignatureStatus(s db.RiskSignature, now time.Time) string {
	switch {
	case s.RevokedAt.Valid:
		return "revoked"
	case s.ExpiresAt.Valid && s.ExpiresAt.Time.Before(now):
		return "expired"
	default:
		return "active"
	}
}

func riskSignatureToResponse(s db.RiskSignature) RiskSignatureResponse {
	ops := s.Ops
	if ops == nil {
		ops = []string{}
	}
	scope := json.RawMessage(s.Scope)
	if len(scope) == 0 {
		scope = json.RawMessage("{}")
	}
	return RiskSignatureResponse{
		ID:            uuidToString(s.ID),
		WorkspaceID:   uuidToString(s.WorkspaceID),
		IssueID:       uuidToPtr(s.IssueID),
		AssetID:       uuidToString(s.AssetID),
		SignedBy:      uuidToString(s.SignedBy),
		Ops:           ops,
		Scope:         scope,
		ContentSha256: s.ContentSha256,
		Fingerprint:   s.Fingerprint,
		Signature:     s.Signature,
		SignedAt:      timestampToString(s.SignedAt),
		ExpiresAt:     timestampToPtr(s.ExpiresAt),
		RevokedAt:     timestampToPtr(s.RevokedAt),
		Status:        riskSignatureStatus(s, time.Now()),
	}
}

// UploadSignatureAsset accepts a multipart upload (field "file", optional
// "name"), generates the per-asset Ed25519 keypair, persists the private
// key under ~/.multica/signing/<asset>.key BEFORE inserting the row (an
// orphan key file is harmless; a row without its key cannot sign), and
// returns the asset.
func (h *Handler) UploadSignatureAsset(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireSignatureArmed(w, r, workspaceID) {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSignatureImageBytes+64<<10)
	if err := r.ParseMultipartForm(maxSignatureImageBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload (image cap is 2 MiB)")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "multipart field 'file' is required")
		return
	}
	defer file.Close()
	image, err := io.ReadAll(io.LimitReader(file, maxSignatureImageBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read upload")
		return
	}
	if len(image) == 0 {
		writeError(w, http.StatusBadRequest, "empty file")
		return
	}
	if len(image) > maxSignatureImageBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "watermark image exceeds 2 MiB")
		return
	}
	mime := r.FormValue("mime")
	if mime == "" {
		if cts := r.MultipartForm.File["file"]; len(cts) > 0 && cts[0].Header.Get("Content-Type") != "" {
			mime = cts[0].Header.Get("Content-Type")
		}
	}
	if len(mime) < 6 || mime[:6] != "image/" {
		writeError(w, http.StatusBadRequest, "signature watermark must be an image (got MIME "+mime+")")
		return
	}
	name := r.FormValue("name")

	pub, priv, err := signing.GenerateKeyPair()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	imageSum := sha256.Sum256(image)

	// Key file first (see method comment), then the row carrying its UUID.
	assetUUID := parseUUID(uuid.New().String())
	if err := signing.SavePrivateKey(uuidToString(assetUUID), priv); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to persist signing key: "+err.Error())
		return
	}
	asset, err := h.Queries.CreateSignatureAsset(r.Context(), db.CreateSignatureAssetParams{
		ID:          assetUUID,
		WorkspaceID: workspaceUUID,
		Name:        name,
		Mime:        mime,
		Image:       image,
		ImageSha256: hex.EncodeToString(imageSum[:]),
		Algorithm:   signing.Algorithm,
		PublicKey:   []byte(pub),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create signature asset: "+err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"asset": signatureAssetToResponse(asset)})
}

// ListSignatureAssets returns the workspace's assets, newest first.
func (h *Handler) ListSignatureAssets(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireSignatureArmed(w, r, workspaceID) {
		return
	}
	assets, err := h.Queries.ListSignatureAssets(r.Context(), workspaceUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list signature assets")
		return
	}
	out := make([]SignatureAssetResponse, 0, len(assets))
	for _, a := range assets {
		out = append(out, signatureAssetToResponse(a))
	}
	writeJSON(w, http.StatusOK, map[string]any{"assets": out})
}

// requireSignatureAssetWorkspace loads the asset and checks the caller is a
// member of its workspace. Asset IDs are opaque, so the workspace comes
// from the row — never from the request.
func (h *Handler) requireSignatureAssetWorkspace(w http.ResponseWriter, r *http.Request) (db.SignatureAsset, bool) {
	assetID := chi.URLParam(r, "assetID")
	if _, ok := requireUserID(w, r); !ok {
		return db.SignatureAsset{}, false
	}
	assetUUID, ok := parseUUIDOrBadRequest(w, assetID, "asset id")
	if !ok {
		return db.SignatureAsset{}, false
	}
	asset, err := h.Queries.GetSignatureAsset(r.Context(), assetUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "signature asset not found")
		return db.SignatureAsset{}, false
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(asset.WorkspaceID)); !ok {
		return db.SignatureAsset{}, false
	}
	// Image serve + retire ride the same arm switch: with the feature off,
	// nothing about a watermark asset is reachable — even its bytes.
	if !h.requireSignatureArmed(w, r, uuidToString(asset.WorkspaceID)) {
		return db.SignatureAsset{}, false
	}
	return asset, true
}

// GetSignatureAssetImage serves the watermark bytes. Private + no-store:
// the watermark identifies the signer and must not sit in shared caches.
func (h *Handler) GetSignatureAssetImage(w http.ResponseWriter, r *http.Request) {
	asset, ok := h.requireSignatureAssetWorkspace(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", asset.Mime)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(asset.Image)
}

// RetireSignatureAsset tombstones an asset. Existing signatures stay
// verifiable off the stored public key; no NEW signings may use it.
func (h *Handler) RetireSignatureAsset(w http.ResponseWriter, r *http.Request) {
	asset, ok := h.requireSignatureAssetWorkspace(w, r)
	if !ok {
		return
	}
	retired, err := h.Queries.RetireSignatureAsset(r.Context(), asset.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to retire signature asset")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"asset": signatureAssetToResponse(retired)})
}

// ListWorkspaceSignatures returns recent signing history for the workspace.
func (h *Handler) ListWorkspaceSignatures(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	workspaceUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	if !h.requireSignatureArmed(w, r, workspaceID) {
		return
	}
	rows, err := h.Queries.ListRiskSignaturesByWorkspace(r.Context(), db.ListRiskSignaturesByWorkspaceParams{
		WorkspaceID: workspaceUUID,
		Limit:       maxSignatureHistory,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list signatures")
		return
	}
	out := make([]RiskSignatureResponse, 0, len(rows))
	for _, s := range rows {
		out = append(out, riskSignatureToResponse(s))
	}
	writeJSON(w, http.StatusOK, map[string]any{"signatures": out})
}

type signIssueRequest struct {
	AssetID     string   `json:"asset_id"`
	Ops         []string `json:"ops"`
	ExpiresDays int      `json:"expires_days"`
}

// SignIssue is the signing ceremony: it binds the user's watermark asset to
// a canonical scope over THIS issue's current content snapshot, signs it,
// records the row, and posts the `type='signature'` timeline marker.
func (h *Handler) SignIssue(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueID"))
	if !ok {
		return
	}
	if !h.requireSignatureArmed(w, r, uuidToString(issue.WorkspaceID)) {
		return
	}

	var req signIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	assetUUID, ok := parseUUIDOrBadRequest(w, req.AssetID, "asset_id")
	if !ok {
		return
	}
	ops, err := signing.NormalizeOps(req.Ops)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ExpiresDays < 0 || req.ExpiresDays > maxSignatureExpiryDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("expires_days must be 0 (none) or ≤ %d", maxSignatureExpiryDays))
		return
	}

	asset, err := h.Queries.GetSignatureAsset(r.Context(), assetUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "signature asset not found")
		return
	}
	if uuidToString(asset.WorkspaceID) != uuidToString(issue.WorkspaceID) {
		writeError(w, http.StatusForbidden, "signature asset belongs to a different workspace")
		return
	}
	if asset.RetiredAt.Valid {
		writeError(w, http.StatusConflict, "signature asset is retired; upload a new one in Settings")
		return
	}
	priv, err := signing.LoadPrivateKey(uuidToString(asset.ID))
	if err != nil {
		if errors.Is(err, signing.ErrKeyNotFound) {
			writeError(w, http.StatusConflict, "signing key not found on this machine; re-upload the signature asset in Settings to regenerate it")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Canonical scope over the CURRENT content snapshot. The expiry (when
	// set) is part of the signed scope, so it is folded in before the single
	// canonicalize → fingerprint → sign pass. The claim-time attestation
	// re-hashes the issue and skips injection on mismatch — editing the task
	// invalidates the authorization until re-signed.
	contentHash := signing.ContentHash(issue.Title, issue.Description.String)
	var expiresAt pgtype.Timestamptz
	if req.ExpiresDays > 0 {
		expiresAt = pgtype.Timestamptz{Time: time.Now().Add(time.Duration(req.ExpiresDays) * 24 * time.Hour), Valid: true}
	}
	scope := signing.Scope{
		IssueID:       uuidToString(issue.ID),
		IssueTitle:    issue.Title,
		ContentSHA256: contentHash,
		Ops:           ops,
		SignedBy:      userID,
	}
	if expiresAt.Valid {
		scope.ExpiresAt = expiresAt.Time.UTC().Format(time.RFC3339)
	}
	scopeJSON, err := scope.Canonical()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fingerprintHash, err := scope.FingerprintHash()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	fingerprint := hex.EncodeToString(fingerprintHash)
	sigHex, err := signing.Sign(priv, fingerprintHash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	userUUID := parseUUID(userID)
	row, err := h.Queries.CreateRiskSignature(r.Context(), db.CreateRiskSignatureParams{
		WorkspaceID:   issue.WorkspaceID,
		IssueID:       issue.ID,
		AssetID:       asset.ID,
		SignedBy:      userUUID,
		Ops:           ops,
		Scope:         scopeJSON,
		ContentSha256: contentHash,
		Fingerprint:   fingerprint,
		Signature:     sigHex,
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to record signature: "+err.Error())
		return
	}

	// Timeline marker: one top-level type='signature' comment. The content
	// is a compact human/CLI-readable summary; the FE renders the watermark
	// card off the comment type, not off this markdown.
	commentContent := fmt.Sprintf(
		"签署授权 · 指纹 `sha256:%s` · 范围 %s · 签署人已确认风险并自行承担全部责任。",
		fingerprint[:16], strings.Join(ops, ", "),
	)
	comment, err := h.Queries.CreateComment(r.Context(), db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "member",
		AuthorID:    userUUID,
		Content:     commentContent,
		Type:        "signature",
		ParentID:    pgtype.UUID{Valid: false},
	})
	if err != nil {
		slog.Warn("signature: create marker comment failed", "error", err, "issue_id", uuidToString(issue.ID))
	} else {
		if h.CausalRecorder != nil {
			h.CausalRecorder.RefreshForIssue(r.Context(), issue.ID)
		}
		h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "member", userID, map[string]any{
			"comment":             commentToResponse(comment, nil, nil),
			"issue_title":         issue.Title,
			"issue_assignee_type": textToPtr(issue.AssigneeType),
			"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
			"issue_status":        issue.Status,
		})
	}

	resp := riskSignatureToResponse(row)
	writeJSON(w, http.StatusCreated, map[string]any{
		"signature": resp,
		"comment":   commentToResponse(comment, nil, nil),
	})
}

// ListIssueSignatures returns the issue's signing history plus the active
// signature (what the claim-time attestation would consume).
func (h *Handler) ListIssueSignatures(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueID"))
	if !ok {
		return
	}
	if !h.requireSignatureArmed(w, r, uuidToString(issue.WorkspaceID)) {
		return
	}
	rows, err := h.Queries.ListRiskSignaturesByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list signatures")
		return
	}
	out := make([]RiskSignatureResponse, 0, len(rows))
	for _, s := range rows {
		out = append(out, riskSignatureToResponse(s))
	}
	var active *RiskSignatureResponse
	if latest, err := h.Queries.GetActiveRiskSignatureForIssue(r.Context(), issue.ID); err == nil {
		resp := riskSignatureToResponse(latest)
		active = &resp
	}
	writeJSON(w, http.StatusOK, map[string]any{"signatures": out, "active": active})
}

func (h *Handler) requireRiskSignatureWorkspace(w http.ResponseWriter, r *http.Request) (db.RiskSignature, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return db.RiskSignature{}, false
	}
	sigID := chi.URLParam(r, "signatureID")
	sigUUID, ok := parseUUIDOrBadRequest(w, sigID, "signature id")
	if !ok {
		return db.RiskSignature{}, false
	}
	row, err := h.Queries.GetRiskSignature(r.Context(), sigUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "signature not found")
		return db.RiskSignature{}, false
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(row.WorkspaceID)); !ok {
		return db.RiskSignature{}, false
	}
	// Verification rides the arm switch too: disabling the feature makes the
	// whole authorization channel unreachable (stale rows cannot keep
	// verifying). The constitution's "verify exits 0" contract only exists
	// on an armed install.
	if !h.requireSignatureArmed(w, r, uuidToString(row.WorkspaceID)) {
		return db.RiskSignature{}, false
	}
	return row, true
}

// RevokeSignature tombstones an authorization. Runs already dispatched keep
// their recorded linkage; new claims stop seeing coverage.
func (h *Handler) RevokeSignature(w http.ResponseWriter, r *http.Request) {
	row, ok := h.requireRiskSignatureWorkspace(w, r)
	if !ok {
		return
	}
	revoked, err := h.Queries.RevokeRiskSignature(r.Context(), row.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to revoke signature")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"signature": riskSignatureToResponse(revoked)})
}

// verifySignature recomputes everything derivable and reports each check.
// valid=true means: scope fingerprints to the stored fingerprint AND the
// Ed25519 signature verifies AND the authorization is neither revoked nor
// expired. Any single failure flips valid with its reason listed.
func (h *Handler) verifySignature(w http.ResponseWriter, r *http.Request, row db.RiskSignature) {
	var scope signing.Scope
	checks := map[string]bool{"scope_parsable": false, "fingerprint_matches": false, "signature_valid": false, "not_revoked": !row.RevokedAt.Valid, "not_expired": true}
	reasons := []string{}
	if err := json.Unmarshal(row.Scope, &scope); err != nil {
		reasons = append(reasons, "stored scope is not parsable JSON")
	} else {
		checks["scope_parsable"] = true
		hash, err := scope.FingerprintHash()
		if err != nil {
			reasons = append(reasons, "stored scope failed normalization: "+err.Error())
		} else if hex.EncodeToString(hash) != row.Fingerprint {
			reasons = append(reasons, "stored scope does not fingerprint to the stored value — the row was tampered with")
		} else {
			checks["fingerprint_matches"] = true
			asset, err := h.Queries.GetSignatureAsset(r.Context(), row.AssetID)
			if err != nil {
				reasons = append(reasons, "signing asset no longer exists")
			} else if signing.Verify(ed25519.PublicKey(asset.PublicKey), hash, row.Signature) {
				checks["signature_valid"] = true
			} else {
				reasons = append(reasons, "Ed25519 signature does not verify against the asset public key")
			}
		}
	}
	if row.ExpiresAt.Valid && row.ExpiresAt.Time.Before(time.Now()) {
		checks["not_expired"] = false
		reasons = append(reasons, "authorization expired at "+row.ExpiresAt.Time.Format(time.RFC3339))
	}
	if row.RevokedAt.Valid {
		reasons = append(reasons, "authorization revoked at "+row.RevokedAt.Time.Format(time.RFC3339))
	}
	valid := true
	for _, ok := range checks {
		if !ok {
			valid = false
			break
		}
	}
	if reasons == nil {
		reasons = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"signature":   riskSignatureToResponse(row),
		"valid":       valid,
		"checks":      checks,
		"reasons":     reasons,
		"verified_at": time.Now().UTC().Format(time.RFC3339),
	})
}

// VerifySignature verifies by signature ID.
func (h *Handler) VerifySignature(w http.ResponseWriter, r *http.Request) {
	row, ok := h.requireRiskSignatureWorkspace(w, r)
	if !ok {
		return
	}
	h.verifySignature(w, r, row)
}

// VerifySignatureByFingerprint verifies by bare-hex fingerprint — the
// `multica signature verify <fp>` entry point. Membership is checked
// against the ROW's workspace, not the request's.
func (h *Handler) VerifySignatureByFingerprint(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	fp := chi.URLParam(r, "fingerprint")
	if len(fp) != 64 {
		writeError(w, http.StatusBadRequest, "fingerprint must be 64 hex chars")
		return
	}
	row, err := h.Queries.GetRiskSignatureByFingerprint(r.Context(), fp)
	if err != nil {
		writeError(w, http.StatusNotFound, "no signature with that fingerprint")
		return
	}
	if _, ok := h.workspaceMember(w, r, uuidToString(row.WorkspaceID)); !ok {
		return
	}
	// CLI verify rides the same arm switch as every other surface (see
	// requireRiskSignatureWorkspace) — a disarmed install has no verify
	// contract, so stale rows cannot keep passing here either.
	if !h.requireSignatureArmed(w, r, uuidToString(row.WorkspaceID)) {
		return
	}
	h.verifySignature(w, r, row)
}

// loadActiveSignatureForClaim is the claim-time consumer: newest active
// signature for the issue whose content snapshot still matches, on a
// workspace where the feature is ARMED. Any non-armed / stale / revoked /
// expired state returns ok=false — no attestation is injected until the
// user re-arms and re-signs.
func (h *Handler) loadActiveSignatureForClaim(ctx context.Context, issueID pgtype.UUID) (db.RiskSignature, db.SignatureAsset, bool) {
	row, err := h.Queries.GetActiveRiskSignatureForIssue(ctx, issueID)
	if err != nil {
		return db.RiskSignature{}, db.SignatureAsset{}, false
	}
	issue, err := h.Queries.GetIssue(ctx, issueID)
	if err != nil {
		return db.RiskSignature{}, db.SignatureAsset{}, false
	}
	ws, err := h.Queries.GetWorkspace(ctx, issue.WorkspaceID)
	if err != nil || !signing.EnabledFromSettings(ws.Settings) {
		return db.RiskSignature{}, db.SignatureAsset{}, false
	}
	if row.ContentSha256 != signing.ContentHash(issue.Title, issue.Description.String) {
		slog.Info("signature: active authorization stale (content changed); skipping attestation",
			"issue_id", uuidToString(issueID),
			"fingerprint", row.Fingerprint,
		)
		return db.RiskSignature{}, db.SignatureAsset{}, false
	}
	asset, err := h.Queries.GetSignatureAsset(ctx, row.AssetID)
	if err != nil {
		return db.RiskSignature{}, db.SignatureAsset{}, false
	}
	return row, asset, true
}

// buildClaimAttestation renders the run-scoped attestation for a claimed
// task. Fail-open on the HMAC key (degraded integrity line, logged): the
// Ed25519 signature remains the primary anchor and a missing key file must
// not block dispatch.
func (h *Handler) buildClaimAttestation(taskID string, row db.RiskSignature, asset db.SignatureAsset) (text string, nonce string) {
	var hmacKey []byte
	if priv, err := signing.LoadPrivateKey(uuidToString(asset.ID)); err == nil {
		hmacKey = signing.AttestationHMACKey(priv)
	} else {
		slog.Warn("signature: attestation built without integrity line (key unavailable)", "error", err, "asset_id", uuidToString(asset.ID))
	}
	expires := "none"
	if row.ExpiresAt.Valid {
		expires = row.ExpiresAt.Time.UTC().Format(time.RFC3339)
	}
	text, nonce, err := signing.Attestation{
		TaskID:        taskID,
		IssueID:       uuidToString(row.IssueID),
		Fingerprint:   row.Fingerprint,
		Signer:        "workspace member",
		Ops:           row.Ops,
		SignedAt:      row.SignedAt.Time.UTC().Format(time.RFC3339),
		ExpiresAt:     expires,
		ContentSHA256: row.ContentSha256,
		HMACKey:       hmacKey,
	}.Build()
	if err != nil {
		slog.Warn("signature: build attestation failed", "error", err, "task_id", taskID)
		return "", ""
	}
	return text, nonce
}
