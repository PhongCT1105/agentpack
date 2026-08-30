package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

type stubAdapter struct {
	id        model.ToolID
	installed bool
	version   string
	inv       model.Inventory
}

func (s stubAdapter) ID() model.ToolID              { return s.id }
func (s stubAdapter) Detect() (bool, string, error) { return s.installed, s.version, nil }
func (s stubAdapter) Scan(model.ScanScope) (model.Inventory, error) {
	return s.inv, nil
}

func stubAdapters() []engine.Adapter {
	return []engine.Adapter{
		stubAdapter{
			id: model.ToolClaudeCode, installed: true, version: "2.0.44",
			inv: model.Inventory{
				Tool: model.ToolClaudeCode,
				Components: []model.Component{
					model.Skill{Spec: model.SkillSpec{
						Name: "brainstorming", Scope: model.ScopeGlobal,
						Dir:         "/home/user/.claude/skills/brainstorming",
						Description: "Explore intent first",
					}},
					model.MCPServer{Spec: model.MCPServerSpec{
						Name: "github", Scope: model.ScopeGlobal,
						Transport: model.TransportStdio, Command: "npx",
						Args: []string{"-y", "@modelcontextprotocol/server-github"},
						Env:  map[string]string{"GITHUB_TOKEN": "ghp_FAKEFAKE"},
					}},
					model.Rule{Spec: model.RuleSpec{
						Name: "CLAUDE.md", Scope: model.ScopeProject,
						Path: "/home/user/projects/demo/CLAUDE.md",
					}},
				},
				Warnings: []model.Warning{
					{Path: "/home/user/.claude/skills/broken", Message: "no SKILL.md"},
				},
			},
		},
		stubAdapter{id: model.ToolCodex, installed: false},
	}
}

func runScan(t *testing.T, args ...string) string {
	t.Helper()
	cmd := newScanCmd(stubAdapters)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	return buf.String()
}

func TestScanTableOutput(t *testing.T) {
	out := runScan(t)

	for _, want := range []string{
		"claude-code 2.0.44",
		"brainstorming",
		"Explore intent first",
		"github",
		"CLAUDE.md",
		"codex: not detected",
		"no SKILL.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q:\n%s", want, out)
		}
	}

	// Grouping: kind headers appear, and the global skill line precedes the
	// project rule line.
	if !strings.Contains(out, "skill") || !strings.Contains(out, "rule") {
		t.Errorf("kind grouping missing:\n%s", out)
	}
	if strings.Index(out, "brainstorming") > strings.Index(out, "CLAUDE.md") {
		t.Errorf("expected skills before rules:\n%s", out)
	}

	// Secret hygiene: raw env values never reach scan output.
	if strings.Contains(out, "ghp_FAKEFAKE") {
		t.Errorf("env value leaked into table output:\n%s", out)
	}
	if !strings.Contains(out, "GITHUB_TOKEN") {
		t.Errorf("env key should be visible in table output:\n%s", out)
	}
}

func TestScanJSONOutput(t *testing.T) {
	out := runScan(t, "--json")

	var results []map[string]any
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		t.Fatalf("--json output is not valid JSON: %v\n%s", err, out)
	}
	if len(results) != 2 {
		t.Fatalf("got %d tools, want 2", len(results))
	}

	first := results[0]
	if first["tool"] != "claude-code" || first["installed"] != true || first["version"] != "2.0.44" {
		t.Errorf("first tool = %v", first)
	}
	comps, ok := first["components"].([]any)
	if !ok || len(comps) != 3 {
		t.Fatalf("components = %v", first["components"])
	}
	skill := comps[0].(map[string]any)
	if skill["kind"] != "skill" || skill["name"] != "brainstorming" || skill["scope"] != "global" {
		t.Errorf("skill JSON = %v", skill)
	}
	mcp := comps[1].(map[string]any)
	if mcp["transport"] != "stdio" || mcp["command"] != "npx" {
		t.Errorf("mcp JSON = %v", mcp)
	}

	// Secret hygiene in JSON too: keys visible, values masked.
	if strings.Contains(out, "ghp_FAKEFAKE") {
		t.Errorf("env value leaked into JSON output:\n%s", out)
	}
	env, ok := mcp["env"].(map[string]any)
	if !ok {
		t.Fatalf("mcp env = %v", mcp["env"])
	}
	if _, ok := env["GITHUB_TOKEN"]; !ok {
		t.Errorf("env key missing from JSON: %v", env)
	}

	second := results[1]
	if second["tool"] != "codex" || second["installed"] != false {
		t.Errorf("second tool = %v", second)
	}

	warnings, ok := first["warnings"].([]any)
	if !ok || len(warnings) != 1 {
		t.Fatalf("warnings JSON = %v", first["warnings"])
	}
	w := warnings[0].(map[string]any)
	if w["path"] == nil || w["message"] == nil {
		t.Errorf("warning JSON keys must be lowercase path/message, got %v", w)
	}
}

func TestMaskArgs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "header flag value masked",
			in:   []string{"-y", "mcp-remote", "https://host/sse", "--header", "Authorization: Bearer FAKE"},
			want: []string{"-y", "mcp-remote", "https://host/sse", "--header", "***"},
		},
		{
			name: "single-arg header masked by key",
			in:   []string{"Authorization: Bearer FAKE"},
			want: []string{"Authorization:***"},
		},
		{
			name: "key=value secret masked",
			in:   []string{"--api_key=FAKE123"},
			want: []string{"--api_key=***"},
		},
		{
			name: "innocuous args untouched",
			in:   []string{"-y", "@modelcontextprotocol/server-github", "/home/user/projects"},
			want: []string{"-y", "@modelcontextprotocol/server-github", "/home/user/projects"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maskArgs(tt.in)
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("maskArgs(%v) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestMaskURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://user:hunter2@host/mcp", "https://redacted@host/mcp"},
		{"https://host/mcp?api_key=FAKE", "https://host/mcp?api_key=%2A%2A%2A"},
		{"https://mcp.linear.app/mcp", "https://mcp.linear.app/mcp"},
		{"://bad url", "://bad url"},
	}
	for _, tt := range tests {
		if got := maskURL(tt.in); got != tt.want {
			t.Errorf("maskURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestSanitizeCell(t *testing.T) {
	if got := sanitizeCell("line one\nline two"); got != "line one …" {
		t.Errorf("sanitizeCell = %q", got)
	}
	if got := sanitizeCell("a\tb"); got != "a b" {
		t.Errorf("sanitizeCell tab = %q", got)
	}
}

func TestScanErrorKeepsPartialInventory(t *testing.T) {
	failing := func() []engine.Adapter {
		return []engine.Adapter{errAdapter{}}
	}
	cmd := newScanCmd(failing)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"claude-code 2.0.44", "error: project scan exploded", "brainstorming"} {
		if !strings.Contains(out, want) {
			t.Errorf("partial-inventory output missing %q:\n%s", want, out)
		}
	}
}

type errAdapter struct{}

func (errAdapter) ID() model.ToolID              { return model.ToolClaudeCode }
func (errAdapter) Detect() (bool, string, error) { return true, "2.0.44", nil }
func (errAdapter) Scan(model.ScanScope) (model.Inventory, error) {
	return model.Inventory{
		Tool: model.ToolClaudeCode,
		Components: []model.Component{
			model.Skill{Spec: model.SkillSpec{Name: "brainstorming", Scope: model.ScopeGlobal}},
		},
	}, errors.New("project scan exploded")
}

func TestScanRegisteredOnRoot(t *testing.T) {
	out, err := execute(t, "--help")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "scan") {
		t.Errorf("root help does not list scan:\n%s", out)
	}
}
