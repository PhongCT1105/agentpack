package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/packio"
)

func runValidate(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newValidateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func validateFixture(rel string) string {
	return filepath.Join("..", "..", "internal", "packio", "testdata", "validate", rel)
}

func TestValidateCommandGoodPack(t *testing.T) {
	out, err := runValidate(t, validateFixture("good"))
	if err != nil {
		t.Fatalf("validate(good) error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "valid") {
		t.Errorf("validate(good) output does not confirm validity:\n%s", out)
	}
}

func TestValidateCommandBadPackFailsNonzero(t *testing.T) {
	out, err := runValidate(t, validateFixture("bad"))
	if err == nil {
		t.Fatalf("validate(bad) succeeded; CI relies on a nonzero exit\noutput:\n%s", out)
	}
	for _, want := range []string{"metadata.name", "skills/dup", "mcp_servers/nostdio"} {
		if !strings.Contains(out, want) {
			t.Errorf("validate(bad) output missing issue %q:\n%s", want, out)
		}
	}
}

func TestReleaseBlocking_ValidateCommandFlagsLeakyPack(t *testing.T) {
	out, err := runValidate(t, validateFixture("leaky"))
	if err == nil {
		t.Fatalf("validate(leaky) succeeded; secret findings must fail validation\noutput:\n%s", out)
	}
	if !strings.Contains(out, "prompts/deploy.md") {
		t.Errorf("validate(leaky) output does not locate the finding:\n%s", out)
	}
	if strings.Contains(out, "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE") {
		t.Error("validate output echoes the full secret")
	}
}

func TestValidateCommandMissingDir(t *testing.T) {
	if _, err := runValidate(t, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("validate(missing dir) succeeded, want error")
	}
}

// reviewablePack writes a minimal, schema-valid pack with one docs-context
// assignment-shaped finding (Reviewable), not a real secret.
func reviewablePack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: reviewme\n"
	if err := os.WriteFile(filepath.Join(dir, packio.ManifestFilename), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("password=FAKEexample12345\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestValidateCommandLoadsAllowlistFile(t *testing.T) {
	dir := reviewablePack(t)
	if err := os.WriteFile(filepath.Join(dir, packio.AllowlistFilename), []byte("notes.md:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runValidate(t, dir)
	if err != nil {
		t.Fatalf("validate(allowlisted) error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "waived") {
		t.Errorf("output does not report the waived finding:\n%s", out)
	}
}

func TestValidateCommandAllowFindingFlagWaivesReviewableFinding(t *testing.T) {
	dir := reviewablePack(t)
	out, err := runValidate(t, "--allow-finding", "notes.md:1", dir)
	if err != nil {
		t.Fatalf("validate(--allow-finding) error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "valid") {
		t.Errorf("output does not confirm validity:\n%s", out)
	}
}

func TestValidateCommandWithoutAllowFindingStillBlocks(t *testing.T) {
	dir := reviewablePack(t)
	out, err := runValidate(t, dir)
	if err == nil {
		t.Fatalf("validate(no allowlist) succeeded; output:\n%s", out)
	}
	if !strings.Contains(out, "need review") {
		t.Errorf("output does not distinguish the reviewable finding:\n%s", out)
	}
}

func TestValidateCommandAllowFindingCannotWaiveFormatMatch(t *testing.T) {
	out, err := runValidate(t, "--allow-finding", "prompts/deploy.md", validateFixture("leaky"))
	if err == nil {
		t.Fatalf("validate(leaky, allow-listed) succeeded; a format match must not be waivable\noutput:\n%s", out)
	}
	if !strings.Contains(out, "prompts/deploy.md") {
		t.Errorf("output does not still flag the finding:\n%s", out)
	}
}
