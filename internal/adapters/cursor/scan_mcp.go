package cursor

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

// mcpEntry mirrors one server object in a Cursor mcp.json. Cursor uses the
// cross-tool shape: command/args/env for stdio servers, url/headers for
// remote ones, with type only sometimes spelled out.
type mcpEntry struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// scanMCPFile reads the mcpServers map of a Cursor mcp.json — ~/.cursor for
// global scope, <project>/.cursor for project scope — and appends one
// MCPServer component per entry, in sorted name order. A missing file is
// normal; an unparseable one becomes a warning, not a failed scan.
//
// Env and header values are carried through raw, exactly as the other
// adapters do: the neutral model is what the save-time redactor reads, and
// masking belongs to whatever renders a scan (see cmd/agentpack/scan.go).
// Warnings raised here therefore quote key names and server names only,
// never a value.
func (a *Adapter) scanMCPFile(inv *model.Inventory, path string, scope model.Scope) error {
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

		// Surface keys the neutral model does not carry (Cursor's OAuth
		// `auth` block, timeouts, …): they would otherwise vanish silently
		// on a future save.
		if unknown := unknownKeys(servers[name], knownEntryKeys); len(unknown) > 0 {
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

	// Unlike ~/.claude.json or config.toml, mcp.json is a dedicated file:
	// mcpServers is the only documented key, so anything beside it is
	// config the scan saw and did not model.
	if unknown := unknownKeys(raw, knownTopKeys); len(unknown) > 0 {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: "top-level keys agentpack does not model: " + strings.Join(unknown, ", "),
		})
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

// knownTopKeys is what mcp.json is expected to hold; knownEntryKeys is what
// mcpEntry models. Anything else is surfaced, not silently dropped.
var (
	knownTopKeys   = map[string]bool{"mcpServers": true}
	knownEntryKeys = map[string]bool{
		"type": true, "command": true, "args": true, "env": true,
		"url": true, "headers": true,
	}
)

// unknownKeys lists a JSON object's keys that are absent from known, sorted
// for determinism. A non-object blob yields nil (the caller already handled
// the shape error).
func unknownKeys(raw json.RawMessage, known map[string]bool) []string {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return nil
	}
	var unknown []string
	for k := range keys {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	return unknown
}
