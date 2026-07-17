package main

import (
	"strings"
	"testing"
	"time"
)

func TestRunUpdateIsDisabledInLocalizedBuild(t *testing.T) {
	// Localized build: runUpdate must always return a "disabled" error,
	// regardless of any caller-supplied timeout. The flag is preserved for
	// back-compat so the CLI surface doesn't change, but the function never
	// reaches GitHub.
	for _, v := range []time.Duration{0, -1 * time.Second, 30 * time.Second, 24 * time.Hour} {
		orig := updateDownloadTimeout
		updateDownloadTimeout = v
		t.Cleanup(func() { updateDownloadTimeout = orig })

		err := runUpdate(nil, nil)
		if err == nil || !strings.Contains(err.Error(), "disabled in this build") {
			t.Errorf("runUpdate(timeout=%v) error = %v, want disabled-in-build error", v, err)
		}
	}
}

func TestUpdateCommandRegistersDownloadTimeoutFlag(t *testing.T) {
	flag := updateCmd.Flags().Lookup("download-timeout")
	if flag == nil {
		t.Fatal("updateCmd is missing --download-timeout")
	}
	if got := flag.DefValue; got != (120 * time.Second).String() {
		t.Fatalf("--download-timeout default = %q, want %q", got, (120 * time.Second).String())
	}
}
