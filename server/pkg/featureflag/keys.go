package featureflag

import "context"

// Product flag keys. Keys are stable identifiers — once shipped, a key's
// meaning must never change, because env overrides and persisted decisions
// key on it. Add new keys here rather than scattering string literals in
// callers, and document the rollout semantics on each constant.

// CustomIssueStatuses gates MUL-6243 (per-workspace custom issue statuses
// over the 7 canonical categories). It is a one-way rollout gate:
// creating/archiving custom statuses is gated, while readers ship ungated —
// the issue_effective_status SQL function and issuestatus.Effective resolve
// custom keys through the catalog and fall back to the raw status for
// workspaces without custom statuses, which is exactly the pre-feature
// behavior. Default is off; set FF_CUSTOM_ISSUE_STATUSES=true (or a
// provider decision) to enable.
const CustomIssueStatuses = "custom_issue_statuses"

// CustomIssueStatusesEnabled reports whether the MUL-6243 custom issue
// status feature is enabled for the request context. A nil *Service is
// valid (see Service.IsEnabled) and reads as off.
func CustomIssueStatusesEnabled(ctx context.Context, flags *Service) bool {
	return flags.IsEnabled(ctx, CustomIssueStatuses, false)
}
