package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func fixtureProjectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "scan", "project"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestScanAgentsMDGlobalAndProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	rules := inv.ByKind(model.KindRule)
	if len(rules) != 2 {
		t.Fatalf("rules = %v, want global + project AGENTS.md", rules)
	}
	for _, c := range rules {
		r := c.(model.Rule)
		if r.Name() != "AGENTS.md" {
			t.Errorf("rule name = %q, want AGENTS.md", r.Name())
		}
		if filepath.Base(r.Spec.Path) != "AGENTS.md" {
			t.Errorf("rule path = %q", r.Spec.Path)
		}
	}
	if rules[0].Scope() != model.ScopeGlobal || rules[1].Scope() != model.ScopeProject {
		t.Errorf("rule scopes = %q, %q; want global then project",
			rules[0].Scope(), rules[1].Scope())
	}
}

func TestScanPrompts(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindCommand)
	want := []string{"plan", "review"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("prompts = %v, want %v (sorted)", got, want)
	}

	for _, c := range inv.ByKind(model.KindCommand) {
		cmd := c.(model.Command)
		if cmd.Scope() != model.ScopeGlobal {
			t.Errorf("prompt %q scope = %q, want global (Codex prompts are global-only)",
				cmd.Name(), cmd.Scope())
		}
		if cmd.Name() == "review" && cmd.Spec.Description != "Structured review of the current diff" {
			t.Errorf("review description = %q", cmd.Spec.Description)
		}
		if cmd.Name() == "plan" && cmd.Spec.Description != "" {
			t.Errorf("plan description = %q, want empty (no frontmatter)", cmd.Spec.Description)
		}
	}

	// prompts/ contains a subdirectory; flat *.md is the modeled surface,
	// so it must warn rather than vanish.
	found := false
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "subdirector") && strings.Contains(w.Path, "prompts") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for prompts subdirectory; warnings = %v", inv.Warnings)
	}
}

func TestScanPromptsMissingDir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindCommand)); n != 0 {
		t.Errorf("missing prompts dir produced %d commands", n)
	}
	if n := len(inv.ByKind(model.KindRule)); n != 0 {
		t.Errorf("missing AGENTS.md produced %d rules", n)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("warnings on empty ~/.codex: %v", inv.Warnings)
	}
}

func TestScanProjectWithoutAgentsMD(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindRule)); n != 0 {
		t.Errorf("empty project produced %d rules", n)
	}
}
