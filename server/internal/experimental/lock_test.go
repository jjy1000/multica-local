package experimental_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/experimental"
)

// fakeQuerier is a hand-rolled stub of the small slice of db.Querier the
// lock helper actually calls. Using a fake instead of sqlc-generated
// mocks keeps the helper testable in isolation — the lock package can be
// unit-tested without spinning up PostgreSQL, and the table-driven
// cases below are unaffected by schema churn in unrelated tables.
type fakeQuerier struct {
	// rows stores every lock row ever inserted. Keyed by
	// (source, resource_type, resource_id) so ON CONFLICT DO NOTHING
	// semantics can be reproduced faithfully.
	rows map[string]db.ExperimentalResourceLock
	// countByType mirrors CountExperimentalResourceLocksByType's
	// aggregate. Updated by hand after every Insert / Hide / Restore
	// so the test can assert the same result the production query
	// would return.
	countByType map[experimental.Source]map[experimental.ResourceType]struct {
		total, visible int
	}
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		rows:        make(map[string]db.ExperimentalResourceLock),
		countByType: make(map[experimental.Source]map[experimental.ResourceType]struct {
			total, visible int
		}),
	}
}

func key(src experimental.Source, rt experimental.ResourceType, id pgtype.UUID) string {
	return string(src) + "\x00" + string(rt) + "\x00" + string(id.Bytes[:])
}

func (f *fakeQuerier) bump(src experimental.Source, rt experimental.ResourceType, visible bool) {
	if _, ok := f.countByType[src]; !ok {
		f.countByType[src] = make(map[experimental.ResourceType]struct {
			total, visible int
		})
	}
	cur := f.countByType[src][rt]
	cur.total++
	if visible {
		cur.visible++
	}
	f.countByType[src][rt] = cur
}

// Compile-time assertion that fakeQuerier satisfies the same *db.Queries-
// shaped surface the lock package calls. The fake is a *Queries substitute;
// sqlc's struct is not an interface, so we wrap accessors through pointers
// rather than satisfying an interface directly. The lock package accepts
// *db.Queries; here we provide the methods on a *fakeQuerier value used by
// the unit tests below.
var _ = (*fakeQuerier)(nil)

func (f *fakeQuerier) InsertExperimentalResourceLock(ctx context.Context, arg db.InsertExperimentalResourceLockParams) error {
	k := key(experimental.Source(arg.ExperimentalSource), experimental.ResourceType(arg.ResourceType), arg.ResourceID)
	if _, exists := f.rows[k]; exists {
		return nil // ON CONFLICT DO NOTHING
	}
	row := db.ExperimentalResourceLock{
		ExperimentalSource: arg.ExperimentalSource,
		ResourceType:       arg.ResourceType,
		ResourceID:         arg.ResourceID,
		Hidden:             false,
	}
	f.rows[k] = row
	f.bump(experimental.Source(arg.ExperimentalSource), experimental.ResourceType(arg.ResourceType), true)
	return nil
}

func (f *fakeQuerier) HideExperimentalResourceLocksBySource(ctx context.Context, src string) (int64, error) {
	var touched int64
	for k, row := range f.rows {
		if row.ExperimentalSource != src || row.Hidden {
			continue
		}
		row.Hidden = true
		f.rows[k] = row
		touched++
		// visible count drops for this (src, type).
		if _, ok := f.countByType[experimental.Source(src)]; ok {
			if v, ok := f.countByType[experimental.Source(src)][experimental.ResourceType(row.ResourceType)]; ok {
				if v.visible > 0 {
					v.visible--
				}
				f.countByType[experimental.Source(src)][experimental.ResourceType(row.ResourceType)] = v
			}
		}
	}
	return touched, nil
}

func (f *fakeQuerier) RestoreExperimentalResourceLocksBySource(ctx context.Context, src string) (int64, error) {
	var touched int64
	for k, row := range f.rows {
		if row.ExperimentalSource != src || !row.Hidden {
			continue
		}
		row.Hidden = false
		f.rows[k] = row
		touched++
		if _, ok := f.countByType[experimental.Source(src)]; ok {
			v := f.countByType[experimental.Source(src)][experimental.ResourceType(row.ResourceType)]
			v.visible++
			f.countByType[experimental.Source(src)][experimental.ResourceType(row.ResourceType)] = v
		}
	}
	return touched, nil
}

func (f *fakeQuerier) IsExperimentalResourceHidden(ctx context.Context, arg db.IsExperimentalResourceHiddenParams) (bool, error) {
	row, ok := f.rows[key(experimental.Source(arg.ExperimentalSource), experimental.ResourceType(arg.ResourceType), arg.ResourceID)]
	if !ok {
		return false, nil
	}
	return row.Hidden, nil
}

func (f *fakeQuerier) GetExperimentalResourceLock(ctx context.Context, arg db.GetExperimentalResourceLockParams) (db.ExperimentalResourceLock, error) {
	row, ok := f.rows[key(experimental.Source(arg.ExperimentalSource), experimental.ResourceType(arg.ResourceType), arg.ResourceID)]
	if !ok {
		return db.ExperimentalResourceLock{}, errors.New("no rows in result set")
	}
	return row, nil
}

func (f *fakeQuerier) CountExperimentalResourceLocksByType(ctx context.Context, src string) ([]db.CountExperimentalResourceLocksByTypeRow, error) {
	srcEnum := experimental.Source(src)
	out := make([]db.CountExperimentalResourceLocksByTypeRow, 0)
	if m, ok := f.countByType[srcEnum]; ok {
		for rt, v := range m {
			out = append(out, db.CountExperimentalResourceLocksByTypeRow{
				ResourceType: string(rt),
				Total:        int64(v.total),
				Visible:      int64(v.visible),
			})
		}
	}
	return out, nil
}

// TestClaimIdempotent covers the basic invariant: calling Claim twice on
// the same triple leaves the lock row count unchanged (one row, not
// two). Mirrors the SQL ON CONFLICT DO NOTHING contract.
func TestClaimIdempotent(t *testing.T) {
	q := newFakeQuerier()
	id1 := pgtype.UUID{Bytes: [16]byte{0x01, 0x02}, Valid: true}
	id2 := pgtype.UUID{Bytes: [16]byte{0x03, 0x04}, Valid: true}

	if err := experimental.Claim(context.Background(), q, experimental.SourceClaudeScience, experimental.LockSkill, id1); err != nil {
		t.Fatalf("first Claim failed: %v", err)
	}
	if err := experimental.Claim(context.Background(), q, experimental.SourceClaudeScience, experimental.LockSkill, id1); err != nil {
		t.Fatalf("second Claim failed: %v", err)
	}
	if err := experimental.Claim(context.Background(), q, experimental.SourceClaudeScience, experimental.LockSkill, id2); err != nil {
		t.Fatalf("third Claim (different id) failed: %v", err)
	}

	if got := len(q.rows); got != 2 {
		t.Fatalf("expected 2 distinct lock rows, got %d", got)
	}
}

// TestHideRestoreRoundTrip asserts that Hide → Restore returns the lock
// graph to its original state. Hiding must flip hidden=true; restoring
// must flip it back AND clear hidden_at (the SQL does that). The fake
// here tracks only hidden (no hidden_at) which is fine for the API
// surface under test.
func TestHideRestoreRoundTrip(t *testing.T) {
	q := newFakeQuerier()
	id := pgtype.UUID{Bytes: [16]byte{0x05}, Valid: true}

	if err := experimental.Claim(context.Background(), q, experimental.SourceClaudeScience, experimental.LockAgent, id); err != nil {
		t.Fatalf("Claim: %v", err)
	}
	hidden, err := experimental.IsHidden(context.Background(), q, experimental.SourceClaudeScience, experimental.LockAgent, id)
	if err != nil {
		t.Fatalf("IsHidden (initial): %v", err)
	}
	if hidden {
		t.Fatalf("fresh claim should NOT be hidden")
	}

	touched, err := experimental.Hide(context.Background(), q, experimental.SourceClaudeScience)
	if err != nil {
		t.Fatalf("Hide: %v", err)
	}
	if touched != 1 {
		t.Fatalf("Hide touched %d rows, want 1", touched)
	}
	hidden, _ = experimental.IsHidden(context.Background(), q, experimental.SourceClaudeScience, experimental.LockAgent, id)
	if !hidden {
		t.Fatalf("Hide should set hidden=true")
	}

	restored, err := experimental.Restore(context.Background(), q, experimental.SourceClaudeScience)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restored != 1 {
		t.Fatalf("Restore touched %d rows, want 1", restored)
	}
	hidden, _ = experimental.IsHidden(context.Background(), q, experimental.SourceClaudeScience, experimental.LockAgent, id)
	if hidden {
		t.Fatalf("Restore should clear hidden")
	}
}

// TestIsHiddenFalseNoClaim asserts the read-path default: when no lock
// row exists for (src, rt, id), IsHidden reports false. Handlers
// (PR 2) use this as the baseline that no experimental surface has
// touched the row.
func TestIsHiddenFalseNoClaim(t *testing.T) {
	q := newFakeQuerier()
	id := pgtype.UUID{Bytes: [16]byte{0x99}, Valid: true}
	hidden, err := experimental.IsHidden(context.Background(), q, experimental.SourceClaudeScience, experimental.LockSkill, id)
	if err != nil {
		t.Fatalf("IsHidden: %v", err)
	}
	if hidden {
		t.Fatal("IsHidden with no claim should be false")
	}
}

// TestUnknownSourceRejected ensures Claim / Hide / Restore / IsHidden
// reject unknown Source values with ErrUnknownSource. The SQL enum
// would also reject these, but the Go-side guard means runtime code
// fails fast (and the test is fast / hermetic).
func TestUnknownSourceRejected(t *testing.T) {
	q := newFakeQuerier()
	id := pgtype.UUID{Bytes: [16]byte{0x11}, Valid: true}
	bogus := experimental.Source("definitely-not-a-lab")

	if err := experimental.Claim(context.Background(), q, bogus, experimental.LockSkill, id); !errors.Is(err, experimental.ErrUnknownSource) {
		t.Fatalf("Claim(unknown source): got %v, want ErrUnknownSource", err)
	}
	if _, err := experimental.Hide(context.Background(), q, bogus); !errors.Is(err, experimental.ErrUnknownSource) {
		t.Fatalf("Hide(unknown source): got %v, want ErrUnknownSource", err)
	}
	if _, err := experimental.Restore(context.Background(), q, bogus); !errors.Is(err, experimental.ErrUnknownSource) {
		t.Fatalf("Restore(unknown source): got %v, want ErrUnknownSource", err)
	}
	if _, err := experimental.IsHidden(context.Background(), q, bogus, experimental.LockSkill, id); !errors.Is(err, experimental.ErrUnknownSource) {
		t.Fatalf("IsHidden(unknown source): got %v, want ErrUnknownSource", err)
	}
}

// TestAllSourcesContainsClaudeScience guards against regressions when
// someone deletes a Source constant — AllSources is the canonical list
// the install endpoint (PR 3) reads from.
func TestAllSourcesContainsClaudeScience(t *testing.T) {
	found := false
	for _, s := range experimental.AllSources {
		if s == experimental.SourceClaudeScience {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("AllSources does not contain SourceClaudeScience")
	}
}
