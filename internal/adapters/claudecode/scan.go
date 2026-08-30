package claudecode

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PhongCT1105/agentpack/internal/model"
	"gopkg.in/yaml.v3"
)

// Scan reads Claude Code configuration in the requested scopes into the
// neutral model. It never writes.
func (a *Adapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	inv := model.Inventory{Tool: model.ToolClaudeCode}

	if scope.Global && a.home != "" {
		root := filepath.Join(a.home, ".claude")
		steps := []func() error{
			func() error { return a.scanSkills(&inv, filepath.Join(root, "skills"), model.ScopeGlobal) },
			func() error { return a.scanMCPFile(&inv, a.globalMCPPath(), model.ScopeGlobal, true) },
			func() error { return a.scanAgents(&inv, root, model.ScopeGlobal) },
			func() error { return a.scanCommands(&inv, root, model.ScopeGlobal) },
			func() error { return scanRuleFile(&inv, filepath.Join(root, "CLAUDE.md"), model.ScopeGlobal) },
			func() error { return scanSettingsFile(&inv, filepath.Join(root, "settings.json"), model.ScopeGlobal) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
	}
	if scope.ProjectDir != "" {
		proj := scope.ProjectDir
		root := filepath.Join(proj, ".claude")
		steps := []func() error{
			func() error { return a.scanSkills(&inv, filepath.Join(root, "skills"), model.ScopeProject) },
			func() error { return a.scanMCPFile(&inv, projectMCPPath(proj), model.ScopeProject, false) },
			func() error { return a.scanAgents(&inv, root, model.ScopeProject) },
			func() error { return a.scanCommands(&inv, root, model.ScopeProject) },
			// Both the shared and the personal instruction/settings files are
			// modeled: scan shows the whole environment; save decides later
			// what is worth porting.
			func() error { return scanRuleFile(&inv, filepath.Join(proj, "CLAUDE.md"), model.ScopeProject) },
			func() error { return scanRuleFile(&inv, filepath.Join(proj, "CLAUDE.local.md"), model.ScopeProject) },
			func() error { return scanSettingsFile(&inv, filepath.Join(root, "settings.json"), model.ScopeProject) },
			func() error {
				return scanSettingsFile(&inv, filepath.Join(root, "settings.local.json"), model.ScopeProject)
			},
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
	}

	return inv, nil
}

// scanSkills reads <dir>/<name>/SKILL.md entries into skill components.
// A missing skills dir is normal (no skills installed); a subdirectory
// without a SKILL.md becomes a warning, not a component. Symlinked skill
// directories are followed, matching Claude Code's own behavior.
func (a *Adapter) scanSkills(inv *model.Inventory, dir string, scope model.Scope) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, e := range entries {
		skillDir := filepath.Join(dir, e.Name())
		info, statErr := os.Stat(skillDir) // follows symlinks, unlike e.IsDir()
		if statErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    skillDir,
				Message: "unreadable entry in skills directory; skipped",
			})
			continue
		}
		if !info.IsDir() {
			continue // stray files (e.g. .DS_Store) are not skills
		}
		if abs, absErr := filepath.Abs(skillDir); absErr == nil {
			skillDir = abs
		}
		md := filepath.Join(skillDir, "SKILL.md")
		raw, readErr := os.ReadFile(md)
		if errors.Is(readErr, fs.ErrNotExist) {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    skillDir,
				Message: "skill directory has no SKILL.md; skipped",
			})
			continue
		}
		if readErr != nil {
			return readErr
		}
		fm, fmErr := parseFrontmatter(raw)
		if fmErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    md,
				Message: "unparseable YAML frontmatter; description dropped",
			})
		}
		inv.Components = append(inv.Components, model.Skill{Spec: model.SkillSpec{
			Name:        e.Name(), // skills are named by their directory, not frontmatter
			Scope:       scope,
			Dir:         skillDir,
			Description: fm.Description,
		}})
	}
	return nil
}

// frontmatter is the subset of a markdown file's leading YAML block that
// scanning cares about.
type frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// parseFrontmatter extracts a leading YAML frontmatter block (--- ... ---).
// A file without frontmatter yields a zero value and nil error; a block that
// fails to parse yields a zero value and the parse error.
func parseFrontmatter(md []byte) (frontmatter, error) {
	body := bytes.TrimPrefix(md, []byte{0xEF, 0xBB, 0xBF}) // tolerate a UTF-8 BOM
	if !bytes.HasPrefix(body, []byte("---\n")) && !bytes.HasPrefix(body, []byte("---\r\n")) {
		return frontmatter{}, nil
	}
	rest := body[3:] // past the opening ---
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return frontmatter{}, nil // unterminated: treat as no frontmatter
	}
	var fm frontmatter
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return frontmatter{}, err
	}
	return fm, nil
}
