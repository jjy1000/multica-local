// Package agent_self_optimization — scheduler.go (0.3.45.1).
//
// Pure-function scheduler: given the last successful run time and the
// current wall clock, return the next instant at which the runner
// should fire. The two gates are stacked:
//
//  1. minInterval: the time since the last successful run must be at
//     least 96 hours (4 days). The user-visible spec is "每四天一次优化".
//  2. wallClock: the trigger time must land on a weekday (Mon-Fri in
//     the user's local timezone) at exactly 10:00 local.
//
// If the next eligible slot is in the future, the function returns
// that slot. If we're already past the minInterval gate but the wall-
// clock target is later today / on a weekend, we walk forward day by
// day until we hit a weekday 10:00 that's also ≥minInterval away.
//
// Pure-function design: no DB access, no I/O, no global state. The
// runner calls this once per minute from the service ticker; tests
// cover every weekday/weekend boundary with table-driven cases.
package agent_self_optimization

import (
	"time"
)

// MinRunInterval is the user-visible "每四天" cadence. Hard-coded — a
// feature flag here would defeat the cadence's whole point (the user
// asked for "every 4 days, fixed").
const MinRunInterval = 96 * time.Hour

// TargetLocalHour is the wall-clock hour at which a run should fire,
// expressed in the user's local timezone (the runner resolves the
// location via time.Local at startup).
const TargetLocalHour = 10

// TargetLocalMinute is the minute-of-hour at which a run should fire.
// Pair with TargetLocalHour.
const TargetLocalMinute = 0

// NextTrigger computes the next eligible trigger instant.
//
// Parameters:
//
//   - lastSuccess: time of the previous successful run. Pass the zero
//     value (time.Time{}) when no run has ever succeeded; the function
//     then treats the minInterval gate as already satisfied and walks
//     forward from `now` to the next weekday 10:00.
//
//   - now: the current wall clock. Caller should pass time.Now() or a
//     DB-derived equivalent (DB time is preferred for cross-instance
//     consistency, but the daemon is single-instance so the local
//     clock is fine).
//
//   - loc: the timezone used to evaluate the weekday + 10:00 gates.
//     Pass time.Local for "user's local clock" semantics; pass time.UTC
//     for the legacy UTC interpretation. The runner resolves this
//     once at startup.
//
// Return value: the next trigger instant in absolute time (always in
// loc). The runner should NOT fire at this instant automatically —
// it schedules a tick that compares now() against NextTrigger and
// fires only when now() ≥ result.
func NextTrigger(lastSuccess, now time.Time, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}

	// 1. Earliest allowed instant = max(now, lastSuccess + 96h).
	earliest := now
	if !lastSuccess.IsZero() {
		afterInterval := lastSuccess.Add(MinRunInterval)
		if afterInterval.After(earliest) {
			earliest = afterInterval
		}
	}

	// 2. From earliest, walk forward to the next weekday 10:00 local.
	candidate := nextWeekdayTenAM(earliest, loc)

	// 3. Belt-and-braces: if candidate landed before earliest (can
	// happen when lastSuccess + 96h falls mid-day and the next 10:00
	// is later today), re-anchor to earliest and walk again.
	if candidate.Before(earliest) {
		candidate = nextWeekdayTenAM(earliest, loc)
	}

	return candidate
}

// nextWeekdayTenAM returns the next instant at or after `from` whose
// local-time weekday is Mon-Fri and whose local-time hour/minute is
// exactly 10:00. The function does NOT consult MinRunInterval — the
// caller owns that gate.
func nextWeekdayTenAM(from time.Time, loc *time.Location) time.Time {
	local := from.In(loc)
	for {
		// Set hour:minute:second to 10:00:00 in local.
		candidate := time.Date(
			local.Year(), local.Month(), local.Day(),
			TargetLocalHour, TargetLocalMinute, 0, 0, loc,
		)
		// If today's 10:00 is still in the future AND today is a
		// weekday, return today.
		if !candidate.Before(from) && isWeekday(candidate.Weekday()) {
			return candidate
		}
		// Otherwise advance to tomorrow and retry.
		local = time.Date(
			local.Year(), local.Month(), local.Day()+1,
			0, 0, 0, 0, loc,
		)
	}
}

// isWeekday reports whether w is Mon-Fri (Go's time.Weekday: Sunday=0,
// Monday=1, ..., Saturday=6).
func isWeekday(w time.Weekday) bool {
	return w >= time.Monday && w <= time.Friday
}