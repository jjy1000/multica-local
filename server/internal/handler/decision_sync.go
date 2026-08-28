// Package handler — decision_sync.go (0.5.22 Semantica × Multica Phase 2;
//                                  0.5.30 P1-2 schema pin;
//                                  0.5.54 P2 0.6.6 stability notes)
//
// Side-effect helper that POSTs a Semantica decision record when a
// Multica issue reaches a terminal state. Wired by
// cmd/server/decision_sync_listeners.go onto the in-process events
// bus (protocol.EventIssueUpdated + EventTaskCompleted / Failed /
// Cancelled) so the fire-and-forget semantics never wedge the publish
// call site.
//
// Design contract:
//   - Non-blocking: `go func()` so the bus's synchronous delivery
//     returns immediately even when the Semantica subprocess is slow
//     or down (mirrors integrations/lark/outbound.go:267 timeout
//     pattern).
//   - Failure-tolerant: every error path calls slog.Warn and returns;
//     we never panic, never propagate. A stuck Semantica server must
//     not wedge the issue-update hot path.
//   - Bounded: 10s per POST (semanticaDecisionTimeout). On timeout
//     the goroutine exits and a Warn is logged.
//   - Idempotent on the Semantica side: the decision `id` is
//     `multica_<issue_uuid>` so Semantica's own upsert-on-id path
//     dedupes repeated fires (the listener subscribes to multiple
//     events that may all see the same terminal transition).
//
// Payload shape (matches Semantica's POST /api/decisions contract;
// see packages/core/api/schemas.ts::SemanticaDecisionRecordSchema and
// the canonical reference in server/internal/service/builtin_skills/
// multica-semantica-decision-advisor/references/api-source-map.md):
//
//	{
//	  "id":          "multica_<issue_uuid>",
//	  "title":       "<issue.title>",
//	  "description": "<issue.description, truncated to 2000 runes>",
//	  "status":      "done" | "cancelled" | "closed",
//	  "outcome":     `Multica issue reached terminal status "<status>"`,
//	  "tags":        ["multica", "lab:semantica"],
//	  "provenance":  {
//	    "source":      "multica",
//	    "issue_id":    "<issue_uuid>",
//	    "workspace_id":"<workspace_uuid>",
//	    "actor_type":  "<system|user|agent>",
//	    "actor_id":    "<uuid-or-empty>",
//	    "occurred_at": "<RFC3339 UTC>"
//	  }
//	}
//
// 0.5.54 P2 notes (re: semantica-agi/semantica v0.6.6, vendored at
// apps/desktop/vendor/semantica-src/ via git subtree):
//   - DecisionRecord wire shape is unchanged from 0.5.30 P1-2; no Go
//     or TS schema extension is required.
//   - Upstream 0.6.6 made entity/relationship IRI minting deterministic
//     (SHA-256 in the declared https://semantica.dev/ns# namespace, was
//     Python's randomised hash() pre-0.6.6). Multica's wire envelope
//     already used `multica_<issue_uuid>` (SHA-stable on the fork
//     side); the fork sees zero behaviour change but exports are now
//     diff/join-able across restarts.
//   - Upstream 0.6.6 also published its first RDF vocabulary at
//     semantica/ontology/vocabulary/semantica-ns.ttl. Multica's wire
//     envelope does not yet emit sem:* predicates — P2 defers that
//     to P4 (ACL, which adds actor_type=team and exposes the
//     vocabulary to the per-actor subgraph view).
//   - The legacy `vendor/semantica/semantica/decisions.py` reference
//     that 0.5.30 P1-2 mentioned (a non-existent path) is no longer
//     referenced anywhere in the fork tree; the canonical location
//     is semantica/context/decision_models.py (the @dataclass
//     Decision / DecisionContext / Policy / Exception / Precedent /
//     ApprovalChain surface — the JSON shape we POST is a subset of
//     the Decision class).

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/experimental"
	causalgraph "github.com/multica-ai/multica/server/internal/service/causal_graph"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	// semanticaDecisionPath is the upstream REST path on the Semantica
	// subprocess that records a decision. Matches
	// semantica.decisions.DecisionRecord ingest route.
	semanticaDecisionPath = "/api/decisions"

	// semanticaDecisionTimeout caps each individual POST so a stuck
	// Semantica server cannot wedge the goroutine for the full HTTP
	// client default (no timeout). 10s matches the Lark outbound
	// pattern (internal/integrations/lark/outbound.go:267).
	semanticaDecisionTimeout = 10 * time.Second

	// semanticaDecisionDescriptionMax is the soft cap on the issue
	// description we forward to Semantica. Keeps the payload under
	// the typical 10 KB upstream ceiling with room for the envelope.
	semanticaDecisionDescriptionMax = 2000
)

// semanticaDecision is the JSON envelope POSTed to Semantica. Field
// names match the upstream DecisionRecord schema; tags are stable
// ("multica" + "lab:semantica") so Semantica queries can filter the
// corpus.
//
// 0.5.56 P4 adds the `visibility` field — the fork's per-decision
// ACL enum (team | individual_private | shared_team). The value is
// computed at write time by experimental.VisibilityFor and stamped
// into semantica_local_decision_acl by the write-through block at
// the end of postDecisionSync.
type semanticaDecision struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Outcome     string   `json:"outcome"`
	Tags        []string `json:"tags"`
	Visibility  string   `json:"visibility,omitempty"`
	Provenance  struct {
		Source      string `json:"source"`
		IssueID     string `json:"issue_id"`
		WorkspaceID string `json:"workspace_id"`
		ActorType   string `json:"actor_type"`
		ActorID     string `json:"actor_id,omitempty"`
		OccurredAt  string `json:"occurred_at"`
	} `json:"provenance"`
}

// SyncIssueDecisionToSemantica fires-and-forgets a POST to the
// Semantica subprocess's /api/decisions endpoint when a Multica issue
// reaches a terminal state. Intended to be called from an event-bus
// Subscribe callback (see cmd/server/decision_sync_listeners.go).
//
// The caller passes the issue row directly so we don't re-read from
// the DB inside the goroutine — the listener has already hydrated it.
// The row is copied by value (db.Issue is a plain struct), so the
// goroutine sees a stable snapshot even if the caller mutates state.
//
// No-op when:
//   - h is nil (test fixtures).
//   - h.ExperimentRegistry is nil (test fixtures).
//   - The semantica flag is disabled (experimental.DefaultFor returns
//     false). We never post when the lab is off so a flip-back
//     retroactively does not flood Semantica.
//   - The Semantica subprocess is not running (LoopbackURL returns
//     ""); we log at Debug so the common cold-boot-before-launch
//     state is not noise.
//
// Concurrency: this function spawns one goroutine per call. The caller
// (bus listener) returns immediately. Failures are logged, never
// returned. The listener also dedupes by issue_id (5-minute TTL) so
// the 4 subscribed events (issue:updated + task:completed/failed/
// cancelled) collapse to a single POST per terminal transition.
func (h *Handler) SyncIssueDecisionToSemantica(
	row db.Issue,
	status string,
	actorType string,
	actorID pgtype.UUID,
) {
	if h == nil {
		return
	}
	go h.postDecisionSync(row, status, actorType, actorID)
}

// postDecisionSync is the goroutine body of SyncIssueDecisionToSemantica.
// Factored out so a test can drive the synchronous portion directly
// (callers that want synchronous fire-forget still go through the
// public method). Uses the passed-in row directly — no DB read here,
// the listener has already hydrated it.
//
// 0.5.56 P4: stamps Visibility into the envelope and writes through
// to semantica_local_decision_acl after the upstream POST succeeds.
// Member-count failure falls back to ModeIndividual (default-safe)
// so a transient DB hiccup never loses a decision; the warn log
// carries the actor_id + workspace_id for triage.
func (h *Handler) postDecisionSync(
	row db.Issue,
	status string,
	actorType string,
	actorID pgtype.UUID,
) {
	ctx, cancel := context.WithTimeout(context.Background(), semanticaDecisionTimeout)
	defer cancel()

	if h.ExperimentRegistry == nil {
		return
	}
	// Per-user flag gating happens at the API layer (router middleware
	// RequireExperimentalFlag). This internal helper deliberately does
	// NOT re-check via experimental.DefaultFor — that helper reads only
	// Catalog.DefaultVal (false for every built-in lab) and would
	// silently drop decisions for every per-user enabled semantica
	// tenant. (0.5.61 audit fix.)
	upstreamURL := h.ExperimentRegistry.LoopbackURL("semantica")
	if upstreamURL == "" {
		slog.Debug("postDecisionSync: semantica subprocess not running, skipping",
			"issue_id", util.UUIDToString(row.ID))
		return
	}

	// Compute mode + visibility from the workspace's member count.
	// Failure path: default to individual mode (default-safe — never
	// leaks a team decision by mistake on a DB hiccup).
	mode := experimental.ModeIndividual
	if h.Queries != nil {
		count, err := experimental.WorkspaceMemberCount(ctx, h.Queries, row.WorkspaceID)
		if err == nil {
			mode = experimental.Mode(count)
		} else {
			slog.Warn("postDecisionSync: workspace member count failed; defaulting to individual",
				"issue_id", util.UUIDToString(row.ID),
				"workspace_id", util.UUIDToString(row.WorkspaceID),
				"error", err)
		}
	}
	visibility := experimental.VisibilityFor(mode, actorType)

	decision := buildSemanticaDecision(row, status, actorType, actorID, visibility)

	payload, err := json.Marshal(decision)
	if err != nil {
		slog.Warn("postDecisionSync: marshal decision failed",
			"issue_id", util.UUIDToString(row.ID),
			"error", err)
		return
	}

	url := upstreamURL + semanticaDecisionPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		slog.Warn("postDecisionSync: build request failed",
			"issue_id", util.UUIDToString(row.ID),
			"error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Marker so the Semantica side can recognise an embedded fire and
	// skip rate-limit / IP-allowlist enforcement when applicable.
	req.Header.Set("X-Multica-Embedded", "1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Warn("postDecisionSync: POST failed",
			"issue_id", util.UUIDToString(row.ID),
			"url", url,
			"error", err)
		return
	}
	defer resp.Body.Close()
	// Drain the body so the keep-alive connection can be reused.
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		slog.Warn("postDecisionSync: non-2xx response",
			"issue_id", util.UUIDToString(row.ID),
			"url", url,
			"status", resp.StatusCode)
		return
	}
	slog.Info("postDecisionSync: recorded decision",
		"issue_id", util.UUIDToString(row.ID),
		"decision_id", decision.ID,
		"issue_status", status,
		"visibility", visibility,
		"mode", mode)

	// 0.5.83 WL3 Tier B: mirror the exported decision BACK into the
	// causal graph as a decision node (roadmap §3.0 Tier B — the
	// semantica listener above stays exactly as-is; this is the second,
	// causal_graph-flag-scoped write). Best-effort and gated: when the
	// causal_graph flag is off (or the mirror errors) nothing here can
	// affect the decision export. The mirror rides the same goroutine,
	// after the upstream 2xx, so a mirrored node implies a delivered
	// decision.
	h.mirrorDecisionToCausalGraph(row, status, actorType, actorID, decision.ID)

	// Write-through to semantica_local_decision_acl. Best-effort: a
	// transient DB error here does NOT roll back the upstream POST
	// (the upstream is the source of truth — its own upsert-on-id
	// dedupes repeated fires). The ACL index catches up on the next
	// reconciliation cycle (P6 territory).
	//
	// 0.5.60 (audit hole #2): the error used to be discarded with
	// `_ =` — the ONLY truly silent failure in the semantica path.
	// Since the P6 reconciler body is observability-only, a missed
	// ACL row stayed invisible until upstream exposes a decisions-list
	// API. Log it.
	if h.Queries != nil && visibility != "" {
		actorIDStr := experimental.ActorIDFor(actorType, actorID, row.WorkspaceID)
		upsertCtx, upsertCancel := context.WithTimeout(context.Background(), semanticaDecisionTimeout)
		if err := h.Queries.UpsertSemanticaDecisionACL(upsertCtx, db.UpsertSemanticaDecisionACLParams{
			DecisionID:  decision.ID,
			WorkspaceID: row.WorkspaceID,
			ActorType:   actorType,
			ActorID:     actorIDStr,
			Visibility:  visibility,
		}); err != nil {
			slog.Warn("postDecisionSync: ACL upsert failed (upstream decision was recorded; ACL index will lag until reconcile)",
				"issue_id", util.UUIDToString(row.ID),
				"decision_id", decision.ID,
				"error", err)
		}
		upsertCancel()
	}
}

// buildSemanticaDecision assembles the JSON envelope from an issue
// row + status. Pure function so the test can drive it without an
// HTTP roundtrip. The visibility string is pre-computed by the
// caller (postDecisionSync) so this helper stays pure.
func buildSemanticaDecision(row db.Issue, status, actorType string, actorID pgtype.UUID, visibility string) semanticaDecision {
	desc := ""
	if row.Description.Valid {
		desc = row.Description.String
	}
	// Rune-aware truncation: byte-indexed slice on a UTF-8 string can
	// split a multi-byte sequence mid-codepoint, producing an invalid
	// UTF-8 payload that json.Marshal silently replaces with U+FFFD
	// for every byte that follows the cut. Slicing on []rune keeps
	// every codepoint whole.
	if utf8.RuneCountInString(desc) > semanticaDecisionDescriptionMax {
		runes := []rune(desc)
		desc = string(runes[:semanticaDecisionDescriptionMax]) + "…"
	}
	d := semanticaDecision{
		ID:          "multica_" + util.UUIDToString(row.ID),
		Title:       row.Title,
		Description: desc,
		Status:      status,
		Outcome:     fmt.Sprintf("Multica issue reached terminal status %q", status),
		Tags:        []string{"multica", "lab:semantica"},
		Visibility:  visibility,
	}
	// Fallback for empty title: Semantica's downstream treats empty
	// titles as unsearchable. The UUID-based fallback keeps the record
	// traceable to the source issue even when the human-visible title
	// was never set.
	if d.Title == "" {
		d.Title = "Untitled issue " + util.UUIDToString(row.ID)
	}
	d.Provenance.Source = "multica"
	d.Provenance.IssueID = util.UUIDToString(row.ID)
	d.Provenance.WorkspaceID = util.UUIDToString(row.WorkspaceID)
	d.Provenance.ActorType = actorType
	if actorID.Valid {
		d.Provenance.ActorID = util.UUIDToString(actorID)
	}
	d.Provenance.OccurredAt = time.Now().UTC().Format(time.RFC3339)
	return d
}

// mirrorDecisionToCausalGraph is the Tier B mirror body (0.5.83 WL3,
// roadmap §3.0). Factored out of postDecisionSync so the causal-graph
// write stays out of the decision-export fast path's error contract:
// nil queries, flag-off, and every write error are swallowed with a
// WRN — the decision export already succeeded by the time this runs.
//
// The decision node dedups on provenance dedup_key "decision:<id>"
// (the export's decision id is deterministic per issue: multica_<uuid>),
// so a re-export of the same issue collapses onto the existing node —
// only last_observed_at staleness is possible, which the maintenance
// ticker reconciles (S2 phase).
func (h *Handler) mirrorDecisionToCausalGraph(
	row db.Issue,
	status string,
	actorType string,
	actorID pgtype.UUID,
	decisionID string,
) {
	if h == nil || h.Queries == nil {
		return
	}
	rec := causalgraph.New(h.Queries)
	if !rec.Enabled(context.Background()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), semanticaDecisionTimeout)
	defer cancel()

	dedupKey := "decision:" + decisionID
	if _, err := h.Queries.FindCausalNodeByDedupKey(ctx, db.FindCausalNodeByDedupKeyParams{
		WorkspaceID: row.WorkspaceID,
		DedupKey:    dedupKey,
	}); err == nil {
		return
	}

	if _, err := h.Queries.CreateCausalNode(ctx, db.CreateCausalNodeParams{
		WorkspaceID: row.WorkspaceID,
		IssueID:     pgtype.UUID{Valid: true, Bytes: row.ID.Bytes},
		NodeType:    "decision",
		Label:       truncateCausalLabel(row.Title, 120),
		Provenance: mustJSONCausal(map[string]string{
			"source":      "semantica_decision_sync",
			"dedup_key":   dedupKey,
			"decision_id": decisionID,
			"issue_id":    util.UUIDToString(row.ID),
			"actor_type":  actorType,
			"actor_id":    util.UUIDToString(actorID),
			"status":      status,
		}),
		CreatedBy: pgtype.Text{Valid: true, String: "system"},
	}); err != nil {
		slog.Warn("causal mirror: decision node write failed",
			"issue_id", util.UUIDToString(row.ID),
			"decision_id", decisionID,
			"error", err)
	}
}

func truncateCausalLabel(s string, max int) string {
	if utf8.RuneCountInString(s) > max {
		runes := []rune(s)
		return string(runes[:max]) + "…"
	}
	return s
}

func mustJSONCausal(v map[string]string) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}
