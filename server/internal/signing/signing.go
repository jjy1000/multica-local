// Package signing implements the cryptographic layer of the signature
// authorization feature: per-asset Ed25519 keypairs for watermark
// signatures, canonical-scope fingerprinting, and the run-scoped
// attestation block injected into agent briefs.
//
// Threat model (see .omc/design/signature-authorization-design.md): the
// attestation TEXT is always copyable — prompt-injected issue/comment
// content can imitate its layout verbatim. What it cannot imitate is
// (a) the Ed25519 signature over the canonical scope, verified server-side
// at every consumption point, and (b) the attestation HMAC, recomputable
// only where the private key lives (~/.multica/signing/). Text is the
// declaration; crypto is the lock.
package signing

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Algorithm is the only signature algorithm v1 ships. The DB CHECK pins the
// same value; adding one is a migration + this constant.
const Algorithm = "ed25519"

// Privileged-operation categories a signature can cover. Phase 1 ships the
// vocabulary as data (no gate consumes it yet); Phase 2's
// requireSignatureCoverage matches these strings literally, so renaming one
// is a breaking change to already-stored scopes.
const (
	OpOffensiveDrill = "offensive_drill"
	OpCreateAgent    = "create_agent"
	OpCreateSquad    = "create_squad"
	OpCreateSkill    = "create_skill"
	OpInstallPlugin  = "install_plugin"
	OpLabDelegate    = "lab_delegate"
)

// KnownOps is the full op vocabulary. Signing requests are validated
// against it so a typo'd op can never silently widen or narrow a scope.
var KnownOps = []string{
	OpOffensiveDrill,
	OpCreateAgent,
	OpCreateSquad,
	OpCreateSkill,
	OpInstallPlugin,
	OpLabDelegate,
}

func knownOp(op string) bool {
	for _, k := range KnownOps {
		if k == op {
			return true
		}
	}
	return false
}

// NormalizeOps validates, de-duplicates, and sorts an op list so two
// signings of the same authorization produce the same canonical scope —
// and therefore the same fingerprint — regardless of UI checkbox order.
// Unknown ops are rejected, not dropped: a scope that silently lost an op
// would fingerprint differently from what the signer believed they signed.
func NormalizeOps(ops []string) ([]string, error) {
	seen := make(map[string]bool, len(ops))
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		op = strings.TrimSpace(op)
		if op == "" {
			continue
		}
		if !knownOp(op) {
			return nil, fmt.Errorf("unknown signature op %q", op)
		}
		if !seen[op] {
			seen[op] = true
			out = append(out, op)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("signature scope requires at least one op")
	}
	sort.Strings(out)
	return out, nil
}

// Scope is the canonical authorization object. JSON field order is fixed
// by the struct, so the same authorization always marshals — and therefore
// fingerprints — identically. Postgres jsonb does NOT preserve key order,
// so verification MUST round-trip stored scopes through this struct (see
// Canonical) instead of hashing the stored bytes.
type Scope struct {
	IssueID       string   `json:"issue_id,omitempty"`
	IssueTitle    string   `json:"issue_title,omitempty"`
	ContentSHA256 string   `json:"content_sha256,omitempty"`
	Ops           []string `json:"ops"`
	SignedBy      string   `json:"signed_by"`
	ExpiresAt     string   `json:"expires_at,omitempty"` // RFC3339; empty = no expiry
}

// Canonical normalizes ops and marshals the scope deterministically.
func (s Scope) Canonical() ([]byte, error) {
	ops, err := NormalizeOps(s.Ops)
	if err != nil {
		return nil, err
	}
	s.Ops = ops
	return json.Marshal(s)
}

// FingerprintHash returns sha256(canonical scope) as raw bytes.
func (s Scope) FingerprintHash() ([]byte, error) {
	canonical, err := s.Canonical()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(canonical)
	return sum[:], nil
}

// Fingerprint returns the hex-encoded sha256(canonical scope). The display
// form adds the "sha256:" prefix; storage is bare hex.
func (s Scope) Fingerprint() (string, error) {
	hash, err := s.FingerprintHash()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(hash), nil
}

// FingerprintShort renders the first 8 hex chars grouped in pairs for
// compact UI badges ("9f3a c21e"). It is a display aid, never an identity.
func FingerprintShort(fingerprint string) string {
	clean := strings.TrimPrefix(fingerprint, "sha256:")
	if len(clean) < 8 {
		return clean
	}
	return clean[:4] + " " + clean[4:8]
}

// ContentHash hashes the issue snapshot a signature binds to. Title and
// description are joined with a NUL separator so ("a","bc") never collides
// with ("ab","c"). Changing this function invalidates nothing stored — it
// only affects future signings — but it MUST stay stable within a release.
func ContentHash(title, description string) string {
	h := sha256.New()
	h.Write([]byte(title))
	h.Write([]byte{0})
	h.Write([]byte(description))
	return hex.EncodeToString(h.Sum(nil))
}

// GenerateKeyPair creates the per-asset Ed25519 keypair.
func GenerateKeyPair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("signing: generate keypair: %w", err)
	}
	return pub, priv, nil
}

// Sign returns the hex-encoded Ed25519 signature over the fingerprint hash
// bytes (exactly what Scope.FingerprintHash produces).
func Sign(priv ed25519.PrivateKey, fingerprintHash []byte) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("signing: invalid private key size")
	}
	return hex.EncodeToString(ed25519.Sign(priv, fingerprintHash)), nil
}

// Verify checks a hex signature over the fingerprint hash bytes with the
// asset's public key.
func Verify(pub ed25519.PublicKey, fingerprintHash []byte, sigHex string) bool {
	if len(pub) != ed25519.PublicKeySize {
		return false
	}
	sig, err := hex.DecodeString(sigHex)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(pub, fingerprintHash, sig)
}

// Private key file management. Keys live under ~/.multica/signing/, one
// file per asset, raw 64-byte Ed25519 private keys, 0600. The directory is
// server-side state the same way profile configs are — never in the DB,
// never in a workdir.

// KeyDirOverride redirects private-key storage. Tests only — not
// synchronized, so tests that set it must not run in parallel with each
// other; production leaves it empty.
var KeyDirOverride string

func keyDir() (string, error) {
	if KeyDirOverride != "" {
		return KeyDirOverride, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("signing: resolve home dir: %w", err)
	}
	return filepath.Join(home, ".multica", "signing"), nil
}

// PrivateKeyPath returns ~/.multica/signing/<assetID>.key. The asset ID is
// validated to be a bare UUID so a forged ID can never traverse out of the
// signing directory.
func PrivateKeyPath(assetID string) (string, error) {
	if strings.ContainsAny(assetID, "/\\.") || strings.TrimSpace(assetID) == "" || len(assetID) > 64 {
		return "", fmt.Errorf("signing: invalid asset id %q", assetID)
	}
	dir, err := keyDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, assetID+".key"), nil
}

// SavePrivateKey writes the key atomically (temp + rename) with 0600.
func SavePrivateKey(assetID string, priv ed25519.PrivateKey) error {
	path, err := PrivateKeyPath(assetID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("signing: create key dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, priv, 0o600); err != nil {
		return fmt.Errorf("signing: write key tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("signing: rename key into place: %w", err)
	}
	return nil
}

// ErrKeyNotFound is returned by LoadPrivateKey when the asset's key file is
// absent on this machine (fresh checkout, migrated host). Signing fails
// closed; verification still works off the DB public key.
var ErrKeyNotFound = errors.New("signing: private key not found on this machine")

func LoadPrivateKey(assetID string) (ed25519.PrivateKey, error) {
	path, err := PrivateKeyPath(assetID)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrKeyNotFound
		}
		return nil, fmt.Errorf("signing: read key: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("signing: key file %s has %d bytes, want %d", path, len(raw), ed25519.PrivateKeySize)
	}
	return ed25519.PrivateKey(raw), nil
}
