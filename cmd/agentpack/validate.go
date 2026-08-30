package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PhongCT1105/agentpack/internal/packio"
)

func newValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <dir>",
		Short: "Check a pack directory against the spec and scan it for secrets",
		Long: `Validate checks a pack directory (docs/spec/pack-manifest.md): manifest
schema, component name uniqueness, source shape, bundled paths — and always
runs the whole-pack secret scan. Any issue or finding exits nonzero, so a
pack repository can run this in CI on every commit.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			issues, findings, err := packio.ValidatePack(dir)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, issue := range issues {
				fmt.Fprintf(out, "issue: %s\n", issue)
			}
			for _, f := range findings {
				fmt.Fprintf(out, "suspected secret: %s:%d %s %s\n", f.Path, f.Line, f.Rule, f.Excerpt)
			}
			if len(issues) > 0 || len(findings) > 0 {
				return fmt.Errorf("pack is invalid: %d issue(s), %d suspected secret(s)", len(issues), len(findings))
			}
			fmt.Fprintf(out, "pack %s is valid\n", dir)
			return nil
		},
	}
}
