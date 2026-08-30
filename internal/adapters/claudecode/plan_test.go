package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// Fixture content. Every secret-shaped value embeds FAKE so the repository's
// secret scanning can tell a seeded fixture from a real leak
// (docs/security.md → Seeded fixtures).
const (
	fakeGitHubToken   = "ghp_FAKEfakeFAKEfake0123456789abcdefghij"
	fakeSupabaseToken = "sbp_FAKEfake0123456789abcdefghijklmnop"

	skillMD     = "---\nname: deploy-check\ndescription: Check a deploy\n---\n# Deploy check\n"
	skillScript = "#!/bin/sh\necho checking\n"
	agentMD     = "---\nname: db-migrator\n---\nMigrate databases carefully.\n"
	commandMD   = "Review the diff and report only real defects.\n"
	ruleMD      = "# Conventions\n\nSmall functions, honest names.\n"
)

// planDirs are the three roots a plan is built against: none of them is ever
// the machine's real home.
type planDirs struct {
	home    string
	project string
	pack    string
}

// newPlanDirs seeds a pack laid out the way packio.Convert writes one
// (skills/<name>/, agents/<name>.md, prompts/<name>.md, rules/<name>.md).
func newPlanDirs(t *testing.T) planDirs {
	t.Helper()
	d := planDirs{home: t.TempDir(), project: t.TempDir(), pack: t.TempDir()}
	writePackFile(t, d.pack, "skills/deploy-check/SKILL.md", skillMD, 0o644)
	writePackFile(t, d.pack, "skills/deploy-check/scripts/check.sh", skillScript, 0o755)
	writePackFile(t, d.pack, "agents/db-migrator.md", agentMD, 0o644)
	writePackFile(t, d.pack, "prompts/review.md", commandMD, 0o644)
	writePackFile(t, d.pack, "rules/conventions.md", ruleMD, 0o644)
	return d
}

func writePackFile(t *testing.T, packDir, rel, content string, perm os.FileMode) {
	t.Helper()
	path := filepath.Join(packDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
}

// newPlanAdapter returns an adapter with no home of its own, so a test that
// forgets to set PlanOpts.Home plans against nothing rather than against the
// machine running the test.
func newPlanAdapter() *Adapter { return &Adapter{} }

func (d planDirs) opts(withProject bool) engine.PlanOpts {
	opts := engine.PlanOpts{Home: d.home, PackDir: d.pack}
	if withProject {
		opts.ProjectDir = d.project
	}
	return opts
}

func formatOp(op engine.Op) string {
	return fmt.Sprintf("{kind:%s path:%s content:%q perm:%v format:%s keypath:%v value:%#v strategy:%q desc:%q}",
		op.Kind, op.Path, string(op.Content), op.Perm, op.Format, op.KeyPath, op.Value, op.Strategy, op.Description)
}

func assertOps(t *testing.T, got, want []engine.Op) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d operations, want %d\ngot:\n  %s\nwant:\n  %s",
			len(got), len(want), joinOps(got), joinOps(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("operation %d:\n got %s\nwant %s", i, formatOp(got[i]), formatOp(want[i]))
		}
	}
}

// toStringMap reads back an env/headers map from a planned entry.
func toStringMap(v any) map[string]string {
	m, _ := v.(map[string]string)
	return m
}

func joinOps(ops []engine.Op) string {
	out := make([]string, 0, len(ops))
	for _, op := range ops {
		out = append(out, formatOp(op))
	}
	return strings.Join(out, "\n  ")
}

// TestPlanOpsPerComponentKind pins the exact operations each component kind
// produces: the target path, the operation kind, and — for MCP servers and
// settings — the key path a merge lands at.
func TestPlanOpsPerComponentKind(t *testing.T) {
	cases := []struct {
		name        string
		withProject bool
		credentials map[string]string
		component   func(d planDirs) model.Component
		want        func(d planDirs) []engine.Op
	}{
		{
			name: "skill global becomes a directory tree of create operations",
			component: func(d planDirs) model.Component {
				return model.Skill{Spec: model.SkillSpec{
					Name: "deploy-check", Scope: model.ScopeGlobal, Dir: "skills/deploy-check",
				}}
			},
			want: func(d planDirs) []engine.Op {
				root := filepath.Join(d.home, ".claude", "skills", "deploy-check")
				return []engine.Op{
					{Kind: engine.OpCreateDir, Path: root, Description: "skill deploy-check"},
					{Kind: engine.OpCreateFile, Path: filepath.Join(root, "SKILL.md"),
						Content: []byte(skillMD), Description: "skill deploy-check"},
					{Kind: engine.OpCreateDir, Path: filepath.Join(root, "scripts"), Description: "skill deploy-check"},
					{Kind: engine.OpCreateFile, Path: filepath.Join(root, "scripts", "check.sh"),
						Content: []byte(skillScript), Perm: 0o755, Description: "skill deploy-check"},
				}
			},
		},
		{
			name:        "skill project lands under the project's .claude",
			withProject: true,
			component: func(d planDirs) model.Component {
				return model.Skill{Spec: model.SkillSpec{
					Name: "deploy-check", Scope: model.ScopeProject, Dir: "skills/deploy-check",
				}}
			},
			want: func(d planDirs) []engine.Op {
				root := filepath.Join(d.project, ".claude", "skills", "deploy-check")
				return []engine.Op{
					{Kind: engine.OpCreateDir, Path: root, Description: "skill deploy-check"},
					{Kind: engine.OpCreateFile, Path: filepath.Join(root, "SKILL.md"),
						Content: []byte(skillMD), Description: "skill deploy-check"},
					{Kind: engine.OpCreateDir, Path: filepath.Join(root, "scripts"), Description: "skill deploy-check"},
					{Kind: engine.OpCreateFile, Path: filepath.Join(root, "scripts", "check.sh"),
						Content: []byte(skillScript), Perm: 0o755, Description: "skill deploy-check"},
				}
			},
		},
		{
			name:        "mcp server global merges into ~/.claude.json at mcpServers.<name>",
			credentials: map[string]string{"GITHUB_TOKEN": fakeGitHubToken},
			component: func(d planDirs) model.Component {
				return model.MCPServer{Spec: model.MCPServerSpec{
					Name:      "github",
					Scope:     model.ScopeGlobal,
					Transport: model.TransportStdio,
					Command:   "npx",
					Args:      []string{"-y", "@modelcontextprotocol/server-github"},
					Env:       map[string]string{"GITHUB_API_URL": "https://api.github.com"},
					Credentials: []model.Credential{{
						Env: "GITHUB_TOKEN", Description: "GitHub personal access token",
					}},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:    engine.OpMergeValue,
					Path:    filepath.Join(d.home, ".claude.json"),
					Format:  engine.FormatJSON,
					KeyPath: []string{"mcpServers", "github"},
					Value: map[string]any{
						"type":    "stdio",
						"command": "npx",
						"args":    []string{"-y", "@modelcontextprotocol/server-github"},
						"env": map[string]string{
							"GITHUB_API_URL": "https://api.github.com",
							// ~/.claude.json does not expand ${VAR}, so the
							// resolved value is what goes in.
							"GITHUB_TOKEN": fakeGitHubToken,
						},
					},
					Strategy:    engine.MergeSet,
					Description: "mcp server github",
				}}
			},
		},
		{
			name:        "mcp server project merges into .mcp.json with env-var indirection",
			withProject: true,
			credentials: map[string]string{"GITHUB_TOKEN": fakeGitHubToken},
			component: func(d planDirs) model.Component {
				return model.MCPServer{Spec: model.MCPServerSpec{
					Name:        "github",
					Scope:       model.ScopeProject,
					Transport:   model.TransportStdio,
					Command:     "npx",
					Args:        []string{"-y", "@modelcontextprotocol/server-github"},
					Credentials: []model.Credential{{Env: "GITHUB_TOKEN"}},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:    engine.OpMergeValue,
					Path:    filepath.Join(d.project, ".mcp.json"),
					Format:  engine.FormatJSON,
					KeyPath: []string{"mcpServers", "github"},
					Value: map[string]any{
						"type":    "stdio",
						"command": "npx",
						"args":    []string{"-y", "@modelcontextprotocol/server-github"},
						"env":     map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
					},
					Strategy:    engine.MergeSet,
					Description: "mcp server github",
				}}
			},
		},
		{
			name:        "remote mcp server carries url and headers",
			credentials: map[string]string{"Authorization": fakeSupabaseToken},
			component: func(d planDirs) model.Component {
				return model.MCPServer{Spec: model.MCPServerSpec{
					Name:      "supabase",
					Scope:     model.ScopeGlobal,
					Transport: model.TransportHTTP,
					URL:       "https://mcp.supabase.com/mcp",
					Headers:   map[string]string{"X-Client-Info": "agentpack"},
					Credentials: []model.Credential{{
						Header: "Authorization", Format: "Bearer {value}",
					}},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:    engine.OpMergeValue,
					Path:    filepath.Join(d.home, ".claude.json"),
					Format:  engine.FormatJSON,
					KeyPath: []string{"mcpServers", "supabase"},
					Value: map[string]any{
						"type": "http",
						"url":  "https://mcp.supabase.com/mcp",
						"headers": map[string]string{
							"X-Client-Info": "agentpack",
							"Authorization": "Bearer " + fakeSupabaseToken,
						},
					},
					Strategy:    engine.MergeSet,
					Description: "mcp server supabase",
				}}
			},
		},
		{
			name: "agent global becomes one markdown file under ~/.claude/agents",
			component: func(d planDirs) model.Component {
				return model.Agent{Spec: model.AgentSpec{
					Name: "db-migrator", Scope: model.ScopeGlobal, Path: "agents/db-migrator.md",
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:        engine.OpCreateFile,
					Path:        filepath.Join(d.home, ".claude", "agents", "db-migrator.md"),
					Content:     []byte(agentMD),
					Description: "agent db-migrator",
				}}
			},
		},
		{
			name:        "command project becomes one markdown file under .claude/commands",
			withProject: true,
			component: func(d planDirs) model.Component {
				return model.Command{Spec: model.CommandSpec{
					Name: "review", Scope: model.ScopeProject, Path: "prompts/review.md",
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:        engine.OpCreateFile,
					Path:        filepath.Join(d.project, ".claude", "commands", "review.md"),
					Content:     []byte(commandMD),
					Description: "command review",
				}}
			},
		},
		{
			name: "rule global renders to ~/.claude/CLAUDE.md",
			component: func(d planDirs) model.Component {
				return model.Rule{Spec: model.RuleSpec{
					Name:   "conventions",
					Scope:  model.ScopeGlobal,
					Path:   "rules/conventions.md",
					Render: map[model.ToolID]string{model.ToolClaudeCode: "CLAUDE.md", model.ToolCodex: "AGENTS.md"},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:        engine.OpCreateFile,
					Path:        filepath.Join(d.home, ".claude", "CLAUDE.md"),
					Content:     []byte(ruleMD),
					Description: "rule conventions",
				}}
			},
		},
		{
			name:        "rule project renders to the project root, not .claude",
			withProject: true,
			component: func(d planDirs) model.Component {
				return model.Rule{Spec: model.RuleSpec{
					Name:   "conventions",
					Scope:  model.ScopeProject,
					Path:   "rules/conventions.md",
					Render: map[model.ToolID]string{model.ToolClaudeCode: "CLAUDE.md"},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind:        engine.OpCreateFile,
					Path:        filepath.Join(d.project, "CLAUDE.md"),
					Content:     []byte(ruleMD),
					Description: "rule conventions",
				}}
			},
		},
		{
			name: "settings merge one operation per top-level key, deeply",
			component: func(d planDirs) model.Component {
				return model.Setting{Spec: model.SettingSpec{
					Name:  "claude-permissions",
					Scope: model.ScopeGlobal,
					Values: map[string]any{
						"model":       "claude-sonnet-4",
						"permissions": map[string]any{"allow": []any{"Bash(npm run test:*)"}},
					},
				}}
			},
			want: func(d planDirs) []engine.Op {
				path := filepath.Join(d.home, ".claude", "settings.json")
				return []engine.Op{
					{Kind: engine.OpMergeValue, Path: path, Format: engine.FormatJSON,
						KeyPath: []string{"model"}, Value: "claude-sonnet-4",
						Strategy: engine.MergeDeep, Description: "setting claude-permissions"},
					{Kind: engine.OpMergeValue, Path: path, Format: engine.FormatJSON,
						KeyPath:  []string{"permissions"},
						Value:    map[string]any{"allow": []any{"Bash(npm run test:*)"}},
						Strategy: engine.MergeDeep, Description: "setting claude-permissions"},
				}
			},
		},
		{
			name:        "settings project land in .claude/settings.json",
			withProject: true,
			component: func(d planDirs) model.Component {
				return model.Setting{Spec: model.SettingSpec{
					Name:   "shared",
					Scope:  model.ScopeProject,
					Values: map[string]any{"model": "claude-sonnet-4"},
				}}
			},
			want: func(d planDirs) []engine.Op {
				return []engine.Op{{
					Kind: engine.OpMergeValue, Path: filepath.Join(d.project, ".claude", "settings.json"),
					Format: engine.FormatJSON, KeyPath: []string{"model"}, Value: "claude-sonnet-4",
					Strategy: engine.MergeDeep, Description: "setting shared",
				}}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPlanDirs(t)
			opts := d.opts(tc.withProject)
			opts.Credentials = tc.credentials

			plan, warnings, err := newPlanAdapter().Plan([]model.Component{tc.component(d)}, opts)
			if err != nil {
				t.Fatalf("Plan() error: %v", err)
			}
			if len(warnings) != 0 {
				t.Errorf("Plan() warned on a component it can apply: %v", warnings)
			}
			if plan.Tool != model.ToolClaudeCode {
				t.Errorf("Plan.Tool = %q, want %q", plan.Tool, model.ToolClaudeCode)
			}
			if err := plan.Validate(); err != nil {
				t.Errorf("plan does not validate: %v", err)
			}
			assertOps(t, plan.Ops, tc.want(d))
		})
	}
}

// TestPlanMCPServerNeverRewritesClaudeJSON is the guard on the mixed file:
// ~/.claude.json holds the user's app state and per-project history next to
// their MCP servers, so an entry goes in as a merge at a key path in every
// mode — a whole-file write there would be data loss.
func TestPlanMCPServerNeverRewritesClaudeJSON(t *testing.T) {
	server := model.MCPServer{Spec: model.MCPServerSpec{
		Name:      "github",
		Scope:     model.ScopeGlobal,
		Transport: model.TransportStdio,
		Command:   "npx",
	}}

	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprintf("replace=%v", replace), func(t *testing.T) {
			d := newPlanDirs(t)
			opts := d.opts(false)
			opts.Replace = replace

			plan, _, err := newPlanAdapter().Plan([]model.Component{server}, opts)
			if err != nil {
				t.Fatalf("Plan() error: %v", err)
			}
			if len(plan.Ops) != 1 {
				t.Fatalf("got %d operations, want 1:\n  %s", len(plan.Ops), joinOps(plan.Ops))
			}
			op := plan.Ops[0]
			if op.Kind != engine.OpMergeValue {
				t.Errorf("operation kind = %q, want %q (never a whole-file write of ~/.claude.json)",
					op.Kind, engine.OpMergeValue)
			}
			if want := filepath.Join(d.home, ".claude.json"); op.Path != want {
				t.Errorf("path = %q, want %q", op.Path, want)
			}
			if want := []string{"mcpServers", "github"}; !reflect.DeepEqual(op.KeyPath, want) {
				t.Errorf("key path = %v, want %v", op.KeyPath, want)
			}
			if op.Strategy != engine.MergeSet {
				t.Errorf("strategy = %q, want %q (the key path already scopes the write)",
					op.Strategy, engine.MergeSet)
			}
			for _, o := range plan.Ops {
				if o.Kind == engine.OpReplaceFile || o.Kind == engine.OpCreateFile {
					t.Errorf("plan writes %s wholesale via %s", o.Path, o.Kind)
				}
			}
		})
	}
}

// TestPlanCredentialInjection covers the product's core promise: where the
// secret goes, and what happens when there is none.
func TestPlanCredentialInjection(t *testing.T) {
	cases := []struct {
		name        string
		scope       model.Scope
		credential  model.Credential
		credentials map[string]string
		wantEnv     map[string]string
		wantHeaders map[string]string
		wantWarning bool
	}{
		{
			name:        "user scope embeds the resolved value (~/.claude.json does not expand)",
			scope:       model.ScopeGlobal,
			credential:  model.Credential{Env: "GITHUB_TOKEN"},
			credentials: map[string]string{"GITHUB_TOKEN": fakeGitHubToken},
			wantEnv:     map[string]string{"GITHUB_TOKEN": fakeGitHubToken},
		},
		{
			name:        "project scope references the env var even when resolved",
			scope:       model.ScopeProject,
			credential:  model.Credential{Env: "GITHUB_TOKEN"},
			credentials: map[string]string{"GITHUB_TOKEN": fakeGitHubToken},
			wantEnv:     map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
		},
		{
			name:        "unresolved user-scope credential still plans the server",
			scope:       model.ScopeGlobal,
			credential:  model.Credential{Env: "GITHUB_TOKEN", Description: "a token", ObtainURL: "https://example.test/tokens"},
			wantEnv:     map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
			wantWarning: true,
		},
		{
			name:        "unresolved project-scope credential still plans the server",
			scope:       model.ScopeProject,
			credential:  model.Credential{Env: "GITHUB_TOKEN"},
			wantEnv:     map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
			wantWarning: true,
		},
		{
			name:        "a blank resolution is treated as unresolved, never written",
			scope:       model.ScopeGlobal,
			credential:  model.Credential{Env: "GITHUB_TOKEN"},
			credentials: map[string]string{"GITHUB_TOKEN": "   "},
			wantEnv:     map[string]string{"GITHUB_TOKEN": "${GITHUB_TOKEN}"},
			wantWarning: true,
		},
		{
			name:        "header credential renders through its format",
			scope:       model.ScopeGlobal,
			credential:  model.Credential{Header: "Authorization", Format: "Bearer {value}"},
			credentials: map[string]string{"Authorization": fakeSupabaseToken},
			wantHeaders: map[string]string{"Authorization": "Bearer " + fakeSupabaseToken},
		},
		{
			name:        "unresolved header credential keeps a visible placeholder",
			scope:       model.ScopeGlobal,
			credential:  model.Credential{Header: "Authorization", Format: "Bearer {value}"},
			wantHeaders: map[string]string{"Authorization": "Bearer {value}"},
			wantWarning: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPlanDirs(t)
			opts := d.opts(true)
			opts.Credentials = tc.credentials

			spec := model.MCPServerSpec{
				Name:        "github",
				Scope:       tc.scope,
				Transport:   model.TransportStdio,
				Command:     "npx",
				Credentials: []model.Credential{tc.credential},
			}
			if tc.wantHeaders != nil {
				spec.Transport = model.TransportHTTP
				spec.Command = ""
				spec.URL = "https://mcp.example.test/mcp"
			}

			plan, warnings, err := newPlanAdapter().Plan([]model.Component{model.MCPServer{Spec: spec}}, opts)
			if err != nil {
				t.Fatalf("Plan() error: %v", err)
			}
			// The server is planned in every case: a missing credential must
			// never make a server disappear from the plan.
			if len(plan.Ops) != 1 {
				t.Fatalf("got %d operations, want 1 (the server itself):\n  %s", len(plan.Ops), joinOps(plan.Ops))
			}
			entry, ok := plan.Ops[0].Value.(map[string]any)
			if !ok {
				t.Fatalf("merge value is %T, want a map", plan.Ops[0].Value)
			}

			if tc.wantEnv != nil {
				if got, _ := entry["env"].(map[string]string); !reflect.DeepEqual(got, tc.wantEnv) {
					t.Errorf("env = %v, want %v", got, tc.wantEnv)
				}
			}
			if tc.wantHeaders != nil {
				if got, _ := entry["headers"].(map[string]string); !reflect.DeepEqual(got, tc.wantHeaders) {
					t.Errorf("headers = %v, want %v", got, tc.wantHeaders)
				}
			}
			for _, m := range []map[string]string{
				toStringMap(entry["env"]), toStringMap(entry["headers"]),
			} {
				for k, v := range m {
					if strings.TrimSpace(v) == "" {
						t.Errorf("%q was written empty; an empty credential fails inside the tool, far from the cause", k)
					}
				}
			}

			if tc.wantWarning && len(warnings) == 0 {
				t.Fatal("an unresolved credential produced no warning")
			}
			if !tc.wantWarning && len(warnings) != 0 {
				t.Fatalf("unexpected warnings: %v", warnings)
			}
			if tc.wantWarning {
				name := tc.credential.Env + tc.credential.Header
				if !strings.Contains(warnings[0].Message, name) {
					t.Errorf("warning %q does not name the injection point %q", warnings[0].Message, name)
				}
			}
		})
	}
}

// TestPlanProjectScopeIndirectionKeepsTheSecretOutOfTheFile is the reason
// indirection is preferred: .mcp.json is the shareable, committed file.
func TestPlanProjectScopeIndirectionKeepsTheSecretOutOfTheFile(t *testing.T) {
	d := newPlanDirs(t)
	opts := d.opts(true)
	opts.Credentials = map[string]string{"GITHUB_TOKEN": fakeGitHubToken}

	plan, _, err := newPlanAdapter().Plan([]model.Component{model.MCPServer{Spec: model.MCPServerSpec{
		Name:        "github",
		Scope:       model.ScopeProject,
		Transport:   model.TransportStdio,
		Command:     "npx",
		Credentials: []model.Credential{{Env: "GITHUB_TOKEN"}},
	}}}, opts)
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if rendered := fmt.Sprintf("%v", plan.Ops); strings.Contains(rendered, fakeGitHubToken) {
		t.Errorf("the resolved secret reached the plan for .mcp.json: %s", rendered)
	}
}

// TestPlanSkipsProjectScopeWithoutProjectDir: no project directory means no
// place to write, and guessing one is worse than skipping.
func TestPlanSkipsProjectScopeWithoutProjectDir(t *testing.T) {
	cases := []struct {
		name      string
		component model.Component
	}{
		{"skill", model.Skill{Spec: model.SkillSpec{Name: "deploy-check", Scope: model.ScopeProject, Dir: "skills/deploy-check"}}},
		{"mcp server", model.MCPServer{Spec: model.MCPServerSpec{
			Name: "github", Scope: model.ScopeProject, Transport: model.TransportStdio, Command: "npx"}}},
		{"agent", model.Agent{Spec: model.AgentSpec{Name: "db-migrator", Scope: model.ScopeProject, Path: "agents/db-migrator.md"}}},
		{"command", model.Command{Spec: model.CommandSpec{Name: "review", Scope: model.ScopeProject, Path: "prompts/review.md"}}},
		{"rule", model.Rule{Spec: model.RuleSpec{Name: "conventions", Scope: model.ScopeProject, Path: "rules/conventions.md",
			Render: map[model.ToolID]string{model.ToolClaudeCode: "CLAUDE.md"}}}},
		{"setting", model.Setting{Spec: model.SettingSpec{Name: "shared", Scope: model.ScopeProject,
			Values: map[string]any{"model": "claude-sonnet-4"}}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPlanDirs(t)
			plan, warnings, err := newPlanAdapter().Plan([]model.Component{tc.component}, d.opts(false))
			if err != nil {
				t.Fatalf("Plan() error: %v", err)
			}
			if !plan.IsEmpty() {
				t.Errorf("planned %d operation(s) without a project directory:\n  %s", len(plan.Ops), joinOps(plan.Ops))
			}
			if len(warnings) != 1 {
				t.Fatalf("want exactly one warning, got %v", warnings)
			}
			if !strings.Contains(warnings[0].Message, tc.component.Name()) {
				t.Errorf("warning %q does not name the skipped component", warnings[0].Message)
			}
		})
	}
}

// TestPlanWarnsInsteadOfDropping: everything the adapter cannot place is
// reported, and a path that would escape its root is refused.
func TestPlanWarnsInsteadOfDropping(t *testing.T) {
	cases := []struct {
		name      string
		component func(d planDirs) model.Component
		wantOps   int
		wantIn    string
	}{
		{
			name: "rule with no claude-code render entry falls back to CLAUDE.md",
			component: func(d planDirs) model.Component {
				return model.Rule{Spec: model.RuleSpec{
					Name: "conventions", Scope: model.ScopeGlobal, Path: "rules/conventions.md",
					Render: map[model.ToolID]string{model.ToolCodex: "AGENTS.md"},
				}}
			},
			wantOps: 1,
			wantIn:  "CLAUDE.md",
		},
		{
			name: "rule whose render path escapes the target is skipped",
			component: func(d planDirs) model.Component {
				return model.Rule{Spec: model.RuleSpec{
					Name: "conventions", Scope: model.ScopeGlobal, Path: "rules/conventions.md",
					Render: map[model.ToolID]string{model.ToolClaudeCode: "../../CLAUDE.md"},
				}}
			},
			wantIn: "not a relative path",
		},
		{
			name: "component name that would escape its directory is skipped",
			component: func(d planDirs) model.Component {
				return model.Skill{Spec: model.SkillSpec{
					Name: "../evil", Scope: model.ScopeGlobal, Dir: "skills/deploy-check",
				}}
			},
			wantIn: "cannot be used as a file name",
		},
		{
			name: "bundled path that escapes the pack is skipped",
			component: func(d planDirs) model.Component {
				return model.Agent{Spec: model.AgentSpec{
					Name: "db-migrator", Scope: model.ScopeGlobal, Path: "../outside.md",
				}}
			},
			wantIn: "escapes the pack directory",
		},
		{
			name: "missing bundled content is reported, not assumed",
			component: func(d planDirs) model.Component {
				return model.Skill{Spec: model.SkillSpec{
					Name: "absent", Scope: model.ScopeGlobal, Dir: "skills/absent",
				}}
			},
			wantIn: "no bundled content",
		},
		{
			name: "mcp server with nothing to start is skipped",
			component: func(d planDirs) model.Component {
				return model.MCPServer{Spec: model.MCPServerSpec{Name: "broken", Scope: model.ScopeGlobal}}
			},
			wantIn: "no command or url",
		},
		{
			name: "settings with no values are reported",
			component: func(d planDirs) model.Component {
				return model.Setting{Spec: model.SettingSpec{Name: "empty", Scope: model.ScopeGlobal}}
			},
			wantIn: "carries no values",
		},
		{
			name:      "an unknown component kind is reported",
			component: func(d planDirs) model.Component { return unknownComponent{} },
			wantIn:    "cannot apply",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newPlanDirs(t)
			plan, warnings, err := newPlanAdapter().Plan([]model.Component{tc.component(d)}, d.opts(true))
			if err != nil {
				t.Fatalf("Plan() error: %v", err)
			}
			if len(plan.Ops) != tc.wantOps {
				t.Errorf("got %d operations, want %d:\n  %s", len(plan.Ops), tc.wantOps, joinOps(plan.Ops))
			}
			if len(warnings) == 0 {
				t.Fatal("component was dropped silently")
			}
			joined := fmt.Sprint(warnings)
			if !strings.Contains(joined, tc.wantIn) {
				t.Errorf("warnings %q do not mention %q", joined, tc.wantIn)
			}
		})
	}
}

// TestPlanReplaceModeOverwritesFilesOnly: replace turns file creation into
// replacement (the executor still backs the file up) and settings merges from
// deep into set — but never widens a merge into a whole-file write.
func TestPlanReplaceModeOverwritesFilesOnly(t *testing.T) {
	d := newPlanDirs(t)
	opts := d.opts(false)
	opts.Replace = true

	components := []model.Component{
		model.Agent{Spec: model.AgentSpec{Name: "db-migrator", Scope: model.ScopeGlobal, Path: "agents/db-migrator.md"}},
		model.Setting{Spec: model.SettingSpec{Name: "s", Scope: model.ScopeGlobal, Values: map[string]any{"model": "claude-sonnet-4"}}},
	}
	plan, warnings, err := newPlanAdapter().Plan(components, opts)
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if plan.Ops[0].Kind != engine.OpReplaceFile {
		t.Errorf("file operation kind = %q, want %q", plan.Ops[0].Kind, engine.OpReplaceFile)
	}
	if plan.Ops[1].Strategy != engine.MergeSet {
		t.Errorf("settings strategy = %q, want %q", plan.Ops[1].Strategy, engine.MergeSet)
	}
	if plan.Ops[1].Path != filepath.Join(d.home, ".claude", "settings.json") {
		t.Errorf("settings path = %q, want ~/.claude/settings.json", plan.Ops[1].Path)
	}
}

// TestPlanWritesNothing: a Planner returns intent. Only the executor writes.
func TestPlanWritesNothing(t *testing.T) {
	d := newPlanDirs(t)
	opts := d.opts(true)
	opts.Credentials = map[string]string{"GITHUB_TOKEN": fakeGitHubToken}

	components := []model.Component{
		model.Skill{Spec: model.SkillSpec{Name: "deploy-check", Scope: model.ScopeGlobal, Dir: "skills/deploy-check"}},
		model.MCPServer{Spec: model.MCPServerSpec{Name: "github", Scope: model.ScopeGlobal,
			Transport: model.TransportStdio, Command: "npx", Credentials: []model.Credential{{Env: "GITHUB_TOKEN"}}}},
		model.Agent{Spec: model.AgentSpec{Name: "db-migrator", Scope: model.ScopeProject, Path: "agents/db-migrator.md"}},
		model.Command{Spec: model.CommandSpec{Name: "review", Scope: model.ScopeProject, Path: "prompts/review.md"}},
		model.Rule{Spec: model.RuleSpec{Name: "conventions", Scope: model.ScopeProject, Path: "rules/conventions.md",
			Render: map[model.ToolID]string{model.ToolClaudeCode: "CLAUDE.md"}}},
		model.Setting{Spec: model.SettingSpec{Name: "s", Scope: model.ScopeGlobal, Values: map[string]any{"model": "claude-sonnet-4"}}},
	}
	plan, warnings, err := newPlanAdapter().Plan(components, opts)
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("plan does not validate: %v", err)
	}
	for _, dir := range []string{d.home, d.project} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Errorf("Plan() wrote to %s: %v", dir, entries)
		}
	}
}

// TestPlanAppliesThroughTheExecutor runs a plan through the real executor.
// It is the end-to-end check that the operations this adapter emits are ones
// the executor can actually perform, and — the point of merging at a key path
// — that the app state living in ~/.claude.json next to the MCP config
// survives a restore untouched.
func TestPlanAppliesThroughTheExecutor(t *testing.T) {
	d := newPlanDirs(t)

	// A ~/.claude.json as it really looks: MCP config blended with app state
	// and per-project history.
	existing := map[string]any{
		"numStartups":          7,
		"installMethod":        "npm",
		"projects":             map[string]any{"/work/thing": map[string]any{"history": []any{"hello"}}},
		"mcpServers":           map[string]any{"already-here": map[string]any{"type": "stdio", "command": "other"}},
		"hasCompletedOnboardi": true,
	}
	writeJSON(t, filepath.Join(d.home, ".claude.json"), existing)
	writeJSON(t, filepath.Join(d.home, ".claude", "settings.json"), map[string]any{
		"permissions": map[string]any{"deny": []any{"Bash(rm -rf:*)"}},
	})

	opts := d.opts(false)
	opts.Credentials = map[string]string{"GITHUB_TOKEN": fakeGitHubToken}
	components := []model.Component{
		model.Skill{Spec: model.SkillSpec{Name: "deploy-check", Scope: model.ScopeGlobal, Dir: "skills/deploy-check"}},
		model.MCPServer{Spec: model.MCPServerSpec{Name: "github", Scope: model.ScopeGlobal,
			Transport: model.TransportStdio, Command: "npx",
			Credentials: []model.Credential{{Env: "GITHUB_TOKEN"}}}},
		model.Setting{Spec: model.SettingSpec{Name: "s", Scope: model.ScopeGlobal,
			Values: map[string]any{"permissions": map[string]any{"allow": []any{"Bash(npm run test:*)"}}}}},
	}

	plan, warnings, err := newPlanAdapter().Plan(components, opts)
	if err != nil {
		t.Fatalf("Plan() error: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}

	ex := &engine.Executor{BackupRoot: t.TempDir()}
	if _, err := ex.Apply(plan); err != nil {
		t.Fatalf("the executor could not apply this plan: %v", err)
	}

	if got := readFile(t, filepath.Join(d.home, ".claude", "skills", "deploy-check", "SKILL.md")); got != skillMD {
		t.Errorf("skill content = %q, want %q", got, skillMD)
	}

	claudeJSON := readJSON(t, filepath.Join(d.home, ".claude.json"))
	for _, key := range []string{"numStartups", "installMethod", "projects", "hasCompletedOnboardi"} {
		if _, ok := claudeJSON[key]; !ok {
			t.Errorf("app state key %q was lost from ~/.claude.json", key)
		}
	}
	servers, _ := claudeJSON["mcpServers"].(map[string]any)
	if _, ok := servers["already-here"]; !ok {
		t.Error("the user's existing MCP server was lost")
	}
	added, _ := servers["github"].(map[string]any)
	env, _ := added["env"].(map[string]any)
	if env["GITHUB_TOKEN"] != fakeGitHubToken {
		t.Errorf("injected credential = %v, want the resolved value", env["GITHUB_TOKEN"])
	}

	perms, _ := readJSON(t, filepath.Join(d.home, ".claude", "settings.json"))["permissions"].(map[string]any)
	if perms["deny"] == nil {
		t.Error("the user's permissions.deny was lost by the settings merge")
	}
	if perms["allow"] == nil {
		t.Error("the pack's permissions.allow was not applied")
	}
}

func writeJSON(t *testing.T, path string, doc map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &doc); err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return doc
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// unknownComponent is a component kind no adapter knows, so that "nothing is
// dropped silently" can be tested at the switch's default arm.
type unknownComponent struct{}

func (unknownComponent) Kind() model.Kind   { return model.Kind("plugin") }
func (unknownComponent) Name() string       { return "marketplace-plugin" }
func (unknownComponent) Scope() model.Scope { return model.ScopeGlobal }
