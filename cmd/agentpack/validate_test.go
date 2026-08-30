package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
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
