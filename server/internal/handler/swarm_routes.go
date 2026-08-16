// Package handler — swarm_routes.go (0.5.21).
//
// Route registration helper. The swarm HTTP handlers live in
// swarm_run.go; this file exposes RegisterSwarmRoutes so cmd/server/
// router.go can wire the routes next to the other experimental gates.
//
// Critical: the by-issue route MUST register BEFORE the {runID}
// route (Active Contract #3: literal slug before {param}). The
// /runs/{runID} route would otherwise capture the literal
// "by-issue" as the param id, and parseUUIDOrBadRequest would 400
// on the literal string.
//
// Mount order (mirrors claude_science_runtime.go + mythos_supervise.go
// patterns):
//   GET    /api/experimental/swarm-topology/runs               — past runs list
//   POST   /api/experimental/swarm-topology/runs               — bootstrap
//   POST   /api/experimental/swarm-topology/runs/{runID}/interrupt
//   GET    /api/experimental/swarm-topology/runs/{runID}/state
//   GET    /api/issues/{issueID}/swarm-runs

package handler

import (
	"github.com/go-chi/chi/v5"
)

// RegisterSwarmRoutes mounts the swarm HTTP surface on the given
// router. Called from cmd/server/router.go after h.Queries is wired
// (so the handlers can read swarm_run + swarm_role rows).
//
// Note: the flag-gate middleware at router.go (DefaultFor("swarm_topology"))
// is applied upstream of this mount — the routes below only fire
// when the user has enabled the flag.
//
// 0.5.22 audit fix (P2-9): trimmed the misleading "by-issue literal"
// comment copied from mythos_supervise.go — this handler set has no
// such literal, so the load-bearing contract is just "literal slugs
// before {id}". Active Contract #3 (chi route order) still applies
// if a future contributor adds /runs/by-issue or similar.
func RegisterSwarmRoutes(r chi.Router, h *Handler) {
	r.Route("/api/experimental/swarm-topology", func(r chi.Router) {
		r.Post("/runs", h.PostSwarmRun)
		// GET /runs (literal) registers BEFORE /runs/{id}/state so
		// chi's matcher prefers the literal when a future
		// contributor adds /runs/by-issue or any other literal.
		r.Get("/runs", h.GetSwarmRunsByWorkspace)
		r.Post("/runs/{id}/interrupt", h.PostSwarmInterrupt)
		r.Get("/runs/{id}/state", h.GetSwarmRunState)
	})
	// Issue-side reverse lookup. Gated along with the experimental
	// surface — if the user has not enabled the flag, no swarm_run can
	// exist (PostSwarmRun is gated too), so the lookup would always
	// 404 anyway. Mounting inside the flag group keeps the route
	// table uniform and avoids an unenumerable bypass path.
	r.Get("/api/issues/{id}/swarm-runs", h.GetSwarmRunsByIssue)
}