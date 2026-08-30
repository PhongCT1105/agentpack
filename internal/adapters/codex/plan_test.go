package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// Fake credential values all carry FAKE so the repository's secret scanner can
// tell a fixture from a leak (docs/security.md → seeded fixtures).
const (
	fakeToken       = "ghp_FAKEFAKEFAKEFAKEFAKE"
	fakeHeaderToken = "sbp_FAKEFAKEFAKEFAKE"
)

// packRule and packServer build what restore's pack→component wiring hands
// the adapter: a neutral component carrying the manifest-only data (render:,
// credentials:) on the model types themselves.
func packRule(r model.Rule, render map[model.ToolID]string) model.Rule {
	r.Spec.Render = render
	return r
}

func packServer(s model.MCPServer, creds []model.Credential) model.MCPServer {
	s.Spec.Credentials = creds
	return s
}

// planHome returns an adapter and options planning against a throwaway home,
// so no test can plan against the machine's real one.
func planHome(t *testing.T) (*Adapter, engine.PlanOpts) {
	t.Helper()
	a := New()
	a.home = "" // force the tests to plan against PlanOpts.Home
	return a, engine.PlanOpts{Home: t.TempDir()}
}

// bundled writes content into a pack directory and returns the pack-relative
// path, the way a manifest's bundled: source names it.
func bundled(t *testing.T, packDir, rel, content string) string {
	t.Helper()
	full := filepath.Join(packDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return rel
}

func codexPath(home string, parts ...string) string {
	return filepath.Join(append([]string{home, ".codex"}, parts...)...)
}

func plan(t *testing.T, a *Adapter, comps []model.Component, opts engine.PlanOpts) (engine.Plan, []model.Warning) {
	t.Helper()
	p, warnings, err := a.Plan(comps, opts)
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("Plan() produced an invalid plan: %v", err)
	}
	if p.Tool != model.ToolCodex {
		t.Errorf("Plan.Tool = %q, want %q", p.Tool, model.ToolCodex)
	}
	return p, warnings
}

func warningsText(warnings []model.Warning) string {
	var b strings.Builder
	for _, w := range warnings {
		b.WriteString(w.String())
		b.WriteString("\n")
	}
	return b.String()
}

// mergeOpFor returns the single merge operation at the given key path.
func mergeOpFor(t *testing.T, p engine.Plan, keyPath ...string) engine.Op {
	t.Helper()
	want := strings.Join(keyPath, ".")
	var found []engine.Op
	for _, op := range p.Ops {
		if op.Kind == engine.OpMergeValue && strings.Join(op.KeyPath, ".") == want {
			found = append(found, op)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one merge at %q, got %d:\n%s", want, len(found), p)
	}
	return found[0]
}

// entryOf reads a merge operation's value as a TOML table.
func entryOf(t *testing.T, op engine.Op) map[string]any {
	t.Helper()
	entry, ok := op.Value.(map[string]any)
	if !ok {
		t.Fatalf("merge value at %s is %T, want a table", strings.Join(op.KeyPath, "."), op.Value)
	}
	return entry
}

func stdioServer(name string, scope model.Scope) model.MCPServer {
	return model.MCPServer{Spec: model.MCPServerSpec{
		Name:      name,
		Scope:     scope,
		Transport: model.TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-github"},
		Env:       map[string]string{"GITHUB_API_URL": "https://api.github.com"},
	}}
}

func remoteServer(name string) model.MCPServer {
	return model.MCPServer{Spec: model.MCPServerSpec{
		Name:      name,
		Scope:     model.ScopeGlobal,
		Transport: model.TransportHTTP,
		URL:       "https://mcp.supabase.com/mcp",
		Headers:   map[string]string{"X-Client-Info": "agentpack"},
	}}
}

// TestPlanMCPServerMergesAtKeyPath is the load-bearing one: config.toml is a
// mixed file, so a server must arrive as a surgical merge at
// mcp_servers.<name> and never as a write of the whole document.
func TestPlanMCPServerMergesAtKeyPath(t *testing.T) {
	a, opts := planHome(t)
	p, warnings := plan(t, a, []model.Component{stdioServer("github", model.ScopeGlobal)}, opts)

	if len(p.Ops) != 1 {
		t.Fatalf("want exactly one operation for one server, got %d:\n%s", len(p.Ops), p)
	}
	op := p.Ops[0]
	if op.Kind != engine.OpMergeValue {
		t.Errorf("op kind = %q, want %q — replacing config.toml would take the user's settings with it", op.Kind, engine.OpMergeValue)
	}
	if want := codexPath(opts.Home, "config.toml"); op.Path != want {
		t.Errorf("op path = %q, want %q", op.Path, want)
	}
	if got := strings.Join(op.KeyPath, "."); got != "mcp_servers.github" {
		t.Errorf("key path = %q, want mcp_servers.github", got)
	}
	if op.Format != engine.FormatTOML {
		t.Errorf("format = %q, want %q", op.Format, engine.FormatTOML)
	}
	if op.Strategy != engine.MergeSet {
		t.Errorf("strategy = %q, want %q (the key path already names one server)", op.Strategy, engine.MergeSet)
	}

	entry := entryOf(t, op)
	if entry[keyCommand] != "npx" {
		t.Errorf("entry command = %v, want npx", entry[keyCommand])
	}
	if env, ok := entry[keyEnv].(map[string]any); !ok || env["GITHUB_API_URL"] != "https://api.github.com" {
		t.Errorf("entry env = %v, want the non-secret env carried through", entry[keyEnv])
	}
	if len(warnings) != 0 {
		t.Errorf("plain server produced warnings: %s", warningsText(warnings))
	}

	// No operation may write config.toml wholesale, whatever else a plan grows.
	for _, op := range p.Ops {
		if op.Path == codexPath(opts.Home, "config.toml") && op.Kind != engine.OpMergeValue {
			t.Errorf("op %q writes config.toml as a file; it must be a merge", op.Kind)
		}
	}
}

// TestPlanOpsPerComponentKind pins the target path and operation kind for each
// component kind Codex can place.
func TestPlanOpsPerComponentKind(t *testing.T) {
	packDir := t.TempDir()
	ruleRel := bundled(t, packDir, "rules/conventions.md", "# conventions\n")
	promptRel := bundled(t, packDir, "prompts/review.md", "review this diff\n")

	tests := []struct {
		name       string
		component  model.Component
		project    string
		wantOps    []string // "<kind> <path relative to home, or @project/…>"
		wantWarned string
	}{
		{
			name: "global rule lands in ~/.codex/AGENTS.md",
			component: model.Rule{Spec: model.RuleSpec{
				Name: "conventions", Scope: model.ScopeGlobal, Path: ruleRel,
			}},
			wantOps: []string{"create_file .codex/AGENTS.md"},
		},
		{
			name: "render map picks the file codex consumes",
			component: packRule(model.Rule{Spec: model.RuleSpec{
					Name: "conventions", Scope: model.ScopeGlobal, Path: ruleRel,
				}}, map[model.ToolID]string{
					model.ToolClaudeCode: "CLAUDE.md",
					model.ToolCodex:      "AGENTS.md",
				}),
			wantOps: []string{"create_file .codex/AGENTS.md"},
		},
		{
			name: "project rule lands at the project root",
			component: model.Rule{Spec: model.RuleSpec{
				Name: "conventions", Scope: model.ScopeProject, Path: ruleRel,
			}},
			project: "PROJECT",
			wantOps: []string{"create_file @project/AGENTS.md"},
		},
		{
			name: "command becomes a prompt file, with its directory",
			component: model.Command{Spec: model.CommandSpec{
				Name: "review", Scope: model.ScopeGlobal, Path: promptRel,
			}},
			wantOps: []string{"create_dir .codex/prompts", "create_file .codex/prompts/review.md"},
		},
		{
			name: "portable settings merge into config.toml",
			component: model.Setting{Spec: model.SettingSpec{
				Name: "codex-defaults", Scope: model.ScopeGlobal,
				Values: map[string]any{"model": "gpt-5-codex"},
			}},
			wantOps: []string{"merge_value .codex/config.toml"},
		},
		{
			name: "a rule renders outside the target root",
			component: packRule(model.Rule{Spec: model.RuleSpec{
					Name: "escape", Scope: model.ScopeGlobal, Path: ruleRel,
				}}, map[model.ToolID]string{model.ToolCodex: "../../.ssh/authorized_keys"}),
			wantWarned: "not a path inside the target directory",
		},
		{
			name: "a project rule with no project directory",
			component: model.Rule{Spec: model.RuleSpec{
				Name: "conventions", Scope: model.ScopeProject, Path: ruleRel,
			}},
			wantWarned: "no project directory",
		},
		{
			name: "a prompt that cannot be a filename",
			component: model.Command{Spec: model.CommandSpec{
				Name: "../escape", Scope: model.ScopeGlobal, Path: promptRel,
			}},
			wantWarned: "cannot be a prompt filename",
		},
		{
			name: "a bundled file that is not there",
			component: model.Rule{Spec: model.RuleSpec{
				Name: "missing", Scope: model.ScopeGlobal, Path: "rules/gone.md",
			}},
			wantWarned: "could not be read",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, opts := planHome(t)
			opts.PackDir = packDir
			if tc.project == "PROJECT" {
				opts.ProjectDir = t.TempDir()
			}

			p, warnings := plan(t, a, []model.Component{tc.component}, opts)

			var got []string
			for _, op := range p.Ops {
				path := op.Path
				switch {
				case opts.ProjectDir != "" && strings.HasPrefix(path, opts.ProjectDir+string(filepath.Separator)):
					path = "@project/" + filepath.ToSlash(strings.TrimPrefix(path, opts.ProjectDir+string(filepath.Separator)))
				case strings.HasPrefix(path, opts.Home+string(filepath.Separator)):
					path = filepath.ToSlash(strings.TrimPrefix(path, opts.Home+string(filepath.Separator)))
				}
				got = append(got, string(op.Kind)+" "+path)
			}
			if strings.Join(got, "; ") != strings.Join(tc.wantOps, "; ") {
				t.Errorf("ops = %v, want %v", got, tc.wantOps)
			}

			if tc.wantWarned == "" {
				if len(warnings) != 0 {
					t.Errorf("unexpected warnings: %s", warningsText(warnings))
				}
				return
			}
			if text := warningsText(warnings); !strings.Contains(text, tc.wantWarned) {
				t.Errorf("warnings = %q, want one containing %q", text, tc.wantWarned)
			}
		})
	}
}

// TestPlanSkillsAndAgentsWarn: Codex has no native home for either, so they
// must degrade into a named warning rather than an invented path.
func TestPlanSkillsAndAgentsWarn(t *testing.T) {
	a, opts := planHome(t)
	comps := []model.Component{
		model.Skill{Spec: model.SkillSpec{Name: "brainstorming", Scope: model.ScopeGlobal, Dir: t.TempDir()}},
		model.Agent{Spec: model.AgentSpec{Name: "db-migrator", Scope: model.ScopeGlobal, Path: "agents/db-migrator.md"}},
	}
	p, warnings := plan(t, a, comps, opts)

	if !p.IsEmpty() {
		t.Fatalf("skills and agents produced operations, want none:\n%s", p)
	}
	if len(warnings) != 2 {
		t.Fatalf("want one warning per skipped component, got %d: %s", len(warnings), warningsText(warnings))
	}
	text := warningsText(warnings)
	for _, want := range []string{"brainstorming", "db-migrator", "no native location"} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings %q must mention %q", text, want)
		}
	}
}

// TestPlanCredentialInjection covers every injection point: the two Codex can
// reference by variable name, and the one it cannot.
func TestPlanCredentialInjection(t *testing.T) {
	tests := []struct {
		name        string
		server      model.MCPServer
		creds       []model.Credential
		resolved    map[string]string
		wantEntry   map[string]any // key -> want (compared with %v)
		wantAbsent  []string
		wantWarned  string
		wantNoValue string // this string must appear nowhere in the planned entry
	}{
		{
			name:   "stdio credential is referenced by variable name",
			server: stdioServer("github", model.ScopeGlobal),
			creds: []model.Credential{{
				Env: "GITHUB_TOKEN", Description: "GitHub PAT",
			}},
			resolved:    map[string]string{"GITHUB_TOKEN": fakeToken},
			wantEntry:   map[string]any{keyEnvVars: "[GITHUB_TOKEN]"},
			wantWarned:  `env_vars = ["GITHUB_TOKEN"] references GITHUB_TOKEN by name`,
			wantNoValue: fakeToken,
		},
		{
			name:   "unresolved stdio credential still plans the server",
			server: stdioServer("github", model.ScopeGlobal),
			creds:  []model.Credential{{Env: "GITHUB_TOKEN"}},
			// no resolved value at all
			wantEntry:  map[string]any{keyEnvVars: "[GITHUB_TOKEN]", keyCommand: "npx"},
			wantWarned: "has no resolved value",
		},
		{
			name:       "remote credential becomes bearer_token_env_var",
			server:     remoteServer("supabase"),
			creds:      []model.Credential{{Env: "SUPABASE_TOKEN"}},
			resolved:   map[string]string{"SUPABASE_TOKEN": fakeToken},
			wantEntry:  map[string]any{keyBearerEnv: "SUPABASE_TOKEN"},
			wantWarned: "exported in the environment Codex runs in",
			// Indirection means the secret never reaches the file.
			wantNoValue: fakeToken,
		},
		{
			name:   "header credential falls back to the resolved value",
			server: remoteServer("supabase"),
			creds: []model.Credential{{
				Header: "Authorization", Format: "Bearer {value}",
			}},
			resolved: map[string]string{"Authorization": fakeHeaderToken},
			wantEntry: map[string]any{
				keyHTTPHeaders: "map[Authorization:Bearer " + fakeHeaderToken + " X-Client-Info:agentpack]",
			},
			wantWarned: "no environment-variable indirection for a header credential",
		},
		{
			name:   "unresolved header credential references its injection point",
			server: remoteServer("supabase"),
			creds: []model.Credential{{
				Header: "Authorization", Format: "Bearer {value}",
			}},
			wantEntry: map[string]any{
				keyHTTPHeaders: "map[Authorization:Bearer ${Authorization} X-Client-Info:agentpack]",
			},
			wantWarned: "has no resolved value",
		},
		{
			name:       "a second remote env credential is reported, not dropped silently",
			server:     remoteServer("supabase"),
			creds:      []model.Credential{{Env: "FIRST_TOKEN"}, {Env: "SECOND_TOKEN"}},
			wantEntry:  map[string]any{keyBearerEnv: "FIRST_TOKEN"},
			wantWarned: `"SECOND_TOKEN" is not planned`,
		},
		{
			name:       "a header credential on a stdio server has nowhere to go",
			server:     stdioServer("github", model.ScopeGlobal),
			creds:      []model.Credential{{Header: "Authorization"}},
			wantAbsent: []string{keyHTTPHeaders, keyEnvVars},
			wantWarned: "Codex sends no headers to a local process",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, opts := planHome(t)
			opts.Credentials = tc.resolved
			comp := packServer(tc.server, tc.creds)

			p, warnings := plan(t, a, []model.Component{comp}, opts)
			op := mergeOpFor(t, p, keyMCPServers, tc.server.Spec.Name)
			entry := entryOf(t, op)

			for key, want := range tc.wantEntry {
				if got := entry[key]; formatValue(got) != want {
					t.Errorf("entry[%q] = %v, want %v", key, formatValue(got), want)
				}
			}
			for _, key := range tc.wantAbsent {
				if _, ok := entry[key]; ok {
					t.Errorf("entry has %q = %v, want it absent", key, entry[key])
				}
			}
			if text := warningsText(warnings); !strings.Contains(text, tc.wantWarned) {
				t.Errorf("warnings = %q, want one containing %q", text, tc.wantWarned)
			}
			if tc.wantNoValue != "" {
				if rendered := formatValue(entry); strings.Contains(rendered, tc.wantNoValue) {
					t.Errorf("the resolved secret reached the planned config: %s", rendered)
				}
			}
		})
	}
}

// TestPlanUnresolvedCredentialKeepsServer states the contract plainly: a
// credential nobody could resolve never costs the user the server.
func TestPlanUnresolvedCredentialKeepsServer(t *testing.T) {
	a, opts := planHome(t)
	comp := packServer(remoteServer("supabase"), []model.Credential{{Header: "Authorization", Format: "Bearer {value}"}})
	p, warnings := plan(t, a, []model.Component{comp}, opts)

	op := mergeOpFor(t, p, keyMCPServers, "supabase")
	headers, ok := entryOf(t, op)[keyHTTPHeaders].(map[string]any)
	if !ok {
		t.Fatalf("no http_headers planned: %v", op.Value)
	}
	value, _ := headers["Authorization"].(string)
	if value == "" || strings.Contains(value, "${}") {
		t.Fatalf("Authorization = %q, want a non-empty reference to the injection point", value)
	}
	if !strings.Contains(value, "Authorization") {
		t.Errorf("Authorization = %q, want it to name its own injection point", value)
	}
	if len(warnings) != 1 {
		t.Fatalf("want exactly one warning, got: %s", warningsText(warnings))
	}
	if !strings.Contains(warnings[0].Message, "no resolved value") {
		t.Errorf("warning = %q, want it to say the credential did not resolve", warnings[0].Message)
	}
}

func TestPlanSettingsPortableSubset(t *testing.T) {
	a, opts := planHome(t)
	comp := model.Setting{Spec: model.SettingSpec{
		Name:  "codex-defaults",
		Scope: model.ScopeGlobal,
		Values: map[string]any{
			"model":                    "gpt-5-codex",
			"profiles":                 map[string]any{"full_auto": map[string]any{"approval_policy": "never"}},
			"mcp_servers":              map[string]any{"github": map[string]any{"command": "npx"}},
			"shell_environment_policy": map[string]any{"inherit": "all"},
			"nonsense_key":             true,
		},
	}}
	p, warnings := plan(t, a, []model.Component{comp}, opts)

	if len(p.Ops) != 2 {
		t.Fatalf("want one merge per portable key, got %d:\n%s", len(p.Ops), p)
	}
	if op := mergeOpFor(t, p, "model"); op.Strategy != engine.MergeSet || op.Value != "gpt-5-codex" {
		t.Errorf("model merge = %v (%q), want the scalar set", op.Value, op.Strategy)
	}
	// A table the user also writes into must merge deeply, or restoring one
	// profile would delete the others.
	if op := mergeOpFor(t, p, "profiles"); op.Strategy != engine.MergeDeep {
		t.Errorf("profiles strategy = %q, want %q", op.Strategy, engine.MergeDeep)
	}

	text := warningsText(warnings)
	for _, want := range []string{"mcp_servers", "shell_environment_policy", "nonsense_key"} {
		if !strings.Contains(text, want) {
			t.Errorf("warnings %q must name the skipped key %q", text, want)
		}
	}
	// mcp_servers must never be written through a settings document: that
	// would replace every server table in one operation.
	for _, op := range p.Ops {
		if len(op.KeyPath) > 0 && op.KeyPath[0] == keyMCPServers {
			t.Errorf("a settings document planned an mcp_servers write: %v", op)
		}
	}
}

// TestPlanWarnsAboutCommentedConfig: merging re-encodes the document, so a
// hand-annotated config.toml loses its comments. The user hears about it
// before they confirm, not after (docs/security.md threat 3, backlog P3.13).
func TestPlanWarnsAboutCommentedConfig(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		wantWarn bool
	}{
		{
			name:     "hand-annotated config",
			existing: "# why this model\nmodel = \"gpt-5-codex\"\n",
			wantWarn: true,
		},
		{
			name:     "trailing comment",
			existing: "model = \"gpt-5-codex\" # pinned\n",
			wantWarn: true,
		},
		{
			name:     "a hash inside a string is not a comment",
			existing: "model = \"gpt-5-codex\"\nfile_opener = \"vscode://file#L1\"\n",
			wantWarn: false,
		},
		{
			name:     "no config file yet",
			existing: "",
			wantWarn: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, opts := planHome(t)
			if tc.existing != "" {
				writeCodexConfig(t, opts.Home, tc.existing)
			}
			_, warnings := plan(t, a, []model.Component{stdioServer("github", model.ScopeGlobal)}, opts)

			got := strings.Contains(warningsText(warnings), "comments and key order are not")
			if got != tc.wantWarn {
				t.Errorf("comment warning = %v, want %v (warnings: %s)", got, tc.wantWarn, warningsText(warnings))
			}
		})
	}
}

// TestPlanWarnsOnceAboutComments keeps the warning from repeating per server.
func TestPlanWarnsOnceAboutComments(t *testing.T) {
	a, opts := planHome(t)
	writeCodexConfig(t, opts.Home, "# annotated\nmodel = \"gpt-5-codex\"\n")
	comps := []model.Component{
		stdioServer("github", model.ScopeGlobal),
		stdioServer("linear", model.ScopeGlobal),
		model.Setting{Spec: model.SettingSpec{Name: "s", Values: map[string]any{"model": "gpt-5-codex"}}},
	}
	_, warnings := plan(t, a, comps, opts)
	if n := strings.Count(warningsText(warnings), "comments and key order are not"); n != 1 {
		t.Errorf("comment warning appeared %d times, want once", n)
	}
}

// TestPlanScopesCodexCannotHonor: Codex keeps MCP servers, prompts and
// settings only in the home tree. A project-scoped one is skipped with a
// warning rather than quietly promoted to every session the user runs.
func TestPlanScopesCodexCannotHonor(t *testing.T) {
	packDir := t.TempDir()
	promptRel := bundled(t, packDir, "prompts/review.md", "review\n")

	comps := []model.Component{
		stdioServer("github", model.ScopeProject),
		model.Command{Spec: model.CommandSpec{Name: "review", Scope: model.ScopeProject, Path: promptRel}},
		model.Setting{Spec: model.SettingSpec{Name: "s", Scope: model.ScopeProject, Values: map[string]any{"model": "gpt-5-codex"}}},
	}

	a, opts := planHome(t)
	opts.PackDir = packDir
	opts.ProjectDir = t.TempDir() // even *with* a project dir, Codex has no place for these
	p, warnings := plan(t, a, comps, opts)

	if !p.IsEmpty() {
		t.Fatalf("project-scoped components produced operations, want none:\n%s", p)
	}
	if len(warnings) != 3 {
		t.Fatalf("want one warning per skipped component, got %d: %s", len(warnings), warningsText(warnings))
	}
	if text := warningsText(warnings); !strings.Contains(text, "project-scoped") {
		t.Errorf("warnings %q must say why the components were skipped", text)
	}
}

func TestPlanReplaceMode(t *testing.T) {
	packDir := t.TempDir()
	ruleRel := bundled(t, packDir, "rules/conventions.md", "# conventions\n")

	a, opts := planHome(t)
	opts.PackDir = packDir
	opts.Replace = true
	comp := model.Rule{Spec: model.RuleSpec{Name: "conventions", Scope: model.ScopeGlobal, Path: ruleRel}}

	p, _ := plan(t, a, []model.Component{comp}, opts)
	if len(p.Ops) != 1 || p.Ops[0].Kind != engine.OpReplaceFile {
		t.Fatalf("replace mode must plan a replace, got:\n%s", p)
	}
}

// TestPlanAbsoluteSourcePath covers a component scanned off this machine: its
// path is already absolute and needs no pack directory.
func TestPlanAbsoluteSourcePath(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(src, []byte("# scanned\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a, opts := planHome(t)
	comp := model.Rule{Spec: model.RuleSpec{Name: "AGENTS.md", Scope: model.ScopeGlobal, Path: src}}
	p, warnings := plan(t, a, []model.Component{comp}, opts)

	if len(p.Ops) != 1 || string(p.Ops[0].Content) != "# scanned\n" {
		t.Fatalf("want one op carrying the file's content, got:\n%s", p)
	}
	if len(warnings) != 0 {
		t.Errorf("unexpected warnings: %s", warningsText(warnings))
	}
}

// TestPlanWritesNothing is the plan/apply split in one assertion: planning a
// full pack must leave the home directory exactly as empty as it found it.
func TestPlanWritesNothing(t *testing.T) {
	packDir := t.TempDir()
	ruleRel := bundled(t, packDir, "rules/conventions.md", "# conventions\n")
	promptRel := bundled(t, packDir, "prompts/review.md", "review\n")

	a, opts := planHome(t)
	opts.PackDir = packDir
	opts.ProjectDir = t.TempDir()
	opts.Credentials = map[string]string{"GITHUB_TOKEN": fakeToken}

	comps := []model.Component{
		packServer(stdioServer("github", model.ScopeGlobal), []model.Credential{{Env: "GITHUB_TOKEN"}}),
		model.Rule{Spec: model.RuleSpec{Name: "conventions", Scope: model.ScopeProject, Path: ruleRel}},
		model.Command{Spec: model.CommandSpec{Name: "review", Scope: model.ScopeGlobal, Path: promptRel}},
		model.Setting{Spec: model.SettingSpec{Name: "defaults", Values: map[string]any{"model": "gpt-5-codex"}}},
		model.Skill{Spec: model.SkillSpec{Name: "brainstorming"}},
	}
	p, _ := plan(t, a, comps, opts)
	if p.IsEmpty() {
		t.Fatal("expected a non-empty plan to make this test meaningful")
	}

	for _, dir := range []string{opts.Home, opts.ProjectDir} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("Plan() wrote %d entries into %s; planning must never touch the disk", len(entries), dir)
		}
	}
	// Every path a plan carries has to be absolute: it is rendered, confirmed
	// and only then applied, possibly from a different working directory.
	for _, path := range p.Paths() {
		if !filepath.IsAbs(path) {
			t.Errorf("plan path %q is not absolute", path)
		}
	}
}

func TestPlanWithoutHome(t *testing.T) {
	a := New()
	a.home = ""
	if _, _, err := a.Plan(nil, engine.PlanOpts{}); err == nil {
		t.Fatal("planning with no home directory must fail rather than guess one")
	}
}

func TestPlanUnplaceableServers(t *testing.T) {
	tests := []struct {
		name       string
		spec       model.MCPServerSpec
		wantWarned string
	}{
		{
			name:       "no command and no url",
			spec:       model.MCPServerSpec{Name: "hollow", Scope: model.ScopeGlobal},
			wantWarned: "neither a command nor a url",
		},
		{
			name:       "stdio without a command",
			spec:       model.MCPServerSpec{Name: "broken", Transport: model.TransportStdio},
			wantWarned: "declares no command",
		},
		{
			name:       "remote without a url",
			spec:       model.MCPServerSpec{Name: "broken", Transport: model.TransportSSE},
			wantWarned: "declares no url",
		},
		{
			name:       "unnamed",
			spec:       model.MCPServerSpec{Command: "npx"},
			wantWarned: "has no name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a, opts := planHome(t)
			p, warnings := plan(t, a, []model.Component{model.MCPServer{Spec: tc.spec}}, opts)
			if !p.IsEmpty() {
				t.Errorf("want no operations for an unplaceable server, got:\n%s", p)
			}
			if text := warningsText(warnings); !strings.Contains(text, tc.wantWarned) {
				t.Errorf("warnings = %q, want one containing %q", text, tc.wantWarned)
			}
		})
	}
}

// TestPlanInfersTransport: a pack written by an older exporter can carry an
// empty transport, and a server with a command is plainly stdio.
func TestPlanInfersTransport(t *testing.T) {
	a, opts := planHome(t)
	comps := []model.Component{
		model.MCPServer{Spec: model.MCPServerSpec{Name: "local", Command: "npx"}},
		model.MCPServer{Spec: model.MCPServerSpec{Name: "remote", URL: "https://mcp.example.com/mcp"}},
	}
	p, _ := plan(t, a, comps, opts)

	if entry := entryOf(t, mergeOpFor(t, p, keyMCPServers, "local")); entry[keyCommand] != "npx" {
		t.Errorf("local entry = %v, want a stdio command", entry)
	}
	if entry := entryOf(t, mergeOpFor(t, p, keyMCPServers, "remote")); entry[keyURL] != "https://mcp.example.com/mcp" {
		t.Errorf("remote entry = %v, want a url", entry)
	}
}

func TestPlanUnknownComponentWarns(t *testing.T) {
	a, opts := planHome(t)
	p, warnings := plan(t, a, []model.Component{oddComponent{}}, opts)
	if !p.IsEmpty() {
		t.Fatalf("want no operations, got:\n%s", p)
	}
	if text := warningsText(warnings); !strings.Contains(text, "cannot place") {
		t.Errorf("warnings = %q, want one naming the unplaceable kind", text)
	}
}

type oddComponent struct{}

func (oddComponent) Kind() model.Kind   { return model.Kind("mystery") }
func (oddComponent) Name() string       { return "unknown" }
func (oddComponent) Scope() model.Scope { return model.ScopeGlobal }

// formatValue renders a planned value for comparison, with map keys sorted so
// the expectation is stable however Go happens to iterate.
func formatValue(v any) string {
	switch val := v.(type) {
	case map[string]any:
		parts := make([]string, 0, len(val))
		for _, k := range sortedAnyKeys(val) {
			parts = append(parts, k+":"+formatValue(val[k]))
		}
		return "map[" + strings.Join(parts, " ") + "]"
	case []string:
		return "[" + strings.Join(val, " ") + "]"
	default:
		return fmt.Sprintf("%v", val)
	}
}

// TestPlanAppliesCleanly runs a plan through the real executor against a
// populated config.toml. It is the end-to-end proof that the operations this
// adapter emits are ones the executor can perform, and that a merge leaves
// the rest of a mixed file alone: the user's model setting, their own MCP
// server and their profiles all have to survive a restore.
func TestPlanAppliesCleanly(t *testing.T) {
	a, opts := planHome(t)
	writeCodexConfig(t, opts.Home, `# hand written
model = "gpt-5-codex"

[mcp_servers.mine]
command = "my-server"

[profiles.full_auto]
approval_policy = "never"
`)

	comps := []model.Component{
		packServer(stdioServer("github", model.ScopeGlobal), []model.Credential{{Env: "GITHUB_TOKEN"}}),
		model.Setting{Spec: model.SettingSpec{
			Name:   "defaults",
			Values: map[string]any{"approval_policy": "on-request"},
		}},
	}
	p, _ := plan(t, a, comps, opts)

	ex := &engine.Executor{BackupRoot: t.TempDir()}
	res, err := ex.Apply(p)
	if err != nil {
		t.Fatalf("applying the planned operations failed: %v", err)
	}
	if !res.Changed() {
		t.Fatal("apply reported no change")
	}

	a.home = opts.Home
	a.lookPath = lookPathHit
	inv, err := a.Scan(model.ScanScope{Global: true})
	if err != nil {
		t.Fatalf("re-scanning the applied config failed: %v", err)
	}
	if got := componentNames(inv, model.KindMCPServer); strings.Join(got, ",") != "github,mine" {
		t.Errorf("servers after apply = %v, want the planned one alongside the user's own", got)
	}
	if gh := mcpByName(t, inv, "github"); gh.Spec.Command != "npx" {
		t.Errorf("github command = %q, want npx", gh.Spec.Command)
	}

	raw, err := os.ReadFile(codexPath(opts.Home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`model = "gpt-5-codex"`, "full_auto", `approval_policy = "on-request"`, "GITHUB_TOKEN"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("config.toml after apply is missing %q:\n%s", want, raw)
		}
	}
	// Indirection, not embedding: the merged file references the variable and
	// holds no secret of its own.
	if strings.Contains(string(raw), fakeToken) {
		t.Errorf("a resolved credential reached config.toml:\n%s", raw)
	}

	// Re-applying the same plan must be a no-op, which is what makes restoring
	// a pack twice safe.
	res, err = ex.Apply(p)
	if err != nil {
		t.Fatalf("re-applying failed: %v", err)
	}
	if res.Changed() {
		t.Errorf("re-applying the same plan changed the machine: %+v", res.Ops)
	}
}

// TestPlanCreatesConfigPrivately: a config.toml agentpack creates may end up
// holding a resolved header credential, so it must not be world-readable.
func TestPlanCreatesConfigPrivately(t *testing.T) {
	a, opts := planHome(t)
	comp := packServer(remoteServer("supabase"), []model.Credential{{Header: "Authorization", Format: "Bearer {value}"}})
	opts.Credentials = map[string]string{"Authorization": fakeHeaderToken}
	p, _ := plan(t, a, []model.Component{comp}, opts)

	ex := &engine.Executor{BackupRoot: t.TempDir()}
	if _, err := ex.Apply(p); err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	info, err := os.Stat(codexPath(opts.Home, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("new config.toml mode = %o, want 600 — it can hold a resolved credential", perm)
	}
}
