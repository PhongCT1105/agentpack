package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// newFixtureAdapter returns an adapter rooted at the scan fixture home.
func newFixtureAdapter(t *testing.T) *Adapter {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("testdata", "scan", "home"))
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	// Deterministic dead-server checks: fixture commands (npx, …) must not
	// depend on what the CI machine has installed.
	a.lookPath = lookPathHit
	return a
}

func fixtureProjectDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "scan", "project"))
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func skillByName(t *testing.T, inv model.Inventory, name string) model.Skill {
	t.Helper()
	for _, c := range inv.ByKind(model.KindSkill) {
		if c.Name() == name {
			return c.(model.Skill)
		}
	}
	t.Fatalf("skill %q not found in inventory; have %v", name, componentNames(inv, model.KindSkill))
	return model.Skill{}
}

func componentNames(inv model.Inventory, kind model.Kind) []string {
	var names []string
	for _, c := range inv.ByKind(kind) {
		names = append(names, c.Name())
	}
	return names
}

func TestScanSkillsGlobal(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if inv.Tool != model.ToolClaudeCode {
		t.Errorf("Inventory.Tool = %q, want %q", inv.Tool, model.ToolClaudeCode)
	}

	got := componentNames(inv, model.KindSkill)
	want := []string{"brainstorming", "code-review"}
	if len(got) != len(want) {
		t.Fatalf("global skills = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("global skills = %v, want %v (deterministic name order)", got, want)
		}
	}

	bs := skillByName(t, inv, "brainstorming")
	if bs.Scope() != model.ScopeGlobal {
		t.Errorf("global skill scope = %q, want %q", bs.Scope(), model.ScopeGlobal)
	}
	if bs.Spec.Description != "Explore user intent and requirements before implementation" {
		t.Errorf("description = %q, want frontmatter description", bs.Spec.Description)
	}
	wantDir := filepath.Join(a.home, ".claude", "skills", "brainstorming")
	if bs.Spec.Dir != wantDir {
		t.Errorf("skill dir = %q, want %q", bs.Spec.Dir, wantDir)
	}

	// code-review's SKILL.md has no frontmatter: still scanned, empty description.
	cr := skillByName(t, inv, "code-review")
	if cr.Spec.Description != "" {
		t.Errorf("no-frontmatter skill description = %q, want empty", cr.Spec.Description)
	}
}

func TestScanSkillsProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindSkill)
	if len(got) != 1 || got[0] != "deploy-check" {
		t.Fatalf("project skills = %v, want [deploy-check]", got)
	}
	dc := skillByName(t, inv, "deploy-check")
	if dc.Scope() != model.ScopeProject {
		t.Errorf("project skill scope = %q, want %q", dc.Scope(), model.ScopeProject)
	}
}

func TestScanSkillsGlobalAndProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	got := componentNames(inv, model.KindSkill)
	want := []string{"brainstorming", "code-review", "deploy-check"}
	if len(got) != len(want) {
		t.Fatalf("combined skills = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("combined skills = %v, want %v", got, want)
		}
	}
}

func TestScanSkillsFollowsSymlinkedDirs(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "elsewhere", "linked-skill")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: Skill living behind a symlink\n---\n# Linked\n"
	if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(skillsDir, "linked-skill")); err != nil {
		// Windows without developer mode cannot create symlinks; the
		// symlink-following behavior is POSIX-only coverage there.
		t.Skipf("cannot create symlink on this platform: %v", err)
	}

	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	got := componentNames(inv, model.KindSkill)
	if len(got) != 1 || got[0] != "linked-skill" {
		t.Fatalf("symlinked skill not scanned; got %v, warnings %v", got, inv.Warnings)
	}
}

func TestScanSkillsWarnsOnMalformedFrontmatter(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".claude", "skills", "bad-frontmatter")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\ndescription: \"unbalanced\nname: [broken\n---\n# Bad\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	// Skill is still modeled (content exists), description dropped, warning raised.
	sk := skillByName(t, inv, "bad-frontmatter")
	if sk.Spec.Description != "" {
		t.Errorf("malformed frontmatter produced description %q, want empty", sk.Spec.Description)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want exactly one warning for malformed frontmatter, got %v", inv.Warnings)
	}
}

func TestScanSkillsMissingDir(t *testing.T) {
	a := New()
	a.home = t.TempDir() // no .claude at all
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() on empty home error: %v", err)
	}
	if n := len(inv.Components); n != 0 {
		t.Errorf("empty home produced %d components, want 0", n)
	}
	if n := len(inv.Warnings); n != 0 {
		t.Errorf("empty home produced warnings: %v", inv.Warnings)
	}
}

func TestScanSkillsEmptyScope(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.Components); n != 0 {
		t.Errorf("empty scope produced %d components, want 0", n)
	}
}

func TestScanSkillsWarnsOnDirWithoutSkillMD(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// The fixture contains skills/broken-no-skillmd/ with no SKILL.md: it
	// must not become a component, and must surface as a warning.
	for _, c := range inv.ByKind(model.KindSkill) {
		if c.Name() == "broken-no-skillmd" {
			t.Fatal("directory without SKILL.md was scanned as a skill")
		}
	}
	found := false
	for _, w := range inv.Warnings {
		if filepath.Base(w.Path) == "broken-no-skillmd" {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for skill dir missing SKILL.md; warnings = %v", inv.Warnings)
	}
}
