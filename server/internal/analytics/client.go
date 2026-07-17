// Package analytics ships product telemetry events to an external analytics
// backend (PostHog). Events feed the acquisition → activation → expansion
// funnel — see docs/analytics.md for the event contract.
//
// Design:
//   - Capture is non-blocking. Request handlers must never wait on analytics
//     network I/O, so we enqueue into a bounded channel and a background
//     worker flushes to PostHog in batches.
//   - When the queue is full events are dropped (and counted). A broken
//     analytics backend must never degrade the product.
//   - When POSTHOG_API_KEY is empty the package runs a no-op client, which
//     keeps local dev and self-hosted instances friction-free.
package analytics

import (
	"log/slog"
	"os"
	"strings"
	"time"
)

// Event is a single analytics capture. Fields mirror PostHog's /capture/ shape
// but are framework-agnostic so alternate backends can plug in later.
type Event struct {
	// Name of the event (e.g. "signup", "workspace_created").
	Name string

	// DistinctID identifies the person this event belongs to. For logged-in
	// users this is user.id; for anonymous events it should be the anon_id
	// that was previously used on the frontend so identity merging works.
	DistinctID string

	// WorkspaceID scopes the event to a workspace. Required when the event is
	// about a workspace-level action (workspace_created, issue_executed, ...).
	// Empty is allowed for pre-workspace events (signup).
	WorkspaceID string

	// Properties is the free-form bag of event attributes. Only serialisable
	// values (string, number, bool, nested maps/slices of the same) should
	// go here. Never put raw PII like full emails here — use email_domain.
	Properties map[string]any

	// SetOnce properties attach to the person record and are only written the
	// first time they appear. Use this for acquisition attribution
	// (initial_utm_source, etc.) so later events don't overwrite the origin.
	SetOnce map[string]any

	// Set properties attach to the person record and overwrite on every write.
	// Use this for mutable cohort signals (role, use_case, platform_preference)
	// that users can legitimately change during onboarding.
	Set map[string]any

	// Timestamp is optional; when zero the client fills in time.Now().
	Timestamp time.Time
}

// Client is the narrow surface the rest of the codebase depends on. Handlers
// call Capture and move on; the implementation is responsible for buffering,
// batching, and shipping.
type Client interface {
	Capture(e Event)
	// Close drains pending events. Call once during graceful shutdown.
	Close()
}

// NewFromEnv returns a Client configured from environment variables.
//
// Localized build: telemetry is permanently disabled. This function always
// returns a NoopClient so the rest of the codebase can keep its
// `analytics.Client` shape (callers, metrics.IncForEvent dispatch, tests)
// without making any outbound network calls. The previous behaviour —
// honouring POSTHOG_API_KEY / POSTHOG_HOST / ANALYTICS_DISABLED / APP_ENV —
// is preserved as commented-out reference for any future re-enable, but
// none of the env reads short-circuit the noop return.
func NewFromEnv() Client {
	slog.Info("analytics: disabled (localized build)")
	return NoopClient{}
}

func isDisabled() bool {
	v := os.Getenv("ANALYTICS_DISABLED")
	return v == "true" || v == "1"
}

func EnvironmentFromEnv() string {
	if v := normalizeEnvironment(os.Getenv("ANALYTICS_ENVIRONMENT")); v != "" {
		return v
	}
	if v := normalizeEnvironment(os.Getenv("APP_ENV")); v != "" {
		return v
	}
	return "dev"
}

func normalizeEnvironment(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "production", "prod":
		return "production"
	case "staging", "stage":
		return "staging"
	case "development", "dev", "test", "local":
		return "dev"
	default:
		return ""
	}
}

// NoopClient silently drops all events. Used in tests, in local dev when
// POSTHOG_API_KEY is unset, and in self-hosted instances that opt out.
type NoopClient struct{}

func (NoopClient) Capture(Event) {}
func (NoopClient) Close()        {}
