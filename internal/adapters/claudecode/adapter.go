// Package claudecode is the adapter for Claude Code. Paths and file formats
// follow docs/research/tool-config-matrix.md, the source of truth for where
// Claude Code stores each component kind.
package claudecode

import (
	"os"
	"os/exec"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// Adapter reads Claude Code configuration into the neutral model. Scanning
// never writes; all filesystem roots are injectable for fixture-driven tests.
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
	return model.ToolClaudeCode
}

// parseVersion extracts the bare version from `claude --version` output,
// e.g. "2.0.44 (Claude Code)\n" → "2.0.44".
func parseVersion(raw string) string {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}
