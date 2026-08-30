package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The TestReleaseBlocking_* naming is a release gate (docs/architecture.md):
// these tests always run with `go test ./...`, and CI/release additionally
// runs `go test -run TestReleaseBlocking ./...` so a secret-leak regression
// can never ship. They must never be skipped.

// leakyFinding describes one seeded leak the scanner must report.
type leakyFinding struct {
	path string
	line int
	rule string
	// secret is the full seeded value (fake, per docs/security.md) that
	// must never appear in the finding's excerpt.
	secret string
}

var leakyFindings = []leakyFinding{
	{"agentpack.yaml", 11, "format:github-token", "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"},
	{"skills/exfil/SKILL.md", 3, "format:sk-key", "sk-FAKE1FAKE2FAKE3FAKE4FAKE"},
	{"config/headers.txt", 3, "assignment", "FAKE567890abcdef567890abcdef567890abcdef"},
	{"rules/CLAUDE.md", 3, "assignment", "FAKEwJx7+Qm2Lp9ZrTv4/Kd8HnBs3YcG"},
	{"prompts/deploy.md", 3, "format:jwt", "eyJhbGciOiJGQUtFIn0.eyJGQUtFIjoiRkFLRSJ9.FAKEFAKEFAKEFAKE"},
	{"creds/id_rsa", 1, "format:private-key", "-----BEGIN OPENSSH PRIVATE KEY-----"},
	{"config/settings.json", 2, "assignment", "FAKE0q7pz2mk9vlt4wyb"},
	{"config/mcp.json", 2, "format:url-password", "postgres://app:FAKEpass@db.internal:5432/app"},
	{"notes.md", 3, "entropy:high", "9f8e7d6c5b4a39281706f5e4d3c2b1a09f8e7d6c5b4a39281706f5e4d3c2b1a0"},
}

func TestReleaseBlocking_LeakyPackFindings(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "leaky"))
	if err != nil {
		t.Fatalf("ScanPack(leaky) error: %v", err)
	}
	for _, want := range leakyFindings {
		found := false
		for _, f := range got {
			if f.Path == want.path && f.Line == want.line && f.Rule == want.rule {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ScanPack(leaky) missed seeded leak %s:%d rule %q\ngot: %+v", want.path, want.line, want.rule, got)
		}
	}
	// Per-line dedupe: exactly one finding per seeded line, none elsewhere.
	if len(got) != len(leakyFindings) {
		t.Errorf("ScanPack(leaky) = %d findings, want %d:\n%+v", len(got), len(leakyFindings), got)
	}
}

func TestReleaseBlocking_CleanPackHasNoFindings(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "clean"))
	if err != nil {
		t.Fatalf("ScanPack(clean) error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanPack(clean) = %d findings, want 0:\n%+v", len(got), got)
	}
}

func TestReleaseBlocking_FindingsNeverEchoTheSecret(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "leaky"))
	if err != nil {
		t.Fatalf("ScanPack(leaky) error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("ScanPack(leaky) returned no findings; masking cannot be verified")
	}
	for _, f := range got {
		for _, want := range leakyFindings {
			// Even an 8-char prefix of a secret is too much to echo.
			prefix := want.secret
			if len(prefix) > 8 {
				prefix = prefix[:8]
			}
			if strings.Contains(f.Excerpt, prefix) {
				t.Errorf("finding %s:%d excerpt %q reveals a prefix of a seeded secret", f.Path, f.Line, f.Excerpt)
			}
		}
		if f.Excerpt == "" {
			t.Errorf("finding %s:%d has an empty excerpt; the user cannot locate the leak", f.Path, f.Line)
		}
		if len(f.Excerpt) > 80 {
			t.Errorf("finding %s:%d excerpt is %d chars; excerpts must stay short and masked: %q", f.Path, f.Line, len(f.Excerpt), f.Excerpt)
		}
	}
}

func TestReleaseBlocking_BinaryFilesStillScannedForFormats(t *testing.T) {
	dir := t.TempDir()
	// A binary blob (null bytes) with an embedded fake token: format
	// channels must still fire; assignment/entropy are text-only.
	blob := append([]byte{0x89, 'P', 'N', 'G', 0x00, 0x00, 0x01}, []byte("ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE")...)
	blob = append(blob, 0x00, 0xff)
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ScanPack(dir)
	if err != nil {
		t.Fatalf("ScanPack(binary) error: %v", err)
	}
	if len(got) != 1 || got[0].Rule != "format:github-token" || got[0].Path != "logo.png" {
		t.Errorf("ScanPack(binary) = %+v, want one format:github-token finding in logo.png", got)
	}
}

func TestScanPackSkipsGitDirAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Leaks inside .git are the VCS's business, not the pack's contents.
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("token = ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("clean\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink pointing outside the pack must not be followed.
	leakTarget := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(leakTarget, []byte("sk-FAKEFAKEFAKEFAKEFAKEFAKE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(leakTarget, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	got, err := ScanPack(dir)
	if err != nil {
		t.Fatalf("ScanPack() error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanPack() = %+v, want no findings from .git or symlinks", got)
	}
}

func TestScanPackErrors(t *testing.T) {
	if _, err := ScanPack(filepath.Join("testdata", "packscan", "does-not-exist")); err == nil {
		t.Error("ScanPack(missing dir) = nil error, want error")
	}
	file := filepath.Join("testdata", "packscan", "clean", "README.md")
	if _, err := ScanPack(file); err == nil {
		t.Error("ScanPack(regular file) = nil error, want error")
	}
}

// TestReleaseBlocking_JSXPropsDoNotFalsePositive guards the false-positive
// class that made save --all unusable in dogfooding (docs/backlog.md P2.9):
// JSX/template attribute values like key={item.userId} share the KEY=value
// shape with a real assignment (the key name "key" is even secret-shaped)
// but are code expressions, never literal credentials.
func TestReleaseBlocking_JSXPropsDoNotFalsePositive(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "clean"))
	if err != nil {
		t.Fatalf("ScanPack(clean) error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ScanPack(clean) = %+v, want no findings (app.jsx's key={item.userId} must not false-positive)", got)
	}
}

func TestScanPackTiersReviewableFindings(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "review"))
	if err != nil {
		t.Fatalf("ScanPack(review) error: %v", err)
	}
	byPath := map[string]Finding{}
	for _, f := range got {
		byPath[f.Path] = f
	}

	cases := []struct {
		path       string
		reviewable bool
	}{
		{"README.md", true},             // docs: assignment-shaped example
		{"scripts/deploy.py", true},     // source: assignment-shaped code
		{"testdata/fixture.json", true}, // test fixture dir
		{"config/settings.json", false}, // real config: never waivable
		{"config/token.md", false},      // format channel always blocks, even in docs
	}
	for _, c := range cases {
		f, ok := byPath[c.path]
		if !ok {
			t.Errorf("ScanPack(review) missing expected finding in %s\ngot: %+v", c.path, got)
			continue
		}
		if f.Reviewable != c.reviewable {
			t.Errorf("ScanPack(review) %s: Reviewable = %v, want %v (rule %s)", c.path, f.Reviewable, c.reviewable, f.Rule)
		}
	}
}

func TestScanPackFindingsAreOrdered(t *testing.T) {
	got, err := ScanPack(filepath.Join("testdata", "packscan", "leaky"))
	if err != nil {
		t.Fatalf("ScanPack(leaky) error: %v", err)
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.Path > cur.Path || (prev.Path == cur.Path && prev.Line > cur.Line) {
			t.Errorf("findings out of order: %s:%d before %s:%d", prev.Path, prev.Line, cur.Path, cur.Line)
		}
	}
}
