// Package gemini is the adapter for Gemini CLI. Paths and file formats
// follow docs/research/tool-config-matrix.md, the source of truth for where
// Gemini CLI stores each component kind.
package gemini

import (
	"os"
	"os/exec"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// Adapter reads Gemini CLI configuration into the neutral model. Scanning
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
	return model.ToolGeminiCLI
}

// parseVersion extracts the bare version from `gemini --version` output,
// e.g. "0.5.3\n" → "0.5.3": the first field starting with a digit. Gemini
// CLI prints the bare version today; scanning fields also survives a
// future "gemini-cli 0.6.0" style banner.
func parseVersion(raw string) string {
	for _, f := range strings.Fields(raw) {
		if f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	return ""
}
