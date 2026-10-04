package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/signing"
)

// Signature authorization handler tests (mig 294). DB-backed per the
// handler suite conventions; private-key files are redirected through
// signing.KeyDirOverride (per-test temp dir — these tests must NOT run in
// parallel with anything else that touches the override).

func withSignatureKeyDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	signing.KeyDirOverride = dir
	t.Cleanup(func() { signing.KeyDirOverride = "" })
	return dir
}

// withSignatureArmed flips the default-OFF arm switch on for the shared
// test workspace and restores the previous settings on cleanup. Every
// signature surface test needs it — the server rejects the whole feature
// on a disarmed workspace even for fixtures with rows present.
func withSignatureArmed(t *testing.T) {
	t.Helper()
	var prev []byte
	if err := testPool.QueryRow(context.Background(),
		`SELECT settings FROM workspace WHERE id = $1`, testWorkspaceID).Scan(&prev); err != nil {
		t.Fatalf("read workspace settings: %v", err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = COALESCE(settings, '{}'::jsonb) || '{"signature_authorization_enabled":true}'::jsonb WHERE id = $1`,
		testWorkspaceID); err != nil {
		t.Fatalf("arm signature feature: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`UPDATE workspace SET settings = $2 WHERE id = $1`, testWorkspaceID, prev)
	})
}

func uploadTestSignatureAsset(t *testing.T, name string) string {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "watermark.png")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write([]byte("\x89PNG fake watermark bytes"))
	_ = mw.WriteField("name", name)
	_ = mw.WriteField("mime", "image/png")
	_ = mw.Close()

	req := httptest.NewRequest("POST", "/api/signature-assets", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-User-ID", testUserID)
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	rec := httptest.NewRecorder()
	testHandler.UploadSignatureAsset(rec, req)
	if rec.Code != 201 {
		t.Fatalf("upload asset: status %d body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Asset SignatureAssetResponse `json:"asset"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Asset.Algorithm != "ed25519" || len(out.Asset.PublicKeyFP) != 16 {
		t.Fatalf("unexpected asset response: %+v", out.Asset)
	}
	return out.Asset.ID
}

func createSignatureTestIssue(t *testing.T, title string) string {
	t.Helper()
	var issueID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		VALUES ($1, $2, 'todo', 'medium', 'member', $3,
		        COALESCE((SELECT MAX(number) FROM issue WHERE workspace_id = $1), 0) + 1)
		RETURNING id
	`, testWorkspaceID, title, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create test issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM comment WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM risk_signature WHERE issue_id = $1`, issueID)
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID
}

func callSignIssue(t *testing.T, issueID, assetID string, ops []string, expiresDays int) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	body := map[string]any{"asset_id": assetID, "ops": ops, "expires_days": expiresDays}
	req := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/signatures", body), "issueID", issueID)
	rec := httptest.NewRecorder()
	testHandler.SignIssue(rec, req)
	if rec.Code != 201 {
		return rec, nil
	}
	var out map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return rec, out
}

func TestSignatureUploadPersistsAssetAndKey(t *testing.T) {
	keyDir := withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")

	// Private key file exists under the override dir with 0600.
	info, err := os.Stat(filepath.Join(keyDir, assetID+".key"))
	if err != nil {
		t.Fatalf("private key file must exist: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
	}

	// Asset is listable for the workspace.
	rec := httptest.NewRecorder()
	testHandler.ListSignatureAssets(rec, newRequest("GET", "/api/signature-assets", nil))
	if rec.Code != 200 {
		t.Fatalf("list assets: status %d", rec.Code)
	}
	var list struct {
		Assets []SignatureAssetResponse `json:"assets"`
	}
	_ = json.NewDecoder(rec.Body).Decode(&list)
	found := false
	for _, a := range list.Assets {
		if a.ID == assetID {
			found = true
		}
	}
	if !found {
		t.Fatal("uploaded asset not returned by list")
	}

	// Watermark bytes are served with no-store.
	imgReq := withURLParam(newRequest("GET", "/api/signature-assets/"+assetID+"/image", nil), "assetID", assetID)
	imgRec := httptest.NewRecorder()
	testHandler.GetSignatureAssetImage(imgRec, imgReq)
	if imgRec.Code != 200 || imgRec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve image: status %d type %s", imgRec.Code, imgRec.Header().Get("Content-Type"))
	}
	if imgRec.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("watermark must be served no-store, got %q", imgRec.Header().Get("Cache-Control"))
	}
	if !strings.Contains(imgRec.Body.String(), "fake watermark") {
		t.Fatal("watermark bytes missing from response")
	}
}

func TestSignIssueRecordsSignatureCommentAndVerifies(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")
	issueID := createSignatureTestIssue(t, "红蓝对抗演练目标网段")

	rec, out := callSignIssue(t, issueID, assetID, []string{signing.OpOffensiveDrill, signing.OpCreateAgent}, 7)
	if out == nil {
		t.Fatalf("sign issue: status %d body %s", rec.Code, rec.Body.String())
	}
	sig := out["signature"].(map[string]any)
	fingerprint, _ := sig["fingerprint"].(string)
	if len(fingerprint) != 64 {
		t.Fatalf("fingerprint must be 64 hex chars, got %q", fingerprint)
	}
	if sig["status"] != "active" {
		t.Fatalf("fresh signature must be active, got %v", sig["status"])
	}

	// Marker comment of type 'signature' exists on the issue.
	var commentType string
	err := testPool.QueryRow(context.Background(),
		`SELECT type FROM comment WHERE issue_id = $1 AND type = 'signature' LIMIT 1`, issueID).Scan(&commentType)
	if err != nil {
		t.Fatalf("signature marker comment must exist: %v", err)
	}

	// Per-issue list reports it as the active signature.
	listReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/signatures", nil), "issueID", issueID)
	listRec := httptest.NewRecorder()
	testHandler.ListIssueSignatures(listRec, listReq)
	var listOut struct {
		Active *RiskSignatureResponse `json:"active"`
	}
	_ = json.NewDecoder(listRec.Body).Decode(&listOut)
	if listOut.Active == nil || listOut.Active.Fingerprint != fingerprint {
		t.Fatalf("active signature missing or mismatched: %+v", listOut.Active)
	}

	// Verify by fingerprint: all checks green.
	vRec := httptest.NewRecorder()
	vReq := withURLParam(newRequest("GET", "/api/signatures/by-fingerprint/"+fingerprint+"/verify", nil), "fingerprint", fingerprint)
	testHandler.VerifySignatureByFingerprint(vRec, vReq)
	if vRec.Code != 200 {
		t.Fatalf("verify: status %d body %s", vRec.Code, vRec.Body.String())
	}
	var verdict struct {
		Valid  bool            `json:"valid"`
		Checks map[string]bool `json:"checks"`
	}
	_ = json.NewDecoder(vRec.Body).Decode(&verdict)
	if !verdict.Valid {
		t.Fatalf("fresh signature must verify, checks=%v", verdict.Checks)
	}
}

func TestSignatureTamperedRowFailsVerification(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")
	issueID := createSignatureTestIssue(t, "tamper target")

	_, out := callSignIssue(t, issueID, assetID, []string{signing.OpLabDelegate}, 0)
	if out == nil {
		t.Fatal("sign failed")
	}
	sig := out["signature"].(map[string]any)
	fingerprint := sig["fingerprint"].(string)
	sigID := sig["id"].(string)

	// Tamper with the stored scope — recompute-verify must catch it.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE risk_signature SET scope = '{"ops":["lab_delegate","create_skill"],"signed_by":"attacker"}'::jsonb WHERE id = $1`, sigID); err != nil {
		t.Fatal(err)
	}
	vRec := httptest.NewRecorder()
	vReq := withURLParam(newRequest("GET", "/api/signatures/"+sigID+"/verify", nil), "signatureID", sigID)
	testHandler.VerifySignature(vRec, vReq)
	var verdict struct {
		Valid   bool     `json:"valid"`
		Reasons []string `json:"reasons"`
	}
	_ = json.NewDecoder(vRec.Body).Decode(&verdict)
	if verdict.Valid {
		t.Fatal("tampered scope must fail verification")
	}
	if len(verdict.Reasons) == 0 {
		t.Fatal("failure must carry reasons")
	}
	_ = fingerprint
}

func TestSignatureRevokeBlocksCoverage(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")
	issueID := createSignatureTestIssue(t, "revoke target")

	srec, out := callSignIssue(t, issueID, assetID, []string{signing.OpCreateSkill}, 0)
	if out == nil {
		t.Fatalf("sign issue: status %d body %s", srec.Code, srec.Body.String())
	}
	sig := out["signature"].(map[string]any)
	sigID := sig["id"].(string)

	rReq := withURLParam(newRequest("POST", "/api/signatures/"+sigID+"/revoke", nil), "signatureID", sigID)
	rRec := httptest.NewRecorder()
	testHandler.RevokeSignature(rRec, rReq)
	if rRec.Code != 200 {
		t.Fatalf("revoke: status %d body %s", rRec.Code, rRec.Body.String())
	}

	// Verify now fails with the revoked reason; active lookup returns nothing.
	vReq := withURLParam(newRequest("GET", "/api/signatures/"+sigID+"/verify", nil), "signatureID", sigID)
	vRec := httptest.NewRecorder()
	testHandler.VerifySignature(vRec, vReq)
	var verdict struct {
		Valid   bool     `json:"valid"`
		Reasons []string `json:"reasons"`
	}
	_ = json.NewDecoder(vRec.Body).Decode(&verdict)
	if verdict.Valid || len(verdict.Reasons) == 0 {
		t.Fatalf("revoked signature must fail with reasons, got valid=%v", verdict.Valid)
	}
	if _, err := testHandler.Queries.GetActiveRiskSignatureForIssue(context.Background(), mustParseUUID(t, issueID)); err == nil {
		t.Fatal("revoked signature must not be returned as active")
	}
}

func TestSignatureStaleContentSnapshotSkipsClaimCoverage(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")
	issueID := createSignatureTestIssue(t, "original title")

	if _, out := callSignIssue(t, issueID, assetID, []string{signing.OpOffensiveDrill}, 0); out == nil {
		t.Fatal("sign failed")
	}
	// Before the edit the claim loader sees coverage.
	if _, _, ok := testHandler.loadActiveSignatureForClaim(context.Background(), mustParseUUID(t, issueID)); !ok {
		t.Fatal("un-edited issue must be covered")
	}
	// Editing the task invalidates the authorization until re-signed.
	if _, err := testPool.Exec(context.Background(), `UPDATE issue SET title = 'edited title' WHERE id = $1`, issueID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := testHandler.loadActiveSignatureForClaim(context.Background(), mustParseUUID(t, issueID)); ok {
		t.Fatal("edited issue must NOT be covered by the pre-edit signature")
	}
}

func TestSignIssueRejectsBadScopes(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "primary")
	issueID := createSignatureTestIssue(t, "bad scopes")

	// Unknown op.
	if rec, out := callSignIssue(t, issueID, assetID, []string{"god_mode"}, 0); out != nil {
		t.Fatalf("unknown op must be rejected, got 201: %s", rec.Body.String())
	}
	// Empty op set.
	if rec, out := callSignIssue(t, issueID, assetID, nil, 0); out != nil {
		t.Fatalf("empty op set must be rejected, got 201: %s", rec.Body.String())
	}
	// Unknown asset.
	if rec, out := callSignIssue(t, issueID, "00000000-0000-0000-0000-000000000000", []string{signing.OpCreateAgent}, 0); out != nil {
		t.Fatalf("unknown asset must 404, got %d", rec.Code)
	}
}

// TestClaimInjectsAuthorizationAttestation is the end-to-end pin for the
// claim-time channel: a covered issue's claim response carries the
// attestation (prepended to agent instructions + dedicated field), and the
// run row records the signature linkage. An unsigned issue claims with no
// attestation at all.
func TestClaimInjectsAuthorizationAttestation(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	ctx := context.Background()

	// Signed issue.
	assetID := uploadTestSignatureAsset(t, "claim")
	signedIssueID := createSignatureTestIssue(t, "红蓝对抗 claim 注入")
	if rec, out := callSignIssue(t, signedIssueID, assetID, []string{signing.OpOffensiveDrill}, 0); out == nil {
		t.Fatalf("sign issue: %d %s", rec.Code, rec.Body.String())
	}
	// Unsigned issue.
	plainIssueID := createSignatureTestIssue(t, "普通任务")

	runtimeID := createClaimReclaimRuntime(t, ctx, "signature-claim-runtime")
	agentID, _ := createClaimReclaimAgentAndIssue(t, ctx, runtimeID, "signature-claim-agent")

	enqueueQueuedTask := func(issueID string) string {
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
			VALUES ($1, $2, $3, 'queued', 0)
			RETURNING id
		`, agentID, runtimeID, issueID).Scan(&taskID); err != nil {
			t.Fatalf("enqueue task: %v", err)
		}
		t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM agent_task_queue WHERE id = $1`, taskID) })
		return taskID
	}

	// Covered claim.
	enqueueQueuedTask(signedIssueID)
	task, body := claimTaskByRuntimeForTest(t, runtimeID)
	if task == nil {
		t.Fatal("covered claim returned no task")
	}
	if !strings.Contains(body, `"authorization_attestation"`) || !strings.Contains(body, signing.AttestationHeader) {
		t.Fatal("covered claim must carry the attestation on the wire")
	}
	if !strings.Contains(body, `"signature_id"`) {
		t.Fatal("covered claim must carry the signature linkage")
	}
	var claimResp struct {
		Task struct {
			Agent struct {
				Instructions string `json:"instructions"`
			} `json:"agent"`
		} `json:"task"`
	}
	if err := json.Unmarshal([]byte(body), &claimResp); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(claimResp.Task.Agent.Instructions), signing.AttestationHeader) {
		t.Fatal("attestation must be PREPENDED to agent instructions (constitution outranks identity)")
	}
	// Run row carries the audit linkage.
	var sigID string
	if err := testPool.QueryRow(ctx,
		`SELECT context->>'signature_id' FROM agent_task_queue WHERE id = $1`, task.ID).Scan(&sigID); err != nil || sigID == "" {
		t.Fatalf("run row must record signature_id, got %q err %v", sigID, err)
	}
	// Free the agent's single concurrency slot so the next claim can run.
	if _, err := testPool.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, task.ID); err != nil {
		t.Fatal(err)
	}

	// Unsigned claim: no attestation anywhere on the wire.
	enqueueQueuedTask(plainIssueID)
	plainTask, plainBody := claimTaskByRuntimeForTest(t, runtimeID)
	if plainTask == nil {
		t.Fatal("plain claim returned no task")
	}
	if strings.Contains(plainBody, signing.AttestationHeader) || strings.Contains(plainBody, `"authorization_attestation"`) {
		t.Fatal("unsigned issue must claim with zero attestation content")
	}
}

// TestSignatureDisarmedRejectsAllSurfaces pins the default-OFF posture:
// with the workspace switch unarmed, every signature surface returns 403
// with the guidance message — upload, list, ceremony, issue list. This is
// the actual gate; the FE hiding the tab is cosmetic.
func TestSignatureDisarmedRejectsAllSurfaces(t *testing.T) {
	withSignatureKeyDir(t)
	// NOTE: deliberately NOT calling withSignatureArmed here.

	// Upload.
	upBuf := &bytes.Buffer{}
	mw := multipart.NewWriter(upBuf)
	fw, _ := mw.CreateFormFile("file", "wm.png")
	fw.Write([]byte("\x89PNG fake"))
	_ = mw.WriteField("mime", "image/png")
	_ = mw.Close()
	upReq := httptest.NewRequest("POST", "/api/signature-assets", upBuf)
	upReq.Header.Set("Content-Type", mw.FormDataContentType())
	upReq.Header.Set("X-User-ID", testUserID)
	upReq.Header.Set("X-Workspace-ID", testWorkspaceID)
	upRec := httptest.NewRecorder()
	testHandler.UploadSignatureAsset(upRec, upReq)
	if upRec.Code != http.StatusForbidden || !strings.Contains(upRec.Body.String(), "not enabled") {
		t.Fatalf("disarmed upload must 403 with guidance, got %d %s", upRec.Code, upRec.Body.String())
	}

	// Asset list.
	listRec := httptest.NewRecorder()
	testHandler.ListSignatureAssets(listRec, newRequest("GET", "/api/signature-assets", nil))
	if listRec.Code != http.StatusForbidden {
		t.Fatalf("disarmed asset list must 403, got %d", listRec.Code)
	}

	// Ceremony.
	issueID := createSignatureTestIssue(t, "disarmed ceremony")
	signReq := withURLParam(newRequest("POST", "/api/issues/"+issueID+"/signatures",
		map[string]any{"asset_id": "00000000-0000-0000-0000-000000000000", "ops": []string{signing.OpOffensiveDrill}}), "issueID", issueID)
	signRec := httptest.NewRecorder()
	testHandler.SignIssue(signRec, signReq)
	if signRec.Code != http.StatusForbidden {
		t.Fatalf("disarmed ceremony must 403, got %d", signRec.Code)
	}

	// Issue signature list.
	listIssueReq := withURLParam(newRequest("GET", "/api/issues/"+issueID+"/signatures", nil), "issueID", issueID)
	listIssueRec := httptest.NewRecorder()
	testHandler.ListIssueSignatures(listIssueRec, listIssueReq)
	if listIssueRec.Code != http.StatusForbidden {
		t.Fatalf("disarmed issue list must 403, got %d", listIssueRec.Code)
	}
}

// TestSignatureDisarmSuspendsExistingCoverage proves arming is part of the
// authorization lifecycle: a workspace with an ACTIVE signature stops
// injecting attestations and stops VERIFYING the moment the switch goes
// off — without touching the row. Re-arming restores both, since the row
// and content snapshot are unchanged.
func TestSignatureDisarmSuspendsExistingCoverage(t *testing.T) {
	withSignatureKeyDir(t)
	withSignatureArmed(t)
	assetID := uploadTestSignatureAsset(t, "suspend")
	issueID := createSignatureTestIssue(t, "disarm suspension target")
	if rec, out := callSignIssue(t, issueID, assetID, []string{signing.OpOffensiveDrill}, 0); out == nil {
		t.Fatalf("sign while armed: %d %s", rec.Code, rec.Body.String())
	}

	var fingerprint string
	if err := testPool.QueryRow(context.Background(),
		`SELECT fingerprint FROM risk_signature WHERE issue_id = $1 ORDER BY signed_at DESC LIMIT 1`, issueID).Scan(&fingerprint); err != nil {
		t.Fatal(err)
	}

	// Armed: coverage + verification both work.
	if _, _, ok := testHandler.loadActiveSignatureForClaim(context.Background(), mustParseUUID(t, issueID)); !ok {
		t.Fatal("armed workspace must see coverage")
	}
	if !verifyByFingerprintOK(t, fingerprint) {
		t.Fatal("armed workspace must verify the signature")
	}

	// Disarm: same row, same content — no coverage, no verification.
	disarmSignature(t)
	if _, _, ok := testHandler.loadActiveSignatureForClaim(context.Background(), mustParseUUID(t, issueID)); ok {
		t.Fatal("disarmed workspace must NOT inject attestations even with an active signature row")
	}
	if verifyByFingerprintOK(t, fingerprint) {
		t.Fatal("disarmed workspace must refuse verification (403), not pass")
	}

	// Re-arm (the cleanup ordering means re-arming again is idempotent):
	// everything comes back without re-signing.
	withSignatureArmed(t)
	if _, _, ok := testHandler.loadActiveSignatureForClaim(context.Background(), mustParseUUID(t, issueID)); !ok {
		t.Fatal("re-armed workspace must restore coverage")
	}
}

func disarmSignature(t *testing.T) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE workspace SET settings = COALESCE(settings, '{}'::jsonb) || '{"signature_authorization_enabled":false}'::jsonb WHERE id = $1`,
		testWorkspaceID); err != nil {
		t.Fatalf("disarm: %v", err)
	}
}

func verifyByFingerprintOK(t *testing.T, fingerprint string) bool {
	t.Helper()
	req := withURLParam(newRequest("GET", "/api/signatures/by-fingerprint/"+fingerprint+"/verify", nil), "fingerprint", fingerprint)
	rec := httptest.NewRecorder()
	testHandler.VerifySignatureByFingerprint(rec, req)
	if rec.Code == http.StatusOK {
		var verdict struct {
			Valid bool `json:"valid"`
		}
		_ = json.NewDecoder(rec.Body).Decode(&verdict)
		return verdict.Valid
	}
	if rec.Code == http.StatusForbidden {
		return false
	}
	t.Fatalf("verify: unexpected status %d body %s", rec.Code, rec.Body.String())
	return false
}
