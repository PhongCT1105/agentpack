// Package codex is the adapter for Codex CLI. Paths and file formats follow
// docs/research/tool-config-matrix.md, the source of truth for where Codex
// stores each component kind.
package codex

import (
	"os"
	"os/exec"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// Adapter reads Codex CLI configuration into the neutral model. Scanning
// never writes; all filesystem roots are injectable for fixture-driven tests.
type Adapter struct {
	home       string
	lookPath   func(file string) (string, error)
	runVersion func(bin string) (string, error)
}

// New returns an Adapter wired to the real environment.
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
	return model.ToolCodex
}

// parseVersion extracts the bare version from `codex --version` output,
// e.g. "codex-cli 0.45.0\n" → "0.45.0": the first field starting with a digit.
func parseVersion(raw string) string {
	for _, f := range strings.Fields(raw) {
		if f[0] >= '0' && f[0] <= '9' {
			return f
		}
	}
	return ""
}
