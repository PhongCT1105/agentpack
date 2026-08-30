package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// AllowEntry identifies findings a human has reviewed and confirmed are not
// secrets — the review flow for Reviewable findings (docs/security.md
// layer 3, docs/backlog.md P2.9). It is parsed from a .agentpack-allow
// file or a --allow-finding flag value.
type AllowEntry struct {
	Path string // slash-separated; a trailing "/" allows every file under it
	Line int    // 0 means "every line in Path"
}

// ParseAllowEntry parses one "<path>[:<line>]" entry. A path ending in "/"
// allows every file under that directory (line numbers make no sense
// there, and are rejected by construction — there is no ":" to parse).
func ParseAllowEntry(raw string) (AllowEntry, error) {
	s := strings.TrimSpace(raw)
	if s == "" || strings.HasPrefix(s, "#") {
		return AllowEntry{}, fmt.Errorf("empty or comment allowlist entry %q", raw)
	}
	if strings.HasSuffix(s, "/") {
		return AllowEntry{Path: s}, nil
	}
	p, lineStr, hasLine := strings.Cut(s, ":")
	if p == "" {
		return AllowEntry{}, fmt.Errorf("allowlist entry %q has no path", raw)
	}
	if !hasLine {
		return AllowEntry{Path: p}, nil
	}
	line, err := strconv.Atoi(lineStr)
	if err != nil || line <= 0 {
		return AllowEntry{}, fmt.Errorf("allowlist entry %q has an invalid line number", raw)
	}
	return AllowEntry{Path: p, Line: line}, nil
}

// ParseAllowlist parses a .agentpack-allow file: one entry per line, blank
// lines and lines starting with "#" ignored, an inline " # comment" suffix
// stripped before parsing.
func ParseAllowlist(data []byte) ([]AllowEntry, error) {
	var entries []AllowEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		e, err := ParseAllowEntry(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// FormatAllowlist renders entries as a .agentpack-allow file, sorted and
// deduplicated for a stable, reviewable diff.
func FormatAllowlist(entries []AllowEntry) []byte {
	sorted := append([]AllowEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Line < sorted[j].Line
	})

	var b bytes.Buffer
	b.WriteString("# .agentpack-allow — findings reviewed and confirmed not to be secrets.\n")
	b.WriteString("# One entry per line: <path>[:<line>] (no line = every line in that file);\n")
	b.WriteString("# a path ending in \"/\" allows every file under that directory.\n")
	seen := map[AllowEntry]bool{}
	for _, e := range sorted {
		if seen[e] {
			continue
		}
		seen[e] = true
		if e.Line == 0 {
			fmt.Fprintf(&b, "%s\n", e.Path)
		} else {
			fmt.Fprintf(&b, "%s:%d\n", e.Path, e.Line)
		}
	}
	return b.Bytes()
}

// matches reports whether entry e waives finding f.
func (e AllowEntry) matches(f Finding) bool {
	if strings.HasSuffix(e.Path, "/") {
		return strings.HasPrefix(f.Path, e.Path)
	}
	if f.Path != e.Path {
		return false
	}
	return e.Line == 0 || e.Line == f.Line
}

// FilterAllowed splits findings into what still blocks (remaining) and
// what a reviewer has explicitly waived (allowed). Only findings with
// Reviewable == true can ever be matched: an allowlist entry can silence
// false-positive noise, never a high-confidence match, so it cannot be
// used to smuggle a real secret through review. usedEntries and
// unusedEntries partition allow for hygiene reporting (a stale entry that
// matched nothing, or matched only non-reviewable findings, is worth
// flagging to the caller).
func FilterAllowed(findings []Finding, allow []AllowEntry) (remaining, allowed []Finding, usedEntries, unusedEntries []AllowEntry) {
	used := make([]bool, len(allow))
	for _, f := range findings {
		waived := false
		if f.Reviewable {
			for i, e := range allow {
				if e.matches(f) {
					used[i] = true
					waived = true
				}
			}
		}
		if waived {
			allowed = append(allowed, f)
		} else {
			remaining = append(remaining, f)
		}
	}
	for i, e := range allow {
		if used[i] {
			usedEntries = append(usedEntries, e)
		} else {
			unusedEntries = append(unusedEntries, e)
		}
	}
	return remaining, allowed, usedEntries, unusedEntries
}
