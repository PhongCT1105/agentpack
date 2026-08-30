package gemini

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Detect reports whether Gemini CLI is present on this machine and, when
// the binary is on PATH, its version. The tool counts as installed if
// either the `gemini` binary resolves or user-level config exists
// (~/.gemini) — config without a binary still holds portable components
// worth scanning.
func (a *Adapter) Detect() (installed bool, version string, err error) {
	binFound := false
	if bin, lookErr := a.lookPath("gemini"); lookErr == nil {
		binFound = true
		if out, verErr := a.runVersion(bin); verErr == nil {
			version = parseVersion(out)
		}
		// A failing version command is not a detection failure; the binary
		// existing is what matters.
	}

	configFound := false
	if a.home != "" {
		if _, statErr := os.Stat(filepath.Join(a.home, ".gemini")); statErr == nil {
			configFound = true
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			// A found binary already decides installed; don't let an
			// unreadable config path erase that.
			return binFound, version, statErr
		}
	}

	return binFound || configFound, version, nil
}
