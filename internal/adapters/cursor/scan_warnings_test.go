package cursor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// writeGlobalMCP seeds ~/.cursor/mcp.json in a temporary home.
func writeGlobalMCP(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".cursor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// globalWarnings drops the standing User Rules notice, which every global
// scan emits, so a test can assert on what the files produced.
func globalWarnings(inv model.Inventory) []model.Warning {
	var out []model.Warning
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "User Rules") {
			continue
		}
		out = append(out, w)
	}
	return out
}

func TestScanWarnsUserRulesAreNotPortable(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	found := false
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "User Rules") && strings.Contains(w.Message, "not portable") {
			found = true
		}
	}
	if !found {
		t.Errorf("global scan did not report the User Rules limitation; warnings = %v", inv.Warnings)
	}

	// The limitation is global-only: a project scan says nothing about it.
	projInv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	for _, w := range projInv.Warnings {
		if strings.Contains(w.Message, "User Rules") {
			t.Errorf("project scan reported the global User Rules limitation: %v", w)
		}
	}
}

func TestScanMCPDeadServerWarns(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {
		"alive": {"command": "present-mcp"},
		"ghost": {"command": "ghost-mcp-server"}
	}}`)
	a := New()
	a.home = home
	a.lookPath = func(cmd string) (string, error) {
		if cmd == "ghost-mcp-server" {
			return "", errors.New("not found")
		}
		return "/usr/local/bin/" + cmd, nil
	}

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	// Both servers stay modeled — dead is a warning, not exclusion.
	if n := len(inv.ByKind(model.KindMCPServer)); n != 2 {
		t.Fatalf("got %d MCP servers, want 2", n)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 {
		t.Fatalf("want exactly one dead-server warning, got %v", warnings)
	}
	if w := warnings[0].Message; !strings.Contains(w, "ghost") || !strings.Contains(w, "may be dead") {
		t.Errorf("warning = %q, want it to name ghost as possibly dead", w)
	}
}

func TestScanMCPStdioWithoutCommandIsDead(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {"husk": {"type": "stdio"}}}`)
	a := New()
	a.home = home
	a.lookPath = lookPathHit

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "no command") {
		t.Fatalf("want one stdio-without-command warning, got %v", warnings)
	}
}

func TestScanMCPRelativePathCommandNotFlagged(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {"local": {"command": "./scripts/mcp.sh"}}}`)
	a := New()
	a.home = home
	a.lookPath = lookPathMiss // even a failing lookup must not flag relative paths

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if w := globalWarnings(inv); len(w) != 0 {
		t.Errorf("relative path command produced warnings: %v", w)
	}
}

func TestScanMCPUnknownKeysWarn(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {
		"github": {"command": "gh-mcp", "timeout": 30000, "autoApprove": ["list_repos"]}
	}}`)
	a := New()
	a.home = home
	a.lookPath = lookPathHit

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 {
		t.Fatalf("want exactly one unknown-keys warning, got %v", warnings)
	}
	if w := warnings[0].Message; !strings.HasSuffix(w, "does not model: autoApprove, timeout") {
		t.Errorf("warning = %q, want exactly the sorted unknown keys", w)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 1 {
		t.Errorf("got %d MCP servers, want 1", n)
	}
}

func TestScanMCPUnknownTopLevelKeysWarn(t *testing.T) {
	home := t.TempDir()
	// mcp.json is a dedicated file, so a sibling of mcpServers is config
	// the scan saw and did not model.
	writeGlobalMCP(t, home, `{"mcpServers": {}, "inputs": [{"id": "token"}]}`)
	a := New()
	a.home = home

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "top-level keys agentpack does not model: inputs") {
		t.Fatalf("want one unknown-top-level-key warning, got %v", warnings)
	}
}

func TestScanMalformedMCPJSON(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {`)
	a := New()
	a.home = home

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("malformed mcp.json must warn, not fail scan: %v", err)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "not valid JSON") {
		t.Fatalf("want one not-valid-JSON warning, got %v", warnings)
	}
}

func TestScanMCPEntryWithUnexpectedShape(t *testing.T) {
	home := t.TempDir()
	writeGlobalMCP(t, home, `{"mcpServers": {
		"bad": ["not", "an", "object"],
		"good": {"command": "echo"}
	}}`)
	a := New()
	a.home = home
	a.lookPath = lookPathHit

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if got := componentNames(inv, model.KindMCPServer); len(got) != 1 || got[0] != "good" {
		t.Fatalf("MCP servers = %v, want only [good]", got)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 || !strings.Contains(warnings[0].Message, "bad") {
		t.Fatalf("want one warning naming the bad entry, got %v", warnings)
	}
}

func TestScanWarnsUnmodeledGlobalEntries(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".cursor")
	for _, dir := range []string{"extensions", "cli", "skills", "commands"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"argv.json", "hooks.json", "mcp.json.bak", ".DS_Store"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a := New()
	a.home = home

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	warnings := globalWarnings(inv)
	if len(warnings) != 1 {
		t.Fatalf("want one aggregated unmodeled-entries warning, got %v", warnings)
	}
	// Cursor's own installation state (extensions/, cli/, argv.json), backup
	// debris, and dotfiles stay quiet; a global commands/ or rules/ directory
	// does not, because the config matrix places those at project level only.
	want := "entries agentpack does not model: commands/, hooks.json, skills/"
	if got := warnings[0].Message; got != want {
		t.Errorf("warning = %q, want %q", got, want)
	}
}
