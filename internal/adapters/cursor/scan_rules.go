package cursor

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/PhongCT1105/agentpack/internal/adapters/mdscan"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// ruleFrontmatter is the leading YAML block of a .cursor/rules/*.mdc file.
// These three fields are what decide when Cursor attaches a rule:
// alwaysApply puts it in every request, globs attach it to matching files,
// and description is what an agent picks the rule by. The neutral rule model
// carries none of them — a rule is a name, a scope, and a file — so they are
// read to report what a port would change, not to fill a component field.
type ruleFrontmatter struct {
	Description string   `yaml:"description"`
	Globs       globList `yaml:"globs"`
	AlwaysApply bool     `yaml:"alwaysApply"`
}

// globList accepts both shapes real .mdc files use: a YAML list
// (globs: ["src/**/*.ts"]) and the bare comma-separated string Cursor's own
// rule editor writes (globs: src/**/*.ts,*.tsx).
type globList []string

// UnmarshalYAML implements yaml.Unmarshaler for the two accepted shapes.
func (g *globList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var s string
		if err := value.Decode(&s); err != nil {
			return err
		}
		*g = splitGlobs(s)
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		*g = nil
		for _, item := range list {
			if item = strings.TrimSpace(item); item != "" {
				*g = append(*g, item)
			}
		}
	default:
		return fmt.Errorf("globs must be a string or a list, got yaml kind %d", value.Kind)
	}
	return nil
}

// splitGlobs turns "src/**/*.ts, *.tsx" into its non-empty parts.
func splitGlobs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

// parseRuleFrontmatter extracts the leading YAML frontmatter block
// (--- ... ---) of an .mdc file. A file without frontmatter yields a zero
// value and nil error; a block that fails to parse yields a zero value and
// the parse error. mdscan.ParseFrontmatter cannot serve here: it models the
// name/description pair the markdown-based tools use, not Cursor's
// attachment fields.
func parseRuleFrontmatter(md []byte) (ruleFrontmatter, error) {
	body := bytes.TrimPrefix(md, []byte{0xEF, 0xBB, 0xBF}) // tolerate a UTF-8 BOM
	if !bytes.HasPrefix(body, []byte("---\n")) && !bytes.HasPrefix(body, []byte("---\r\n")) {
		return ruleFrontmatter{}, nil
	}
	rest := body[3:] // past the opening ---
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return ruleFrontmatter{}, nil // unterminated: treat as no frontmatter
	}
	var fm ruleFrontmatter
	if err := yaml.Unmarshal(quoteGlobsLine(rest[:end]), &fm); err != nil {
		return ruleFrontmatter{}, err
	}
	return fm, nil
}

// quoteGlobsLine rewrites a top-level `globs: **/*.ts,*.tsx` line as
// `globs: '**/*.ts,*.tsx'`. Cursor's own rule editor writes the value
// unquoted, but a leading `*` opens a YAML alias, so a strict parse rejects
// the most ordinary auto-attached rule there is. Only an unquoted, non-list
// globs value at the start of a line is touched — a quoted value, a flow
// list, a block scalar, and a genuinely malformed block all still reach the
// parser as written.
func quoteGlobsLine(block []byte) []byte {
	lines := bytes.Split(block, []byte("\n"))
	for i, line := range lines {
		if !bytes.HasPrefix(line, []byte("globs:")) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(string(line), "globs:"))
		if value == "" || strings.HasPrefix(value, "'") || strings.HasPrefix(value, `"`) ||
			strings.HasPrefix(value, "[") || strings.HasPrefix(value, "#") ||
			strings.HasPrefix(value, ">") || strings.HasPrefix(value, "|") {
			continue // already unambiguous YAML, or the head of a block scalar
		}
		lines[i] = []byte("globs: '" + strings.ReplaceAll(value, "'", "''") + "'")
	}
	return bytes.Join(lines, []byte("\n"))
}

// scanRules reads project rules (<project>/.cursor/rules/*.mdc). A missing
// directory is normal. Everything else the directory holds is reported
// rather than dropped: Cursor's rules system reads only .mdc files, and this
// scanner models only the flat top level, so nested rule folders — which
// Cursor does support — surface as warnings instead of vanishing.
func scanRules(inv *model.Inventory, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, e := range entries {
		name := e.Name()
		if mdscan.IsDebris(name) || strings.HasPrefix(name, ".") {
			continue // backup debris and .DS_Store are not rules
		}
		path := filepath.Join(dir, name)
		info, statErr := os.Stat(path) // follow symlinks
		if statErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unreadable entry; skipped",
			})
			continue
		}
		if info.IsDir() {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "nested rule folders are not modeled; skipped",
			})
			continue
		}
		if filepath.Ext(name) != ".mdc" {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "Cursor's rules system reads only .mdc files; skipped",
			})
			continue
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unreadable file; skipped",
			})
			continue
		}
		if abs, absErr := filepath.Abs(path); absErr == nil {
			path = abs
		}
		if fm, fmErr := parseRuleFrontmatter(raw); fmErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path, Message: "unparseable YAML frontmatter; rule metadata dropped",
			})
		} else {
			warnRuleFrontmatter(inv, path, fm)
		}
		inv.Components = append(inv.Components, model.Rule{Spec: model.RuleSpec{
			Name: name, Scope: model.ScopeProject, Path: path,
		}})
	}
	return nil
}

// warnRuleFrontmatter reports the frontmatter a rule sets that the neutral
// model cannot carry. The values survive a Cursor→Cursor round trip inside
// the .mdc file itself, but a rule rendered for a tool whose instruction
// file is always-on (CLAUDE.md, AGENTS.md) quietly changes meaning — an
// agent-requested or glob-scoped rule becomes unconditional. A rule with no
// frontmatter is manual-only in Cursor and loses nothing, so it says nothing.
func warnRuleFrontmatter(inv *model.Inventory, path string, fm ruleFrontmatter) {
	var unmodeled []string
	if fm.AlwaysApply {
		unmodeled = append(unmodeled, "alwaysApply")
	}
	if fm.Description != "" {
		unmodeled = append(unmodeled, "description")
	}
	if len(fm.Globs) > 0 {
		unmodeled = append(unmodeled, "globs")
	}
	if len(unmodeled) == 0 {
		return
	}
	inv.Warnings = append(inv.Warnings, model.Warning{
		Path:    path,
		Message: "frontmatter agentpack does not model: " + strings.Join(unmodeled, ", "),
	})
}

// scanLegacyRules models a legacy .cursorrules file at the project root.
// Cursor still reads it, so it is real config worth scanning, but the
// current format is .cursor/rules/*.mdc; the warning is what tells the user
// a port has a conversion to make (docs/research/tool-config-matrix.md).
func scanLegacyRules(inv *model.Inventory, path string) error {
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
	inv.Warnings = append(inv.Warnings, model.Warning{
		Path:    path,
		Message: "legacy Cursor rules file; the current format is .cursor/rules/*.mdc",
	})
	inv.Components = append(inv.Components, model.Rule{Spec: model.RuleSpec{
		Name: filepath.Base(path), Scope: model.ScopeProject, Path: path,
	}})
	return nil
}

// scanCommands reads project slash commands (<project>/.cursor/commands/*.md).
// Commands are named by their filename — that is the slash the user types —
// and are plain markdown, so a description only appears when the author
// wrote a frontmatter block.
func scanCommands(inv *model.Inventory, dir string) error {
	return mdscan.ScanFlatDir(inv, dir, false,
		func(name, path, description string) {
			inv.Components = append(inv.Components, model.Command{Spec: model.CommandSpec{
				Name: name, Scope: model.ScopeProject, Path: path, Description: description,
			}})
		})
}
