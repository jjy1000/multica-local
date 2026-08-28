// Package causalgraph — deterministic curator scan (0.5.83 WL3 S2,
// roadmap §3.3 item 13).
//
// The LLM curator is the hidden causal_graph_curator agent driven by
// the multica-causal-graph-curator skill (tier D via the POST
// /api/causal-graph/suggestions gate). This file is the LLM-FREE
// complement: a deterministic pattern scan over a comment window that
// proposes issue-level depends_on edges the same gated way. Both feed
// the same trust ladder — everything lands status='suggested',
// proposed_by='curator', confidence ≤ 0.5, and the nightly evolver
// decides when the scan runs.
//
// Extraction grammar (deliberately conservative):
//
//	<predicate> <ISSUE-REF>
//	  predicates: "depends on" / "blocked by" / "依赖" / "阻塞于" /
//	              "取决于" / "被阻塞"
//	  ISSUE-REF:  <PREFIX>-<number> (the workspace issue_prefix, e.g.
//	              MULT-12), matched case-insensitively
//
// Only depends_on is proposed — "blocks"-style inversions and causal
// language ("caused by") are too ambiguous for a deterministic parser
// and stay the LLM curator's job. A reference resolves only when the
// prefix matches the source issue's own workspace and the number maps
// to a real issue there; cross-workspace and self references are
// dropped. Every proposal probes FindCausalEdgeBetween first, so a
// confirmed OR tombstoned (rejected) pair is never re-proposed —
// ICP-5 never-nag.
package causalgraph

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// CuratorScanConfidence is the fixed confidence for deterministic
// scan proposals (below the 0.5 tier-D ceiling by construction).
const CuratorScanConfidence = "0.400"

// predicateWindow is how much text before a reference the predicate
// search looks at (characters, lowercased).
const predicateWindow = 32

// issueRefPattern matches <PREFIX>-<number> (e.g. MULT-12). The prefix
// is letters/digits starting with a letter but is only ACCEPTED when
// it equals the source workspace's issue_prefix, so junk like
// "IPv4-1" resolves to nothing.
var issueRefPattern = regexp.MustCompile(`([A-Za-z][A-Za-z0-9]*)-(\d+)`)

// depPredicates are the "source depends on target" markers searched
// in the lowercased text preceding a reference. Order is irrelevant —
// first hit wins.
var depPredicates = []string{
	"depends on", "blocked by", "依赖", "阻塞于", "取决于", "被阻塞",
}

// Curator runs the deterministic comment-window scan. Pool answers
// the cross-table window queries (comment × issue × workspace);
// Queries writes the graph through the same sqlc surface as the
// Tier A recorder.
type Curator struct {
	Pool    *pgxpool.Pool
	Queries *db.Queries
}

func NewCurator(pool *pgxpool.Pool, q *db.Queries) *Curator {
	return &Curator{Pool: pool, Queries: q}
}

// scanCandidate is one extracted (source issue --depends_on--> target
// ref) mention before resolution.
type scanCandidate struct {
	srcIssue  pgtype.UUID
	srcTitle  string
	commentID pgtype.UUID
	refNumber string
}

// ScanWindow walks every comment created in (since, until], extracts
// dependency mentions, and proposes suggested edges. Returns the
// number of proposals WRITTEN (probes, self-refs and unresolved refs
// don't count). Per-candidate errors are logged and skipped — one bad
// row never aborts the pass.
func (c *Curator) ScanWindow(ctx context.Context, since, until time.Time) (int, error) {
	rows, err := c.Pool.Query(ctx, `
		SELECT i.id, i.title, w.issue_prefix, c.id, c.content
		FROM comment c
		JOIN issue i ON i.id = c.issue_id
		JOIN workspace w ON w.id = i.workspace_id
		WHERE c.created_at > $1 AND c.created_at <= $2
		ORDER BY c.created_at ASC
	`, since, until)
	if err != nil {
		return 0, err
	}
	type commentRow struct {
		issueID   pgtype.UUID
		title     string
		wsPrefix  string
		commentID pgtype.UUID
		content   string
	}
	var comments []commentRow
	for rows.Next() {
		var r commentRow
		if err := rows.Scan(&r.issueID, &r.title, &r.wsPrefix, &r.commentID, &r.content); err != nil {
			continue
		}
		comments = append(comments, r)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}

	var candidates []scanCandidate
	for _, r := range comments {
		prefix := strings.ToLower(r.wsPrefix)
		lower := strings.ToLower(r.content)
		for _, loc := range issueRefPattern.FindAllStringSubmatchIndex(r.content, -1) {
			refPrefix := strings.ToLower(r.content[loc[2]:loc[3]])
			if refPrefix != prefix {
				continue // cross-workspace or junk reference — drop
			}
			start := loc[0] - predicateWindow
			if start < 0 {
				start = 0
			}
			before := lower[start:loc[0]]
			matched := false
			for _, pred := range depPredicates {
				if strings.Contains(before, pred) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
			candidates = append(candidates, scanCandidate{
				srcIssue:  r.issueID,
				srcTitle:  r.title,
				commentID: r.commentID,
				refNumber: r.content[loc[4]:loc[5]],
			})
		}
	}

	proposed := 0
	seen := map[string]bool{}
	for _, cand := range candidates {
		key := util.UUIDToString(cand.srcIssue) + ">" + cand.refNumber
		if seen[key] {
			continue // same source mentions the same target twice in the window
		}
		seen[key] = true
		ok, err := c.proposeDependency(ctx, cand)
		if err != nil {
			slog.Warn("curator scan proposal failed",
				"src_issue", util.UUIDToString(cand.srcIssue),
				"ref_number", cand.refNumber,
				"error", err)
			continue
		}
		if ok {
			proposed++
		}
	}
	return proposed, nil
}

// proposeDependency resolves the reference to an issue in the source
// issue's workspace, ensures both root nodes, probes for an existing
// edge (any status), and writes the suggested proposal. Returns false
// when the pair was skipped (self-ref, unknown target, already
// decided).
func (c *Curator) proposeDependency(ctx context.Context, cand scanCandidate) (bool, error) {
	var dstIssue, workspaceID pgtype.UUID
	err := c.Pool.QueryRow(ctx, `
		SELECT id, workspace_id FROM issue
		WHERE workspace_id = (SELECT workspace_id FROM issue WHERE id = $1)
		  AND number = $2
		LIMIT 1
	`, cand.srcIssue.Bytes, cand.refNumber).Scan(&dstIssue, &workspaceID)
	if err != nil {
		return false, nil // unknown reference — not an error
	}
	if util.UUIDToString(dstIssue) == util.UUIDToString(cand.srcIssue) {
		return false, nil
	}

	srcRoot, err := c.ensureIssueRoot(ctx, workspaceID, cand.srcIssue, cand.srcTitle)
	if err != nil {
		return false, err
	}
	dstRoot, err := c.ensureIssueRoot(ctx, workspaceID, dstIssue, "")
	if err != nil {
		return false, err
	}
	if _, err := c.Queries.FindCausalEdgeBetween(ctx, db.FindCausalEdgeBetweenParams{
		FromNodeID: srcRoot,
		ToNodeID:   dstRoot,
		EdgeType:   "depends_on",
	}); err == nil {
		return false, nil // decided before (active or tombstone) — never re-propose
	}

	if _, err := c.Queries.CreateCausalEdge(ctx, db.CreateCausalEdgeParams{
		WorkspaceID: workspaceID,
		FromNodeID:  srcRoot,
		ToNodeID:    dstRoot,
		EdgeType:    "depends_on",
		Confidence:  scanNumeric(CuratorScanConfidence),
		Provenance: mustJSON(map[string]string{
			"source":     "curator_scan",
			"dedup_key":  "curator_dep:" + util.UUIDToString(cand.srcIssue) + ":" + util.UUIDToString(dstIssue),
			"comment_id": util.UUIDToString(cand.commentID),
		}),
		CreatedBy:  pgtype.Text{Valid: true, String: "system"},
		ProposedBy: pgtype.Text{Valid: true, String: "curator"},
		EdgeStatus: pgtype.Text{Valid: true, String: "suggested"},
	}); err != nil {
		return false, err
	}
	return true, nil
}

// ensureIssueRoot returns the issue's root constraint node, creating
// it once if absent. The provenance shape matches the Tier A
// recorder's root fallback exactly, so both paths land on the SAME
// node via the mig 279 dedup index.
func (c *Curator) ensureIssueRoot(ctx context.Context, workspaceID, issueID pgtype.UUID, title string) (pgtype.UUID, error) {
	rootKey := dedupIssueRoot + util.UUIDToString(issueID)
	if root, err := c.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: workspaceID,
		DedupKey:    rootKey,
	}); err == nil {
		return root.ID, nil
	}
	label := title
	if label == "" {
		// Resolve the title lazily for the target side; fall back to
		// the number if the row vanished mid-pass.
		var t string
		if err := c.Pool.QueryRow(ctx,
			`SELECT title FROM issue WHERE id = $1`, issueID.Bytes).Scan(&t); err == nil && t != "" {
			label = t
		}
	}
	if label == "" {
		label = "issue " + util.UUIDToString(issueID)
	}
	root, err := c.Queries.CreateCausalNode(ctx, db.CreateCausalNodeParams{
		WorkspaceID: workspaceID,
		IssueID:     pgtype.UUID{Valid: true, Bytes: issueID.Bytes},
		NodeType:    "constraint",
		Label:       truncateLabel(label, 120),
		Provenance: mustJSON(map[string]string{
			"source":    "issue_root",
			"dedup_key": rootKey,
			"issue_id":  util.UUIDToString(issueID),
		}),
		CreatedBy: pgtype.Text{Valid: true, String: "system"},
	})
	if err != nil {
		return pgtype.UUID{}, err
	}
	return root.ID, nil
}

// scanNumeric builds a pgtype.Numeric from a decimal string (the
// confidence literals in this file are fixed decimals, so string
// parsing is exact — no float rounding surprises).
func scanNumeric(s string) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(s)
	return n
}
