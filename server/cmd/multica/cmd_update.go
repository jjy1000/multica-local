package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/multica-ai/multica/server/internal/cli"
)

var updateDownloadTimeout time.Duration = cli.DefaultUpdateDownloadTimeout

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update multica to the latest version",
	RunE:  runUpdate,
}

func init() {
	updateCmd.Flags().DurationVar(&updateDownloadTimeout, "download-timeout", cli.DefaultUpdateDownloadTimeout, "Maximum time to wait for the release archive download")
}

// runUpdate is a no-op in the localized build. The command is retained so
// `multica update --help` and any scripted callers don't break, but no
// network calls to GitHub and no brew / download / replace steps execute.
func runUpdate(_ *cobra.Command, _ []string) error {
	fmt.Fprintf(os.Stderr, "Current version: %s (commit: %s, built: %s)\n", version, commit, date)
	fmt.Fprintln(os.Stderr, "Self-update is disabled in this build. Pull a newer binary manually if you need one.")
	_ = updateDownloadTimeout // flag retained for back-compat; ignored in localized build
	return fmt.Errorf("multica update is disabled in this build")
}
