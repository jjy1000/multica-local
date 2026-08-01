package agent_self_optimization

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakePrefQuerier implements PrefQuerier for flagOnForUser tests.
// The err field takes precedence over enabled when set.
type fakePrefQuerier struct {
	enabled bool
	err     error
}

func (f *fakePrefQuerier) GetExperimentalPrefEnabled(ctx context.Context, arg db.GetExperimentalPrefEnabledParams) (bool, error) {
	return f.enabled, f.err
}

// compile-time check that the test fake satisfies PrefQuerier.
var _ PrefQuerier = (*fakePrefQuerier)(nil)

// TestFlagOnForUser covers the five branches of flagOnForUser:
//  1. zero-value UUID → false (defensive guard)
//  2. no pref row (ErrNoRows) → false (not opted in)
//  3. enabled=true → true
//  4. enabled=false → false
//  5. DB error → false (never silently enable on transient failure)
func TestFlagOnForUser(t *testing.T) {
	ctx := context.Background()
	var uuidVal pgtype.UUID
	uuidVal.Scan("3f2d577f-03cf-451c-99ff-ceaf5fef1ef3")

	t.Run("zero UUID returns false", func(t *testing.T) {
		got := flagOnForUser(ctx, &fakePrefQuerier{enabled: true}, pgtype.UUID{})
		if got {
			t.Fatalf("flagOnForUser(zero) = true; want false")
		}
	})

	t.Run("ErrNoRows returns false", func(t *testing.T) {
		got := flagOnForUser(ctx, &fakePrefQuerier{err: pgx.ErrNoRows}, uuidVal)
		if got {
			t.Fatalf("flagOnForUser(NoRows) = true; want false")
		}
	})

	t.Run("enabled=true returns true", func(t *testing.T) {
		got := flagOnForUser(ctx, &fakePrefQuerier{enabled: true}, uuidVal)
		if !got {
			t.Fatalf("flagOnForUser(enabled=true) = false; want true")
		}
	})

	t.Run("enabled=false returns false", func(t *testing.T) {
		got := flagOnForUser(ctx, &fakePrefQuerier{enabled: false}, uuidVal)
		if got {
			t.Fatalf("flagOnForUser(enabled=false) = true; want false")
		}
	})

	t.Run("DB error returns false (no silent fallback to catalog default)", func(t *testing.T) {
		got := flagOnForUser(ctx, &fakePrefQuerier{enabled: true, err: errors.New("simulated db outage")}, uuidVal)
		if got {
			t.Fatalf("flagOnForUser(dbErr, enabled=true) = true; want false")
		}
	})
}
