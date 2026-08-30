package packio

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PhongCT1105/agentpack/internal/model"
)

// specExample is a full manifest exercising every component kind and every
// source/credential variant from docs/spec/pack-manifest.md v0.1.
const specExample = `apiVersion: agentpack/v0
kind: Pack

metadata:
  name: fullstack-startup
  title: Full-Stack Startup Engineer
  description: >
    Frontend, debugging, testing, code review, GitHub integration,
    browser automation, and database tools for full-stack work.
  author: PhongCT1105
  license: MIT
  tags: [fullstack, react, supabase, github]

targets: [claude-code, codex, cursor, gemini-cli]

components:
  skills:
    - name: superpowers
      source:
        plugin: superpowers@claude-plugins-official
    - name: find-skills
      source:
        npm: "skills"
        ref: vercel-labs/find-skills
    - name: my-custom-skill
      source:
        bundled: skills/my-custom-skill

  mcp_servers:
    - name: github
      transport: stdio
      command: npx
      args: ["-y", "@modelcontextprotocol/server-github"]
      env:
        GITHUB_API_URL: https://api.github.com
      credentials:
        - env: GITHUB_TOKEN
          description: GitHub personal access token (repo scope)
          obtain_url: https://github.com/settings/tokens
    - name: supabase
      transport: http
      url: https://mcp.supabase.com/mcp
      headers:
        X-Client-Info: agentpack
      credentials:
        - header: Authorization
          format: "Bearer {value}"
          description: Supabase access token
          obtain_url: https://supabase.com/dashboard/account/tokens

  agents:
    - name: db-migrator
      source:
        bundled: agents/db-migrator.md
      targets: [claude-code]

  rules:
    - name: engineering-conventions
      source:
        bundled: rules/conventions.md
      scope: project
      render:
        claude-code: CLAUDE.md
        codex: AGENTS.md
        gemini-cli: GEMINI.md
        cursor: .cursor/rules/conventions.mdc

  commands:
    - name: review
      description: structured code review prompt
      source:
        bundled: prompts/review.md
      targets: [claude-code, codex]
      optional: true

  settings:
    - name: claude-permissions
      targets: [claude-code]
      values:
        permissions:
          allow:
            - "Bash(npm run test:*)"
            - "Bash(gh pr view:*)"
`

// specExampleManifest is the struct the specExample YAML must decode to.
func specExampleManifest() *Manifest {
	return &Manifest{
		APIVersion: APIVersion,
		Kind:       KindPack,
		Metadata: Metadata{
			Name:  "fullstack-startup",
			Title: "Full-Stack Startup Engineer",
			Description: "Frontend, debugging, testing, code review, GitHub integration, " +
				"browser automation, and database tools for full-stack work.\n",
			Author:  "PhongCT1105",
			License: "MIT",
			Tags:    []string{"fullstack", "react", "supabase", "github"},
		},
		Targets: []model.ToolID{model.ToolClaudeCode, model.ToolCodex, model.ToolCursor, model.ToolGeminiCLI},
		Components: Components{
			Skills: []Skill{
				{
					ComponentMeta: ComponentMeta{Name: "superpowers"},
					Source:        Source{Plugin: "superpowers@claude-plugins-official"},
				},
				{
					ComponentMeta: ComponentMeta{Name: "find-skills"},
					Source:        Source{NPM: "skills", Ref: "vercel-labs/find-skills"},
				},
				{
					ComponentMeta: ComponentMeta{Name: "my-custom-skill"},
					Source:        Source{Bundled: "skills/my-custom-skill"},
				},
			},
			MCPServers: []MCPServer{
				{
					ComponentMeta: ComponentMeta{Name: "github"},
					Transport:     model.TransportStdio,
					Command:       "npx",
					Args:          []string{"-y", "@modelcontextprotocol/server-github"},
					Env:           map[string]string{"GITHUB_API_URL": "https://api.github.com"},
					Credentials: []Credential{
						{
							Env:         "GITHUB_TOKEN",
							Description: "GitHub personal access token (repo scope)",
							ObtainURL:   "https://github.com/settings/tokens",
						},
					},
				},
				{
					ComponentMeta: ComponentMeta{Name: "supabase"},
					Transport:     model.TransportHTTP,
					URL:           "https://mcp.supabase.com/mcp",
					Headers:       map[string]string{"X-Client-Info": "agentpack"},
					Credentials: []Credential{
						{
							Header:      "Authorization",
							Format:      "Bearer {value}",
							Description: "Supabase access token",
							ObtainURL:   "https://supabase.com/dashboard/account/tokens",
						},
					},
				},
			},
			Agents: []Agent{
				{
					ComponentMeta: ComponentMeta{Name: "db-migrator", Targets: []model.ToolID{model.ToolClaudeCode}},
					Source:        Source{Bundled: "agents/db-migrator.md"},
				},
			},
			Rules: []Rule{
				{
					ComponentMeta: ComponentMeta{Name: "engineering-conventions", Scope: model.ScopeProject},
					Source:        Source{Bundled: "rules/conventions.md"},
					Render: map[model.ToolID]string{
						model.ToolClaudeCode: "CLAUDE.md",
						model.ToolCodex:      "AGENTS.md",
						model.ToolGeminiCLI:  "GEMINI.md",
						model.ToolCursor:     ".cursor/rules/conventions.mdc",
					},
				},
			},
			Commands: []Command{
				{
					ComponentMeta: ComponentMeta{
						Name:        "review",
						Description: "structured code review prompt",
						Targets:     []model.ToolID{model.ToolClaudeCode, model.ToolCodex},
						Optional:    true,
					},
					Source: Source{Bundled: "prompts/review.md"},
				},
			},
			Settings: []Setting{
				{
					ComponentMeta: ComponentMeta{Name: "claude-permissions", Targets: []model.ToolID{model.ToolClaudeCode}},
					Values: map[string]any{
						"permissions": map[string]any{
							"allow": []any{"Bash(npm run test:*)", "Bash(gh pr view:*)"},
						},
					},
				},
			},
		},
	}
}

func TestDecodeManifestSpecExample(t *testing.T) {
	got, err := DecodeManifest([]byte(specExample))
	if err != nil {
		t.Fatalf("DecodeManifest(spec example) error: %v", err)
	}
	want := specExampleManifest()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeManifest(spec example) mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestRoundTripStructYAMLStruct(t *testing.T) {
	tests := []struct {
		name string
		in   *Manifest
	}{
		{
			name: "minimal",
			in: &Manifest{
				APIVersion: APIVersion,
				Kind:       KindPack,
				Metadata:   Metadata{Name: "tiny"},
			},
		},
		{
			name: "full spec example",
			in:   specExampleManifest(),
		},
		{
			name: "sse server with env credential and plain header",
			in: &Manifest{
				APIVersion: APIVersion,
				Kind:       KindPack,
				Metadata:   Metadata{Name: "sse-pack"},
				Targets:    []model.ToolID{model.ToolClaudeCode},
				Components: Components{
					MCPServers: []MCPServer{
						{
							ComponentMeta: ComponentMeta{
								Name:        "events",
								Description: "SSE event stream",
								Scope:       model.ScopeProject,
								Optional:    true,
							},
							Transport: model.TransportSSE,
							URL:       "https://events.example.com/sse",
							Headers:   map[string]string{"X-API-Version": "2"},
							Credentials: []Credential{
								{Env: "EVENTS_TOKEN", Description: "events API token"},
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := EncodeManifest(tt.in)
			if err != nil {
				t.Fatalf("EncodeManifest() error: %v", err)
			}
			got, err := DecodeManifest(data)
			if err != nil {
				t.Fatalf("DecodeManifest(EncodeManifest()) error: %v\nencoded:\n%s", err, data)
			}
			if !reflect.DeepEqual(got, tt.in) {
				t.Errorf("round trip mismatch\n got: %#v\nwant: %#v\nencoded:\n%s", got, tt.in, data)
			}
		})
	}
}

func TestRoundTripYAMLSemanticsPreserved(t *testing.T) {
	// decode → encode → decode must yield an identical struct: encoding may
	// normalize formatting but must never lose or change data.
	first, err := DecodeManifest([]byte(specExample))
	if err != nil {
		t.Fatalf("first DecodeManifest() error: %v", err)
	}
	data, err := EncodeManifest(first)
	if err != nil {
		t.Fatalf("EncodeManifest() error: %v", err)
	}
	second, err := DecodeManifest(data)
	if err != nil {
		t.Fatalf("second DecodeManifest() error: %v\nencoded:\n%s", err, data)
	}
	if !reflect.DeepEqual(first, second) {
		t.Errorf("decode→encode→decode changed data\nfirst:  %#v\nsecond: %#v", first, second)
	}
}

func TestEncodeManifestDeterministic(t *testing.T) {
	m := specExampleManifest()
	a, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("EncodeManifest() error: %v", err)
	}
	b, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("EncodeManifest() second call error: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("EncodeManifest() not deterministic\nfirst:\n%s\nsecond:\n%s", a, b)
	}
	if len(a) == 0 || a[len(a)-1] != '\n' {
		t.Errorf("EncodeManifest() output must end with a newline")
	}
}

func TestEncodeManifestOmitsEmptyOptionalFields(t *testing.T) {
	m := &Manifest{
		APIVersion: APIVersion,
		Kind:       KindPack,
		Metadata:   Metadata{Name: "tiny"},
	}
	data, err := EncodeManifest(m)
	if err != nil {
		t.Fatalf("EncodeManifest() error: %v", err)
	}
	out := string(data)
	// None of the optional keys should appear for a minimal manifest.
	for _, key := range []string{"title:", "description:", "author:", "license:", "tags:", "targets:", "skills:", "mcp_servers:", "agents:", "rules:", "commands:", "settings:", "optional:", "scope:"} {
		if strings.Contains(out, key) {
			t.Errorf("EncodeManifest(minimal) output contains %q, want omitted\noutput:\n%s", key, out)
		}
	}
}

func TestDecodeManifestErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr string // substring the error message must contain
	}{
		{
			name:    "empty input",
			in:      "",
			wantErr: "empty",
		},
		{
			name:    "not yaml",
			in:      "{[",
			wantErr: "yaml",
		},
		{
			name:    "unsupported apiVersion",
			in:      "apiVersion: agentpack/v99\nkind: Pack\nmetadata:\n  name: x\n",
			wantErr: "agentpack/v99",
		},
		{
			name:    "missing apiVersion",
			in:      "kind: Pack\nmetadata:\n  name: x\n",
			wantErr: "apiVersion",
		},
		{
			name:    "wrong kind",
			in:      "apiVersion: agentpack/v0\nkind: Sack\nmetadata:\n  name: x\n",
			wantErr: "Sack",
		},
		{
			name:    "unknown top-level field",
			in:      "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: x\ncomponentz: {}\n",
			wantErr: "componentz",
		},
		{
			name:    "unknown component field",
			in:      "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: x\ncomponents:\n  skills:\n    - name: s\n      sauce:\n        bundled: skills/s\n",
			wantErr: "sauce",
		},
		{
			// Security layer 1: the schema must refuse a secret value even
			// on purpose — a credential entry has no value field.
			name:    "credential value field rejected",
			in:      "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: x\ncomponents:\n  mcp_servers:\n    - name: m\n      transport: stdio\n      command: mcp\n      credentials:\n        - env: FAKE_TOKEN\n          value: not-a-real-secret\n",
			wantErr: "value",
		},
		{
			name:    "second yaml document",
			in:      "apiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: x\n---\napiVersion: agentpack/v0\nkind: Pack\nmetadata:\n  name: y\n",
			wantErr: "document",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := DecodeManifest([]byte(tt.in))
			if err == nil {
				t.Fatalf("DecodeManifest() = %#v, want error containing %q", m, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("DecodeManifest() error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestComponentMetaEffectiveScope(t *testing.T) {
	tests := []struct {
		name string
		meta ComponentMeta
		want model.Scope
	}{
		{"unset defaults to global", ComponentMeta{Name: "x"}, model.ScopeGlobal},
		{"explicit global", ComponentMeta{Name: "x", Scope: model.ScopeGlobal}, model.ScopeGlobal},
		{"explicit project", ComponentMeta{Name: "x", Scope: model.ScopeProject}, model.ScopeProject},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.meta.EffectiveScope(); got != tt.want {
				t.Errorf("EffectiveScope() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManifestFilenameConstant(t *testing.T) {
	// Pinned: the manifest filename is part of the pack format.
	if ManifestFilename != "agentpack.yaml" {
		t.Errorf("ManifestFilename = %q, want %q", ManifestFilename, "agentpack.yaml")
	}
	if APIVersion != "agentpack/v0" {
		t.Errorf("APIVersion = %q, want %q", APIVersion, "agentpack/v0")
	}
	if KindPack != "Pack" {
		t.Errorf("KindPack = %q, want %q", KindPack, "Pack")
	}
}
