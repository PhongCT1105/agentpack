package packio

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestReadPackValid(t *testing.T) {
	dir := filepath.Join("testdata", "validate", "good")
	pack, err := ReadPack(dir)
	if err != nil {
		t.Fatalf("ReadPack(good) error: %v", err)
	}
	if pack.Dir != dir {
		t.Errorf("pack.Dir = %q, want %q", pack.Dir, dir)
	}
	if pack.Manifest == nil {
		t.Fatal("pack.Manifest is nil")
	}
	if got := pack.Manifest.Metadata.Name; got != "good-pack" {
		t.Errorf("manifest name = %q, want %q", got, "good-pack")
	}
	if n := len(pack.Manifest.Components.MCPServers); n != 1 {
		t.Errorf("manifest has %d mcp servers, want 1", n)
	}
}

func TestReadPackPreviewFixture(t *testing.T) {
	pack, err := ReadPack(filepath.Join("testdata", "read", "preview"))
	if err != nil {
		t.Fatalf("ReadPack(preview) error: %v", err)
	}
	m := pack.Manifest
	if m.Metadata.Name != "fullstack-startup" {
		t.Errorf("manifest name = %q, want fullstack-startup", m.Metadata.Name)
	}
	counts := map[string]int{
		"skills":      len(m.Components.Skills),
		"mcp_servers": len(m.Components.MCPServers),
		"agents":      len(m.Components.Agents),
		"rules":       len(m.Components.Rules),
		"commands":    len(m.Components.Commands),
		"settings":    len(m.Components.Settings),
	}
	want := map[string]int{
		"skills": 3, "mcp_servers": 2, "agents": 1,
		"rules": 1, "commands": 1, "settings": 1,
	}
	for section, n := range want {
		if counts[section] != n {
			t.Errorf("%s: got %d components, want %d", section, counts[section], n)
		}
	}
}

func TestReadPackMissingDir(t *testing.T) {
	_, err := ReadPack(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("ReadPack(missing dir) succeeded, want error")
	}
	var inv *InvalidPackError
	if errors.As(err, &inv) {
		t.Errorf("missing dir reported as InvalidPackError, want plain I/O error: %v", err)
	}
}

func TestReadPackNotAPack(t *testing.T) {
	_, err := ReadPack(t.TempDir())
	var inv *InvalidPackError
	if !errors.As(err, &inv) {
		t.Fatalf("ReadPack(empty dir) error = %v, want *InvalidPackError", err)
	}
	if len(inv.Issues) == 0 {
		t.Error("InvalidPackError carries no issues for a directory without a manifest")
	}
}

func TestReadPackSchemaViolations(t *testing.T) {
	_, err := ReadPack(filepath.Join("testdata", "validate", "bad"))
	var inv *InvalidPackError
	if !errors.As(err, &inv) {
		t.Fatalf("ReadPack(bad) error = %v, want *InvalidPackError", err)
	}
	if len(inv.Issues) == 0 {
		t.Error("InvalidPackError carries no issues for a schema-violating pack")
	}
}

// Restore must never operate on a pack the secret scanner flags: a leaky
// pack read is refused outright, same as validate failing.
func TestReleaseBlocking_ReadPackRefusesLeakyPack(t *testing.T) {
	_, err := ReadPack(filepath.Join("testdata", "validate", "leaky"))
	var inv *InvalidPackError
	if !errors.As(err, &inv) {
		t.Fatalf("ReadPack(leaky) error = %v, want *InvalidPackError", err)
	}
	if len(inv.Findings) == 0 {
		t.Error("InvalidPackError carries no findings for a leaky pack")
	}
}
