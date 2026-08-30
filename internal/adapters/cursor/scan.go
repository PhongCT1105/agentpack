package cursor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/adapters/mdscan"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// Scan reads Cursor configuration in the requested scopes into the neutral
// model. It never writes.
//
// Cursor splits cleanly by scope: ~/.cursor/mcp.json is the only global file
// agentpack can read, while rules, commands, and project MCP servers live
// under <project>/.cursor (docs/research/tool-config-matrix.md). Global
// "User Rules" are deliberately missing from the inventory — they are stored
// inside Cursor's own settings database, not a config file — so a global
// scan records that limitation as a warning instead of leaving the user to
// read the empty result as "nothing configured".
func (a *Adapter) Scan(scope model.ScanScope) (model.Inventory, error) {
	inv := model.Inventory{Tool: model.ToolCursor}

	if scope.Global && a.home != "" {
		root := filepath.Join(a.home, ".cursor")
		steps := []func() error{
			func() error { return a.scanMCPFile(&inv, filepath.Join(root, "mcp.json"), model.ScopeGlobal) },
			func() error { return warnUnmodeledEntries(&inv, root, globalModeled, globalAppInternal) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
		warnUserRules(&inv)
	}
	if scope.ProjectDir != "" {
		proj := scope.ProjectDir
		root := filepath.Join(proj, ".cursor")
		steps := []func() error{
			func() error { return a.scanMCPFile(&inv, filepath.Join(root, "mcp.json"), model.ScopeProject) },
			func() error { return scanRules(&inv, filepath.Join(root, "rules")) },
			func() error { return scanLegacyRules(&inv, filepath.Join(proj, ".cursorrules")) },
			func() error { return scanCommands(&inv, filepath.Join(root, "commands")) },
			func() error { return warnUnmodeledEntries(&inv, root, projectModeled, nil) },
		}
		for _, step := range steps {
			if err := step(); err != nil {
				return inv, err
			}
		}
	}

	return inv, nil
}

// warnUserRules records Cursor's one global surface agentpack cannot reach.
// User Rules (Settings → Rules) are kept in Cursor's internal settings
// storage rather than a config file, so they can be neither read nor ported
// — the config matrix documents them as not portable in v1. Reporting them
// on every global scan is deliberate: staying silent would make a machine
// with carefully written User Rules look like a machine with none.
func warnUserRules(inv *model.Inventory) {
	inv.Warnings = append(inv.Warnings, model.Warning{
		Message: "Cursor User Rules (Settings → Rules) live in Cursor's internal settings storage, not a config file; they are not scanned and not portable",
	})
}

// globalModeled and projectModeled are the entries of a .cursor directory
// this adapter reads, per scope. Cursor's global surface is MCP config only;
// rules and commands are project-level (docs/research/tool-config-matrix.md).
// Keeping the sets scope-specific is what makes a stray ~/.cursor/commands
// or ~/.cursor/rules directory surface as a warning instead of being skipped
// as "already modeled".
var (
	globalModeled  = map[string]bool{"mcp.json": true}
	projectModeled = map[string]bool{"mcp.json": true, "rules": true, "commands": true}
)

// globalAppInternal names the ~/.cursor entries that hold Cursor's own
// installation state — the extension cache, the CLI shim, launcher argv —
// never portable config. They are ignored silently; reporting them on every
// scan would drown the warnings that matter.
var globalAppInternal = map[string]bool{
	"extensions":      true,
	"extensions.json": true,
	"argv.json":       true,
	"cli":             true,
	"logs":            true,
	"machineid":       true,
}

// warnUnmodeledEntries reports, in one warning, the entries of a .cursor
// directory this adapter does not model. Cursor keeps growing new surfaces
// (skills/, hooks.json, environment.json) and a scan must never drop what it
// saw in silence; a single sorted list keeps that honest without one warning
// per file. Backup debris, dotfiles, and (for the global directory) Cursor's
// own installation state are skipped.
func warnUnmodeledEntries(inv *model.Inventory, dir string, modeled, ignored map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}

	var unmodeled []string
	for _, e := range entries {
		name := e.Name()
		if modeled[name] || ignored[name] || mdscan.IsDebris(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		unmodeled = append(unmodeled, name)
	}
	if len(unmodeled) == 0 {
		return nil
	}
	sort.Strings(unmodeled)
	inv.Warnings = append(inv.Warnings, model.Warning{
		Path:    dir,
		Message: "entries agentpack does not model: " + strings.Join(unmodeled, ", "),
	})
	return nil
}
