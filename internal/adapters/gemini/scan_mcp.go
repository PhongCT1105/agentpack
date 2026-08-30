package gemini

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// mcpEntry mirrors one server object in a Gemini CLI mcpServers map
// (~/.gemini/settings.json and .gemini/settings.json share the shape).
// Gemini splits the remote transports across two keys: `url` is an SSE
// endpoint, `httpUrl` a streamable-HTTP one. Gemini-specific extras (cwd,
// timeout, trust, includeTools, …) are not modeled here — they surface as
// warnings so they cannot vanish silently.
type mcpEntry struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`     // SSE endpoint
	HTTPURL string            `json:"httpUrl"` // streamable HTTP endpoint
	Headers map[string]string `json:"headers"`
}

// appendMCPServers models the mcpServers map of a settings.json into one
// MCPServer component per entry, in sorted name order. raw is the map as it
// appeared in the (mixed) settings file; an empty or misshapen map warns
// rather than failing the scan.
//
// Env and header values are carried through verbatim — including the
// `$VAR` / `${VAR}` placeholders Gemini expands at load time — because the
// neutral model is raw scanned data by contract (internal/model), and
// redaction happens on the way into a pack. Nothing here ever prints a
// value: warnings name keys only.
func (a *Adapter) appendMCPServers(inv *model.Inventory, path string, raw json.RawMessage, scope model.Scope) {
	if len(raw) == 0 {
		return
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(raw, &servers); err != nil {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: "mcpServers has an unexpected shape; skipped",
		})
		return
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

		// Surface keys the neutral model does not carry (cwd, timeout,
		// trust, oauth, …): they would otherwise vanish silently on a
		// future save.
		if unknown := unknownEntryKeys(servers[name]); len(unknown) > 0 {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has keys agentpack does not model: %s", name, strings.Join(unknown, ", ")),
			})
		}

		// Transport is implied by which endpoint key is set; Gemini has no
		// explicit `type` field. Precedence matches Gemini's own resolution
		// order (command, then httpUrl, then url), and a server declaring
		// more than one is reported rather than quietly narrowed.
		var transport model.Transport
		url := ""
		switch {
		case entry.Command != "":
			transport = model.TransportStdio
		case entry.HTTPURL != "":
			transport, url = model.TransportHTTP, entry.HTTPURL
		case entry.URL != "":
			transport, url = model.TransportSSE, entry.URL
		default:
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s has no command, httpUrl, or url; transport unknown", name),
			})
		}
		if declared := declaredTransports(entry); len(declared) > 1 {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path: path,
				Message: fmt.Sprintf("mcpServers.%s declares more than one transport (%s); scanned as %s",
					name, strings.Join(declared, ", "), transport),
			})
		}

		// Dead-server check: a stdio server whose command does not resolve
		// on this machine is likely stale config.
		if transport == model.TransportStdio && a.commandMissing(entry.Command) {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    path,
				Message: fmt.Sprintf("mcpServers.%s command %q not found on this machine; server may be dead", name, entry.Command),
			})
		}

		inv.Components = append(inv.Components, model.MCPServer{Spec: model.MCPServerSpec{
			Name:      name,
			Scope:     scope,
			Transport: transport,
			Command:   entry.Command,
			Args:      entry.Args,
			Env:       entry.Env,
			URL:       url,
			Headers:   entry.Headers,
		}})
	}
}

// declaredTransports lists the endpoint keys an entry sets, in precedence
// order. More than one means the config is ambiguous.
func declaredTransports(entry mcpEntry) []string {
	var declared []string
	if entry.Command != "" {
		declared = append(declared, "command")
	}
	if entry.HTTPURL != "" {
		declared = append(declared, "httpUrl")
	}
	if entry.URL != "" {
		declared = append(declared, "url")
	}
	return declared
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
	"command": true, "args": true, "env": true,
	"url": true, "httpUrl": true, "headers": true,
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
