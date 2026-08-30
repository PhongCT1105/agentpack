package packio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/secrets"
)

func fixture(t *testing.T, rel string) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "convert", rel))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// mcpInventory returns a claude-code inventory with one stdio server whose
// env mixes plain, secret (fake), uncertain, and placeholder values, and
// one http server with a plain and a secret header.
func mcpInventory() model.Inventory {
	return model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "github", Scope: model.ScopeGlobal, Transport: model.TransportStdio,
				Command: "npx", Args: []string{"-y", "@modelcontextprotocol/server-github"},
				Env: map[string]string{
					"GITHUB_API_URL": "https://api.github.com",
					"GITHUB_TOKEN":   "ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE",
					"SUPABASE_URL":   "https://FAKE0q7pz2mk9vlt4wyb.supabase.co",
					"HOME_REF":       "${HOME}",
				},
			}},
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "supabase", Scope: model.ScopeGlobal, Transport: model.TransportHTTP,
				URL: "https://mcp.supabase.com/mcp",
				Headers: map[string]string{
					"X-Client-Info": "agentpack",
					"Authorization": "Bearer FAKE-TOKEN-VALUE",
				},
			}},
		},
	}
}

func TestConvertSplitsEnvAndCredentials(t *testing.T) {
	res, err := Convert([]model.Inventory{mcpInventory()}, ConvertOptions{Name: "test-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	servers := res.Manifest.Components.MCPServers
	if len(servers) != 2 {
		t.Fatalf("got %d mcp servers, want 2: %+v", len(servers), servers)
	}

	github := servers[0]
	if github.Name != "github" {
		t.Fatalf("first server = %q, want github", github.Name)
	}
	wantEnv := map[string]string{
		"GITHUB_API_URL": "https://api.github.com",
		"HOME_REF":       "${HOME}",
	}
	if len(github.Env) != len(wantEnv) {
		t.Errorf("github env = %v, want %v", github.Env, wantEnv)
	}
	for k, v := range wantEnv {
		if github.Env[k] != v {
			t.Errorf("github env[%s] = %q, want %q", k, github.Env[k], v)
		}
	}
	// GITHUB_TOKEN (secret) and SUPABASE_URL (uncertain, default-redacted)
	// become credentials, sorted by env name.
	if len(github.Credentials) != 2 ||
		github.Credentials[0].Env != "GITHUB_TOKEN" ||
		github.Credentials[1].Env != "SUPABASE_URL" {
		t.Errorf("github credentials = %+v, want GITHUB_TOKEN then SUPABASE_URL", github.Credentials)
	}

	supabase := servers[1]
	if len(supabase.Headers) != 1 || supabase.Headers["X-Client-Info"] != "agentpack" {
		t.Errorf("supabase headers = %v, want only X-Client-Info", supabase.Headers)
	}
	if len(supabase.Credentials) != 1 || supabase.Credentials[0].Header != "Authorization" {
		t.Errorf("supabase credentials = %+v, want header Authorization", supabase.Credentials)
	}
	// The non-secret auth scheme is preserved as the format so restore can
	// reconstruct the header (spec: format: "Bearer {value}").
	if got := supabase.Credentials[0].Format; got != "Bearer {value}" {
		t.Errorf("Authorization credential format = %q, want %q", got, "Bearer {value}")
	}

	// The secret values must not appear anywhere in the encoded manifest.
	data, err := EncodeManifest(res.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, secretVal := range []string{"ghp_FAKE", "FAKE-TOKEN-VALUE", "FAKE0q7pz2mk9vlt4wyb"} {
		if strings.Contains(string(data), secretVal) {
			t.Errorf("encoded manifest contains redacted value %q:\n%s", secretVal, data)
		}
	}

	// Redactions are reported for the save UI.
	if len(res.Redactions) != 3 {
		t.Errorf("got %d redactions, want 3: %+v", len(res.Redactions), res.Redactions)
	}
	for _, r := range res.Redactions {
		if r.Verdict.Level == secrets.Plain {
			t.Errorf("redaction %+v has Plain verdict", r)
		}
		if r.Action != "credential" {
			t.Errorf("redaction %+v action = %q, want credential", r, r.Action)
		}
	}
}

func TestConvertUncertainCallbackCanKeep(t *testing.T) {
	opts := ConvertOptions{
		Name: "test-pack",
		TreatUncertainAsSecret: func(key, value string, v secrets.Verdict) bool {
			return key != "SUPABASE_URL" // keep this one as plain env
		},
	}
	res, err := Convert([]model.Inventory{mcpInventory()}, opts)
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	github := res.Manifest.Components.MCPServers[0]
	if github.Env["SUPABASE_URL"] == "" {
		t.Errorf("SUPABASE_URL not kept in env: %v", github.Env)
	}
	if len(github.Credentials) != 1 || github.Credentials[0].Env != "GITHUB_TOKEN" {
		t.Errorf("credentials = %+v, want only GITHUB_TOKEN (secrets are never keepable)", github.Credentials)
	}
}

func bundledInventories(t *testing.T) []model.Inventory {
	return []model.Inventory{
		{
			Tool: model.ToolClaudeCode,
			Components: []model.Component{
				model.Skill{Spec: model.SkillSpec{
					Name: "pdf-tools", Scope: model.ScopeGlobal,
					Dir: fixture(t, "skills/pdf-tools"), Description: "Split, merge, and extract text from PDFs.",
				}},
				model.Agent{Spec: model.AgentSpec{
					Name: "db-migrator", Scope: model.ScopeGlobal,
					Path: fixture(t, "agents/db-migrator.md"), Description: "Plans and applies database migrations.",
				}},
				model.Command{Spec: model.CommandSpec{
					Name: "review", Scope: model.ScopeGlobal, Path: fixture(t, "commands/review.md"),
				}},
				model.Rule{Spec: model.RuleSpec{
					Name: "CLAUDE.md", Scope: model.ScopeGlobal, Path: fixture(t, "rules-global/CLAUDE.md"),
				}},
				model.Rule{Spec: model.RuleSpec{
					Name: "CLAUDE.md", Scope: model.ScopeProject, Path: fixture(t, "rules-project/CLAUDE.md"),
				}},
			},
		},
		{
			Tool: model.ToolCodex,
			Components: []model.Component{
				model.Rule{Spec: model.RuleSpec{
					Name: "AGENTS.md", Scope: model.ScopeGlobal, Path: fixture(t, "codex/AGENTS.md"),
				}},
			},
		},
	}
}

func TestConvertBundlesContent(t *testing.T) {
	res, err := Convert(bundledInventories(t), ConvertOptions{Name: "bundle-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	m := res.Manifest

	if len(m.Targets) != 2 || m.Targets[0] != model.ToolClaudeCode || m.Targets[1] != model.ToolCodex {
		t.Errorf("targets = %v, want [claude-code codex]", m.Targets)
	}

	if len(m.Components.Skills) != 1 || m.Components.Skills[0].Source.Bundled != "skills/pdf-tools" {
		t.Errorf("skills = %+v, want bundled skills/pdf-tools", m.Components.Skills)
	}
	if got := m.Components.Skills[0].Targets; len(got) != 1 || got[0] != model.ToolClaudeCode {
		t.Errorf("skill targets = %v, want [claude-code]", got)
	}
	if len(m.Components.Agents) != 1 || m.Components.Agents[0].Source.Bundled != "agents/db-migrator.md" {
		t.Errorf("agents = %+v, want bundled agents/db-migrator.md", m.Components.Agents)
	}
	if len(m.Components.Commands) != 1 || m.Components.Commands[0].Source.Bundled != "prompts/review.md" {
		t.Errorf("commands = %+v, want bundled prompts/review.md", m.Components.Commands)
	}

	rules := m.Components.Rules
	if len(rules) != 3 {
		t.Fatalf("got %d rules, want 3: %+v", len(rules), rules)
	}
	// Names are slugged and deduplicated deterministically.
	if rules[0].Name != "claude-md" {
		t.Errorf("rules[0].Name = %q, want claude-md", rules[0].Name)
	}
	if rules[1].Name == rules[0].Name || rules[2].Name == rules[0].Name || rules[1].Name == rules[2].Name {
		t.Errorf("rule names not unique: %q %q %q", rules[0].Name, rules[1].Name, rules[2].Name)
	}
	if rules[1].Scope != model.ScopeProject {
		t.Errorf("rules[1].Scope = %q, want project", rules[1].Scope)
	}
	// Render preserves each tool's consumed filename.
	if rules[0].Render[model.ToolClaudeCode] != "CLAUDE.md" {
		t.Errorf("rules[0].Render = %v, want claude-code: CLAUDE.md", rules[0].Render)
	}
	if rules[2].Render[model.ToolCodex] != "AGENTS.md" {
		t.Errorf("rules[2].Render = %v, want codex: AGENTS.md", rules[2].Render)
	}

	// Every bundled source has a corresponding copy instruction.
	toPaths := map[string]bool{}
	for _, b := range res.Bundles {
		toPaths[b.ToPath] = true
	}
	for _, want := range []string{"skills/pdf-tools", "agents/db-migrator.md", "prompts/review.md"} {
		if !toPaths[want] {
			t.Errorf("no bundle copies to %q; bundles: %+v", want, res.Bundles)
		}
	}
	if len(res.Bundles) != 6 {
		t.Errorf("got %d bundles, want 6 (skill, agent, command, 3 rules): %+v", len(res.Bundles), res.Bundles)
	}
}

func TestConvertDeterministic(t *testing.T) {
	invs := []model.Inventory{mcpInventory()}
	a, err := Convert(invs, ConvertOptions{Name: "det-pack"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Convert(invs, ConvertOptions{Name: "det-pack"})
	if err != nil {
		t.Fatal(err)
	}
	ay, err := EncodeManifest(a.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	by, err := EncodeManifest(b.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(ay) != string(by) {
		t.Errorf("Convert() not deterministic:\n%s\nvs:\n%s", ay, by)
	}
}

func TestConvertSettingsRedaction(t *testing.T) {
	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.Setting{Spec: model.SettingSpec{
				Name: "settings.json", Scope: model.ScopeGlobal, Path: "/home/user/.claude/settings.json",
				Values: map[string]any{
					"model": "claude-fable-5",
					"permissions": map[string]any{
						"allow": []any{"Bash(go test:*)"},
					},
					"env": map[string]any{
						"API_TOKEN": "FAKE0q7pz2mk9vlt4wyb",
						"EDITOR":    "vim",
					},
				},
			}},
		},
	}
	res, err := Convert([]model.Inventory{inv}, ConvertOptions{Name: "settings-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	if len(res.Manifest.Components.Settings) != 1 {
		t.Fatalf("settings = %+v, want 1", res.Manifest.Components.Settings)
	}
	got := res.Manifest.Components.Settings[0]
	if got.Name != "settings-json" {
		t.Errorf("setting name = %q, want settings-json (slugged)", got.Name)
	}
	if got.Values["model"] != "claude-fable-5" {
		t.Errorf("plain value dropped: %v", got.Values)
	}
	env, _ := got.Values["env"].(map[string]any)
	if env == nil || env["EDITOR"] != "vim" {
		t.Errorf("nested plain value dropped: %v", got.Values)
	}
	if _, leaked := env["API_TOKEN"]; leaked {
		t.Errorf("secret settings value survived conversion: %v", env)
	}
	if len(res.Redactions) != 1 || res.Redactions[0].Key != "API_TOKEN" || res.Redactions[0].Action != "dropped" {
		t.Errorf("redactions = %+v, want one dropped API_TOKEN", res.Redactions)
	}
}

func TestConvertWarnsOnUnportableParts(t *testing.T) {
	inv := model.Inventory{
		Tool: model.ToolCodex,
		Components: []model.Component{
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "weird", Scope: model.ScopeGlobal, Transport: model.Transport("carrier-pigeon"),
			}},
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "args-leak", Scope: model.ScopeGlobal, Transport: model.TransportStdio,
				Command: "serve", Args: []string{"--token=ghp_FAKEFAKEFAKEFAKEFAKEFAKEFAKEFAKE"},
			}},
		},
	}
	res, err := Convert([]model.Inventory{inv}, ConvertOptions{Name: "warn-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	// The invalid transport is skipped, the args server is kept (the pack
	// scan will block the save; the user must fix their tool config).
	if len(res.Manifest.Components.MCPServers) != 1 || res.Manifest.Components.MCPServers[0].Name != "args-leak" {
		t.Errorf("servers = %+v, want only args-leak", res.Manifest.Components.MCPServers)
	}
	var sawTransport, sawArgs bool
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "transport") {
			sawTransport = true
		}
		if strings.Contains(w.Message, "argument") {
			sawArgs = true
		}
	}
	if !sawTransport || !sawArgs {
		t.Errorf("warnings = %+v, want transport-skip and argument-secret warnings", res.Warnings)
	}
}

func TestConvertNameCollisionsExhaustDisambiguators(t *testing.T) {
	// Five identically-named skills from the same tool and scope must walk
	// the whole candidate chain and stay unique and deterministic.
	var comps []model.Component
	for i := 0; i < 5; i++ {
		comps = append(comps, model.Skill{Spec: model.SkillSpec{
			Name: "helper", Scope: model.ScopeGlobal, Dir: fixture(t, "skills/pdf-tools"),
		}})
	}
	res, err := Convert([]model.Inventory{{Tool: model.ToolClaudeCode, Components: comps}}, ConvertOptions{Name: "clash-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	seenNames := map[string]bool{}
	seenPaths := map[string]bool{}
	for _, s := range res.Manifest.Components.Skills {
		if seenNames[s.Name] {
			t.Errorf("duplicate skill name %q", s.Name)
		}
		seenNames[s.Name] = true
	}
	for _, b := range res.Bundles {
		if seenPaths[b.ToPath] {
			t.Errorf("duplicate bundle destination %q", b.ToPath)
		}
		seenPaths[b.ToPath] = true
	}
	if !seenNames["helper"] || !seenNames["helper-2"] {
		t.Errorf("expected chain to reach numeric fallback, got %v", seenNames)
	}
}

func TestConvertWarnsOnUncertainURL(t *testing.T) {
	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.MCPServer{Spec: model.MCPServerSpec{
				Name: "supabase", Scope: model.ScopeGlobal, Transport: model.TransportHTTP,
				URL: "https://FAKE0q7pz2mk9vlt4wyb.supabase.co/mcp",
			}},
		},
	}
	res, err := Convert([]model.Inventory{inv}, ConvertOptions{Name: "url-pack"})
	if err != nil {
		t.Fatalf("Convert() error: %v", err)
	}
	// The URL ships (no injection point exists), but never silently.
	if len(res.Manifest.Components.MCPServers) != 1 {
		t.Fatalf("servers = %+v, want 1", res.Manifest.Components.MCPServers)
	}
	found := false
	for _, w := range res.Warnings {
		if strings.Contains(w.Message, "high-entropy") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %+v, want an uncertain-URL warning", res.Warnings)
	}
}

func TestConvertRejectsBadPackName(t *testing.T) {
	for _, name := range []string{"", "Bad Name", "UPPER", "-lead", "trail-", "dots.dots"} {
		if _, err := Convert(nil, ConvertOptions{Name: name}); err == nil {
			t.Errorf("Convert(name=%q) = nil error, want error", name)
		}
	}
}

func TestWritePackRoundTrip(t *testing.T) {
	res, err := Convert(bundledInventories(t), ConvertOptions{Name: "bundle-pack"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	findings, err := WritePack(dir, res)
	if err != nil {
		t.Fatalf("WritePack() error: %v (findings %+v)", err, findings)
	}
	if len(findings) != 0 {
		t.Fatalf("WritePack() findings = %+v, want none", findings)
	}

	data, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	if err != nil {
		t.Fatalf("manifest not written: %v", err)
	}
	if _, err := DecodeManifest(data); err != nil {
		t.Errorf("written manifest does not decode: %v", err)
	}
	for _, rel := range []string{
		"skills/pdf-tools/SKILL.md",
		"skills/pdf-tools/scripts/helper.py",
		"agents/db-migrator.md",
		"prompts/review.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("bundled file %s not copied: %v", rel, err)
		}
	}
	// All three rules copied under distinct paths.
	entries, err := os.ReadDir(filepath.Join(dir, "rules"))
	if err != nil || len(entries) != 3 {
		t.Errorf("rules dir has %d entries (err %v), want 3", len(entries), err)
	}
}

func TestReleaseBlocking_WritePackBlocksOnLeakyBundle(t *testing.T) {
	inv := model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.Skill{Spec: model.SkillSpec{
				Name: "leaky", Scope: model.ScopeGlobal, Dir: fixture(t, "leaky-skill"),
			}},
		},
	}
	res, err := Convert([]model.Inventory{inv}, ConvertOptions{Name: "leaky-pack"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	findings, err := WritePack(dir, res)
	if err == nil {
		t.Fatal("WritePack(leaky bundle) = nil error, want blocking error")
	}
	if len(findings) == 0 {
		t.Error("WritePack(leaky bundle) returned no findings")
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("leaky pack dir left on disk after blocked save: %v", statErr)
	}
}

func TestWritePackIntoExistingEmptyDir(t *testing.T) {
	res, err := Convert(nil, ConvertOptions{Name: "empty-pack"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir() // exists and is empty
	if _, err := WritePack(dir, res); err != nil {
		t.Fatalf("WritePack(existing empty dir) error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ManifestFilename)); err != nil {
		t.Errorf("manifest not written into existing empty dir: %v", err)
	}
}

func TestWritePackRefusesNonEmptyDir(t *testing.T) {
	res, err := Convert(nil, ConvertOptions{Name: "empty-pack"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePack(dir, res); err == nil {
		t.Error("WritePack(non-empty dir) = nil error, want error")
	}
	if _, err := os.Stat(filepath.Join(dir, "existing.txt")); err != nil {
		t.Errorf("existing file damaged by refused write: %v", err)
	}
}
