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
