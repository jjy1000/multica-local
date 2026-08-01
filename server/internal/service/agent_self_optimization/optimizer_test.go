package agent_self_optimization

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func pgtypeUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	var u pgtype.UUID
	if err := u.Scan(s); err != nil {
		t.Fatalf("scan uuid: %v", err)
	}
	return u
}

// fakeLLM returns canned responses per call role.
type fakeLLM struct {
	proposalText  string
	validatorText string
}

func (f *fakeLLM) provider(ctx context.Context, system, prompt string) (string, error) {
	if strings.Contains(system, "skill optimizer") {
		return f.proposalText, nil
	}
	return "", nil
}

func (f *fakeLLM) validator(ctx context.Context, system, prompt string) (string, error) {
	return f.validatorText, nil
}

// agentEvidence builds an agent-shaped OptimizeEvidence for the tests.
func agentEvidence(t *testing.T, enrolled bool, events []db.AgentTrustEvent) OptimizeEvidence {
	t.Helper()
	return OptimizeEvidence{
		TargetType:        SubjectAgent,
		TargetID:          pgtypeUUID(t, "11111111-1111-1111-1111-111111111111"),
		TargetName:        "helper",
		CurrentText:       "Do the thing.",
		Issues:            []db.ListDoneIssuesForSelfOptRow{{Title: "Fix time bug"}},
		TrustEvents:       events,
		AutoApplyEnrolled: enrolled,
	}
}

// ---------- scheduler ----------

func TestNextTriggerWeeklyCatchUp(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 8, 3, 9, 0, 0, 0, loc) // Monday 09:00

	// Fresh install (no last run): catch-up fires immediately — the user
	// just opted in, the first optimization should happen on the next
	// tick rather than waiting for the weekday 10:00 slot. The runner's
	// data-sufficiency gate protects against empty evidence.
	got := NextTrigger(time.Time{}, now, loc)
	if !got.Equal(now) {
		t.Fatalf("NextTrigger(fresh) = %v; want now %v", got, now)
	}

	// Last run 8 days ago (> 7d) → catch-up: fire immediately (now).
	last := now.Add(-8 * 24 * time.Hour)
	got = NextTrigger(last, now, loc)
	if !got.Equal(now) {
		t.Fatalf("NextTrigger(catch-up) = %v; want now %v", got, now)
	}

	// Last run 3 days ago (Fri Jul 31) → interval gate lands Fri Aug 7
	// 10:00, which IS a weekday 10:00 slot — fire then.
	last = now.Add(-3 * 24 * time.Hour)
	got = NextTrigger(last, now, loc)
	want := time.Date(2026, 8, 7, 10, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Fatalf("NextTrigger(steady) = %v; want %v", got, want)
	}
}

// ---------- optimizer: proposal application ----------

func TestApplyProposal(t *testing.T) {
	cur := "Always verify results.\n"

	// add
	e, newState, ok := applyProposal(cur, EditProposal{EditType: "add", After: "Ask clarifying questions first."})
	if !ok || !strings.Contains(newState, "Ask clarifying questions") {
		t.Fatalf("add failed: ok=%v state=%q", ok, newState)
	}

	// replace
	e, newState, ok = applyProposal(cur, EditProposal{EditType: "replace", Before: "Always verify", After: "Always verify twice"})
	if !ok || !strings.Contains(newState, "Always verify twice") {
		t.Fatalf("replace failed: ok=%v state=%q", ok, newState)
	}

	// delete
	e, newState, ok = applyProposal(cur, EditProposal{EditType: "delete", Before: "Always verify results."})
	if !ok || newState != "" {
		t.Fatalf("delete failed: ok=%v state=%q", ok, newState)
	}

	// delete with non-matching before → rejected
	_, _, ok = applyProposal(cur, EditProposal{EditType: "delete", Before: "Nonexistent line"})
	if ok {
		t.Fatalf("delete of missing text should fail")
	}

	// edit type fields are carried into the InstructionEdit.
	if e.EditType != "delete" || e.BeforeText != "Always verify results." {
		t.Fatalf("edit record not populated: %+v", e)
	}
}

func TestOptimizerRejectionBuffer(t *testing.T) {
	o := stubOptimizer(NewOptimizer())
	// Production rows always carry an explicit application state (migration
	// 230 backfills it; the runner feeds ListNegativeExperienceEdits which
	// filters to rejected/reverted). 0.5.2 adversarial review d6: a REVERTED
	// edit is hard negative experience too — the user explicitly rolled it
	// back, so re-proposing it would undo that decision.
	rejected := []db.AgentOptEdit{
		{EditType: "add", BeforeText: "", AfterText: "Do X", Application: string(ApplicationRejected)},
		{EditType: "replace", BeforeText: "Old", AfterText: "New", Application: string(ApplicationApplied)}, // applied → NOT negative
		{EditType: "add", BeforeText: "", AfterText: "Reverted line", Application: string(ApplicationReverted)},
	}
	if !o.isRejected(EditProposal{EditType: "add", Before: "", After: "Do X"}, rejected) {
		t.Fatalf("matching rejected add should be blocked")
	}
	if o.isRejected(EditProposal{EditType: "replace", Before: "Old", After: "New"}, rejected) {
		t.Fatalf("applied edit must NOT be in the rejection buffer")
	}
	if !o.isRejected(EditProposal{EditType: "add", Before: "", After: "Reverted line"}, rejected) {
		t.Fatalf("reverted edit must be in the rejection buffer (d6)")
	}
	if o.isRejected(EditProposal{EditType: "add", After: "Do Y"}, rejected) {
		t.Fatalf("unrelated proposal must not be blocked")
	}
}

func TestParseProposals(t *testing.T) {
	// Bare array.
	arr, err := parseProposals(`[{"edit_type":"add","after":"X","rationale":"R"}]`)
	if err != nil || len(arr) != 1 || arr[0].EditType != "add" {
		t.Fatalf("bare array parse failed: %v %v", arr, err)
	}
	// Wrapped.
	wrapped, err := parseProposals(`{"proposals":[{"edit_type":"delete","before":"Y"}]}`)
	if err != nil || len(wrapped) != 1 || wrapped[0].EditType != "delete" {
		t.Fatalf("wrapped parse failed: %v %v", wrapped, err)
	}
	// Garbage.
	if _, err := parseProposals("not json"); err == nil {
		t.Fatalf("garbage should error")
	}
	// Markdown fence (provider CLI wraps JSON).
	fenced, err := parseProposals("```json\n[{\"edit_type\":\"add\",\"after\":\"Z\"}]\n```")
	if err != nil || len(fenced) != 1 || fenced[0].EditType != "add" {
		t.Fatalf("fenced parse failed: %v %v", fenced, err)
	}
}

// stubOptimizer returns an Optimizer with a nil-safe lab-managed check
// (tests pass nil *db.Queries to OptimizeSubject).
func stubOptimizer(o *Optimizer) *Optimizer {
	o.LabManagedFunc = func(ctx context.Context, q *db.Queries, targetType string, targetID pgtype.UUID) (bool, error) {
		return false, nil
	}
	return o
}

func TestOptimizeAgentAutoApplyEnroll(t *testing.T) {
	// 0.5.3 trust semantics: trust ≥ 8 parks the subject (no proposals at
	// all). For auto-apply the scope must be trust (low trust + negative
	// evidence) or enrolled. This test pins the enroll path: an enrolled
	// agent with a correction anchor + score ≥ 90 auto-applies.
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"Always double-check timestamps.","rationale":"correction about time"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":95,"reason":"fixes documented time bug"}`}).validator

	ev := agentEvidence(t, true, []db.AgentTrustEvent{
		{EventType: "correction", TaskID: pgtypeUUID(t, "55555555-5555-5555-5555-555555555555")},
		{EventType: "review_pass", ScoreAfter: num(7.5)}, // mid-trust: < 8 (not retained), ≥ 7 (enroll scope)
	})
	applied, suggested, rejected, finalState, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 1 || applied[0].Application != ApplicationApplied {
		t.Fatalf("expected 1 applied edit, got applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
	if !strings.Contains(finalState, "Always double-check timestamps") {
		t.Fatalf("final state missing applied add: %q", finalState)
	}
}

func TestOptimizeAgentRetentionParksAtHighTrust(t *testing.T) {
	// 0.5.3 retention: trust ≥ 8 → NO proposals at all (the subject is
	// "不用再进化"). Even a high-scoring add must not be proposed.
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"New line.","rationale":"x"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":95,"reason":"high"}`}).validator

	ev := agentEvidence(t, true, []db.AgentTrustEvent{
		{EventType: "review_pass", ScoreAfter: num(9.5)},
	})
	applied, suggested, rejected, finalState, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 0 || len(suggested) != 0 || len(rejected) != 0 {
		t.Fatalf("retention must propose nothing: applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
	if finalState != ev.CurrentText {
		t.Fatalf("retention must not change text: %q", finalState)
	}
}

func TestOptimizeAgentTrustScopeAutoAppliesWithoutMarker(t *testing.T) {
	// 0.5.3 trust semantics inversion: a LOW-trust agent (score < 7 +
	// negative evidence) is a SYSTEM MANDATE to fix — auto-apply works
	// WITHOUT the enrollment marker (the user's own spec directs the
	// system to fix low-trust subjects).
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"Always verify edge cases.","rationale":"user correction"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":92,"reason":"fixes corrected task"}`}).validator

	ev := agentEvidence(t, false, []db.AgentTrustEvent{ // NOT enrolled
		{EventType: "correction", TaskID: pgtypeUUID(t, "55555555-5555-5555-5555-555555555555")},
		{EventType: "review_fail", ScoreAfter: num(4.5)},
	})
	applied, suggested, rejected, finalState, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("low-trust mandate must auto-apply without marker: applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
	if !strings.Contains(finalState, "Always verify edge cases") {
		t.Fatalf("final state missing trust-scope add: %q", finalState)
	}
	// The persisted edit must carry the trust scope.
	if applied[0].Scope != ScopeTrust {
		t.Fatalf("trust-scope edit must carry scope=%s, got %s", ScopeTrust, applied[0].Scope)
	}
}

func TestOptimizeAgentDeleteNeverAutoApplies(t *testing.T) {
	// delete edit, score 95 — by-construction NEVER auto-applies.
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"delete","before":"Do the thing.","rationale":"remove redundancy"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":95,"reason":"high"}`}).validator

	ev := agentEvidence(t, true, []db.AgentTrustEvent{
		{EventType: "correction", TaskID: pgtypeUUID(t, "55555555-5555-5555-5555-555555555555")},
		{EventType: "review_pass", ScoreAfter: num(6.0)},
	})
	applied, suggested, _, finalState, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("delete must never auto-apply, got applied=%v", applied)
	}
	if len(suggested) != 1 {
		t.Fatalf("expected delete to land in suggested, got suggested=%v", suggested)
	}
	if finalState != ev.CurrentText {
		t.Fatalf("delete must not change instructions: %q", finalState)
	}
}

func TestOptimizeAgentNotEnrolledSuggested(t *testing.T) {
	// score 95 but NOT enrolled AND trust mid-range (no mandate) →
	// suggested (never auto).
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"New line.","rationale":"x"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":95,"reason":"high"}`}).validator

	ev := agentEvidence(t, false, []db.AgentTrustEvent{
		{EventType: "review_pass", ScoreAfter: num(7.5)}, // no negative evidence, not low
	})
	applied, suggested, _, _, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 0 || len(suggested) != 1 {
		t.Fatalf("not-enrolled mid-trust high score must go to suggested: applied=%v suggested=%v", applied, suggested)
	}
}

func TestOptimizeAgentLowScoreRejected(t *testing.T) {
	// add edit, score 30 (< ProposeFloor=60) → rejected.
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"Speculative.","rationale":"?"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":30,"reason":"speculative"}`}).validator

	ev := agentEvidence(t, true, []db.AgentTrustEvent{
		{EventType: "correction", TaskID: pgtypeUUID(t, "55555555-5555-5555-5555-555555555555")},
	})
	applied, suggested, rejected, finalState, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeAgent: %v", err)
	}
	if len(applied) != 0 || len(suggested) != 0 || len(rejected) != 1 {
		t.Fatalf("low score must be rejected: applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
	if finalState != ev.CurrentText {
		t.Fatalf("rejected must not change instructions: %q", finalState)
	}
}

func TestOptimizeAgentNoEvidence(t *testing.T) {
	o := stubOptimizer(NewOptimizer())
	ev := OptimizeEvidence{TargetType: SubjectAgent, TargetID: pgtypeUUID(t, "11111111-1111-1111-1111-111111111111"), TargetName: "x"}
	applied, suggested, rejected, final, err := o.OptimizeAgent(context.Background(), nil, ev)
	if err != nil || len(applied) != 0 || len(suggested) != 0 || len(rejected) != 0 || final != "" {
		t.Fatalf("no-evidence run should be a no-op: %v %v %v %q %v", applied, suggested, rejected, final, err)
	}
}

// ---------- 0.5.3 non-agent subjects ----------

func TestOptimizeSkillAlwaysSuggested(t *testing.T) {
	// Skills have no trust ledger → every edit lands in the human-confirm
	// tier (suggested), never auto-applied.
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"## Usage\nRun with the --dry-run flag first.","rationale":"add usage doc"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":95,"reason":"clear improvement"}`}).validator

	ev := OptimizeEvidence{
		TargetType:  SubjectSkill,
		TargetID:    pgtypeUUID(t, "22222222-2222-2222-2222-222222222222"),
		TargetName:  "my-skill",
		CurrentText: "# my-skill\nA skill for X.",
	}
	applied, suggested, rejected, finalText, err := o.OptimizeSubject(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeSubject: %v", err)
	}
	if len(applied) != 0 || len(rejected) != 0 {
		t.Fatalf("skill edits must never auto-apply nor reject outright: applied=%v rejected=%v", applied, rejected)
	}
	if len(suggested) != 1 {
		t.Fatalf("expected 1 suggested skill edit, got %v", suggested)
	}
	if suggested[0].TargetType != SubjectSkill || suggested[0].Scope != ScopeEnroll {
		t.Fatalf("suggested edit must carry skill target + enroll scope: %+v", suggested[0])
	}
	if finalText != ev.CurrentText {
		t.Fatalf("suggested-only must not change text: %q", finalText)
	}
}

func TestOptimizeSquadSuggestedOnly(t *testing.T) {
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"replace","before":"Report daily.","after":"Report after every milestone.","rationale":"clearer cadence"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":80,"reason":"clearer"}`}).validator

	ev := OptimizeEvidence{
		TargetType:  SubjectSquad,
		TargetID:    pgtypeUUID(t, "33333333-3333-3333-3333-333333333333"),
		TargetName:  "data-squad",
		CurrentText: "Report daily.",
	}
	applied, suggested, rejected, _, err := o.OptimizeSubject(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeSubject: %v", err)
	}
	if len(applied) != 0 || len(rejected) != 0 || len(suggested) != 1 {
		t.Fatalf("squad replace should be suggested-only: applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
}

func TestOptimizeAutopilotSuggestedOnly(t *testing.T) {
	o := stubOptimizer(NewOptimizer())
	o.ProviderLLM = (&fakeLLM{
		proposalText: `[{"edit_type":"add","after":"[巡检] {{date}} — 例行检查","rationale":"title prefix"}]`,
	}).provider
	o.ValidatorLLM = (&fakeLLM{validatorText: `{"score":85,"reason":"clear title"}`}).validator

	ev := OptimizeEvidence{
		TargetType:  SubjectAutopilot,
		TargetID:    pgtypeUUID(t, "44444444-4444-4444-4444-444444444444"),
		TargetName:  "巡检",
		CurrentText: "{{date}} 巡检",
	}
	applied, suggested, rejected, _, err := o.OptimizeSubject(context.Background(), nil, ev)
	if err != nil {
		t.Fatalf("OptimizeSubject: %v", err)
	}
	if len(applied) != 0 || len(rejected) != 0 || len(suggested) != 1 {
		t.Fatalf("autopilot edit should be suggested-only: applied=%v suggested=%v rejected=%v", applied, suggested, rejected)
	}
}

// ---------- safety tokens (0.5.3 narrowing) ----------

func TestSafetyTokenNarrowing(t *testing.T) {
	// 0.5.3: 必须 / 不得 were removed from the hard-block list — they are
	// high-frequency instruction verbs. deny/never/confirm/secret/禁止/不可
	// still block auto-apply.
	if !touchesSafetyToken("deny access") {
		t.Fatalf("deny must block")
	}
	if !touchesSafetyToken("never reveal") {
		t.Fatalf("never must block")
	}
	if !touchesSafetyToken("confirm before") {
		t.Fatalf("confirm must block")
	}
	if !touchesSafetyToken("secret value") {
		t.Fatalf("secret must block")
	}
	if !touchesSafetyToken("禁止删除") {
		t.Fatalf("禁止 must block")
	}
	if !touchesSafetyToken("不可覆盖") {
		t.Fatalf("不可 must block")
	}
	// Narrowed: instruction verbs no longer block.
	if touchesSafetyToken("必须完成任务") {
		t.Fatalf("必须 must NOT block (narrowed 0.5.3)")
	}
	if touchesSafetyToken("不得超时") {
		t.Fatalf("不得 must NOT block (narrowed 0.5.3)")
	}
}

// ---------- thresholds ----------

func TestClassifyApplicationThresholds(t *testing.T) {
	// Direct threshold checks.
	if ProposeFloor != 60.0 {
		t.Fatalf("ProposeFloor = %v; want 60", ProposeFloor)
	}
	if AutoApplyGate != 90.0 {
		t.Fatalf("AutoApplyGate = %v; want 90", AutoApplyGate)
	}
	if RetainTrustScore != 8.0 {
		t.Fatalf("RetainTrustScore = %v; want 8", RetainTrustScore)
	}
	if OptimizeTrustThreshold != 7.0 {
		t.Fatalf("OptimizeTrustThreshold = %v; want 7", OptimizeTrustThreshold)
	}
}

func num(v float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.1f", v))
	return n
}

// TestRevalidateAppliedEditsShortCircuits pins the post-hoc commit gate's
// (0.5.2 adversarial review d1) DB-free no-op path: an invalid target id must
// return nil without touching the database. The DB-backed re-score + rollback
// path is covered by the handler-level integration tests against the real
// schema.
func TestRevalidateAppliedEditsShortCircuits(t *testing.T) {
	o := stubOptimizer(NewOptimizer())

	// Invalid target id → no DB access, nil error.
	if err := o.RevalidateAppliedEdits(context.Background(), nil, SubjectAgent, pgtype.UUID{}, "x", "", pgtypeUUID(t, "22222222-2222-2222-2222-222222222222")); err != nil {
		t.Fatalf("invalid target id should short-circuit: %v", err)
	}

	// Cheap invariant: the retain floor must sit at ProposeFloor (an applied
	// edit that would not even make the suggested tier today is not worth
	// keeping — the revalidation is deliberately generous).
	if RevalidateRetainFloor != ProposeFloor {
		t.Fatalf("RevalidateRetainFloor = %v; want %v (== ProposeFloor)", RevalidateRetainFloor, ProposeFloor)
	}
}
