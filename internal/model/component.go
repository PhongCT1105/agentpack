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

// Credential names a secret that must be supplied for a component to work —
// the injection point (an env var or a header), how the value is rendered,
// and where an installer obtains one. It mirrors the manifest's
// `credentials:` entry (docs/spec/pack-manifest.md) and, like it, has
// deliberately no value field: a credential travels as a *requirement*, and
// the resolved secret reaches an adapter separately, in engine.PlanOpts
// (docs/security.md layer 1).
//
// Scanners do not produce these — a scan sees values, and the redactor turns
// the secret ones into credentials on save. Restore is where they matter: an
// adapter must know an injection point exists in order to reference it even
// when no value was resolved.
type Credential struct {
	Env         string // inject as this env var…
	Header      string // …or as this header (exactly one of the two)
	Format      string // header rendering template, e.g. "Bearer {value}"
	Description string
	ObtainURL   string
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

	// Credentials are the secrets this server needs that Env and Headers
	// therefore do not carry. Empty on the scan side; populated on restore
	// from the pack's `credentials:` list.
	Credentials []Credential
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

	// Render maps a tool to the relative path that tool reads the content
	// from (claude-code → "CLAUDE.md", codex → "AGENTS.md"). It mirrors the
	// manifest's `render:` map: one logical rule, one file name per tool.
	// Empty on the scan side, where the path a rule was found at already
	// says how the tool consumes it.
	Render map[ToolID]string
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
