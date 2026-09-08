package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/experimental"
	"github.com/multica-ai/multica/server/internal/issueguard"
	"github.com/multica-ai/multica/server/internal/issuestatus"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// IssueResponse is the JSON response for an issue.
type IssueResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	Number      int32   `json:"number"`
	Identifier  string  `json:"identifier"`
	Title       string  `json:"title"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	// StatusCategory is the canonical status whose platform behavior Status
	// carries — identical to Status for the 7 built-ins, and the inherited
	// category for a custom status. Omitted when the endpoint does not resolve
	// it, so consumers must fall back to Status rather than assume a blank
	// value means "no category". (MUL-6243)
	StatusCategory string `json:"status_category,omitempty"`
	// StatusName is a CUSTOM status's display name, carried beside the key so a
	// consumer that only ever sees `status` is not left holding a bare handle.
	// A key derived from a non-Latin name is opaque by construction
	// (`in_review_2`), and an agent reading an issue has nothing else to match
	// against the status a human named for it.
	//
	// Always emitted, unlike StatusCategory. Empty is a MEANING here — "this is
	// a built-in, localize it from the key" — not the "this endpoint did not
	// resolve it" that an absent status_category signals. (MUL-6749)
	StatusName    string  `json:"status_name"`
	Priority      string  `json:"priority"`
	AssigneeType  *string `json:"assignee_type"`
	AssigneeID    *string `json:"assignee_id"`
	CreatorType   string  `json:"creator_type"`
	CreatorID     string  `json:"creator_id"`
	ParentIssueID *string `json:"parent_issue_id"`
	ProjectID     *string `json:"project_id"`
	Position      float64 `json:"position"`
	// Stage groups sub-issues under the same parent into ordered barrier
	// groups (null = unstaged). See issue_child_done.go for how a closed
	// stage gates the child-done -> parent wake.
	Stage     *int32  `json:"stage"`
	StartDate *string `json:"start_date"`
	DueDate   *string `json:"due_date"`
	// LabSource associates this issue with an experimental lab flag key
	// (e.g. "claude_science_lab"). Null means no lab association.
	LabSource *string `json:"lab_source"`
	// LabMode (0.3.31) — mythos_swarm dual-mode. Only meaningful when
	// LabSource="mythos_swarm". "sole" = mythos owns the issue end-to-end;
	// "enhancer" = mythos preludes + supervises, the user-picked assignee
	// executes. Null means no mode set (treats as "sole").
	LabMode   *string `json:"lab_mode"`
	CreatedAt string  `json:"created_at"`
	UpdatedAt string  `json:"updated_at"`
	// Metadata is the per-issue KV map (see issue_metadata.go). Always emitted
	// (empty object when unset) so frontend code can `issue.metadata[key]`
	// without nil-guarding the parent field.
	Metadata    map[string]any          `json:"metadata"`
	Reactions   []IssueReactionResponse `json:"reactions,omitempty"`
	Attachments []AttachmentResponse    `json:"attachments,omitempty"`
	// Labels are bulk-attached by list/detail endpoints so the client can render
	// chips without an N+1 round-trip per row. Pointer + omitempty so paths that
	// don't load labels (e.g. UpdateIssue, batch UpdateIssues, the issue:updated
	// WS broadcast) emit no `labels` field at all — the client merge then
	// preserves whatever labels are already in cache. nil pointer = "field
	// absent, do not touch"; non-nil (incl. empty slice) = authoritative list.
	Labels *[]LabelResponse `json:"labels,omitempty"`
}

// validIssuePriorities mirrors the CHECK constraint on the issue table. Write
// handlers pre-validate it so callers get a clean 400 with the allowed values
// instead of a database CHECK violation bubbling up as a 500.
var validIssuePriorities = []string{"urgent", "high", "medium", "low", "none"}

// validIssueStatuses is the 7 BUILT-IN status keys. Since MUL-6243 it is no
// longer the set of writable statuses — write paths validate against the
// workspace's catalog via resolveIssueStatusKey — and it survives only for the
// issue-table grouping/filtering paths, which key their group descriptors and
// compound cells off a fixed status list.
//
// KNOWN LIMITATION: a custom status is therefore not yet selectable as an
// issue-table group or filter value. That is a self-contained follow-up (the
// table's group descriptors and compound cell keys need to become catalog
// driven); it is scoped out here so this change cannot alter the table view for
// workspaces that have no custom statuses.
var validIssueStatuses = issuestatus.Canonical()

// resolveIssueStatusKey checks a status against the workspace's catalog and
// returns the CANONICAL key to store. This is the application-layer replacement
// for the enum CHECK that upstream migration 337 dropped, so every write path
// must route through it — a missed entrypoint is how an unresolvable key would
// reach the column.
//
// Returning the resolved key (rather than a bare bool) is load-bearing:
// resolution is case- and whitespace-insensitive, so `"  HUMAN_REVIEW "` and
// `"human_review"` both validate. Writing the caller's raw string back would
// then store a value the column's format constraint rejects, turning an input
// the API just accepted into a 500. Callers must persist what this returns.
func (h *Handler) resolveIssueStatusKey(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, status string) (string, bool) {
	key, _, ok := h.resolveIssueStatusKeyKind(w, r, workspaceID, status)
	return key, ok
}

// resolveIssueStatusKeyKind is resolveIssueStatusKey plus whether the target is
// a CUSTOM status. Callers use that to decide whether the write needs the
// shared catalog lock — see runWithIssueStatusGuard.
func (h *Handler) resolveIssueStatusKeyKind(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID, status string) (string, bool, bool) {
	entry, err := issuestatus.Resolve(r.Context(), h.Queries, workspaceID, status)
	if err != nil {
		if errors.Is(err, issuestatus.ErrUnknownStatus) {
			// Labels, not bare keys: a derived key says nothing about what the
			// status means, so listing `in_review_2` alone leaves the caller no
			// way to find the one they were told to use. (MUL-6749)
			allowed, listErr := issuestatus.ActiveKeyLabels(r.Context(), h.Queries, workspaceID)
			if listErr != nil || len(allowed) == 0 {
				allowed = issuestatus.Canonical()
			}
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"invalid status %q; valid values: %s", status, strings.Join(allowed, ", ")))
			return "", false, false
		}
		slog.Warn("resolve issue status failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to validate status")
		return "", false, false
	}
	return entry.Key, !entry.IsSystem, true
}

// errIssueStatusArchivedRace signals that the target custom status was archived
// between the request's pre-flight validation and the write itself. Callers map
// it to 409: the request was valid when it arrived, and retrying against the
// refreshed catalog is the right remedy.
var errIssueStatusArchivedRace = errors.New("issue status was archived while the write was in flight")

// assertIssueStatusStillActive is the write half of the archive race guard. It
// takes the SHARED catalog lock and RE-RESOLVES the status inside the caller's
// transaction.
//
// The re-resolve is the part that actually closes the race. Handlers resolve a
// status up front, before any transaction, to answer a bad request with a clean
// 400 — but an archive can commit between that pre-flight check and the write.
// Re-checking here, under the lock, means the status is provably active at the
// moment the row is written. ArchiveIssueStatus holds the EXCLUSIVE side around
// its in-use census, so the two orderings are both covered:
//
//   - archive first: it commits, this re-resolve then fails and the write is
//     rejected, so no issue is stranded on an archived status;
//   - writer first: the census blocks until this transaction commits, then sees
//     the issue and refuses the archive with a conflict.
//
// A built-in status is a no-op: it can never be archived (enforced by
// issue_status_system_not_archivable), so the common path takes no lock and
// pays nothing. (MUL-6243)
func assertIssueStatusStillActive(ctx context.Context, qtx *db.Queries, workspaceID pgtype.UUID, statusKey string) error {
	if statusKey == "" || issuestatus.IsBuiltIn(statusKey) {
		return nil
	}
	// Catalog lock before any row lock, everywhere, so the two write paths
	// cannot deadlock against each other.
	if err := qtx.LockIssueStatusCatalogShared(ctx, workspaceID); err != nil {
		return err
	}
	if _, err := issuestatus.Resolve(ctx, qtx, workspaceID, statusKey); err != nil {
		if errors.Is(err, issuestatus.ErrUnknownStatus) {
			return errIssueStatusArchivedRace
		}
		return err
	}
	return nil
}

// runWithIssueStatusGuard runs an issue write that lands on a custom status
// inside a transaction that re-verifies the status under the shared catalog
// lock (see assertIssueStatusStillActive). A built-in target skips the
// transaction entirely.
func (h *Handler) runWithIssueStatusGuard(ctx context.Context, workspaceID pgtype.UUID, statusKey string, fn func(q *db.Queries) error) error {
	if statusKey == "" || issuestatus.IsBuiltIn(statusKey) {
		return fn(h.Queries)
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	qtx := h.Queries.WithTx(tx)
	if err := assertIssueStatusStillActive(ctx, qtx, workspaceID, statusKey); err != nil {
		return err
	}
	if err := fn(qtx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// writeIssueStatusRaceError renders errIssueStatusArchivedRace as a 409 and
// reports whether it handled the error.
func writeIssueStatusRaceError(w http.ResponseWriter, err error) bool {
	if errors.Is(err, errIssueStatusArchivedRace) {
		writeError(w, http.StatusConflict,
			"the target status was archived while this request was in flight; reload the status list and retry")
		return true
	}
	return false
}

func validateIssueEnum(w http.ResponseWriter, field, value string, allowed []string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid %s %q; valid values: %s", field, value, strings.Join(allowed, ", ")))
	return false
}

// fillStatusCategories resolves status_category for responses whose status is
// CUSTOM. The pure builders below fill it for built-in keys — where key IS the
// category — and leave it empty otherwise, so this is the step that makes the
// field authoritative on every payload a client caches or buckets by.
//
// Uses one Resolver for the whole slice: built-in statuses cost no query, and a
// page full of custom ones costs one catalog read rather than one per row. The
// Resolver includes ARCHIVED statuses, because an issue left on an archived
// status still belongs in its category's column. (MUL-6243)
func (h *Handler) fillStatusCategories(ctx context.Context, wsID pgtype.UUID, resps []IssueResponse) {
	fill := h.newStatusCategoryFiller(ctx, wsID)
	for i := range resps {
		fill(&resps[i])
	}
}

// newStatusCategoryFiller returns a request-scoped filler backed by ONE
// Resolver. Reuse it across every response a request builds: the Resolver reads
// the catalog at most once, so a page of custom-status rows costs one query
// rather than one per row. Creating a filler per row would reintroduce the N+1
// this exists to avoid. (MUL-6243)
func (h *Handler) newStatusCategoryFiller(ctx context.Context, wsID pgtype.UUID) func(*IssueResponse) {
	resolver := issuestatus.NewResolver(wsID)
	return func(resp *IssueResponse) {
		if resp == nil || resp.StatusCategory != "" {
			return
		}
		resp.StatusCategory = resolver.Effective(ctx, h.Queries, resp.Status)
		// Same Resolver, same single catalog read, so the name rides along for
		// free. Built-ins return "" and stay omitted. (MUL-6749)
		resp.StatusName = resolver.Name(ctx, h.Queries, resp.Status)
	}
}

// fillStatusCategory is the single-response form. Only for endpoints that build
// exactly ONE response; anything looping must use newStatusCategoryFiller.
func (h *Handler) fillStatusCategory(ctx context.Context, wsID pgtype.UUID, resp *IssueResponse) {
	h.newStatusCategoryFiller(ctx, wsID)(resp)
}

func issueToResponse(i db.Issue, issuePrefix string) IssueResponse {
	identifier := issuePrefix + "-" + strconv.Itoa(int(i.Number))
	// A built-in status IS its own category, so this costs no catalog lookup and
	// every response carries it. A CUSTOM status is left empty here and filled
	// in by endpoints that resolve the catalog (see the children endpoints'
	// Resolver); consumers fall back on the same rule. (MUL-6243)
	statusCategory := ""
	if issuestatus.IsBuiltIn(i.Status) {
		statusCategory = i.Status
	}
	return IssueResponse{
		ID:             uuidToString(i.ID),
		WorkspaceID:    uuidToString(i.WorkspaceID),
		Number:         i.Number,
		Identifier:     identifier,
		Title:          i.Title,
		Description:    textToPtr(i.Description),
		Status:         i.Status,
		StatusCategory: statusCategory,
		Priority:       i.Priority,
		AssigneeType:   textToPtr(i.AssigneeType),
		AssigneeID:     uuidToPtr(i.AssigneeID),
		CreatorType:    i.CreatorType,
		CreatorID:      uuidToString(i.CreatorID),
		ParentIssueID:  uuidToPtr(i.ParentIssueID),
		ProjectID:      uuidToPtr(i.ProjectID),
		Position:       i.Position,
		Stage:          int4ToPtr(i.Stage),
		StartDate:      dateToPtr(i.StartDate),
		DueDate:        dateToPtr(i.DueDate),
		LabSource:      textToPtr(i.LabSource),
		LabMode:        textToPtr(i.LabMode),
		CreatedAt:      timestampToString(i.CreatedAt),
		UpdatedAt:      timestampToString(i.UpdatedAt),
		Metadata:       parseIssueMetadata(i.Metadata),
	}
}

// issueListRowToResponse converts a list-query row (no description) to an IssueResponse.
func issueListRowToResponse(i db.ListIssuesRow, issuePrefix string) IssueResponse {
	// Same pure built-in resolution as issueToResponse. (MUL-6243)
	statusCategory := ""
	if issuestatus.IsBuiltIn(i.Status) {
		statusCategory = i.Status
	}
	identifier := issuePrefix + "-" + strconv.Itoa(int(i.Number))
	return IssueResponse{
		ID:             uuidToString(i.ID),
		WorkspaceID:    uuidToString(i.WorkspaceID),
		Number:         i.Number,
		Identifier:     identifier,
		Title:          i.Title,
		Description:    textToPtr(i.Description),
		Status:         i.Status,
		StatusCategory: statusCategory,
		Priority:       i.Priority,
		AssigneeType:   textToPtr(i.AssigneeType),
		AssigneeID:     uuidToPtr(i.AssigneeID),
		CreatorType:    i.CreatorType,
		CreatorID:      uuidToString(i.CreatorID),
		ParentIssueID:  uuidToPtr(i.ParentIssueID),
		ProjectID:      uuidToPtr(i.ProjectID),
		Position:       i.Position,
		Stage:          int4ToPtr(i.Stage),
		StartDate:      dateToPtr(i.StartDate),
		DueDate:        dateToPtr(i.DueDate),
		LabSource:      textToPtr(i.LabSource),
		LabMode:        textToPtr(i.LabMode),
		CreatedAt:      timestampToString(i.CreatedAt),
		UpdatedAt:      timestampToString(i.UpdatedAt),
		Metadata:       parseIssueMetadata(i.Metadata),
	}
}

// labelsByIssue bulk-loads labels for the given issue IDs and returns a map
// keyed by issue UUID string. On error or empty input, returns an empty map —
// label rendering is non-critical and we'd rather serve issues without labels
// than fail the whole list call.
func (h *Handler) labelsByIssue(ctx context.Context, wsUUID pgtype.UUID, issueIDs []pgtype.UUID) map[string][]LabelResponse {
	out := map[string][]LabelResponse{}
	if len(issueIDs) == 0 {
		return out
	}
	rows, err := h.Queries.ListLabelsForIssues(ctx, db.ListLabelsForIssuesParams{
		IssueIds:    issueIDs,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		slog.Warn("ListLabelsForIssues failed", "error", err)
		return out
	}
	for _, r := range rows {
		issueID := uuidToString(r.IssueID)
		out[issueID] = append(out[issueID], LabelResponse{
			ID:          uuidToString(r.ID),
			WorkspaceID: uuidToString(r.WorkspaceID),
			Name:        r.Name,
			Color:       r.Color,
			CreatedAt:   timestampToString(r.CreatedAt),
			UpdatedAt:   timestampToString(r.UpdatedAt),
		})
	}
	return out
}

func openIssueRowToResponse(i db.ListOpenIssuesRow, issuePrefix string) IssueResponse {
	// Same pure built-in resolution as issueToResponse. (MUL-6243)
	statusCategory := ""
	if issuestatus.IsBuiltIn(i.Status) {
		statusCategory = i.Status
	}
	identifier := issuePrefix + "-" + strconv.Itoa(int(i.Number))
	return IssueResponse{
		ID:             uuidToString(i.ID),
		WorkspaceID:    uuidToString(i.WorkspaceID),
		Number:         i.Number,
		Identifier:     identifier,
		Title:          i.Title,
		Description:    textToPtr(i.Description),
		Status:         i.Status,
		StatusCategory: statusCategory,
		Priority:       i.Priority,
		AssigneeType:   textToPtr(i.AssigneeType),
		AssigneeID:     uuidToPtr(i.AssigneeID),
		CreatorType:    i.CreatorType,
		CreatorID:      uuidToString(i.CreatorID),
		ParentIssueID:  uuidToPtr(i.ParentIssueID),
		ProjectID:      uuidToPtr(i.ProjectID),
		Position:       i.Position,
		Stage:          int4ToPtr(i.Stage),
		StartDate:      dateToPtr(i.StartDate),
		DueDate:        dateToPtr(i.DueDate),
		LabSource:      textToPtr(i.LabSource),
		LabMode:        textToPtr(i.LabMode),
		CreatedAt:      timestampToString(i.CreatedAt),
		UpdatedAt:      timestampToString(i.UpdatedAt),
		Metadata:       parseIssueMetadata(i.Metadata),
	}
}

type IssueAssigneeGroupResponse struct {
	ID           string          `json:"id"`
	AssigneeType *string         `json:"assignee_type"`
	AssigneeID   *string         `json:"assignee_id"`
	Issues       []IssueResponse `json:"issues"`
	Total        int64           `json:"total"`
}

type GroupedIssuesResponse struct {
	Groups []IssueAssigneeGroupResponse `json:"groups"`
}

type groupedIssueRow struct {
	db.ListIssuesRow
	GroupTotal int64
}

func assigneeGroupID(assigneeType pgtype.Text, assigneeID pgtype.UUID) string {
	if assigneeType.Valid && assigneeID.Valid {
		return "assignee:" + assigneeType.String + ":" + uuidToString(assigneeID)
	}
	return "assignee:unassigned"
}

// SearchIssueResponse extends IssueResponse with search metadata.
type SearchIssueResponse struct {
	IssueResponse
	MatchSource               string  `json:"match_source"`
	MatchedSnippet            *string `json:"matched_snippet,omitempty"`
	MatchedDescriptionSnippet *string `json:"matched_description_snippet,omitempty"`
	MatchedCommentSnippet     *string `json:"matched_comment_snippet,omitempty"`
}

// extractSnippet extracts a snippet of text around the first occurrence of query.
// Returns up to ~120 runes centered on the match. Uses rune-based slicing to
// avoid splitting multi-byte UTF-8 characters (important for CJK content).
// For multi-word queries, tries phrase match first; if not found, locates the
// earliest occurring individual term and centers the snippet around it.
func extractSnippet(content, query string) string {
	runes := []rune(content)
	lowerRunes := []rune(strings.ToLower(content))
	queryRunes := []rune(strings.ToLower(query))

	idx := findRuneSubstring(lowerRunes, queryRunes)

	// If phrase not found, try individual terms for multi-word queries.
	matchLen := len(queryRunes)
	if idx < 0 {
		terms := strings.Fields(strings.ToLower(query))
		if len(terms) > 1 {
			earliest := -1
			earliestLen := 0
			for _, term := range terms {
				termRunes := []rune(term)
				pos := findRuneSubstring(lowerRunes, termRunes)
				if pos >= 0 && (earliest < 0 || pos < earliest) {
					earliest = pos
					earliestLen = len(termRunes)
				}
			}
			if earliest >= 0 {
				idx = earliest
				matchLen = earliestLen
			}
		}
	}

	if idx < 0 {
		if len(runes) > 120 {
			return string(runes[:120]) + "..."
		}
		return content
	}
	start := idx - 40
	if start < 0 {
		start = 0
	}
	end := idx + matchLen + 80
	if end > len(runes) {
		end = len(runes)
	}
	snippet := string(runes[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(runes) {
		snippet = snippet + "..."
	}
	return snippet
}

// findRuneSubstring returns the index of needle in haystack, or -1 if not found.
func findRuneSubstring(haystack, needle []rune) int {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return -1
	}
	for i := 0; i <= len(haystack)-len(needle); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// descriptionContains checks if the description text contains the search phrase or all terms.
func descriptionContains(desc pgtype.Text, phrase string, terms []string) bool {
	if !desc.Valid || desc.String == "" {
		return false
	}
	lower := strings.ToLower(desc.String)
	if strings.Contains(lower, strings.ToLower(phrase)) {
		return true
	}
	if len(terms) > 1 {
		for _, t := range terms {
			if !strings.Contains(lower, strings.ToLower(t)) {
				return false
			}
		}
		return true
	}
	return false
}

// escapeLike escapes LIKE special characters (%, _, \) in user input.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// splitSearchTerms splits a query into individual search terms, filtering empty strings.
func splitSearchTerms(q string) []string {
	fields := strings.FieldsFunc(q, func(r rune) bool {
		return unicode.IsSpace(r)
	})
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			terms = append(terms, f)
		}
	}
	return terms
}

// identifierNumberRe matches patterns like "MUL-123" or "ABC-45".
var identifierNumberRe = regexp.MustCompile(`(?i)^[a-z]+-(\d+)$`)

// parseQueryNumber extracts an issue number from the query if it looks like
// an identifier (e.g. "MUL-123") or a bare number (e.g. "123").
func parseQueryNumber(q string) (int, bool) {
	q = strings.TrimSpace(q)
	// Check for identifier pattern like "MUL-123"
	if m := identifierNumberRe.FindStringSubmatch(q); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n, true
		}
	}
	// Check for bare number
	if n, err := strconv.Atoi(q); err == nil && n > 0 {
		return n, true
	}
	return 0, false
}

// searchResult holds a raw row from the dynamic search query.
type searchResult struct {
	issue                 db.Issue
	matchSource           string
	matchedCommentContent string
}

// buildSearchQuery builds a dynamic SQL query for issue search.
// It uses LOWER(column) LIKE for case-insensitive matching compatible with pg_bigm 1.2 GIN indexes.
// Search patterns are lowercased in Go to avoid redundant LOWER() on the pattern side in SQL.
// LIKE patterns are pre-built in Go (e.g. "%html%") so pg_bigm can extract bigrams from a single parameter value.
func buildSearchQuery(phrase string, terms []string, queryNum int, hasNum bool, includeClosed bool, terminalStatusKeys []string) (string, []any) {
	// Lowercase in Go so SQL only needs LOWER() on the column side.
	phrase = strings.ToLower(phrase)
	for i, t := range terms {
		terms[i] = strings.ToLower(t)
	}

	// Parameter index tracker
	argIdx := 1
	args := []any{}
	nextArg := func(val any) string {
		args = append(args, val)
		s := fmt.Sprintf("$%d", argIdx)
		argIdx++
		return s
	}

	escapedPhrase := escapeLike(phrase)
	// $1: exact phrase (for exact title match)
	phraseParam := nextArg(escapedPhrase)
	// $2: "%phrase%" (contains pattern — pre-built for pg_bigm index usage)
	phraseContainsParam := nextArg("%" + escapedPhrase + "%")
	// $3: "phrase%" (starts-with pattern)
	phraseStartsWithParam := nextArg(escapedPhrase + "%")

	wsParam := nextArg(nil) // $4 — workspace_id, will be filled by caller position

	// Build per-term LIKE conditions only for multi-word search.
	var termContainsParams []string
	if len(terms) > 1 {
		for _, t := range terms {
			et := escapeLike(t)
			termContainsParams = append(termContainsParams, nextArg("%"+et+"%"))
		}
	}

	// --- WHERE clause ---
	var whereParts []string

	// Full phrase match: title, description, or comment
	phraseMatch := fmt.Sprintf(
		"(LOWER(i.title) LIKE %s OR LOWER(COALESCE(i.description, '')) LIKE %s OR EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE %s))",
		phraseContainsParam, phraseContainsParam, phraseContainsParam,
	)
	whereParts = append(whereParts, phraseMatch)

	// Multi-word AND match (each term must appear somewhere)
	if len(termContainsParams) > 1 {
		var termConditions []string
		for _, tp := range termContainsParams {
			termConditions = append(termConditions, fmt.Sprintf(
				"(LOWER(i.title) LIKE %s OR LOWER(COALESCE(i.description, '')) LIKE %s OR EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE %s))",
				tp, tp, tp,
			))
		}
		whereParts = append(whereParts, "("+strings.Join(termConditions, " AND ")+")")
	}

	// Number match
	numParam := ""
	if hasNum {
		numParam = nextArg(queryNum)
		whereParts = append(whereParts, fmt.Sprintf("i.number = %s", numParam))
	}

	whereClause := "(" + strings.Join(whereParts, " OR ") + ")"

	if !includeClosed {
		// Negate only known terminal keys so an unknown legacy key remains
		// searchable instead of disappearing from the default result set.
		terminalStatusesParam := nextArg(terminalStatusKeys)
		whereClause += fmt.Sprintf(" AND NOT (i.status = ANY(%s::text[]))", terminalStatusesParam)
	}

	// --- ORDER BY clause ---
	// Build ranking CASE with fine-grained tiers.
	var rankCases []string

	// Tier 0: Identifier exact match
	if hasNum {
		rankCases = append(rankCases, fmt.Sprintf("WHEN i.number = %s THEN 0", numParam))
	}

	// Tier 1: Exact title match
	rankCases = append(rankCases, fmt.Sprintf("WHEN LOWER(i.title) = %s THEN 1", phraseParam))

	// Tier 2: Title starts with phrase
	rankCases = append(rankCases, fmt.Sprintf("WHEN LOWER(i.title) LIKE %s THEN 2", phraseStartsWithParam))

	// Tier 3: Title contains phrase
	rankCases = append(rankCases, fmt.Sprintf("WHEN LOWER(i.title) LIKE %s THEN 3", phraseContainsParam))

	// Tier 4: Title matches all words (multi-word only)
	if len(termContainsParams) > 1 {
		var titleTerms []string
		for _, tp := range termContainsParams {
			titleTerms = append(titleTerms, fmt.Sprintf("LOWER(i.title) LIKE %s", tp))
		}
		rankCases = append(rankCases, fmt.Sprintf("WHEN (%s) THEN 4", strings.Join(titleTerms, " AND ")))
	}

	// Tier 5: Description contains phrase
	rankCases = append(rankCases, fmt.Sprintf("WHEN LOWER(COALESCE(i.description, '')) LIKE %s THEN 5", phraseContainsParam))

	// Tier 6: Description matches all words (multi-word only)
	if len(termContainsParams) > 1 {
		var descTerms []string
		for _, tp := range termContainsParams {
			descTerms = append(descTerms, fmt.Sprintf("LOWER(COALESCE(i.description, '')) LIKE %s", tp))
		}
		rankCases = append(rankCases, fmt.Sprintf("WHEN (%s) THEN 6", strings.Join(descTerms, " AND ")))
	}

	// Tier 7: Comment contains phrase
	rankCases = append(rankCases, fmt.Sprintf("WHEN EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE %s) THEN 7", phraseContainsParam))

	// Tier 8: Comment matches all words (multi-word only)
	if len(termContainsParams) > 1 {
		var commentTerms []string
		for _, tp := range termContainsParams {
			commentTerms = append(commentTerms, fmt.Sprintf("LOWER(c.content) LIKE %s", tp))
		}
		rankCases = append(rankCases, fmt.Sprintf("WHEN EXISTS (SELECT 1 FROM comment c WHERE c.issue_id = i.id AND c.workspace_id = $4 AND (%s)) THEN 8", strings.Join(commentTerms, " AND ")))
	}

	rankExpr := "CASE " + strings.Join(rankCases, " ") + " ELSE 9 END"

	// Status priority: active issues first
	statusRank := `CASE i.status
		WHEN 'in_progress' THEN 0
		WHEN 'in_review' THEN 1
		WHEN 'todo' THEN 2
		WHEN 'blocked' THEN 3
		WHEN 'backlog' THEN 4
		WHEN 'done' THEN 5
		WHEN 'cancelled' THEN 6
		ELSE 7
	END`

	// Cancelled issues are abandoned work. statusRank alone cannot keep them
	// down because it is only a tie-breaker within one relevance tier: a
	// cancelled issue whose title matches the phrase exactly (tier 1) still
	// outranks an in_progress issue that merely contains it (tier 3), and a
	// workspace with many cancelled issues can fill the whole LIMIT window and
	// push live work off the page entirely. So demote cancelled ahead of
	// rankExpr — they sort after every other match and are the first rows the
	// LIMIT drops. Unlike 'done', which is finished work worth referencing,
	// cancelled work was thrown away. The exception is a direct hit: an exact
	// identifier or exact title means the user is targeting that one issue and
	// knows what they asked for.
	//
	// The title half reuses tier 1's predicate verbatim, including its quirk:
	// phraseParam is escapeLike'd, so a title containing _ or % never compares
	// equal and is not treated as a direct hit. Such an issue is still returned
	// by number; keeping the two predicates identical matters more than working
	// around an escaping bug that belongs with tier 1.
	directHitParts := []string{fmt.Sprintf("LOWER(i.title) = %s", phraseParam)}
	if hasNum {
		directHitParts = append(directHitParts, fmt.Sprintf("i.number = %s", numParam))
	}
	cancelledRank := fmt.Sprintf(
		"CASE WHEN i.status = 'cancelled' AND NOT (%s) THEN 1 ELSE 0 END",
		strings.Join(directHitParts, " OR "),
	)

	// --- match_source expression ---
	matchSourceExpr := fmt.Sprintf(`CASE
		WHEN LOWER(i.title) LIKE %s THEN 'title'
		WHEN LOWER(COALESCE(i.description, '')) LIKE %s THEN 'description'
		ELSE 'comment'
	END`, phraseContainsParam, phraseContainsParam)

	// For multi-word: also check if all terms match in title/description
	if len(termContainsParams) > 1 {
		var titleTerms []string
		var descTerms []string
		for _, tp := range termContainsParams {
			titleTerms = append(titleTerms, fmt.Sprintf("LOWER(i.title) LIKE %s", tp))
			descTerms = append(descTerms, fmt.Sprintf("LOWER(COALESCE(i.description, '')) LIKE %s", tp))
		}
		matchSourceExpr = fmt.Sprintf(`CASE
			WHEN LOWER(i.title) LIKE %s THEN 'title'
			WHEN (%s) THEN 'title'
			WHEN LOWER(COALESCE(i.description, '')) LIKE %s THEN 'description'
			WHEN (%s) THEN 'description'
			ELSE 'comment'
		END`,
			phraseContainsParam, strings.Join(titleTerms, " AND "),
			phraseContainsParam, strings.Join(descTerms, " AND "),
		)
	}

	// --- matched_comment_content subquery ---
	// Always return matching comment content regardless of match_source,
	// so frontend can display comment snippet alongside title/description matches.
	commentSubquery := fmt.Sprintf(`COALESCE(
		(SELECT c.content FROM comment c
		 WHERE c.issue_id = i.id AND c.workspace_id = $4 AND LOWER(c.content) LIKE %s
		 ORDER BY c.created_at DESC LIMIT 1),
		''
	)`, phraseContainsParam)

	if len(termContainsParams) > 1 {
		var commentTerms []string
		for _, tp := range termContainsParams {
			commentTerms = append(commentTerms, fmt.Sprintf("LOWER(c.content) LIKE %s", tp))
		}
		commentSubquery = fmt.Sprintf(`COALESCE(
			(SELECT c.content FROM comment c
			 WHERE c.issue_id = i.id AND c.workspace_id = $4 AND (LOWER(c.content) LIKE %s OR (%s))
			 ORDER BY c.created_at DESC LIMIT 1),
			''
		)`, phraseContainsParam, strings.Join(commentTerms, " AND "))
	}

	limitParam := nextArg(nil)  // placeholder
	offsetParam := nextArg(nil) // placeholder

	query := fmt.Sprintf(`SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
		i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
		i.parent_issue_id, i.acceptance_criteria, i.context_refs, i.position,
		i.start_date, i.due_date, i.created_at, i.updated_at, i.number, i.project_id,
		%s AS match_source,
		%s AS matched_comment_content
	FROM issue i
	WHERE i.workspace_id = %s AND %s
	ORDER BY %s, %s, %s, i.updated_at DESC
	LIMIT %s OFFSET %s`,
		matchSourceExpr,
		commentSubquery,
		wsParam,
		whereClause,
		cancelledRank,
		rankExpr,
		statusRank,
		limitParam,
		offsetParam,
	)

	return query, args
}

func (h *Handler) SearchIssues(w http.ResponseWriter, r *http.Request) {
	parentCtx := r.Context()
	workspaceID := h.resolveWorkspaceID(r)

	q := r.URL.Query().Get("q")
	if q == "" {
		writeError(w, http.StatusBadRequest, "q parameter is required")
		return
	}

	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 50 {
		limit = 50
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	includeClosed := r.URL.Query().Get("include_closed") == "true"

	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	terms := splitSearchTerms(q)
	queryNum, hasNum := parseQueryNumber(q)
	var terminalStatusKeys []string
	if !includeClosed {
		resolvedKeys, err := h.terminalIssueStatusKeys(r.Context(), wsUUID)
		if err != nil {
			slog.Warn("expand terminal status categories failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to resolve status categories")
			return
		}
		terminalStatusKeys = resolvedKeys
	}

	sqlQuery, args := buildSearchQuery(q, terms, queryNum, hasNum, includeClosed, terminalStatusKeys)
	// Fill placeholder args: $4 = workspace_id, last two = limit, offset
	args[3] = wsUUID
	args[len(args)-2] = limit
	args[len(args)-1] = offset

	// Bound the search query so a runaway LIKE/ILIKE on a workspace with many
	// issues cannot hang the HTTP connection indefinitely. 5s is well above
	// the p99 of healthy searches but short enough that a stuck pg query
	// surfaces as 504 instead of a silent client retry loop.
	ctx, cancel := context.WithTimeout(parentCtx, 5*time.Second)
	defer cancel()
	rows, err := h.DB.Query(ctx, sqlQuery, args...)
	if err != nil {
		// Distinguish the Postgres-side statement_timeout (SQLSTATE 57014)
		// from a Go-context deadline. The two are emitted as different HTTP
		// statuses (503 vs 504) so the frontend can tell them apart:
		//   - 503: Postgres killed the query (DB-side hard cap)
		//   - 504: the Go context expired (network / handler slow)
		// See search_503.go for the canonical mapping.
		if isSearchStatementTimeout(err) {
			slog.Warn("search issues hit pg statement_timeout", "workspace_id", workspaceID, "query", q)
			writeError(w, http.StatusServiceUnavailable, "search cancelled by database timeout; please narrow your query")
			return
		}
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			slog.Warn("search issues timed out", "workspace_id", workspaceID, "query", q)
			writeError(w, http.StatusGatewayTimeout, "search took too long; please narrow your query")
			return
		}
		slog.Warn("search issues failed", "error", err, "workspace_id", workspaceID, "query", q)
		writeError(w, http.StatusInternalServerError, "failed to search issues")
		return
	}
	defer rows.Close()

	var results []searchResult
	for rows.Next() {
		var sr searchResult
		if err := rows.Scan(
			&sr.issue.ID,
			&sr.issue.WorkspaceID,
			&sr.issue.Title,
			&sr.issue.Description,
			&sr.issue.Status,
			&sr.issue.Priority,
			&sr.issue.AssigneeType,
			&sr.issue.AssigneeID,
			&sr.issue.CreatorType,
			&sr.issue.CreatorID,
			&sr.issue.ParentIssueID,
			&sr.issue.AcceptanceCriteria,
			&sr.issue.ContextRefs,
			&sr.issue.Position,
			&sr.issue.StartDate,
			&sr.issue.DueDate,
			&sr.issue.CreatedAt,
			&sr.issue.UpdatedAt,
			&sr.issue.Number,
			&sr.issue.ProjectID,
			&sr.matchSource,
			&sr.matchedCommentContent,
		); err != nil {
			slog.Warn("search issues scan failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to search issues")
			return
		}
		results = append(results, sr)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("search issues rows error", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to search issues")
		return
	}

	prefix := h.getIssuePrefix(ctx, wsUUID)
	fillSearch := h.newStatusCategoryFiller(ctx, wsUUID)
	resp := make([]SearchIssueResponse, len(results))
	for i, sr := range results {
		sir := SearchIssueResponse{
			IssueResponse: issueToResponse(sr.issue, prefix),
			MatchSource:   sr.matchSource,
		}
		fillSearch(&sir.IssueResponse)
		// Always populate comment snippet when a matching comment exists
		if sr.matchedCommentContent != "" {
			snippet := extractSnippet(sr.matchedCommentContent, q)
			sir.MatchedCommentSnippet = &snippet
			// Keep backward compat: also set MatchedSnippet for comment-source matches
			if sr.matchSource == "comment" {
				sir.MatchedSnippet = &snippet
			}
		}
		// Populate description snippet when description matches
		if sr.matchSource == "description" || descriptionContains(sr.issue.Description, q, terms) {
			if sr.issue.Description.Valid && sr.issue.Description.String != "" {
				snippet := extractSnippet(sr.issue.Description.String, q)
				sir.MatchedDescriptionSnippet = &snippet
			}
		}
		resp[i] = sir
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"issues": resp,
	})
}

func (h *Handler) ListIssues(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	// Parse optional filter params. Malformed UUIDs in filters return 400 —
	// silently coercing them to a zero UUID would mask a client bug and let
	// the query return an empty result set (or worse, match a NULL row).
	var priorityFilter pgtype.Text
	if p := r.URL.Query().Get("priority"); p != "" {
		priorityFilter = pgtype.Text{String: p, Valid: true}
	}
	var assigneeFilter pgtype.UUID
	if a := r.URL.Query().Get("assignee_id"); a != "" {
		id, ok := parseUUIDOrBadRequest(w, a, "assignee_id")
		if !ok {
			return
		}
		assigneeFilter = id
	}
	var assigneeIdsFilter []pgtype.UUID
	if ids := r.URL.Query().Get("assignee_ids"); ids != "" {
		for _, raw := range strings.Split(ids, ",") {
			if s := strings.TrimSpace(raw); s != "" {
				id, ok := parseUUIDOrBadRequest(w, s, "assignee_ids")
				if !ok {
					return
				}
				assigneeIdsFilter = append(assigneeIdsFilter, id)
			}
		}
	}
	var creatorFilter pgtype.UUID
	if c := r.URL.Query().Get("creator_id"); c != "" {
		id, ok := parseUUIDOrBadRequest(w, c, "creator_id")
		if !ok {
			return
		}
		creatorFilter = id
	}
	var projectFilter pgtype.UUID
	if p := r.URL.Query().Get("project_id"); p != "" {
		id, ok := parseUUIDOrBadRequest(w, p, "project_id")
		if !ok {
			return
		}
		projectFilter = id
	}
	// involves_user_id widens the assignee filter to surface issues where the
	// user is the indirect assignee (their owned agent, or a squad they belong
	// to / lead / have an agent inside). Direct member-assignment is excluded
	// by design — that is the meaning of `assignee_id` (tab 1), and tab 3 must
	// be disjoint from tab 1.
	var involvesUserFilter pgtype.UUID
	if u := r.URL.Query().Get("involves_user_id"); u != "" {
		id, ok := parseUUIDOrBadRequest(w, u, "involves_user_id")
		if !ok {
			return
		}
		involvesUserFilter = id
	}

	// MUL-6409 (fork completion): multi-value + category filters mirroring
	// ListGroupedIssues below. The bucketed board cache fans out one
	// listIssues request per status category and renders the returned
	// `total` as the column badge — every filter the renderer can narrow
	// by must parse HERE, or the badge keeps counting the unfiltered
	// workspace while the column body shows the filtered subset.
	statusesFilter := splitCommaParam(r.URL.Query().Get("statuses"))
	statusCategoriesFilter := splitCommaParam(r.URL.Query().Get("status_categories"))
	if len(statusCategoriesFilter) == 0 {
		statusCategoriesFilter = splitCommaParam(r.URL.Query().Get("status_category"))
	}
	assigneeFilters, ok := parseActorFilterList(w, r.URL.Query().Get("assignee_filters"), "assignee_filters")
	if !ok {
		return
	}
	includeNoAssignee := r.URL.Query().Get("include_no_assignee") == "true"
	creatorFilters, ok := parseActorFilterList(w, r.URL.Query().Get("creator_filters"), "creator_filters")
	if !ok {
		return
	}
	projectIDs, ok := parseUUIDParamList(w, r.URL.Query().Get("project_ids"), "project_ids")
	if !ok {
		return
	}
	includeNoProject := r.URL.Query().Get("include_no_project") == "true"
	labelIDs, ok := parseUUIDParamList(w, r.URL.Query().Get("label_ids"), "label_ids")
	if !ok {
		return
	}

	metadataFilter, ok := parseMetadataFilterParam(w, r.URL.Query().Get("metadata"))
	if !ok {
		return
	}
	dateFilter, ok := parseIssueDateFilter(w, r.URL.Query())
	if !ok {
		return
	}

	// exclude_lab=true (0.3.33) hides lab-bound issues from the main
	// workspace task list so users see only their regular work.
	// Passing &exclude_lab=false overrides this filter to show
	// experimental-lab issues alongside normal ones. The default
	// is false — the frontend sends exclude_lab=true on every
	// fetch from the workspace list.
	excludeLab := r.URL.Query().Get("exclude_lab") == "true"
	var excludeLabParam pgtype.Bool
	if excludeLab {
		excludeLabParam = pgtype.Bool{Bool: true, Valid: true}
	}

	// open_only=true returns all non-done/cancelled issues (no limit).
	if r.URL.Query().Get("open_only") == "true" {
		terminalStatusKeys, err := h.terminalIssueStatusKeys(ctx, wsUUID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to resolve status categories")
			return
		}
		issues, err := h.Queries.ListOpenIssues(ctx, db.ListOpenIssuesParams{
			WorkspaceID:        wsUUID,
			TerminalStatusKeys: terminalStatusKeys,
			Priority:           priorityFilter,
			AssigneeID:         assigneeFilter,
			AssigneeIds:        assigneeIdsFilter,
			CreatorID:          creatorFilter,
			ProjectID:          projectFilter,
			InvolvesUserID:     involvesUserFilter,
			ExcludeLab:         excludeLabParam,
			MetadataFilter:     metadataFilter,
		})
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to list issues")
			return
		}

		prefix := h.getIssuePrefix(ctx, wsUUID)
		ids := make([]pgtype.UUID, len(issues))
		for i, issue := range issues {
			ids[i] = issue.ID
		}
		labelsMap := h.labelsByIssue(ctx, wsUUID, ids)
		fillOpen := h.newStatusCategoryFiller(ctx, wsUUID)
		resp := make([]IssueResponse, len(issues))
		for i, issue := range issues {
			resp[i] = openIssueRowToResponse(issue, prefix)
			fillOpen(&resp[i])
			labels := labelsMap[resp[i].ID]
			if labels == nil {
				labels = []LabelResponse{}
			}
			resp[i].Labels = &labels
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"issues": resp,
			"total":  len(resp),
		})
		return
	}

	limit := 100
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 100 {
		limit = 100
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v >= 0 {
			offset = v
		}
	}

	var statusFilter pgtype.Text
	if s := r.URL.Query().Get("status"); s != "" {
		statusFilter = pgtype.Text{String: s, Valid: true}

	}

	// scheduled=true restricts the result to issues that have at least one of
	// start_date / due_date set. Used by the Project Gantt view, which only
	// renders schedulable rows and shouldn't pay for the full project list.
	var scheduledFilter pgtype.Bool
	if r.URL.Query().Get("scheduled") == "true" {
		scheduledFilter = pgtype.Bool{Bool: true, Valid: true}
	}

	// Parse sort and direction params for dynamic ORDER BY.
	// Manual sort (position) is always ASC — direction is ignored because
	// the user defines order through drag-and-drop, reversing it has no
	// product meaning.
	sortCol := "position"
	if s := r.URL.Query().Get("sort"); s != "" {
		switch s {
		case "position", "title", "created_at", "start_date", "due_date":
			sortCol = s
		case "priority":
			sortCol = "CASE i.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END"
		default:
			writeError(w, http.StatusBadRequest, "invalid sort value")
			return
		}
	}
	sortDir := "ASC"
	if sortCol != "position" {
		if d := r.URL.Query().Get("direction"); d != "" {
			switch strings.ToLower(d) {
			case "asc":
				sortDir = "ASC"
			case "desc":
				sortDir = "DESC"
			default:
				writeError(w, http.StatusBadRequest, "invalid direction value")
				return
			}
		}
	}

	// Build dynamic SQL — same approach as ListGroupedIssues.
	where := []string{"i.workspace_id = $1"}
	args := []any{wsUUID}
	addArg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	if statusFilter.Valid {
		where = append(where, fmt.Sprintf("i.status = %s", addArg(statusFilter.String)))

	}
	if priorityFilter.Valid {
		where = append(where, fmt.Sprintf("i.priority = %s", addArg(priorityFilter.String)))
	}
	if assigneeFilter.Valid {
		where = append(where, fmt.Sprintf("i.assignee_id = %s::uuid", addArg(assigneeFilter)))
	}
	if len(assigneeIdsFilter) > 0 {
		where = append(where, fmt.Sprintf("i.assignee_id = ANY(%s::uuid[])", addArg(assigneeIdsFilter)))
	}
	if creatorFilter.Valid {
		where = append(where, fmt.Sprintf("i.creator_id = %s::uuid", addArg(creatorFilter)))
	}
	if projectFilter.Valid {
		where = append(where, fmt.Sprintf("i.project_id = %s::uuid", addArg(projectFilter)))
	}
	if scheduledFilter.Valid {
		where = append(where, "(i.start_date IS NOT NULL OR i.due_date IS NOT NULL)")
	}
	if excludeLab {
		where = append(where, "i.lab_source IS NULL")
	}
	if metadataFilter != nil {
		where = append(where, fmt.Sprintf("i.metadata @> %s::jsonb", addArg(string(metadataFilter))))
	}
	where = appendIssueDateFilter(where, addArg, dateFilter)
	if involvesUserFilter.Valid {
		ref := addArg(involvesUserFilter)
		where = append(where, fmt.Sprintf(`(
    (i.assignee_type = 'agent' AND i.assignee_id IN (
       SELECT a.id FROM agent a
        WHERE a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
    ))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
       SELECT sm.squad_id
         FROM squad_member sm
         JOIN squad s ON s.id = sm.squad_id
        WHERE s.workspace_id = $1
          AND sm.member_type = 'member'
          AND sm.member_id   = %[1]s::uuid
       UNION
       SELECT s.id
         FROM squad s
         JOIN agent a ON a.id = s.leader_id
        WHERE s.workspace_id = $1
          AND a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
       UNION
       SELECT sm.squad_id
         FROM squad_member sm
         JOIN squad s ON s.id = sm.squad_id
         JOIN agent a ON a.id = sm.member_id
        WHERE s.workspace_id = $1
          AND sm.member_type = 'agent'
          AND a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
    ))
)`, ref))
	}

	// Fragments below are copy-for-copy from ListGroupedIssues — keep both
	// in lock-step when editing either.
	if len(statusesFilter) > 0 {
		where = append(where, fmt.Sprintf("i.status = ANY(%s::text[])", addArg(statusesFilter)))
	}
	if len(statusCategoriesFilter) > 0 {
		// Expanded to keys so the (workspace_id, status) index still drives
		// the scan. (MUL-6243)
		keys, err := issuestatus.ExpandCategories(r.Context(), h.Queries, wsUUID, statusCategoriesFilter)
		if err != nil {
			slog.Warn("expand status categories failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to resolve status categories")
			return
		}
		where = append(where, fmt.Sprintf("i.status = ANY(%s::text[])", addArg(keys)))
	}
	if len(assigneeFilters) > 0 || includeNoAssignee {
		ors := make([]string, 0, len(assigneeFilters)+1)
		for _, filter := range assigneeFilters {
			ors = append(ors, fmt.Sprintf(
				"(i.assignee_type = %s::text AND i.assignee_id = %s::uuid)",
				addArg(filter.actorType),
				addArg(filter.actorID),
			))
		}
		if includeNoAssignee {
			ors = append(ors, "(i.assignee_type IS NULL AND i.assignee_id IS NULL)")
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if len(creatorFilters) > 0 {
		ors := make([]string, 0, len(creatorFilters))
		for _, filter := range creatorFilters {
			ors = append(ors, fmt.Sprintf(
				"(i.creator_type = %s::text AND i.creator_id = %s::uuid)",
				addArg(filter.actorType),
				addArg(filter.actorID),
			))
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if len(projectIDs) > 0 || includeNoProject {
		ors := make([]string, 0, 2)
		if len(projectIDs) > 0 {
			ors = append(ors, fmt.Sprintf("i.project_id = ANY(%s::uuid[])", addArg(projectIDs)))
		}
		if includeNoProject {
			ors = append(ors, "i.project_id IS NULL")
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	if len(labelIDs) > 0 {
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM issue_to_label itl WHERE itl.issue_id = i.id AND itl.label_id = ANY(%s::uuid[]))",
			addArg(labelIDs),
		))
	}

	whereSql := strings.Join(where, " AND ")

	// Build ORDER BY clause.
	orderBy := sortCol
	if !strings.HasPrefix(sortCol, "CASE") {
		orderBy = "i." + sortCol
	}
	orderBy += " " + sortDir
	if sortCol == "start_date" || sortCol == "due_date" {
		orderBy += " NULLS LAST"
	}
	orderBy += ", i.created_at DESC"

	offsetRef := addArg(int64(offset))
	limitRef := addArg(int64(limit))

	query := fmt.Sprintf(`SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
       i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
       i.parent_issue_id, i.position, i.start_date, i.due_date, i.created_at, i.updated_at, i.number, i.project_id, i.metadata
FROM issue i
WHERE %s
ORDER BY %s
LIMIT %s OFFSET %s`, whereSql, orderBy, limitRef, offsetRef)

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		slog.Warn("ListIssues query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list issues")
		return
	}
	defer rows.Close()

	var issues []db.ListIssuesRow
	for rows.Next() {
		var row db.ListIssuesRow
		if err := rows.Scan(
			&row.ID,
			&row.WorkspaceID,
			&row.Title,
			&row.Description,
			&row.Status,
			&row.Priority,
			&row.AssigneeType,
			&row.AssigneeID,
			&row.CreatorType,
			&row.CreatorID,
			&row.ParentIssueID,
			&row.Position,
			&row.StartDate,
			&row.DueDate,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.Number,
			&row.ProjectID,
			&row.Metadata,
		); err != nil {
			slog.Warn("ListIssues scan failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list issues")
			return
		}
		issues = append(issues, row)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("ListIssues rows failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list issues")
		return
	}

	// Get the true total count for pagination awareness.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM issue i WHERE %s`, whereSql)
	// Count query uses the same args minus the OFFSET and LIMIT params (last two added).
	countArgs := args[:len(args)-2]
	var total int64
	if err := h.DB.QueryRow(ctx, countQuery, countArgs...).Scan(&total); err != nil {
		total = int64(len(issues))
	}

	prefix := h.getIssuePrefix(ctx, wsUUID)
	ids := make([]pgtype.UUID, len(issues))
	for i, issue := range issues {
		ids[i] = issue.ID
	}
	labelsMap := h.labelsByIssue(ctx, wsUUID, ids)
	resp := make([]IssueResponse, len(issues))
	for i, issue := range issues {
		resp[i] = issueListRowToResponse(issue, prefix)
		labels := labelsMap[resp[i].ID]
		if labels == nil {
			labels = []LabelResponse{}
		}
		resp[i].Labels = &labels
	}
	h.fillStatusCategories(ctx, wsUUID, resp)

	writeJSON(w, http.StatusOK, map[string]any{
		"issues": resp,
		"total":  total,
	})
}

type issueActorFilter struct {
	actorType string
	actorID   pgtype.UUID
}

type issueDateFilter struct {
	column string
	start  time.Time
	end    time.Time
}

func parseIssueDateFilter(w http.ResponseWriter, values url.Values) (*issueDateFilter, bool) {
	field := strings.TrimSpace(values.Get("date_field"))
	startRaw := strings.TrimSpace(values.Get("date_start"))
	endRaw := strings.TrimSpace(values.Get("date_end"))
	if field == "" && startRaw == "" && endRaw == "" {
		return nil, true
	}
	if field == "" || startRaw == "" || endRaw == "" {
		writeError(w, http.StatusBadRequest, "date_field, date_start, and date_end are required together")
		return nil, false
	}

	column := ""
	switch field {
	case "created_at":
		column = "created_at"
	case "updated_at":
		column = "updated_at"
	default:
		writeError(w, http.StatusBadRequest, "invalid date_field")
		return nil, false
	}

	start, err := time.Parse(time.RFC3339Nano, startRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date_start")
		return nil, false
	}
	end, err := time.Parse(time.RFC3339Nano, endRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date_end")
		return nil, false
	}
	if !start.Before(end) {
		writeError(w, http.StatusBadRequest, "date_start must be before date_end")
		return nil, false
	}

	return &issueDateFilter{column: column, start: start, end: end}, true
}

func appendIssueDateFilter(where []string, addArg func(any) string, filter *issueDateFilter) []string {
	if filter == nil {
		return where
	}
	startRef := addArg(filter.start)
	endRef := addArg(filter.end)
	return append(where, fmt.Sprintf(
		"i.%s >= %s AND i.%s < %s",
		filter.column,
		startRef,
		filter.column,
		endRef,
	))
}

func splitCommaParam(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func isIssueActorType(s string) bool {
	return s == "member" || s == "agent" || s == "squad"
}

func parseUUIDParamList(w http.ResponseWriter, raw, fieldName string) ([]pgtype.UUID, bool) {
	parts := splitCommaParam(raw)
	if len(parts) == 0 {
		return nil, true
	}
	ids := make([]pgtype.UUID, 0, len(parts))
	for _, part := range parts {
		id, ok := parseUUIDOrBadRequest(w, part, fieldName)
		if !ok {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

func parseActorFilterList(w http.ResponseWriter, raw, fieldName string) ([]issueActorFilter, bool) {
	parts := splitCommaParam(raw)
	if len(parts) == 0 {
		return nil, true
	}
	filters := make([]issueActorFilter, 0, len(parts))
	for _, part := range parts {
		pieces := strings.SplitN(part, ":", 2)
		if len(pieces) != 2 || !isIssueActorType(pieces[0]) || strings.TrimSpace(pieces[1]) == "" {
			writeError(w, http.StatusBadRequest, "invalid "+fieldName)
			return nil, false
		}
		id, ok := parseUUIDOrBadRequest(w, strings.TrimSpace(pieces[1]), fieldName)
		if !ok {
			return nil, false
		}
		filters = append(filters, issueActorFilter{
			actorType: pieces[0],
			actorID:   id,
		})
	}
	return filters, true
}

func (h *Handler) ListGroupedIssues(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if h.DB == nil {
		writeError(w, http.StatusInternalServerError, "database is unavailable")
		return
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "assignee"
	}
	if groupBy != "assignee" {
		writeError(w, http.StatusBadRequest, "unsupported group_by")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	limit := 50
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 100 {
		limit = 100
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if v, err := strconv.Atoi(o); err == nil && v > 0 {
			offset = v
		}
	}

	where := []string{"i.workspace_id = $1"}
	args := []any{wsUUID}
	addArg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	statuses := splitCommaParam(r.URL.Query().Get("statuses"))
	if len(statuses) == 0 {
		statuses = splitCommaParam(r.URL.Query().Get("status"))
	}
	if len(statuses) > 0 {
		where = append(where, fmt.Sprintf("i.status = ANY(%s::text[])", addArg(statuses)))
	}
	// See ListIssues: category filtering is what lets the board keep a fixed
	// column count as a workspace adds custom statuses. (MUL-6243)
	statusCategories := splitCommaParam(r.URL.Query().Get("status_categories"))
	if len(statusCategories) == 0 {
		statusCategories = splitCommaParam(r.URL.Query().Get("status_category"))
	}
	if len(statusCategories) > 0 {
		// See ListIssues: expanded to keys so the (workspace_id, status) index
		// still drives the scan. (MUL-6243)
		keys, err := issuestatus.ExpandCategories(r.Context(), h.Queries, wsUUID, statusCategories)
		if err != nil {
			slog.Warn("expand status categories failed", append(logger.RequestAttrs(r), "error", err)...)
			writeError(w, http.StatusInternalServerError, "failed to resolve status categories")
			return
		}
		where = append(where, fmt.Sprintf("i.status = ANY(%s::text[])", addArg(keys)))
	}

	priorities := splitCommaParam(r.URL.Query().Get("priorities"))
	if len(priorities) == 0 {
		priorities = splitCommaParam(r.URL.Query().Get("priority"))
	}
	if len(priorities) > 0 {
		where = append(where, fmt.Sprintf("i.priority = ANY(%s::text[])", addArg(priorities)))
	}

	assigneeTypes := splitCommaParam(r.URL.Query().Get("assignee_types"))
	if len(assigneeTypes) > 0 {
		for _, assigneeType := range assigneeTypes {
			if !isIssueActorType(assigneeType) {
				writeError(w, http.StatusBadRequest, "invalid assignee_types")
				return
			}
		}
		where = append(where, fmt.Sprintf("i.assignee_type = ANY(%s::text[])", addArg(assigneeTypes)))
	}

	if raw := r.URL.Query().Get("assignee_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "assignee_id")
		if !ok {
			return
		}
		where = append(where, fmt.Sprintf("i.assignee_id = %s::uuid", addArg(id)))
	}
	if raw := r.URL.Query().Get("assignee_ids"); raw != "" {
		ids, ok := parseUUIDParamList(w, raw, "assignee_ids")
		if !ok {
			return
		}
		if len(ids) > 0 {
			where = append(where, fmt.Sprintf("i.assignee_id = ANY(%s::uuid[])", addArg(ids)))
		}
	}
	if raw := r.URL.Query().Get("creator_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "creator_id")
		if !ok {
			return
		}
		where = append(where, fmt.Sprintf("i.creator_id = %s::uuid", addArg(id)))
	}
	if raw := r.URL.Query().Get("project_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "project_id")
		if !ok {
			return
		}
		where = append(where, fmt.Sprintf("i.project_id = %s::uuid", addArg(id)))
	}
	if filter, ok := parseMetadataFilterParam(w, r.URL.Query().Get("metadata")); !ok {
		return
	} else if filter != nil {
		where = append(where, fmt.Sprintf("i.metadata @> %s::jsonb", addArg(string(filter))))
	}
	// Mirror the involves_user_id 4-branch UNION from sqlc's ListIssues /
	// ListOpenIssues / CountIssues. ListGroupedIssues is a hand-written dynamic
	// SQL builder that does not share parameters with sqlc, so the fragment is
	// re-implemented here in lock-step. Member-direct assignment is excluded by
	// design: that semantics belongs to tab 1 (`assignee_id`), and tab 3 must
	// stay disjoint from tab 1.
	if raw := r.URL.Query().Get("involves_user_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "involves_user_id")
		if !ok {
			return
		}
		ref := addArg(id)
		where = append(where, fmt.Sprintf(`(
    (i.assignee_type = 'agent' AND i.assignee_id IN (
       SELECT a.id FROM agent a
        WHERE a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
    ))
    OR (i.assignee_type = 'squad' AND i.assignee_id IN (
       SELECT sm.squad_id
         FROM squad_member sm
         JOIN squad s ON s.id = sm.squad_id
        WHERE s.workspace_id = $1
          AND sm.member_type = 'member'
          AND sm.member_id   = %[1]s::uuid
       UNION
       SELECT s.id
         FROM squad s
         JOIN agent a ON a.id = s.leader_id
        WHERE s.workspace_id = $1
          AND a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
       UNION
       SELECT sm.squad_id
         FROM squad_member sm
         JOIN squad s ON s.id = sm.squad_id
         JOIN agent a ON a.id = sm.member_id
        WHERE s.workspace_id = $1
          AND sm.member_type = 'agent'
          AND a.workspace_id = $1
          AND a.owner_id     = %[1]s::uuid
    ))
)`, ref))
	}

	assigneeFilters, ok := parseActorFilterList(w, r.URL.Query().Get("assignee_filters"), "assignee_filters")
	if !ok {
		return
	}
	includeNoAssignee := r.URL.Query().Get("include_no_assignee") == "true"
	if len(assigneeFilters) > 0 || includeNoAssignee {
		ors := make([]string, 0, len(assigneeFilters)+1)
		for _, filter := range assigneeFilters {
			ors = append(ors, fmt.Sprintf(
				"(i.assignee_type = %s::text AND i.assignee_id = %s::uuid)",
				addArg(filter.actorType),
				addArg(filter.actorID),
			))
		}
		if includeNoAssignee {
			ors = append(ors, "(i.assignee_type IS NULL AND i.assignee_id IS NULL)")
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}

	creatorFilters, ok := parseActorFilterList(w, r.URL.Query().Get("creator_filters"), "creator_filters")
	if !ok {
		return
	}
	if len(creatorFilters) > 0 {
		ors := make([]string, 0, len(creatorFilters))
		for _, filter := range creatorFilters {
			ors = append(ors, fmt.Sprintf(
				"(i.creator_type = %s::text AND i.creator_id = %s::uuid)",
				addArg(filter.actorType),
				addArg(filter.actorID),
			))
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}

	projectIDs, ok := parseUUIDParamList(w, r.URL.Query().Get("project_ids"), "project_ids")
	if !ok {
		return
	}
	includeNoProject := r.URL.Query().Get("include_no_project") == "true"
	if len(projectIDs) > 0 || includeNoProject {
		ors := make([]string, 0, 2)
		if len(projectIDs) > 0 {
			ors = append(ors, fmt.Sprintf("i.project_id = ANY(%s::uuid[])", addArg(projectIDs)))
		}
		if includeNoProject {
			ors = append(ors, "i.project_id IS NULL")
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}

	labelIDs, ok := parseUUIDParamList(w, r.URL.Query().Get("label_ids"), "label_ids")
	if !ok {
		return
	}
	if len(labelIDs) > 0 {
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM issue_to_label itl WHERE itl.issue_id = i.id AND itl.label_id = ANY(%s::uuid[]))",
			addArg(labelIDs),
		))
	}

	dateFilter, ok := parseIssueDateFilter(w, r.URL.Query())
	if !ok {
		return
	}
	where = appendIssueDateFilter(where, addArg, dateFilter)

	if groupAssigneeType := r.URL.Query().Get("group_assignee_type"); groupAssigneeType != "" {
		if groupAssigneeType == "none" {
			where = append(where, "(i.assignee_type IS NULL AND i.assignee_id IS NULL)")
		} else {
			if !isIssueActorType(groupAssigneeType) {
				writeError(w, http.StatusBadRequest, "invalid group_assignee_type")
				return
			}
			rawID := r.URL.Query().Get("group_assignee_id")
			if rawID == "" {
				writeError(w, http.StatusBadRequest, "invalid group_assignee_id")
				return
			}
			assigneeID, ok := parseUUIDOrBadRequest(w, rawID, "group_assignee_id")
			if !ok {
				return
			}
			where = append(where, fmt.Sprintf(
				"(i.assignee_type = %s::text AND i.assignee_id = %s::uuid)",
				addArg(groupAssigneeType),
				addArg(assigneeID),
			))
		}
	}

	sortCol := "position"
	if s := r.URL.Query().Get("sort"); s != "" {
		switch s {
		case "position", "title", "created_at", "start_date", "due_date":
			sortCol = s
		case "priority":
			sortCol = "CASE i.priority WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 WHEN 'low' THEN 3 ELSE 4 END"
		default:
			writeError(w, http.StatusBadRequest, "invalid sort value")
			return
		}
	}
	sortDir := "ASC"
	if sortCol != "position" {
		if d := r.URL.Query().Get("direction"); d != "" {
			switch strings.ToLower(d) {
			case "asc":
				sortDir = "ASC"
			case "desc":
				sortDir = "DESC"
			default:
				writeError(w, http.StatusBadRequest, "invalid direction value")
				return
			}
		}
	}

	intraGroupOrder := sortCol
	if !strings.HasPrefix(sortCol, "CASE") {
		intraGroupOrder = "i." + sortCol
	}
	intraGroupOrder += " " + sortDir
	if sortCol == "start_date" || sortCol == "due_date" {
		intraGroupOrder += " NULLS LAST"
	}
	intraGroupOrder += ", i.created_at DESC"

	offsetRef := addArg(int64(offset))
	limitRef := addArg(int64(limit))
	query := fmt.Sprintf(`
WITH ranked AS (
	SELECT
		i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
		i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
		i.parent_issue_id, i.position, i.due_date, i.created_at, i.updated_at,
		i.number, i.project_id, i.metadata,
		COUNT(*) OVER (PARTITION BY i.assignee_type, i.assignee_id) AS group_total,
		ROW_NUMBER() OVER (
			PARTITION BY i.assignee_type, i.assignee_id
			ORDER BY %s
		) AS rn
	FROM issue i
	WHERE %s
)
SELECT
	id, workspace_id, title, description, status, priority,
	assignee_type, assignee_id, creator_type, creator_id,
	parent_issue_id, position, due_date, created_at, updated_at,
	number, project_id, metadata, group_total
FROM ranked
WHERE rn > %s AND rn <= %s + %s
ORDER BY
	CASE assignee_type
		WHEN 'member' THEN 0
		WHEN 'agent' THEN 1
		WHEN 'squad' THEN 2
		ELSE 3
	END,
	assignee_type NULLS LAST,
	assignee_id NULLS LAST,
	rn`, intraGroupOrder, strings.Join(where, " AND "), offsetRef, offsetRef, limitRef)

	rows, err := h.DB.Query(ctx, query, args...)
	if err != nil {
		slog.Warn("ListGroupedIssues query failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list grouped issues")
		return
	}
	defer rows.Close()

	groupedRows := []groupedIssueRow{}
	for rows.Next() {
		var row groupedIssueRow
		if err := rows.Scan(
			&row.ID,
			&row.WorkspaceID,
			&row.Title,
			&row.Description,
			&row.Status,
			&row.Priority,
			&row.AssigneeType,
			&row.AssigneeID,
			&row.CreatorType,
			&row.CreatorID,
			&row.ParentIssueID,
			&row.Position,
			&row.DueDate,
			&row.CreatedAt,
			&row.UpdatedAt,
			&row.Number,
			&row.ProjectID,
			&row.Metadata,
			&row.GroupTotal,
		); err != nil {
			slog.Warn("ListGroupedIssues scan failed", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to list grouped issues")
			return
		}
		groupedRows = append(groupedRows, row)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("ListGroupedIssues rows failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list grouped issues")
		return
	}

	ids := make([]pgtype.UUID, len(groupedRows))
	for i, row := range groupedRows {
		ids[i] = row.ID
	}
	labelsMap := h.labelsByIssue(ctx, wsUUID, ids)
	prefix := h.getIssuePrefix(ctx, wsUUID)
	// One Resolver for the whole page — a per-row filler would query the
	// catalog once per custom-status row. (MUL-6243)
	fillGrouped := h.newStatusCategoryFiller(ctx, wsUUID)

	groups := []IssueAssigneeGroupResponse{}
	groupIndex := map[string]int{}
	for _, row := range groupedRows {
		groupID := assigneeGroupID(row.AssigneeType, row.AssigneeID)
		idx, exists := groupIndex[groupID]
		if !exists {
			idx = len(groups)
			groupIndex[groupID] = idx
			groups = append(groups, IssueAssigneeGroupResponse{
				ID:           groupID,
				AssigneeType: textToPtr(row.AssigneeType),
				AssigneeID:   uuidToPtr(row.AssigneeID),
				Issues:       []IssueResponse{},
				Total:        row.GroupTotal,
			})
		}

		issue := issueListRowToResponse(row.ListIssuesRow, prefix)
		fillGrouped(&issue)
		labels := labelsMap[issue.ID]
		if labels == nil {
			labels = []LabelResponse{}
		}
		issue.Labels = &labels
		groups[idx].Issues = append(groups[idx].Issues, issue)
	}

	writeJSON(w, http.StatusOK, GroupedIssuesResponse{Groups: groups})
}

func (h *Handler) GetIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := issueToResponse(issue, prefix)
	h.fillStatusCategory(r.Context(), issue.WorkspaceID, &resp)
	detailLabels := h.labelsByIssue(r.Context(), issue.WorkspaceID, []pgtype.UUID{issue.ID})[uuidToString(issue.ID)]
	if detailLabels == nil {
		detailLabels = []LabelResponse{}
	}
	resp.Labels = &detailLabels

	// Fetch issue reactions.
	reactions, err := h.Queries.ListIssueReactions(r.Context(), issue.ID)
	if err == nil && len(reactions) > 0 {
		resp.Reactions = make([]IssueReactionResponse, len(reactions))
		for i, rx := range reactions {
			resp.Reactions[i] = issueReactionToResponse(rx)
		}
	}

	// Fetch issue-level attachments.
	attachments, err := h.Queries.ListAttachmentsByIssue(r.Context(), db.ListAttachmentsByIssueParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err == nil && len(attachments) > 0 {
		resp.Attachments = make([]AttachmentResponse, len(attachments))
		for i, a := range attachments {
			resp.Attachments[i] = h.attachmentToResponse(a)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) ListChildIssues(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	children, err := h.Queries.ListChildIssues(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list child issues")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	// Sub-issue progress is computed from these rows (the CLI's `issue children`
	// stage counts, among others), so they carry the resolved category — a
	// custom done status must count as done. One Resolver for the whole list:
	// built-in statuses still cost no query, and a list full of custom ones
	// costs one catalog read rather than one per row.
	statusResolver := issuestatus.NewResolver(issue.WorkspaceID)
	resp := make([]IssueResponse, len(children))
	for i, child := range children {
		resp[i] = issueToResponse(child, prefix)
		resp[i].StatusCategory = statusResolver.Effective(r.Context(), h.Queries, child.Status)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issues": resp,
	})
}

// Cap on the number of parents we'll fan-out children for in one request.
// Swimlane's visible-lane count is naturally bounded by what fits on screen
// (typically <= 50), but cap explicitly so a malicious caller can't ANY()
// across the whole workspace's issue set in a single round trip.
const listChildrenByParentsLimit = 200

// ListChildrenByParents returns the union of children for the
// provided parent ids. Replaces the N-call fan-out Swimlane would otherwise
// have to make on mount (one /issues/:id/children per visible parent lane).
//
// Workspace scope is enforced at the query level — any parent_id that doesn't
// belong to the caller's workspace simply yields zero children, so callers
// can't probe parents across workspace boundaries.
func (h *Handler) ListChildrenByParents(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	raw := r.URL.Query().Get("parent_ids")
	if raw == "" {
		// Empty input is a no-op response (not an error) — simplifies the
		// client which calls this unconditionally on Swimlane mount even
		// when there are zero visible parent lanes.
		writeJSON(w, http.StatusOK, map[string]any{"issues": []IssueResponse{}})
		return
	}

	parts := strings.Split(raw, ",")
	if len(parts) > listChildrenByParentsLimit {
		writeError(w, http.StatusBadRequest, "too many parent_ids")
		return
	}
	parentIDs := make([]pgtype.UUID, 0, len(parts))
	for _, s := range parts {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		id, ok := parseUUIDOrBadRequest(w, s, "parent_ids")
		if !ok {
			return
		}
		parentIDs = append(parentIDs, id)
	}
	if len(parentIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"issues": []IssueResponse{}})
		return
	}

	children, err := h.Queries.ListChildrenByParents(r.Context(), db.ListChildrenByParentsParams{
		WorkspaceID: wsUUID,
		ParentIds:   parentIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list child issues")
		return
	}
	prefix := h.getIssuePrefix(r.Context(), wsUUID)
	// Sub-issue progress is computed from these rows (the CLI's `issue children`
	// stage counts, among others), so they carry the resolved category — a
	// custom done status must count as done. One Resolver for the whole list:
	// built-in statuses still cost no query, and a list full of custom ones
	// costs one catalog read rather than one per row.
	statusResolver := issuestatus.NewResolver(wsUUID)
	resp := make([]IssueResponse, len(children))
	for i, child := range children {
		resp[i] = issueToResponse(child, prefix)
		resp[i].StatusCategory = statusResolver.Effective(r.Context(), h.Queries, child.Status)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issues": resp,
	})
}

func (h *Handler) ChildIssueProgress(w http.ResponseWriter, r *http.Request) {
	wsID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, wsID, "workspace_id")
	if !ok {
		return
	}

	terminalStatusKeys, err := h.terminalIssueStatusKeys(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve status categories")
		return
	}
	rows, err := h.Queries.ChildIssueProgress(r.Context(), db.ChildIssueProgressParams{
		WorkspaceID:        wsUUID,
		TerminalStatusKeys: terminalStatusKeys,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get child issue progress")
		return
	}

	type progressEntry struct {
		ParentIssueID string `json:"parent_issue_id"`
		Total         int64  `json:"total"`
		Done          int64  `json:"done"`
	}
	resp := make([]progressEntry, len(rows))
	for i, row := range rows {
		resp[i] = progressEntry{
			ParentIssueID: uuidToString(row.ParentIssueID),
			Total:         row.Total,
			Done:          row.Done,
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"progress": resp,
	})
}

// QuickCreateIssueRequest is the body for POST /api/issues/quick-create. The
// user picks an actor (agent or squad) in the modal and types one line of
// natural language; the server validates the actor's reachability up front,
// queues a quick-create task, and returns 202 immediately. The agent
// translates the prompt into a `multica issue create` invocation in the
// background; success and failure both surface as inbox notifications to
// the requester.
//
// Exactly one of AgentID / SquadID is required. When SquadID is set, the
// task is enqueued against the squad's leader agent and the leader receives
// the same Operating Protocol briefing it would for an issue assigned to
// the squad, so it can choose to delegate to a squad member as usual.
//
// ProjectID is optional and lets the modal target a specific project so
// the agent's `multica issue create` invocation passes `--project <uuid>`
// instead of letting it default. The frontend remembers the user's last
// pick per workspace, so frequent users skip retyping "in project X".
//
// ParentIssueID is optional and is set by the "Add sub issue" entry point
// when the modal is opened from an existing issue. The agent passes it
// through as `--parent <uuid>` so the new issue is filed as a sub-issue,
// keeping the sub-issue intent of the entry point regardless of whether
// the user submits via manual or agent mode.
type QuickCreateIssueRequest struct {
	AgentID       string   `json:"agent_id,omitempty"`
	SquadID       string   `json:"squad_id,omitempty"`
	Prompt        string   `json:"prompt"`
	ProjectID     string   `json:"project_id,omitempty"`
	ParentIssueID string   `json:"parent_issue_id,omitempty"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
}

// QuickCreateIssueResponse echoes the queued task id so the frontend can
// correlate the eventual inbox item, even though completion is fully async.
type QuickCreateIssueResponse struct {
	TaskID string `json:"task_id"`
}

func (h *Handler) QuickCreateIssue(w http.ResponseWriter, r *http.Request) {
	var req QuickCreateIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	hasAgent := strings.TrimSpace(req.AgentID) != ""
	hasSquad := strings.TrimSpace(req.SquadID) != ""
	if hasAgent == hasSquad {
		writeError(w, http.StatusBadRequest, "exactly one of agent_id or squad_id is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	requesterID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	requesterUUID, ok := parseUUIDOrBadRequest(w, requesterID, "requester_id")
	if !ok {
		return
	}

	// Resolve the actor to the agent that will actually run the task. For
	// agent picks that's the agent itself; for squad picks it's the squad's
	// leader agent. The leader receives a squad-leader briefing on dispatch
	// (see daemon.go), matching the behavior of an issue assigned to the
	// squad — picking a squad here is functionally "ask the squad leader to
	// create this issue, on behalf of the squad".
	var agentUUID pgtype.UUID
	var squadUUID pgtype.UUID
	if hasSquad {
		var ok bool
		squadUUID, ok = parseUUIDOrBadRequest(w, req.SquadID, "squad_id")
		if !ok {
			return
		}
		squad, err := h.Queries.GetSquadInWorkspace(r.Context(), db.GetSquadInWorkspaceParams{
			ID:          squadUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			writeError(w, http.StatusNotFound, "squad not found")
			return
		}
		if squad.ArchivedAt.Valid {
			writeError(w, http.StatusBadRequest, "squad is archived")
			return
		}
		agentUUID = squad.LeaderID
	} else {
		var ok bool
		agentUUID, ok = parseUUIDOrBadRequest(w, req.AgentID, "agent_id")
		if !ok {
			return
		}
	}

	// Reuse the same workspace-membership / archived / private-agent
	// ownership rules as `validateAssigneePair` so a user can't POST a
	// private agent_id they shouldn't be able to dispatch (the frontend
	// filters them out, but the handler is the trust boundary). Squad
	// picks reach this with the resolved leader agent; the same rules
	// apply — a private leader behind a squad the user can't reach
	// should still be rejected.
	if status, msg := h.validateAssigneePair(
		r.Context(), r, workspaceID,
		pgtype.Text{String: "agent", Valid: true},
		agentUUID,
	); status != 0 {
		writeError(w, status, msg)
		return
	}

	// Re-load the agent for the runtime liveness check below. Safe by
	// construction: validateAssigneePair just confirmed it exists in this
	// workspace and the caller has visibility.
	agent, err := h.Queries.GetAgentInWorkspace(r.Context(), db.GetAgentInWorkspaceParams{
		ID:          agentUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "agent not found")
		return
	}
	if !agent.RuntimeID.Valid {
		writeAgentUnavailable(w, "agent has no runtime")
		return
	}
	if !h.isRuntimeOnline(r.Context(), agent.RuntimeID) {
		writeAgentUnavailable(w, "agent's runtime is offline")
		return
	}

	// Daemon CLI version gate. The agent-side prompt + create-flow rely on
	// behaviors introduced in MinQuickCreateCLIVersion (URL attachment
	// handling, quick-create attachment binding, no-retry on partial failure).
	// Older daemons either double-create issues on partial CLI failures, drop
	// attachment bindings, or mishandle pasted screenshot URLs; fail closed
	// before enqueuing rather than surface the breakage as an inbox failure
	// twenty seconds later. Dev-built
	// daemons (git-describe shape) are exempted inside CheckMinCLIVersion
	// so `make daemon` works without weakening staging or production.
	if status, payload := h.checkQuickCreateDaemonVersion(r.Context(), agent.RuntimeID); status != 0 {
		writeJSON(w, status, payload)
		return
	}

	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}

	// Optional project_id — validate it belongs to the same workspace before
	// pinning the task to it. The handler is the trust boundary; the frontend
	// already only shows projects from the active workspace, but we re-check
	// here so a forged request can't smuggle a foreign project ID through.
	var projectUUID pgtype.UUID
	if strings.TrimSpace(req.ProjectID) != "" {
		pid, ok := parseUUIDOrBadRequest(w, req.ProjectID, "project_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
			ID:          pid,
			WorkspaceID: wsUUID,
		}); err != nil {
			writeError(w, http.StatusBadRequest, "project not found")
			return
		}
		projectUUID = pid
	}

	// Optional parent_issue_id — validate same-workspace membership just like
	// the regular CreateIssue path. Frontend seeds this from the "Add sub
	// issue" entry, but the handler re-checks so a forged request can't
	// smuggle a foreign parent UUID through.
	var parentIssueUUID pgtype.UUID
	if strings.TrimSpace(req.ParentIssueID) != "" {
		pid, ok := parseUUIDOrBadRequest(w, req.ParentIssueID, "parent_issue_id")
		if !ok {
			return
		}
		parent, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          pid,
			WorkspaceID: wsUUID,
		})
		if err != nil || !parent.ID.Valid {
			writeError(w, http.StatusBadRequest, "parent issue not found in this workspace")
			return
		}
		parentIssueUUID = pid
	}

	task, err := h.TaskService.EnqueueQuickCreateTask(r.Context(), wsUUID, requesterUUID, agentUUID, squadUUID, prompt, projectUUID, parentIssueUUID, attachmentIDs)
	if err != nil {
		slog.Warn("quick-create enqueue failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "failed to enqueue quick-create task")
		return
	}

	writeJSON(w, http.StatusAccepted, QuickCreateIssueResponse{TaskID: uuidToString(task.ID)})
}

// writeAgentUnavailable returns 422 with a stable error code so the modal
// can show a "switch agent" hint without parsing the human-readable reason.
func writeAgentUnavailable(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(map[string]any{
		"code":   "agent_unavailable",
		"reason": reason,
	})
}

// isRuntimeOnline returns true when the given runtime is currently
// reachable (status == "online"). Quick-create rejects submissions whose
// agent's runtime is offline so the user gets immediate feedback in the
// modal instead of an inbox failure twenty seconds later.
//
// Test-only override: when h.RuntimeOnlineOverride is non-nil the gate
// returns the dereferenced value without reading the DB. Production
// always leaves the field nil.
func (h *Handler) isRuntimeOnline(ctx context.Context, runtimeID pgtype.UUID) bool {
	if h.RuntimeOnlineOverride != nil {
		return *h.RuntimeOnlineOverride
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeID)
	if err != nil {
		return false
	}
	return rt.Status == "online"
}

// checkQuickCreateDaemonVersion enforces MinQuickCreateCLIVersion against the
// CLI version the daemon reported at registration time (stored on the runtime
// row's metadata.cli_version). Returns (0, nil) when the version is
// acceptable, otherwise (status, payload) ready to hand to writeJSON.
//
// Failure shape is stable so the modal can branch on the `code` field and
// surface a "needs upgrade" hint that points at the specific runtime:
//
//	422 {
//	  "code": "daemon_version_unsupported",
//	  "current_version": "0.2.18" | "",
//	  "min_version":     "0.2.21",
//	  "runtime_id":      "<uuid>"
//	}
func (h *Handler) checkQuickCreateDaemonVersion(ctx context.Context, runtimeID pgtype.UUID) (int, map[string]any) {
	// Test-only override (0.5.88): see Handler.QuickCreateVersionGateOverride.
	// Short-circuits before the metadata read so a parallel test's
	// metadata-restoring cleanup can never flake this gate.
	if h.QuickCreateVersionGateOverride != nil {
		return 0, nil
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeID)
	if err != nil {
		// Runtime row vanished between the online check and here — treat
		// as unavailable rather than wedging the request on a 500.
		return http.StatusUnprocessableEntity, map[string]any{
			"code":   "agent_unavailable",
			"reason": "agent's runtime is no longer registered",
		}
	}
	current := readRuntimeCLIVersion(rt.Metadata)
	switch err := agent.CheckMinCLIVersion(current); {
	case err == nil:
		return 0, nil
	case errors.Is(err, agent.ErrCLIVersionMissing), errors.Is(err, agent.ErrCLIVersionTooOld):
		return http.StatusUnprocessableEntity, map[string]any{
			"code":            "daemon_version_unsupported",
			"current_version": current,
			"min_version":     agent.MinQuickCreateCLIVersion,
			"runtime_id":      uuidToString(runtimeID),
		}
	default:
		// Defensive fall-through: unknown error from the version check is
		// also fail-closed, since the gate exists precisely because we
		// can't trust older daemons with this flow.
		return http.StatusUnprocessableEntity, map[string]any{
			"code":            "daemon_version_unsupported",
			"current_version": current,
			"min_version":     agent.MinQuickCreateCLIVersion,
			"runtime_id":      uuidToString(runtimeID),
		}
	}
}

// readRuntimeCLIVersion pulls metadata.cli_version off a runtime row. The
// metadata column is JSONB on the wire; the daemon stores the multica CLI
// version under that key during registration (see DaemonRegister).
func readRuntimeCLIVersion(metadata []byte) string {
	if len(metadata) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(metadata, &m); err != nil {
		return ""
	}
	if v, ok := m["cli_version"].(string); ok {
		return v
	}
	return ""
}

type CreateIssueRequest struct {
	Title         string   `json:"title"`
	Description   *string  `json:"description"`
	Status        string   `json:"status"`
	Priority      string   `json:"priority"`
	AssigneeType  *string  `json:"assignee_type"`
	AssigneeID    *string  `json:"assignee_id"`
	ParentIssueID *string  `json:"parent_issue_id"`
	ProjectID     *string  `json:"project_id"`
	Stage         *int32   `json:"stage,omitempty"`
	StartDate     *string  `json:"start_date"`
	DueDate       *string  `json:"due_date"`
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	LabSource     *string  `json:"lab_source,omitempty"`
	// LabMode (0.3.31): pairs with LabSource. nil means "do not
	// change" on UpdateIssue; explicit "" means "clear". On create,
	// nil means "no lab mode" (server defaults to NULL).
	// Values: "sole" (mythos owns the issue end-to-end) or
	// "enhancer" (mythos preludes + supervises; user-picked assignee
	// executes). Currently only meaningful when LabSource="mythos_swarm".
	LabMode *string `json:"lab_mode,omitempty"`
	// OriginType / OriginID stamp the new issue with its provenance so
	// platform-internal flows can deterministically locate it later. Only
	// trusted callers should set these — currently the daemon CLI passes
	// them through for quick-create tasks (origin_type=quick_create,
	// origin_id=agent_task_queue.id).
	OriginType *string `json:"origin_type,omitempty"`
	OriginID   *string `json:"origin_id,omitempty"`

	AllowDuplicate bool `json:"allow_duplicate,omitempty"`
}

func duplicateIssueMessage(issue IssueResponse) string {
	return issueguard.DuplicateMessage(issue.Identifier, issue.Title, issue.Status)
}

func (h *Handler) CreateIssue(w http.ResponseWriter, r *http.Request) {
	var req CreateIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}

	// Get creator from context (set by auth middleware)
	creatorID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	status := req.Status
	if status == "" {
		status = "todo"
	}
	priority := req.Priority
	if priority == "" {
		priority = "none"
	}
	status, ok = h.resolveIssueStatusKey(w, r, wsUUID, status)
	if !ok {
		return
	}
	if !validateIssueEnum(w, "priority", priority, validIssuePriorities) {
		return
	}
	if req.Stage != nil && *req.Stage < 1 {
		writeError(w, http.StatusBadRequest, "stage must be >= 1")
		return
	}

	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	if req.AssigneeType != nil {
		assigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
	}
	if req.AssigneeID != nil {
		id, ok := parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
		if !ok {
			return
		}
		assigneeID = id
	}

	// 0.3.31 → 0.3.33: lab ↔ assignee mutex.
	//
	// Originally every `lab_source` reserved the agent roster — that
	// was over-restrictive. By 0.3.33 most labs (claude_science_lab,
	// pythia_oracle, llm_wiki_bridge, code_canvas,
	// agent_self_optimization, chat_pin_ui)
	// ship with their own runtime agents / skills / environments and
	// no longer want manual assignees — pinning a single agent on
	// top is meaningless and the gate just gets in the way of the
	// user's "tag the issue, the lab figures it out" workflow.
	//
	// Two sources keep the mutex:
	//   - `mythos_swarm` — its 5-agent RDT roster is meaningful
	//     enough that the user might want to override the default
	//     assignment, *or* in enhancer mode MUST pair with a target
	//     assignee (mythos preludes + supervises while the chosen
	//     actor executes).
	//   - `swarm_topology` (0.5.21) — self-organising multi-agent
	//     system parallel to claude_science_lab; user explicitly
	//     designed it so the swarm IS the assignee (no manual
	//     override allowed). lab_mode='enhancer' is NOT supported
	//     for swarm_topology — the swarm owns the issue end-to-end
	//     across all phases.
	//
	// This gate runs BEFORE validateAssigneePair so a non-existent
	// member/agent row never produces a confusing 400 ("does not
	// refer to a member") when the real issue is the contract
	// violation.
	if req.LabMode != nil && !validLabModeValue(*req.LabMode) {
		writeError(w, http.StatusBadRequest, "lab_mode must be 'sole' or 'enhancer'")
		return
	}
	if req.LabSource != nil {
		enhancerMode := req.LabMode != nil && *req.LabMode == "enhancer"
		hasAssignee := assigneeType.Valid || assigneeID.Valid
		labSource := *req.LabSource
		// 0.5.90 OpenMythos: sole mode is disabled for NEW bindings —
		// the swarm lab runs exclusively as the enhancer outer loop
		// paired with a user-picked target assignee. Legacy sole-bound
		// issues keep resolving (forward-only law); this gate only
		// rejects writes that would CREATE a new sole binding.
		if labSource == "mythos_swarm" && req.LabMode != nil && *req.LabMode == "sole" {
			writeError(w, http.StatusBadRequest,
				"lab_mode='sole' is disabled for mythos_swarm (OpenMythos): the outer loop runs only in enhancer mode with a target assignee")
			return
		}
		switch {
		case !enhancerMode && hasAssignee:
			// 0.5.86 assignee-lock: InteractionModelAssignee labs own
			// the assignee slot. Replaces the 0.3.33 hardcoded
			// mythos/swarm pair — same strictness for those two
			// (mythos resolves no leader → any assignee still 400s;
			// swarm_topology now also accepts its own coordinator),
			// plus the leader check for the rest of the family.
			assigneeTypeStr := ""
			if assigneeType.Valid {
				assigneeTypeStr = assigneeType.String
			}
			assigneeIDStr := ""
			if assigneeID.Valid {
				assigneeIDStr = uuidToString(assigneeID)
			}
			if msg := h.assigneeLabLockError(r.Context(), wsUUID, labSource, assigneeTypeStr, assigneeIDStr); msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
		case enhancerMode && !hasAssignee && labSource == "mythos_swarm":
			writeError(w, http.StatusBadRequest,
				"lab_mode=enhancer requires an assignee (the target agent or squad)")
			return
		case enhancerMode && labSource == "swarm_topology":
			writeError(w, http.StatusBadRequest,
				"lab_mode=enhancer is not supported for lab_source=swarm_topology; the swarm owns the issue end-to-end")
			return
		case enhancerMode && labSource != "mythos_swarm":
			// 0.5.60 (audit P1-1): parity with UpdateIssue. Pre-fix,
			// Create accepted enhancer on ANY lab (201) while Update
			// rejected the exact same state (400) — such a row could
			// never be PATCHed again.
			writeError(w, http.StatusBadRequest,
				"lab_mode=enhancer is only supported when lab_source=mythos_swarm")
			return
		}
	}

	if status, msg := h.validateAssigneePair(r.Context(), r, workspaceID, assigneeType, assigneeID); status != 0 {
		writeError(w, status, msg)
		return
	}

	var parentIssueID pgtype.UUID
	var projectID pgtype.UUID
	if req.ProjectID != nil {
		id, ok := parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
		if !ok {
			return
		}
		projectID = id
	}
	if req.ParentIssueID != nil {
		id, ok := parseUUIDOrBadRequest(w, *req.ParentIssueID, "parent_issue_id")
		if !ok {
			return
		}
		parentIssueID = id
	}
	// Cross-workspace parent / project existence is enforced inside
	// IssueService.Create (atomically with the create), so every entry
	// point — HTTP, Lark, future MCP — gets the same boundary check
	// without duplicating the lookup here.

	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}

	var startDate pgtype.Date
	if req.StartDate != nil && *req.StartDate != "" {
		d, err := util.ParseCalendarDate(*req.StartDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid start_date format, expected YYYY-MM-DD")
			return
		}
		startDate = d
	}

	var dueDate pgtype.Date
	if req.DueDate != nil && *req.DueDate != "" {
		d, err := util.ParseCalendarDate(*req.DueDate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid due_date format, expected YYYY-MM-DD")
			return
		}
		dueDate = d
	}

	// Determine creator identity: agent (via X-Agent-ID header) or member.
	creatorType, actualCreatorID := h.resolveActor(r, creatorID, workspaceID)

	// Optional origin stamping (quick-create / autopilot). Only the
	// allowed origin types are accepted; anything else is rejected so a
	// rogue caller can't mint arbitrary origin labels. Both fields must
	// be provided together.
	var originType pgtype.Text
	var originID pgtype.UUID
	if req.OriginType != nil || req.OriginID != nil {
		if req.OriginType == nil || req.OriginID == nil {
			writeError(w, http.StatusBadRequest, "origin_type and origin_id must be provided together")
			return
		}
		switch *req.OriginType {
		case "quick_create":
			// Allowed — daemon CLI passes this through from a quick-create task.
		default:
			writeError(w, http.StatusBadRequest, "unsupported origin_type")
			return
		}
		oid, ok := parseUUIDOrBadRequest(w, *req.OriginID, "origin_id")
		if !ok {
			return
		}
		originType = pgtype.Text{String: *req.OriginType, Valid: true}
		originID = oid
	} else if creatorType == "agent" {
		// MUL-4305: an agent creating an issue via the ordinary create path
		// carries no explicit origin, which historically left the new issue
		// unattributed. Any run later derived from it (agent assignment,
		// squad-leader trigger) then lost the top-of-chain human originator,
		// so A2A @-mentions from those runs failed the canInvokeAgent gate
		// against private agents. Stamp the acting task as the issue's origin
		// so resolveOriginatorForIssueTask can inherit its originator — the
		// same trick CreateComment uses with comment.source_task_id (MUL-4015).
		//
		// The task id is taken from the SERVER-trusted X-Task-ID: resolveActor
		// only returns creatorType=="agent" when either X-Actor-Source=task_token
		// (the auth middleware bound X-Agent-ID/X-Task-ID from the mat_ token and
		// stripped any client value) or the X-Agent-ID/X-Task-ID pair was
		// validated against the DB. A member-forged X-Task-ID never reaches here
		// because it would have resolved to creatorType=="member". We still
		// re-check the task belongs to the acting agent before trusting it.
		if taskIDHeader := r.Header.Get("X-Task-ID"); taskIDHeader != "" {
			if taskUUID, perr := util.ParseUUID(taskIDHeader); perr == nil {
				if task, terr := h.Queries.GetAgentTask(r.Context(), taskUUID); terr == nil && uuidToString(task.AgentID) == actualCreatorID {
					originType = pgtype.Text{String: "agent_create", Valid: true}
					originID = taskUUID
				}
			}
		}
	}

	// Prefix is workspace-level; pre-compute once so both the broadcast
	// payload builder and the HTTP response share the same value.
	prefix := h.getIssuePrefix(r.Context(), wsUUID)

	// One filler for this create, shared by the broadcast payload and the HTTP
	// response below, so a custom-status create reads the catalog once per
	// request rather than once per payload. (MUL-6243)
	fillCreated := h.newStatusCategoryFiller(r.Context(), wsUUID)

	// Analytics agent ID: assignee agent when the issue is being assigned
	// to an agent, otherwise the creator agent for agent-authored issues.
	// Resolved here (not in the service) because creator identity is HTTP-side.
	analyticsAgentID := ""
	if assigneeType.Valid && assigneeType.String == "agent" {
		analyticsAgentID = uuidToString(assigneeID)
	}
	if creatorType == "agent" && analyticsAgentID == "" {
		analyticsAgentID = actualCreatorID
	}

	// 0.3.26: validate lab_source against the catalog. Mirrors the same
	// gate in UpdateIssue so a curl POST with `lab_source: "bogus"` is
	// rejected before reaching IssueService.Create — see the long-form
	// comment in UpdateIssue for the rationale.
	if req.LabSource != nil && !experimental.IsKnownKey(*req.LabSource) {
		writeError(w, http.StatusBadRequest,
			"lab_source must match a known experimental flag key")
		return
	}
	// 0.5.105 (audit H3): frozen labs reject NEW bindings entirely.
	if req.LabSource != nil {
		if msg := frozenLabSourceBindError(*req.LabSource); msg != "" {
			writeError(w, http.StatusBadRequest, msg)
			return
		}
	}

	// 0.3.31: lab ↔ assignee mutex — checked earlier (right after
	// parseUUIDOrBadRequest) so it wins over validateAssigneePair. The
	// catalog check above is a precondition for that earlier check, so
	// it has to come first.

	buildAttachmentResponses := func(atts []db.Attachment) []AttachmentResponse {
		if len(atts) == 0 {
			return nil
		}
		out := make([]AttachmentResponse, len(atts))
		for i, a := range atts {
			out[i] = h.attachmentToResponse(a)
		}
		return out
	}

	res, err := h.IssueService.Create(r.Context(), service.IssueCreateParams{
		WorkspaceID:    wsUUID,
		Title:          req.Title,
		Description:    ptrToText(req.Description),
		Status:         status,
		Priority:       priority,
		AssigneeType:   assigneeType,
		AssigneeID:     assigneeID,
		CreatorType:    creatorType,
		CreatorID:      parseUUID(actualCreatorID),
		ParentIssueID:  parentIssueID,
		ProjectID:      projectID,
		StartDate:      startDate,
		DueDate:        dueDate,
		OriginType:     originType,
		OriginID:       originID,
		Stage:          ptrToInt4(req.Stage),
		LabSource:      ptrToText(req.LabSource),
		LabMode:        ptrToText(req.LabMode),
		AttachmentIDs:  attachmentIDs,
		AllowDuplicate: req.AllowDuplicate,
	}, service.IssueCreateOpts{
		ActorID:          actualCreatorID,
		AnalyticsAgentID: analyticsAgentID,
		Platform:         func() string { p, _, _ := middleware.ClientMetadataFromContext(r.Context()); return p }(),
		BroadcastPayload: func(issue db.Issue, atts []db.Attachment) map[string]any {
			payload := issueToResponse(issue, prefix)
			// The event other tabs receive must carry the category too — filling
			// only the HTTP response below is too late for them, and a create
			// they cannot bucket forces a full refetch. Shares one filler with
			// the HTTP response so a custom-status create reads the catalog once
			// per request, not once per payload. (MUL-6243)
			fillCreated(&payload)
			payload.Attachments = buildAttachmentResponses(atts)
			return map[string]any{"issue": payload}
		},
	})

	if errors.Is(err, service.ErrActiveDuplicate) {
		dup := *res.DuplicateIssue
		existing := issueToResponse(dup, h.getIssuePrefix(r.Context(), dup.WorkspaceID))
		h.fillStatusCategory(r.Context(), dup.WorkspaceID, &existing)
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":  "active_duplicate_issue",
			"error": duplicateIssueMessage(existing),
			"issue": existing,
		})
		return
	}
	if errors.Is(err, service.ErrParentIssueNotFound) {
		writeError(w, http.StatusBadRequest, "parent issue not found in this workspace")
		return
	}
	if errors.Is(err, service.ErrProjectNotFound) {
		writeError(w, http.StatusBadRequest, "project not found in this workspace")
		return
	}
	if errors.Is(err, service.ErrIssueStatusUnavailable) {
		writeError(w, http.StatusConflict,
			"the target status was archived while this request was in flight; reload the status list and retry")
		return
	}
	if err != nil {
		slog.Warn("create issue failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeInternalError(w, "create issue", err)
		return
	}

	issue := res.Issue
	slog.Info("issue created", append(logger.RequestAttrs(r), "issue_id", uuidToString(issue.ID), "title", issue.Title, "status", issue.Status, "workspace_id", workspaceID)...)

	// 0.3.31: persist issue.lab_mode alongside lab_source. The SQL
	// CreateIssue path does not accept LabMode (it stays a sqlc
	// generated row with the explicit column list) so we issue a
	// single follow-up UPDATE. Best-effort: a write failure here
	// would leave lab_source set but lab_mode NULL, which the
	// renderer treats as 'sole' anyway. Logging only; not fatal.
	if req.LabMode != nil && *req.LabMode != "" {
		if err := h.Queries.UpdateIssueLabMode(r.Context(), db.UpdateIssueLabModeParams{
			ID:          issue.ID,
			LabMode:     pgtype.Text{String: *req.LabMode, Valid: true},
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("create issue: persist lab_mode failed",
				append(logger.RequestAttrs(r),
					"issue_id", uuidToString(issue.ID),
					"lab_mode", *req.LabMode,
					"error", err)...)
		} else {
			// Mirror the written value onto the in-memory row so the
			// response body reflects the persisted state instead of
			// the CreateIssueParams default (NULL).
			issue.LabMode = pgtype.Text{String: *req.LabMode, Valid: true}
		}
	}

	// 0.5.88 delegation loop: when the created issue is BOTH lab-bound AND
	// a sub-issue (parent_issue_id + lab_source — exactly the shape
	// `multica lab delegate --parent` produces), record a causal
	// parent --depends_on--> child edge so the linkage is visible to the
	// claim-time subgraph briefing on either side. Placed in the create
	// success path after the lab_mode persist (CreateIssue has no earlier
	// causal touch — issue root nodes are ensured inside the recorder, so
	// both endpoints exist by the time the edge lands). Best-effort: WRN +
	// continue on every error inside the recorder; nil-safe and
	// flag-gated (fail-closed) exactly like the RefreshForIssue call
	// sites. Issue creation must NEVER fail because of this.
	if h.CausalRecorder != nil && parentIssueID.Valid && req.LabSource != nil && *req.LabSource != "" {
		h.CausalRecorder.RecordDelegationEdge(r.Context(), issue)
	}

	resp := issueToResponse(issue, prefix)
	fillCreated(&resp)
	resp.Attachments = buildAttachmentResponses(res.Attachments)
	writeJSON(w, http.StatusCreated, resp)
}

// validLabModeValue reports whether v is an acceptable issue.lab_mode
// value. The migration-157 CHECK accepts only 'sole' | 'enhancer';
// validating at the HTTP boundary turns a bogus value into a clean 400
// instead of a 23514 CHECK violation surfacing as a 500 (audit P1-2).
// Empty string means "clear / unset" on the write paths and is accepted.
func validLabModeValue(v string) bool {
	return v == "" || v == "sole" || v == "enhancer"
}

type UpdateIssueRequest struct {
	Title         *string  `json:"title"`
	Description   *string  `json:"description"`
	Status        *string  `json:"status"`
	Priority      *string  `json:"priority"`
	AssigneeType  *string  `json:"assignee_type"`
	AssigneeID    *string  `json:"assignee_id"`
	Position      *float64 `json:"position"`
	StartDate     *string  `json:"start_date"`
	DueDate       *string  `json:"due_date"`
	ParentIssueID *string  `json:"parent_issue_id"`
	ProjectID     *string  `json:"project_id"`
	Stage         *int32   `json:"stage"`
	LabSource     *string  `json:"lab_source"`
	// LabMode (0.3.31): see the matching field on CreateIssueRequest.
	// nil on update = leave existing value untouched; explicit "" =
	// clear to NULL. Currently only meaningful when LabSource="mythos_swarm".
	LabMode *string `json:"lab_mode"`
	// AttachmentIDs lets the description editor bind newly uploaded files to
	// this issue so they surface in `GET /api/issues/:id/attachments` and the
	// editor's preview Eye keeps working past a refresh. Existing bindings
	// are idempotent — re-sending the same id is a no-op.
	AttachmentIDs []string `json:"attachment_ids"`
	// SuppressRun, when true, applies the assignee/status change as usual but
	// skips starting the agent run this write would otherwise trigger
	// ("暂时不启动" — MUL-3375). It is not an undo: the change takes effect and
	// the issue can be run later via manual run/rerun. Optional; omitted or
	// false keeps today's behavior. Mirrors comment suppress_agent_ids.
	SuppressRun bool `json:"suppress_run,omitempty"`
	// HandoffNote is an optional free-text instruction injected into the run's
	// opening context when this write starts an agent/squad run ("交接说明" —
	// MUL-3375). Only consumed when a run actually starts: SuppressRun=true or
	// a parked/non-triggering write drops it. Never fabricates a comment.
	HandoffNote string `json:"handoff_note,omitempty"`
}

func (h *Handler) UpdateIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	prevIssue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}
	userID := requestUserID(r)
	workspaceID := uuidToString(prevIssue.WorkspaceID)

	// Read body as raw bytes so we can detect which fields were explicitly sent.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req UpdateIssueRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Track which fields were explicitly present in JSON (even if null)
	var rawFields map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawFields)

	// Pre-fill nullable fields (bare sqlc.narg) with current values
	params := db.UpdateIssueParams{
		ID:            prevIssue.ID,
		AssigneeType:  prevIssue.AssigneeType,
		AssigneeID:    prevIssue.AssigneeID,
		StartDate:     prevIssue.StartDate,
		DueDate:       prevIssue.DueDate,
		ParentIssueID: prevIssue.ParentIssueID,
		ProjectID:     prevIssue.ProjectID,
		Stage:         prevIssue.Stage,
		LabSource:     prevIssue.LabSource,
	}

	// COALESCE fields — only set when explicitly provided
	if req.Title != nil {
		params.Title = pgtype.Text{String: *req.Title, Valid: true}
	}
	if req.Description != nil {
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	// statusKeyForGuard is the resolved key when this request sets a status, and
	// empty otherwise. Empty means "this write does not touch status", which the
	// guard treats as nothing to protect.
	statusKeyForGuard := ""
	if req.Status != nil {
		statusKey, _, ok := h.resolveIssueStatusKeyKind(w, r, prevIssue.WorkspaceID, *req.Status)
		if !ok {
			return
		}
		statusKeyForGuard = statusKey
		params.Status = pgtype.Text{String: statusKey, Valid: true}
	}
	if req.Priority != nil {
		if !validateIssueEnum(w, "priority", *req.Priority, validIssuePriorities) {
			return
		}
		params.Priority = pgtype.Text{String: *req.Priority, Valid: true}
	}
	if req.Position != nil {
		params.Position = pgtype.Float8{Float64: *req.Position, Valid: true}
	}
	// Nullable fields — only override when explicitly present in JSON
	if _, ok := rawFields["assignee_type"]; ok {
		if req.AssigneeType != nil {
			params.AssigneeType = pgtype.Text{String: *req.AssigneeType, Valid: true}
		} else {
			params.AssigneeType = pgtype.Text{Valid: false} // explicit null = unassign
		}
	}
	if _, ok := rawFields["assignee_id"]; ok {
		if req.AssigneeID != nil {
			id, ok := parseUUIDOrBadRequest(w, *req.AssigneeID, "assignee_id")
			if !ok {
				return
			}
			params.AssigneeID = id
		} else {
			params.AssigneeID = pgtype.UUID{Valid: false} // explicit null = unassign
		}
	}
	if _, ok := rawFields["start_date"]; ok {
		if req.StartDate != nil && *req.StartDate != "" {
			d, err := util.ParseCalendarDate(*req.StartDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid start_date format, expected YYYY-MM-DD")
				return
			}
			params.StartDate = d
		} else {
			params.StartDate = pgtype.Date{Valid: false} // explicit null = clear date
		}
	}
	if _, ok := rawFields["due_date"]; ok {
		if req.DueDate != nil && *req.DueDate != "" {
			d, err := util.ParseCalendarDate(*req.DueDate)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid due_date format, expected YYYY-MM-DD")
				return
			}
			params.DueDate = d
		} else {
			params.DueDate = pgtype.Date{Valid: false} // explicit null = clear date
		}
	}
	if _, ok := rawFields["parent_issue_id"]; ok {
		if req.ParentIssueID != nil {
			newParentID, ok := parseUUIDOrBadRequest(w, *req.ParentIssueID, "parent_issue_id")
			if !ok {
				return
			}
			// Cannot set self as parent. Compare against prevIssue.ID (the
			// resolved entity), not the raw URL string — `id` may be an
			// identifier like "MUL-7".
			if newParentID == prevIssue.ID {
				writeError(w, http.StatusBadRequest, "an issue cannot be its own parent")
				return
			}
			// Validate parent exists in the same workspace.
			if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
				ID:          newParentID,
				WorkspaceID: prevIssue.WorkspaceID,
			}); err != nil {
				writeError(w, http.StatusBadRequest, "parent issue not found in this workspace")
				return
			}
			// Cycle detection: walk up from the new parent to ensure we don't reach this issue.
			cursor := newParentID
			for depth := 0; depth < 10; depth++ {
				ancestor, err := h.Queries.GetIssue(r.Context(), cursor)
				if err != nil || !ancestor.ParentIssueID.Valid {
					break
				}
				if ancestor.ParentIssueID == prevIssue.ID {
					writeError(w, http.StatusBadRequest, "circular parent relationship detected")
					return
				}
				cursor = ancestor.ParentIssueID
			}
			params.ParentIssueID = newParentID
		} else {
			params.ParentIssueID = pgtype.UUID{Valid: false} // explicit null = remove parent
		}
	}
	if _, ok := rawFields["project_id"]; ok {
		if req.ProjectID != nil {
			projectUUID, ok := parseUUIDOrBadRequest(w, *req.ProjectID, "project_id")
			if !ok {
				return
			}
			if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
				ID:          projectUUID,
				WorkspaceID: prevIssue.WorkspaceID,
			}); err != nil {
				if !isNotFound(err) {
					slog.Error("update issue: validate project scope",
						append(logger.RequestAttrs(r), "project_id", uuidToString(projectUUID), "error", err)...)
					writeError(w, http.StatusInternalServerError, "failed to validate project")
					return
				}
				writeError(w, http.StatusBadRequest, "project not found in this workspace")
				return
			}
			params.ProjectID = projectUUID
		} else {
			params.ProjectID = pgtype.UUID{Valid: false}
		}
	}
	if _, ok := rawFields["stage"]; ok {
		if req.Stage != nil {
			if *req.Stage < 1 {
				writeError(w, http.StatusBadRequest, "stage must be >= 1")
				return
			}
			params.Stage = pgtype.Int4{Int32: *req.Stage, Valid: true}
		} else {
			params.Stage = pgtype.Int4{Valid: false} // explicit null = unstage
		}
	}
	// 0.3.31: lab ↔ assignee mutex. Same rule as CreateIssue — a
	// non-empty `lab_source` reserves the agent roster, so any
	// non-empty assignee on the resulting issue is a contradiction.
	// Runs BEFORE the catalog / IsKnownKey check so the contract
	// violation is the user-facing error (not a confusing
	// "lab_source must match a known experimental flag key"), and
	// BEFORE `validateAssigneePair` so a non-existent member/agent
	// row never produces a misleading "does not refer to a member"
	// error when the real issue is the contract violation.
	//
	// Gate trigger: the mutex fires when EITHER side of the
	// (lab_source, assignee) pair will be non-empty on the resulting
	// issue. We compute the post-PATCH state explicitly because
	// `rawFields` is per-key — checking only `rawFields["lab_source"]`
	// would miss the case where the caller PATCHes only the
	// assignee on a pre-existing lab-tagged issue (the lab
	// outlives the PATCH, the assignee lands under it, the
	// contract is broken, but rawFields never reports the lab).
	// Symmetric: PATCHing only `lab_source` to a non-empty value
	// when the issue already has an assignee must also fail.
	if _, ok := rawFields["lab_mode"]; ok && req.LabMode != nil && !validLabModeValue(*req.LabMode) {
		// 0.5.60 (audit P1-2): boundary-validate the enum so a bogus
		// value 400s here instead of dying on the mig-157 CHECK (23514)
		// as a 500 deep in the write.
		writeError(w, http.StatusBadRequest, "lab_mode must be 'sole' or 'enhancer'")
		return
	}
	{
		var postLab string
		if _, ok := rawFields["lab_source"]; ok && req.LabSource != nil {
			postLab = *req.LabSource
		} else {
			postLab = prevIssue.LabSource.String
		}
		if postLab != "" {
			// Resolve post-PATCH assignee. Explicit `null` in the
			// body surfaces as req.AssigneeType / req.AssigneeID
			// being a non-nil pointer to an empty string / zero
			// UUID — we treat that as "user explicitly cleared this
			// field", which is the only way to atomically PATCH
			// `lab_source: "x"` + clear an existing assignee in a
			// single round trip.
			var postAssigneeType string
			var postAssigneeID string
			if _, ok := rawFields["assignee_type"]; ok && req.AssigneeType != nil {
				postAssigneeType = *req.AssigneeType
			} else {
				postAssigneeType = prevIssue.AssigneeType.String
			}
			if _, ok := rawFields["assignee_id"]; ok && req.AssigneeID != nil {
				postAssigneeID = *req.AssigneeID
			} else {
				postAssigneeID = uuidToString(prevIssue.AssigneeID)
			}
			hasAssignee := postAssigneeType != "" || postAssigneeID != ""
			// 0.3.31: enhancer-mode exception. Resolved post-update so
			// a client can switch mode in a single PATCH without an
			// intermediate state. lab_mode is read from the request
			// first, then falls back to the persisted value.
			postLabMode := prevIssue.LabMode.String
			if _, ok := rawFields["lab_mode"]; ok && req.LabMode != nil {
				postLabMode = *req.LabMode
			}
			enhancerMode := postLabMode == "enhancer"
			postLabSource := prevIssue.LabSource.String
			if _, ok := rawFields["lab_source"]; ok && req.LabSource != nil {
				postLabSource = *req.LabSource
			}
			// 0.5.90 OpenMythos parity with CreateIssue: reject PATCHes
			// that would (re)bind a sole mythos binding. Scoped to writes
			// that touch lab_mode, so legacy sole-bound issues stay
			// editable in every other field (forward-only law).
			if postLabSource == "mythos_swarm" && postLabMode == "sole" {
				if _, touchedMode := rawFields["lab_mode"]; touchedMode {
					writeError(w, http.StatusBadRequest,
						"lab_mode='sole' is disabled for mythos_swarm (OpenMythos): the outer loop runs only in enhancer mode with a target assignee")
					return
				}
			}
			// Mirror the same 0.3.33 narrowing as CreateIssue. Two
			// sources keep the mutex: mythos_swarm (with enhancer
			// reverse-requirement) and swarm_topology (0.5.21,
			// lock-to-coordinator only, no enhancer). Other labs
			// are auto-dispatched by their own runtime now, so the
			// user is free to tag a lab and keep a manual assignee
			// if the workspace has one.
			//
			// 0.5.86 assignee-lock: the hardcoded pair is replaced by
			// the InteractionModelAssignee gate (CreateIssue parity),
			// scoped by WHAT the PATCH touches:
			//   - assignee fields touched → full lock check on the
			//     post-state (reassigning an assignee-model lab issue
			//     to anyone but the leader 400s; legacy human rows
			//     stay editable in every other field).
			//   - lab_source only (stale assignee) → allowed for
			//     assignee-model labs ONLY when the leader AGENT ROW
			//     exists in this workspace, so the 0.3.46 P0#4
			//     leader-rewrite can actually land (pinned by
			//     TestUpdateIssueLabSourceRewritesStaleAssignee).
			//     A name-level hit with no row — swarm_topology's
			//     swarm_coordinator is provisioned dynamically at
			//     swarm bootstrap — would leave the human assignee
			//     in place, the exact state the 0.5.22/0.5.60 swarm
			//     mutex pins forbid, so those 400. Legacy/auxiliary
			//     labs keep the tag-and-keep-assignee freedom.
			_, touchedAssigneeType := rawFields["assignee_type"]
			_, touchedAssigneeID := rawFields["assignee_id"]
			touchedAssignee := touchedAssigneeType || touchedAssigneeID
			touchedLab := func() bool { _, ok := rawFields["lab_source"]; return ok }()
			switch {
			case !enhancerMode && hasAssignee && touchedAssignee:
				if msg := h.assigneeLabLockError(r.Context(), prevIssue.WorkspaceID, postLabSource, postAssigneeType, postAssigneeID); msg != "" {
					writeError(w, http.StatusBadRequest, msg)
					return
				}
			case !enhancerMode && hasAssignee && touchedLab:
				// Only assignee-model labs go through the
				// leader-rewrite bypass, and only when the bypass
				// can actually complete: the leader AGENT ROW must
				// exist in this workspace (swarm_topology's
				// swarm_coordinator is created dynamically at swarm
				// bootstrap, so name-level resolution alone would
				// 200 and strand the human assignee — the exact
				// state the 0.5.22/0.5.60 swarm mutex pins forbid).
				if experimental.IsAssigneeModelLab(postLabSource) {
					leaderName, leaderOK := h.resolveLabLeader(r.Context(), postLabSource)
					leaderPresent := false
					if leaderOK && leaderName != "" {
						if _, err := h.Queries.GetAgentByWorkspaceAndName(r.Context(), db.GetAgentByWorkspaceAndNameParams{
							WorkspaceID: prevIssue.WorkspaceID,
							Name:        leaderName,
						}); err == nil {
							leaderPresent = true
						}
					}
					if !leaderPresent {
						msg := "lab_source=" + postLabSource + " requires the lab to own the assignee; clear the manual assignee"
						if leaderOK && leaderName != "" {
							msg = "lab_source=" + postLabSource + " locks the assignee to its lab agent (" + leaderName + "), but that agent is not installed yet; enable the lab first or clear the manual assignee"
						}
						writeError(w, http.StatusBadRequest, msg)
						return
					}
				}
			case enhancerMode && !hasAssignee:
				writeError(w, http.StatusBadRequest,
					"lab_mode=enhancer requires an assignee (the target agent or squad)")
				return
			case enhancerMode && postLabSource != "mythos_swarm":
				writeError(w, http.StatusBadRequest,
					"lab_mode=enhancer is only supported when lab_source=mythos_swarm")
				return
			}
		}
	}

	if _, ok := rawFields["lab_source"]; ok {
		if req.LabSource != nil {
			// 0.3.26: validate lab_source against the catalog. The
			// frontend `LabPicker` already filters to known keys
			// (since 0.3.26 A1), but POST/PATCH from outside the
			// picker (curl scripts, custom clients, future API
			// consumers) used to persist any string. A typo-bound
			// `lab_source` row is invisible to Labs UI and gives the
			// list-row FlaskConical pill an undefined title. Match
			// the same `IsKnownKey` gate `UpdateExperimentalFlag`
			// uses for the same reason.
			if !experimental.IsKnownKey(*req.LabSource) {
				writeError(w, http.StatusBadRequest,
					"lab_source must match a known experimental flag key")
				return
			}
			// 0.5.105 (audit H3): frozen labs reject NEW bindings.
			if msg := frozenLabSourceBindError(*req.LabSource); msg != "" {
				writeError(w, http.StatusBadRequest, msg)
				return
			}
			params.LabSource = pgtype.Text{String: *req.LabSource, Valid: true}
		} else {
			params.LabSource = pgtype.Text{Valid: false} // explicit null = remove lab
		}
	}

	// Validate the resulting (assignee_type, assignee_id) pair when the caller
	// touches either field. Existing data on the issue is left alone if the
	// caller is not changing it.
	_, touchedType := rawFields["assignee_type"]
	_, touchedID := rawFields["assignee_id"]
	if touchedType || touchedID {
		if status, msg := h.validateAssigneePair(r.Context(), r, workspaceID, params.AssigneeType, params.AssigneeID); status != 0 {
			writeError(w, status, msg)
			return
		}
	}

	attachmentIDs, ok := parseUUIDSliceOrBadRequest(w, req.AttachmentIDs, "attachment_ids")
	if !ok {
		return
	}

	// A write landing on a custom status re-verifies it under the shared
	// catalog lock inside a transaction (see runWithIssueStatusGuard); a
	// built-in target skips the transaction entirely. The description is
	// replaced in the same UPDATE, so this single guard covers the whole
	// write. (MUL-6243)
	var issue db.Issue
	err = h.runWithIssueStatusGuard(r.Context(), prevIssue.WorkspaceID, statusKeyForGuard, func(q *db.Queries) error {
		var innerErr error
		issue, innerErr = q.UpdateIssue(r.Context(), params)
		return innerErr
	})
	if writeIssueStatusRaceError(w, err) {
		return
	}
	if err != nil {
		slog.Warn("update issue failed", append(logger.RequestAttrs(r), "error", err, "issue_id", id, "workspace_id", workspaceID)...)
		writeInternalError(w, "update issue", err)
		return
	}

	// 0.3.31: persist issue.lab_mode if the caller touched it. Same
	// pattern as CreateIssue: sqlc's UpdateIssue does not write the
	// column so we issue a follow-up UPDATE. Best-effort.
	if _, touched := rawFields["lab_mode"]; touched && req.LabMode != nil {
		if err := h.Queries.UpdateIssueLabMode(r.Context(), db.UpdateIssueLabModeParams{
			ID:          issue.ID,
			LabMode:     pgtype.Text{String: *req.LabMode, Valid: *req.LabMode != ""},
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("update issue: persist lab_mode failed",
				append(logger.RequestAttrs(r),
					"issue_id", id,
					"lab_mode", *req.LabMode,
					"error", err)...)
		} else {
			issue.LabMode = pgtype.Text{String: *req.LabMode, Valid: *req.LabMode != ""}
		}
	}

	if len(attachmentIDs) > 0 {
		h.linkAttachmentsByIssueIDs(r.Context(), issue.ID, issue.WorkspaceID, attachmentIDs)
	}

	// 0.3.34 lab auto-dispatch: when the caller flips lab_source onto
	// a value with a known leader agent (claude_science_lab → research,
	// pythia_oracle → pythia_runtime, …) the issue's assignee
	// must end up pointing at that leader. The auto-pickup path then
	// runs through enqueueAgentTask downstream of the WS update broadcast.
	//
	// 0.3.46 bug fix (P0#4): the original gate (`!issue.AssigneeType.Valid`)
	// only auto-assigned when the issue had no assignee yet. If the user
	// had previously assigned a non-leader agent (e.g. created the issue
	// without a lab, picked any agent from the AssigneePicker, then later
	// flipped lab_source onto claude_science_lab) the auto-assign was
	// skipped and the lab ended up running on the wrong agent — the lab
	// runner resolves its leader from `issue.assignee_id`, so a stale
	// assignee silently breaks the lab while the UI shows the lab badge.
	//
	// New contract: when the caller flips lab_source to a value with a
	// known leader AND the existing assignee does NOT already point at
	// that leader, rewrite the assignee to the leader. We leave the
	// assignee alone when it already matches (preserves the common path
	// where AssigneePicker fired first and chose the lab leader
	// intentionally). Mythos enhancer-mode is not auto-overwritten — the
	// runner expects the user-picked target assignee, and the leader
	// helper returns ("", false) for mythos_swarm so this branch is a
	// no-op for that lab.
	labAutoAssigned := false
	if _, touchedLabSource := rawFields["lab_source"]; touchedLabSource &&
		req.LabSource != nil && *req.LabSource != "" {
		if h.shouldRewriteAssigneeForLabLeader(r.Context(), &issue, *req.LabSource) {
			h.assignDefaultLabAgentOnUpdate(r.Context(), &issue, *req.LabSource)
			// The auto-assign mutates issue.AssigneeType/AssigneeID in place.
			// If it stuck, treat this as an assignee change so WillEnqueueRun
			// dispatches the lab leader (RunSourceAssign). Without this the
			// PATCH that only flips lab_source carries no assignee_* field, so
			// the assigneeChanged calc below stays false and the research run
			// never starts (MUL: "selecting the lab must start the work").
			labAutoAssigned = issue.AssigneeType.Valid
		}
	}

	prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
	resp := issueToResponse(issue, prefix)
	slog.Info("issue updated", append(logger.RequestAttrs(r), "issue_id", id, "workspace_id", workspaceID)...)

	// 0.5.84 P0 #3: bulk-touch the causal graph for this issue
	// (every active node whose primary issue_id matches + every
	// 1-hop graph neighbour). Without this the 30-day stale TTL
	// flips volatile action/outcome/decision nodes to
	// status='stale' and they vanish from the active-only UI
	// filter, silently breaking chains the user can still see in
	// the issue's recent history. nil-safe / flag-gated inside the
	// recorder.
	if h.CausalRecorder != nil {
		h.CausalRecorder.RefreshForIssue(r.Context(), issue.ID)
	}

	h.fillStatusCategory(r.Context(), issue.WorkspaceID, &resp)
	assigneeChanged := (req.AssigneeType != nil || req.AssigneeID != nil) &&
		(prevIssue.AssigneeType.String != issue.AssigneeType.String || uuidToString(prevIssue.AssigneeID) != uuidToString(issue.AssigneeID))
	// Lab auto-assign happens without an explicit assignee_* field in the
	// request, so fold it into assigneeChanged to arm the dispatch path.
	if labAutoAssigned {
		assigneeChanged = true
	}
	statusChanged := req.Status != nil && prevIssue.Status != issue.Status
	priorityChanged := req.Priority != nil && prevIssue.Priority != issue.Priority
	// project_changed gates the client's per-project issue-list refetch the way
	// status/assignee flags gate theirs. Without it the client must diff
	// project_id against its own cache, which breaks once an optimistic local
	// move has overwritten the cached value (MUL-3669 / #4548).
	projectChanged := req.ProjectID != nil && uuidToString(prevIssue.ProjectID) != uuidToString(issue.ProjectID)
	descriptionChanged := req.Description != nil && textToPtr(prevIssue.Description) != resp.Description
	titleChanged := req.Title != nil && prevIssue.Title != issue.Title
	prevStartDate := dateToPtr(prevIssue.StartDate)
	startDateChanged := prevStartDate != resp.StartDate && (prevStartDate == nil) != (resp.StartDate == nil) ||
		(prevStartDate != nil && resp.StartDate != nil && *prevStartDate != *resp.StartDate)
	prevDueDate := dateToPtr(prevIssue.DueDate)
	dueDateChanged := prevDueDate != resp.DueDate && (prevDueDate == nil) != (resp.DueDate == nil) ||
		(prevDueDate != nil && resp.DueDate != nil && *prevDueDate != *resp.DueDate)

	// Determine actor identity: agent (via X-Agent-ID header) or member.
	actorType, actorID := h.resolveActor(r, userID, workspaceID)

	h.publish(protocol.EventIssueUpdated, workspaceID, actorType, actorID, map[string]any{
		"issue":               resp,
		"assignee_changed":    assigneeChanged,
		"status_changed":      statusChanged,
		"priority_changed":    priorityChanged,
		"project_changed":     projectChanged,
		"start_date_changed":  startDateChanged,
		"due_date_changed":    dueDateChanged,
		"description_changed": descriptionChanged,
		"title_changed":       titleChanged,
		"prev_title":          prevIssue.Title,
		"prev_assignee_type":  textToPtr(prevIssue.AssigneeType),
		"prev_assignee_id":    uuidToPtr(prevIssue.AssigneeID),
		"prev_status":         prevIssue.Status,
		"prev_priority":       prevIssue.Priority,
		"prev_start_date":     prevStartDate,
		"prev_due_date":       prevDueDate,
		"prev_description":    textToPtr(prevIssue.Description),
		"creator_type":        prevIssue.CreatorType,
		"creator_id":          uuidToString(prevIssue.CreatorID),
	})

	// Reconcile the task queue. Whether this write starts an agent run — and
	// for whom (agent assignee or squad leader) — is decided by the single
	// WillEnqueueRun predicate, shared verbatim with the preview endpoint so
	// the two never drift (MUL-3375). Cancellation on reassignment is a
	// separate side effect and always runs, independent of the run decision.
	if assigneeChanged {
		h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
	}
	if trigger, ok := h.IssueService.WillEnqueueRun(r.Context(),
		service.IssueTriggerInput{
			Issue:           issue,
			PrevStatus:      prevIssue.Status,
			AssigneeChanged: assigneeChanged,
			StatusChanged:   statusChanged,
		},
		h.issueTriggerWriteProbe(r, actorType, issue),
	); ok && !req.SuppressRun {
		h.dispatchIssueRun(r.Context(), issue, trigger, actorType, actorID, req.HandoffNote)
	}

	// Cancel active tasks when the issue is cancelled by a user.
	// This is distinct from agent-managed status transitions — cancellation
	// is a user-initiated terminal action that should stop execution.
	if statusChanged && issue.Status == "cancelled" {
		h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
	}

	// Platform-driven parent notification: when this issue transitions into
	// `done` and has a parent, post a top-level system comment on the parent
	// (MUL-2538 — replaces the agent-prompt rule that caused self-mention
	// loops in PR #2918). The helper guards on transition + parent state and
	// fails best-effort.
	// 0.5.22 MUL-4063: actor identity is no longer threaded through.
	if statusChanged {
		h.notifyParentOfChildDone(r.Context(), prevIssue, issue)
	}

	writeJSON(w, http.StatusOK, resp)
}

// validateAssigneePair verifies the (assignee_type, assignee_id) pair refers
// to an existing entity in the workspace. For agent assignees it also rejects
// archived agents and runs the private-agent gate via canAccessPrivateAgent
// — assigning an issue is a task-producing surface, so it must use the same
// assignDefaultLabAgentOnUpdate mirrors IssueService.assignDefaultLabAgent
// for the Update path. Called when a PATCH flips lab_source onto a
// value with a known leader agent (claude_science_lab → research,
// pythia_oracle → pythia_runtime, …) and the existing
// assignee does NOT already point at the leader (see
// shouldRewriteAssigneeForLabLeader, 0.3.46 P0#4). We do not have
// an IssueService handle here, so the helper goes directly through
// h.Queries. Errors are logged and swallowed — a stale experimental
// agent row must not 500 an issue update.
func (h *Handler) assignDefaultLabAgentOnUpdate(ctx context.Context, issue *db.Issue, labSource string) {
	leaderName, ok := h.resolveLabLeader(ctx, labSource)
	if !ok {
		return
	}
	leader, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: issue.WorkspaceID,
		Name:        leaderName,
	})
	if err != nil {
		slog.Info("assignDefaultLabAgentOnUpdate: leader agent not installed",
			"issue_id", util.UUIDToString(issue.ID),
			"lab_source", labSource,
			"expected_agent_name", leaderName,
			"error", err)
		return
	}
	if err := h.Queries.UpdateIssueAssignee(ctx, db.UpdateIssueAssigneeParams{
		ID:           issue.ID,
		AssigneeType: pgtype.Text{String: "agent", Valid: true},
		AssigneeID:   leader.ID,
		WorkspaceID:  issue.WorkspaceID,
	}); err != nil {
		slog.Warn("assignDefaultLabAgentOnUpdate: write failed",
			"issue_id", util.UUIDToString(issue.ID),
			"agent_name", leaderName,
			"error", err)
		return
	}
	issue.AssigneeType = pgtype.Text{String: "agent", Valid: true}
	issue.AssigneeID = leader.ID
	slog.Info("assignDefaultLabAgentOnUpdate: assigned default",
		"issue_id", util.UUIDToString(issue.ID),
		"lab_source", labSource,
		"agent_name", leaderName)
}

// defaultLabLeaderForKey is the handler-side mirror of
// service.IssueService.defaultLeaderAgentForLab. Kept in sync so the
// handler doesn't pull the IssueService just for this lookup.
func defaultLabLeaderForKey(labSource string) (string, bool) {
	switch labSource {
	case "claude_science_lab":
		return "research", true
	case "pythia_oracle":
		// 0.3.54: Pythia oracle provisions a pythia_runtime leader
		// agent bound to the multica-pythia skill. The Python
		// service itself is started by the desktop manager-factory,
		// not the server; the leader agent row is what the daemon
		// auto-assigns task rows to.
		return "pythia_runtime", true
	case "code_canvas":
		// 0.3.54: code_canvas provisions a code_canvas_worker
		// leader that the daemon drives against the bundled
		// run.sh stub subprocess. Real subprocess lifecycle is
		// owned by manager-factory.ts.
		return "code_canvas_worker", true
	case "agent_creation_studio":
		// 0.5.5: the studio leader agent is boot-provisioned by
		// `boot_provision_product_labs.go` and surfaces directly in
		// the AssigneePicker. 0.5.6: the flag literal is removed
		// from the catalog, but the case stays so any legacy
		// `lab_source='agent_creation_studio'` issue still resolves
		// to the leader (the 0.3.46 P0#4 leader-rewrite contract).
		return AgentCreationExpertName, true
	case "semantica":
		// 0.5.22 Semantica × Multica Phase 2: leader for the
		// semantica lab. Install handler provisions the agent row
		// bound to the multica-semantica-decision-advisor skill.
		// String MUST match defaultLeaderAgentForLab in
		// service/issue.go and upsertSemanticaDecisionAdvisorAgent
		// in install_semantica.go.
		return "semantica_decision_advisor", true
	case "mythos_swarm":
		// Mythos owns the roster via its own runner; auto-assign is
		// intentionally suppressed (the sole-mutex gate above keeps
		// AssigneeType empty).
		return "", false
	case "timesfm":
		// 0.5.86: timesfm_oracle was provisioned by InstallTimesfm at
		// flag-enable since 0.5.82 but was missing from both leader
		// tables — lab_source=timesfm binds left the issue unassigned
		// and the 0.3.46 leader-rewrite contract silently no-op'd.
		// The row lands together with the 0.5.86 assignee-lock
		// (InteractionModelAssignee) and the forecast-report writeback.
		// String MUST match defaultLeaderAgentForLab in
		// service/issue.go and the timesfm install handler.
		return "timesfm_oracle", true
	default:
		return "", false
	}
}

// resolveLabLeader is the handler-side leader resolver used by the
// Update path. Built-in labs resolve through defaultLabLeaderForKey;
// user plugins ("user_<slug>" flag keys) resolve their leader from the
// stored manifest's interaction-model contract — the 0.5.88 top-level
// leader_agent field first, the legacy capabilities.leader block as
// fallback (experimental.UserPluginLeaderAgent) — mirroring
// IssueService.resolveLabLeader on the create path. Missing plugin or
// manifest without a leader falls through to ("", false).
func (h *Handler) resolveLabLeader(ctx context.Context, labSource string) (string, bool) {
	if name, ok := defaultLabLeaderForKey(labSource); ok {
		return name, true
	}
	if experimental.IsUserPluginKey(labSource) {
		plugin, err := h.Queries.GetUserPluginByFlagKey(ctx, labSource)
		if err != nil {
			return "", false
		}
		return experimental.UserPluginLeaderAgent(plugin.ManifestJson)
	}
	return "", false
}

// assigneeLabLockError — 0.5.86 assignee-lock hard gate (独立工作型).
// For labs classified InteractionModelAssignee, the bound issue's
// assignee slot belongs to the lab's leader agent. Returns a non-empty
// 400 message when the (labSource, assigneeType, assigneeID)
// combination violates the lock; "" when allowed. An EMPTY assignee is
// always allowed — the 0.3.46 leader-rewrite fills it (or mythos'
// sole-mode roster owns the work). Callers skip the check entirely in
// mythos enhancer mode (the target assignee is the point of enhancer).
//
// Semantics per lab family:
//   - leader resolvable (claude_science_lab/research,
//     pythia_oracle/pythia_runtime, timesfm/timesfm_oracle,
//     semantica/semantica_decision_advisor, swarm_topology/
//     swarm_coordinator): only that exact agent row (assignee_type=
//     "agent") passes; anything else → 400 naming the leader.
//   - no leader (mythos_swarm roster): ANY manual assignee → 400,
//     preserving the 0.3.33 sole-mutex behavior through the same path.
//   - leader row missing (install never ran): 400 with an
//     install-first hint rather than silently allowing a human.
//   - user plugins ("user_<slug>", 0.5.88 P4): the interaction model
//     and leader resolve from the stored manifest contract
//     (interaction_model + leader_agent, legacy capabilities.leader
//     fallback) instead of the built-in tables — same semantics as the
//     resolvable-leader built-ins once the manifest declares
//     interaction_model="assignee".
func (h *Handler) assigneeLabLockError(ctx context.Context, workspaceID pgtype.UUID, labSource, assigneeType, assigneeID string) string {
	if labSource == "" {
		return ""
	}
	// 0.5.88 P4: user plugins carry the interaction-model contract in
	// their stored manifest — resolve it from the user_plugin row (the
	// single source of truth for external plugins, registered at boot /
	// CRUD into the dynamic flag space) instead of the built-in catalog
	// tables. A missing/soft-deleted row resolves to auxiliary (no
	// lock): a gone plugin must never keep locking an issue.
	if experimental.IsUserPluginKey(labSource) {
		plugin, err := h.Queries.GetUserPluginByFlagKey(ctx, labSource)
		if err != nil || experimental.UserPluginInteractionModel(plugin.ManifestJson) != experimental.InteractionModelAssignee {
			return ""
		}
	} else if !experimental.IsAssigneeModelLab(labSource) {
		return ""
	}
	if assigneeType == "" && assigneeID == "" {
		return "" // empty is fine — leader-rewrite / roster fills it
	}
	leaderName, ok := h.resolveLabLeader(ctx, labSource)
	if !ok || leaderName == "" {
		return "lab_source=" + labSource + " requires the lab to own the assignee (its agent roster runs the issue); clear the manual assignee or remove the lab"
	}
	leaderHint := "lab_source=" + labSource + " locks the assignee to the lab agent (" + leaderName + "); pick the lab agent or clear the manual assignee"
	if assigneeType != "agent" || assigneeID == "" {
		return leaderHint
	}
	leader, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: workspaceID,
		Name:        leaderName,
	})
	if err != nil {
		return "lab_source=" + labSource + " locks the assignee to its lab agent (" + leaderName + "), but that agent is not installed yet; enable/install the lab first"
	}
	if uuidToString(leader.ID) != assigneeID {
		return leaderHint
	}
	return ""
}

// shouldRewriteAssigneeForLabLeader — 0.3.46 (P0#4) companion helper.
// Returns true when the caller is flipping lab_source onto a value
// with a known leader AND the current issue.assignee does NOT already
// point at that leader. The leader lookup is best-effort: if the
// leader agent row is missing (install never ran) we cannot tell
// whether the existing assignee matches, so we conservatively skip
// the rewrite to avoid clobbering a deliberate user choice with
// "no leader installed". assignDefaultLabAgentOnUpdate logs the
// miss and proceeds; this gate just keeps the auto-rewrite honest.
func (h *Handler) shouldRewriteAssigneeForLabLeader(ctx context.Context, issue *db.Issue, labSource string) bool {
	leaderName, ok := h.resolveLabLeader(ctx, labSource)
	if !ok {
		return false
	}
	// Mythos / other no-leader labs return ("", false) — caller handles.
	if leaderName == "" {
		return false
	}
	// No assignee yet → rewrite needed (assignDefaultLabAgentOnUpdate
	// will look up + write).
	if !issue.AssigneeType.Valid || !issue.AssigneeID.Valid {
		return true
	}
	// Already pointing at an agent — only rewrite if it is NOT the
	// expected leader. Same-leader path is a no-op.
	if issue.AssigneeType.String != "agent" {
		return true
	}
	leader, err := h.Queries.GetAgentByWorkspaceAndName(ctx, db.GetAgentByWorkspaceAndNameParams{
		WorkspaceID: issue.WorkspaceID,
		Name:        leaderName,
	})
	if err != nil {
		// Leader not installed — preserve current assignee rather than
		// guessing. Caller logs the install miss separately.
		return false
	}
	return leader.ID != issue.AssigneeID
}

// predicate as chat / @-mention / history. Agent callers (X-Agent-ID) bypass
// the gate so A2A flows can still hand work off to private agents.
//
// Returns (statusCode, errorMessage). statusCode == 0 means the pair is valid;
// callers should treat any non-zero status as a rejection and surface it back
// to the client.
func (h *Handler) validateAssigneePair(ctx context.Context, r *http.Request, workspaceID string, assigneeType pgtype.Text, assigneeID pgtype.UUID) (int, string) {
	// Both unset → unassigned issue, valid.
	if !assigneeType.Valid && !assigneeID.Valid {
		return 0, ""
	}
	// Exactly one of type/id provided → callers must always pair them.
	if assigneeType.Valid != assigneeID.Valid {
		return http.StatusBadRequest, "assignee_type and assignee_id must be provided together"
	}
	wsUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return http.StatusBadRequest, "invalid workspace_id"
	}
	switch assigneeType.String {
	case "member":
		if _, err := h.Queries.GetMemberByUserAndWorkspace(ctx, db.GetMemberByUserAndWorkspaceParams{
			UserID:      assigneeID,
			WorkspaceID: wsUUID,
		}); err != nil {
			return http.StatusBadRequest, "assignee_id does not refer to a member of this workspace"
		}
		return 0, ""
	case "agent":
		agent, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
			ID:          assigneeID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			return http.StatusBadRequest, "assignee_id does not refer to an agent of this workspace"
		}
		if agent.ArchivedAt.Valid {
			return http.StatusBadRequest, "cannot assign to archived agent"
		}
		actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
		if !h.canAccessPrivateAgent(ctx, agent, actorType, actorID, workspaceID) {
			return http.StatusForbidden, "cannot assign to private agent"
		}
		return 0, ""
	case "squad":
		squad, err := h.Queries.GetSquadInWorkspace(ctx, db.GetSquadInWorkspaceParams{
			ID:          assigneeID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			return http.StatusBadRequest, "assignee_id does not refer to a squad in this workspace"
		}
		if squad.ArchivedAt.Valid {
			return http.StatusBadRequest, "cannot assign to an archived squad"
		}
		leader, err := h.Queries.GetAgent(ctx, squad.LeaderID)
		if err != nil || leader.ArchivedAt.Valid {
			return http.StatusBadRequest, "squad leader is archived; cannot assign to this squad"
		}
		actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
		if !h.canAccessPrivateAgent(ctx, leader, actorType, actorID, workspaceID) {
			return http.StatusForbidden, "cannot assign to squad with private leader"
		}
		// 0.3.27 B7: squad_creator_scope at dispatch. The squad's
		// creator_id (set at install time) is the only workspace
		// member authorised to assign issues to this squad. Other
		// members can still @-mention the leader or comment on its
		// issues, but the squad's own task-producing surface stays
		// restricted to the creator — same rule as
		// `canMutateSquad` (squad metadata edits). Without this gate
		// any workspace member could dispatch squad tasks by
		// reassigning a single issue, defeating the original 0.3.5
		// fix scope.
		if actorType == "member" && squad.CreatorID.Valid && actorID != "" {
			actorUUID, perr := util.ParseUUID(actorID)
			if perr == nil && squad.CreatorID != actorUUID {
				return http.StatusForbidden, "squad creator scope: only the squad's creator may dispatch tasks to it"
			}
		}
		return 0, ""
	default:
		return http.StatusBadRequest, "assignee_type must be 'member', 'agent', or 'squad'"
	}
}

// shouldEnqueueAgentTask returns true when an issue creation or assignment
// should trigger the assigned agent. Backlog issues are skipped — backlog
// acts as a parking lot where issues can be pre-assigned without immediately
// triggering execution. Moving out of backlog is handled separately in
// UpdateIssue.
func (h *Handler) shouldEnqueueAgentTask(ctx context.Context, issue db.Issue) bool {
	// A custom status in the backlog category parks like Backlog. (MUL-6243)
	if issuestatus.Effective(ctx, h.Queries, issue.WorkspaceID, issue.Status) == "backlog" {
		return false
	}
	return h.isAgentAssigneeReady(ctx, issue)
}

// shouldEnqueueOnComment returns true if a member comment on this issue should
// trigger the assigned agent. Fires for any status — comments are
// conversational and can happen at any stage, including after completion
// (e.g. follow-up questions on a done issue).
//
// Mirrors the private-agent gate that computeMentionedAgentCommentTriggers applies on the
// @mention path: once an owner/admin assigns a private agent to an issue, the
// agent's UUID is "welded" onto the issue and remains visible to every member
// who can view it. Without this check any of those members could dispatch a new
// task to the private agent simply by commenting (#3300).
func (h *Handler) shouldEnqueueOnComment(ctx context.Context, issue db.Issue, actorType, actorID string, opts commentTriggerComputeOptions) bool {
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return false
	}
	agent, err := h.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid {
		return false
	}
	if !h.canAccessPrivateAgent(ctx, agent, actorType, actorID, uuidToString(issue.WorkspaceID)) {
		return false
	}
	// Coalescing queue: allow enqueue when a task is running (so the agent
	// picks up new comments on the next cycle) but skip if this agent already
	// has a pending task (natural dedup for rapid-fire comments).
	hasPending, err := h.hasPendingTaskForIssueAndAgent(ctx, issue.ID, issue.AssigneeID, opts)
	if err != nil || hasPending {
		return false
	}
	return true
}

// isAgentRunningOnIssue reports whether the calling agent's current task
// (identified by X-Task-ID) is running for the exact issue being promoted.
// That is the only true self-loop on backlog→active: the agent flipping
// the same issue its own task is executing for would immediately re-enqueue
// itself, complete the run, flip again, and so on.
//
// Same-agent cross-issue handoff (Agent A finishing a task on issue I1 then
// promoting issue I2 — even when I2 is also assigned to A) is NOT a loop
// and must fire; that is the documented serial sub-task chain. Member
// actors never match.
//
// X-Task-ID is guaranteed to be present and consistent when actorType is
// "agent": resolveActor demotes the actor to "member" otherwise (handler.go
// resolveActor). We still recheck defensively — a future caller could pass
// agent identity through a different path.
func (h *Handler) isAgentRunningOnIssue(r *http.Request, actorType string, issue db.Issue) bool {
	if actorType != "agent" {
		return false
	}
	taskIDStr := r.Header.Get("X-Task-ID")
	if taskIDStr == "" {
		return false
	}
	taskUUID, err := util.ParseUUID(taskIDStr)
	if err != nil {
		return false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		return false
	}
	if !task.IssueID.Valid {
		return false
	}
	return uuidToString(task.IssueID) == uuidToString(issue.ID)
}

// isAgentAssigneeReady checks if an issue is assigned to an active agent
// with a valid runtime.
func (h *Handler) isAgentAssigneeReady(ctx context.Context, issue db.Issue) bool {
	if !issue.AssigneeType.Valid || issue.AssigneeType.String != "agent" || !issue.AssigneeID.Valid {
		return false
	}

	agent, err := h.Queries.GetAgent(ctx, issue.AssigneeID)
	if err != nil || !agent.RuntimeID.Valid || agent.ArchivedAt.Valid {
		return false
	}

	return true
}

func (h *Handler) DeleteIssue(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, id)
	if !ok {
		return
	}

	h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
	// Fail any linked autopilot runs before delete (ON DELETE SET NULL clears issue_id).
	h.Queries.FailAutopilotRunsByIssue(r.Context(), issue.ID)

	// Collect all attachment URLs (issue-level + comment-level) before CASCADE delete.
	attachmentURLs, _ := h.Queries.ListAttachmentURLsByIssueOrComments(r.Context(), issue.ID)

	err := h.Queries.DeleteIssue(r.Context(), db.DeleteIssueParams{
		ID:          issue.ID,
		WorkspaceID: issue.WorkspaceID,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete issue")
		return
	}

	h.deleteS3Objects(r.Context(), attachmentURLs)
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, uuidToString(issue.WorkspaceID))
	// Always emit the resolved UUID — frontend caches key by UUID, so an
	// identifier-style payload ("MUL-123") would leave stale entries on
	// other clients after an identifier-path delete.
	resolvedID := uuidToString(issue.ID)
	h.publish(protocol.EventIssueDeleted, uuidToString(issue.WorkspaceID), actorType, actorID, map[string]any{"issue_id": resolvedID})
	slog.Info("issue deleted", append(logger.RequestAttrs(r), "issue_id", resolvedID, "workspace_id", uuidToString(issue.WorkspaceID))...)
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Batch operations
// ---------------------------------------------------------------------------

type BatchUpdateIssuesRequest struct {
	IssueIDs []string           `json:"issue_ids"`
	Updates  UpdateIssueRequest `json:"updates"`
}

func (h *Handler) BatchUpdateIssues(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req BatchUpdateIssuesRequest
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.IssueIDs) == 0 {
		writeError(w, http.StatusBadRequest, "issue_ids is required")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	// Detect which fields in "updates" were explicitly set (including null).
	var rawTop map[string]json.RawMessage
	json.Unmarshal(bodyBytes, &rawTop)
	var rawUpdates map[string]json.RawMessage
	if raw, exists := rawTop["updates"]; exists {
		json.Unmarshal(raw, &rawUpdates)
	}

	// Short-circuit when no mutation field is present in `updates`. Without
	// this, the loop below runs N no-op UPDATEs (every if-guard skips, every
	// COALESCE preserves the existing value) and reports `{"updated": N}` —
	// the response cheerfully claims success while nothing changed. Most
	// real-world cases that hit this path are caller mistakes (status placed
	// at the top level, "update" misspelled as singular). Telling the truth
	// here — `{"updated": 0}` — keeps the wire shape stable while making the
	// count match reality. See multica-ai/multica#1660.
	hasMutation := req.Updates.Title != nil ||
		req.Updates.Description != nil ||
		req.Updates.Status != nil ||
		req.Updates.Priority != nil ||
		req.Updates.Position != nil
	if !hasMutation {
		for _, k := range []string{"assignee_type", "assignee_id", "start_date", "due_date", "parent_issue_id", "project_id", "stage", "lab_source"} {
			if _, ok := rawUpdates[k]; ok {
				hasMutation = true
				break
			}
		}
	}
	if !hasMutation {
		writeJSON(w, http.StatusOK, map[string]any{"updated": 0})
		return
	}
	if req.Updates.Priority != nil {
		if !validateIssueEnum(w, "priority", *req.Updates.Priority, validIssuePriorities) {
			return
		}
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	// Status is validated against this workspace's catalog, so it has to wait
	// for wsUUID above. One check for the whole batch — every issue in it
	// shares the workspace — and a rejection rather than a silent skip, so a
	// bad status cannot report `{"updated": N}`. (MUL-6243)
	batchStatusKey := ""
	if req.Updates.Status != nil {
		batchStatusKey, _, ok = h.resolveIssueStatusKeyKind(w, r, wsUUID, *req.Updates.Status)
		if !ok {
			return
		}
	}
	// The batch shares one project_id, so it is checked once here rather than
	// per issue, and rejected instead of skipped like the per-item guards in
	// the loop: a foreign project invalidates the whole request.
	batchProjectID := pgtype.UUID{Valid: false}
	if _, ok := rawUpdates["project_id"]; ok && req.Updates.ProjectID != nil {
		projectUUID, ok := parseUUIDOrBadRequest(w, *req.Updates.ProjectID, "project_id")
		if !ok {
			return
		}
		if _, err := h.Queries.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{
			ID:          projectUUID,
			WorkspaceID: wsUUID,
		}); err != nil {
			if !isNotFound(err) {
				slog.Error("batch update issues: validate project scope",
					append(logger.RequestAttrs(r), "project_id", uuidToString(projectUUID), "error", err)...)
				writeError(w, http.StatusInternalServerError, "failed to validate project")
				return
			}
			writeError(w, http.StatusBadRequest, "project not found in this workspace")
			return
		}
		batchProjectID = projectUUID
	}

	updated := 0

	for _, issueID := range req.IssueIDs {
		issueUUID, err := util.ParseUUID(issueID)
		if err != nil {
			continue
		}
		prevIssue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			continue
		}

		params := db.UpdateIssueParams{
			ID:            prevIssue.ID,
			AssigneeType:  prevIssue.AssigneeType,
			AssigneeID:    prevIssue.AssigneeID,
			StartDate:     prevIssue.StartDate,
			DueDate:       prevIssue.DueDate,
			ParentIssueID: prevIssue.ParentIssueID,
			ProjectID:     prevIssue.ProjectID,
			Stage:         prevIssue.Stage,
		}

		// 0.3.31: lab ↔ assignee mutex (batch variant). Mirrors the
		// UpdateIssue gate above, including the 0.3.33 narrowing:
		// only `mythos_swarm` (sole mode — enhancer REQUIRES an
		// assignee) and `swarm_topology` (0.5.21, no modes) still
		// reserve the roster. Other labs
		// auto-assign their own leader agent since 0.3.47, so the
		// old "any lab + any assignee → skip" rule silently dropped
		// every batch status/priority move against lab-tagged issues
		// (the auto-assigned leader tripped the gate). The gate
		// trigger uses prevIssue values for fields the batch does
		// not touch, so a batch that PATCHes only the assignee
		// against a pre-labbed mythos issue is still caught. Batch
		// cannot change lab_mode, so the persisted value decides
		// enhancer-ness. Per the batch endpoint's per-issue
		// skip-on-failure contract (see parent_issue_id /
		// project_id / stage branches below), we `continue` on a
		// mutex violation rather than 400 the whole batch — a
		// single mis-tagged issue in a 50-issue move should not
		// fail the other 49.
		{
			var postLab string
			if _, ok := rawUpdates["lab_source"]; ok && req.Updates.LabSource != nil {
				postLab = *req.Updates.LabSource
			} else {
				postLab = prevIssue.LabSource.String
			}
			enhancerMode := prevIssue.LabMode.String == "enhancer"
			var postAssigneeType string
			var postAssigneeID string
			if _, ok := rawUpdates["assignee_type"]; ok && req.Updates.AssigneeType != nil {
				postAssigneeType = *req.Updates.AssigneeType
			} else {
				postAssigneeType = prevIssue.AssigneeType.String
			}
			if _, ok := rawUpdates["assignee_id"]; ok && req.Updates.AssigneeID != nil {
				postAssigneeID = *req.Updates.AssigneeID
			} else {
				postAssigneeID = uuidToString(prevIssue.AssigneeID)
			}
			hasAssignee := postAssigneeType != "" || postAssigneeID != ""
			switch {
			case postLab == "mythos_swarm" && !enhancerMode && hasAssignee:
				slog.Warn("batch update rejected: lab/assignee mutex",
					"issue_id", issueID, "post_lab", postLab)
				continue
			case postLab == "mythos_swarm" && enhancerMode && !hasAssignee:
				// Enhancer needs its user-picked target assignee; a
				// batch clearing it would strand the supervise loop.
				slog.Warn("batch update rejected: enhancer requires assignee",
					"issue_id", issueID, "post_lab", postLab)
				continue
			case postLab == "swarm_topology" && hasAssignee:
				// 0.5.60 (audit P0-1): the 0.5.21 swarm mutex extension
				// landed in CreateIssue/UpdateIssue/UI but was never
				// propagated here — a batch PATCH flipping lab_source to
				// swarm_topology onto an assigned issue (or assigning a
				// swarm issue) silently persisted, bypassing the 400 the
				// single-issue paths return. Swarm has no enhancer mode,
				// so a single case suffices (lab_mode is ignored).
				slog.Warn("batch update rejected: lab/assignee mutex",
					"issue_id", issueID, "post_lab", postLab)
				continue
			}
		}

		// 0.3.31: BatchUpdateIssues used to silently drop
		// `lab_source` because no branch here read it. The
		// `UpdateIssueRequest` struct (carried in
		// `req.Updates.LabSource`) already declared the field,
		// so a client sending `{"updates": {"lab_source": "x"}}`
		// saw the field accepted at parse time and then lost in
		// the SQL update. Wire it through the same way
		// UpdateIssue does, including the catalog
		// (IsKnownKey) gate, so a typo or a forbidden key is
		// rejected (silently per-issue) instead of persisting
		// whatever string the client sent.
		if _, ok := rawUpdates["lab_source"]; ok {
			if req.Updates.LabSource != nil {
				if *req.Updates.LabSource != "" && !experimental.IsKnownKey(*req.Updates.LabSource) {
					slog.Warn("batch update rejected: lab_source not in catalog",
						"issue_id", issueID, "lab_source", *req.Updates.LabSource)
					continue
				}
				// 0.5.105 (audit H3): frozen labs reject NEW bindings;
				// batch honours the continue-per-issue contract.
				if msg := frozenLabSourceBindError(*req.Updates.LabSource); msg != "" {
					slog.Warn("batch update rejected: lab_source frozen",
						"issue_id", issueID, "lab_source", *req.Updates.LabSource)
					continue
				}
				params.LabSource = pgtype.Text{String: *req.Updates.LabSource, Valid: true}
			} else {
				params.LabSource = pgtype.Text{Valid: false}
			}
		}

		// 0.3.47 (P0#4 Batch parity): mirror UpdateIssue's
		// shouldRewriteAssigneeForLabLeader + assignDefaultLabAgentOnUpdate
		// path. Without this, a batch PATCH like
		// `{"updates": {"lab_source": "claude_science_lab"}}` against
		// N unassigned issues persists N lab-tagged issues with no
		// leader assignee — WillEnqueueRun never arms because
		// assigneeChanged stays false, so the research leader never
		// starts. CLAUDE.md (Active Contracts §2) explicitly binds
		// BatchUpdateIssues to the same helper. Since the mutex gate
		// was narrowed to mythos_swarm (0.3.33 parity), a batch may
		// legitimately carry lab_source AND assignee_* together for
		// other labs — an explicit assignee is a deliberate user
		// choice, so the auto-rewrite yields to it.
		_, batchTouchedType := rawUpdates["assignee_type"]
		_, batchTouchedID := rawUpdates["assignee_id"]
		labAutoRewrote := false
		if params.LabSource.Valid && params.LabSource.String != "" &&
			!batchTouchedType && !batchTouchedID {
			if h.shouldRewriteAssigneeForLabLeader(r.Context(), &prevIssue, params.LabSource.String) {
				// resolveLabLeader (NOT defaultLabLeaderForKey) so
				// user_<slug> plugins resolve their manifest leader
				// here too — the gate above already used it, so a
				// built-in-only lookup would pass the gate and then
				// silently skip the rewrite for every user plugin.
				leaderName, _ := h.resolveLabLeader(r.Context(), params.LabSource.String)
				leader, lookupErr := h.Queries.GetAgentByWorkspaceAndName(r.Context(), db.GetAgentByWorkspaceAndNameParams{
					WorkspaceID: prevIssue.WorkspaceID,
					Name:        leaderName,
				})
				if lookupErr != nil {
					slog.Info("BatchUpdateIssues: leader agent not installed, skipping auto-rewrite",
						"issue_id", issueID, "lab_source", params.LabSource.String,
						"expected_agent_name", leaderName, "error", lookupErr)
				} else {
					params.AssigneeType = pgtype.Text{String: "agent", Valid: true}
					params.AssigneeID = leader.ID
					labAutoRewrote = true
					slog.Info("BatchUpdateIssues: assigned default lab leader",
						"issue_id", issueID, "lab_source", params.LabSource.String,
						"agent_name", leaderName)
				}
			}
		}

		if req.Updates.Title != nil {
			params.Title = pgtype.Text{String: *req.Updates.Title, Valid: true}
		}
		if req.Updates.Description != nil {
			params.Description = pgtype.Text{String: *req.Updates.Description, Valid: true}
		}
		if req.Updates.Status != nil {
			params.Status = pgtype.Text{String: batchStatusKey, Valid: true}
		}
		if req.Updates.Priority != nil {
			params.Priority = pgtype.Text{String: *req.Updates.Priority, Valid: true}
		}
		if req.Updates.Position != nil {
			params.Position = pgtype.Float8{Float64: *req.Updates.Position, Valid: true}
		}
		if _, ok := rawUpdates["assignee_type"]; ok {
			if req.Updates.AssigneeType != nil {
				params.AssigneeType = pgtype.Text{String: *req.Updates.AssigneeType, Valid: true}
			} else {
				params.AssigneeType = pgtype.Text{Valid: false}
			}
		}
		if _, ok := rawUpdates["assignee_id"]; ok {
			if req.Updates.AssigneeID != nil {
				assigneeUUID, err := util.ParseUUID(*req.Updates.AssigneeID)
				if err != nil {
					continue
				}
				params.AssigneeID = assigneeUUID
			} else {
				params.AssigneeID = pgtype.UUID{Valid: false}
			}
		}
		if _, ok := rawUpdates["start_date"]; ok {
			if req.Updates.StartDate != nil && *req.Updates.StartDate != "" {
				d, err := util.ParseCalendarDate(*req.Updates.StartDate)
				if err != nil {
					continue
				}
				params.StartDate = d
			} else {
				params.StartDate = pgtype.Date{Valid: false}
			}
		}
		if _, ok := rawUpdates["due_date"]; ok {
			if req.Updates.DueDate != nil && *req.Updates.DueDate != "" {
				d, err := util.ParseCalendarDate(*req.Updates.DueDate)
				if err != nil {
					continue
				}
				params.DueDate = d
			} else {
				params.DueDate = pgtype.Date{Valid: false}
			}
		}

		if _, ok := rawUpdates["parent_issue_id"]; ok {
			if req.Updates.ParentIssueID != nil {
				newParentID, err := util.ParseUUID(*req.Updates.ParentIssueID)
				if err != nil {
					continue
				}
				// Cannot set self as parent.
				if newParentID == prevIssue.ID {
					continue
				}
				// Validate parent exists in the same workspace.
				if _, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
					ID:          newParentID,
					WorkspaceID: prevIssue.WorkspaceID,
				}); err != nil {
					continue
				}
				// Cycle detection: walk up from the new parent to ensure we don't reach this issue.
				cycleDetected := false
				cursor := newParentID
				for depth := 0; depth < 10; depth++ {
					ancestor, err := h.Queries.GetIssue(r.Context(), cursor)
					if err != nil || !ancestor.ParentIssueID.Valid {
						break
					}
					if ancestor.ParentIssueID == prevIssue.ID {
						cycleDetected = true
						break
					}
					cursor = ancestor.ParentIssueID
				}
				if cycleDetected {
					continue
				}
				params.ParentIssueID = newParentID
			} else {
				params.ParentIssueID = pgtype.UUID{Valid: false}
			}
		}
		if _, ok := rawUpdates["project_id"]; ok {
			// Resolved before the loop; an explicit null stays invalid and clears.
			params.ProjectID = batchProjectID
		}
		if _, ok := rawUpdates["stage"]; ok {
			if req.Updates.Stage != nil {
				if *req.Updates.Stage < 1 {
					continue
				}
				params.Stage = pgtype.Int4{Int32: *req.Updates.Stage, Valid: true}
			} else {
				params.Stage = pgtype.Int4{Valid: false} // explicit null = unstage
			}
		}

		// Validate the resulting assignee pair when this batch update touches
		// either assignee field. Skip the issue silently on failure.
		// (batchTouchedType/batchTouchedID were computed before the lab
		// leader auto-rewrite above.)
		if batchTouchedType || batchTouchedID {
			if status, _ := h.validateAssigneePair(r.Context(), r, workspaceID, params.AssigneeType, params.AssigneeID); status != 0 {
				continue
			}
		}

		var issue db.Issue
		err = h.runWithIssueStatusGuard(r.Context(), wsUUID, batchStatusKey, func(q *db.Queries) error {
			var innerErr error
			issue, innerErr = q.UpdateIssue(r.Context(), params)
			return innerErr
		})
		if err != nil {
			// The archive race is a property of the batch's shared target
			// status, not of one issue, so every remaining item would fail the
			// same way. Abort with 409 instead of reporting a partial update.
			if writeIssueStatusRaceError(w, err) {
				return
			}
			slog.Warn("batch update issue failed", "issue_id", issueID, "error", err)
			continue
		}

		// H4 (audit 2026-09-06): mirror UpdateIssue's RefreshForIssue call
		// (L3385-3387) — a batch PATCH flipping lab_source / status / title
		// on N issues used to silently bypass Active Contract #9, letting
		// the 30-day stale TTL flip volatile action/outcome/decision nodes
		// to status='stale'. nil-safe / flag-gated inside the recorder.
		if h.CausalRecorder != nil {
			h.CausalRecorder.RefreshForIssue(r.Context(), issue.ID)
		}

		prefix := h.getIssuePrefix(r.Context(), issue.WorkspaceID)
		resp := issueToResponse(issue, prefix)
		actorType, actorID := h.resolveActor(r, userID, workspaceID)

		// One Resolver for the whole batch — a per-issue filler would query the
		// catalog once per custom-status row. (MUL-6243)
		fillBatch := h.newStatusCategoryFiller(r.Context(), wsUUID)
		fillBatch(&resp)
		assigneeChanged := (req.Updates.AssigneeType != nil || req.Updates.AssigneeID != nil) &&
			(prevIssue.AssigneeType.String != issue.AssigneeType.String || uuidToString(prevIssue.AssigneeID) != uuidToString(issue.AssigneeID))
		// 0.3.47 (P0#4 Batch parity): batch rewrites the assignee above
		// without an explicit assignee_* field in the request, so fold
		// the lab auto-rewrite into assigneeChanged to arm the dispatch
		// path (mirrors UpdateIssue's labAutoAssigned fold at line 2842).
		if labAutoRewrote {
			assigneeChanged = true
		}
		statusChanged := req.Updates.Status != nil && prevIssue.Status != issue.Status
		priorityChanged := req.Updates.Priority != nil && prevIssue.Priority != issue.Priority
		projectChanged := req.Updates.ProjectID != nil && uuidToString(prevIssue.ProjectID) != uuidToString(issue.ProjectID)

		h.publish(protocol.EventIssueUpdated, workspaceID, actorType, actorID, map[string]any{
			"issue":            resp,
			"assignee_changed": assigneeChanged,
			"status_changed":   statusChanged,
			"priority_changed": priorityChanged,
			"project_changed":  projectChanged,
		})

		if assigneeChanged {
			h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
		}
		// Same single predicate as UpdateIssue — batch must not grow its own
		// copy of the enqueue rule (the historical source of four-entry-point
		// drift, MUL-3375). suppress_run applies batch-wide.
		if trigger, ok := h.IssueService.WillEnqueueRun(r.Context(),
			service.IssueTriggerInput{
				Issue:           issue,
				PrevStatus:      prevIssue.Status,
				AssigneeChanged: assigneeChanged,
				StatusChanged:   statusChanged,
			},
			h.issueTriggerWriteProbe(r, actorType, issue),
		); ok && !req.Updates.SuppressRun {
			h.dispatchIssueRun(r.Context(), issue, trigger, actorType, actorID, req.Updates.HandoffNote)
		}

		// Cancel active tasks when the issue is cancelled by a user.
		if statusChanged && issue.Status == "cancelled" {
			h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
		}

		// Platform-driven parent notification, mirrored from UpdateIssue
		// (MUL-2538). Best-effort; failure does not abort the batch.
		// 0.5.22 MUL-4063: actor identity is no longer threaded through.
		if statusChanged {
			h.notifyParentOfChildDone(r.Context(), prevIssue, issue)
		}

		updated++
	}

	slog.Info("batch update issues", append(logger.RequestAttrs(r), "count", updated)...)
	writeJSON(w, http.StatusOK, map[string]any{"updated": updated})
}

type BatchDeleteIssuesRequest struct {
	IssueIDs []string `json:"issue_ids"`
}

func (h *Handler) BatchDeleteIssues(w http.ResponseWriter, r *http.Request) {
	var req BatchDeleteIssuesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.IssueIDs) == 0 {
		writeError(w, http.StatusBadRequest, "issue_ids is required")
		return
	}

	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	deleted := 0
	for _, issueID := range req.IssueIDs {
		issueUUID, err := util.ParseUUID(issueID)
		if err != nil {
			continue
		}
		issue, err := h.Queries.GetIssueInWorkspace(r.Context(), db.GetIssueInWorkspaceParams{
			ID:          issueUUID,
			WorkspaceID: wsUUID,
		})
		if err != nil {
			continue
		}

		h.TaskService.CancelTasksForIssue(r.Context(), issue.ID)
		h.Queries.FailAutopilotRunsByIssue(r.Context(), issue.ID)

		// Collect attachment URLs before CASCADE delete to clean up S3 objects.
		attachmentURLs, _ := h.Queries.ListAttachmentURLsByIssueOrComments(r.Context(), issue.ID)

		if err := h.Queries.DeleteIssue(r.Context(), db.DeleteIssueParams{
			ID:          issue.ID,
			WorkspaceID: issue.WorkspaceID,
		}); err != nil {
			slog.Warn("batch delete issue failed", "issue_id", issueID, "error", err)
			continue
		}

		h.deleteS3Objects(r.Context(), attachmentURLs)

		// Always emit the resolved UUID — frontend caches key by UUID.
		actorType, actorID := h.resolveActor(r, userID, workspaceID)
		h.publish(protocol.EventIssueDeleted, workspaceID, actorType, actorID, map[string]any{"issue_id": uuidToString(issue.ID)})
		deleted++
	}

	slog.Info("batch delete issues", append(logger.RequestAttrs(r), "count", deleted)...)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}
