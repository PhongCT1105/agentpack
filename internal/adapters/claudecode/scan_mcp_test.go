package claudecode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

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

func TestScanMCPGlobal(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	// ~/.claude.json is a mixed file (app state + mcpServers); only the
	// mcpServers key must be modeled, in sorted name order.
	got := componentNames(inv, model.KindMCPServer)
	want := []string{"filesystem", "github", "linear"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("global MCP servers = %v, want %v", got, want)
	}

	gh := mcpByName(t, inv, "github")
	if gh.Scope() != model.ScopeGlobal {
		t.Errorf("scope = %q, want global", gh.Scope())
	}
	if gh.Spec.Transport != model.TransportStdio {
		t.Errorf("github transport = %q, want stdio (explicit type)", gh.Spec.Transport)
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

	// filesystem has no explicit type but has a command → stdio inferred.
	fsrv := mcpByName(t, inv, "filesystem")
	if fsrv.Spec.Transport != model.TransportStdio {
		t.Errorf("filesystem transport = %q, want inferred stdio", fsrv.Spec.Transport)
	}

	// linear is an http server with a header.
	ln := mcpByName(t, inv, "linear")
	if ln.Spec.Transport != model.TransportHTTP {
		t.Errorf("linear transport = %q, want http", ln.Spec.Transport)
	}
	if ln.Spec.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("linear url = %q", ln.Spec.URL)
	}
	if ln.Spec.Headers["Authorization"] != "Bearer FAKE-TOKEN-VALUE" {
		t.Errorf("linear headers = %v", ln.Spec.Headers)
	}
	if ln.Spec.Command != "" {
		t.Errorf("linear command = %q, want empty for http transport", ln.Spec.Command)
	}

	// The fixture's projects["/home/user/projects/demo"].mcpServers holds a
	// local-scope server agentpack does not model: it must not become a
	// component but must be reported. The empty project must not warn.
	for _, c := range inv.ByKind(model.KindMCPServer) {
		if c.Name() == "local-notes" {
			t.Error("local-scope server was modeled as a component")
		}
	}
	localWarnings := 0
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "local-scope MCP servers") {
			localWarnings++
			if !strings.Contains(w.Message, "/home/user/projects/demo") {
				t.Errorf("local-scope warning does not name the project: %q", w.Message)
			}
		}
	}
	if localWarnings != 1 {
		t.Errorf("want exactly 1 local-scope warning, got %d (warnings: %v)", localWarnings, inv.Warnings)
	}
}

func TestScanMCPWrongTypedMCPServersKey(t *testing.T) {
	home := t.TempDir()
	cfg := `{"numStartups": 1, "mcpServers": ["not", "a", "map"]}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "unexpected shape") {
		t.Fatalf("want one unexpected-shape warning, got %v", inv.Warnings)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 0 {
		t.Errorf("wrong-typed mcpServers produced %d components", n)
	}
}

func TestScanMCPProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindMCPServer)
	if len(got) != 1 || got[0] != "playwright" {
		t.Fatalf("project MCP servers = %v, want [playwright]", got)
	}
	pw := mcpByName(t, inv, "playwright")
	if pw.Scope() != model.ScopeProject {
		t.Errorf("scope = %q, want project", pw.Scope())
	}
	// ${VAR} expansion strings pass through verbatim at scan time.
	if pw.Spec.Env["PW_LICENSE"] != "${PW_LICENSE}" {
		t.Errorf("env = %v, want verbatim ${PW_LICENSE}", pw.Spec.Env)
	}
}

func TestScanMCPScopesDoNotCross(t *testing.T) {
	a := newFixtureAdapter(t)

	globalOnly, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range globalOnly.ByKind(model.KindMCPServer) {
		if c.Name() == "playwright" {
			t.Error("project server leaked into global-only scan")
		}
	}

	projectOnly, err := a.Scan(model.ScanScope{ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range projectOnly.ByKind(model.KindMCPServer) {
		if c.Name() == "github" {
			t.Error("global server leaked into project-only scan")
		}
	}
}

func TestScanMCPMissingFiles(t *testing.T) {
	a := New()
	a.home = t.TempDir()
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if n := len(inv.ByKind(model.KindMCPServer)); n != 0 {
		t.Errorf("missing config files produced %d MCP servers", n)
	}
	if len(inv.Warnings) != 0 {
		t.Errorf("missing config files produced warnings: %v", inv.Warnings)
	}
}

func TestScanMCPMalformedClaudeJSON(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("malformed ~/.claude.json must warn, not fail scan: %v", err)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want one warning for malformed ~/.claude.json, got %v", inv.Warnings)
	}
	if filepath.Base(inv.Warnings[0].Path) != ".claude.json" {
		t.Errorf("warning path = %q, want the .claude.json path", inv.Warnings[0].Path)
	}
}

func TestScanMCPUnknownTransportWarns(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {"weird": {"type": "websocket", "url": "wss://example.test/ws"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	// The server is still modeled (preserved), with a warning (reported).
	w := mcpByName(t, inv, "weird")
	if string(w.Spec.Transport) != "websocket" {
		t.Errorf("transport = %q, want raw websocket preserved", w.Spec.Transport)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want one warning for unknown transport, got %v", inv.Warnings)
	}
}

func TestScanMCPServerWithoutCommandOrURL(t *testing.T) {
	home := t.TempDir()
	cfg := `{"mcpServers": {"hollow": {}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.home = home
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	// No way to infer a transport: modeled with empty transport + warning.
	h := mcpByName(t, inv, "hollow")
	if h.Spec.Transport != "" {
		t.Errorf("transport = %q, want empty for uninferrable server", h.Spec.Transport)
	}
	if len(inv.Warnings) != 1 {
		t.Fatalf("want one warning for uninferrable transport, got %v", inv.Warnings)
	}
}
