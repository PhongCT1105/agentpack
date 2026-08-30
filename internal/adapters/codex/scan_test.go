package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func newFixtureAdapter(t *testing.T) *Adapter {
	t.Helper()
	home, err := filepath.Abs(filepath.Join("testdata", "scan", "home"))
	if err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	return a
}

func componentNames(inv model.Inventory, kind model.Kind) []string {
	var names []string
	for _, c := range inv.ByKind(kind) {
		names = append(names, c.Name())
	}
	return names
}

func mcpByName(t *testing.T, inv model.Inventory, name string) model.MCPServer {
	t.Helper()
	for _, c := range inv.ByKind(model.KindMCPServer) {
		if c.Name() == name {
			return c.(model.MCPServer)
		}
	}
	t.Fatalf("MCP server %q not found; have %v", name, componentNames(inv, model.KindMCPServer))
	return model.MCPServer{}
}

func TestScanMCPFromConfigTOML(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if inv.Tool != model.ToolCodex {
		t.Errorf("Inventory.Tool = %q, want %q", inv.Tool, model.ToolCodex)
	}

	got := componentNames(inv, model.KindMCPServer)
	want := []string{"github", "linear"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MCP servers = %v, want %v (sorted)", got, want)
	}

	gh := mcpByName(t, inv, "github")
	if gh.Scope() != model.ScopeGlobal {
		t.Errorf("github scope = %q, want global", gh.Scope())
	}
	if gh.Spec.Transport != model.TransportStdio {
		t.Errorf("github transport = %q, want inferred stdio", gh.Spec.Transport)
	}
	if gh.Spec.Command != "npx" {
		t.Errorf("github command = %q, want npx", gh.Spec.Command)
	}
	if len(gh.Spec.Args) != 2 || gh.Spec.Args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("github args = %v", gh.Spec.Args)
	}
	if gh.Spec.Env["GITHUB_TOKEN"] != "ghp_FAKEFAKE" {
		t.Errorf("github env = %v, want fixture fake token", gh.Spec.Env)
	}

	ln := mcpByName(t, inv, "linear")
	if ln.Spec.Transport != model.TransportHTTP {
		t.Errorf("linear transport = %q, want inferred http", ln.Spec.Transport)
	}
	if ln.Spec.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("linear url = %q", ln.Spec.URL)
	}

	// config.toml's other tables (model, profiles, …) must not leak into
	// the inventory, and auth.json must never be modeled.
	if len(inv.Components) != 2 {
		t.Errorf("inventory has %d components, want exactly the 2 MCP servers: %v",
			len(inv.Components), inv.Components)
	}
}

func TestScanGlobalOnlyProjectScopeEmpty(t *testing.T) {
	a := newFixtureAdapter(t)
	// Codex has no project-level MCP config (tool-config-matrix); a project
	// scan must yield nothing for MCP rather than inventing scope.
	inv, err := a.Scan(model.ScanScope{ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 0 {
		t.Errorf("project scan produced %d MCP servers, want 0", n)
	}
}

func TestScanMissingConfig(t *testing.T) {
	a := New()
	a.home = t.TempDir()
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("empty home produced components %v warnings %v", inv.Components, inv.Warnings)
	}
}

func TestScanMalformedTOML(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = [unclosed"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("malformed config.toml must warn, not fail scan: %v", err)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "not valid TOML") {
		t.Fatalf("want one not-valid-TOML warning, got %v", inv.Warnings)
	}
}

func TestScanEntryWithUnexpectedShape(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "[mcp_servers.bad]\ncommand = 42\n\n[mcp_servers.good]\ncommand = \"echo\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	got := componentNames(inv, model.KindMCPServer)
	if len(got) != 1 || got[0] != "good" {
		t.Fatalf("MCP servers = %v, want only [good]", got)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "bad") {
		t.Fatalf("want one warning naming the bad entry, got %v", inv.Warnings)
	}
}

func TestScanEntryWithoutCommandOrURL(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[mcp_servers.hollow]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	h := mcpByName(t, inv, "hollow")
	if h.Spec.Transport != "" {
		t.Errorf("transport = %q, want empty for uninferrable server", h.Spec.Transport)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want one warning for uninferrable transport, got %v", inv.Warnings)
	}
}
