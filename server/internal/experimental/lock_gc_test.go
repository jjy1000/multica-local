package experimental

import (
	"context"
	"errors"
	"testing"
)

// fakeOrphanSweeper pins the SweepOrphanedExperimentalResources contract
// without a database: count passthrough, error wrapping, fail-fast.
type fakeOrphanSweeper struct {
	locks    int64
	vis      int64
	lockErr  error
	visErr   error
	lockCalls int
	visCalls  int
}

func (f *fakeOrphanSweeper) DeleteOrphanResourceLocks(ctx context.Context) (int64, error) {
	f.lockCalls++
	return f.locks, f.lockErr
}

func (f *fakeOrphanSweeper) DeleteOrphanResourceVisibilityRows(ctx context.Context) (int64, error) {
	f.visCalls++
	return f.vis, f.visErr
}

func TestSweepOrphanedExperimentalResources(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cases := []struct {
		name      string
		fake      fakeOrphanSweeper
		wantLocks int64
		wantVis   int64
		wantErr   bool
	}{
		{"both tables clean", fakeOrphanSweeper{}, 0, 0, false},
		{"counts pass through", fakeOrphanSweeper{locks: 3401, vis: 378}, 3401, 378, false},
		{"lock error aborts", fakeOrphanSweeper{lockErr: errors.New("boom")}, 0, 0, true},
		{"visibility error surfaces lock partial", fakeOrphanSweeper{locks: 5, visErr: errors.New("boom")}, 5, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := tc.fake
			locks, vis, err := SweepOrphanedExperimentalResources(ctx, &f)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if locks != tc.wantLocks || vis != tc.wantVis {
				t.Errorf("got (%d,%d), want (%d,%d)", locks, vis, tc.wantLocks, tc.wantVis)
			}
			// Fail-fast: a lock failure must not reach the visibility query.
			if tc.fake.lockErr != nil && f.visCalls != 0 {
				t.Errorf("visibility sweep ran despite lock failure")
			}
		})
	}
}
