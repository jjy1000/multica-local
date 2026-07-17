package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// ---------------------------------------------------------------------------
// pythia commands — experimental gateway to the bundled Pythia prediction
// oracle. The desktop's pythia-manager brings the service up on a free
// loopback port and exposes the URL via the pythia_oracle Labs flag.
// This command set is the agent-facing CLI bridge: the agent shells out
// to `multica pythia <verb>` and the verb proxies an HTTP call to the
// running manager. We do NOT talk to a Multica API endpoint here — the
// manager is a separate process. The wire contract is documented in the
// Skill at server/internal/service/builtin_skills/multica-pythia/SKILL.md.
//
// When the pythia_oracle flag is OFF or the manager is not running, the
// status verb reports the absence so the agent can stop. Every other verb
// hard-fails with an actionable error pointing the user at the Labs tab.
// ---------------------------------------------------------------------------

var pythiaCmd = &cobra.Command{
	Use:   "pythia",
	Short: "Talk to the local Pythia prediction oracle (experimental)",
}

var pythiaStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether the Pythia subprocess is running and on what URL",
	RunE:  runPythiaStatus,
}

var pythiaBriefCmd = &cobra.Command{
	Use:   "brief",
	Short: "Fetch the latest world brief from Pythia",
	RunE:  runPythiaBrief,
}

var pythiaPredictCmd = &cobra.Command{
	Use:   "predict",
	Short: "Run a forecast for a scenario at a given horizon",
	RunE:  runPythiaPredict,
}

var pythiaWhatifCmd = &cobra.Command{
	Use:   "whatif",
	Short: "Counterfactual: what happens when a specific intervention is injected",
	RunE:  runPythiaWhatif,
}

func init() {
	pythiaCmd.AddCommand(pythiaStatusCmd)
	pythiaCmd.AddCommand(pythiaBriefCmd)
	pythiaCmd.AddCommand(pythiaPredictCmd)
	pythiaCmd.AddCommand(pythiaWhatifCmd)

	for _, c := range []*cobra.Command{pythiaBriefCmd, pythiaPredictCmd, pythiaWhatifCmd} {
		c.Flags().String("url", "", "Pythia loopback URL (run `multica pythia status` first)")
		c.Flags().String("output", "json", "Output format: json (default) or plain")
	}
	pythiaPredictCmd.Flags().String("scenario", "", "Scenario text (required)")
	pythiaPredictCmd.Flags().String("horizon", "week", "Forecast horizon: 24h, week, month, year")
	pythiaWhatifCmd.Flags().String("intervention", "", "Counterfactual intervention (required)")
	pythiaStatusCmd.Flags().String("output", "json", "Output format: json (default) or plain")

	// Pythia is experimental; surface it under a dedicated group so
	// `multica --help` keeps core commands unobstructed.
	pythiaCmd.GroupID = groupExperimental
}

// groupExperimental clusters experimental commands. Defined here as a
// const so main.go can co-register it without a circular import.
const groupExperimental = "experimental"

func runPythiaStatus(cmd *cobra.Command, _ []string) error {
	// The CLI cannot introspect the desktop-managed subprocess — that
	// is by design (the manager is local to the user's machine; the
	// CLI runs server-side through the Multica server). We output a
	// JSON envelope the agent can inspect: status="unknown", url=null,
	// hint="not available from CLI; check Labs status via the desktop".
	//
	// This shape lets the agent bail gracefully when invoked outside
	// the desktop session.
	out, _ := cmd.Flags().GetString("output")
	status := map[string]any{
		"status": "unknown",
		"url":    nil,
		"hint":   "pythia status is not exposed via the server CLI; the desktop's pythia-manager owns the subprocess lifecycle",
	}
	if out == "json" {
		return writeJSON(status)
	}
	fmt.Fprintf(os.Stdout, "status=%s url=<unset> hint=%s\n", status["status"], status["hint"])
	return nil
}

// pythiaHTTPClient fetches a JSON resource from the loopback URL the
// desktop installed. Single-purpose so any loopback call is centralized
// here; no global client, no Multica API auth — Pythia sits behind
// 127.0.0.1 and trusts the local user.
func pythiaHTTPGet(ctx context.Context, url string, path string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pythia unreachable at %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pythia response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("pythia %s -> %d: %s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode pythia response: %w", err)
	}
	return nil
}

func pythiaHTTPPost(ctx context.Context, url string, path string, payload any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode pythia payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("pythia unreachable at %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read pythia response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("pythia %s -> %d: %s", path, resp.StatusCode, string(body))
	}
	if out != nil {
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode pythia response: %w", err)
		}
	}
	return nil
}

func runPythiaBrief(cmd *cobra.Command, _ []string) error {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		return fmt.Errorf("--url is required (run `multica pythia status` first; the agent must already have the loopback URL from the desktop)")
	}
	var resp map[string]any
	if err := pythiaHTTPGet(cmd.Context(), url, "/brief", &resp); err != nil {
		return err
	}
	return writeJSON(resp)
}

func runPythiaPredict(cmd *cobra.Command, _ []string) error {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		return fmt.Errorf("--url is required")
	}
	scenario, _ := cmd.Flags().GetString("scenario")
	horizon, _ := cmd.Flags().GetString("horizon")
	if scenario == "" {
		return fmt.Errorf("--scenario is required")
	}
	var resp map[string]any
	if err := pythiaHTTPPost(cmd.Context(), url, "/predict", map[string]any{
		"scenario": scenario,
		"horizon":  horizon,
	}, &resp); err != nil {
		return err
	}
	return writeJSON(resp)
}

func runPythiaWhatif(cmd *cobra.Command, _ []string) error {
	url, _ := cmd.Flags().GetString("url")
	if url == "" {
		return fmt.Errorf("--url is required")
	}
	intervention, _ := cmd.Flags().GetString("intervention")
	if intervention == "" {
		return fmt.Errorf("--intervention is required")
	}
	var resp map[string]any
	if err := pythiaHTTPPost(cmd.Context(), url, "/whatif", map[string]any{
		"intervention": intervention,
	}, &resp); err != nil {
		return err
	}
	return writeJSON(resp)
}

// writeJSON centralizes JSON output formatting for pythia subcommands.
// Falls back to fmt.Printf on encoding error so the agent still sees a
// human-readable line instead of nothing.
func writeJSON(v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stdout, "%+v\n", v)
		return nil
	}
	fmt.Fprintln(os.Stdout, string(buf))
	return nil
}
