package claudecode

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// scanMarkdownDir reads flat <dir>/*.md files (agents and commands share the
// layout — docs/research/tool-config-matrix.md pins both to `*.md`). Each
// file becomes a component via emit; subdirectories are real content the
// flat model skips, so they warn. useFrontmatterName lets an agent's
// frontmatter `name:` override the filename stem, matching Claude Code.
func scanMarkdownDir(inv *model.Inventory, dir string, useFrontmatterName bool,
	emit func(name, path, description string)) error {

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		info, statErr := os.Stat(path) // follow symlinks
		if statErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unreadable entry; skipped",
			})
			continue
		}
		if info.IsDir() {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "subdirectories are not modeled; skipped",
			})
			continue
		}
		if !strings.HasSuffix(e.Name(), ".md") {
			continue // stray non-markdown files (e.g. .DS_Store) are not components
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unreadable file; skipped",
			})
			continue
		}
		fm, fmErr := parseFrontmatter(raw)
		if fmErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unparseable YAML frontmatter; metadata dropped",
			})
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		if useFrontmatterName && fm.Name != "" {
			name = fm.Name
		}
		if abs, absErr := filepath.Abs(path); absErr == nil {
			path = abs
		}
		emit(name, path, fm.Description)
	}
	return nil
}

// scanAgents reads agent definitions (<root>/agents/*.md).
func (a *Adapter) scanAgents(inv *model.Inventory, root string, scope model.Scope) error {
	return scanMarkdownDir(inv, filepath.Join(root, "agents"), true,
		func(name, path, description string) {
			inv.Components = append(inv.Components, model.Agent{Spec: model.AgentSpec{
				Name: name, Scope: scope, Path: path, Description: description,
			}})
		})
}

// scanCommands reads reusable prompts (<root>/commands/*.md). Command names
// come from the filename: that is the slash-command the user types.
func (a *Adapter) scanCommands(inv *model.Inventory, root string, scope model.Scope) error {
	return scanMarkdownDir(inv, filepath.Join(root, "commands"), false,
		func(name, path, description string) {
			inv.Components = append(inv.Components, model.Command{Spec: model.CommandSpec{
				Name: name, Scope: scope, Path: path, Description: description,
			}})
		})
}

// scanRuleFile models one instruction file (CLAUDE.md variants) if present.
func scanRuleFile(inv *model.Inventory, path string, scope model.Scope) error {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path: path, Message: "expected a file, found a directory; skipped",
		})
		return nil
	}
	if abs, absErr := filepath.Abs(path); absErr == nil {
		path = abs
	}
	inv.Components = append(inv.Components, model.Rule{Spec: model.RuleSpec{
		Name: filepath.Base(path), Scope: scope, Path: path,
	}})
	return nil
}

// scanSettingsFile models one settings document if present. The parsed
// values are raw scanned data; save-time redaction filters them.
func scanSettingsFile(inv *model.Inventory, path string, scope model.Scope) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var values map[string]any
	if jsonErr := json.Unmarshal(raw, &values); jsonErr != nil {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path: path, Message: "not a valid JSON object; settings skipped",
		})
		return nil
	}
	if abs, absErr := filepath.Abs(path); absErr == nil {
		path = abs
	}
	inv.Components = append(inv.Components, model.Setting{Spec: model.SettingSpec{
		Name: filepath.Base(path), Scope: scope, Path: path, Values: values,
	}})
	return nil
}
