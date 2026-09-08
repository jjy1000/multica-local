// Package handler — semantica_decisions_test.go (0.5.105, audit H1)
//
// Regression pin for the semantica decisions route move. The endpoint
// used to sit on the OUTER router in cmd/server/router.go with no auth
// middleware and no flag guard: requestUserID reads the raw X-User-ID
// header, so an unauthenticated caller could spoof a member identity,
// and a flag-off install still served the surface — both violations of
// the Labs Platform invariant #1 (flag-off MUST bypass entirely).
//
// The route now mounts inside the authenticated group behind
// RequireExperimentalFlag("semantica"). Router wiring itself is verified
// at cold-start; this file pins the middleware contract on the exact
// flag key so a typo (e.g. gating on a renamed key) fails here.
package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSemanticaDecisionsFlagOffReturns404(t *testing.T) {
	h := &Handler{} // Queries nil → experimentalFlagEnabled falls through to the catalog default (off for every built-in flag).
	mw := h.RequireExperimentalFlag("semantica")

	probeCalled := false
	probe := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probeCalled = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/experimental/semantica/decisions?workspace=11111111-1111-1111-1111-111111111111", nil)
	rec := httptest.NewRecorder()

	mw(probe).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("flag-off semantica decisions: got status %d, want 404 (guard answers indistinguishably-from-missing)", rec.Code)
	}
	if probeCalled {
		t.Fatal("flag-off semantica decisions: probe handler was reached — guard did not short-circuit")
	}
}
