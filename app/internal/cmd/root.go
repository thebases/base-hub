package cmd

// File overview:
// - Responsibility: defines the root CLI command and the shared flags that backend subcommands inherit.
// - Key symbols: `Execute`.
// - Pattern: Concentrates startup ordering here so dependency initialization, embedded assets, and shutdown hooks stay predictable during deploys and local runs.

import (
	"os"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:           "base-hub",
	Short:         "BASE HUB CLI",
	Long:          "A command-line tool to manage the BASE HUB application, including configuration, database migrations, and other utilities.",
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Error(err)
		os.Exit(1)
	}
}
