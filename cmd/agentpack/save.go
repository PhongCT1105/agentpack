package main

import (
	"bufio"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/packio"
	"github.com/PhongCT1105/agentpack/internal/secrets"
)

func newSaveCmd(adapters func() []engine.Adapter) *cobra.Command {
	var (
		all             bool
		name            string
		projectDir      string
		reviewUncertain bool
		allowFinding    []string
		strict          bool
	)
	cmd := &cobra.Command{
		Use:   "save <dir>",
		Short: "Save the scanned environment as a secrets-free pack directory",
		Long: `Save scans the installed tools and writes their portable components into
a pack directory (docs/spec/pack-manifest.md).

Every env var, header, and settings value passes the secrets redactor:
secret values become credential requirements — the pack stores what is
needed, never the value. After writing, an independent whole-pack scan
re-checks every file; findings remove the pack and fail the save.

Personal files (CLAUDE.local.md, settings.local.json) are never saved.
Values the redactor is uncertain about (the SUPABASE_URL problem) are
redacted by default; pass --review-uncertain to decide each one — the
default answer still redacts, so an unattended run stays safe.

Some scan findings are reviewable: assignment- and entropy-shaped matches
in bundled source, docs, or test fixtures (JSX props, prose examples,
seeded fixture data) are common false positives, distinct from a
known-format token match which always blocks. After inspecting a
reviewable finding, pass --allow-finding <path>[:<line>] (repeatable) to
waive it; a path ending in "/" waives every file under it. Waived findings
are written to .agentpack-allow in the pack so a later validate run (e.g.
in CI) does not need --allow-finding repeated.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !all {
				return errors.New("interactive component selection is not implemented yet; pass --all to save every portable component")
			}
			dir := args[0]
			packName := name
			if packName == "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return err
				}
				packName = packio.Slugify(filepath.Base(abs))
				if packName == "" {
					return fmt.Errorf("cannot derive a pack name from directory %q; pass --name", dir)
				}
			}
			allow, err := parseAllowFindings(allowFinding)
			if err != nil {
				return err
			}
			return runSaveAll(cmd, adapters(), dir, packName, projectDir, reviewUncertain, allow, strict)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "save every portable component without prompting")
	cmd.Flags().StringVar(&name, "name", "", "pack name (default: derived from the target directory)")
	cmd.Flags().StringVar(&projectDir, "project", defaultProjectDir(),
		"project directory to scan for project-scoped components (empty to skip)")
	cmd.Flags().BoolVar(&reviewUncertain, "review-uncertain", false,
		"prompt for each value the redactor is uncertain about (default answer redacts)")
	cmd.Flags().BoolVar(&strict, "strict", false,
		"treat reviewable findings (heuristic matches in docs, source, tests and lockfiles) as blocking")
	cmd.Flags().StringArrayVar(&allowFinding, "allow-finding", nil,
		"waive a reviewable secret-scan finding: <path>[:<line>] (repeatable; path ending in / waives a whole directory)")
	return cmd
}

func runSaveAll(cmd *cobra.Command, adapters []engine.Adapter, dir, name, projectDir string, reviewUncertain bool, allow []secrets.AllowEntry, strict bool) error {
	out := cmd.OutOrStdout()
	scope := model.ScanScope{Global: true, ProjectDir: projectDir}
	results := engine.ScanAll(adapters, scope)

	var invs []model.Inventory
	var skippedPersonal []string
	var scanErrs []string
	for _, res := range results {
		if res.Err != nil {
			scanErrs = append(scanErrs, fmt.Sprintf("%s: %v", res.Tool, res.Err))
		}
		if !res.Installed {
			continue
		}
		inv, skipped := filterPersonal(res.Inventory)
		skippedPersonal = append(skippedPersonal, skipped...)
		fmt.Fprintf(out, "scanned %s: %d component(s)\n", res.Tool, len(inv.Components))
		invs = append(invs, inv)
	}
	// A failed scan means an incomplete inventory; a silently partial pack
	// is worse than no pack.
	if len(scanErrs) > 0 {
		return fmt.Errorf("refusing to save a partial pack, scanning failed:\n  %s", strings.Join(scanErrs, "\n  "))
	}
	if len(skippedPersonal) > 0 {
		fmt.Fprintf(out, "skipped personal component(s), never saved: %s\n", strings.Join(skippedPersonal, ", "))
	}
	total := 0
	for _, inv := range invs {
		total += len(inv.Components)
	}
	if total == 0 {
		if n := len(skippedPersonal); n > 0 {
			return fmt.Errorf("no portable components to save (%d personal component(s) were filtered out)", n)
		}
		return errors.New("no portable components found on this machine; nothing to save")
	}

	opts := packio.ConvertOptions{Name: name}
	if reviewUncertain {
		opts.TreatUncertainAsSecret = newUncertainPrompter(cmd)
	}
	res, err := packio.Convert(invs, opts)
	if err != nil {
		return err
	}

	if len(res.Redactions) > 0 {
		fmt.Fprintln(out, "redacted (values are never stored in a pack):")
		uncertain := false
		for _, r := range res.Redactions {
			fmt.Fprintf(out, "  %s: %s — %s\n", r.Component, r.Key, r.Verdict.Reason)
			if r.Verdict.Level == secrets.Uncertain {
				uncertain = true
			}
		}
		if uncertain && !reviewUncertain {
			fmt.Fprintln(out, "  (uncertain values were redacted by default; rerun with --review-uncertain to decide each one)")
		}
	}
	blocking, reviewable, allowed, err := packio.WritePack(dir, res, allow, strict)
	// Printed after the write: bundling records what it refused to copy
	// (vendored dependencies, VCS metadata, dotenv files) as warnings.
	for _, w := range res.Warnings {
		fmt.Fprintf(out, "warning: %s\n", w)
	}
	if len(reviewable) > 0 {
		printReviewableSummary(out, reviewable)
	}
	if len(allowed) > 0 {
		fmt.Fprintf(out, "waived %d reviewed finding(s) (recorded in %s):\n", len(allowed), packio.AllowlistFilename)
		for _, f := range allowed {
			fmt.Fprintf(out, "  %s:%d %s\n", f.Path, f.Line, f.Rule)
		}
	}
	if len(blocking) > 0 {
		printFindings(cmd.ErrOrStderr(), blocking)
		fmt.Fprintln(cmd.ErrOrStderr(), "fix the source files above, or if a reviewable finding is a false positive, retry with --allow-finding <path>[:<line>]; nothing was saved")
	}
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "wrote pack %q (%d component(s)) to %s\n", name, countComponents(res.Manifest), dir)
	return nil
}

// newUncertainPrompter returns a TreatUncertainAsSecret callback that asks
// the user about each uncertain value. Only uncertain values are ever
// displayed — showing the value is the point of the review, and it is the
// user's own terminal — while confirmed secrets redact without being
// echoed. Empty answers, EOF, and read errors all default to redacting, so
// a piped or unattended run can never keep a value by accident.
func newUncertainPrompter(cmd *cobra.Command) func(ref, key, value string, v secrets.Verdict) bool {
	reader := bufio.NewReader(cmd.InOrStdin())
	// Prompts go to stderr: with stdout redirected to a file the question
	// stays visible, and the displayed values stay out of the redirect.
	out := cmd.ErrOrStderr()
	return func(ref, key, value string, v secrets.Verdict) bool {
		fmt.Fprintf(out, "uncertain value in %s — %s\n", ref, key)
		fmt.Fprintf(out, "  value:  %s\n", value)
		fmt.Fprintf(out, "  reason: %s\n", v.Reason)
		for {
			// "Redact from the pack" is accurate for both outcomes: env and
			// header values become credential requirements, settings values
			// are dropped (settings have no credentials field).
			fmt.Fprint(out, "redact this value from the pack? [Y/n] ")
			line, err := reader.ReadString('\n')
			answer := strings.ToLower(strings.TrimSpace(line))
			switch {
			case answer == "" || answer == "y" || answer == "yes":
				return true
			case answer == "n" || answer == "no":
				fmt.Fprintf(out, "keeping %s as a plain value\n", key)
				return false
			}
			if err != nil { // EOF or read failure: safe default
				return true
			}
			fmt.Fprintln(out, "please answer y or n")
		}
	}
}

// filterPersonal removes components that live in personal, gitignored
// files — they are the user's private overlay, never pack content. The
// ".local." naming convention exists only for rule and settings files
// (CLAUDE.local.md, settings.local.json); other kinds keep their names.
func filterPersonal(inv model.Inventory) (model.Inventory, []string) {
	var skipped []string
	kept := inv
	kept.Components = nil
	for _, c := range inv.Components {
		personal := (c.Kind() == model.KindRule || c.Kind() == model.KindSetting) &&
			strings.Contains(strings.ToLower(c.Name()), ".local.")
		if personal {
			skipped = append(skipped, fmt.Sprintf("%s/%s", c.Kind(), c.Name()))
			continue
		}
		kept.Components = append(kept.Components, c)
	}
	return kept, skipped
}

func countComponents(m *packio.Manifest) int {
	return len(m.Components.Skills) + len(m.Components.MCPServers) +
		len(m.Components.Agents) + len(m.Components.Rules) +
		len(m.Components.Commands) + len(m.Components.Settings)
}
