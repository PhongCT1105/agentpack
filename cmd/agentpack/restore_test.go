package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runRestore(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newRestoreCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func packioFixture(rel string) string {
	return filepath.Join("..", "..", "internal", "packio", "testdata", rel)
}

func TestRestoreCommandPreview(t *testing.T) {
	out, err := runRestore(t, packioFixture(filepath.Join("read", "preview")))
	if err != nil {
		t.Fatalf("restore(preview) error: %v\noutput:\n%s", err, out)
	}

	// Header: pack identity.
	for _, want := range []string{"fullstack-startup", "Full-Stack Startup Engineer", "claude-code, codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing header element %q:\n%s", want, out)
		}
	}

	// Full contents: every component appears, with its source or shape.
	for _, want := range []string{
		"superpowers", "plugin: superpowers@claude-plugins-official",
		"find-skills", "npm: skills (vercel-labs/find-skills)",
		"notes", "bundled: skills/notes",
		"github", "npx -y @modelcontextprotocol/server-github",
		"GITHUB_API_URL=https://api.github.com",
		"supabase", "https://mcp.supabase.com/mcp",
		"X-Client-Info=agentpack",
		"db-migrator", "bundled: agents/db-migrator.md",
		"conventions", "bundled: rules/conventions.md",
		"claude-code → CLAUDE.md",
		"review", "bundled: prompts/review.md",
		"claude-permissions",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview contents missing %q:\n%s", want, out)
		}
	}

	// Credentials section: every requirement, injection point, obtain URL.
	for _, want := range []string{
		"credentials to collect",
		"GITHUB_TOKEN",
		"GitHub personal access token (repo scope)",
		"https://github.com/settings/tokens",
		"Authorization",
		"Supabase access token",
		"https://supabase.com/dashboard/account/tokens",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview credentials missing %q:\n%s", want, out)
		}
	}

	// External services section.
	for _, want := range []string{
		"external services",
		"plugin marketplace",
		"npm package",
		"remote MCP server",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preview external services missing %q:\n%s", want, out)
		}
	}

	// The pack has a project-scoped rule; restore will need a target dir.
	if !strings.Contains(out, "project-scoped") {
		t.Errorf("preview does not flag project-scoped components:\n%s", out)
	}

	// No apply yet: the preview must say so explicitly.
	if !strings.Contains(out, "nothing was applied") {
		t.Errorf("preview does not state that nothing was applied:\n%s", out)
	}
}

func TestRestoreCommandBundledOnlyPackHasNoExternalServices(t *testing.T) {
	out, err := runRestore(t, packioFixture(filepath.Join("validate", "good")))
	if err != nil {
		t.Fatalf("restore(good) error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "external services: none") {
		t.Errorf("bundled-only pack should report no external services:\n%s", out)
	}
	// good-pack still declares one credential.
	if !strings.Contains(out, "GITHUB_TOKEN") {
		t.Errorf("preview missing good-pack credential:\n%s", out)
	}
}

func TestRestoreCommandInvalidPackFailsNonzero(t *testing.T) {
	out, err := runRestore(t, packioFixture(filepath.Join("validate", "bad")))
	if err == nil {
		t.Fatalf("restore(bad) succeeded; invalid packs must be refused\noutput:\n%s", out)
	}
	if !strings.Contains(out, "metadata.name") {
		t.Errorf("restore(bad) output does not show validation issues:\n%s", out)
	}
}

func TestReleaseBlocking_RestoreCommandRefusesLeakyPack(t *testing.T) {
	out, err := runRestore(t, packioFixture(filepath.Join("validate", "leaky")))
	if err == nil {
		t.Fatalf("restore(leaky) succeeded; leaky packs must be refused\noutput:\n%s", out)
	}
	if !strings.Contains(out, "prompts/deploy.md") {
		t.Errorf("restore(leaky) output does not locate the finding:\n%s", out)
	}
	if strings.Contains(out, "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE") {
		t.Error("restore output echoes the full secret")
	}
}

func TestRestoreCommandMissingDir(t *testing.T) {
	if _, err := runRestore(t, filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("restore(missing dir) succeeded, want error")
	}
}
