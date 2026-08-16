// Package handler — decision_sync.go (0.5.22 Semantica × Multica Phase 2)
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
// Payload shape (matches Semantica's DecisionRecord schema at
// vendor/semantica/semantica/decisions.py — keep in sync if that
// schema changes upstream):
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
type semanticaDecision struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Outcome     string   `json:"outcome"`
	Tags        []string `json:"tags"`
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
	if !experimental.DefaultFor("semantica") {
		return
	}
	upstreamURL := h.ExperimentRegistry.LoopbackURL("semantica")
	if upstreamURL == "" {
		slog.Debug("postDecisionSync: semantica subprocess not running, skipping",
			"issue_id", util.UUIDToString(row.ID))
		return
	}

	decision := buildSemanticaDecision(row, status, actorType, actorID)

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
		"issue_status", status)
}

// buildSemanticaDecision assembles the JSON envelope from an issue
// row + status. Pure function so the test can drive it without an
// HTTP roundtrip.
func buildSemanticaDecision(row db.Issue, status, actorType string, actorID pgtype.UUID) semanticaDecision {
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
