package mdscan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestIsDebris(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"settings.json.bak", true},
		{"settings.json.bak2", true},
		{"config.toml.backup", true},
		{"CLAUDE.md.orig", true},
		{"prompts.old", true},
		{"review.md~", true},
		{"review.md", false},
		{"backup-strategy.md", false}, // "bak" inside a word is not debris
		{"SKILL.md", false},
		{"old-review.md", false},
	}
	for _, tt := range tests {
		if got := IsDebris(tt.name); got != tt.want {
			t.Errorf("IsDebris(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestScanFlatDirIgnoresDebris(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "review.md"), []byte("real"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Debris: a .bak file that still ends in .md, and a debris-named subdir.
	if err := os.WriteFile(filepath.Join(dir, "review.bak.md"), []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "archive.old"), 0o755); err != nil {
		t.Fatal(err)
	}

	var inv model.Inventory
	var names []string
	err := ScanFlatDir(&inv, dir, false, func(name, path, description string) {
		names = append(names, name)
	})
	if err != nil {
		t.Fatalf("ScanFlatDir error: %v", err)
	}
	if len(names) != 1 || names[0] != "review" {
		t.Errorf("scanned = %v, want only [review]", names)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("debris produced warnings: %v", inv.Warnings)
	}
}
