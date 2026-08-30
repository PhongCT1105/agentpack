package claudecode

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Detect reports whether Claude Code is present on this machine and, when
// the binary is on PATH, its version. The tool counts as installed if either
// the `claude` binary resolves or user-level config exists (~/.claude or
// ~/.claude.json) — config without a binary still holds portable components
// worth scanning.
func (a *Adapter) Detect() (installed bool, version string, err error) {
	binFound := false
	if bin, lookErr := a.lookPath("claude"); lookErr == nil {
		binFound = true
		if out, verErr := a.runVersion(bin); verErr == nil {
			version = parseVersion(out)
		}
		// A failing version command is not a detection failure; the binary
		// existing is what matters.
	}

	configFound := false
	if a.home != "" {
		for _, p := range []string{
			filepath.Join(a.home, ".claude"),
			filepath.Join(a.home, ".claude.json"),
		} {
			if _, statErr := os.Stat(p); statErr == nil {
				configFound = true
				break
			} else if !errors.Is(statErr, fs.ErrNotExist) {
				// A found binary already decides installed; don't let an
				// unreadable config path erase that.
				return binFound, version, statErr
			}
		}
	}

	return binFound || configFound, version, nil
}
