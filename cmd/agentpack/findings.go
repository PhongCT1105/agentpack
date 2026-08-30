package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/PhongCT1105/agentpack/internal/secrets"
)

// parseAllowFindings turns repeated --allow-finding flag values into the
// review flow's allowlist (internal/secrets, docs/backlog.md P2.9).
func parseAllowFindings(raw []string) ([]secrets.AllowEntry, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	entries := make([]secrets.AllowEntry, 0, len(raw))
	for _, r := range raw {
		e, err := secrets.ParseAllowEntry(r)
		if err != nil {
			return nil, fmt.Errorf("--allow-finding %q: %w", r, err)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// printFindings reports secret-scan findings still blocking, separating
// always-blocking matches (known token formats, or any match in a
// config-shaped file) from reviewable ones (assignment/entropy matches in
// bundled source, docs, or test fixtures — common false-positive sources,
// docs/security.md layer 3) so the reader knows which need a source fix
// and which can be waived with --allow-finding after inspection.
func printFindings(w io.Writer, findings []secrets.Finding) {
	var blocking, reviewable []secrets.Finding
	for _, f := range findings {
		if f.Reviewable {
			reviewable = append(reviewable, f)
		} else {
			blocking = append(blocking, f)
		}
	}
	if len(blocking) > 0 {
		fmt.Fprintln(w, "suspected secrets (always blocking, cannot be waived):")
		for _, f := range blocking {
			fmt.Fprintf(w, "  %s:%d %s %s\n", f.Path, f.Line, f.Rule, f.Excerpt)
		}
	}
	if len(reviewable) > 0 {
		fmt.Fprintf(w, "%d finding(s) need review (likely false positives in source/docs/test content):\n", len(reviewable))
		for _, f := range reviewable {
			fmt.Fprintf(w, "  %s:%d %s %s\n", f.Path, f.Line, f.Rule, f.Excerpt)
		}
	}
}

// printReviewableSummary reports heuristic matches in docs, source, test and
// lockfile content. These do not block a save (see packio.WritePack): a
// single vendored third-party skill can produce thousands of them, and a
// gate that fails on that is a gate people switch off. They are summarized
// by file rather than listed line by line, because the useful question is
// "which files should I look at", not "here are 1400 lines".
func printReviewableSummary(w io.Writer, findings []secrets.Finding) {
	byFile := map[string]int{}
	for _, f := range findings {
		byFile[f.Path]++
	}
	files := make([]string, 0, len(byFile))
	for p := range byFile {
		files = append(files, p)
	}
	sort.Slice(files, func(i, j int) bool {
		if byFile[files[i]] != byFile[files[j]] {
			return byFile[files[i]] > byFile[files[j]]
		}
		return files[i] < files[j]
	})

	fmt.Fprintf(w, "%d reviewable finding(s) in %d bundled file(s) — not blocking; these shapes are common in docs, source, tests and lockfiles:\n",
		len(findings), len(files))
	const maxFiles = 10
	for i, p := range files {
		if i == maxFiles {
			fmt.Fprintf(w, "  ... and %d more file(s)\n", len(files)-maxFiles)
			break
		}
		fmt.Fprintf(w, "  %s (%d)\n", p, byFile[p])
	}
	fmt.Fprintln(w, "  review them, or rerun with --strict to treat them as blocking")
}
