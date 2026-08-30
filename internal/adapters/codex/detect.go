package codex

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Detect reports whether Codex CLI is present: either the `codex` binary
// resolves on PATH or user-level config exists (~/.codex). Config without a
// binary still holds portable components worth scanning.
func (a *Adapter) Detect() (installed bool, version string, err error) {
	binFound := false
	if bin, lookErr := a.lookPath("codex"); lookErr == nil {
		binFound = true
		if out, verErr := a.runVersion(bin); verErr == nil {
			version = parseVersion(out)
		}
	}

	configFound := false
	if a.home != "" {
		if _, statErr := os.Stat(filepath.Join(a.home, ".codex")); statErr == nil {
			configFound = true
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			// A found binary already decides installed; don't let an
			// unreadable config path erase that.
			return binFound, version, statErr
		}
	}

	return binFound || configFound, version, nil
}
