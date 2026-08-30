package secrets

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Finding is one suspected secret in a pack file. Excerpt is masked — it
// identifies the leak without reproducing it (secrets never appear in
// output, docs/security.md threat 4).
type Finding struct {
	Path    string // slash-separated, relative to the pack root
	Line    int    // 1-based
	Rule    string // e.g. "format:github-token", "assignment", "entropy:high"
	Excerpt string // masked, safe to display
}

// Unanchored cores of the value formats, for scanning free text. Matches
// are boundary-checked on both sides (checkTail is off only where a match
// legitimately ends mid-token) so tokens embedded in longer identifiers
// don't fire — a false positive here hard-blocks save. needDigit guards
// prefixes whose tail alphabet overlaps hyphenated prose ("sk-learn-based-
// pipeline"): real keys essentially always carry a digit.
var scanFormats = []struct {
	rule      string
	re        *regexp.Regexp
	checkTail bool
	needDigit bool
}{
	{"format:private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`), false, false},
	{"format:github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{20,}`), true, false},
	{"format:stripe-key", regexp.MustCompile(`[sr]k_(live|test)_[A-Za-z0-9]{16,}`), true, false},
	{"format:sk-key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`), true, true},
	{"format:slack-token", regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`), true, true},
	{"format:aws-access-key", regexp.MustCompile(`(AKIA|ASIA)[0-9A-Z]{16}`), true, false},
	{"format:gitlab-token", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{16,}`), true, true},
	{"format:npm-token", regexp.MustCompile(`npm_[A-Za-z0-9]{16,}`), true, false},
	{"format:jwt", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]*`), true, false},
	{"format:url-password", regexp.MustCompile(`[A-Za-z][A-Za-z0-9+.-]*://[^/\s:@]+:[^/\s@]+@`), false, false},
}

// assignmentRe extracts key/value pairs from text lines in any of the
// config syntaxes packs bundle (YAML, JSON, TOML, env, shell exports).
var assignmentRe = regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_.-]*)["']?\s*[:=]\s*["']?([^"'\s,;]+)`)

// docPlaceholderRes are documentation stand-ins for a secret value —
// <your-token>, ****, xxxx — which are fine to keep in a pack.
var docPlaceholderRes = []*regexp.Regexp{
	regexp.MustCompile(`^<[^<>]+>$`),
	regexp.MustCompile(`^\*{3,}$`),
	regexp.MustCompile(`^(?i:x{4,})$`),
}

// ScanPack walks every regular file under root (a pack directory) and
// reports suspected secrets. It is layer 3 of the security model: an
// independent re-check after redaction, run by save and validate; findings
// block both. `.git` is skipped; symlinks are not followed; binary files
// are scanned with the format channel only.
func ScanPack(root string) ([]Finding, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("scanning pack: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("scanning pack: %s is not a directory", root)
	}

	var findings []Finding
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // symlinks, devices, sockets
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		findings = append(findings, scanContent(filepath.ToSlash(rel), data)...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].Path != findings[j].Path {
			return findings[i].Path < findings[j].Path
		}
		return findings[i].Line < findings[j].Line
	})
	return findings, nil
}

func scanContent(relPath string, data []byte) []Finding {
	var findings []Finding
	taken := map[int]bool{} // lines that already have a finding

	// Channel 1: known credential formats, boundary-checked.
	content := string(data)
	for _, f := range scanFormats {
		for _, loc := range f.re.FindAllStringIndex(content, -1) {
			if loc[0] > 0 {
				prev, _ := utf8.DecodeLastRuneInString(content[:loc[0]])
				if isWordRune(prev) {
					continue
				}
			}
			if f.checkTail && loc[1] < len(content) {
				next, _ := utf8.DecodeRuneInString(content[loc[1]:])
				if isWordRune(next) {
					continue
				}
			}
			if f.needDigit && !hasDigit(content[loc[0]:loc[1]]) {
				continue
			}
			line := 1 + strings.Count(content[:loc[0]], "\n")
			if taken[line] {
				continue
			}
			taken[line] = true
			findings = append(findings, Finding{
				Path: relPath, Line: line, Rule: f.rule,
				Excerpt: mask(content[loc[0]:loc[1]]),
			})
		}
	}

	// Binary files get the format channel only: assignment and entropy
	// heuristics assume text and would drown in noise.
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return findings
	}

	// Channels 2 and 3 work line by line.
	for i, lineText := range strings.Split(content, "\n") {
		line := i + 1
		if taken[line] {
			continue
		}

		// Channel 2: KEY=value / "key": "value" where the key names a
		// secret and the value looks like a literal credential.
		matched := false
		for _, m := range assignmentRe.FindAllStringSubmatchIndex(lineText, -1) {
			key, value := lineText[m[2]:m[3]], lineText[m[4]:m[5]]
			if _, secretKey := keyLooksSecret(key); !secretKey {
				continue
			}
			// "Authorization: Bearer <token>" — the credential is the token
			// after the auth-scheme word, not the word itself.
			if isAuthScheme(value) {
				rest := strings.TrimLeft(lineText[m[5]:], ` "'`)
				if tok, _, _ := strings.Cut(rest, " "); tok != "" {
					value = strings.Trim(tok, `"',;.`)
				}
			}
			if !assignmentValueSuspicious(value) {
				continue
			}
			taken[line] = true
			findings = append(findings, Finding{
				Path: relPath, Line: line, Rule: "assignment",
				Excerpt: key + "=" + mask(value),
			})
			matched = true
			break
		}
		if matched {
			continue
		}

		// Channel 3: secret-grade entropy only. The uncertain band that
		// save prompts about would be pure noise over prose and docs.
		if entropyLevel(lineText, false) == Secret {
			taken[line] = true
			run := longestRun(lineText)
			findings = append(findings, Finding{
				Path: relPath, Line: line, Rule: "entropy:high",
				Excerpt: mask(run),
			})
		}
	}
	return findings
}

// assignmentValueSuspicious reports whether a value assigned to a
// secret-named key looks like a literal credential rather than a
// placeholder, reference, or prose fragment.
func assignmentValueSuspicious(value string) bool {
	if len(value) < 8 || IsPlaceholder(value) {
		return false
	}
	for _, re := range docPlaceholderRes {
		if re.MatchString(value) {
			return false
		}
	}
	if strings.Contains(value, "://") { // URLs: the url-password format covers real leaks
		return false
	}
	// Credentials essentially always carry a digit, mixed case, or symbols.
	return hasDigit(value) ||
		(strings.ToLower(value) != value && strings.ToUpper(value) != value) ||
		strings.ContainsAny(value, "+/=")
}

// longestRun returns the longest token run in a line, for excerpting the
// value an entropy finding fired on.
func longestRun(lineText string) string {
	best := ""
	for _, run := range tokenRuns(lineText, false) {
		if len(run) > len(best) {
			best = run
		}
	}
	if best == "" {
		return lineText
	}
	return best
}

// mask shortens a matched secret to an identifying, non-reproducible
// excerpt: a quarter of the value, capped at 6 leading characters, so a
// short secret never loses most of itself to its own finding.
func mask(s string) string {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	keep := min(6, n/4)
	if keep == 0 {
		return "…"
	}
	runes := []rune(s)
	return fmt.Sprintf("%s… (%d chars)", string(runes[:keep]), n)
}

func isAuthScheme(s string) bool {
	switch strings.ToLower(s) {
	case "bearer", "basic", "token":
		return true
	}
	return false
}

func isWordRune(r rune) bool {
	return r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}
