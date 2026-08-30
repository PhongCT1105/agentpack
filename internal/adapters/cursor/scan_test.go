package cursor

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
	a.home = fixtureHome(t)
	// Deterministic dead-server checks: fixture commands (npx, …) must not
	// depend on what the CI machine has installed.
	a.lookPath = lookPathHit
	return a
}

func fixtureHome(t *testing.T) string    { return absFixture(t, "home") }
func fixtureProject(t *testing.T) string { return absFixture(t, "project") }

func absFixture(t *testing.T, name string) string {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "scan", name))
	if err != nil {
		t.Fatal(err)
	}
	return dir
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

// TestScanInventoryGolden pins the whole scanned inventory — every
// component and every warning, in order — for the fixture machine. The
// fixture is a realistic Cursor layout: global MCP config plus a project
// carrying MCP servers, .mdc rules, a legacy .cursorrules, slash commands,
// and the debris and unmodeled surfaces a real .cursor directory collects.
func TestScanInventoryGolden(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if inv.Tool != model.ToolCursor {
		t.Errorf("Inventory.Tool = %q, want %q", inv.Tool, model.ToolCursor)
	}

	wantComponents := []string{
		"mcp_server github (global) stdio npx -y @modelcontextprotocol/server-github env=[GITHUB_API_URL GITHUB_TOKEN]",
		"mcp_server linear (global) http https://mcp.linear.app/mcp headers=[Authorization]",
		"mcp_server supabase (global) sse https://mcp.supabase.com/sse headers=[Authorization]",
		"mcp_server internal-docs (project) http https://docs.internal.example.com/mcp headers=[X-Api-Key]",
		"mcp_server playwright (project) stdio npx -y @playwright/mcp@latest",
		"rule always-safety.mdc (project) <project>/.cursor/rules/always-safety.mdc",
		"rule api-conventions.mdc (project) <project>/.cursor/rules/api-conventions.mdc",
		"rule scratch-notes.mdc (project) <project>/.cursor/rules/scratch-notes.mdc",
		"rule testing.mdc (project) <project>/.cursor/rules/testing.mdc",
		"rule .cursorrules (project) <project>/.cursorrules",
		"command review (project) <project>/.cursor/commands/review.md Structured review of the current diff",
		"command write-tests (project) <project>/.cursor/commands/write-tests.md",
	}
	assertLines(t, "components", renderComponents(t, inv), wantComponents)

	wantWarnings := []string{
		"~/.cursor/mcp.json: mcpServers.supabase has keys agentpack does not model: auth",
		"~/.cursor: entries agentpack does not model: skills/",
		"Cursor User Rules (Settings → Rules) live in Cursor's internal settings storage, not a config file; they are not scanned and not portable",
		"<project>/.cursor/rules/README.md: Cursor's rules system reads only .mdc files; skipped",
		"<project>/.cursor/rules/always-safety.mdc: frontmatter agentpack does not model: alwaysApply, description",
		"<project>/.cursor/rules/api-conventions.mdc: frontmatter agentpack does not model: description, globs",
		"<project>/.cursor/rules/frontend: nested rule folders are not modeled; skipped",
		"<project>/.cursor/rules/testing.mdc: frontmatter agentpack does not model: description, globs",
		"<project>/.cursorrules: legacy Cursor rules file; the current format is .cursor/rules/*.mdc",
		"<project>/.cursor: entries agentpack does not model: environment.json",
	}
	assertLines(t, "warnings", renderWarnings(t, inv), wantWarnings)
}

// TestScanNeverRendersSecretValues is the hygiene half of the golden test:
// every fake credential in the fixtures embeds FAKE, so a rendering that
// shows names and shapes but no values must not contain it.
func TestScanNeverRendersSecretValues(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	rendered := strings.Join(append(renderComponents(t, inv), renderWarnings(t, inv)...), "\n")
	if strings.Contains(rendered, "FAKE") {
		t.Errorf("a fixture credential value reached rendered scan output:\n%s", rendered)
	}
}

func TestScanMCPGlobal(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindMCPServer)
	want := []string{"github", "linear", "supabase"}
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
	if gh.Spec.Command != "npx" || len(gh.Spec.Args) != 2 {
		t.Errorf("github command/args = %q %v", gh.Spec.Command, gh.Spec.Args)
	}
	// Raw values reach the neutral model — the save-time redactor is what
	// reads them; masking belongs to whatever renders a scan.
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
	if ln.Spec.Headers["Authorization"] != "Bearer FAKE-TOKEN-VALUE" {
		t.Errorf("linear headers = %v", ln.Spec.Headers)
	}

	sb := mcpByName(t, inv, "supabase")
	if sb.Spec.Transport != model.TransportSSE {
		t.Errorf("supabase transport = %q, want explicit sse", sb.Spec.Transport)
	}

	// A global scan must not invent project-scoped components.
	for _, c := range inv.Components {
		if c.Scope() != model.ScopeGlobal {
			t.Errorf("global-only scan produced %s-scoped %s %q", c.Scope(), c.Kind(), c.Name())
		}
	}
}

func TestScanMCPProject(t *testing.T) {
	a := newFixtureAdapter(t)
	inv, err := a.Scan(model.ScanScope{ProjectDir: fixtureProject(t)})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}

	got := componentNames(inv, model.KindMCPServer)
	want := []string{"internal-docs", "playwright"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("MCP servers = %v, want %v (sorted)", got, want)
	}
	docs := mcpByName(t, inv, "internal-docs")
	if docs.Scope() != model.ScopeProject {
		t.Errorf("internal-docs scope = %q, want project", docs.Scope())
	}
	// ${VAR} placeholders are values like any other: carried, not resolved.
	if docs.Spec.Headers["X-Api-Key"] != "${INTERNAL_DOCS_KEY}" {
		t.Errorf("internal-docs headers = %v", docs.Spec.Headers)
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

func TestScanMissingConfig(t *testing.T) {
	a := New()
	a.home = t.TempDir()
	inv, err := a.Scan(model.ScanScope{Global: true, ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Scan() error: %v", err)
	}
	if len(inv.Components) != 0 {
		t.Errorf("empty home produced %d components", len(inv.Components))
	}
	// The User Rules limitation is a property of Cursor, not of the files
	// present, so it is the one warning an empty machine still gets.
	if len(inv.Warnings) != 1 || !strings.Contains(inv.Warnings[0].Message, "User Rules") {
		t.Errorf("want only the User Rules warning, got %v", inv.Warnings)
	}
}

// renderComponents flattens the inventory into stable, path-abbreviated
// lines. MCP servers render their transport, command/url, and the *names* of
// their env vars and headers — never a value, matching what a scan is
// allowed to print.
func renderComponents(t *testing.T, inv model.Inventory) []string {
	t.Helper()
	lines := make([]string, 0, len(inv.Components))
	for _, c := range inv.Components {
		line := fmt.Sprintf("%s %s (%s)", c.Kind(), c.Name(), c.Scope())
		if detail := componentDetail(t, c); detail != "" {
			line += " " + detail
		}
		lines = append(lines, line)
	}
	return lines
}

func componentDetail(t *testing.T, c model.Component) string {
	t.Helper()
	switch v := c.(type) {
	case model.MCPServer:
		parts := []string{string(v.Spec.Transport)}
		switch {
		case v.Spec.Command != "":
			parts = append(parts, append([]string{v.Spec.Command}, v.Spec.Args...)...)
		case v.Spec.URL != "":
			parts = append(parts, v.Spec.URL)
		}
		if keys := sortedKeys(v.Spec.Env); len(keys) > 0 {
			parts = append(parts, fmt.Sprintf("env=%v", keys))
		}
		if keys := sortedKeys(v.Spec.Headers); len(keys) > 0 {
			parts = append(parts, fmt.Sprintf("headers=%v", keys))
		}
		return strings.Join(parts, " ")
	case model.Rule:
		return abbrev(t, v.Spec.Path)
	case model.Command:
		return strings.TrimSpace(abbrev(t, v.Spec.Path) + " " + v.Spec.Description)
	default:
		t.Fatalf("unexpected component kind %q in cursor inventory", c.Kind())
		return ""
	}
}

func renderWarnings(t *testing.T, inv model.Inventory) []string {
	t.Helper()
	lines := make([]string, 0, len(inv.Warnings))
	for _, w := range inv.Warnings {
		if w.Path == "" {
			lines = append(lines, w.Message)
			continue
		}
		lines = append(lines, abbrev(t, w.Path)+": "+w.Message)
	}
	return lines
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// abbrev rewrites an absolute fixture path as ~/… or <project>/… and
// normalizes separators, so the golden lines read the same on every OS.
func abbrev(t *testing.T, path string) string {
	t.Helper()
	p := filepath.ToSlash(path)
	for _, sub := range []struct{ root, label string }{
		{filepath.ToSlash(fixtureHome(t)), "~"},
		{filepath.ToSlash(fixtureProject(t)), "<project>"},
	} {
		if p == sub.root {
			return sub.label
		}
		if strings.HasPrefix(p, sub.root+"/") {
			return sub.label + strings.TrimPrefix(p, sub.root)
		}
	}
	return p
}

func assertLines(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s:\n got (%d):\n%s\nwant (%d):\n%s",
			what, len(got), strings.Join(got, "\n"), len(want), strings.Join(want, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d]:\n got %q\nwant %q", what, i, got[i], want[i])
		}
	}
}
