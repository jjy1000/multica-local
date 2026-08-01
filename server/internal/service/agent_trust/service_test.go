package agent_trust

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeQuerier implements Querier with in-memory maps so the score
// arithmetic + event ledger can be tested without a database.
type fakeQuerier struct {
	profiles map[string]db.AgentTrustProfile // key: wsID|agentID
	events   []db.AgentTrustEvent
	issues   map[pgtype.UUID]db.Issue
	err      error // when set, every call fails (DB-down path)
}

func newFakeQuerier() *fakeQuerier {
	return &fakeQuerier{
		profiles: map[string]db.AgentTrustProfile{},
		issues:   map[pgtype.UUID]db.Issue{},
	}
}

func fk(s string) pgtype.UUID {
	var u pgtype.UUID
	_ = u.Scan(s)
	return u
}

func num(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	// Scan via string (like production floatToNumeric) — Scanning a
	// float64 directly sets Numeric.Float64 with Int=nil, which
	// numericToFloat64 rejects.
	_ = n.Scan(fmt.Sprintf("%.1f", v))
	return n
}

func (f *fakeQuerier) key(ws, agent pgtype.UUID) string {
	return ws.String() + "|" + agent.String()
}

func (f *fakeQuerier) GetAgentTrustProfile(ctx context.Context, arg db.GetAgentTrustProfileParams) (db.AgentTrustProfile, error) {
	if f.err != nil {
		return db.AgentTrustProfile{}, f.err
	}
	p, ok := f.profiles[f.key(arg.WorkspaceID, arg.AgentID)]
	if !ok {
		return db.AgentTrustProfile{}, pgx.ErrNoRows
	}
	return p, nil
}

func (f *fakeQuerier) applyProfile(ws, agent pgtype.UUID, score float64) db.AgentTrustProfile {
	p := f.profiles[f.key(ws, agent)]
	p.WorkspaceID = ws
	p.AgentID = agent
	p.Score = num(score)
	if p.ReviewThreshold.Valid == false || p.ReviewThreshold.Int == nil {
		p.ReviewThreshold = num(7.0)
	}
	f.profiles[f.key(ws, agent)] = p
	return p
}

func (f *fakeQuerier) ApplyAgentTrustCorrection(ctx context.Context, arg db.ApplyAgentTrustCorrectionParams) (db.AgentTrustProfile, error) {
	if f.err != nil {
		return db.AgentTrustProfile{}, f.err
	}
	p := f.applyProfile(arg.WorkspaceID, arg.AgentID, scoreOf(arg.Score))
	p.CorrectionCount++
	f.profiles[f.key(arg.WorkspaceID, arg.AgentID)] = p
	return p, nil
}

func (f *fakeQuerier) ApplyAgentTrustReviewRequested(ctx context.Context, arg db.ApplyAgentTrustReviewRequestedParams) (db.AgentTrustProfile, error) {
	if f.err != nil {
		return db.AgentTrustProfile{}, f.err
	}
	p := f.applyProfile(arg.WorkspaceID, arg.AgentID, scoreOf(arg.Score))
	p.ReviewRequestedCount++
	f.profiles[f.key(arg.WorkspaceID, arg.AgentID)] = p
	return p, nil
}

func (f *fakeQuerier) ApplyAgentTrustReviewPass(ctx context.Context, arg db.ApplyAgentTrustReviewPassParams) (db.AgentTrustProfile, error) {
	if f.err != nil {
		return db.AgentTrustProfile{}, f.err
	}
	p := f.applyProfile(arg.WorkspaceID, arg.AgentID, scoreOf(arg.Score))
	p.ReviewPassCount++
	f.profiles[f.key(arg.WorkspaceID, arg.AgentID)] = p
	return p, nil
}

func (f *fakeQuerier) ApplyAgentTrustReviewFail(ctx context.Context, arg db.ApplyAgentTrustReviewFailParams) (db.AgentTrustProfile, error) {
	if f.err != nil {
		return db.AgentTrustProfile{}, f.err
	}
	p := f.applyProfile(arg.WorkspaceID, arg.AgentID, scoreOf(arg.Score))
	p.ReviewFailCount++
	f.profiles[f.key(arg.WorkspaceID, arg.AgentID)] = p
	return p, nil
}

func (f *fakeQuerier) CreateAgentTrustEvent(ctx context.Context, arg db.CreateAgentTrustEventParams) (db.AgentTrustEvent, error) {
	if f.err != nil {
		return db.AgentTrustEvent{}, f.err
	}
	ev := db.AgentTrustEvent{
		WorkspaceID: arg.WorkspaceID,
		AgentID:     arg.AgentID,
		EventType:   arg.EventType,
		ScoreDelta:  arg.ScoreDelta,
		ScoreBefore: arg.ScoreBefore,
		ScoreAfter:  arg.ScoreAfter,
		TaskID:      arg.TaskID,
		IssueID:     arg.IssueID,
		Note:        arg.Note,
	}
	f.events = append(f.events, ev)
	return ev, nil
}

func (f *fakeQuerier) GetIssue(ctx context.Context, id pgtype.UUID) (db.Issue, error) {
	if f.err != nil {
		return db.Issue{}, f.err
	}
	iss, ok := f.issues[id]
	if !ok {
		return db.Issue{}, pgx.ErrNoRows
	}
	return iss, nil
}

// scoreOf converts a pgtype.Numeric back to float64 for the fake.
func scoreOf(n pgtype.Numeric) float64 {
	v, ok := numericToFloat64(n)
	if !ok {
		return 0
	}
	return v
}

// approx compares floats with a 1e-9 tolerance — the in-memory score math
// (6.8 - 0.5 = 6.300000000000001) drifts from the exact decimal the DB
// stores; production writes via 1-decimal strings so the stored value is
// exact, only the Go float arithmetic is approximate.
func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-9
}

// fakeReviewer returns a canned verdict.
type fakeReviewer struct {
	verdict ReviewVerdict
}

func (f *fakeReviewer) Review(ctx context.Context, task db.AgentTaskQueue, result []byte) ReviewVerdict {
	return f.verdict
}

var _ Querier = (*fakeQuerier)(nil)
var _ Reviewer = (*fakeReviewer)(nil)

const (
	testAgentA = "11111111-1111-1111-1111-111111111111"
	testAgentB = "22222222-2222-2222-2222-222222222222"
	testWS     = "33333333-3333-3333-3333-333333333333"
	testIssue  = "44444444-4444-4444-4444-444444444444"
)

func taskFor(agent, issue string, status string) db.AgentTaskQueue {
	return db.AgentTaskQueue{
		AgentID: fk(agent),
		IssueID: fk(issue),
		Status:  status,
		Result:  []byte("sub-agent output"),
	}
}

func TestInitialScoreIsFive(t *testing.T) {
	if InitialScore != 5.0 {
		t.Fatalf("InitialScore = %v; want 5.0", InitialScore)
	}
	if MaxScore != 10.0 {
		t.Fatalf("MaxScore = %v; want 10.0", MaxScore)
	}
	if CorrectionPenalty != 0.5 {
		t.Fatalf("CorrectionPenalty = %v; want 0.5", CorrectionPenalty)
	}
	if ReviewPassBonus != 0.2 {
		t.Fatalf("ReviewPassBonus = %v; want 0.2", ReviewPassBonus)
	}
	if DefaultReviewThreshold != 7.0 {
		t.Fatalf("DefaultReviewThreshold = %v; want 7.0", DefaultReviewThreshold)
	}
}

func TestScoreDefaultsWhenNoProfile(t *testing.T) {
	// An agent with no row is treated as the initial score.
	if got := Score(db.AgentTrustProfile{}, false); got != InitialScore {
		t.Fatalf("Score(no row) = %v; want %v", got, InitialScore)
	}
	if got := ReviewThreshold(db.AgentTrustProfile{}, false); got != DefaultReviewThreshold {
		t.Fatalf("ReviewThreshold(no row) = %v; want %v", got, DefaultReviewThreshold)
	}
}

func TestApplyCorrectionDeductsAndRecordsEvent(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()

	after, err := s.ApplyCorrection(ctx, f, fk(testWS), fk(testAgentA), pgtype.UUID{}, fk(testIssue), "wrong result", fk("99999999-9999-9999-9999-999999999999"))
	if err != nil {
		t.Fatalf("ApplyCorrection: %v", err)
	}
	if after != 4.5 {
		t.Fatalf("after first correction = %v; want 4.5", after)
	}
	if len(f.events) != 1 {
		t.Fatalf("events = %d; want 1", len(f.events))
	}
	ev := f.events[0]
	if ev.EventType != string(EventCorrection) {
		t.Fatalf("event_type = %s; want correction", ev.EventType)
	}
	if scoreOf(ev.ScoreDelta) != -0.5 {
		t.Fatalf("score_delta = %v; want -0.5", scoreOf(ev.ScoreDelta))
	}
	if ev.Note.String != "wrong result" {
		t.Fatalf("note = %q; want wrong result", ev.Note.String)
	}
	if ev.IssueID.Valid != true || ev.IssueID.String() != testIssue {
		t.Fatalf("issue_id not recorded: %v", ev.IssueID)
	}
}

func TestApplyCorrectionClampsAtZero(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	// Pre-set the profile to 0.3 so a correction would go negative.
	f.applyProfile(fk(testWS), fk(testAgentA), 0.3)

	after, err := s.ApplyCorrection(ctx, f, fk(testWS), fk(testAgentA), pgtype.UUID{}, pgtype.UUID{}, "", pgtype.UUID{})
	if err != nil {
		t.Fatalf("ApplyCorrection: %v", err)
	}
	if after != 0.0 {
		t.Fatalf("after clamp = %v; want 0.0", after)
	}
}

func TestApplyCorrectionClampsAtMax(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	f.applyProfile(fk(testWS), fk(testAgentA), 9.9)

	// Correction from 9.9 cannot exceed MaxScore (it can't go up, but the
	// clamp also guards the review-pass direction below).
	after, err := s.ApplyCorrection(ctx, f, fk(testWS), fk(testAgentA), pgtype.UUID{}, pgtype.UUID{}, "", pgtype.UUID{})
	if err != nil {
		t.Fatalf("ApplyCorrection: %v", err)
	}
	if after != 9.4 {
		t.Fatalf("after = %v; want 9.4", after)
	}
}

func TestReviewPassRestores(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	s.SetReviewer(&fakeReviewer{verdict: VerdictPass})
	f.applyProfile(fk(testWS), fk(testAgentA), 6.8)
	f.issues[fk(testIssue)] = db.Issue{
		ID:          fk(testIssue),
		WorkspaceID: fk(testWS),
	}

	task := taskFor(testAgentA, testIssue, "completed")
	s.ProcessTaskCompletion(ctx, f, task, task.Result)

	prof, ok := f.profiles[f.key(fk(testWS), fk(testAgentA))]
	if !ok {
		t.Fatalf("no profile after review pass")
	}
	if got := scoreOf(prof.Score); got != 7.0 {
		t.Fatalf("score after pass = %v; want 7.0 (6.8 + 0.2)", got)
	}
	if prof.ReviewPassCount != 1 {
		t.Fatalf("review_pass_count = %d; want 1", prof.ReviewPassCount)
	}
	// Events: review_requested + review_pass.
	if len(f.events) != 2 {
		t.Fatalf("events = %d; want 2", len(f.events))
	}
	if f.events[1].EventType != string(EventReviewPass) {
		t.Fatalf("second event = %s; want review_pass", f.events[1].EventType)
	}
	if scoreOf(f.events[1].ScoreAfter) != 7.0 {
		t.Fatalf("event score_after = %v; want 7.0", scoreOf(f.events[1].ScoreAfter))
	}
}

func TestReviewFailDeducts(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	s.SetReviewer(&fakeReviewer{verdict: VerdictFail})
	f.applyProfile(fk(testWS), fk(testAgentA), 6.8)
	f.issues[fk(testIssue)] = db.Issue{
		ID:          fk(testIssue),
		WorkspaceID: fk(testWS),
	}

	task := taskFor(testAgentA, testIssue, "completed")
	s.ProcessTaskCompletion(ctx, f, task, task.Result)

	prof := f.profiles[f.key(fk(testWS), fk(testAgentA))]
	if !approx(scoreOf(prof.Score), 6.3) {
		t.Fatalf("score after fail = %v; want 6.3 (6.8 - 0.5)", scoreOf(prof.Score))
	}
	if prof.ReviewFailCount != 1 {
		t.Fatalf("review_fail_count = %d; want 1", prof.ReviewFailCount)
	}
	if f.events[1].EventType != string(EventReviewFail) {
		t.Fatalf("second event = %s; want review_fail", f.events[1].EventType)
	}
}

func TestGateSkipsWhenScoreAtOrAboveThreshold(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	// score 8.0 ≥ 7.0 → no review, no events, no profile change.
	s.SetReviewer(&fakeReviewer{verdict: VerdictFail}) // would deduct if called
	f.applyProfile(fk(testWS), fk(testAgentA), 8.0)
	f.issues[fk(testIssue)] = db.Issue{
		ID:          fk(testIssue),
		WorkspaceID: fk(testWS),
	}

	task := taskFor(testAgentA, testIssue, "completed")
	s.ProcessTaskCompletion(ctx, f, task, task.Result)

	if len(f.events) != 0 {
		t.Fatalf("events = %d; want 0 (trusted, no review)", len(f.events))
	}
	prof := f.profiles[f.key(fk(testWS), fk(testAgentA))]
	if got := scoreOf(prof.Score); got != 8.0 {
		t.Fatalf("score = %v; want 8.0 unchanged", got)
	}
}

func TestGateSkipsChatTasks(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	s.SetReviewer(&fakeReviewer{verdict: VerdictFail})
	// Chat task: no issue → no workspace → gate returns early.
	task := db.AgentTaskQueue{AgentID: fk(testAgentA)}
	s.ProcessTaskCompletion(ctx, f, task, nil)

	if len(f.events) != 0 {
		t.Fatalf("events = %d; want 0 (chat task skipped)", len(f.events))
	}
}

func TestReviewSkipOnNoLLM(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	// Reviewer returns skip (no provider CLI) → no score change, but a
	// review_skipped event is recorded.
	s.SetReviewer(&fakeReviewer{verdict: VerdictSkip})
	f.applyProfile(fk(testWS), fk(testAgentA), 6.0)
	f.issues[fk(testIssue)] = db.Issue{
		ID:          fk(testIssue),
		WorkspaceID: fk(testWS),
	}

	task := taskFor(testAgentA, testIssue, "completed")
	s.ProcessTaskCompletion(ctx, f, task, task.Result)

	prof := f.profiles[f.key(fk(testWS), fk(testAgentA))]
	if got := scoreOf(prof.Score); got != 6.0 {
		t.Fatalf("score after skip = %v; want 6.0 unchanged", got)
	}
	last := f.events[len(f.events)-1]
	if last.EventType != string(EventReviewSkipped) {
		t.Fatalf("last event = %s; want review_skipped", last.EventType)
	}
}

func TestProcessTaskCompletionFailOpenOnDBError(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	f.err = pgx.ErrNoRows // issue lookup fails
	s := NewService()
	s.SetReviewer(&fakeReviewer{verdict: VerdictFail})

	task := taskFor(testAgentA, testIssue, "completed")
	s.ProcessTaskCompletion(ctx, f, task, task.Result)
	// Must not panic; no events recorded.
	if len(f.events) != 0 {
		t.Fatalf("events = %d; want 0", len(f.events))
	}
}

func TestReviewTaskManual(t *testing.T) {
	ctx := context.Background()
	f := newFakeQuerier()
	s := NewService()
	s.SetReviewer(&fakeReviewer{verdict: VerdictPass})
	f.applyProfile(fk(testWS), fk(testAgentA), 6.0)
	f.issues[fk(testIssue)] = db.Issue{
		ID:          fk(testIssue),
		WorkspaceID: fk(testWS),
	}

	task := taskFor(testAgentA, testIssue, "completed")
	verdict := s.ReviewTask(ctx, f, task, task.Result)
	if verdict != VerdictPass {
		t.Fatalf("verdict = %v; want pass", verdict)
	}
	prof := f.profiles[f.key(fk(testWS), fk(testAgentA))]
	if got := scoreOf(prof.Score); got != 6.2 {
		t.Fatalf("score after manual pass = %v; want 6.2", got)
	}
}

func TestClampScore(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{-1, 0}, {0, 0}, {4.5, 4.5}, {10, 10}, {11, 10},
	}
	for _, c := range cases {
		if got := clampScore(c.in); got != c.want {
			t.Fatalf("clampScore(%v) = %v; want %v", c.in, got, c.want)
		}
	}
}
