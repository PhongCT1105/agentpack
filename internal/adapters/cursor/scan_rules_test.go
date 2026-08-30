package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestScanRulesProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindRule)
	want := []string{
		"always-safety.mdc", "api-conventions.mdc", "scratch-notes.mdc",
		"testing.mdc", ".cursorrules",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("rules = %v, want %v", got, want)
	}

	for _, c := range inv.ByKind(model.KindRule) {
		r := c.(model.Rule)
		if r.Scope() != model.ScopeProject {
			t.Errorf("rule %q scope = %q, want project (Cursor rules are project-level)", r.Name(), r.Scope())
		}
		if !filepath.IsAbs(r.Spec.Path) {
			t.Errorf("rule %q path = %q, want absolute", r.Name(), r.Spec.Path)
		}
		if filepath.Base(r.Spec.Path) != r.Name() {
			t.Errorf("rule %q path = %q, want the file it names", r.Name(), r.Spec.Path)
		}
	}
}

func TestScanRulesWarnings(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	tests := []struct {
		name     string
		file     string
		contains string
	}{
		// Cursor's rules system ignores plain .md, so a scan must say the
		// file was seen and skipped rather than drop it.
		{"non-mdc file", "README.md", "only .mdc"},
		// Cursor does support rule folders; this scanner models the flat
		// top level, so the folder is reported instead of vanishing.
		{"nested folder", "frontend", "nested rule folders"},
		{"always-applied rule", "always-safety.mdc", "does not model: alwaysApply, description"},
		{"glob-scoped rule", "api-conventions.mdc", "does not model: description, globs"},
		{"comma-separated globs", "testing.mdc", "does not model: description, globs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, w := range inv.Warnings {
				if filepath.Base(w.Path) == tt.file && strings.Contains(w.Message, tt.contains) {
					found = true
				}
			}
			if !found {
				t.Errorf("no warning for %s containing %q; warnings = %v", tt.file, tt.contains, inv.Warnings)
			}
		})
	}

	// A rule without frontmatter is manual-only in Cursor and loses nothing,
	// so it is modeled in silence; the .bak copy is debris and stays silent
	// too.
	for _, w := range inv.Warnings {
		if base := filepath.Base(w.Path); base == "scratch-notes.mdc" || strings.Contains(base, ".bak") {
			t.Errorf("unexpected warning for %s: %v", base, w)
		}
	}
}

func TestScanLegacyCursorRules(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	found := false
	for _, c := range inv.ByKind(model.KindRule) {
		if c.Name() == ".cursorrules" {
			found = true
		}
	}
	if !found {
		t.Fatalf("legacy .cursorrules not modeled; rules = %v", componentNames(inv, model.KindRule))
	}
	warned := false
	for _, w := range inv.Warnings {
		if filepath.Base(w.Path) == ".cursorrules" && strings.Contains(w.Message, "legacy") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no legacy-format warning for .cursorrules; warnings = %v", inv.Warnings)
	}
}

func TestScanCommands(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindCommand)
	want := []string{"review", "write-tests"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("commands = %v, want %v", got, want)
	}
	for _, c := range inv.ByKind(model.KindCommand) {
		cmd := c.(model.Command)
		if cmd.Scope() != model.ScopeProject {
			t.Errorf("command %q scope = %q, want project", cmd.Name(), cmd.Scope())
		}
		if cmd.Name() == "review" && cmd.Spec.Description != "Structured review of the current diff" {
			t.Errorf("review description = %q", cmd.Spec.Description)
		}
		// Cursor commands are plain markdown; most carry no frontmatter.
		if cmd.Name() == "write-tests" && cmd.Spec.Description != "" {
			t.Errorf("write-tests description = %q, want empty", cmd.Spec.Description)
		}
	}
}

func TestScanProjectWithoutCursorDir(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("project without .cursor produced components %v warnings %v", inv.Components, inv.Warnings)
	}
}

func TestScanRuleMalformedFrontmatterWarns(t *testing.T) {
	proj := t.TempDir()
	dir := filepath.Join(proj, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mdc := "---\ndescription: \"unbalanced\nalwaysApply: [broken\n---\n# Bad\n"
	if err := os.WriteFile(filepath.Join(dir, "bad.mdc"), []byte(mdc), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	a.home = t.TempDir()
	inv, err := a.Scan(model.ScanScope{ProjectDir: proj})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	// The rule is still modeled — the file exists and is portable; only its
	// metadata is unreadable.
	if got := componentNames(inv, model.KindRule); len(got) != 1 || got[0] != "bad.mdc" {
		t.Fatalf("rules = %v, want [bad.mdc]", got)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "unparseable") {
		t.Fatalf("want one unparseable-frontmatter warning, got %v", inv.Warnings)
	}
}

func TestParseRuleFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		md      string
		want    ruleFrontmatter
		wantErr bool
	}{
		{
			name: "yaml list globs",
			md:   "---\ndescription: API rules\nglobs: [\"src/**/*.ts\", \"src/**/*.tsx\"]\nalwaysApply: false\n---\nbody\n",
			want: ruleFrontmatter{Description: "API rules", Globs: globList{"src/**/*.ts", "src/**/*.tsx"}},
		},
		{
			// What Cursor's own rule editor writes: unquoted, and a leading
			// * would otherwise read as a YAML alias.
			name: "unquoted comma-separated globs",
			md:   "---\nglobs: *_test.go, src/**/*.spec.ts\nalwaysApply: false\n---\nbody\n",
			want: ruleFrontmatter{Globs: globList{"*_test.go", "src/**/*.spec.ts"}},
		},
		{
			name: "unquoted single glob",
			md:   "---\nglobs: **/*.ts\n---\nbody\n",
			want: ruleFrontmatter{Globs: globList{"**/*.ts"}},
		},
		{
			name: "unquoted globs with CRLF line endings",
			md:   "---\r\nglobs: **/*.ts\r\nalwaysApply: true\r\n---\r\nbody\r\n",
			want: ruleFrontmatter{Globs: globList{"**/*.ts"}, AlwaysApply: true},
		},
		{
			name: "quoted globs are left alone",
			md:   "---\nglobs: '*.ts,*.tsx'\n---\nbody\n",
			want: ruleFrontmatter{Globs: globList{"*.ts", "*.tsx"}},
		},
		{
			name: "always applied",
			md:   "---\ndescription: Guardrails\nalwaysApply: true\n---\nbody\n",
			want: ruleFrontmatter{Description: "Guardrails", AlwaysApply: true},
		},
		{
			name: "no frontmatter",
			md:   "# Just a rule body\n",
			want: ruleFrontmatter{},
		},
		{
			name: "unterminated frontmatter is not frontmatter",
			md:   "---\ndescription: never closed\n",
			want: ruleFrontmatter{},
		},
		{
			name:    "malformed yaml",
			md:      "---\ndescription: \"unbalanced\nalwaysApply: [broken\n---\nbody\n",
			wantErr: true,
		},
		{
			name:    "globs of an unsupported shape",
			md:      "---\nglobs:\n  include: \"*.ts\"\n---\nbody\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRuleFrontmatter([]byte(tt.md))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseRuleFrontmatter() = %+v, want an error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRuleFrontmatter() error: %v", err)
			}
			if got.Description != tt.want.Description || got.AlwaysApply != tt.want.AlwaysApply ||
				strings.Join(got.Globs, "|") != strings.Join(tt.want.Globs, "|") {
				t.Errorf("parseRuleFrontmatter() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
