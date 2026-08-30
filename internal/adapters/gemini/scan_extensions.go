package gemini

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/PhongCT1105/agentpack/internal/adapters/mdscan"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// extensionManifest is the part of ~/.gemini/extensions/<name>/
// gemini-extension.json a scan reads: enough to name what is installed.
// The rest of the manifest (contextFileName, excludeTools, …) belongs to
// the extension, not to the user's portable config.
type extensionManifest struct {
	Name       string                     `json:"name"`
	Version    string                     `json:"version"`
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
}

// scanExtensions inventories ~/.gemini/extensions/<name>/gemini-extension.json.
//
// An extension is an installed bundle — it ships its own MCP servers,
// context file and tool filters — and the neutral model has no kind for
// one: porting it means reinstalling it from its source, not copying a
// tree, and flattening its servers into MCPServer components would let
// `save` publish them as if the user had configured them. So each is
// reported as a warning naming what it holds. Seen, named, not modeled —
// never silently dropped (docs/architecture.md, principle 3).
//
// The directory layout is the one marked *(verify)* in
// docs/research/tool-config-matrix.md; a subdirectory without a
// gemini-extension.json is reported as unrecognized rather than guessed at.
func scanExtensions(inv *model.Inventory, dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	for _, e := range entries {
		if mdscan.IsDebris(e.Name()) {
			continue
		}
		extDir := filepath.Join(dir, e.Name())
		info, statErr := os.Stat(extDir) // follows symlinks, unlike e.IsDir()
		if statErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    extDir,
				Message: "unreadable entry in extensions directory; skipped",
			})
			continue
		}
		if !info.IsDir() {
			continue // stray files (e.g. .DS_Store) are not extensions
		}
		if abs, absErr := filepath.Abs(extDir); absErr == nil {
			extDir = abs
		}

		manifestPath := filepath.Join(extDir, "gemini-extension.json")
		raw, readErr := os.ReadFile(manifestPath)
		if errors.Is(readErr, fs.ErrNotExist) {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    extDir,
				Message: "extension directory has no gemini-extension.json; skipped",
			})
			continue
		}
		if readErr != nil {
			return readErr
		}
		var manifest extensionManifest
		if jsonErr := json.Unmarshal(raw, &manifest); jsonErr != nil {
			inv.Warnings = append(inv.Warnings, model.Warning{
				Path:    manifestPath,
				Message: "gemini-extension.json is not a valid manifest; extension skipped",
			})
			continue
		}

		name := manifest.Name
		if name == "" {
			name = e.Name() // an unnamed manifest still installs by directory
		}
		msg := fmt.Sprintf("extension %q", name)
		if manifest.Version != "" {
			msg += " v" + manifest.Version
		}
		msg += " is installed but not modeled by agentpack; skipped"
		if n := len(manifest.MCPServers); n > 0 {
			msg += fmt.Sprintf(" (it defines %d MCP server(s) of its own)", n)
		}
		inv.Warnings = append(inv.Warnings, model.Warning{Path: manifestPath, Message: msg})
	}
	return nil
}
