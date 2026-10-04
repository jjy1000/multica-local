package signing

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopeFingerprintDeterministic(t *testing.T) {
	a := Scope{IssueID: "11111111-1111-1111-1111-111111111111", Ops: []string{OpCreateAgent, OpOffensiveDrill}, SignedBy: "u1"}
	b := Scope{IssueID: "11111111-1111-1111-1111-111111111111", Ops: []string{OpOffensiveDrill, OpCreateAgent, OpOffensiveDrill}, SignedBy: "u1"} // reordered + duped
	fa, err := a.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	fb, err := b.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fa != fb {
		t.Fatalf("op order/duplication must not change the fingerprint: %s vs %s", fa, fb)
	}

	c := a
	c.ContentSHA256 = "different"
	fc, err := c.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	if fa == fc {
		t.Fatal("content snapshot change must change the fingerprint")
	}
}

func TestNormalizeOpsRejectsUnknown(t *testing.T) {
	if _, err := NormalizeOps([]string{OpCreateAgent}); err != nil {
		t.Fatalf("known op rejected: %v", err)
	}
	if _, err := NormalizeOps(nil); err == nil {
		t.Fatal("empty op set must be rejected — a scope with no ops authorizes nothing and must not be signable")
	}
	if _, err := NormalizeOps([]string{OpCreateAgent, "god_mode"}); err == nil {
		t.Fatal("unknown op must be rejected, not silently dropped")
	}
}

func TestSignVerifyRoundtrip(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	scope := Scope{Ops: []string{OpLabDelegate}, SignedBy: "u1"}
	hash, err := scope.FingerprintHash()
	if err != nil {
		t.Fatal(err)
	}
	sig, err := Sign(priv, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !Verify(pub, hash, sig) {
		t.Fatal("signature must verify")
	}
	// Tampered signature hex fails.
	if Verify(pub, hash, "00"+sig[2:]) {
		t.Fatal("tampered signature must not verify")
	}
	// Wrong key fails.
	pub2, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if Verify(pub2, hash, sig) {
		t.Fatal("signature must not verify against a different key")
	}
	// Wrong scope fails.
	other := Scope{Ops: []string{OpCreateSkill}, SignedBy: "u1"}
	otherHash, _ := other.FingerprintHash()
	if Verify(pub, otherHash, sig) {
		t.Fatal("signature must not verify over a different scope")
	}
}

func TestPrivateKeyFileLifecycle(t *testing.T) {
	dir := t.TempDir()
	KeyDirOverride = dir
	t.Cleanup(func() { KeyDirOverride = "" })

	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	if err := SavePrivateKey("asset-1", priv); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadPrivateKey("asset-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(priv, loaded) {
		t.Fatal("loaded key differs from saved key")
	}
	info, err := os.Stat(filepath.Join(dir, "asset-1.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode = %v, want 0600", info.Mode().Perm())
	}

	if _, err := LoadPrivateKey("missing"); err == nil {
		t.Fatal("missing key must fail closed")
	}

	// Path traversal: a forged asset id must never escape the signing dir.
	for _, evil := range []string{"../evil", "a/b", "..", "x.y"} {
		if _, err := PrivateKeyPath(evil); err == nil {
			t.Fatalf("asset id %q must be rejected", evil)
		}
	}
}

func TestContentHashSeparator(t *testing.T) {
	if ContentHash("a", "bc") == ContentHash("ab", "c") {
		t.Fatal("NUL separator must prevent title/description boundary collisions")
	}
	if ContentHash("a", "b") != ContentHash("a", "b") {
		t.Fatal("content hash must be deterministic")
	}
}

func TestAttestationBuildAndHMAC(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	key := AttestationHMACKey(priv)
	in := Attestation{
		TaskID:        "task-1",
		IssueID:       "issue-1",
		Fingerprint:   strings.Repeat("a", 64),
		Signer:        "alice",
		Ops:           []string{OpOffensiveDrill},
		SignedAt:      "2026-10-04T00:00:00Z",
		ContentSHA256: strings.Repeat("b", 64),
		HMACKey:       key,
	}
	text, nonce, err := in.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, AttestationHeader) || !strings.Contains(text, AttestationFooter) {
		t.Fatal("attestation must carry its delimiters")
	}
	if !strings.Contains(text, "integrity: hmac-sha256:") {
		t.Fatal("HMAC key present → integrity line must be present")
	}
	if !VerifyAttestationHMAC(key, "task-1", nonce, in.Fingerprint, extractHexAfter(text, "integrity: hmac-sha256:")) {
		t.Fatal("integrity line must verify over (task, nonce, fingerprint)")
	}
	// Replay protection: same text does not verify for a different task.
	if VerifyAttestationHMAC(key, "task-2", nonce, in.Fingerprint, extractHexAfter(text, "integrity: hmac-sha256:")) {
		t.Fatal("attestation lifted from one run must not verify for another")
	}

	// Degraded mode (no key) still builds, without an integrity line.
	in.HMACKey = nil
	text2, _, err := in.Build()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text2, "UNAVAILABLE") {
		t.Fatal("missing HMAC key must be visible in the attestation text")
	}

	// Nonce varies per build.
	text3, nonce3, _ := in.Build()
	if text2 == text3 && nonce == nonce3 {
		t.Fatal("nonce must vary between builds")
	}
}

func TestAttestationHMACKeyDerivationStable(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	k1 := AttestationHMACKey(priv)
	k2 := AttestationHMACKey([]byte(priv))
	if !bytes.Equal(k1, k2) {
		t.Fatal("HMAC key derivation must be stable for the same private key")
	}
	_, priv2, _ := GenerateKeyPair()
	if bytes.Equal(k1, AttestationHMACKey(priv2)) {
		t.Fatal("different assets must derive different HMAC keys")
	}
	// The derivation must not expose the raw seed.
	if bytes.Contains(k1, priv.Seed()) {
		t.Fatal("derived HMAC key must not embed the raw Ed25519 seed")
	}
}

func extractHexAfter(s, marker string) string {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(marker):]
	end := strings.IndexAny(rest, "\n ")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

var _ = ed25519.PublicKey(nil)
