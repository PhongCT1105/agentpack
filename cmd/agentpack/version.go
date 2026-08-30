package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Set at build time via -ldflags; goreleaser overrides all three.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print agentpack version information",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "agentpack %s\n  commit: %s\n  built:  %s\n", version, commit, date)
		},
	}
}
