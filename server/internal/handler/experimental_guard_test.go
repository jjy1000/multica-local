package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeExperimentalQuerier is a stand-in for the generated Queries struct
// that implements experimental.Querier. It lets us drive
// experimentalFlagEnabled through each layer of its resolution order
// without a live database.
type fakeExperimentalQuerier struct {
	// row is returned when err is nil.
	row db.ExperimentalPref
	// err, when non-nil, simulates a lookup miss (pgx.ErrNoRows) or a
	// transient DB failure — both must fall through to the catalog
	// default rather than surfacing the flag.
	err error
	// gotParams captures the last call so a test can assert the guard
	// forwards the caller identity + flag key unchanged.
	gotParams db.GetExperimentalPrefParams
}

func (f *fakeExperimentalQuerier) GetExperimentalPref(
	_ context.Context,
	arg db.GetExperimentalPrefParams,
) (db.ExperimentalPref, error) {
	f.gotParams = arg
	if f.err != nil {
		return db.ExperimentalPref{}, f.err
	}
	return f.row, nil
}

var _ experimental.Querier = (*fakeExperimentalQuerier)(nil)

// A syntactically valid UUID so parseUUID (util.MustParseUUID) does not
// panic. The value is arbitrary; the fake ignores it beyond capture.
const guardTestUserID = "11111111-1111-1111-1111-111111111111"

// guardFlagKey is any real catalog key. Every current flag has DefaultVal
// false, so the catalog-default branch resolves to false and the only
// way experimentalFlagEnabled can return true is an explicit stored
// preference — exactly the per-user opt-in path this guard restores.
const guardFlagKey = "pythia_oracle"

func TestExperimentalFlagEnabled(t *testing.T) {
	tests := []struct {
		name    string
		userID  string
		querier func() *fakeExperimentalQuerier
		want    bool
	}{
		{
			// No stored preference (the common case) must fall through
			// to the catalog default, which is off for every flag.
			name:   "no stored pref falls back to catalog default",
			userID: guardTestUserID,
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{err: pgx.ErrNoRows}
			},
			want: false,
		},
		{
			// Explicit per-user opt-in flips the flag on even though the
			// catalog default is off — this is the asymmetry the guard
			// exists to close.
			name:   "stored pref enabled returns true",
			userID: guardTestUserID,
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{
					row: db.ExperimentalPref{FlagKey: guardFlagKey, Enabled: true},
				}
			},
			want: true,
		},
		{
			// Explicit per-user opt-out stays off.
			name:   "stored pref disabled returns false",
			userID: guardTestUserID,
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{
					row: db.ExperimentalPref{FlagKey: guardFlagKey, Enabled: false},
				}
			},
			want: false,
		},
		{
			// A transient DB error must not surface the flag; it falls
			// through to the conservative catalog default.
			name:   "db error falls back to catalog default",
			userID: guardTestUserID,
			querier: func() *fakeExperimentalQuerier {
				return &fakeExperimentalQuerier{err: context.DeadlineExceeded}
			},
			want: false,
		},
		{
			// An unauthenticated caller (no X-User-ID) can never have a
			// stored preference, so the catalog default applies.
			name:    "empty user id uses catalog default",
			userID:  "",
			querier: func() *fakeExperimentalQuerier { return &fakeExperimentalQuerier{} },
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := tt.querier()
			got := experimentalFlagEnabled(context.Background(), q, tt.userID, guardFlagKey)
			if got != tt.want {
				t.Fatalf("experimentalFlagEnabled(%q) = %v, want %v", tt.userID, got, tt.want)
			}
			// When a user is present the guard must query with the
			// resolved key so per-flag preferences don't cross-wire.
			if tt.userID != "" && q.gotParams.FlagKey != guardFlagKey {
				t.Fatalf("querier called with flag %q, want %q", q.gotParams.FlagKey, guardFlagKey)
			}
		})
	}
}

// TestExperimentalFlagEnabledNilQuerier guards the boot path where the
// Handler has no Queries wired (defensive: the guard must not panic and
// must resolve to the catalog default).
func TestExperimentalFlagEnabledNilQuerier(t *testing.T) {
	if experimentalFlagEnabled(context.Background(), nil, guardTestUserID, guardFlagKey) {
		t.Fatalf("nil querier should resolve to catalog default (false)")
	}
}
