package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLLMForecast_EnvMissing_ReturnsError(t *testing.T) {
	t.Setenv("MULTICA_AGENT_RUNTIME_URL", "")
	t.Setenv("MULTICA_API_TOKEN", "")
	_, err := llmForecast(context.Background(), "strategist", "week", "test", 42)
	if err == nil {
		t.Fatal("expected error when env unset")
	}
}

func TestLLMForecast_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("auth header wrong: %s", r.Header.Get("Authorization"))
		}
		if r.Header.Get("X-Experiment-Source") != "claude-lab-forecast" {
			t.Errorf("source header wrong: %s", r.Header.Get("X-Experiment-Source"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"scenario":"test","narrative":"n","probability":0.5,"confidence":0.7,"horizon":"week","persona":"strategist"}`))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_AGENT_RUNTIME_URL", srv.URL)
	t.Setenv("MULTICA_API_TOKEN", "test-token")
	env, err := llmForecast(context.Background(), "strategist", "week", "scenario", 42)
	if err != nil {
		t.Fatalf("expected ok, got %v", err)
	}
	if env.OracleSource != "llm" {
		t.Errorf("expected oracle_source=llm, got %s", env.OracleSource)
	}
	if env.Scenario != "test" {
		t.Errorf("scenario not propagated: %s", env.Scenario)
	}
}

func TestLLMForecast_HTTP500_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("upstream down"))
	}))
	defer srv.Close()
	t.Setenv("MULTICA_AGENT_RUNTIME_URL", srv.URL)
	t.Setenv("MULTICA_API_TOKEN", "tok")
	_, err := llmForecast(context.Background(), "p", "h", "s", 1)
	if err == nil {
		t.Fatal("expected error on 500")
	}
}
