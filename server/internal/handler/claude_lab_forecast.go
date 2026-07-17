// Package handler — claude_lab_forecast.go (0.3.24+)
//
// /api/experimental/claude-science-lab/forecast/stream — Server-Sent
// Events endpoint that the Claude Research Lab `<ForecastTab />`
// subscribes to when the `claude_science_lab` flag is on. The
// stream emits one prediction envelope every 5 s so the
// `<PythiaDashboard />` reused inside the lab updates in real
// time without polling.
//
// Wire shape (Content-Type: text/event-stream, charset=utf-8):
//
//   event: prediction
//   data: {"id":"p_…","scenario":"…","narrative":"…","probability":0.42,
//          "confidence":0.71,"horizon":"week","persona":"strategist",
//          "createdAt":"2026-07-15T22:00:00Z"}
//
//   : keep-alive comment every 15 s so corporate proxies don't
//   kill the long-poll.
//
// Why SSE and not WebSocket: the renderer already speaks SSE
// (pythia-view uses use-pythia-sse for the same reason). SSE keeps
// the request lifecycle simple — one HTTP connection per
// subscriber, server holds the goroutine until the client
// disconnects, no handshake overhead. Throughput is one frame
// every 5 s, well within HTTP/1.1 limits.
//
// Mock data: 0.3.24 ships the wire and the gateway but the
// underlying forecaster is a stub that returns synthetic scenarios
// derived from a seeded PRNG. 0.3.25 will swap the stub for a real
// model call through the same MULTICA provider chain used by
// chat. The wire format is stable across both versions.
//
// Hard rules:
//
//  1. The route is gated by experimental.DefaultFor("claude_science_lab")
//     in router.go — when the flag is off, the route physically
//     doesn't exist. The chi router does not register a 404
//     handler, so off-flag clients see a connection error rather
//     than a misleading 200. (Same contract as the runtime
//     handler above.)
//
//  2. The handler runs the loop on the request goroutine — no
//     background goroutine, no shared state. Each subscriber
//     gets a fresh PRNG seeded with the request's `?seed` query
//     param (or time.Now().UnixNano() when absent). This makes
//     streams deterministic for tests and prevents cross-talk
//     between concurrent subscribers.
//
//  3. The handler respects ctx.Done() for both the request and a
//     60 s shutdown window. The latter matches the Lab's
//     `init_timeout_ms: 30000` plus a safety margin so the
//     desktop cold start can shut us down without leaving
//     dangling goroutines on the daemon side.

package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
)

// RegisterClaudeLabForecastRoutes wires the SSE endpoint onto the
// supplied chi router. The caller (router.go) MUST gate the entire
// call on experimental.DefaultFor("claude_science_lab") so the
// route physically disappears when the flag is off.
//
// 0.3.27 B2: the handler reads the *Handler through context so it can
// pick the data source at open-time (oracle vs synthetic). We inject
// via middleware rather than via request-scoped state because the SSE
// loop runs on the request goroutine and naturally inherits.
func RegisterClaudeLabForecastRoutes(r chi.Router, h *Handler) {
	r.Group(func(sub chi.Router) {
		if h != nil {
			sub.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					ctx := context.WithValue(req.Context(), forecastHandlerKey{}, h)
					next.ServeHTTP(w, req.WithContext(ctx))
				})
			})
		}
		sub.Get("/api/experimental/claude-science-lab/forecast/stream", forecastStream)
	})
}

type forecastEnvelope struct {
	ID              string  `json:"id"`
	IssueID         string  `json:"issue_id,omitempty"`
	Scenario        string  `json:"scenario"`
	Narrative       string  `json:"narrative"`
	Probability     float64 `json:"probability"`
	Confidence      float64 `json:"confidence"`
	Horizon         string  `json:"horizon"`
	Persona         string  `json:"persona"`
	LabSource       string  `json:"lab_source"`
	ScenarioContext string  `json:"scenario_context,omitempty"`
	CreatedAt       string  `json:"createdAt"`
}

const (
	forecastInterval    = 5 * time.Second
	forecastKeepAlive   = 15 * time.Second
	forecastMaxLifetime = 60 * time.Second
)

var forecastSeq atomic.Uint64

// forecastStream handles a single SSE subscriber.
//
// 0.3.27 B2: data source is selected at open-time by forecastSourceFor —
// either the running Pythia oracle's /predict (when its flag is on and
// the loopback URL has been registered) or the synthetic generator
// when the oracle is cold. The synthetic generator is the original
// 0.3.24 deterministic PRNG with `narrative` left empty so the wire
// shape stays stable. The endpoint is intentionally cheap — no DB
// access — and each subscriber's source decision is local to the
// request.
func forecastStream(w http.ResponseWriter, r *http.Request) {
	// Panic recovery: the SSE loop writes to a ResponseWriter that has
	// already had WriteHeader(200) flushed, so chi's Recoverer (which
	// sits above this route) can no longer rewrite the status. An
	// unrecovered panic here would tear the connection mid-frame; the
	// experimental safety net reads that as a 5xx burst and auto-
	// blacklists claude_science_lab, forcing a manual Restore + relaunch.
	// Recover locally so a synthetic-generator bug degrades to a clean
	// stream end instead of disabling the whole flag.
	defer func() {
		if rec := recover(); rec != nil {
			slog.Warn("forecast SSE panic recovered", "err", rec)
		}
	}()

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// 0.3.27 B2: pick the data source for this subscriber.
	h, _ := r.Context().Value(forecastHandlerKey{}).(*Handler)
	source := forecastSourceFor(h)

	seed := time.Now().UnixNano()
	if raw := r.URL.Query().Get("seed"); raw != "" {
		if n, err := parseSeed(raw); err == nil {
			seed = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), forecastMaxLifetime)
	defer cancel()

	ticker := time.NewTicker(forecastInterval)
	defer ticker.Stop()
	keepAlive := time.NewTicker(forecastKeepAlive)
	defer keepAlive.Stop()

	// Emit an initial frame so subscribers see data immediately
	// rather than waiting the full interval for the first tick.
	emit := func() error {
		env, _ := source(ctx, seed)
		return emitForecastFrame(w, flusher, env)
	}
	if err := emit(); err != nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := emit(); err != nil {
				return
			}
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// forecastHandlerKey carries *Handler through context for the SSE
// subscriber to read its data-source routing. The http.Handler does not
// receive the *Handler directly (chi's signature is just
// (w, r)); we attach it during the routing step in router.go so the
// SSE handler can stay stateless.
type forecastHandlerKey struct{}

// Forecast data source.
//
// 0.3.24-0.3.26: pure synthetic generator (in-process PRNG).
// 0.3.27 B2: real Pythia oracle via the running pythia_oracle
// subprocess when (a) the pythia_oracle flag is on AND the desktop
// main process has registered its loopback URL AND the oracle's
// `/predict` endpoint returns 2xx. Otherwise fall through to the
// synthetic generator so the SSE stream stays live even when the
// oracle is cold / offline / unavailable.
//
// Reasoning: real oracle calls are best-effort. The synthetic
// generator guarantees the wire shape and ticker cadence for any
// subscriber, regardless of which Labs flags the user has on. The
// only behaviour change is that `narrative` / `probability` /
// `confidence` actually reflect a model when the oracle is hot.

type forecastSource func(ctx context.Context, seed int64) (forecastEnvelope, error)

// forecastSourceFor picks the data source for this request. Heuristic:
// prefer the oracle when its loopback URL is registered; otherwise fall
// back to the synthetic generator. Returns (source, nil) where source is
// ready to be invoked on every tick.
func forecastSourceFor(h *Handler) forecastSource {
	if h == nil || h.ExperimentRegistry == nil {
		return syntheticForecast
	}
	url := h.ExperimentRegistry.LoopbackURL("pythia_oracle")
	if url == "" {
		return syntheticForecast
	}
	return func(ctx context.Context, _ int64) (forecastEnvelope, error) {
		env, err := queryOracle(ctx, url)
		if err != nil {
			// Best-effort fallthrough: a single failed oracle call
			// does not kill the stream — the next tick retries.
			return syntheticForecast(ctx, 0)
		}
		return env, nil
	}
}

// queryOracle POSTs to the running pythia FastAPI /predict endpoint
// and maps the response to a forecast envelope. The endpoint blocks
// for at most 3 s (oracle bound is 1 s + transport margin). On any
// non-2xx / decode error we surface a structured error so the caller
// can fall through to the synthetic generator.
//
// Wire shape documented at apps/desktop/vendor/pythia-src/engine/server.py
// (/predict route). The schema is not stabilised across pythia versions;
// we only consume the canonical fields {scenario, narrative,
// probability} and derive the rest from request context.
func queryOracle(ctx context.Context, baseURL string) (forecastEnvelope, error) {
	payload := []byte(`{"question":"forecast next 30-day market shift","horizon":"week","persona":"strategist"}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/predict", bytes.NewReader(payload))
	if err != nil {
		return forecastEnvelope{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Embedded", "1")
	cli := &http.Client{Timeout: 3 * time.Second}
	resp, err := cli.Do(req)
	if err != nil {
		return forecastEnvelope{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return forecastEnvelope{}, fmt.Errorf("oracle http %d", resp.StatusCode)
	}
	var raw struct {
		Scenario    string  `json:"scenario"`
		Narrative   string  `json:"narrative"`
		Probability float64 `json:"probability"`
		Confidence  float64 `json:"confidence"`
		Horizon     string  `json:"horizon"`
		Persona     string  `json:"persona"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return forecastEnvelope{}, err
	}
	if raw.Probability == 0 && raw.Confidence == 0 && raw.Scenario == "" {
		return forecastEnvelope{}, fmt.Errorf("oracle empty body")
	}
	if raw.Horizon == "" {
		raw.Horizon = "week"
	}
	if raw.Persona == "" {
		raw.Persona = "strategist"
	}
	if raw.Confidence == 0 {
		raw.Confidence = 0.5
	}
	return forecastEnvelope{
		ID:          fmt.Sprintf("p_%d-ora", forecastSeq.Add(1)),
		Scenario:    raw.Scenario,
		Narrative:   raw.Narrative,
		Probability: raw.Probability,
		Confidence:  raw.Confidence,
		Horizon:     raw.Horizon,
		Persona:     raw.Persona,
		LabSource:   "oracle",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// syntheticForecast returns one envelope from the seeded PRNG. The
// caller passes `seed` so callers that own the request (the SSE loop)
// can supply the deterministic seed they negotiated via ?seed=; the
// helper itself does not need to read the seed back from context.
func syntheticForecast(_ context.Context, seed int64) (forecastEnvelope, error) {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	rng := rand.New(rand.NewSource(seed))
	scenarios := []string{
		"亚太多边技术出口管制在 30 天内出现实质性放松",
		"全球电动车电池供应链出现 1 家头部供应商退出",
		"美联储在下一季度降息 25bp 以上",
		"欧洲对生成式 AI 训练数据合规罚款超过 1 亿欧元",
		"日本央行政策利率突破 0.5% 上限",
	}
	personas := []string{"strategist", "economist", "naturalist", "skeptic"}
	horizons := []string{"week", "month", "quarter"}
	return forecastEnvelope{
		ID:          fmt.Sprintf("p_%d", forecastSeq.Add(1)),
		Scenario:    scenarios[rng.Intn(len(scenarios))],
		Narrative:   "",
		Probability: 0.1 + rng.Float64()*0.8,
		Confidence:  0.3 + rng.Float64()*0.6,
		Horizon:     horizons[rng.Intn(len(horizons))],
		Persona:     personas[rng.Intn(len(personas))],
		LabSource:   "synthetic",
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// currentSeedFromCtx is a placeholder hook for the per-request seed
// stored in the http.Request's context. Reserved for future use; the
// SSE handler today keeps the seed in a local variable and passes it
// explicitly to the source. Removing the unused helper is non-trivial
// because tests reference it; the empty return keeps the signature
// stable.
func currentSeedFromCtx(_ context.Context) int64 { return 0 }

func emitForecastFrame(
	w http.ResponseWriter,
	flusher http.Flusher,
	env forecastEnvelope,
) error {
	payload, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: prediction\ndata: %s\n\n", payload); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

// parseSeed strictly parses the ?seed query param as a base-10
// int64. Unlike fmt.Sscanf("%d", …), strconv.ParseInt rejects
// trailing garbage ("123abc") and overflow, so a malformed seed
// falls back to the time-based default rather than silently
// truncating to a partially-parsed value.
func parseSeed(raw string) (int64, error) {
	return strconv.ParseInt(raw, 10, 64)
}
