package claudecode

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestScanMCPDeadServerWarns(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {
		"alive": {"type": "stdio", "command": "present-mcp"},
		"ghost": {"type": "stdio", "command": "ghost-mcp-server"}
	}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
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
	// Both servers stay modeled — dead is a warning, not exclusion.
	if n := len(inv.ByKind(model.KindMCPServer)); n != 2 {
		t.Fatalf("got %d MCP servers, want 2", n)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want exactly one dead-server warning, got %v", inv.Warnings)
	}
	w := inv.Warnings[0].Message
	if !strings.Contains(w, "ghost") || !strings.Contains(w, "may be dead") {
		t.Errorf("warning = %q, want it to name ghost as possibly dead", w)
	}
}

func TestScanMCPStdioWithoutCommandIsDead(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {"husk": {"type": "stdio"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	a.lookPath = lookPathHit

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "no command") {
		t.Fatalf("want one stdio-without-command warning, got %v", inv.Warnings)
	}
}

func TestScanMCPRelativePathCommandNotFlagged(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {"local": {"type": "stdio", "command": "./scripts/mcp.sh"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	a.lookPath = lookPathMiss // even a failing lookup must not flag relative paths

	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("relative path command produced warnings: %v", inv.Warnings)
	}
}

func TestScanMCPUnknownKeysWarn(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {
		"github": {"type": "stdio", "command": "gh-mcp", "timeout": 30000, "autoRestart": true}
	}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
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
	w := inv.Warnings[0].Message
	if !strings.Contains(w, "autoRestart, timeout") {
		t.Errorf("warning = %q, want sorted unknown keys autoRestart, timeout", w)
	}
	// The server itself is still modeled.
	if n := len(inv.ByKind(model.KindMCPServer)); n != 1 {
		t.Errorf("got %d MCP servers, want 1", n)
	}
}

func TestScanSkillsIgnoresDebris(t *testing.T) {
	home := t.TempDir()
	skills := filepath.Join(home, ".claude", "skills")
	real := filepath.Join(skills, "real-skill")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "SKILL.md"), []byte("# Real"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Debris: a .bak-named directory (no SKILL.md inside) must neither warn
	// nor become a component.
	debris := filepath.Join(skills, "real-skill.bak")
	if err := os.MkdirAll(debris, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(debris, "junk.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := New()
	a.home = home
	a.lookPath = lookPathHit
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	got := componentNames(inv, model.KindSkill)
	if len(got) != 1 || got[0] != "real-skill" {
		t.Errorf("skills = %v, want only real-skill", got)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("debris produced warnings: %v", inv.Warnings)
	}
}
