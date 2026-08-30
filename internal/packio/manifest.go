// Package packio reads and writes packs: the manifest schema
// (docs/spec/pack-manifest.md), YAML encode/decode, and — in later phases —
// whole-pack read/write and validation. The manifest types are the wire
// format between save (inventory → pack) and restore (pack → plans).
//
// Security invariant, load-bearing by construction: no type in this package
// has a field that can hold a secret value. MCP env vars and headers carry
// non-secret values only; anything secret is a Credential, which names the
// injection point but never stores a value (docs/security.md layer 1).
package packio

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"

	"github.com/PhongCT1105/agentpack/internal/model"
)

const (
	// APIVersion is the manifest version this build reads and writes.
	// DecodeManifest refuses anything else.
	APIVersion = "agentpack/v0"

	// KindPack is the only document kind defined by the spec.
	KindPack = "Pack"

	// ManifestFilename is the manifest's fixed name inside a pack directory.
	ManifestFilename = "agentpack.yaml"

	// AllowlistFilename holds reviewed secret-scan findings a human has
	// confirmed are not secrets (docs/security.md layer 3 review flow).
	// ValidatePack loads it automatically when present; save writes it when
	// --allow-finding waives a finding. Optional: most packs have none.
	AllowlistFilename = ".agentpack-allow"
)

// Manifest is the top-level agentpack.yaml document.
type Manifest struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   Metadata       `yaml:"metadata"`
	Targets    []model.ToolID `yaml:"targets,omitempty"`
	Components Components     `yaml:"components,omitempty"`
}

// Metadata identifies and describes a pack. Name is required (lowercase,
// [a-z0-9-]); the rest is optional but recommended for published packs.
type Metadata struct {
	Name        string   `yaml:"name"`
	Title       string   `yaml:"title,omitempty"`
	Description string   `yaml:"description,omitempty"`
	Author      string   `yaml:"author,omitempty"`
	License     string   `yaml:"license,omitempty"`
	Tags        []string `yaml:"tags,omitempty"`
}

// Components holds every component list, keyed by the spec's plural section
// names (the model.Kind values are the singular forms used in scan output).
type Components struct {
	Skills     []Skill     `yaml:"skills,omitempty"`
	MCPServers []MCPServer `yaml:"mcp_servers,omitempty"`
	Agents     []Agent     `yaml:"agents,omitempty"`
	Rules      []Rule      `yaml:"rules,omitempty"`
	Commands   []Command   `yaml:"commands,omitempty"`
	Settings   []Setting   `yaml:"settings,omitempty"`
}

// ComponentMeta is the common fields every component carries. It is inlined
// into each component type so the YAML stays flat.
type ComponentMeta struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description,omitempty"`
	Scope       model.Scope    `yaml:"scope,omitempty"`   // empty means global
	Targets     []model.ToolID `yaml:"targets,omitempty"` // overrides pack-level targets
	Optional    bool           `yaml:"optional,omitempty"`
}

// EffectiveScope resolves the spec default: an unset scope means global.
func (m ComponentMeta) EffectiveScope() model.Scope {
	if m.Scope == "" {
		return model.ScopeGlobal
	}
	return m.Scope
}

// Source says where a component's content comes from. Exactly one source
// type must be set (enforced by validation, not by the type): Plugin for a
// marketplace ref, NPM (+ optional Ref) for `npx skills add`-style installs,
// Bundled for a path inside the pack directory.
type Source struct {
	Plugin  string `yaml:"plugin,omitempty"`
	NPM     string `yaml:"npm,omitempty"`
	Ref     string `yaml:"ref,omitempty"`
	Bundled string `yaml:"bundled,omitempty"`
}

// Skill references or bundles a skill.
type Skill struct {
	ComponentMeta `yaml:",inline"`
	Source        Source `yaml:"source"`
}

// MCPServer describes an MCP server without its secrets. Env and Headers
// hold non-secret values only; secret env vars and headers are declared as
// Credentials, whose values are never stored.
type MCPServer struct {
	ComponentMeta `yaml:",inline"`
	Transport     model.Transport   `yaml:"transport"`
	Command       string            `yaml:"command,omitempty"` // stdio
	Args          []string          `yaml:"args,omitempty"`    // stdio
	Env           map[string]string `yaml:"env,omitempty"`     // non-secret env only
	URL           string            `yaml:"url,omitempty"`     // http/sse
	Headers       map[string]string `yaml:"headers,omitempty"` // non-secret headers only
	Credentials   []Credential      `yaml:"credentials,omitempty"`
}

// Credential names a secret an installer must collect — the injection point
// (an env var or a header), what it is, and where to obtain it. There is
// deliberately no value field.
type Credential struct {
	Env         string `yaml:"env,omitempty"`    // inject as this env var
	Header      string `yaml:"header,omitempty"` // or inject as this header…
	Format      string `yaml:"format,omitempty"` // …rendered as e.g. "Bearer {value}"
	Description string `yaml:"description,omitempty"`
	ObtainURL   string `yaml:"obtain_url,omitempty"`
}

// Agent references or bundles an agent definition.
type Agent struct {
	ComponentMeta `yaml:",inline"`
	Source        Source `yaml:"source"`
}

// Rule is instruction content plus per-tool placement: Render maps a target
// tool to the path/filename that tool consumes (CLAUDE.md, AGENTS.md, …).
type Rule struct {
	ComponentMeta `yaml:",inline"`
	Source        Source                  `yaml:"source"`
	Render        map[model.ToolID]string `yaml:"render,omitempty"`
}

// Command references or bundles a reusable prompt / slash command.
type Command struct {
	ComponentMeta `yaml:",inline"`
	Source        Source `yaml:"source"`
}

// Setting is a per-tool document of non-secret, portable settings.
type Setting struct {
	ComponentMeta `yaml:",inline"`
	Values        map[string]any `yaml:"values,omitempty"`
}

// DecodeManifest parses one agentpack.yaml document. It is strict: unknown
// fields, an apiVersion or kind this build does not understand, and multiple
// YAML documents are all errors — a typo'd field must fail loudly rather
// than silently drop config.
func DecodeManifest(data []byte) (*Manifest, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	var m Manifest
	if err := dec.Decode(&m); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errors.New("manifest is empty")
		}
		return nil, fmt.Errorf("parsing manifest yaml: %w", err)
	}

	if m.APIVersion != APIVersion {
		if m.APIVersion == "" {
			return nil, fmt.Errorf("manifest is missing apiVersion (this build supports %q)", APIVersion)
		}
		return nil, fmt.Errorf("unsupported apiVersion %q (this build supports %q)", m.APIVersion, APIVersion)
	}
	if m.Kind != KindPack {
		return nil, fmt.Errorf("unsupported kind %q (want %q)", m.Kind, KindPack)
	}

	// A pack manifest is exactly one document.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("manifest must contain exactly one YAML document")
	}

	return &m, nil
}

// EncodeManifest renders a manifest as YAML. Output is deterministic
// (yaml.v3 sorts map keys) so re-saving an unchanged pack yields an
// unchanged file.
func EncodeManifest(m *Manifest) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("encoding manifest yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding manifest yaml: %w", err)
	}
	return buf.Bytes(), nil
}
