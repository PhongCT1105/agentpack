package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/packio"
)

// saveFixtures writes real source files for bundled components and returns
// an adapters func whose inventory references them.
func saveFixtures(t *testing.T, leaky bool) func() []engine.Adapter {
	t.Helper()
	src := t.TempDir()

	skillDir := filepath.Join(src, "skills", "notes")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillBody := "# Notes skill\n\nTake better notes.\n"
	if leaky {
		skillBody += "\nPasted: ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE\n"
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillBody), 0o644); err != nil {
		t.Fatal(err)
	}
	rulePath := filepath.Join(src, "CLAUDE.md")
	if err := os.WriteFile(rulePath, []byte("# Conventions\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	localRulePath := filepath.Join(src, "CLAUDE.local.md")
	if err := os.WriteFile(localRulePath, []byte("# My personal notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.Skill{Spec: model.SkillSpec{
				Name: "notes", Scope: model.ScopeGlobal, Dir: skillDir,
			}},
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "github", Scope: model.ScopeGlobal,
				Transport: model.TransportStdio, Command: "npx",
				Env: map[string]string{
					"GITHUB_TOKEN":   "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE",
					"GITHUB_API_URL": "https://api.github.com",
				},
			}},
			model.Rule{Spec: model.RuleSpec{
				Name: "CLAUDE.md", Scope: model.ScopeProject, Path: rulePath,
			}},
			// Personal components must never be saved into a pack.
			model.Rule{Spec: model.RuleSpec{
				Name: "CLAUDE.local.md", Scope: model.ScopeProject, Path: localRulePath,
			}},
			model.Setting{Spec: model.SettingSpec{
				Name: "settings.local.json", Scope: model.ScopeProject,
				Path:   filepath.Join(src, "settings.local.json"),
				Values: map[string]any{"model": "opus"},
			}},
		},
	}
	return func() []engine.Adapter {
		return []engine.Adapter{
			stubAdapter{id: model.ToolClaudeCode, installed: true, version: "2.0.44", inv: inv},
			stubAdapter{id: model.ToolCodex, installed: false},
		}
	}
}

func runSave(t *testing.T, adapters func() []engine.Adapter, args ...string) (string, error) {
	t.Helper()
	return runSaveWithInput(t, adapters, "", args...)
}

func runSaveWithInput(t *testing.T, adapters func() []engine.Adapter, stdin string, args ...string) (string, error) {
	t.Helper()
	cmd := newSaveCmd(adapters)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(stdin))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestSaveRequiresAll(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	_, err := runSave(t, saveFixtures(t, false), dir)
	if err == nil || !strings.Contains(err.Error(), "--all") {
		t.Errorf("save without --all: err = %v, want error mentioning --all", err)
	}
}

func TestSaveAllWritesPack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSave(t, saveFixtures(t, false), "--all", "--name", "my-setup", dir)
	if err != nil {
		t.Fatalf("save --all error: %v\noutput:\n%s", err, out)
	}

	data, err := os.ReadFile(filepath.Join(dir, packio.ManifestFilename))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	m, err := packio.DecodeManifest(data)
	if err != nil {
		t.Fatalf("manifest does not decode: %v", err)
	}
	if m.Metadata.Name != "my-setup" {
		t.Errorf("pack name = %q, want my-setup", m.Metadata.Name)
	}
	if len(m.Components.Skills) != 1 || len(m.Components.MCPServers) != 1 {
		t.Errorf("components = %+v, want 1 skill + 1 mcp server", m.Components)
	}
	// Personal files are filtered: only the shared rule survives.
	if len(m.Components.Rules) != 1 || len(m.Components.Settings) != 0 {
		t.Errorf("rules/settings = %+v/%+v, want 1 shared rule and no settings",
			m.Components.Rules, m.Components.Settings)
	}
	if !strings.Contains(out, "CLAUDE.local.md") || !strings.Contains(out, "personal") {
		t.Errorf("output does not mention skipped personal components:\n%s", out)
	}

	// The secret env var became a credential and its value is nowhere.
	srv := m.Components.MCPServers[0]
	if len(srv.Credentials) != 1 || srv.Credentials[0].Env != "GITHUB_TOKEN" {
		t.Errorf("credentials = %+v, want GITHUB_TOKEN", srv.Credentials)
	}
	if strings.Contains(string(data), "ghp_FAKE") {
		t.Error("manifest contains the redacted token value")
	}
	if !strings.Contains(out, "GITHUB_TOKEN") {
		t.Errorf("output does not report the redaction:\n%s", out)
	}

	if _, err := os.Stat(filepath.Join(dir, "skills", "notes", "SKILL.md")); err != nil {
		t.Errorf("bundled skill missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "rules")); err != nil {
		t.Errorf("bundled rules missing: %v", err)
	}
}

func TestSaveDefaultNameFromDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My_Dev Setup")
	out, err := runSave(t, saveFixtures(t, false), "--all", dir)
	if err != nil {
		t.Fatalf("save --all error: %v\noutput:\n%s", err, out)
	}
	data, err := os.ReadFile(filepath.Join(dir, packio.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	m, err := packio.DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if m.Metadata.Name != "my-dev-setup" {
		t.Errorf("default pack name = %q, want my-dev-setup", m.Metadata.Name)
	}
}

func TestReleaseBlocking_SaveBlocksLeakyPack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSave(t, saveFixtures(t, true), "--all", "--name", "leaky", dir)
	if err == nil {
		t.Fatalf("save of leaky content succeeded; output:\n%s", out)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("blocked save left pack dir on disk: %v", statErr)
	}
	if !strings.Contains(out, "SKILL.md") {
		t.Errorf("output does not point at the leaky file:\n%s", out)
	}
	if strings.Contains(out, "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE") {
		t.Error("output echoes the full secret")
	}
}

func TestSaveFailsWhenNothingInstalled(t *testing.T) {
	adapters := func() []engine.Adapter {
		return []engine.Adapter{stubAdapter{id: model.ToolCodex, installed: false}}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	_, err := runSave(t, adapters, "--all", dir)
	if err == nil || !strings.Contains(err.Error(), "no portable components") {
		t.Errorf("save with nothing installed: err = %v, want 'no portable components' error", err)
	}
}

// uncertainAdapters returns an inventory whose MCP server carries one
// clearly-secret env var and one uncertain one (the SUPABASE_URL problem).
func uncertainAdapters() func() []engine.Adapter {
	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "supabase", Scope: model.ScopeGlobal,
				Transport: model.TransportStdio, Command: "npx",
				Env: map[string]string{
					"SUPABASE_URL":   "https://FAKE0q7pz2mk9vlt4wyb.supabase.co",
					"SUPABASE_TOKEN": "FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6",
				},
			}},
		},
	}
	return func() []engine.Adapter {
		return []engine.Adapter{stubAdapter{id: model.ToolClaudeCode, installed: true, inv: inv}}
	}
}

func savedManifest(t *testing.T, dir string) *packio.Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, packio.ManifestFilename))
	if err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	m, err := packio.DecodeManifest(data)
	if err != nil {
		t.Fatalf("manifest does not decode: %v", err)
	}
	return m
}

func TestSaveReviewUncertainKeepAnswer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSaveWithInput(t, uncertainAdapters(), "n\n",
		"--all", "--name", "u-pack", "--review-uncertain", dir)
	if err != nil {
		t.Fatalf("save error: %v\noutput:\n%s", err, out)
	}
	m := savedManifest(t, dir)
	srv := m.Components.MCPServers[0]
	if srv.Env["SUPABASE_URL"] == "" {
		t.Errorf("answered n (keep); SUPABASE_URL missing from env: %+v", srv)
	}
	// The clearly-secret value must never be prompted for and always redacts.
	if len(srv.Credentials) != 1 || srv.Credentials[0].Env != "SUPABASE_TOKEN" {
		t.Errorf("credentials = %+v, want only SUPABASE_TOKEN", srv.Credentials)
	}
	if !strings.Contains(out, "SUPABASE_URL") {
		t.Errorf("prompt did not mention the uncertain key:\n%s", out)
	}
	if strings.Contains(out, "FAKEq7PzX2mK9vLtR4wYbN8cJ5hD3fG6") {
		t.Error("output shows the confirmed-secret value; only uncertain values may be displayed for review")
	}
}

func TestSaveReviewUncertainDefaultIsRedact(t *testing.T) {
	for name, stdin := range map[string]string{"empty answer": "\n", "eof": ""} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "pack")
			out, err := runSaveWithInput(t, uncertainAdapters(), stdin,
				"--all", "--name", "u-pack", "--review-uncertain", dir)
			if err != nil {
				t.Fatalf("save error: %v\noutput:\n%s", err, out)
			}
			srv := savedManifest(t, dir).Components.MCPServers[0]
			if _, kept := srv.Env["SUPABASE_URL"]; kept {
				t.Errorf("default answer must redact; SUPABASE_URL kept: %+v", srv)
			}
			if len(srv.Credentials) != 2 {
				t.Errorf("credentials = %+v, want SUPABASE_TOKEN and SUPABASE_URL", srv.Credentials)
			}
		})
	}
}

func TestSaveReviewUncertainInvalidAnswerReprompts(t *testing.T) {
	// Two uncertain values, prompted in sorted key order. The first gets an
	// invalid answer then "n" (keep); the second hits EOF (redact).
	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "supabase", Scope: model.ScopeGlobal,
				Transport: model.TransportStdio, Command: "npx",
				Env: map[string]string{
					"SUPA_A_URL": "https://FAKE0q7pz2mk9vlt4wyb.supabase.co",
					"SUPA_B_URL": "https://FAKE0q7pz2mk9vlt4wyb.supabase.co",
				},
			}},
		},
	}
	adapters := func() []engine.Adapter {
		return []engine.Adapter{stubAdapter{id: model.ToolClaudeCode, installed: true, inv: inv}}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSaveWithInput(t, adapters, "bogus\nn\n",
		"--all", "--name", "u-pack", "--review-uncertain", dir)
	if err != nil {
		t.Fatalf("save error: %v\noutput:\n%s", err, out)
	}
	if !strings.Contains(out, "please answer") {
		t.Errorf("invalid answer did not re-prompt:\n%s", out)
	}
	srv := savedManifest(t, dir).Components.MCPServers[0]
	if srv.Env["SUPA_A_URL"] == "" {
		t.Errorf("first value (answered n after re-prompt) not kept: %+v", srv)
	}
	if len(srv.Credentials) != 1 || srv.Credentials[0].Env != "SUPA_B_URL" {
		t.Errorf("credentials = %+v, want only SUPA_B_URL (EOF default)", srv.Credentials)
	}
}

func TestSaveWithoutReviewFlagRedactsAndPointsAtFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSave(t, uncertainAdapters(), "--all", "--name", "u-pack", dir)
	if err != nil {
		t.Fatalf("save error: %v\noutput:\n%s", err, out)
	}
	srv := savedManifest(t, dir).Components.MCPServers[0]
	if len(srv.Credentials) != 2 {
		t.Errorf("credentials = %+v, want both redacted by default", srv.Credentials)
	}
	if !strings.Contains(out, "--review-uncertain") {
		t.Errorf("output does not point at --review-uncertain:\n%s", out)
	}
	// Without review, the uncertain value must not be displayed.
	if strings.Contains(out, "FAKE0q7pz2mk9vlt4wyb") {
		t.Errorf("non-interactive output shows a value:\n%s", out)
	}
}

// erroringAdapter reports installed but fails its scan.
type erroringAdapter struct{ stubAdapter }

func (e erroringAdapter) Scan(model.ScanScope) (model.Inventory, error) {
	return model.Inventory{Tool: e.id}, errors.New("config file unreadable")
}

func TestSaveRefusesPartialPackOnScanError(t *testing.T) {
	adapters := func() []engine.Adapter {
		return []engine.Adapter{
			erroringAdapter{stubAdapter{id: model.ToolClaudeCode, installed: true}},
		}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	_, err := runSave(t, adapters, "--all", dir)
	if err == nil || !strings.Contains(err.Error(), "partial pack") {
		t.Errorf("save with failing scan: err = %v, want partial-pack refusal", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("refused save still wrote something: %v", statErr)
	}
}

func TestSaveExplainsWhenEverythingWasPersonal(t *testing.T) {
	src := t.TempDir()
	localRule := filepath.Join(src, "CLAUDE.local.md")
	if err := os.WriteFile(localRule, []byte("# personal\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapters := func() []engine.Adapter {
		return []engine.Adapter{stubAdapter{
			id: model.ToolClaudeCode, installed: true,
			inv: model.Inventory{Tool: model.ToolClaudeCode, Components: []model.Component{
				model.Rule{Spec: model.RuleSpec{Name: "CLAUDE.local.md", Scope: model.ScopeProject, Path: localRule}},
			}},
		}}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	out, err := runSave(t, adapters, "--all", dir)
	if err == nil || !strings.Contains(err.Error(), "personal") {
		t.Errorf("err = %v, want mention of filtered personal components", err)
	}
	if !strings.Contains(out, "CLAUDE.local.md") {
		t.Errorf("output does not list the skipped component:\n%s", out)
	}
}

// A skill with .local. in its name is NOT personal — the convention only
// applies to rule/settings files.
func TestSaveKeepsDottedLocalSkillName(t *testing.T) {
	src := t.TempDir()
	skillDir := filepath.Join(src, "my.local.setup")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapters := func() []engine.Adapter {
		return []engine.Adapter{stubAdapter{
			id: model.ToolClaudeCode, installed: true,
			inv: model.Inventory{Tool: model.ToolClaudeCode, Components: []model.Component{
				model.Skill{Spec: model.SkillSpec{Name: "my.local.setup", Scope: model.ScopeGlobal, Dir: skillDir}},
			}},
		}}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if _, err := runSave(t, adapters, "--all", "--name", "dotted", dir); err != nil {
		t.Fatalf("save error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, packio.ManifestFilename))
	if err != nil {
		t.Fatal(err)
	}
	m, err := packio.DecodeManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Components.Skills) != 1 {
		t.Errorf("skills = %+v, want the dotted-name skill saved", m.Components.Skills)
	}
}

// scopeRecordingAdapter captures the ScanScope it was given.
type scopeRecordingAdapter struct {
	stubAdapter
	got *model.ScanScope
}

func (s scopeRecordingAdapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	*s.got = scope
	return s.inv, nil
}

func TestSaveForwardsProjectDir(t *testing.T) {
	var got model.ScanScope
	src := t.TempDir()
	rule := filepath.Join(src, "CLAUDE.md")
	if err := os.WriteFile(rule, []byte("# r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	adapters := func() []engine.Adapter {
		return []engine.Adapter{scopeRecordingAdapter{
			stubAdapter: stubAdapter{
				id: model.ToolClaudeCode, installed: true,
				inv: model.Inventory{Tool: model.ToolClaudeCode, Components: []model.Component{
					model.Rule{Spec: model.RuleSpec{Name: "CLAUDE.md", Scope: model.ScopeProject, Path: rule}},
				}},
			},
			got: &got,
		}}
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if _, err := runSave(t, adapters, "--all", "--name", "proj", "--project", "/some/project", dir); err != nil {
		t.Fatalf("save error: %v", err)
	}
	if !got.Global || got.ProjectDir != "/some/project" {
		t.Errorf("scan scope = %+v, want Global with ProjectDir=/some/project", got)
	}
}

func TestSaveRefusesNonEmptyTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := runSave(t, saveFixtures(t, false), "--all", dir)
	if err == nil {
		t.Error("save into non-empty dir succeeded, want refusal")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "keep.txt")); statErr != nil {
		t.Errorf("existing file damaged: %v", statErr)
	}
}
