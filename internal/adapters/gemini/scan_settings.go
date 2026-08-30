package gemini

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

// mcpServersKey is the settings.json key holding MCP server definitions.
// It is modeled as its own component kind, so the settings pass skips it.
const mcpServersKey = "mcpServers"

// portableSettingKeys is the settings.json subset agentpack understands and
// carries into a Setting component. Gemini CLI has shipped two shapes of
// the same file and both are still found on real machines: the original
// flat layout (theme, contextFileName, …) and the newer grouped layout
// whose top-level keys are sections (general, ui, tools, …). Both are
// listed here; a key outside this set is never assumed portable.
//
// settings.json is a mixed file — portable preferences blended with app,
// account, and machine state (docs/research/tool-config-matrix.md,
// observation 3) — so this is an allowlist by design, and it excludes the
// state keys below plus anything a future Gemini release adds.
var portableSettingKeys = map[string]bool{
	// Flat layout.
	"accessibility":                    true,
	"allowMCPServers":                  true,
	"autoAccept":                       true,
	"bugCommand":                       true,
	"chatCompression":                  true,
	"checkpointing":                    true,
	"contextFileName":                  true,
	"coreTools":                        true,
	"customThemes":                     true,
	"disableAutoUpdate":                true,
	"disableUpdateNag":                 true,
	"dnsResolutionOrder":               true,
	"excludeMCPServers":                true,
	"excludeTools":                     true,
	"excludedProjectEnvVars":           true,
	"extensions":                       true,
	"fileFiltering":                    true,
	"hideBanner":                       true,
	"hideTips":                         true,
	"hideWindowTitle":                  true,
	"includeDirectories":               true,
	"loadMemoryFromIncludeDirectories": true,
	"maxSessionTurns":                  true,
	"mcpServerCommand":                 true,
	"memoryImportFormat":               true,
	"model":                            true,
	"preferredEditor":                  true,
	"sandbox":                          true,
	"showMemoryUsage":                  true,
	"summarizeToolOutput":              true,
	"telemetry":                        true,
	"theme":                            true,
	"toolCallCommand":                  true,
	"toolDiscoveryCommand":             true,
	"usageStatisticsEnabled":           true,
	"vimMode":                          true,
	// Grouped layout sections. "model", "telemetry" and "extensions" above
	// carry over unchanged between the two layouts.
	"advanced":     true,
	"context":      true,
	"experimental": true,
	"general":      true,
	"mcp":          true,
	"privacy":      true,
	"tools":        true,
	"ui":           true,
}

// stateSettingKeys are keys Gemini CLI stores in the same file but that
// describe this machine or this account rather than a portable preference:
// which auth mode was selected, which folders were trusted, which one-time
// nudges have been seen, IDE-integration wiring. They are recognized on
// purpose so a scan can say "seen, deliberately not ported" instead of
// lumping them in with keys agentpack simply does not know.
var stateSettingKeys = map[string]bool{
	// Flat layout.
	"folderTrust":                true,
	"folderTrustFeature":         true,
	"hasSeenIdeIntegrationNudge": true,
	"ideMode":                    true,
	"selectedAuthType":           true,
	// Grouped layout: security holds auth selection + folder trust, ide
	// holds the local editor integration state.
	"ide":      true,
	"security": true,
}

// scanSettingsFile reads one settings.json — the mixed file Gemini CLI uses
// for everything at once. Only keys agentpack understands are modeled:
// mcpServers becomes MCP server components, the portable subset above
// becomes a single Setting component, and every other key is reported as a
// warning rather than carried or dropped. A missing file is normal; an
// unparseable one warns instead of failing the scan.
func (a *Adapter) scanSettingsFile(inv *model.Inventory, path string, scope model.Scope) error {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var top map[string]json.RawMessage
	if jsonErr := json.Unmarshal(raw, &top); jsonErr != nil {
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: "not a valid JSON object; settings and mcpServers skipped",
		})
		return nil
	}

	a.appendMCPServers(inv, path, top[mcpServersKey], scope)

	values := map[string]any{}
	var state, unknown []string
	for key, rawValue := range top {
		switch {
		case key == mcpServersKey:
			continue // modeled above as its own component kind
		case stateSettingKeys[key]:
			state = append(state, key)
		case portableSettingKeys[key]:
			var value any
			if valErr := json.Unmarshal(rawValue, &value); valErr != nil {
				unknown = append(unknown, key)
				continue
			}
			values[key] = value
		default:
			unknown = append(unknown, key)
		}
	}

	// Warnings name keys only — never values: settings.json holds API keys
	// and endpoints alongside preferences, and scan output gets pasted into
	// issues.
	if len(state) > 0 {
		sort.Strings(state)
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: fmt.Sprintf("settings keys hold machine or account state; not ported: %s", strings.Join(state, ", ")),
		})
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		inv.Warnings = append(inv.Warnings, model.Warning{
			Path:    path,
			Message: fmt.Sprintf("settings keys agentpack does not model: %s", strings.Join(unknown, ", ")),
		})
	}

	// A file whose only modeled content was mcpServers yields no Setting
	// component: there is nothing portable left to carry.
	if len(values) == 0 {
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
