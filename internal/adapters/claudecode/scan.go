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
		if err := a.scanSkills(&inv, filepath.Join(a.home, ".claude", "skills"), model.ScopeGlobal); err != nil {
			return inv, err
		}
	}
	if scope.ProjectDir != "" {
		if err := a.scanSkills(&inv, filepath.Join(scope.ProjectDir, ".claude", "skills"), model.ScopeProject); err != nil {
			return inv, err
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
		desc, fmErr := frontmatterDescription(raw)
		if fmErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    md,
				Message: "unparseable YAML frontmatter; description dropped",
			})
		}
		inv.Components = append(inv.Components, model.Skill{Spec: model.SkillSpec{
			Name:        e.Name(),
			Scope:       scope,
			Dir:         skillDir,
			Description: desc,
		}})
	}
	return nil
}

// frontmatterDescription extracts `description:` from a leading YAML
// frontmatter block (--- ... ---). A file without frontmatter yields ("",
// nil); a frontmatter block that fails to parse yields ("", error).
func frontmatterDescription(md []byte) (string, error) {
	body := bytes.TrimPrefix(md, []byte{0xEF, 0xBB, 0xBF}) // tolerate a UTF-8 BOM
	if !bytes.HasPrefix(body, []byte("---\n")) && !bytes.HasPrefix(body, []byte("---\r\n")) {
		return "", nil
	}
	rest := body[3:] // past the opening ---
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return "", nil // unterminated: treat as no frontmatter
	}
	var fm struct {
		Description string `yaml:"description"`
	}
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return "", err
	}
	return fm.Description, nil
}
