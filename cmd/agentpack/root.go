package main

import (
	"github.com/spf13/cobra"
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agentpack",
		Short: "Package and share agentic development environments without secrets",
		Long: `agentpack scans the AI coding tools installed on a machine (Claude Code,
Codex CLI, Cursor, Gemini CLI), saves their portable configuration — skills,
MCP servers, agents, rules, commands, settings — into a secrets-free pack
directory, and restores packs onto other machines.`,
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: false,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newVersionCmd())
	cmd.AddCommand(newScanCmd(defaultAdapters))
	return cmd
}
