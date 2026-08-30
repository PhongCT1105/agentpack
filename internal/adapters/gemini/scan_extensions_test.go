package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// writeExtension creates ~/.gemini/extensions/<dir>/gemini-extension.json.
func writeExtension(t *testing.T, home, dir, manifest string) {
	t.Helper()
	extDir := filepath.Join(home, ".gemini", "extensions", dir)
	if err := os.MkdirAll(extDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest == "" {
		return
	}
	if err := os.WriteFile(filepath.Join(extDir, "gemini-extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}

func scanHome(t *testing.T, home string) model.Inventory {
	t.Helper()
	a := New()
	a.home = home
	a.lookPath = lookPathHit
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	return inv
}

// An extension is inventoried by name and version, but never turned into
// components: its MCP servers belong to the extension, not to the user's
// portable config, and `save` must not publish them as if the user had
// configured them.
func TestScanExtensionsAreReportedNotModeled(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	for _, c := range inv.Components {
		switch c.Name() {
		case "semgrep", "trivy", "security-scanner":
			t.Errorf("extension content leaked into the inventory as %s %q", c.Kind(), c.Name())
		}
	}
	// The extension ships its own GEMINI.md; only the user's own rule files
	// are modeled.
	if n := len(inv.ByKind(model.KindRule)); n != 1 {
		t.Errorf("rules = %v, want only ~/.gemini/GEMINI.md", componentNames(inv, model.KindRule))
	}

	var found string
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "security-scanner") {
			found = w.Message
		}
	}
	if !strings.Contains(found, "v1.2.0") || !strings.Contains(found, "2 MCP server(s)") {
		t.Errorf("extension warning = %q, want it to name the version and server count", found)
	}
}

func TestScanExtensionsWithoutManifestWarn(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	found := false
	for _, w := range inv.Warnings {
		if filepath.Base(w.Path) == "broken-no-manifest" &&
			strings.Contains(w.Message, "no gemini-extension.json") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning for the extension dir without a manifest; warnings = %v", inv.Warnings)
	}
}

// Backup debris (extensions/legacy-linter.bak/) is ignored silently:
// warning about every stale copy would drown the warnings that matter.
func TestScanExtensionsIgnoreDebris(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	for _, w := range inv.Warnings {
		if strings.Contains(w.String(), "legacy-linter") {
			t.Errorf("debris extension dir surfaced: %s", w)
		}
	}
}

func TestScanExtensionsUnnamedAndMalformed(t *testing.T) {
	home := t.TempDir()
	writeExtension(t, home, "unnamed", `{"version": "0.1.0"}`)
	writeExtension(t, home, "corrupt", `{"name": "corrupt",`)
	writeExtension(t, home, "stray-file", "")
	if err := os.WriteFile(filepath.Join(home, ".gemini", "extensions", ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	inv := scanHome(t, home)
	if len(inv.Components) != 0 {
		t.Errorf("extensions produced components: %v", inv.Components)
	}
	if len(inv.Warnings) != 3 {
		t.Fatalf("want one warning per extension dir (3), got %v", inv.Warnings)
	}
	// Directory order is os.ReadDir's: corrupt, stray-file, unnamed.
	if !strings.Contains(inv.Warnings[0].Message, "not a valid manifest") {
		t.Errorf("corrupt manifest warning = %q", inv.Warnings[0].Message)
	}
	if !strings.Contains(inv.Warnings[1].Message, "no gemini-extension.json") {
		t.Errorf("manifest-less dir warning = %q", inv.Warnings[1].Message)
	}
	// An unnamed manifest still installs under its directory name.
	if !strings.Contains(inv.Warnings[2].Message, `"unnamed" v0.1.0`) {
		t.Errorf("unnamed extension warning = %q", inv.Warnings[2].Message)
	}
}

func TestScanExtensionsMissingDir(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".gemini"), 0o755); err != nil {
		t.Fatal(err)
	}
	inv := scanHome(t, home)
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("~/.gemini without extensions produced %v / %v", inv.Components, inv.Warnings)
	}
}
