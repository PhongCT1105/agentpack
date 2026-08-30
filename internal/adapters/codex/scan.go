package codex

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
	"github.com/PhongCT1105/agentpack/internal/adapters/mdscan"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// mcpEntry mirrors one [mcp_servers.<name>] table in ~/.codex/config.toml.
// Codex-specific extras (startup_timeout_sec, …) are ignored here.
type mcpEntry struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	URL     string            `toml:"url"`
}

// Scan reads Codex configuration in the requested scopes into the neutral
// model. It never writes. Codex keeps MCP servers and prompts only in the
// global ~/.codex tree; project scope carries just the repo-root AGENTS.md
// (docs/research/tool-config-matrix.md). Codex also reads AGENTS.md files
// nested deeper in a repo, but agentpack models the root one only — walking
// a whole project tree is not a scan's job.
func (a *Adapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	inv := model.Inventory{Tool: model.ToolCodex}

	if scope.Global && a.home != "" {
		root := filepath.Join(a.home, ".codex")
		if err := a.scanConfigTOML(&inv); err != nil {
			return inv, err
		}
		if err := scanRuleFile(&inv, filepath.Join(root, "AGENTS.md"), model.ScopeGlobal); err != nil {
			return inv, err
		}
		if err := scanPrompts(&inv, filepath.Join(root, "prompts")); err != nil {
			return inv, err
		}
	}
	if scope.ProjectDir != "" {
		if err := scanRuleFile(&inv, filepath.Join(scope.ProjectDir, "AGENTS.md"), model.ScopeProject); err != nil {
			return inv, err
		}
	}

	return inv, nil
}

// scanRuleFile models one AGENTS.md if present.
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

// scanPrompts reads reusable prompts (~/.codex/prompts/*.md) as command
// components; prompts are named by filename, the slash the user types.
func scanPrompts(inv *model.Inventory, dir string) error {
	return mdscan.ScanFlatDir(inv, dir, false,
		func(name, path, description string) {
			inv.Components = append(inv.Components, model.Command{Spec: model.CommandSpec{
				Name: name, Scope: model.ScopeGlobal, Path: path, Description: description,
			}})
		})
}

// scanConfigTOML surgically reads the [mcp_servers.*] tables of
// ~/.codex/config.toml — a mixed file whose other tables (model, profiles,
// history, …) are settings handled elsewhere, never MCP config. A missing
// file is normal; an unparseable one becomes a warning, not a failed scan.
func (a *Adapter) scanConfigTOML(inv *model.Inventory) error {
	path := filepath.Join(a.home, ".codex", "config.toml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var doc struct {
		MCPServers map[string]toml.Primitive `toml:"mcp_servers"`
	}
	md, tomlErr := toml.Decode(string(raw), &doc)
	if tomlErr != nil {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: "not valid TOML; mcp_servers skipped",
		})
		return nil
	}

	names := make([]string, 0, len(doc.MCPServers))
	for name := range doc.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		var entry mcpEntry
		if err := md.PrimitiveDecode(doc.MCPServers[name], &entry); err != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcp_servers.%s has an unexpected shape; skipped", name),
			})
			continue
		}

		var transport model.Transport
		switch {
		case entry.Command != "":
			transport = model.TransportStdio
		case entry.URL != "":
			transport = model.TransportHTTP
		default:
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcp_servers.%s has no command or url; transport unknown", name),
			})
		}

		inv.Components = append(inv.Components, model.MCPServer{Spec: model.MCPServerSpec{
			Name:      name,
			Scope:     model.ScopeGlobal,
			Transport: transport,
			Command:   entry.Command,
			Args:      entry.Args,
			Env:       entry.Env,
			URL:       entry.URL,
		}})
	}
	return nil
}
