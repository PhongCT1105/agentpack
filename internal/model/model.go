// Package model defines the neutral component model shared by all adapters.
// Adapters normalize tool-specific config into these types on scan; the pack
// writer and restore planner consume them. The core never touches tool config
// directly — see docs/architecture.md.
package model

// ToolID identifies a supported tool adapter. Values are the canonical
// adapter ids used in pack manifests (targets:) — see docs/spec/pack-manifest.md.
type ToolID string

const (
	ToolClaudeCode ToolID = "claude-code"
	ToolCodex      ToolID = "codex"
	ToolCursor     ToolID = "cursor"
	ToolGeminiCLI  ToolID = "gemini-cli"
)

// Tools returns all supported tool ids in canonical order.
func Tools() []ToolID {
	return []ToolID{ToolClaudeCode, ToolCodex, ToolCursor, ToolGeminiCLI}
}

// Valid reports whether t is a known tool id.
func (t ToolID) Valid() bool {
	switch t {
	case ToolClaudeCode, ToolCodex, ToolCursor, ToolGeminiCLI:
		return true
	}
	return false
}

// Kind classifies a component. The string values appear in machine-readable
// scan output, so changing one is a breaking change. Note: pack manifests use
// the plural section keys (skills:, mcp_servers:, …), not these values; the
// Kind → section-key mapping lives with the manifest types in packio.
type Kind string

const (
	KindSkill     Kind = "skill"
	KindMCPServer Kind = "mcp_server"
	KindAgent     Kind = "agent"
	KindRule      Kind = "rule"
	KindCommand   Kind = "command"
	KindSetting   Kind = "setting"
)

// Kinds returns all component kinds in canonical display order.
func Kinds() []Kind {
	return []Kind{KindSkill, KindMCPServer, KindAgent, KindRule, KindCommand, KindSetting}
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case KindSkill, KindMCPServer, KindAgent, KindRule, KindCommand, KindSetting:
		return true
	}
	return false
}

// Scope says where a component lives: user-level config or a project directory.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool {
	return s == ScopeGlobal || s == ScopeProject
}

// Component is the neutral representation of one portable piece of tool
// config. Concrete types (skills, MCP servers, …) live alongside the adapters
// that produce them and implement this interface.
type Component interface {
	Kind() Kind
	Name() string
	Scope() Scope
}

// Warning records something a scan saw but could not model. Warnings surface
// to the user instead of being silently dropped.
type Warning struct {
	Path    string // file or directory the warning refers to, if any
	Message string
}

func (w Warning) String() string {
	if w.Path == "" {
		return w.Message
	}
	return w.Path + ": " + w.Message
}

// Inventory is the result of scanning one tool.
type Inventory struct {
	Tool       ToolID
	Components []Component
	Warnings   []Warning
}

// ByKind returns the components of kind k, preserving scan order.
func (inv Inventory) ByKind(k Kind) []Component {
	var out []Component
	for _, c := range inv.Components {
		if c.Kind() == k {
			out = append(out, c)
		}
	}
	return out
}
