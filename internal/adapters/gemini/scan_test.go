package gemini

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// newFixtureAdapter returns an adapter rooted at the scan fixture home.
func newFixtureAdapter(t *testing.T) *Adapter {
	t.Helper()
	a := New()
	a.home = fixtureHomeDir(t)
	// Deterministic dead-server checks: fixture commands (npx, …) must not
	// depend on what the CI machine has installed.
	a.lookPath = lookPathHit
	return a
}

func fixtureHomeDir(t *testing.T) string {
	t.Helper()
	return absFixture(t, filepath.Join("testdata", "scan", "home"))
}

func fixtureProjectDir(t *testing.T) string {
	t.Helper()
	return absFixture(t, filepath.Join("testdata", "scan", "project"))
}

func absFixture(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	return abs
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

func settingByScope(t *testing.T, inv model.Inventory, scope model.Scope) model.Setting {
	t.Helper()
	for _, c := range inv.ByKind(model.KindSetting) {
		if c.Scope() == scope {
			return c.(model.Setting)
		}
	}
	t.Fatalf("no %s-scope setting in inventory", scope)
	return model.Setting{}
}

// componentLines renders an inventory's components as one stable line each
// — kind|scope|name|detail — the golden form the scan tests assert against.
// Detail carries key names and endpoints only: a golden file that could
// print a secret value would be a leak waiting to be pasted somewhere.
func componentLines(inv model.Inventory) []string {
	lines := make([]string, 0, len(inv.Components))
	for _, c := range inv.Components {
		lines = append(lines, fmt.Sprintf("%s|%s|%s|%s", c.Kind(), c.Scope(), c.Name(), componentDetail(c)))
	}
	return lines
}

func componentDetail(c model.Component) string {
	switch v := c.(type) {
	case model.MCPServer:
		detail := string(v.Spec.Transport)
		if v.Spec.Command != "" {
			detail += " " + v.Spec.Command
		} else if v.Spec.URL != "" {
			detail += " " + v.Spec.URL
		}
		if keys := sortedKeys(v.Spec.Env); len(keys) > 0 {
			detail += " env:" + strings.Join(keys, ",")
		}
		if keys := sortedKeys(v.Spec.Headers); len(keys) > 0 {
			detail += " headers:" + strings.Join(keys, ",")
		}
		return detail
	case model.Setting:
		keys := make([]string, 0, len(v.Spec.Values))
		for k := range v.Spec.Values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return strings.Join(keys, ",")
	default:
		return ""
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// warningLines renders warnings as path|message with the fixture roots
// replaced by placeholders, so goldens do not depend on the checkout path.
func warningLines(t *testing.T, inv model.Inventory) []string {
	t.Helper()
	home, project := fixtureHomeDir(t), fixtureProjectDir(t)
	lines := make([]string, 0, len(inv.Warnings))
	for _, w := range inv.Warnings {
		path := strings.ReplaceAll(w.Path, home, "$HOME")
		path = strings.ReplaceAll(path, project, "$PROJECT")
		lines = append(lines, filepath.ToSlash(path)+"|"+w.Message)
	}
	return lines
}

func assertLines(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s:\n got (%d):\n  %s\nwant (%d):\n  %s",
			label, len(got), strings.Join(got, "\n  "), len(want), strings.Join(want, "\n  "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s line %d:\n got %q\nwant %q", label, i, got[i], want[i])
		}
	}
}

// TestScanGlobalInventory pins the whole global-scope inventory of the
// fixture home: components in scan order, then every warning.
func TestScanGlobalInventory(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if inv.Tool != model.ToolGeminiCLI {
		t.Errorf("Inventory.Tool = %q, want %q", inv.Tool, model.ToolGeminiCLI)
	}

	assertLines(t, "global components", componentLines(inv), []string{
		"mcp_server|global|github|stdio npx env:GITHUB_PERSONAL_ACCESS_TOKEN",
		"mcp_server|global|linear|http https://mcp.linear.app/mcp headers:Authorization",
		"mcp_server|global|notion|sse https://mcp.notion.com/sse",
		"setting|global|settings.json|autoAccept,checkpointing,contextFileName,excludeTools," +
			"fileFiltering,preferredEditor,theme,usageStatisticsEnabled,vimMode",
		"rule|global|GEMINI.md|",
	})

	assertLines(t, "global warnings", warningLines(t, inv), []string{
		"$HOME/.gemini/settings.json|mcpServers.github has keys agentpack does not model: cwd, timeout, trust",
		"$HOME/.gemini/settings.json|settings keys hold machine or account state; not ported: " +
			"folderTrust, hasSeenIdeIntegrationNudge, selectedAuthType",
		"$HOME/.gemini/settings.json|settings keys agentpack does not model: customWittyPhrases",
		"$HOME/.gemini/extensions/broken-no-manifest|extension directory has no gemini-extension.json; skipped",
		`$HOME/.gemini/extensions/security-scanner/gemini-extension.json|extension "security-scanner" v1.2.0 ` +
			"is installed but not modeled by agentpack; skipped (it defines 2 MCP server(s) of its own)",
	})
}

// TestScanProjectInventory pins the whole project-scope inventory: the
// grouped-layout settings.json, its MCP server, and GEMINI.md.
func TestScanProjectInventory(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	assertLines(t, "project components", componentLines(inv), []string{
		"mcp_server|project|postgres|stdio ./scripts/pg-mcp.sh env:PGPASSWORD",
		"setting|project|settings.json|context,general,tools,ui",
		"rule|project|GEMINI.md|",
	})

	assertLines(t, "project warnings", warningLines(t, inv), []string{
		"$PROJECT/.gemini/settings.json|settings keys hold machine or account state; not ported: ide, security",
	})
}

func TestScanGlobalAndProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	got := componentNames(inv, model.KindMCPServer)
	want := []string{"github", "linear", "notion", "postgres"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MCP servers = %v, want %v (global sorted, then project)", got, want)
	}
	if n := len(inv.ByKind(model.KindRule)); n != 2 {
		t.Errorf("rules = %d, want global + project GEMINI.md", n)
	}
	if n := len(inv.ByKind(model.KindSetting)); n != 2 {
		t.Errorf("settings = %d, want one document per scope", n)
	}
}

func TestScanComponentPathsAreAbsolute(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	for _, c := range inv.ByKind(model.KindRule) {
		r := c.(model.Rule)
		if !filepath.IsAbs(r.Spec.Path) {
			t.Errorf("rule %q path %q is not absolute", r.Name(), r.Spec.Path)
		}
		if filepath.Base(r.Spec.Path) != "GEMINI.md" {
			t.Errorf("rule path = %q, want a GEMINI.md", r.Spec.Path)
		}
	}
	for _, c := range inv.ByKind(model.KindSetting) {
		s := c.(model.Setting)
		if !filepath.IsAbs(s.Spec.Path) {
			t.Errorf("setting %q path %q is not absolute", s.Name(), s.Spec.Path)
		}
	}
	global := settingByScope(t, inv, model.ScopeGlobal)
	wantPath := filepath.Join(fixtureHomeDir(t), ".gemini", "settings.json")
	if global.Spec.Path != wantPath {
		t.Errorf("global settings path = %q, want %q", global.Spec.Path, wantPath)
	}
}

// The neutral model carries MCP values raw — including Gemini's $VAR
// placeholders — because redaction happens on the way into a pack
// (internal/model). Scan itself must not rewrite or drop them.
func TestScanMCPCarriesRawEntryData(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProjectDir(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	gh := mcpByName(t, inv, "github")
	if gh.Spec.Command != "npx" {
		t.Errorf("github command = %q, want npx", gh.Spec.Command)
	}
	if len(gh.Spec.Args) != 2 || gh.Spec.Args[1] != "@modelcontextprotocol/server-github" {
		t.Errorf("github args = %v", gh.Spec.Args)
	}
	if gh.Spec.Env["GITHUB_PERSONAL_ACCESS_TOKEN"] != "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE" {
		t.Errorf("github env = %v, want the fixture's fake token verbatim", gh.Spec.Env)
	}

	pg := mcpByName(t, inv, "postgres")
	if len(pg.Spec.Args) != 2 || pg.Spec.Args[0] != "--database" {
		t.Errorf("postgres args = %v", pg.Spec.Args)
	}
	// A relative command is never dead-server flagged: resolution depends on
	// the tool's working directory, not agentpack's.
	for _, w := range inv.Warnings {
		if strings.Contains(w.Message, "postgres") {
			t.Errorf("relative command produced a warning: %s", w)
		}
	}
}

func TestScanMissingConfig(t *testing.T) {
	a := New()
	a.home = t.TempDir() // no ~/.gemini at all
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() on empty home error: %v", err)
	}
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("empty home produced components %v warnings %v", inv.Components, inv.Warnings)
	}
}

func TestScanEmptyScope(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Components) != 0 || len(inv.Warnings) != 0 {
		t.Errorf("empty scope produced components %v warnings %v", inv.Components, inv.Warnings)
	}
}
