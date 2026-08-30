package model

import "testing"

// Concrete component types must satisfy Component.
var (
	_ Component = Skill{}
	_ Component = MCPServer{}
)

func TestSkillComponent(t *testing.T) {
	s := Skill{Spec: SkillSpec{
		Name:        "brainstorming",
		Scope:       ScopeGlobal,
		Dir:         "/home/user/.claude/skills/brainstorming",
		Description: "Explore intent before implementation",
	}}
	if s.Kind() != KindSkill {
		t.Errorf("Skill.Kind() = %q, want %q", s.Kind(), KindSkill)
	}
	if s.Name() != "brainstorming" {
		t.Errorf("Skill.Name() = %q, want %q", s.Name(), "brainstorming")
	}
	if s.Scope() != ScopeGlobal {
		t.Errorf("Skill.Scope() = %q, want %q", s.Scope(), ScopeGlobal)
	}
}

func TestMCPServerComponent(t *testing.T) {
	m := MCPServer{Spec: MCPServerSpec{
		Name:      "github",
		Scope:     ScopeProject,
		Transport: TransportStdio,
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-github"},
		Env:       map[string]string{"GITHUB_API_URL": "https://api.github.com"},
	}}
	if m.Kind() != KindMCPServer {
		t.Errorf("MCPServer.Kind() = %q, want %q", m.Kind(), KindMCPServer)
	}
	if m.Name() != "github" {
		t.Errorf("MCPServer.Name() = %q, want %q", m.Name(), "github")
	}
	if m.Scope() != ScopeProject {
		t.Errorf("MCPServer.Scope() = %q, want %q", m.Scope(), ScopeProject)
	}
}

func TestTransportValid(t *testing.T) {
	tests := []struct {
		tr   Transport
		want bool
	}{
		{TransportStdio, true},
		{TransportHTTP, true},
		{TransportSSE, true},
		{Transport(""), false},
		{Transport("websocket"), false},
	}
	for _, tt := range tests {
		if got := tt.tr.Valid(); got != tt.want {
			t.Errorf("Transport(%q).Valid() = %v, want %v", tt.tr, got, tt.want)
		}
	}
	// Wire values per docs/spec/pack-manifest.md.
	if TransportStdio != "stdio" || TransportHTTP != "http" || TransportSSE != "sse" {
		t.Errorf("transport constants = %q/%q/%q, want stdio/http/sse",
			TransportStdio, TransportHTTP, TransportSSE)
	}
}
