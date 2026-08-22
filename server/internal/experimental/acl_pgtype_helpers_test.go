// Package experimental — acl_pgtype_helpers_test.go (test fixture only)
//
// Tiny helper to avoid importing pgtype's UUID zero-value syntax in
// every test. Not exported (lowercase p) — only used by acl_test.go.
package experimental

import "github.com/jackc/pgx/v5/pgtype"

func pgtypeUUIDEmpty() pgtype.UUID {
	return pgtype.UUID{}
}

func pgtypeUUIDFromString(s string) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(s); err != nil {
		panic(err)
	}
	return id
}