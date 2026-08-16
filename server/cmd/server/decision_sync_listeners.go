// Package main — decision_sync_listeners.go (0.5.22 Semantica × Multica Phase 2)
//
// Wires the terminal-issue → POST /api/decisions side-effect onto the
// in-process events bus. Mirrors the autopilot_listeners.go shape
// (Subscribe + status filter + goroutine-offload + bus-side panic
// recover) so the publish call site never blocks on a stuck Semantica
// POST.
//
// Hook contract:
//   - protocol.EventIssueUpdated with status_changed == true. The
//     issue row is re-read from the DB (never trusting the payload's
//     `issue` field, whose concrete type varies by emitter), then synced
//     only when row.Status is terminal AND row.LabSource == "semantica".
//   - protocol.EventTaskCompleted / EventTaskFailed / EventTaskCancelled
//     as a belt-and-braces fallback for the daemon.go:2583-2595 gap
//     where the agent finishes but the issue status has not flipped
//     yet. Same DB-driven re-read.
//
// Concurrency contract:
//   - events.Bus.Publish is SYNCHRONOUS (bus.go:67-77 invokes handlers
//     on the publisher's goroutine). This listener therefore does only
//     a single-row PK read (GetIssue) before returning; the HTTP POST
//     runs inside SyncIssueDecisionToSemantica's own goroutine, so a
//     slow Semantica server can never wedge the issue-update hot path.
//   - bus.go:69-73 recovers per-handler panics; the inner
//     postDecisionSync is also best-effort (slog.Warn on any error,
//     never panics) so a Semantica outage cannot crash the server.

package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// recentSyncDedup is the in-memory idempotency cache for terminal-issue
// decision sync. Each issue_id fires at most once per recentSyncTTL;
// the listener subscribes to FOUR events (issue:updated + task:completed/
// failed/cancelled) that all funnel into the same sync helper, and a
// terminal transition reliably fires 2-4 of them within a few hundred
// ms. The Semantica side is also idempotent on decision.id
// ("multica_<issue_uuid>") so this is belt-and-braces, not the
// authoritative dedup point.
//
// sync.Map is used for concurrent-safe Load/Store without an explicit
// mutex. Entries are evicted by startRecentSyncJanitor (every
// recentSyncTTL, see below) so memory stays bounded even when terminal
// issues accumulate over months of desktop uptime.
var recentSyncDedup sync.Map // map[string]time.Time

// recentSyncTTL bounds the dedup window. 5 minutes is plenty — a
// Multica issue transitions to terminal exactly once, and any
// replay within 5 min is the same logical event. Declared `var` (not
// `const`) so tests can shrink it to milliseconds without time.Sleep.
var recentSyncTTL = 5 * time.Minute

// shouldFireRecentSync returns true if the issue_id has not been
// synced within the recentSyncTTL window. Uses LoadOrStore so the
// "have I been here recently?" check + the "stamp now" update are
// atomic — without this, two goroutines hitting the same key within
// microseconds can both observe a fresh-enough slot and both return
// true (the 4-event fan-out reliably triggers this). Worst case is
// benign: Semantica dedupes by decision.id upstream, so duplicate
// POSTs collapse. We still pin the collapse-to-single-POST contract
// at the listener site so a future refactor cannot regress it.
func shouldFireRecentSync(issueID string) bool {
	now := time.Now()
	v, loaded := recentSyncDedup.LoadOrStore(issueID, now)
	if loaded {
		if last, ok := v.(time.Time); ok && now.Sub(last) < recentSyncTTL {
			return false
		}
		// Stale entry from a prior goroutine — overwrite and fire.
		recentSyncDedup.Store(issueID, now)
	}
	return true
}

// startRecentSyncJanitor evicts stale entries from recentSyncDedup so
// the map does not grow without bound. Runs forever (the desktop
// process exits cleanly on quit; no shutdown wiring required). The
// ticker interval matches recentSyncTTL so every entry is checked at
// least once past its expiry. Range + Delete is safe on sync.Map and
// does not block concurrent Load/Store.
func startRecentSyncJanitor() {
	ticker := time.NewTicker(recentSyncTTL)
	defer ticker.Stop()
	for range ticker.C {
		evictStaleSyncEntries()
	}
}

// evictStaleSyncEntries is the body of the janitor extracted for
// direct unit-test invocation. Range + Delete is safe on sync.Map.
// Wrong-type stored values (corruption) are silently skipped — the
// caller's worst case is the cache behaving as if every entry is
// missing, which only widens the dedup window without functional
// harm.
func evictStaleSyncEntries() {
	cutoff := time.Now().Add(-recentSyncTTL)
	recentSyncDedup.Range(func(k, v any) bool {
		if t, ok := v.(time.Time); ok && t.Before(cutoff) {
			recentSyncDedup.Delete(k)
		}
		return true
	})
}

// syncIssueRow bundles the pure decision: "given this hydrated issue
// row, should we sync it?" — split out of syncIfTerminal so the dedup
// gate, terminal-status filter, and lab-source filter are testable
// without a DB or HTTP stack.
//
// ORDER MATTERS: the terminal + lab-source filters run FIRST, and
// shouldFireRecentSync runs LAST. shouldFireRecentSync has the side
// effect of stamping the dedup slot — if it ran first, a non-terminal
// status_changed event (e.g. todo→in_progress at task dispatch) would
// consume the slot, and the real terminal transition a few minutes
// later (within recentSyncTTL) would be suppressed, silently dropping
// the decision POST. The dedup must only collapse events that would
// actually fire.
func syncIssueRow(issueID string, row db.Issue) bool {
	if !isTerminalStatus(row.Status) {
		return false
	}
	if !row.LabSource.Valid || row.LabSource.String != "semantica" {
		return false
	}
	return shouldFireRecentSync(issueID)
}

// registerDecisionSyncListeners wires the terminal-issue → Semantica
// side-effect onto the in-process event bus. Called from main.go
// alongside registerAutopilotListeners.
func registerDecisionSyncListeners(bus *events.Bus, h *handler.Handler, queries *db.Queries) {
	ctx := context.Background()

	bus.Subscribe(protocol.EventIssueUpdated, func(e events.Event) {
		maybeSyncFromIssueUpdated(ctx, h, queries, e)
	})
	bus.Subscribe(protocol.EventTaskCompleted, func(e events.Event) {
		maybeSyncFromTaskEvent(ctx, h, queries, e)
	})
	bus.Subscribe(protocol.EventTaskFailed, func(e events.Event) {
		maybeSyncFromTaskEvent(ctx, h, queries, e)
	})
	bus.Subscribe(protocol.EventTaskCancelled, func(e events.Event) {
		maybeSyncFromTaskEvent(ctx, h, queries, e)
	})

	// Janitor: bound the recentSyncDedup map. Runs forever (the desktop
	// process exits cleanly on quit, no shutdown wiring needed).
	go startRecentSyncJanitor()
}

// maybeSyncFromIssueUpdated handles the issue-status path. It re-reads
// the issue row from the DB rather than trusting the payload's `issue`
// field — UpdateIssue / BatchUpdateIssues embed a handler.IssueResponse
// struct, while other emitters pass a map, and the listener must not
// depend on either concrete shape.
func maybeSyncFromIssueUpdated(ctx context.Context, h *handler.Handler, q *db.Queries, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	statusChanged, _ := payload["status_changed"].(bool)
	if !statusChanged {
		return
	}
	issueID := extractIssueID(payload)
	if issueID == "" {
		return
	}
	syncIfTerminal(ctx, h, q, issueID, e)
}

// maybeSyncFromTaskEvent handles the task-terminal fallback path: the
// agent may finish (task:completed) before the issue status has been
// flipped to done. Re-reads the issue row and defers to the same
// shared sync helper.
func maybeSyncFromTaskEvent(ctx context.Context, h *handler.Handler, q *db.Queries, e events.Event) {
	payload, ok := e.Payload.(map[string]any)
	if !ok {
		return
	}
	issueID, _ := payload["issue_id"].(string)
	if issueID == "" {
		return
	}
	syncIfTerminal(ctx, h, q, issueID, e)
}

// syncIfTerminal loads the issue row and, when it is in a terminal
// status AND bound to the semantica lab, fires the Semantica decision
// POST. The single-row PK read is the only blocking work here; the
// HTTP POST itself runs inside SyncIssueDecisionToSemantica's own
// goroutine. Listener-side dedup (recentSyncDedup) collapses the 4
// subscribed events to a single POST per terminal transition.
//
// The dedup + status + lab-source gates live in syncIssueRow so they
// can be unit-tested without a DB. This function only handles the
// I/O (GetIssue + handler fire).
func syncIfTerminal(ctx context.Context, h *handler.Handler, q *db.Queries, issueID string, e events.Event) {
	issueUUID := parseUUIDOrZero(issueID)
	row, err := q.GetIssue(ctx, issueUUID)
	if err != nil {
		slog.Debug("decision_sync_listener: GetIssue failed",
			"issue_id", issueID, "error", err)
		return
	}
	if !syncIssueRow(issueID, row) {
		return
	}
	actorType, actorID := resolveActor(e)
	// Pass the already-loaded row through so the handler goroutine
	// does not re-read from the DB.
	h.SyncIssueDecisionToSemantica(row, row.Status, actorType, actorID)
}

// extractIssueID pulls the issue UUID from an issue:updated payload
// without assuming a concrete type for `issue` — it may be a
// handler.IssueResponse (value or pointer) from UpdateIssue /
// BatchUpdateIssues, or a plain map from other emitters. Falls back
// to a top-level `issue_id` key.
func extractIssueID(payload map[string]any) string {
	if id, ok := payload["issue_id"].(string); ok && id != "" {
		return id
	}
	switch iss := payload["issue"].(type) {
	case handler.IssueResponse:
		return iss.ID
	case *handler.IssueResponse:
		// Nil pointer is a realistic emission shape when a publisher
		// serializes via `&resp` after an error path that leaves resp
		// unset. Field access on a nil *struct panics, so guard before
		// dereferencing.
		if iss == nil {
			return ""
		}
		return iss.ID
	case map[string]any:
		id, _ := iss["id"].(string)
		return id
	}
	return ""
}

// isTerminalStatus mirrors the daemon-side terminal set. Mirrored
// here so the listener has no handler-package dependency on the
// status-transition helpers.
func isTerminalStatus(s string) bool {
	switch s {
	case "done", "closed", "cancelled":
		return true
	}
	return false
}

// resolveActor extracts actor_type / actor_id from the event envelope.
// Empty strings fall through; the helper tolerates them.
func resolveActor(e events.Event) (string, pgtype.UUID) {
	actorType := e.ActorType
	if actorType == "" {
		actorType = "system"
	}
	var actorID pgtype.UUID
	if e.ActorID != "" {
		if u, err := util.ParseUUID(e.ActorID); err == nil {
			actorID = u
		}
	}
	return actorType, actorID
}

// parseUUIDOrZero is a tiny helper that returns pgtype.UUID{} on
// invalid input instead of panicking. Mirrors the daemon's
// parseUUID toleration: a malformed workspace_id on a system event
// must not crash the listener.
func parseUUIDOrZero(s string) pgtype.UUID {
	if s == "" {
		return pgtype.UUID{}
	}
	u, err := util.ParseUUID(s)
	if err != nil {
		return pgtype.UUID{}
	}
	return u
}
