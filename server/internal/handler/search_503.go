package handler

import (
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// searchStatementTimeout bounds every /search request at the Postgres level.
//
// Local default is 5s (matches the existing context.WithTimeout in
// SearchIssues / SearchProjects since 0.3.5). Upstream's 3s would also be
// safe on indexed data, but self-hosted deployments occasionally run on
// unindexed schemas (no pg_bigm / pg_trgm) where 3s is too tight — keep
// 5s for the localized build.
//
// The upstream SET-LOCAL-statement_timeout belt-and-suspenders is layered on
// top of the existing Go-side context.WithTimeout. Either path firing
// surfaces to the client as http.StatusServiceUnavailable (503) — distinct
// from the 504 Gateway Timeout we already emit for context-level expiry,
// so the frontend can tell apart "Postgres killed the query" from "the
// request hung past the Go context deadline".
//
// Reference: MUL-4059 (search卡死没有任何反应) — original fix in 0.3.5
// added the 5s + 504 path. This file layers the SQLSTATE 57014 → 503 mapping
// on top so the two failure modes are distinguishable.
const searchStatementTimeout = 5 * time.Second

// searchStatementTimeoutOverride, when non-zero, replaces
// searchStatementTimeout for the duration of a test. Always read through
// effectiveSearchStatementTimeout below. Set ONLY from test setup; never
// mutate from production code paths.
var searchStatementTimeoutOverride time.Duration

// effectiveSearchStatementTimeout returns the active timeout. Tests can
// swap it via searchStatementTimeoutOverride to exercise timeout paths
// without waiting 5s of wall clock.
func effectiveSearchStatementTimeout() time.Duration {
	if searchStatementTimeoutOverride > 0 {
		return searchStatementTimeoutOverride
	}
	return searchStatementTimeout
}

// isSearchStatementTimeout reports whether err is the canonical Postgres
// query_canceled error (SQLSTATE 57014). Both `SET LOCAL statement_timeout`
// firing and a client-side context cancellation surface as 57014 — the two
// are indistinguishable from the client side, which is intentional in the
// pgx layer (pgx surfaces 57014 for any "the server cancelled the
// statement" path).
//
// Use this in addition to errors.Is(err, context.DeadlineExceeded): the
// Go-context path emits 504, the Postgres-side statement_timeout emits 503
// per upstream's contract.
func isSearchStatementTimeout(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "57014"
	}
	return false
}