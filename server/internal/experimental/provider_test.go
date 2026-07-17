package experimental

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/featureflag"
)

// fakeQuerier is the minimum surface for testing UserPrefProvider. The
// map keyed by (userID, flagKey) lets each test set up exactly the row
// state it needs without a real DB.
type fakeQuerier struct {
	rows   map[string]bool // key = userID + "|" + flagKey
	getErr error
}

func (f *fakeQuerier) GetExperimentalPref(_ context.Context, arg db.GetExperimentalPrefParams) (db.ExperimentalPref, error) {
	if f.getErr != nil {
		return db.ExperimentalPref{}, f.getErr
	}
	enabled, ok := f.rows[arg.UserID.String()+"|"+arg.FlagKey]
	if !ok {
		return db.ExperimentalPref{}, pgx.ErrNoRows
	}
	return db.ExperimentalPref{
		UserID:  arg.UserID,
		FlagKey: arg.FlagKey,
		Enabled: enabled,
	}, nil
}

func TestUserPrefProvider_NoUserIDInContext(t *testing.T) {
	// Without an EvalContext carrying user_id, the provider cannot answer
	// per-user. It must return (zero, false) so the chain falls through
	// to the catalog default.
	p := NewUserPrefProvider(&fakeQuerier{rows: map[string]bool{}})
	d, found := p.Lookup(context.Background(), "chat_pin_ui")
	if found {
		t.Fatalf("expected found=false when no user_id in context, got %+v", d)
	}
}

func TestUserPrefProvider_NoRowFallsThrough(t *testing.T) {
	// User has not toggled this flag → no row → (zero, false). The chain
	// then provides the catalog default (false in our case).
	p := NewUserPrefProvider(&fakeQuerier{rows: map[string]bool{}})
	ctx := featureflag.WithEvalContext(context.Background(), featureflag.EvalContext{
		UserID: "11111111-1111-1111-1111-111111111111",
	})
	d, found := p.Lookup(ctx, "chat_pin_ui")
	if found {
		t.Fatalf("expected found=false when no row, got %+v", d)
	}
}

func TestUserPrefProvider_EnabledOverride(t *testing.T) {
	// User has explicitly enabled chat_pin_ui → Decision.Enabled=true,
	// Reason=ReasonStatic, Source="user_pref".
	userID := pgtype.UUID{}
	if err := userID.Scan("11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("scan user_id: %v", err)
	}
	p := NewUserPrefProvider(&fakeQuerier{rows: map[string]bool{
		userID.String() + "|chat_pin_ui": true,
	}})
	ctx := featureflag.WithEvalContext(context.Background(), featureflag.EvalContext{
		UserID: userID.String(),
	})
	d, found := p.Lookup(ctx, "chat_pin_ui")
	if !found {
		t.Fatalf("expected found=true when row exists, got %+v", d)
	}
	if !d.Enabled {
		t.Fatalf("expected Enabled=true, got %+v", d)
	}
	if d.Variant != "on" {
		t.Fatalf("expected Variant=on, got %q", d.Variant)
	}
	if d.Reason != featureflag.ReasonStatic {
		t.Fatalf("expected Reason=ReasonStatic, got %q", d.Reason)
	}
	if d.Source != "user_pref" {
		t.Fatalf("expected Source=user_pref, got %q", d.Source)
	}
}

func TestUserPrefProvider_DisabledOverride(t *testing.T) {
	// User has explicitly disabled a flag — the provider must surface the
	// "off" decision, not fall through to the default. This is the case
	// that makes the Labs UI feel responsive: toggling off takes effect
	// immediately even when the catalog default is true.
	userID := pgtype.UUID{}
	if err := userID.Scan("22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("scan user_id: %v", err)
	}
	p := NewUserPrefProvider(&fakeQuerier{rows: map[string]bool{
		userID.String() + "|chat_pin_ui": false,
	}})
	ctx := featureflag.WithEvalContext(context.Background(), featureflag.EvalContext{
		UserID: userID.String(),
	})
	d, found := p.Lookup(ctx, "chat_pin_ui")
	if !found {
		t.Fatalf("expected found=true when row exists, got %+v", d)
	}
	if d.Enabled {
		t.Fatalf("expected Enabled=false, got %+v", d)
	}
	if d.Variant != "off" {
		t.Fatalf("expected Variant=off, got %q", d.Variant)
	}
}

func TestUserPrefProvider_DBErrorReturnsErrorDecision(t *testing.T) {
	// A non-ErrNoRows error must surface as Reason=ReasonError with
	// found=true so the chain stops here and the Service logs the
	// failure. We never want to silently downgrade a stored preference
	// to the catalog default on a DB outage.
	userID := pgtype.UUID{}
	if err := userID.Scan("33333333-3333-3333-3333-333333333333"); err != nil {
		t.Fatalf("scan user_id: %v", err)
	}
	p := NewUserPrefProvider(&fakeQuerier{getErr: errors.New("simulated db failure")})
	ctx := featureflag.WithEvalContext(context.Background(), featureflag.EvalContext{
		UserID: userID.String(),
	})
	d, found := p.Lookup(ctx, "chat_pin_ui")
	if !found {
		t.Fatalf("expected found=true on error (so chain stops here), got %+v", d)
	}
	if d.Reason != featureflag.ReasonError {
		t.Fatalf("expected Reason=ReasonError, got %q", d.Reason)
	}
}

func TestUserPrefProvider_NilReceiver(t *testing.T) {
	// A nil *UserPrefProvider must not panic — chain providers can be
	// called when the wiring is partial (e.g. unit tests). This matches
	// the Service nil-safe contract.
	var p *UserPrefProvider
	d, found := p.Lookup(context.Background(), "chat_pin_ui")
	if found {
		t.Fatalf("expected nil receiver to miss, got %+v", d)
	}
}

func TestIsKnownKey(t *testing.T) {
	if !IsKnownKey("chat_pin_ui") {
		t.Fatal("chat_pin_ui should be a known key")
	}
	if IsKnownKey("nonexistent_flag") {
		t.Fatal("nonexistent_flag should not be a known key")
	}
}

func TestDefaultFor(t *testing.T) {
	// Catalog default for chat_pin_ui is explicitly false per the plan.
	// If a future commit flips it to true, update this assertion and the
	// plan in lockstep — the catalog default determines what users see
	// on first visit to the labs tab.
	if got := DefaultFor("chat_pin_ui"); got != false {
		t.Fatalf("expected chat_pin_ui default to be false, got %v", got)
	}
	if got := DefaultFor("nonexistent"); got != false {
		t.Fatalf("expected unknown key default to be false, got %v", got)
	}
}