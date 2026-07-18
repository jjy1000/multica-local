// Package agent_self_optimization — runner.go (0.3.45.1).
//
// One-pass runner: the user-facing spec is "已完结的任务 → 学习 → 建议"。
// The runner pulls every "done" issue (filtered by SourceFilter), groups
// the titles + descriptions into clusters, derives heuristic prompt
// suggestions, writes a report, and creates the self-opt issue row.
//
// MVP boundary (per 0.3.45.1 release notes): NO LLM call. Suggestions
// are derived from keyword frequency + simple heuristic rules. 0.3.46+
// will swap the heuristic for an LLM-driven rationale via
// /api/runtime/llm-call. The wire shape (prompt_suggestions JSONB +
// report_md + created_issue_id) is stable, so the swap is local.
//
// Auto-archive: the runner creates the self-opt issue with
// lab_source='agent_self_optimization'. The main workspace task
// panel's exclude_lab=true filter (ListIssues / ListOpenIssues,
// introduced 0.3.33) hides the row immediately. The
// /experimental/self-opt-history view surfaces it for the user.
// That's "归档" in the user-visible sense without needing an
// archived_at column on the issue table.
package agent_self_optimization

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// MaxIssuesPerRun bounds the SQL scan size so a multi-month back-log
// doesn't blow up the runner's memory or the LLM call (in 0.3.46+).
// 1000 issues × ~5 KB each = ~5 MB in worst case; we accept the cap
// because older issues contribute diminishing signal.
const MaxIssuesPerRun = 1000

// LookbackWindow is the default time horizon when no previous run
// exists. Aligns with the user spec "已完结的任务" — the runner
// should look back at least the last month on a fresh install.
const LookbackWindow = 30 * 24 * time.Hour

// PromptSuggestion is one entry in the JSONB prompt_suggestions
// column. The shape is append-only — adding fields does not break
// existing rows because the renderer ignores unknown keys.
type PromptSuggestion struct {
	AgentID          uuid.UUID `json:"agent_id"`
	AgentName        string    `json:"agent_name"`
	CurrentExcerpt   string    `json:"current_excerpt,omitempty"`
	SuggestedChange  string    `json:"suggested_change"`
	Rationale        string    `json:"rationale"`
	Confidence       float64   `json:"confidence"` // 0.0-1.0
	BackingIssueIDs  []uuid.UUID `json:"backing_issue_ids,omitempty"`
}

// RunReport is the markdown report rendered into
// agent_self_opt_run.report_md. Sections are stable (the renderer
// parses by heading); new sections go below.
type RunReport struct {
	WorkspaceID      uuid.UUID         `json:"-"`
	StartedAt        time.Time         `json:"started_at"`
	FinishedAt       time.Time         `json:"finished_at"`
	SourceIssueCount int               `json:"source_issue_count"`
	Suggestions      []PromptSuggestion `json:"suggestions"`
	FailureThemes    []FailureTheme    `json:"failure_themes,omitempty"`
}

// FailureTheme is a heuristic cluster of titles that share keywords.
// 0.3.46+ may replace this with an LLM-driven summary.
type FailureTheme struct {
	Theme      string   `json:"theme"`
	IssueCount int      `json:"issue_count"`
	SampleIDs  []uuid.UUID `json:"sample_ids,omitempty"`
}

// RunInputs captures the per-run inputs the runner needs. Passed by
// value so the runner is goroutine-safe across concurrent workspaces.
type RunInputs struct {
	WorkspaceID pgtype.UUID
	TriggerKind string // "scheduled" | "manual"
	// Optional override for LookbackWindow — pass zero value to use
	// the package default. Useful for the CLI manual-trigger path
	// where the user wants to scan "all history".
	SinceOverride time.Time
}

// RunResult is what the runner returns to Service for persistence.
type RunResult struct {
	RunID              pgtype.UUID
	CreatedIssueID     pgtype.UUID
	SourceIssueCount   int
	Report             RunReport
	ReportMarkdown     string
	PromptSuggestions  []PromptSuggestion
	KBAppendixPath     string
}

// Run executes one self-opt pass. Caller (Service.Tick) holds the
// advisory lock so two daemons can't run for the same workspace
// concurrently.
//
// Failure modes:
//   - DB read error → return (RunResult{}, err); Service writes 'failed'
//   - Issue scan yields zero rows → still writes a "no input" report
//     so the user sees a row in the history view even when nothing
//     was scanned (rather than a gap that looks like "the lab broke").
//   - CreateIssue error → return err; Service writes 'failed'.
//   - KB write error → soft fail; result.KBAppendixPath stays "" and
//     Service persists the run without it. The user still gets the
//     report in the history view.
func Run(ctx context.Context, q *db.Queries, inputs RunInputs, kb KBWriter) (*RunResult, error) {
	if !inputs.WorkspaceID.Valid {
		return nil, fmt.Errorf("runner: workspace_id required")
	}
	if inputs.TriggerKind != "scheduled" && inputs.TriggerKind != "manual" {
		return nil, fmt.Errorf("runner: invalid trigger_kind %q", inputs.TriggerKind)
	}

	// 1. Resolve the source filter (visibility + hardcoded safety net).
	filter, err := NewSourceFilter(ctx, q)
	if err != nil {
		slog.Warn("agent-self-opt: source filter resolve failed; "+
			"using hardcoded safety net only",
			"workspace", inputs.WorkspaceID, "err", err)
		filter = &SourceFilter{HiddenAgentNames: append([]string(nil), hiddenAgentNames...)}
	}

	// 2. Compute the since timestamp. If a previous run exists, use
	// finished_at - 1h as the inclusive lower bound (the 1h overlap
	// catches issues that landed in flight).
	since := inputs.SinceOverride
	if since.IsZero() {
		last, lerr := q.LastSuccessfulAgentSelfOptRun(ctx, inputs.WorkspaceID)
		if lerr == nil && last.FinishedAt.Valid {
			since = last.FinishedAt.Time.Add(-1 * time.Hour)
		} else if lerr != nil && lerr != pgx.ErrNoRows {
			return nil, fmt.Errorf("read last successful run: %w", lerr)
		} else {
			since = time.Now().Add(-LookbackWindow)
		}
	}

	// 3. Scan done issues, filtering by SourceFilter. We do this with
	// a raw SQL composition because the WHERE clause needs NOT IN for
	// agent IDs + NOT IN for agent names via subquery. The fallback
	// path when the filter is empty is `WHERE ... AND TRUE` so the
	// runner doesn't have to branch the SQL string.
	rows, err := scanDoneIssues(ctx, q, inputs.WorkspaceID, since, filter)
	if err != nil {
		return nil, fmt.Errorf("scan done issues: %w", err)
	}
	slog.Info("agent-self-opt: scan complete",
		"workspace", inputs.WorkspaceID,
		"since", since,
		"matched", len(rows),
		"filter", filter.String())

	// 4. Group + derive suggestions (heuristic for MVP).
	suggestions, themes := deriveSuggestions(rows)

	// 5. Create the self-opt issue (lab_source='agent_self_optimization'
	// → automatically hidden from the main panel via exclude_lab).
	issueTitle := fmt.Sprintf("[self-opt] 学习报告 · %s",
		time.Now().Format("2006-01-02 15:04"))
	created, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID:  inputs.WorkspaceID,
		Title:        issueTitle,
		Description:  pgtype.Text{String: buildIssueDescription(rows, suggestions), Valid: true},
		Status:       "done", // immediately done; the row carries the report
		Priority:     "low",
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		// AssigneeID is the 智能体优化专家 agent — provisioned by
		// install_agent_self_opt.go on flag-on. Its UUID is a
		// deployment constant in experimental.AgentSelfOptimizationAgentID
		// so the runner does not need to look it up.
		AssigneeID: pgtype.UUID{
			Bytes: experimental.AgentSelfOptimizationAgentID(),
			Valid: true,
		},
		// CreatorType / CreatorID are plain SQL types (not pgtype).
		CreatorType: "agent",
		CreatorID: pgtype.UUID{
			Bytes: experimental.AgentSelfOptimizationAgentID(),
			Valid: true,
		},
		Stage:     pgtype.Int4{}, // not a stage; lab_source carries the badge
		LabSource: pgtype.Text{String: "agent_self_optimization", Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("create self-opt issue: %w", err)
	}

	// 6. Compose the report markdown.
	report := RunReport{
		WorkspaceID:      uuid.UUID(inputs.WorkspaceID.Bytes),
		StartedAt:        time.Now(),
		FinishedAt:       time.Now(),
		SourceIssueCount: len(rows),
		Suggestions:      suggestions,
		FailureThemes:    themes,
	}
	md := renderReportMarkdown(report)

	// 7. Write to KB (best-effort). A KB failure does not abort the run.
	var kbPath string
	if kb != nil {
		if path, kerr := kb.Append(ctx, uuid.UUID(inputs.WorkspaceID.Bytes), md); kerr != nil {
			slog.Warn("agent-self-opt: kb write failed",
				"workspace", inputs.WorkspaceID, "err", kerr)
		} else {
			kbPath = path
		}
	}

	return &RunResult{
		CreatedIssueID:    created.ID,
		SourceIssueCount:  len(rows),
		Report:            report,
		ReportMarkdown:    md,
		PromptSuggestions: suggestions,
		KBAppendixPath:    kbPath,
	}, nil
}

// scanDoneIssues pulls done issues since `since` for the workspace,
// filtered by SourceFilter. Returns the rows in updated_at DESC order.
//
// Implementation note: we use a SQL composition rather than a
// generated sqlc query because the NOT IN clause needs to be
// conditionally omitted (when no IDs are hidden, the WHERE becomes
// `WHERE assignee_id NOT IN ('00000000-...'::uuid)` which is harmless
// but verbose). Inline SQL keeps the runner's call site compact.
func scanDoneIssues(
	ctx context.Context,
	q *db.Queries,
	workspaceID pgtype.UUID,
	since time.Time,
	filter *SourceFilter,
) ([]db.ListDoneIssuesForSelfOptRow, error) {
	// The actual query lives in pkg/db/queries/agent_self_optimization.sql
	// as ListDoneIssuesForSelfOpt — added in this commit. We pass the
	// exclusion lists via the dedicated parameters.
	excludedIDs := filter.ExcludedAgentIDList()
	excludedNames := filter.ExcludedAgentNameList()

	rows, err := q.ListDoneIssuesForSelfOpt(ctx, db.ListDoneIssuesForSelfOptParams{
		WorkspaceID: workspaceID,
		UpdatedAt:   pgtype.Timestamptz{Time: since, Valid: true},
		Column3:     pgUUIDsFromUUIDs(excludedIDs),
		Column4:     excludedNames,
		Limit:       int32(MaxIssuesPerRun),
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// wordSplit splits a Chinese + English mix title into normalised
// tokens. Whitespace + punctuation are separators; CJK characters are
// each their own 1-gram token. Returned slice is sorted/deduped by the
// caller — this helper just emits the raw stream.
var wordSplit = regexp.MustCompile(`[\s\p{P}]+`)

func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := wordSplit.Split(text, -1)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

// deriveSuggestions runs the MVP heuristic: for every distinct agent
// in the issue set, count the top failure keywords in their assigned
// issues and emit a prompt_suggestion per agent.
func deriveSuggestions(rows []db.ListDoneIssuesForSelfOptRow) ([]PromptSuggestion, []FailureTheme) {
	type agentAccum struct {
		name    string
		id      uuid.UUID
		issues  []uuid.UUID
		titles  []string
	}
	byAgent := make(map[uuid.UUID]*agentAccum)
	for _, r := range rows {
		if !r.AssigneeID.Valid || r.AssigneeType.String != "agent" {
			continue
		}
		aid := uuid.UUID(r.AssigneeID.Bytes)
		acc, ok := byAgent[aid]
		if !ok {
			acc = &agentAccum{id: aid, name: r.AssigneeName.String}
			byAgent[aid] = acc
		}
		acc.issues = append(acc.issues, uuid.UUID(r.ID.Bytes))
		acc.titles = append(acc.titles, r.Title)
	}

	suggestions := make([]PromptSuggestion, 0, len(byAgent))
	for _, acc := range byAgent {
		top := topKeywords(acc.titles, 3)
		if len(top) == 0 {
			continue
		}
		suggestion := strings.Join(top, " / ")
		confidence := 0.4
		if len(acc.issues) >= 5 {
			confidence = 0.65
		}
		if len(acc.issues) >= 20 {
			confidence = 0.8
		}
		suggestions = append(suggestions, PromptSuggestion{
			AgentID:         acc.id,
			AgentName:       acc.name,
			SuggestedChange: fmt.Sprintf("在 system prompt 中显式覆盖以下高频关键词场景: %s", suggestion),
			Rationale:       fmt.Sprintf("过去窗口共 %d 个已完结 issue 与该智能体相关,关键词聚类提示该智能体在这些主题上有重复任务。", len(acc.issues)),
			Confidence:      confidence,
			BackingIssueIDs: acc.issues,
		})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Confidence > suggestions[j].Confidence
	})

	// Failure themes: aggregate keyword counts across ALL agents.
	allTitles := make([]string, 0, len(rows))
	for _, r := range rows {
		allTitles = append(allTitles, r.Title)
	}
	themes := clusterThemes(allTitles, rows)

	return suggestions, themes
}

// topKeywords returns the n most-frequent tokens across titles.
func topKeywords(titles []string, n int) []string {
	counts := make(map[string]int)
	for _, t := range titles {
		for _, tok := range tokenize(t) {
			if len(tok) < 2 {
				continue
			}
			counts[tok]++
		}
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	out := make([]string, 0, n)
	for i := 0; i < n && i < len(pairs); i++ {
		out = append(out, pairs[i].k)
	}
	return out
}

// clusterThemes returns the top 5 cross-agent keyword clusters with
// the sample issue IDs that contributed. Used by the renderer to draw
// a "high-frequency topic" card.
func clusterThemes(titles []string, rows []db.ListDoneIssuesForSelfOptRow) []FailureTheme {
	counts := make(map[string]int)
	sampleIDs := make(map[string][]uuid.UUID)
	for i, t := range titles {
		for _, tok := range tokenize(t) {
			if len(tok) < 2 {
				continue
			}
			counts[tok]++
			if i < len(rows) {
				sampleIDs[tok] = append(sampleIDs[tok], uuid.UUID(rows[i].ID.Bytes))
			}
		}
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(counts))
	for k, v := range counts {
		if v >= 2 { // skip single-occurrence noise
			pairs = append(pairs, kv{k, v})
		}
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })
	out := make([]FailureTheme, 0, 5)
	for i := 0; i < 5 && i < len(pairs); i++ {
		ids := sampleIDs[pairs[i].k]
		if len(ids) > 5 {
			ids = ids[:5]
		}
		out = append(out, FailureTheme{
			Theme:      pairs[i].k,
			IssueCount: pairs[i].v,
			SampleIDs:  ids,
		})
	}
	return out
}

// buildIssueDescription composes the markdown body the user sees in
// the issue panel. Keep it short — the full report is in the run row.
func buildIssueDescription(rows []db.ListDoneIssuesForSelfOptRow, suggestions []PromptSuggestion) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Self-opt 扫描窗口内共匹配到 %d 个已完结 issue。\n\n", len(rows))
	fmt.Fprintf(&b, "派生 %d 条 prompt 改进建议(置信度从高到低):\n", len(suggestions))
	for _, s := range suggestions {
		fmt.Fprintf(&b, "- **%s** (%.0f%%): %s\n", s.AgentName, s.Confidence*100, s.SuggestedChange)
	}
	b.WriteString("\n完整报告见「试验性功能 / 智能体自优化」面板。\n")
	return b.String()
}

// renderReportMarkdown serialises the report to markdown. The
// renderer (self-opt-history-view.tsx) parses by `## ` headings.
func renderReportMarkdown(r RunReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 智能体自优化报告\n\n")
	fmt.Fprintf(&b, "**工作区**: `%s`\n\n", r.WorkspaceID)
	fmt.Fprintf(&b, "**扫描窗口**: %s — %s\n\n",
		r.StartedAt.Format("2006-01-02 15:04"), r.FinishedAt.Format("2006-01-02 15:04"))
	fmt.Fprintf(&b, "**扫描源 issue 数**: %d\n\n", r.SourceIssueCount)

	b.WriteString("## 建议清单\n\n")
	if len(r.Suggestions) == 0 {
		b.WriteString("_本窗口内无可派生建议(issue 数过少)。_\n")
	} else {
		for _, s := range r.Suggestions {
			fmt.Fprintf(&b, "### %s _(置信度 %.0f%%)_\n\n", s.AgentName, s.Confidence*100)
			fmt.Fprintf(&b, "- **建议**: %s\n", s.SuggestedChange)
			fmt.Fprintf(&b, "- **依据**: %s\n", s.Rationale)
			if len(s.BackingIssueIDs) > 0 {
				fmt.Fprintf(&b, "- **样本数**: %d 个已完结 issue\n", len(s.BackingIssueIDs))
			}
			b.WriteString("\n")
		}
	}

	if len(r.FailureThemes) > 0 {
		b.WriteString("## 高频主题\n\n")
		for _, t := range r.FailureThemes {
			fmt.Fprintf(&b, "- **%s** (%d 个 issue 命中)\n", t.Theme, t.IssueCount)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n\n_由 agent_self_optimization 服务在 daemon 内自动生成,0.3.46+ 将接入 LLM rationale。_\n")
	return b.String()
}

// MarshalPromptSuggestions serialises suggestions for the JSONB column.
// Returned bytes are valid JSON (either `[]` or `[...]`).
func MarshalPromptSuggestions(s []PromptSuggestion) ([]byte, error) {
	if s == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(s)
}

// pgUUIDsFromUUIDs converts a slice of uuid.UUID to []pgtype.UUID for
// sqlc. The empty case returns a non-nil zero-length slice so the
// placeholder array still binds cleanly (matches experimental.UUIDsToPgtype
// but in the reverse direction — the runner prefers uuid.UUID values
// everywhere and only converts at the sqlc boundary).
func pgUUIDsFromUUIDs(ids []uuid.UUID) []pgtype.UUID {
	if ids == nil {
		return nil
	}
	out := make([]pgtype.UUID, len(ids))
	for i, id := range ids {
		out[i] = pgtype.UUID{Bytes: id, Valid: true}
	}
	return out
}