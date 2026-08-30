package packio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/secrets"
)

func TestValidatePackGood(t *testing.T) {
	issues, findings, allowed, err := ValidatePack(filepath.Join("testdata", "validate", "good"), nil)
	if err != nil {
		t.Fatalf("ValidatePack(good) error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("ValidatePack(good) issues = %v, want none", issues)
	}
	if len(findings) != 0 {
		t.Errorf("ValidatePack(good) findings = %+v, want none", findings)
	}
	if len(allowed) != 0 {
		t.Errorf("ValidatePack(good) allowed = %+v, want none", allowed)
	}
}

func TestValidatePackBad(t *testing.T) {
	issues, findings, _, err := ValidatePack(filepath.Join("testdata", "validate", "bad"), nil)
	if err != nil {
		t.Fatalf("ValidatePack(bad) error: %v", err)
	}
	if len(findings) != 0 {
		t.Errorf("ValidatePack(bad) findings = %+v, want none (bad pack has no secrets)", findings)
	}

	// Every seeded violation must surface as an issue. Substrings pair a
	// component ref with the failure so a wrong attribution fails too.
	wantIssues := []struct{ ref, msg string }{
		{"metadata.name", "Bad_Name"},
		{"targets", "notatool"},
		{"skills/dup", "duplicate"},
		{"skills/dup", "exactly one source"},
		{"skills/dup", "does not exist"},
		{"skills/escape", "escapes the pack"},
		{"skills/absolute", "escapes the pack"},
		{"skills/refless", "ref requires npm"},
		{"skills/sourceless", "exactly one source"},
		{"skills/afile", "must be a directory"},
		{"mcp_servers/nostdio", "requires a command"},
		{"mcp_servers/nourl", "requires a url"},
		{"mcp_servers/pigeon", "unknown transport"},
		{"mcp_servers/badcred", "exactly one of env or header"},
		{"mcp_servers/badcred", "format requires header"},
		{"mcp_servers/badscope", "unknown scope"},
		{"mcp_servers/badscope", "alsonotatool"},
		{"commands/dirprompt", "must be a file"},
		{"rules/badrender", "does not exist"},
		{"rules/badrender", "notatool"},
	}
	for _, want := range wantIssues {
		found := false
		for _, issue := range issues {
			if strings.Contains(issue.Ref, want.ref) && strings.Contains(issue.Message, want.msg) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing issue for %s (%q); got:\n%s", want.ref, want.msg, formatIssues(issues))
		}
	}

	// The empty-name issue must be attributed to the empty ref exactly —
	// a substring match would accept mis-attribution to another skill.
	emptyNameIssues := 0
	for _, issue := range issues {
		if issue.Ref == "skills/" && strings.Contains(issue.Message, "name is required") {
			emptyNameIssues++
		}
	}
	if emptyNameIssues != 1 {
		t.Errorf("got %d name-is-required issues at ref skills/, want exactly 1:\n%s", emptyNameIssues, formatIssues(issues))
	}
}

func formatIssues(issues []Issue) string {
	var b strings.Builder
	for _, i := range issues {
		b.WriteString("  " + i.String() + "\n")
	}
	return b.String()
}

func TestReleaseBlocking_ValidateFlagsLeakyPack(t *testing.T) {
	issues, findings, allowed, err := ValidatePack(filepath.Join("testdata", "validate", "leaky"), nil)
	if err != nil {
		t.Fatalf("ValidatePack(leaky) error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("ValidatePack(leaky) issues = %v, want none (schema is valid)", issues)
	}
	if len(findings) != 1 || findings[0].Path != "prompts/deploy.md" || findings[0].Rule != "format:github-token" {
		t.Errorf("ValidatePack(leaky) findings = %+v, want one github-token finding in prompts/deploy.md", findings)
	}
	if len(allowed) != 0 {
		t.Errorf("ValidatePack(leaky) allowed = %+v, want none", allowed)
	}
}

func TestReleaseBlocking_ValidateRejectsSymlinkedContent(t *testing.T) {
	// A symlinked bundled file is invisible to the secret scanner but would
	// be dereferenced by archivers and restores: content could ship without
	// ever being scanned. Validate must reject it.
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	manifest := "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: sneaky\ncomponents:\n  commands:\n    - name: deploy\n      source:\n        bundled: prompts/deploy.md\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "prompts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "prompts", "deploy.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	issues, _, _, err := ValidatePack(dir, nil)
	if err != nil {
		t.Fatalf("ValidatePack(symlinked) error: %v", err)
	}
	found := false
	for _, i := range issues {
		if i.Ref == "prompts/deploy.md" && strings.Contains(i.Message, "not a regular file") {
			found = true
		}
	}
	if !found {
		t.Errorf("issues = %v, want a symlink rejection for prompts/deploy.md", issues)
	}
}

func TestValidatePackRejectsBackslashPaths(t *testing.T) {
	// Backslashes would be filenames on unix but separators (and escapes)
	// on Windows; validation verdicts must not depend on the platform.
	dir := t.TempDir()
	manifest := "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: slashy\ncomponents:\n  commands:\n    - name: cmd\n      source:\n        bundled: '..\\outside'\n  rules:\n    - name: r\n      source:\n        bundled: rules/r.md\n      render:\n        claude-code: 'sub\\CLAUDE.md'\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	issues, _, _, err := ValidatePack(dir, nil)
	if err != nil {
		t.Fatalf("ValidatePack(backslashes) error: %v", err)
	}
	var sawBundled, sawRender bool
	for _, i := range issues {
		if i.Ref == "commands/cmd" && strings.Contains(i.Message, "forward slashes") {
			sawBundled = true
		}
		if i.Ref == "rules/r" && strings.Contains(i.Message, "slash-separated") {
			sawRender = true
		}
	}
	if !sawBundled || !sawRender {
		t.Errorf("issues = %v, want backslash rejections for bundled and render paths", issues)
	}
}

func TestValidatePackMissingManifest(t *testing.T) {
	issues, _, _, err := ValidatePack(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("ValidatePack(empty dir) error: %v", err)
	}
	found := false
	for _, i := range issues {
		if strings.Contains(i.Message, ManifestFilename) {
			found = true
		}
	}
	if !found {
		t.Errorf("issues = %v, want a missing-manifest issue", issues)
	}
}

func TestValidatePackMissingDir(t *testing.T) {
	if _, _, _, err := ValidatePack(filepath.Join("testdata", "validate", "nope"), nil); err == nil {
		t.Error("ValidatePack(missing dir) = nil error, want error")
	}
}

func TestValidatePackAcceptsWritePackOutput(t *testing.T) {
	// Whatever save writes, validate must accept: the two ends of the
	// format agree.
	res, err := Convert(bundledInventories(t), ConvertOptions{Name: "roundtrip"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if _, _, _, err := WritePack(dir, res, nil, false); err != nil {
		t.Fatal(err)
	}
	issues, findings, allowed, err := ValidatePack(dir, nil)
	if err != nil {
		t.Fatalf("ValidatePack(written pack) error: %v", err)
	}
	if len(issues) != 0 || len(findings) != 0 || len(allowed) != 0 {
		t.Errorf("written pack does not validate: issues=%v findings=%+v allowed=%+v", issues, findings, allowed)
	}
}

// TestValidatePackLoadsAllowlistFile covers the durable side of the review
// flow (docs/backlog.md P2.9): a committed .agentpack-allow in the pack
// waives a matching reviewable finding automatically, without --allow-finding.
func TestValidatePackLoadsAllowlistFile(t *testing.T) {
	dir := t.TempDir()
	manifest := "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: reviewme\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("password=FAKEexample12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, AllowlistFilename), []byte("notes.md:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	issues, blocking, allowed, err := ValidatePack(dir, nil)
	if err != nil {
		t.Fatalf("ValidatePack(allowlisted) error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("ValidatePack(allowlisted) issues = %v, want none", issues)
	}
	if len(blocking) != 0 {
		t.Errorf("ValidatePack(allowlisted) blocking = %+v, want none (waived by %s)", blocking, AllowlistFilename)
	}
	if len(allowed) != 1 || allowed[0].Path != "notes.md" {
		t.Errorf("ValidatePack(allowlisted) allowed = %+v, want the notes.md finding", allowed)
	}
}

// TestValidatePackAllowFindingCannotWaiveAFormatMatch mirrors the WritePack
// safety guarantee: neither a --allow-finding argument nor a committed
// .agentpack-allow entry can silence a known-format token match.
func TestValidatePackAllowFindingCannotWaiveAFormatMatch(t *testing.T) {
	dir := t.TempDir()
	manifest := "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: notwaivable\n"
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	allow := []secrets.AllowEntry{{Path: "notes.md"}}
	issues, blocking, allowed, err := ValidatePack(dir, allow)
	if err != nil {
		t.Fatalf("ValidatePack(allow-listed format match) error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("ValidatePack(allow-listed format match) issues = %v, want none", issues)
	}
	if len(blocking) != 1 {
		t.Errorf("ValidatePack(allow-listed format match) blocking = %+v, want the format match to still block", blocking)
	}
	if len(allowed) != 0 {
		t.Errorf("ValidatePack(allow-listed format match) allowed = %+v, want none", allowed)
	}
}
