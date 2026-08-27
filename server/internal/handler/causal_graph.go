// Package handler — causal_graph.go (0.5.83 WL3)
//
// Gated REST surface for the issue causal graph (route group wrapped
// in RequireExperimentalFlag("causal_graph") in router.go — off-flag
// the routes physically vanish, uniform 404 per experimental_guard.go,
// identical to the timesfm/pythia mounts).
//
// Surface (wire CONTRACT — the client zod schemas in
// packages/core/api/causal_graph.ts and .omc/wl3-server-contract.md
// pin these shapes verbatim; do not rename fields):
//
//	GET    /api/issues/{issueID}/dependencies
//	POST   /api/issues/{issueID}/dependencies            {target_issue_id, type?, evidence_comment_id?}
//	DELETE /api/issues/{issueID}/dependencies/{dependencyID}
//	GET    /api/causal-graph/nodes?workspace_id=&issue_id=&type=&limit=&offset=
//	POST   /api/causal-graph/nodes                       {label, type, issue_id?, description?, metadata?, lab_source?, lab_run_id?}
//	GET    /api/causal-graph/edges?workspace_id=&issue_id=&type=&status=&min_confidence=&limit=&offset=
//	POST   /api/causal-graph/edges                       {from_node_id, to_node_id, type, confidence?, weight?, metadata?}
//	DELETE /api/causal-graph/edges/{edgeID}
//	GET    /api/causal-graph/subgraph?issue_id=&depth=   → {nodes, edges, depth}
//	GET    /api/causal-graph/path?from=&to=              → {nodes, edges} | 404
//	POST   /api/causal-graph/edges/{edgeID}/confirm      suggested → active (Tier D gate)
//	POST   /api/causal-graph/edges/{edgeID}/reject       suggested → deleted
//
// Law notes:
//
//   - Issue-scoped params resolve through loadIssueForUser
//     (identifier-or-UUID + workspace membership, same loader the rest
//     of the issue surface uses). Pure UUID inputs go through
//     parseUUIDOrBadRequest.
//   - The typed CHECK sets (node: decision|action|outcome|assumption|
//     evidence|constraint; edge: causes|supports|contradicts|
//     depends_on|enables|blocks) are validated here for a clean 400
//     before the DB CHECK turns them into a 500.
//   - Workspace scoping on the causal-graph endpoints follows the
//     mythos/code-canvas handler trust shape (?workspace_id= parsed
//     and every query filtered by it) — the desktop is the only
//     client and the flag gate is the outer fence.
package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	dbpkg "github.com/multica-ai/multica/server/pkg/db/generated"
)

// Verbatim CONTRACT sets (migrations 277/278 CHECKs). Any drift here
// must land in the same commit as the migration + client schemas.
var (
	causalNodeTypes = map[string]bool{
		"decision": true, "action": true, "outcome": true,
		"assumption": true, "evidence": true, "constraint": true,
	}
	causalEdgeTypes = map[string]bool{
		"causes": true, "supports": true, "contradicts": true,
		"depends_on": true, "enables": true, "blocks": true,
	}
	issueDependencyTypes = map[string]bool{
		"blocks": true, "blocked_by": true, "related": true,
	}
)

// ── wire shapes ───────────────────────────────────────────────────────

type causalNodeJSON struct {
	ID             string          `json:"id"`
	WorkspaceID    string          `json:"workspace_id"`
	IssueID        *string         `json:"issue_id"`
	Type           string          `json:"type"`
	Label          string          `json:"label"`
	Description    *string         `json:"description"`
	Metadata       json.RawMessage `json:"metadata"`
	Provenance     json.RawMessage `json:"provenance"`
	CreatedAt      string          `json:"created_at"`
	CreatedBy      *string         `json:"created_by"`
	LabSource      *string         `json:"lab_source"`
	LabRunID       *string         `json:"lab_run_id"`
	Status         string          `json:"status"`
	LastObservedAt string          `json:"last_observed_at"`
}

type causalEdgeJSON struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspace_id"`
	FromNodeID  string          `json:"from_node_id"`
	ToNodeID    string          `json:"to_node_id"`
	Type        string          `json:"type"`
	Weight      *float64        `json:"weight"`
	Confidence  *float64        `json:"confidence"`
	Metadata    json.RawMessage `json:"metadata"`
	Provenance  json.RawMessage `json:"provenance"`
	CreatedAt   string          `json:"created_at"`
	CreatedBy   *string         `json:"created_by"`
	ProposedBy  *string         `json:"proposed_by"`
	Status      string          `json:"status"`
}

type issueDependencyJSON struct {
	ID                string  `json:"id"`
	IssueID           string  `json:"issue_id"`
	DependsOnIssueID  string  `json:"depends_on_issue_id"`
	Type              string  `json:"type"`
	EvidenceCommentID *string `json:"evidence_comment_id"`
	CreatedBy         *string `json:"created_by"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`
}

// ── pgtype → wire helpers ─────────────────────────────────────────────

func uuidOrNil(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := util.UUIDToString(u)
	return &s
}

func textOrNil(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := t.String
	return &s
}

func numericOrNil(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, err := n.Float64Value()
	if err != nil || !f.Valid {
		return nil
	}
	v := f.Float64
	return &v
}

// jsonbOrNil passes the stored JSONB through verbatim; NULL/empty
// renders as {} so clients can always treat it as an object.
func jsonbOrNil(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage("{}")
	}
	return json.RawMessage(b)
}

func causalNodeToJSON(n dbpkg.CausalNode) causalNodeJSON {
	return causalNodeJSON{
		ID:             util.UUIDToString(n.ID),
		WorkspaceID:    util.UUIDToString(n.WorkspaceID),
		IssueID:        uuidOrNil(n.IssueID),
		Type:           n.Type,
		Label:          n.Label,
		Description:    textOrNil(n.Description),
		Metadata:       jsonbOrNil(n.Metadata),
		Provenance:     jsonbOrNil(n.Provenance),
		CreatedAt:      n.CreatedAt.Time.UTC().Format(timeRFC3339),
		CreatedBy:      textOrNil(n.CreatedBy),
		LabSource:      textOrNil(n.LabSource),
		LabRunID:       uuidOrNil(n.LabRunID),
		Status:         n.Status,
		LastObservedAt: n.LastObservedAt.Time.UTC().Format(timeRFC3339),
	}
}

func causalEdgeToJSON(e dbpkg.CausalEdge) causalEdgeJSON {
	return causalEdgeJSON{
		ID:          util.UUIDToString(e.ID),
		WorkspaceID: util.UUIDToString(e.WorkspaceID),
		FromNodeID:  util.UUIDToString(e.FromNodeID),
		ToNodeID:    util.UUIDToString(e.ToNodeID),
		Type:        e.Type,
		Weight:      numericOrNil(e.Weight),
		Confidence:  numericOrNil(e.Confidence),
		Metadata:    jsonbOrNil(e.Metadata),
		Provenance:  jsonbOrNil(e.Provenance),
		CreatedAt:   e.CreatedAt.Time.UTC().Format(timeRFC3339),
		CreatedBy:   textOrNil(e.CreatedBy),
		ProposedBy:  textOrNil(e.ProposedBy),
		Status:      e.Status,
	}
}

func issueDependencyToJSON(d dbpkg.IssueDependency) issueDependencyJSON {
	return issueDependencyJSON{
		ID:                util.UUIDToString(d.ID),
		IssueID:           util.UUIDToString(d.IssueID),
		DependsOnIssueID:  util.UUIDToString(d.DependsOnIssueID),
		Type:              d.Type,
		EvidenceCommentID: uuidOrNil(d.EvidenceCommentID),
		CreatedBy:         textOrNil(d.CreatedBy),
		CreatedAt:         d.CreatedAt.Time.UTC().Format(timeRFC3339),
		UpdatedAt:         d.UpdatedAt.Time.UTC().Format(timeRFC3339),
	}
}

// timeRFC3339 is the canonical timestamp rendering for this surface
// (mirrors timesfm run rows).
const timeRFC3339 = "2006-01-02T15:04:05Z07:00"

// ── small param helpers ───────────────────────────────────────────────

func causalLimit(r *http.Request) int {
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 100 {
			return n
		}
	}
	return 50
}

func causalOffset(r *http.Request) int {
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

// scopeWorkspace resolves the ?workspace_id= fence for single-workspace
// list/mutation endpoints (mythos handler trust shape).
func scopeWorkspace(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	return parseUUIDOrBadRequest(w, r.URL.Query().Get("workspace_id"), "workspace_id")
}

func writeCausalDBError(w http.ResponseWriter, err error) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23514" {
		// CHECK / partial-unique violations surface as 409 conflicts,
		// not raw 500s — the client can act on them (e.g. an active
		// edge with the same triple already exists).
		writeError(w, http.StatusConflict, "conflicts with an existing graph constraint: "+pgErr.Detail)
		return
	}
	writeError(w, http.StatusInternalServerError, "causal graph write failed: "+err.Error())
}

// ── issue_dependency (revived, mig 276) ───────────────────────────────

func (h *Handler) listIssueDependencies(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueID"))
	if !ok {
		return
	}
	deps, err := h.Queries.ListIssueDependencies(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dependencies: "+err.Error())
		return
	}
	dependents, err := h.Queries.ListIssueDependents(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list dependents: "+err.Error())
		return
	}
	outDeps := make([]issueDependencyJSON, 0, len(deps))
	for _, d := range deps {
		outDeps = append(outDeps, issueDependencyToJSON(d))
	}
	outDepsReverse := make([]issueDependencyJSON, 0, len(dependents))
	for _, d := range dependents {
		outDepsReverse = append(outDepsReverse, issueDependencyToJSON(d))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"dependencies": outDeps,
		"dependents":   outDepsReverse,
	})
}

type createDependencyRequest struct {
	TargetIssueID     string `json:"target_issue_id"`
	Type              string `json:"type,omitempty"`
	EvidenceCommentID string `json:"evidence_comment_id,omitempty"`
}

func (h *Handler) createIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueID"))
	if !ok {
		return
	}
	var req createDependencyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	target, ok := h.loadIssueForUser(w, r, req.TargetIssueID)
	if !ok {
		return
	}
	if target.ID == issue.ID {
		writeError(w, http.StatusBadRequest, "an issue cannot depend on itself")
		return
	}
	depType := req.Type
	if depType == "" {
		// "issue A depends on B" reads blocked_by in the 001_init type
		// set (A blocked_by B).
		depType = "blocked_by"
	}
	if !issueDependencyTypes[depType] {
		writeError(w, http.StatusBadRequest, "type must be one of blocks|blocked_by|related")
		return
	}
	var evidenceID pgtype.UUID
	if req.EvidenceCommentID != "" {
		var ok bool
		if evidenceID, ok = parseUUIDOrBadRequest(w, req.EvidenceCommentID, "evidence_comment_id"); !ok {
			return
		}
	}
	// No unique constraint backs the pair (001_init) — check first;
	// the single-user fork makes the race window irrelevant.
	if _, err := h.Queries.GetIssueDependencyByPair(r.Context(), dbpkg.GetIssueDependencyByPairParams{
		IssueID:          issue.ID,
		DependsOnIssueID: target.ID,
	}); err == nil {
		writeError(w, http.StatusConflict, "this dependency already exists")
		return
	}
	dep, err := h.Queries.CreateIssueDependency(r.Context(), dbpkg.CreateIssueDependencyParams{
		IssueID:           issue.ID,
		DependsOnIssueID:  target.ID,
		DepType:           depType,
		EvidenceCommentID: pgtype.UUID{Valid: evidenceID.Valid, Bytes: evidenceID.Bytes},
	})
	if err != nil {
		writeCausalDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, issueDependencyToJSON(dep))
}

func (h *Handler) deleteIssueDependency(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chi.URLParam(r, "issueID"))
	if !ok {
		return
	}
	depID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "dependencyID"), "dependencyID")
	if !ok {
		return
	}
	dep, err := h.Queries.GetIssueDependency(r.Context(), depID)
	if err != nil {
		writeError(w, http.StatusNotFound, "dependency not found")
		return
	}
	if dep.IssueID != issue.ID {
		// Scope: only the owning issue's surface can remove the edge.
		writeError(w, http.StatusNotFound, "dependency not found")
		return
	}
	if err := h.Queries.DeleteIssueDependency(r.Context(), depID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete dependency: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ── causal nodes ──────────────────────────────────────────────────────

func (h *Handler) listCausalNodes(w http.ResponseWriter, r *http.Request) {
	wsID, ok := scopeWorkspace(w, r)
	if !ok {
		return
	}
	params := dbpkg.ListCausalNodesParams{
		WorkspaceID: wsID,
		Lim:         pgtype.Int4{Valid: true, Int32: int32(causalLimit(r))},
		Offset:      pgtype.Int4{Valid: true, Int32: int32(causalOffset(r))},
	}
	if raw := r.URL.Query().Get("issue_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "issue_id")
		if !ok {
			return
		}
		params.IssueID = pgtype.UUID{Valid: true, Bytes: id.Bytes}
	}
	if raw := r.URL.Query().Get("type"); raw != "" {
		params.NodeType = pgtype.Text{Valid: true, String: raw}
	}
	nodes, err := h.Queries.ListCausalNodes(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list nodes: "+err.Error())
		return
	}
	out := make([]causalNodeJSON, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, causalNodeToJSON(n))
	}
	writeJSON(w, http.StatusOK, out)
}

type createNodeRequest struct {
	Label       string          `json:"label"`
	Type        string          `json:"type"`
	IssueID     string          `json:"issue_id,omitempty"`
	Description string          `json:"description,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
	LabSource   string          `json:"lab_source,omitempty"`
	LabRunID    string          `json:"lab_run_id,omitempty"`
}

func (h *Handler) createCausalNode(w http.ResponseWriter, r *http.Request) {
	var req createNodeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Label == "" {
		writeError(w, http.StatusBadRequest, "label is required")
		return
	}
	if !causalNodeTypes[req.Type] {
		writeError(w, http.StatusBadRequest,
			"type must be one of decision|action|outcome|assumption|evidence|constraint")
		return
	}
	params := dbpkg.CreateCausalNodeParams{
		NodeType: req.Type,
		Label:    req.Label,
	}
	if req.Description != "" {
		params.Description = pgtype.Text{Valid: true, String: req.Description}
	}
	if len(req.Metadata) > 0 {
		params.Metadata = req.Metadata
	}
	// The issue half scopes the node when present (loader = identifier-
	// or-UUID + membership); abstract nodes take ?workspace_id=.
	if req.IssueID != "" {
		issue, ok := h.loadIssueForUser(w, r, req.IssueID)
		if !ok {
			return
		}
		params.WorkspaceID = issue.WorkspaceID
		params.IssueID = pgtype.UUID{Valid: true, Bytes: issue.ID.Bytes}
	} else {
		wsID, ok := scopeWorkspace(w, r)
		if !ok {
			return
		}
		params.WorkspaceID = wsID
	}
	if req.LabSource != "" {
		params.LabSource = pgtype.Text{Valid: true, String: req.LabSource}
	}
	if req.LabRunID != "" {
		id, ok := parseUUIDOrBadRequest(w, req.LabRunID, "lab_run_id")
		if !ok {
			return
		}
		params.LabRunID = id
	}
	node, err := h.Queries.CreateCausalNode(r.Context(), params)
	if err != nil {
		writeCausalDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, causalNodeToJSON(node))
}

// ── causal edges ──────────────────────────────────────────────────────

func (h *Handler) listCausalEdges(w http.ResponseWriter, r *http.Request) {
	wsID, ok := scopeWorkspace(w, r)
	if !ok {
		return
	}
	params := dbpkg.ListCausalEdgesParams{
		WorkspaceID: wsID,
		Lim:         pgtype.Int4{Valid: true, Int32: int32(causalLimit(r))},
		Offset:      pgtype.Int4{Valid: true, Int32: int32(causalOffset(r))},
	}
	if raw := r.URL.Query().Get("issue_id"); raw != "" {
		id, ok := parseUUIDOrBadRequest(w, raw, "issue_id")
		if !ok {
			return
		}
		params.IssueID = pgtype.UUID{Valid: true, Bytes: id.Bytes}
	}
	if raw := r.URL.Query().Get("type"); raw != "" {
		params.EdgeType = pgtype.Text{Valid: true, String: raw}
	}
	if raw := r.URL.Query().Get("status"); raw != "" {
		params.EdgeStatus = pgtype.Text{Valid: true, String: raw}
	}
	if raw := r.URL.Query().Get("min_confidence"); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 0 || f > 1 {
			writeError(w, http.StatusBadRequest, "min_confidence must be a number in [0,1]")
			return
		}
		conf, _ := numericFromFloat(f)
		params.MinConfidence = conf
	}
	edges, err := h.Queries.ListCausalEdges(r.Context(), params)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list edges: "+err.Error())
		return
	}
	out := make([]causalEdgeJSON, 0, len(edges))
	for _, e := range edges {
		out = append(out, causalEdgeToJSON(e))
	}
	writeJSON(w, http.StatusOK, out)
}

type createEdgeRequest struct {
	FromNodeID string          `json:"from_node_id"`
	ToNodeID   string          `json:"to_node_id"`
	Type       string          `json:"type"`
	Weight     *float64        `json:"weight,omitempty"`
	Confidence *float64        `json:"confidence,omitempty"`
	Metadata   json.RawMessage `json:"metadata,omitempty"`
}

// numericFromFloat encodes a float into pgtype.Numeric (both the list
// filter and the create path use it).
func numericFromFloat(f float64) (pgtype.Numeric, bool) {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	var n pgtype.Numeric
	if err := n.Scan(s); err != nil {
		return pgtype.Numeric{}, false
	}
	return n, true
}

func (h *Handler) createCausalEdge(w http.ResponseWriter, r *http.Request) {
	wsID, ok := scopeWorkspace(w, r)
	if !ok {
		return
	}
	var req createEdgeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !causalEdgeTypes[req.Type] {
		writeError(w, http.StatusBadRequest,
			"type must be one of causes|supports|contradicts|depends_on|enables|blocks")
		return
	}
	fromID, ok := parseUUIDOrBadRequest(w, req.FromNodeID, "from_node_id")
	if !ok {
		return
	}
	toID, ok := parseUUIDOrBadRequest(w, req.ToNodeID, "to_node_id")
	if !ok {
		return
	}
	if fromID == toID {
		writeError(w, http.StatusBadRequest, "a node cannot cause itself")
		return
	}
	// Both endpoints must exist and live in the scoped workspace.
	fromNode, err := h.Queries.GetCausalNode(r.Context(), fromID)
	if err != nil || fromNode.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "from_node not found")
		return
	}
	toNode, err := h.Queries.GetCausalNode(r.Context(), toID)
	if err != nil || toNode.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "to_node not found")
		return
	}
	if toNode.WorkspaceID != fromNode.WorkspaceID {
		writeError(w, http.StatusBadRequest, "edge endpoints must share a workspace")
		return
	}
	params := dbpkg.CreateCausalEdgeParams{
		WorkspaceID: wsID,
		FromNodeID:  fromID,
		ToNodeID:    toID,
		EdgeType:    req.Type,
	}
	if req.Weight != nil {
		if w2, ok := numericFromFloat(*req.Weight); ok {
			params.Weight = w2
		}
	}
	if req.Confidence != nil {
		if *req.Confidence < 0 || *req.Confidence > 1 {
			writeError(w, http.StatusBadRequest, "confidence must be in [0,1]")
			return
		}
		if c, ok := numericFromFloat(*req.Confidence); ok {
			params.Confidence = c
		}
	}
	if len(req.Metadata) > 0 {
		params.Metadata = req.Metadata
	}
	// Manual user edges are active immediately; provenance stamps the
	// manual source so a later Tier D proposal can reference it.
	params.Provenance = []byte(`{"source":"manual"}`)
	params.CreatedBy = pgtype.Text{Valid: true, String: "user"}
	edge, err := h.Queries.CreateCausalEdge(r.Context(), params)
	if err != nil {
		writeCausalDBError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, causalEdgeToJSON(edge))
}

func (h *Handler) getCausalEdgeScoped(w http.ResponseWriter, r *http.Request) (dbpkg.CausalEdge, bool) {
	wsID, ok := scopeWorkspace(w, r)
	if !ok {
		return dbpkg.CausalEdge{}, false
	}
	edgeID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "edgeID"), "edgeID")
	if !ok {
		return dbpkg.CausalEdge{}, false
	}
	edge, err := h.Queries.GetCausalEdge(r.Context(), edgeID)
	if err != nil || edge.WorkspaceID != wsID {
		writeError(w, http.StatusNotFound, "edge not found")
		return dbpkg.CausalEdge{}, false
	}
	return edge, true
}

func (h *Handler) deleteCausalEdge(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.getCausalEdgeScoped(w, r); !ok {
		return
	}
	edgeID, _ := parseUUIDOrBadRequest(w, chi.URLParam(r, "edgeID"), "edgeID")
	if err := h.Queries.DeleteCausalEdge(r.Context(), edgeID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete edge: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// confirmCausalEdge implements the Tier D curation gate: suggested →
// active. Two 409 shapes: the edge was already decided (not suggested
// any more) and the partial unique index caught a racing active edge
// with the same (from, to, type) triple.
func (h *Handler) confirmCausalEdge(w http.ResponseWriter, r *http.Request) {
	edge, ok := h.getCausalEdgeScoped(w, r)
	if !ok {
		return
	}
	if edge.Status != "suggested" {
		writeError(w, http.StatusConflict, "edge is not in the suggested state")
		return
	}
	edgeID, _ := parseUUIDOrBadRequest(w, chi.URLParam(r, "edgeID"), "edgeID")
	confirmed, err := h.Queries.ConfirmCausalEdge(r.Context(), edgeID)
	if err != nil {
		writeCausalDBError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, causalEdgeToJSON(confirmed))
}

func (h *Handler) rejectCausalEdge(w http.ResponseWriter, r *http.Request) {
	edge, ok := h.getCausalEdgeScoped(w, r)
	if !ok {
		return
	}
	if edge.Status != "suggested" {
		writeError(w, http.StatusConflict, "edge is not in the suggested state")
		return
	}
	edgeID, _ := parseUUIDOrBadRequest(w, chi.URLParam(r, "edgeID"), "edgeID")
	rejected, err := h.Queries.RejectCausalEdge(r.Context(), edgeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to reject edge: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, causalEdgeToJSON(rejected))
}

// ── subgraph + path (BFS) ─────────────────────────────────────────────

// causalSubgraph serves GET /api/causal-graph/subgraph?issue_id=&depth=.
// Seeds from the issue's active nodes and expands UNDIRECTED (both
// edge directions) depth times — the same slice powers the issue-side
// popup and (next phase) agent context injection. Depth clamps to
// [1,4], default 2.
func (h *Handler) causalSubgraph(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, r.URL.Query().Get("issue_id"))
	if !ok {
		return
	}
	depth := 2
	if raw := r.URL.Query().Get("depth"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			depth = n
		}
	}
	if depth < 1 {
		depth = 1
	}
	if depth > 4 {
		depth = 4
	}

	seed, err := h.Queries.ListCausalNodesByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to seed subgraph: "+err.Error())
		return
	}
	if len(seed) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"issue_id": util.UUIDToString(issue.ID),
			"depth":    depth,
			"nodes":    []causalNodeJSON{},
			"edges":    []causalEdgeJSON{},
		})
		return
	}

	visited := make(map[pgtype.UUID]bool, len(seed))
	frontier := make([]pgtype.UUID, 0, len(seed))
	nodesByID := make(map[pgtype.UUID]dbpkg.CausalNode, len(seed))
	for _, n := range seed {
		visited[n.ID] = true
		frontier = append(frontier, n.ID)
		nodesByID[n.ID] = n
	}
	edgesOut := make(map[pgtype.UUID]dbpkg.CausalEdge)

	for d := 0; d < depth && len(frontier) > 0; d++ {
		touching, err := h.Queries.ListActiveEdgesTouching(r.Context(), frontier)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "subgraph expansion failed: "+err.Error())
			return
		}
		var nextIDs []pgtype.UUID
		for _, e := range touching {
			edgesOut[e.ID] = e
			for _, endpoint := range []pgtype.UUID{e.FromNodeID, e.ToNodeID} {
				if visited[endpoint] {
					continue
				}
				visited[endpoint] = true
				nextIDs = append(nextIDs, endpoint)
			}
		}
		frontier = frontier[:0]
		if len(nextIDs) > 0 {
			neighborNodes, err := h.Queries.ListCausalNodesByIDs(r.Context(), nextIDs)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "subgraph expansion failed: "+err.Error())
				return
			}
			for _, n := range neighborNodes {
				nodesByID[n.ID] = n
				frontier = append(frontier, n.ID)
			}
		}
	}

	nodes := make([]causalNodeJSON, 0, len(nodesByID))
	for _, n := range nodesByID {
		nodes = append(nodes, causalNodeToJSON(n))
	}
	edges := make([]causalEdgeJSON, 0, len(edgesOut))
	for _, e := range edgesOut {
		edges = append(edges, causalEdgeToJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"issue_id": util.UUIDToString(issue.ID),
		"depth":    depth,
		"nodes":    nodes,
		"edges":    edges,
	})
}

// causalPath serves GET /api/causal-graph/path?from=&to= — directed
// BFS over active edges only, shortest hop count first. 404 when the
// target is unreachable (a missing answer, not an error).
func (h *Handler) causalPath(w http.ResponseWriter, r *http.Request) {
	fromID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("from"), "from")
	if !ok {
		return
	}
	toID, ok := parseUUIDOrBadRequest(w, r.URL.Query().Get("to"), "to")
	if !ok {
		return
	}
	fromNode, err := h.Queries.GetCausalNode(r.Context(), fromID)
	if err != nil {
		writeError(w, http.StatusNotFound, "from node not found")
		return
	}
	toNode, err := h.Queries.GetCausalNode(r.Context(), toID)
	if err != nil {
		writeError(w, http.StatusNotFound, "to node not found")
		return
	}
	if fromNode.WorkspaceID != toNode.WorkspaceID {
		writeError(w, http.StatusBadRequest, "path endpoints must share a workspace")
		return
	}

	if fromNode.ID == toNode.ID {
		writeJSON(w, http.StatusOK, map[string]any{
			"nodes": []causalNodeJSON{causalNodeToJSON(fromNode)},
			"edges": []causalEdgeJSON{},
		})
		return
	}

	visited := map[pgtype.UUID]bool{fromNode.ID: true}
	parentEdge := make(map[pgtype.UUID]dbpkg.CausalEdge)
	parentNode := make(map[pgtype.UUID]pgtype.UUID)
	frontier := []pgtype.UUID{fromNode.ID}
	found := false
	for len(frontier) > 0 && !found {
		edges, err := h.Queries.ListActiveEdgesFrom(r.Context(), frontier)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "path search failed: "+err.Error())
			return
		}
		next := frontier[:0]
		frontier = frontier[:0]
		for _, e := range edges {
			if visited[e.ToNodeID] {
				continue
			}
			visited[e.ToNodeID] = true
			parentEdge[e.ToNodeID] = e
			parentNode[e.ToNodeID] = e.FromNodeID
			if e.ToNodeID == toNode.ID {
				found = true
				break
			}
			next = append(next, e.ToNodeID)
		}
		frontier = append(frontier, next...)
	}
	if !found {
		writeError(w, http.StatusNotFound, "no causal path between the nodes")
		return
	}

	// Walk parents back to the source.
	var pathNodes []dbpkg.CausalNode
	var pathEdges []dbpkg.CausalEdge
	cursor := toNode.ID
	for {
		node, err := h.Queries.GetCausalNode(r.Context(), cursor)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "path reconstruction failed: "+err.Error())
			return
		}
		pathNodes = append([]dbpkg.CausalNode{node}, pathNodes...)
		edge, hasEdge := parentEdge[cursor]
		if !hasEdge {
			break
		}
		pathEdges = append([]dbpkg.CausalEdge{edge}, pathEdges...)
		cursor = parentNode[cursor]
	}

	nodes := make([]causalNodeJSON, 0, len(pathNodes))
	for _, n := range pathNodes {
		nodes = append(nodes, causalNodeToJSON(n))
	}
	edges := make([]causalEdgeJSON, 0, len(pathEdges))
	for _, e := range pathEdges {
		edges = append(edges, causalEdgeToJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodes": nodes,
		"edges": edges,
	})
}

// RegisterCausalGraphRoutes mounts the gated surface. The caller wraps
// it in RequireExperimentalFlag("causal_graph") (router.go).
func RegisterCausalGraphRoutes(r chi.Router, h *Handler) {
	r.Get("/api/issues/{issueID}/dependencies", h.listIssueDependencies)
	r.Post("/api/issues/{issueID}/dependencies", h.createIssueDependency)
	r.Delete("/api/issues/{issueID}/dependencies/{dependencyID}", h.deleteIssueDependency)

	r.Get("/api/causal-graph/nodes", h.listCausalNodes)
	r.Post("/api/causal-graph/nodes", h.createCausalNode)
	r.Get("/api/causal-graph/edges", h.listCausalEdges)
	r.Post("/api/causal-graph/edges", h.createCausalEdge)
	r.Delete("/api/causal-graph/edges/{edgeID}", h.deleteCausalEdge)
	r.Post("/api/causal-graph/edges/{edgeID}/confirm", h.confirmCausalEdge)
	r.Post("/api/causal-graph/edges/{edgeID}/reject", h.rejectCausalEdge)

	r.Get("/api/causal-graph/subgraph", h.causalSubgraph)
	r.Get("/api/causal-graph/path", h.causalPath)
}
