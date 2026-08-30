// Package cursor is the adapter for Cursor. Paths and file formats follow
// docs/research/tool-config-matrix.md, the source of truth for where Cursor
// stores each component kind.
package cursor

import (
	"os"
	"os/exec"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// Adapter reads Cursor configuration into the neutral model. Scanning never
// writes; all filesystem roots are injectable for fixture-driven tests.
type Adapter struct {
	home       string                            // user home dir (~)
	lookPath   func(file string) (string, error) // exec.LookPath
	runVersion func(bin string) (string, error)  // runs `<bin> --version`
}

// New returns an Adapter wired to the real environment. If the home
// directory cannot be determined it is left empty, which detect/scan treat
// as "no user-level config".
func New() *Adapter {
	home, _ := os.UserHomeDir()
	return &Adapter{
		home:     home,
		lookPath: exec.LookPath,
		runVersion: func(bin string) (string, error) {
			out, err := exec.Command(bin, "--version").Output()
			return string(out), err
		},
	}
}

// ID returns the canonical tool id.
func (a *Adapter) ID() model.ToolID {
	return model.ToolCursor
}

// parseVersion extracts the bare version from `cursor --version` output.
// Cursor's launcher follows the VS Code convention of printing version,
// commit hash, and architecture on separate lines ("1.7.44\n<sha>\narm64\n"),
// so the first field that starts with a digit is the version — a rule that
// also survives a "Cursor 1.7.44" style banner.
func parseVersion(raw string) string {
	for _, f := range strings.Fields(raw) {
		if f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	return ""
}
