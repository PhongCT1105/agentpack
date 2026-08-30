package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestScanAgents(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindAgent)
	// db-migrator's frontmatter name overrides its filename (migrate.md);
	// reviewer.md has no frontmatter name so the filename stem is used.
	want := []string{"db-migrator", "reviewer", "tester"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("agents = %v, want %v", got, want)
	}

	var dbm model.Agent
	for _, c := range inv.ByKind(model.KindAgent) {
		if c.Name() == "db-migrator" {
			dbm = c.(model.Agent)
		}
	}
	if dbm.Scope() != model.ScopeGlobal {
		t.Errorf("db-migrator scope = %q, want global", dbm.Scope())
	}
	if dbm.Spec.Description != "Plans and applies database migrations safely" {
		t.Errorf("db-migrator description = %q", dbm.Spec.Description)
	}
	if filepath.Base(dbm.Spec.Path) != "migrate.md" {
		t.Errorf("db-migrator path = %q, want the migrate.md file", dbm.Spec.Path)
	}

	for _, c := range inv.ByKind(model.KindAgent) {
		if c.Name() == "tester" && c.Scope() != model.ScopeProject {
			t.Errorf("tester scope = %q, want project", c.Scope())
		}
	}
}

func TestScanCommands(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindCommand)
	want := []string{"deploy", "review", "fix-issue"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v (global sorted, then project)", got, want)
	}

	for _, c := range inv.ByKind(model.KindCommand) {
		cmd := c.(model.Command)
		if cmd.Name() == "review" && cmd.Spec.Description != "Run a structured code review over the current diff" {
			t.Errorf("review description = %q", cmd.Spec.Description)
		}
		if cmd.Name() == "fix-issue" && cmd.Scope() != model.ScopeProject {
			t.Errorf("fix-issue scope = %q, want project", cmd.Scope())
		}
	}

	// The commands dir contains a subdirectory (namespaced commands); the
	// matrix models flat *.md only, so it must warn rather than vanish.
	found := false
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "subdirector") && strings.Contains(w.Path, "commands") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for commands subdirectory; warnings = %v", inv.Warnings)
	}
}

func TestScanRules(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	type key struct {
		name  string
		scope model.Scope
	}
	gotRules := map[key]model.Rule{}
	for _, c := range inv.ByKind(model.KindRule) {
		r := c.(model.Rule)
		gotRules[key{r.Name(), r.Scope()}] = r
	}

	wantKeys := []key{
		{"CLAUDE.md", model.ScopeGlobal},
		{"CLAUDE.md", model.ScopeProject},
		{"CLAUDE.local.md", model.ScopeProject},
	}
	if len(gotRules) != len(wantKeys) {
		t.Fatalf("rules = %v, want %v", gotRules, wantKeys)
	}
	for _, k := range wantKeys {
		r, ok := gotRules[k]
		if !ok {
			t.Errorf("missing rule %v", k)
			continue
		}
		if filepath.Base(r.Spec.Path) != k.name {
			t.Errorf("rule %v path = %q", k, r.Spec.Path)
		}
	}
}

func TestScanSettings(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	type key struct {
		name  string
		scope model.Scope
	}
	gotSettings := map[key]model.Setting{}
	for _, c := range inv.ByKind(model.KindSetting) {
		s := c.(model.Setting)
		gotSettings[key{s.Name(), s.Scope()}] = s
	}

	wantKeys := []key{
		{"settings.json", model.ScopeGlobal},
		{"settings.json", model.ScopeProject},
		{"settings.local.json", model.ScopeProject},
	}
	if len(gotSettings) != len(wantKeys) {
		t.Fatalf("settings = %v, want %v", gotSettings, wantKeys)
	}

	gs := gotSettings[key{"settings.json", model.ScopeGlobal}]
	if gs.Spec.Values["model"] != "opus" {
		t.Errorf("global settings model = %v, want opus", gs.Spec.Values["model"])
	}
	ps := gotSettings[key{"settings.json", model.ScopeProject}]
	perms, ok := ps.Spec.Values["permissions"].(map[string]any)
	if !ok {
		t.Fatalf("project settings permissions = %T, want object", ps.Spec.Values["permissions"])
	}
	if _, ok := perms["allow"]; !ok {
		t.Errorf("project settings permissions.allow missing: %v", perms)
	}
}

func TestScanSettingsMalformedWarns(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("{oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindSetting)); n != 0 {
		t.Errorf("malformed settings produced %d components", n)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "not a valid JSON object") {
		t.Fatalf("want one not-a-valid-JSON-object warning, got %v", inv.Warnings)
	}
}

func TestScanComponentsMissingEverything(t *testing.T) {
	a := New()
	a.home = t.TempDir()
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("empty machine produced components %v warnings %v", inv.Components, inv.Warnings)
	}
}
