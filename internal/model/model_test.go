package model

import (
	"reflect"
	"testing"
)

// fakeComponent is a minimal Component implementation for testing.
type fakeComponent struct {
	kind  Kind
	name  string
	scope Scope
}

func (f fakeComponent) Kind() Kind   { return f.kind }
func (f fakeComponent) Name() string { return f.name }
func (f fakeComponent) Scope() Scope { return f.scope }

func TestKindValid(t *testing.T) {
	tests := []struct {
		kind Kind
		want bool
	}{
		{KindSkill, true},
		{KindMCPServer, true},
		{KindAgent, true},
		{KindRule, true},
		{KindCommand, true},
		{KindSetting, true},
		{Kind(""), false},
		{Kind("plugin"), false},
		{Kind("Skill"), false}, // case-sensitive: manifest values are lowercase
	}
	for _, tt := range tests {
		if got := tt.kind.Valid(); got != tt.want {
			t.Errorf("Kind(%q).Valid() = %v, want %v", tt.kind, got, tt.want)
		}
	}
}

func TestKindsStableOrder(t *testing.T) {
	want := []Kind{KindSkill, KindMCPServer, KindAgent, KindRule, KindCommand, KindSetting}
	if got := Kinds(); !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds() = %v, want %v", got, want)
	}
	// Every enumerated kind must be valid.
	for _, k := range Kinds() {
		if !k.Valid() {
			t.Errorf("Kinds() contains invalid kind %q", k)
		}
	}
}

func TestKindStringValues(t *testing.T) {
	// These string values appear in machine-readable scan output; changing
	// them is a breaking change, so pin them. (Manifests use the plural
	// section keys, mapped in packio — not these values.)
	tests := []struct {
		kind Kind
		want string
	}{
		{KindSkill, "skill"},
		{KindMCPServer, "mcp_server"},
		{KindAgent, "agent"},
		{KindRule, "rule"},
		{KindCommand, "command"},
		{KindSetting, "setting"},
	}
	for _, tt := range tests {
		if got := string(tt.kind); got != tt.want {
			t.Errorf("kind constant = %q, want %q", got, tt.want)
		}
	}
}

func TestScopeValid(t *testing.T) {
	tests := []struct {
		scope Scope
		want  bool
	}{
		{ScopeGlobal, true},
		{ScopeProject, true},
		{Scope(""), false},
		{Scope("local"), false},
		{Scope("Global"), false},
	}
	for _, tt := range tests {
		if got := tt.scope.Valid(); got != tt.want {
			t.Errorf("Scope(%q).Valid() = %v, want %v", tt.scope, got, tt.want)
		}
	}
}

func TestScopeStringValues(t *testing.T) {
	if string(ScopeGlobal) != "global" {
		t.Errorf("ScopeGlobal = %q, want %q", ScopeGlobal, "global")
	}
	if string(ScopeProject) != "project" {
		t.Errorf("ScopeProject = %q, want %q", ScopeProject, "project")
	}
}

func TestToolIDValues(t *testing.T) {
	// Canonical adapter ids per docs/spec/pack-manifest.md `targets`.
	tests := []struct {
		tool ToolID
		want string
	}{
		{ToolClaudeCode, "claude-code"},
		{ToolCodex, "codex"},
		{ToolCursor, "cursor"},
		{ToolGeminiCLI, "gemini-cli"},
	}
	for _, tt := range tests {
		if got := string(tt.tool); got != tt.want {
			t.Errorf("tool constant = %q, want %q", got, tt.want)
		}
		if !tt.tool.Valid() {
			t.Errorf("ToolID(%q).Valid() = false, want true", tt.tool)
		}
	}
	if ToolID("claude").Valid() {
		t.Error(`ToolID("claude").Valid() = true, want false`)
	}
	if ToolID("").Valid() {
		t.Error(`ToolID("").Valid() = true, want false`)
	}
}

func TestToolsStableOrder(t *testing.T) {
	want := []ToolID{ToolClaudeCode, ToolCodex, ToolCursor, ToolGeminiCLI}
	if got := Tools(); !reflect.DeepEqual(got, want) {
		t.Errorf("Tools() = %v, want %v", got, want)
	}
	for _, tool := range Tools() {
		if !tool.Valid() {
			t.Errorf("Tools() contains invalid tool %q", tool)
		}
	}
}

func TestInventoryByKind(t *testing.T) {
	inv := Inventory{
		Tool: ToolClaudeCode,
		Components: []Component{
			fakeComponent{KindSkill, "brainstorming", ScopeGlobal},
			fakeComponent{KindMCPServer, "github", ScopeGlobal},
			fakeComponent{KindSkill, "code-review", ScopeProject},
		},
	}

	skills := inv.ByKind(KindSkill)
	if len(skills) != 2 {
		t.Fatalf("ByKind(KindSkill) returned %d components, want 2", len(skills))
	}
	if skills[0].Name() != "brainstorming" || skills[1].Name() != "code-review" {
		t.Errorf("ByKind(KindSkill) = [%s, %s], want scan order preserved",
			skills[0].Name(), skills[1].Name())
	}

	if got := inv.ByKind(KindAgent); len(got) != 0 {
		t.Errorf("ByKind(KindAgent) on inventory without agents = %v, want empty", got)
	}
}

func TestInventoryByKindEmptyInventory(t *testing.T) {
	var inv Inventory
	if got := inv.ByKind(KindSkill); len(got) != 0 {
		t.Errorf("ByKind on zero-value inventory = %v, want empty", got)
	}
}

func TestWarningString(t *testing.T) {
	tests := []struct {
		name string
		w    Warning
		want string
	}{
		{
			name: "with path",
			w:    Warning{Path: "/home/user/.claude.json", Message: "unknown key \"foo\""},
			want: `/home/user/.claude.json: unknown key "foo"`,
		},
		{
			name: "without path",
			w:    Warning{Message: "tool not detected"},
			want: "tool not detected",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.String(); got != tt.want {
				t.Errorf("Warning.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
