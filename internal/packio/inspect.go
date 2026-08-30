package packio

import "github.com/PhongCT1105/agentpack/internal/model"

// This file derives restore-facing views from a manifest: what credentials
// an installer must collect, what lives outside the pack, and whether a
// target project directory is needed. Pure functions over the manifest —
// they read nothing from disk.

// CredentialRequirement ties one credential to the MCP server that needs
// it: one entry per value a restore must collect before applying.
type CredentialRequirement struct {
	Server     string // MCP server component name
	Credential Credential
}

// CredentialRequirements flattens every credential in the manifest, in
// manifest order.
func (m *Manifest) CredentialRequirements() []CredentialRequirement {
	var reqs []CredentialRequirement
	for _, srv := range m.Components.MCPServers {
		for _, cred := range srv.Credentials {
			reqs = append(reqs, CredentialRequirement{Server: srv.Name, Credential: cred})
		}
	}
	return reqs
}

// ExternalService is one place outside the pack that a restored
// environment will connect to or install from: a remote MCP endpoint, a
// plugin marketplace, an npm registry package. Bundled sources and stdio
// commands are local and not listed.
type ExternalService struct {
	ComponentRef string // "skills/superpowers", "mcp_servers/supabase", …
	Kind         string // "plugin marketplace", "npm package", "remote MCP server (http)"
	Ref          string // marketplace ref, package name, or URL
}

// ExternalServices lists everything external the pack depends on, in
// manifest section order (skills, mcp_servers, agents, rules, commands) —
// the "no black boxes" part of restore: a user sees every outside contact
// point before anything is installed.
func (m *Manifest) ExternalServices() []ExternalService {
	var svcs []ExternalService
	addSource := func(section, name string, src Source) {
		ref := section + "/" + name
		switch {
		case src.Plugin != "":
			svcs = append(svcs, ExternalService{ComponentRef: ref, Kind: "plugin marketplace", Ref: src.Plugin})
		case src.NPM != "":
			pkg := src.NPM
			if src.Ref != "" {
				pkg += " (" + src.Ref + ")"
			}
			svcs = append(svcs, ExternalService{ComponentRef: ref, Kind: "npm package", Ref: pkg})
		}
	}

	for _, s := range m.Components.Skills {
		addSource("skills", s.Name, s.Source)
	}
	for _, srv := range m.Components.MCPServers {
		if srv.URL != "" && (srv.Transport == model.TransportHTTP || srv.Transport == model.TransportSSE) {
			svcs = append(svcs, ExternalService{
				ComponentRef: "mcp_servers/" + srv.Name,
				Kind:         "remote MCP server (" + string(srv.Transport) + ")",
				Ref:          srv.URL,
			})
		}
	}
	for _, a := range m.Components.Agents {
		addSource("agents", a.Name, a.Source)
	}
	for _, r := range m.Components.Rules {
		addSource("rules", r.Name, r.Source)
	}
	for _, c := range m.Components.Commands {
		addSource("commands", c.Name, c.Source)
	}
	return svcs
}

// ProjectScoped reports whether any component applies at project scope —
// if so, restore needs a target project directory.
func (m *Manifest) ProjectScoped() bool {
	for _, meta := range m.componentMetas() {
		if meta.EffectiveScope() == model.ScopeProject {
			return true
		}
	}
	return false
}

// componentMetas collects the common metadata of every component in the
// manifest, in manifest section order.
func (m *Manifest) componentMetas() []ComponentMeta {
	var metas []ComponentMeta
	for _, c := range m.Components.Skills {
		metas = append(metas, c.ComponentMeta)
	}
	for _, c := range m.Components.MCPServers {
		metas = append(metas, c.ComponentMeta)
	}
	for _, c := range m.Components.Agents {
		metas = append(metas, c.ComponentMeta)
	}
	for _, c := range m.Components.Rules {
		metas = append(metas, c.ComponentMeta)
	}
	for _, c := range m.Components.Commands {
		metas = append(metas, c.ComponentMeta)
	}
	for _, c := range m.Components.Settings {
		metas = append(metas, c.ComponentMeta)
	}
	return metas
}
