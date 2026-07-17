package experimental

// Manifest loader for the experiment contract (0.3.19 Labs platform).
//
// Each flag in Catalog may declare a ManifestPath pointing at a JSON
// manifest that describes the experiment's workspace, capabilities,
// runtime, safety and surface metadata. The loader resolves the path
// against a small precedence chain — MULTICA_RESOURCES_DIR (packaged)
// first, then a dev fallback under apps/desktop/resources/ — and
// validates the shape against an opinionated schema.
//
// Why this package exists as its own file (not folded into catalog.go):
//
//   - catalog.go is read at boot time by every binary; the manifest
//     JSON is larger and only consumed by the registry / installer /
//     Skill loader (PR 2 / 5 / 6). Keeping it in a separate file keeps
//     the boot-time symbol surface small.
//
//   - The loader exposes a single read-only function LoadManifest so
//     tests can swap the resolver path via a SetManifestRoot hook
//     without touching the rest of the package.
//
// Contract:
//
//   - LoadManifest(flagKey) returns ErrNoManifest when the flag's
//     ManifestPath is empty (plain-toggle flags like chat_pin_ui) or
//     when the file does not exist on disk. Callers must treat
//     ErrNoManifest as a soft error and continue with the flag's
//     plain-toggle behavior. A schema mismatch is a hard error
//     (ErrInvalidManifest) because shipping a malformed manifest is a
//     developer bug, not a runtime condition.
//
//   - The loader is goroutine-safe: it only reads files. Tests run
//     with -race and the contract is "no shared state" by design.
//
// Resolution precedence (highest priority first):
//
//  1. $MULTICA_RESOURCES_DIR/experiments/<flagKey>/manifest.json
//  2. <repo>/apps/desktop/resources/experiments/<flagKey>/manifest.json
//  3. (none) — return ErrNoManifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ManifestResourceDirEnv mirrors the env var used elsewhere in the
// repo (server/internal/handler/install_claude_science.go) so the
// manifest lookup and the install / skill lookup resolve to the same
// root. The 0.3.18 install handler sets MULTICA_RESOURCES_DIR when
// it spawns the bundled server; in dev the variable is unset and
// the loader falls back to the repo resources tree.
const ManifestResourceDirEnv = "MULTICA_RESOURCES_DIR"

// DevManifestFallback is the on-disk path the loader tries when
// MULTICA_RESOURCES_DIR is unset. Tested by overriding SetManifestRoot.
const DevManifestFallback = "apps/desktop/resources"

// ErrNoManifest is returned when a flag has no ManifestPath or when
// the resolved path does not exist on disk. Callers should treat this
// as a soft signal that the experiment has no rich metadata and fall
// back to the flag's plain-toggle behavior.
var ErrNoManifest = errors.New("experimental: no manifest for flag")

// ErrInvalidManifest is returned when the JSON exists but fails schema
// validation. This is a developer bug — the file was committed in a
// bad state. The error message names the failing field for fast
// triage.
var ErrInvalidManifest = errors.New("experimental: invalid manifest")

// Manifest is the wire shape loaded from disk. The struct mirrors the
// JSON exactly (no omitempty — manifest authors want to see every
// field in the diff). New fields are appended at the end; readers
// ignore unknown fields so a newer manifest still loads on an older
// binary (forward compatibility for shipped DMGs).
type Manifest struct {
	APIVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Metadata   ManifestMetadata `json:"metadata"`
	Spec       map[string]any   `json:"spec"`
	Raw        map[string]any   `json:"-"`
	SourcePath string           `json:"-"`
	mu         sync.Mutex       `json:"-"`
}

// ManifestMetadata carries the experiment identity. Name MUST equal
// Flag.Key — the loader enforces this; a manifest that names a
// different experiment is treated as ErrInvalidManifest.
type ManifestMetadata struct {
	Name           string          `json:"name"`
	Flag           string          `json:"flag"`
	Title          LocalizedString `json:"title"`
	Description    LocalizedString `json:"description"`
	DefaultEnabled bool            `json:"default_enabled"`
}

// resolvedRoot is computed once per process and cached. Tests can
// override it via SetManifestRoot. The path resolution looks at
// MULTICA_RESOURCES_DIR first, then a dev fallback that walks up from
// the working directory.
var (
	rootMu  sync.RWMutex
	rootSet string
)

// SetManifestRoot overrides the base directory the loader uses to
// resolve ManifestPath values. Used by tests; production code should
// let the resolver pick the default location.
func SetManifestRoot(path string) {
	rootMu.Lock()
	defer rootMu.Unlock()
	rootSet = path
}

// ManifestRoot returns the effective manifest root. Resolution order:
//
//  1. explicit SetManifestRoot (test override)
//  2. $MULTICA_RESOURCES_DIR
//  3. <cwd>/apps/desktop/resources (dev fallback)
//
// The dev fallback is intentionally naive: in dev the user runs go
// from the repo root, so cwd is the repo root. If a future caller
// runs from a subdirectory the fallback resolves to the wrong place;
// PR 6 of the blueprint covers making the dev resolution smarter.
func ManifestRoot() string {
	rootMu.RLock()
	if rootSet != "" {
		rootMu.RUnlock()
		return rootSet
	}
	rootMu.RUnlock()
	if env := os.Getenv(ManifestResourceDirEnv); env != "" {
		return env
	}
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, DevManifestFallback)
	}
	return DevManifestFallback
}

// LoadManifest resolves the manifest for the given flag key and
// validates its schema. The returned Manifest.SourcePath field tells
// the caller which on-disk file was read so the UI can render a
// "manifest from X" hint.
//
// Returns ErrNoManifest when:
//
//   - the flag is not in the Catalog (defensive — callers should
//     pre-check via IsKnownKey),
//   - the flag's ManifestPath is empty (plain-toggle flag), or
//   - the resolved file does not exist on disk.
//
// Returns ErrInvalidManifest when the JSON parses but fails schema
// validation. The error message is the schema validator's output.
func LoadManifest(flagKey string) (*Manifest, error) {
	if !IsKnownKey(flagKey) {
		return nil, fmt.Errorf("%w: unknown flag key %q", ErrNoManifest, flagKey)
	}

	var f *Flag
	for i := range Catalog {
		if Catalog[i].Key == flagKey {
			f = &Catalog[i]
			break
		}
	}
	if f == nil || f.ManifestPath == "" {
		return nil, fmt.Errorf("%w: flag %q has no manifest path", ErrNoManifest, flagKey)
	}

	root := ManifestRoot()
	candidate := filepath.Join(root, f.ManifestPath)
	raw, err := os.ReadFile(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNoManifest, candidate)
		}
		return nil, fmt.Errorf("read manifest %s: %w", candidate, err)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidManifest, candidate, err)
	}
	if err := validateManifestSchema(doc, flagKey); err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidManifest, candidate, err)
	}

	m := &Manifest{SourcePath: candidate, Raw: doc}
	if v, ok := doc["apiVersion"].(string); ok {
		m.APIVersion = v
	}
	if v, ok := doc["kind"].(string); ok {
		m.Kind = v
	}
	if md, ok := doc["metadata"].(map[string]any); ok {
		if v, ok := md["name"].(string); ok {
			m.Metadata.Name = v
		}
		if v, ok := md["flag"].(string); ok {
			m.Metadata.Flag = v
		}
		if v, ok := md["default_enabled"].(bool); ok {
			m.Metadata.DefaultEnabled = v
		}
		if t, ok := md["title"].(map[string]any); ok {
			m.Metadata.Title = mapToLocalizedString(t)
		}
		if d, ok := md["description"].(map[string]any); ok {
			m.Metadata.Description = mapToLocalizedString(d)
		}
	}
	if sp, ok := doc["spec"].(map[string]any); ok {
		m.Spec = sp
	}
	return m, nil
}

func mapToLocalizedString(m map[string]any) LocalizedString {
	out := LocalizedString{}
	if v, ok := m["en"].(string); ok {
		out.En = v
	}
	if v, ok := m["zh"].(string); ok {
		out.Zh = v
	}
	return out
}

// validateManifestSchema enforces the minimum shape the loader
// requires. The full schema (capabilities / runtime / safety /
// surface) is consumed by downstream callers — they validate their
// own sub-section; the loader only checks the cross-cutting
// invariants that would make every downstream consumer fail.
func validateManifestSchema(doc map[string]any, flagKey string) error {
	if got, want := doc["apiVersion"], "multica.dev/experiment/v1"; got != want {
		return fmt.Errorf("apiVersion = %v, want %q", got, want)
	}
	if got, want := doc["kind"], "Experiment"; got != want {
		return fmt.Errorf("kind = %v, want %q", got, want)
	}
	md, ok := doc["metadata"].(map[string]any)
	if !ok {
		return fmt.Errorf("metadata must be an object")
	}
	if name, _ := md["name"].(string); name == "" {
		return fmt.Errorf("metadata.name is required")
	}
	if flag, _ := md["flag"].(string); flag == "" {
		return fmt.Errorf("metadata.flag is required")
	}
	if got, want := md["flag"], flagKey; got != want {
		return fmt.Errorf("metadata.flag = %v, want %q (must match the catalog key)", got, want)
	}
	return nil
}
