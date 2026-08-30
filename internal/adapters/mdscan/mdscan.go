// Package mdscan holds the markdown-scanning helpers shared by adapters:
// YAML frontmatter extraction and flat *.md directory scans. Several tools
// store agents/commands/prompts as flat markdown files with an optional
// frontmatter block, so the mechanics live here and each adapter supplies
// only its layout knowledge.
package mdscan

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
	"gopkg.in/yaml.v3"
)

// Frontmatter is the subset of a markdown file's leading YAML block that
// scanning cares about.
type Frontmatter struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
}

// ParseFrontmatter extracts a leading YAML frontmatter block (--- ... ---).
// A file without frontmatter yields a zero value and nil error; a block that
// fails to parse yields a zero value and the parse error.
func ParseFrontmatter(md []byte) (Frontmatter, error) {
	body := bytes.TrimPrefix(md, []byte{0xEF, 0xBB, 0xBF}) // tolerate a UTF-8 BOM
	if !bytes.HasPrefix(body, []byte("---\n")) && !bytes.HasPrefix(body, []byte("---\r\n")) {
		return Frontmatter{}, nil
	}
	rest := body[3:] // past the opening ---
	end := bytes.Index(rest, []byte("\n---"))
	if end < 0 {
		return Frontmatter{}, nil // unterminated: treat as no frontmatter
	}
	var fm Frontmatter
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return Frontmatter{}, err
	}
	return fm, nil
}

// ScanFlatDir reads flat <dir>/*.md files. Each becomes a component via
// emit; subdirectories are real content the flat model skips, so they warn.
// useFrontmatterName lets a file's frontmatter `name:` override the filename
// stem (agent definitions work that way; commands/prompts are named by the
// filename the user types).
func ScanFlatDir(inv *model.Inventory, dir string, useFrontmatterName bool,
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
		fm, fmErr := ParseFrontmatter(raw)
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
