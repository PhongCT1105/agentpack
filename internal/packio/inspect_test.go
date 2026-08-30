package packio

import (
	"reflect"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

func TestCredentialRequirements(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		want     []CredentialRequirement
	}{
		{
			name:     "no servers",
			manifest: Manifest{},
			want:     nil,
		},
		{
			name: "servers without credentials",
			manifest: Manifest{Components: Components{MCPServers: []MCPServer{
				{ComponentMeta: ComponentMeta{Name: "filesystem"}, Transport: model.TransportStdio, Command: "npx"},
			}}},
			want: nil,
		},
		{
			name: "env and header credentials flatten in manifest order",
			manifest: Manifest{Components: Components{MCPServers: []MCPServer{
				{
					ComponentMeta: ComponentMeta{Name: "github"},
					Transport:     model.TransportStdio,
					Command:       "npx",
					Credentials: []Credential{
						{Env: "GITHUB_TOKEN", Description: "GitHub PAT", ObtainURL: "https://github.com/settings/tokens"},
					},
				},
				{
					ComponentMeta: ComponentMeta{Name: "supabase"},
					Transport:     model.TransportHTTP,
					URL:           "https://mcp.supabase.com/mcp",
					Credentials: []Credential{
						{Header: "Authorization", Format: "Bearer {value}", Description: "Supabase token"},
						{Env: "SUPABASE_PROJECT_REF", Description: "project ref"},
					},
				},
			}}},
			want: []CredentialRequirement{
				{Server: "github", Credential: Credential{Env: "GITHUB_TOKEN", Description: "GitHub PAT", ObtainURL: "https://github.com/settings/tokens"}},
				{Server: "supabase", Credential: Credential{Header: "Authorization", Format: "Bearer {value}", Description: "Supabase token"}},
				{Server: "supabase", Credential: Credential{Env: "SUPABASE_PROJECT_REF", Description: "project ref"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.manifest.CredentialRequirements()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("CredentialRequirements() = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestExternalServices(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		want     []ExternalService
	}{
		{
			name:     "empty manifest",
			manifest: Manifest{},
			want:     nil,
		},
		{
			name: "bundled-only pack has no external services",
			manifest: Manifest{Components: Components{
				Skills:   []Skill{{ComponentMeta: ComponentMeta{Name: "notes"}, Source: Source{Bundled: "skills/notes"}}},
				Rules:    []Rule{{ComponentMeta: ComponentMeta{Name: "conv"}, Source: Source{Bundled: "rules/conv.md"}}},
				Commands: []Command{{ComponentMeta: ComponentMeta{Name: "review"}, Source: Source{Bundled: "prompts/review.md"}}},
				MCPServers: []MCPServer{
					{ComponentMeta: ComponentMeta{Name: "fs"}, Transport: model.TransportStdio, Command: "npx"},
				},
			}},
			want: nil,
		},
		{
			name: "plugin, npm, and remote servers in section order",
			manifest: Manifest{Components: Components{
				Skills: []Skill{
					{ComponentMeta: ComponentMeta{Name: "superpowers"}, Source: Source{Plugin: "superpowers@claude-plugins-official"}},
					{ComponentMeta: ComponentMeta{Name: "find-skills"}, Source: Source{NPM: "skills", Ref: "vercel-labs/find-skills"}},
					{ComponentMeta: ComponentMeta{Name: "notes"}, Source: Source{Bundled: "skills/notes"}},
				},
				MCPServers: []MCPServer{
					{ComponentMeta: ComponentMeta{Name: "github"}, Transport: model.TransportStdio, Command: "npx"},
					{ComponentMeta: ComponentMeta{Name: "supabase"}, Transport: model.TransportHTTP, URL: "https://mcp.supabase.com/mcp"},
					{ComponentMeta: ComponentMeta{Name: "events"}, Transport: model.TransportSSE, URL: "https://events.example.com/sse"},
				},
				Agents: []Agent{
					{ComponentMeta: ComponentMeta{Name: "helper"}, Source: Source{NPM: "agent-helper"}},
				},
			}},
			want: []ExternalService{
				{ComponentRef: "skills/superpowers", Kind: "plugin marketplace", Ref: "superpowers@claude-plugins-official"},
				{ComponentRef: "skills/find-skills", Kind: "npm package", Ref: "skills (vercel-labs/find-skills)"},
				{ComponentRef: "mcp_servers/supabase", Kind: "remote MCP server (http)", Ref: "https://mcp.supabase.com/mcp"},
				{ComponentRef: "mcp_servers/events", Kind: "remote MCP server (sse)", Ref: "https://events.example.com/sse"},
				{ComponentRef: "agents/helper", Kind: "npm package", Ref: "agent-helper"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.manifest.ExternalServices()
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExternalServices() = %#v\nwant %#v", got, tt.want)
			}
		})
	}
}

func TestProjectScoped(t *testing.T) {
	tests := []struct {
		name     string
		manifest Manifest
		want     bool
	}{
		{"empty", Manifest{}, false},
		{
			"all global (default scope)",
			Manifest{Components: Components{
				Skills:   []Skill{{ComponentMeta: ComponentMeta{Name: "a"}}},
				Settings: []Setting{{ComponentMeta: ComponentMeta{Name: "s"}}},
			}},
			false,
		},
		{
			"explicit global",
			Manifest{Components: Components{
				Rules: []Rule{{ComponentMeta: ComponentMeta{Name: "r", Scope: model.ScopeGlobal}}},
			}},
			false,
		},
		{
			"one project-scoped rule",
			Manifest{Components: Components{
				Skills: []Skill{{ComponentMeta: ComponentMeta{Name: "a"}}},
				Rules:  []Rule{{ComponentMeta: ComponentMeta{Name: "r", Scope: model.ScopeProject}}},
			}},
			true,
		},
		{
			"project-scoped mcp server",
			Manifest{Components: Components{
				MCPServers: []MCPServer{{ComponentMeta: ComponentMeta{Name: "m", Scope: model.ScopeProject}}},
			}},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.manifest.ProjectScoped(); got != tt.want {
				t.Errorf("ProjectScoped() = %v, want %v", got, tt.want)
			}
		})
	}
}
