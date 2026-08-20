// Package service: AutopilotQuotaExceededError is the sentinel a 429
// handler (MUL-6472) can serialize without embedding commercial copy
// or plan names. Definitions extracted from upstream
// internal/service/autopilot_quota.go (0.5.45 audit batch) so the
// trigger error path in handler/autopilot.go can dispatch:
//
//	var quotaErr *service.AutopilotQuotaExceededError
//	if errors.As(err, &quotaErr) { ... }
//
// The full quota subsystem (period table, reservation, decision
// counters, entitlement client) is NOT ported — that requires the
// upstream migrations 261-374 + sqlc regen + service refactor that
// the fork has not landed. This file ships just the error type
// so MUL-6472's quota branch typechecks; the quota branch
// itself stays latent in the fork until the upstream schema lands.
package service

import "time"

// AutopilotQuotaExceededError is returned only for an enforce decision whose
// Cloud-provided interval is full. HTTP callers can serialize the facts without
// embedding commercial copy or plan names in OSS.
type AutopilotQuotaExceededError struct {
	Used     int64
	Reserved int64
	Limit    int64
	ResetAt  time.Time
}

func (e *AutopilotQuotaExceededError) Error() string {
	return "autopilot run quota exceeded"
}
