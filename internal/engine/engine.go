// Package engine orchestrates the adapters: scan today; plan/apply,
// lockfile, and backups in Phase 3.
package engine

import (
	"github.com/PhongCT1105/agentpack/internal/model"
)

// Adapter is what the engine needs from a tool adapter. Plan() joins the
// interface in Phase 3 (docs/architecture.md).
type Adapter interface {
	ID() model.ToolID
	Detect() (installed bool, version string, err error)
	Scan(scope model.ScanScope) (model.Inventory, error)
}

// ScanResult is one tool's outcome: detection state plus, when installed,
// its scanned inventory. Err carries a detect/scan failure for that tool
// only — one broken tool must not hide the others.
type ScanResult struct {
	Tool      model.ToolID
	Installed bool
	Version   string
	Inventory model.Inventory
	Err       error
}

// ScanAll detects every adapter's tool and scans the installed ones,
// preserving adapter order. Failures are recorded per result, never
// returned: rendering decides how to show them.
func ScanAll(adapters []Adapter, scope model.ScanScope) []ScanResult {
	results := make([]ScanResult, 0, len(adapters))
	for _, a := range adapters {
		res := ScanResult{Tool: a.ID()}
		installed, version, err := a.Detect()
		if err != nil {
			res.Err = err
			results = append(results, res)
			continue
		}
		res.Installed = installed
		res.Version = version
		if installed {
			inv, scanErr := a.Scan(scope)
			res.Inventory = inv
			res.Err = scanErr
		}
		results = append(results, res)
	}
	return results
}
