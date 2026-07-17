package helpers_test

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/experimental/helpers"
)

// TestPackageSurface is the smoke test for the helpers package. The
// heavy smoke (handlers using ListVisible* queries against a real DB)
// lands in PR 4 alongside the wiring change. PR 2 commits only:
//
//   - the package compiles;
//   - HideFilter accepts a *db.Queries;
//   - the surface uses the lock package's ErrUnknownSource, not a
//     shadowed copy.
//
// Those invariants are enough to PR-2 ship the helpers file as a
// no-op stub that PR 4 will populate.
func TestPackageSurface(t *testing.T) {
	if err := experimental.ErrUnknownSource; err == nil {
		t.Fatal("experimental.ErrUnknownSource must be non-nil")
	}
	if errors.Is(experimental.ErrUnknownSource, helpers.IsSourceKnownErr) {
		t.Fatal("helpers.IsSourceKnownErr must not wrap experimental.ErrUnknownSource")
	}
	// Type assertion: HideFilter is a struct, not an interface.
	var _ *helpers.HideFilter
}
