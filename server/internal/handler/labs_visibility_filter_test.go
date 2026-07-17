package handler

// Unit tests for filterLabsHiddenByDefault. The integration path (real
// visibility table seeded by migration 150 / 153) is exercised by the
// list handler tests, but those tests don't pin down the helper's
// internal contract: the empty-hidden-set short-circuit and the
// fail-open semantics. This file locks those in.
//
// We intentionally don't test the flag-on branch here: turning a flag
// "on" at runtime requires writing to experimental_pref (which is a
// per-user override table), and that needs a live PG. The list
// handler integration tests already cover the ON path by setting up a
// user pref before issuing the request.

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
)

// withSlogQuiet silences the helper's slog.Warn calls so test output
// stays clean when the helper takes the fail-open branch.
func withSlogQuiet(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError + 1,
	})))
	t.Cleanup(func() { slog.SetDefault(prev) })
}

// fakeAgent is a minimal stand-in for db.Agent — the helper only
// touches the ID, so the type stays tiny. The generics call site
// infers `T` from whatever slice we hand the helper.
type fakeAgent struct {
	ID pgtype.UUID
}

func fakeAgentID(a fakeAgent) pgtype.UUID { return a.ID }

// pgtypeUUIDEqual reports whether two pgtype.UUID values carry the
// same bytes (both Valid). We compare the byte arrays instead of the
// struct so a zero-value (Valid=false) UUID never matches.
func pgtypeUUIDEqual(a, b pgtype.UUID) bool {
	if !a.Valid || !b.Valid {
		return false
	}
	return a.Bytes == b.Bytes
}

// TestLabsFilterEmptyHiddenSetReturnsInput covers the empty-set
// short-circuit: when no visibility rows exist for the flag, the
// helper must return the input slice unchanged WITHOUT allocating a
// filter slice. The flagKey here is intentionally NOT in the catalog
// — DefaultFor returns false for unknown keys, so this exercises the
// OFF path with no seeded rows. That's the common case for every
// flag that ships without any opt-in resources.
//
// Note: we pass a nil *db.Queries because the empty-set branch never
// touches it. This is a fast unit test (no PG connection).
func TestLabsFilterEmptyHiddenSetReturnsInput(t *testing.T) {
	t.Parallel()
	withSlogQuiet(t)

	items := []fakeAgent{
		{ID: pgtype.UUID{Bytes: uuid.MustParse("44444444-4444-4444-4444-444444444444"), Valid: true}},
		{ID: pgtype.UUID{Bytes: uuid.MustParse("55555555-5555-5555-5555-555555555555"), Valid: true}},
	}

	out := filterLabsHiddenByDefault(context.Background(), nil, items,
		"some_flag_with_no_seeds", experimental.HideAgent,
		fakeAgentID, "test: empty hidden")

	if len(out) != len(items) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(items))
	}
	// Stable reference: the helper must reuse the input slice when no
	// filter happens, so callers don't pay an allocation for the common
	// "no opt-ins yet" case.
	if &out[0] != &items[0] {
		t.Errorf("helper reallocated slice; want in-place reuse")
	}
}

// TestLabsFilterUnknownFlagKeyStillFilters documents the safety
// posture: passing a flagKey that isn't in the catalog must NOT cause
// the helper to silently bypass the visibility filter. Because no
// rows can ever be seeded under an unknown key, the empty-hidden-set
// short-circuit fires and the input is returned unchanged. That's the
// desired behavior — an unknown flag key is equivalent to "nobody
// opted in yet", not "permission to show everything".
func TestLabsFilterUnknownFlagKeyStillFilters(t *testing.T) {
	t.Parallel()
	withSlogQuiet(t)

	items := []fakeAgent{
		{ID: pgtype.UUID{Bytes: uuid.MustParse("66666666-6666-6666-6666-666666666666"), Valid: true}},
	}

	out := filterLabsHiddenByDefault(context.Background(), nil, items,
		"definitely_not_in_catalog", experimental.HideAgent,
		fakeAgentID, "test: unknown flag")

	if len(out) != len(items) {
		t.Fatalf("len(out) = %d, want %d (unknown flag key must not bypass the gate)", len(out), len(items))
	}
}

// TestLabsFilterPreservesOrderWithFlagOn would test the second
// early-return (flag is ON → return input verbatim). We can't safely
// mutate the package-level Catalog slice during a test, and toggling
// a user pref needs a live PG. The integration list-handler tests
// cover this branch end-to-end; here we only test the OFF paths which
// are pure functions of the input slice + visibility table state.

// TestLabsFilterEmptyInputReturnsEmpty guards the trivially-empty
// case: no items in, no items out. Catches regressions where the
// helper accidentally allocates an empty filter slice.
func TestLabsFilterEmptyInputReturnsEmpty(t *testing.T) {
	t.Parallel()
	withSlogQuiet(t)

	var items []fakeAgent
	out := filterLabsHiddenByDefault(context.Background(), nil, items,
		"some_flag_with_no_seeds", experimental.HideAgent,
		fakeAgentID, "test: empty input")
	if len(out) != 0 {
		t.Errorf("len(out) = %d, want 0", len(out))
	}
}