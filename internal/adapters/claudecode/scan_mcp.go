package claudecode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// mcpEntry mirrors one server object in Claude Code's mcpServers maps
// (~/.claude.json and .mcp.json share the shape).
type mcpEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// scanMCPFile surgically reads the mcpServers key of path — a mixed file
// like ~/.claude.json also holds app state, which is deliberately ignored —
// and appends one MCPServer component per entry, in sorted name order.
// A missing file is normal; an unparseable one becomes a warning, not a
// failed scan.
// checkLocalScope additionally inspects the per-project mcpServers entries
// that only ~/.claude.json carries ("local scope" in the config matrix):
// they are real config agentpack does not model yet, so non-empty ones must
// surface as warnings rather than vanish silently.
func (a *Adapter) scanMCPFile(inv *model.Inventory, path string, scope model.Scope, checkLocalScope bool) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: "not valid JSON; mcpServers skipped",
		})
		return nil
	}

	var servers map[string]json.RawMessage
	if rawServers, ok := top["mcpServers"]; ok {
		if err := json.Unmarshal(rawServers, &servers); err != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: "mcpServers has an unexpected shape; skipped",
			})
			servers = nil
		}
	}

	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		var entry mcpEntry
		if err := json.Unmarshal(servers[name], &entry); err != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has an unexpected shape; skipped", name),
			})
			continue
		}

		// Surface keys the neutral model does not carry: they would
		// otherwise vanish silently on a future save.
		if unknown := unknownEntryKeys(servers[name]); len(unknown) > 0 {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has keys agentpack does not model: %s", name, strings.Join(unknown, ", ")),
			})
		}

		transport := model.Transport(entry.Type)
		switch {
		case entry.Type == "" && entry.Command != "":
			transport = model.TransportStdio
		case entry.Type == "" && entry.URL != "":
			transport = model.TransportHTTP
		case entry.Type == "":
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has no type, command, or url; transport unknown", name),
			})
		case !transport.Valid():
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has unrecognized transport %q", name, entry.Type),
			})
		}

		// Dead-server check: a stdio server whose command does not resolve
		// on this machine is likely stale config.
		if transport == model.TransportStdio {
			if entry.Command == "" {
				inv.Warnings = append(inv.Warnings, model.Warning{
					Path:    path,
					Message: fmt.Sprintf("mcpServers.%s is stdio but has no command; server is dead", name),
				})
			} else if a.commandMissing(entry.Command) {
				inv.Warnings = append(inv.Warnings, model.Warning{
					Path:    path,
					Message: fmt.Sprintf("mcpServers.%s command %q not found on this machine; server may be dead", name, entry.Command),
				})
			}
		}

		inv.Components = append(inv.Components, model.MCPServer{Spec: model.MCPServerSpec{
			Name:      name,
			Scope:     scope,
			Transport: transport,
			Command:   entry.Command,
			Args:      entry.Args,
			Env:       entry.Env,
			URL:       entry.URL,
			Headers:   entry.Headers,
		}})
	}

	if checkLocalScope {
		warnLocalScopeMCP(inv, path, top["projects"])
	}
	return nil
}

// commandMissing reports whether a stdio command fails to resolve. Relative
// path commands (./scripts/mcp.sh) are never flagged: their resolution
// depends on the tool's working directory, not agentpack's.
func (a *Adapter) commandMissing(cmd string) bool {
	if strings.ContainsAny(cmd, `/\`) && !filepath.IsAbs(cmd) {
		return false
	}
	_, err := a.lookPath(cmd)
	return err != nil
}

// knownEntryKeys is what mcpEntry models; anything else in a server object
// is surfaced, not silently dropped.
var knownEntryKeys = map[string]bool{
	"type": true, "command": true, "args": true, "env": true,
	"url": true, "headers": true,
}

// unknownEntryKeys lists a server object's keys the neutral model does not
// carry, sorted for determinism. A non-object blob yields nil (the caller
// already handled the shape error).
func unknownEntryKeys(raw json.RawMessage) []string {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil
	}
	var unknown []string
	for k := range keys {
		if !knownEntryKeys[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// warnLocalScopeMCP reports per-project ("local scope") MCP servers nested
// under projects.<dir>.mcpServers in ~/.claude.json. Best effort: if the
// projects blob has an unexpected shape it is app state we ignore.
func warnLocalScopeMCP(inv *model.Inventory, path string, rawProjects json.RawMessage) {
	if len(rawProjects) == 0 {
		return
	}
	var projects map[string]struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if err := json.Unmarshal(rawProjects, &projects); err != nil {
		return
	}
	dirs := make([]string, 0, len(projects))
	for dir := range projects {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if len(projects[dir].MCPServers) > 0 {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("local-scope MCP servers under projects[%q] are not modeled; skipped", dir),
			})
		}
	}
}

// ~/.claude.json holds the user-scope mcpServers map; .mcp.json at the
// project root is the shareable project-scope file
// (docs/research/tool-config-matrix.md).
func (a *Adapter) globalMCPPath() string      { return filepath.Join(a.home, ".claude.json") }
func projectMCPPath(projectDir string) string { return filepath.Join(projectDir, ".mcp.json") }
