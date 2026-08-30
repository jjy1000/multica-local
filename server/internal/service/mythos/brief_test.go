package mythos

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// 0.5.90 OpenMythos claim-brief builder tests. DB-less, mirroring the
// causal claim_brief test seam: a fake EnhancerBriefQuerier feeds row
// shapes and the builder's contract is pinned — silent ("", nil) on
// every empty/error path, injects only for supervising/completed runs
// with a readable coda, byte-capped, UTF-8-safe.
type fakeBriefQuerier struct {
	runs []db.MythosRun
	err  error
}

func (f *fakeBriefQuerier) ListMythosRunsByIssueAndWorkspace(_ context.Context, _ db.ListMythosRunsByIssueAndWorkspaceParams) ([]db.MythosRun, error) {
	return f.runs, f.err
}

func briefRun(status string, coda []byte) db.MythosRun {
	return db.MythosRun{
		ID:              pgtype.UUID{Valid: true},
		WorkspaceID:     pgtype.UUID{Valid: true},
		Problem:         "Ship the release notes",
		Status:          status,
		CodaConclusions: coda,
	}
}

var briefWS = pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
var briefIssue = pgtype.UUID{Bytes: [16]byte{2}, Valid: true}

func TestBuildEnhancerBrief(t *testing.T) {
	t.Run("no runs renders nothing", func(t *testing.T) {
		out, err := BuildEnhancerBrief(context.Background(), &fakeBriefQuerier{}, briefWS, briefIssue)
		if err != nil || out != "" {
			t.Fatalf("expected silent empty, got %q, %v", out, err)
		}
	})

	t.Run("querier error renders nothing (silent fallback)", func(t *testing.T) {
		out, err := BuildEnhancerBrief(context.Background(),
			&fakeBriefQuerier{err: errors.New("db down")}, briefWS, briefIssue)
		if err != nil || out != "" {
			t.Fatalf("expected silent empty on error, got %q, %v", out, err)
		}
	})

	t.Run("nil querier and invalid ids render nothing", func(t *testing.T) {
		if out, _ := BuildEnhancerBrief(context.Background(), nil, briefWS, briefIssue); out != "" {
			t.Fatalf("nil querier must render nothing, got %q", out)
		}
		if out, _ := BuildEnhancerBrief(context.Background(), &fakeBriefQuerier{}, pgtype.UUID{}, briefIssue); out != "" {
			t.Fatalf("invalid workspace must render nothing, got %q", out)
		}
	})

	t.Run("running run (coda not landed) renders nothing", func(t *testing.T) {
		q := &fakeBriefQuerier{runs: []db.MythosRun{briefRun("running", []byte(`["strategy"]`))}}
		if out, _ := BuildEnhancerBrief(context.Background(), q, briefWS, briefIssue); out != "" {
			t.Fatalf("mid-pipeline run must render nothing, got %q", out)
		}
	})

	t.Run("failed run renders nothing", func(t *testing.T) {
		q := &fakeBriefQuerier{runs: []db.MythosRun{briefRun("failed", []byte(`["partial"]`))}}
		if out, _ := BuildEnhancerBrief(context.Background(), q, briefWS, briefIssue); out != "" {
			t.Fatalf("failed run must render nothing, got %q", out)
		}
	})

	t.Run("supervising run with coda array injects the strategy", func(t *testing.T) {
		q := &fakeBriefQuerier{runs: []db.MythosRun{
			briefRun("supervising", []byte(`["First conclusion.","Second conclusion."]`)),
		}}
		out, err := BuildEnhancerBrief(context.Background(), q, briefWS, briefIssue)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		for _, want := range []string{
			"## OpenMythos Strategy (outer loop, read-only)",
			"supervising",
			"Ship the release notes",
			"First conclusion.",
			"Second conclusion.",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("brief missing %q\nbrief: %s", want, out)
			}
		}
	})

	t.Run("malformed coda degrades to raw bytes", func(t *testing.T) {
		q := &fakeBriefQuerier{runs: []db.MythosRun{
			briefRun("completed", []byte("not-json")),
		}}
		out, _ := BuildEnhancerBrief(context.Background(), q, briefWS, briefIssue)
		if !strings.Contains(out, "not-json") {
			t.Errorf("malformed coda should degrade to raw text, got: %s", out)
		}
	})

	t.Run("oversized brief is byte-capped with valid UTF-8", func(t *testing.T) {
		big := strings.Repeat("策略段落。", 2000) // 20000 bytes of 4-byte runes
		q := &fakeBriefQuerier{runs: []db.MythosRun{
			briefRun("supervising", mustJSON(t, []string{big})),
		}}
		out, _ := BuildEnhancerBrief(context.Background(), q, briefWS, briefIssue)
		if len(out) > EnhancerBriefMaxBytes+32 { // cap + truncation suffix
			t.Fatalf("brief not capped: %d bytes", len(out))
		}
		if !utf8.ValidString(out) {
			t.Fatal("capped brief split a rune — invalid UTF-8")
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}
