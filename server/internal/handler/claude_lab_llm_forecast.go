package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// ForecastEnvelope mirrors the shape returned by syntheticForecast
// in claude_lab_forecast.go. New oracle_source field is additive
// (omitempty) so wire format stays backwards-compatible.
type ForecastEnvelope struct {
	Scenario                string  `json:"scenario"`
	Narrative               string  `json:"narrative"`
	Probability             float64 `json:"probability"`
	Confidence              float64 `json:"confidence"`
	Horizon                 string  `json:"horizon"`
	Persona                 string  `json:"persona"`
	OracleSource            string  `json:"oracle_source,omitempty"` // "llm" | "synthetic"
	SyntheticOracleFailover bool    `json:"synthetic_oracle_failover,omitempty"`
}

// llmForecast calls the Multica runtime bridge for a real LLM forecast.
// Returns an error if MULTICA_AGENT_RUNTIME_URL is unset, the HTTP call
// fails, or the response body is not parseable as ForecastEnvelope.
//
// The SSE loop in claude_lab_forecast.go (next PR) will treat a non-nil
// error as the trigger to fall back to syntheticForecast + set
// SyntheticOracleFailover=true + OracleSource="synthetic".
func llmForecast(ctx context.Context, persona, horizon, scenarioContext string, seed int64) (ForecastEnvelope, error) {
	runtimeURL := os.Getenv("MULTICA_AGENT_RUNTIME_URL")
	token := os.Getenv("MULTICA_API_TOKEN")
	if runtimeURL == "" || token == "" {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: MULTICA_AGENT_RUNTIME_URL or MULTICA_API_TOKEN not set")
	}
	payload, _ := json.Marshal(map[string]any{
		"prompt":      fmt.Sprintf("persona=%s horizon=%s scenario=%s seed=%d", persona, horizon, scenarioContext, seed),
		"json_schema": "ForecastEnvelope",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runtimeURL+"/api/runtime/llm-call", bytes.NewReader(payload))
	if err != nil {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Experiment-Source", "claude-lab-forecast")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: http do: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: status %d: %s", resp.StatusCode, body)
	}
	var env ForecastEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return ForecastEnvelope{}, fmt.Errorf("llmForecast: unmarshal: %w", err)
	}
	env.OracleSource = "llm"
	return env, nil
}
