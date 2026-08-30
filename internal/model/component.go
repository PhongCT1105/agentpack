package model

// ScanScope selects what a Scan covers. Zero value scans nothing.
type ScanScope struct {
	Global     bool   // scan user-level config (~)
	ProjectDir string // if non-empty, scan this project directory
}

// SkillSpec is the data of a scanned skill. Skill wraps it to satisfy
// Component; the Spec field keeps plain, literal-friendly data separate from
// the interface methods (whose names would collide with field names).
type SkillSpec struct {
	Name        string
	Scope       Scope
	Dir         string // absolute path of the directory containing SKILL.md
	Description string // from SKILL.md frontmatter, may be empty
}

// Skill is a neutral skill component (a directory with a SKILL.md).
type Skill struct {
	Spec SkillSpec
}

func (s Skill) Kind() Kind   { return KindSkill }
func (s Skill) Name() string { return s.Spec.Name }
func (s Skill) Scope() Scope { return s.Spec.Scope }

// Transport is how a client talks to an MCP server. Wire values per
// docs/spec/pack-manifest.md.
type Transport string

const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
	TransportSSE   Transport = "sse"
)

// Valid reports whether t is a known transport.
func (t Transport) Valid() bool {
	return t == TransportStdio || t == TransportHTTP || t == TransportSSE
}

// MCPServerSpec is the data of a scanned MCP server. Env and Headers hold
// raw scanned values — secrets included — and MUST pass through the secrets
// redactor before any of this reaches a pack (docs/security.md layer 2).
type MCPServerSpec struct {
	Name      string
	Scope     Scope
	Transport Transport         // may hold an unknown raw value, surfaced as a Warning by scanners
	Command   string            // stdio
	Args      []string          // stdio
	Env       map[string]string // stdio
	URL       string            // http/sse
	Headers   map[string]string // http/sse
}

// MCPServer is a neutral MCP server component.
type MCPServer struct {
	Spec MCPServerSpec
}

func (m MCPServer) Kind() Kind   { return KindMCPServer }
func (m MCPServer) Name() string { return m.Spec.Name }
func (m MCPServer) Scope() Scope { return m.Spec.Scope }

// AgentSpec is the data of a scanned agent definition (a markdown file,
// usually with YAML frontmatter naming and describing the agent).
type AgentSpec struct {
	Name        string
	Scope       Scope
	Path        string // the .md file
	Description string // from frontmatter, may be empty
}

// Agent is a neutral agent-definition component.
type Agent struct {
	Spec AgentSpec
}

func (a Agent) Kind() Kind   { return KindAgent }
func (a Agent) Name() string { return a.Spec.Name }
func (a Agent) Scope() Scope { return a.Spec.Scope }

// CommandSpec is the data of a scanned reusable prompt / slash command.
type CommandSpec struct {
	Name        string
	Scope       Scope
	Path        string // the .md file
	Description string // from frontmatter, may be empty
}

// Command is a neutral command component.
type Command struct {
	Spec CommandSpec
}

func (c Command) Kind() Kind   { return KindCommand }
func (c Command) Name() string { return c.Spec.Name }
func (c Command) Scope() Scope { return c.Spec.Scope }

// RuleSpec is the data of a scanned instruction/rule file (CLAUDE.md,
// AGENTS.md, GEMINI.md, .cursor/rules/*.mdc). Name is the file's base name;
// uniqueness holds within (kind, scope).
type RuleSpec struct {
	Name  string
	Scope Scope
	Path  string
}

// Rule is a neutral rule component.
type Rule struct {
	Spec RuleSpec
}

func (r Rule) Kind() Kind   { return KindRule }
func (r Rule) Name() string { return r.Spec.Name }
func (r Rule) Scope() Scope { return r.Spec.Scope }

// SettingSpec is the data of a scanned settings document. Values holds the
// parsed document as generic JSON; like MCP env values it is raw scanned
// data and must pass the secrets redactor before reaching a pack.
type SettingSpec struct {
	Name   string
	Scope  Scope
	Path   string
	Values map[string]any
}

// Setting is a neutral settings component.
type Setting struct {
	Spec SettingSpec
}

func (s Setting) Kind() Kind   { return KindSetting }
func (s Setting) Name() string { return s.Spec.Name }
func (s Setting) Scope() Scope { return s.Spec.Scope }
