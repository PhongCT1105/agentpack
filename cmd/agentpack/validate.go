package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/PhongCT1105/agentpack/internal/packio"
)

func newValidateCmd() *cobra.Command {
	var allowFinding []string
	cmd := &cobra.Command{
		Use:   "validate <dir>",
		Short: "Check a pack directory against the spec and scan it for secrets",
		Long: `Validate checks a pack directory (docs/spec/pack-manifest.md): manifest
schema, component name uniqueness, source shape, bundled paths — and always
runs the whole-pack secret scan. Any issue or a still-blocking finding exits
nonzero, so a pack repository can run this in CI on every commit.

A committed .agentpack-allow file in the pack (docs/spec/pack-manifest.md)
waives reviewable findings automatically — see agentpack save --help.
--allow-finding <path>[:<line>] adds one-off local waivers for this run only.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := args[0]
			allow, err := parseAllowFindings(allowFinding)
			if err != nil {
				return err
			}
			issues, blocking, allowed, err := packio.ValidatePack(dir, allow)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			for _, issue := range issues {
				fmt.Fprintf(out, "issue: %s\n", issue)
			}
			if len(allowed) > 0 {
				fmt.Fprintf(out, "waived %d reviewed finding(s):\n", len(allowed))
				for _, f := range allowed {
					fmt.Fprintf(out, "  %s:%d %s\n", f.Path, f.Line, f.Rule)
				}
			}
			printFindings(out, blocking)
			if len(issues) > 0 || len(blocking) > 0 {
				return fmt.Errorf("pack is invalid: %d issue(s), %d suspected secret(s)", len(issues), len(blocking))
			}
			fmt.Fprintf(out, "pack %s is valid\n", dir)
			return nil
		},
	}
	cmd.Flags().StringArrayVar(&allowFinding, "allow-finding", nil,
		"waive a reviewable secret-scan finding for this run: <path>[:<line>] (repeatable; path ending in / waives a whole directory)")
	return cmd
}
