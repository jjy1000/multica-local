package agent_self_optimization

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakePrefQuerier implements PrefQuerier. 0.5.5.1: kept for
// compile-time symmetry — the production code no longer reads from
// it, but the surface is still in `flag.go` so a future caller does
// not get a type-resolution error.
type fakePrefQuerier struct {
	enabled bool
}

func (f *fakePrefQuerier) GetExperimentalPrefEnabled(_ context.Context, _ db.GetExperimentalPrefEnabledParams) (bool, error) {
	return f.enabled, nil
}

// compile-time check that the test fake satisfies PrefQuerier.
var _ PrefQuerier = (*fakePrefQuerier)(nil)

// TestFlagOnForUser is the 0.5.5.1 stub contract. The previous
// implementation read `experimental_pref` and gated the self-opt
// scheduler on a per-user opt-in row. 0.5.5.1 promotes the flag to
// product-level (catalog.DefaultVal=true) and the gate is removed:
// every call returns `true` regardless of the underlying querier
// state. The user-facing control point moved to the autopilot row's
// own `enabled` field.
//
// The test below pins the stub so a future refactor cannot silently
// re-introduce the per-user gate (a known regression class per
// 0.3.45.2 / 0.3.46 audit).
func TestFlagOnForUser(t *testing.T) {
	ctx := context.Background()
	var uuidVal pgtype.UUID
	uuidVal.Scan("3f2d577f-03cf-451c-99ff-ceaf5fef1ef3")

	cases := []struct {
		name    string
		querier *fakePrefQuerier
		uid     pgtype.UUID
	}{
		{"zero UUID still returns true (stub)", &fakePrefQuerier{enabled: false}, pgtype.UUID{}},
		{"any querier state returns true", &fakePrefQuerier{enabled: false}, uuidVal},
		{"enabled=false querier still returns true", &fakePrefQuerier{enabled: false}, uuidVal},
		{"enabled=true querier returns true", &fakePrefQuerier{enabled: true}, uuidVal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !flagOnForUser(ctx, tc.querier, tc.uid) {
				t.Fatalf("flagOnForUser(%s) = false; want true (0.5.5.1 stub contract)", tc.name)
			}
		})
	}
}

// TestFlagOnExperimental is the 0.5.5.1 catalog-side stub contract.
// 0.3.45.1 read the catalog default; 0.5.5.1 always returns true
// because the catalog default itself is now `true` and the value
// is no longer consulted.
func TestFlagOnExperimental(t *testing.T) {
	if !flagOnExperimental() {
		t.Fatalf("flagOnExperimental() = false; want true (0.5.5.1 stub contract)")
	}
}
