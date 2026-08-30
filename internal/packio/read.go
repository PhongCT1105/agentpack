package packio

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/PhongCT1105/agentpack/internal/secrets"
)

// Pack is a pack directory that has been read and fully validated. Every
// consumer downstream of ReadPack (restore, diff) may assume the manifest
// is schema-valid and the directory passed the secret scan.
type Pack struct {
	Dir      string
	Manifest *Manifest
}

// InvalidPackError reports a pack directory that was readable but failed
// validation — schema issues, secret findings, or both. Consumers must
// refuse to operate on it; the fields carry everything needed to tell the
// user why.
type InvalidPackError struct {
	Dir      string
	Issues   []Issue
	Findings []secrets.Finding
}

func (e *InvalidPackError) Error() string {
	return fmt.Sprintf("pack %s is invalid: %d issue(s), %d suspected secret(s)",
		e.Dir, len(e.Issues), len(e.Findings))
}

// ReadPack reads a pack directory for consumption. It runs the full
// ValidatePack gate first — a pack that would fail `agentpack validate`
// must not be restorable either — and returns *InvalidPackError when the
// gate fails. A plain error means I/O-level failure (directory unreadable).
func ReadPack(dir string) (*Pack, error) {
	issues, findings, _, err := ValidatePack(dir, nil)
	if err != nil {
		return nil, err
	}
	if len(issues) > 0 || len(findings) > 0 {
		return nil, &InvalidPackError{Dir: dir, Issues: issues, Findings: findings}
	}

	data, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		return nil, fmt.Errorf("reading pack: %w", err)
	}
	m, err := DecodeManifest(data)
	if err != nil {
		// ValidatePack already decoded this file, so only a race with a
		// concurrent writer lands here.
		return nil, fmt.Errorf("reading pack: %w", err)
	}
	return &Pack{Dir: dir, Manifest: m}, nil
}
