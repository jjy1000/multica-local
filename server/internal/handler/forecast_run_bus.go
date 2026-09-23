// Package handler — forecast_run_bus.go (0.5.111)
//
// In-memory pubsub for per-issue pythia forecast runs. The background
// runner publishes every landed round / synthesized report / terminal
// status here; the SSE stream handler (forecast_run_stream.go)
// subscribes so panel clients watch a run live and reconnect mid-run
// without losing rounds (the SocialSim "后台线程 + 事件缓冲" pattern —
// persistence carries the replay, the bus carries the live tail).
//
// Deliberately tiny and bounded: channels are buffered (64) and sends
// are non-blocking — a slow or stalled subscriber must never wedge the
// runner, which keeps running (and persisting) regardless of audience.

package handler

import (
	"context"
	"sync"
)

// pythiaRunEvent is one bus frame. Type discriminates:
//   - "round"   — Index = 0-based envelope index, Envelope = the frame.
//   - "report"  — Report = the synthesized conclusion report (may be ""
//     when synthesis fell back to the mechanical summary; the status
//     frame still follows).
//   - "status"  — Status = "completed" | "aborted" | "failed". Terminal;
//     the stream closes after forwarding it.
type pythiaRunEvent struct {
	Type     string            `json:"type"`
	Index    int               `json:"index,omitempty"`
	Envelope *forecastEnvelope `json:"envelope,omitempty"`
	Report   string            `json:"report,omitempty"`
	Status   string            `json:"status,omitempty"`
}

// pythiaRunBus fans runner events out to stream subscribers, keyed by
// run id (UUID string). cancels maps running runs to their context
// CancelFunc so the cancel endpoint can stop a detached runner.
type pythiaRunBus struct {
	mu      sync.Mutex
	subs    map[string]map[chan pythiaRunEvent]struct{}
	cancels map[string]context.CancelFunc
}

var pythiaForecastBus = newPythiaRunBus()

func newPythiaRunBus() *pythiaRunBus {
	return &pythiaRunBus{
		subs:    make(map[string]map[chan pythiaRunEvent]struct{}),
		cancels: make(map[string]context.CancelFunc),
	}
}

const pythiaRunBusBuffer = 64

// subscribe registers a channel for the run and returns an unsubscribe
// func (safe to call twice).
func (b *pythiaRunBus) subscribe(runID string) (<-chan pythiaRunEvent, func()) {
	ch := make(chan pythiaRunEvent, pythiaRunBusBuffer)
	b.mu.Lock()
	set, ok := b.subs[runID]
	if !ok {
		set = make(map[chan pythiaRunEvent]struct{})
		b.subs[runID] = set
	}
	set[ch] = struct{}{}
	b.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			if set, ok := b.subs[runID]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(b.subs, runID)
				}
			}
			b.mu.Unlock()
		})
	}
}

// publish fans one event out to every subscriber. Non-blocking: a full
// subscriber buffer drops the frame (the stream's DB-backed snapshot on
// reconnect is the recovery path, so a dropped live frame self-heals).
func (b *pythiaRunBus) publish(runID string, ev pythiaRunEvent) {
	b.mu.Lock()
	set := b.subs[runID]
	for ch := range set {
		select {
		case ch <- ev:
		default:
		}
	}
	b.mu.Unlock()
}

// registerCancel stores the runner's CancelFunc for the run.
func (b *pythiaRunBus) registerCancel(runID string, cancel context.CancelFunc) {
	b.mu.Lock()
	b.cancels[runID] = cancel
	b.mu.Unlock()
}

// cancelRun signals the running goroutine. Returns false when no live
// runner is registered (the caller should fall back to a direct DB
// status write).
func (b *pythiaRunBus) cancelRun(runID string) bool {
	b.mu.Lock()
	cancel, ok := b.cancels[runID]
	b.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// removeRun drops the runner registration (deferred by the runner when
// its goroutine exits).
func (b *pythiaRunBus) removeRun(runID string) {
	b.mu.Lock()
	delete(b.cancels, runID)
	delete(b.subs, runID)
	b.mu.Unlock()
}
