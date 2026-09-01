package handler

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// failQueryDBTX delegates every query to the real pool except the ones whose
// SQL contains failOn, which fail with a transient-looking error. Fork note:
// upstream keeps this helper in claim_project_context_test.go, which this fork
// does not carry; it is inlined here so the fallback test is self-sufficient.
type failQueryDBTX struct {
	db.DBTX
	failOn string
	err    error
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

func (f failQueryDBTX) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, f.failOn) {
		return errRow{err: f.err}
	}
	return f.DBTX.QueryRow(ctx, sql, args...)
}

func (f failQueryDBTX) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if strings.Contains(sql, f.failOn) {
		return nil, f.err
	}
	return f.DBTX.Query(ctx, sql, args...)
}

func TestProjectTerminalIssueStatusKeysFallsBackToCanonicalKeys(t *testing.T) {
	// Minimal handler: projectTerminalIssueStatusKeys only touches Queries, and
	// copying the shared *Handler would duplicate its sync.RWMutex (vet-fatal).
	h := &Handler{Queries: db.New(failQueryDBTX{
		DBTX:   testPool,
		failOn: "SELECT key FROM issue_status",
		err:    errors.New("status catalog unavailable"),
	})}

	got := h.projectTerminalIssueStatusKeys(context.Background(), parseUUID(testWorkspaceID))
	want := []string{issuestatus.Done, issuestatus.Cancelled}
	if !slices.Equal(got, want) {
		t.Fatalf("project terminal keys = %v, want canonical fallback %v", got, want)
	}
}
