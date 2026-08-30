package cursor

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Detect reports whether Cursor is present on this machine and, when the
// launcher is on PATH, its version. Cursor counts as installed if either the
// `cursor` binary resolves or user-level config exists (~/.cursor). Both
// halves matter: Cursor is a GUI editor whose shell command is opt-in
// ("Install 'cursor' command in PATH"), so a configured machine often has no
// binary on PATH, while a fresh install has the binary and no ~/.cursor yet.
func (a *Adapter) Detect() (installed bool, version string, err error) {
	binFound := false
	if bin, lookErr := a.lookPath("cursor"); lookErr == nil {
		binFound = true
		if out, verErr := a.runVersion(bin); verErr == nil {
			version = parseVersion(out)
		}
		// A failing version command is not a detection failure; the binary
		// existing is what matters.
	}

	configFound := false
	if a.home != "" {
		if _, statErr := os.Stat(filepath.Join(a.home, ".cursor")); statErr == nil {
			configFound = true
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			// A found binary already decides installed; don't let an
			// unreadable config path erase that.
			return binFound, version, statErr
		}
	}

	return binFound || configFound, version, nil
}
