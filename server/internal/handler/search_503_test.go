package handler

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// TestIsSearchStatementTimeout verifies the canonical SQLSTATE 57014 mapping.
// Both pg-side statement_timeout and Go-side context cancellation surface as
// 57014 in pgx (the two are indistinguishable from the client side), so we
// accept either path here. Anything else returns false.
func TestIsSearchStatementTimeout(t *testing.T) {
	if isSearchStatementTimeout(nil) {
		t.Fatal("nil error must not match")
	}
	if isSearchStatementTimeout(errors.New("plain error")) {
		t.Fatal("plain error must not match")
	}
	if !isSearchStatementTimeout(&pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}) {
		t.Fatal("SQLSTATE 57014 must match")
	}
	if isSearchStatementTimeout(&pgconn.PgError{Code: "42P01", Message: "undefined table"}) {
		t.Fatal("SQLSTATE 42P01 (undefined_table) must NOT match")
	}
	// A wrapped PgError must still match — errors.As should unwrap.
	wrapped := errWrap{inner: &pgconn.PgError{Code: "57014"}}
	if !isSearchStatementTimeout(wrapped) {
		t.Fatal("wrapped SQLSTATE 57014 must match via errors.As unwrap")
	}
}

// errWrap is the minimum surface errors.As needs to walk the chain. Tests
// use it to prove the helper unwraps non-pgconn error types before
// inspecting.
type errWrap struct{ inner error }

func (e errWrap) Error() string { return "wrapped: " + e.inner.Error() }
func (e errWrap) Unwrap() error { return e.inner }

// TestEffectiveSearchStatementTimeout covers the testability override hook:
// setting searchStatementTimeoutOverride replaces the default for the
// duration of the test, and reset is mandatory (defer the cleanup).
func TestEffectiveSearchStatementTimeout(t *testing.T) {
	prev := searchStatementTimeoutOverride
	t.Cleanup(func() { searchStatementTimeoutOverride = prev })

	if got := effectiveSearchStatementTimeout(); got != searchStatementTimeout {
		t.Fatalf("default = %v, want %v", got, searchStatementTimeout)
	}

	searchStatementTimeoutOverride = 1500 * 1500 * 1000 // intentionally non-default
	if got := effectiveSearchStatementTimeout(); got != searchStatementTimeoutOverride {
		t.Fatalf("override = %v, want %v", got, searchStatementTimeoutOverride)
	}

	searchStatementTimeoutOverride = 0 // 0 means "use default"
	if got := effectiveSearchStatementTimeout(); got != searchStatementTimeout {
		t.Fatalf("zero override should fall back to default; got %v", got)
	}
}

// TestSearchStatementTimeoutDefault guards against accidentally lowering the
// production timeout. The 5s default was chosen for self-hosted schemas
// without pg_bigm/pg_trgm indexes — upstream's 3s would be unsafe there.
// If this constant ever moves, the matching test in search_timeout_test.go
// must move with it.
func TestSearchStatementTimeoutDefault(t *testing.T) {
	if searchStatementTimeout != 5*1000*1000*1000 {
		t.Fatalf("searchStatementTimeout = %v ns, want 5s — self-hosted deployments without pg_bigm depend on this cap", searchStatementTimeout)
	}
}