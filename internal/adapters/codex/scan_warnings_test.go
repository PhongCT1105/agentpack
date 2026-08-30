package codex

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func writeCodexConfig(t *testing.T, home, content string) {
	t.Helper()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScanMCPDeadServerWarns(t *testing.T) {
	home := t.TempDir()
	writeCodexConfig(t, home, `
[mcp_servers.alive]
command = "present-mcp"

[mcp_servers.ghost]
command = "ghost-mcp-server"
`)
	a := New()
	a.home = home
	a.lookPath = func(cmd string) (string, error) {
		if cmd == "ghost-mcp-server" {
			return "", errors.New("not found")
		}
		return "/home/user/.local/bin/" + cmd, nil
	}

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 2 {
		t.Fatalf("got %d MCP servers, want 2 (dead is a warning, not exclusion)", n)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want exactly one dead-server warning, got %v", inv.Warnings)
	}
	w := inv.Warnings[0].Message
	if !strings.Contains(w, "ghost") || !strings.Contains(w, "may be dead") {
		t.Errorf("warning = %q, want it to name ghost as possibly dead", w)
	}
}

func TestScanMCPUnknownKeysWarn(t *testing.T) {
	home := t.TempDir()
	writeCodexConfig(t, home, `
model = "gpt-5-codex"

[mcp_servers.github]
command = "gh-mcp"
startup_timeout_sec = 20
enabled = true
`)
	a := New()
	a.home = home
	a.lookPath = lookPathHit

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want exactly one unknown-keys warning, got %v", inv.Warnings)
	}
	// The exact suffix pins both the sorted key list and that top-level
	// settings keys (model) are not reported as MCP unknowns.
	w := inv.Warnings[0].Message
	if !strings.HasSuffix(w, "does not model: enabled, startup_timeout_sec") {
		t.Errorf("warning = %q, want exactly the sorted unknown MCP keys", w)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 1 {
		t.Errorf("got %d MCP servers, want 1", n)
	}
}
