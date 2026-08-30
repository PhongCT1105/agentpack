package codex

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// The adapter can both read and write Codex configuration; this fails the
// build if Plan ever drifts from the interface the executor consumes.
var _ engine.Planner = (*Adapter)(nil)

// Codex's own spelling of the config.toml keys this adapter writes, collected
// so a Codex rename is a one-line fix rather than a grep. Sources:
// docs/research/tool-config-matrix.md § Codex CLI and Codex's MCP
// documentation (developers.openai.com/codex/mcp). The remote-auth keys are
// the ones the matrix flags as *(verify against current Codex)* — see
// planRemoteServer for what is confirmed and what is not.
const (
	codexDir   = ".codex"
	configFile = "config.toml"
	promptsDir = "prompts"
	agentsFile = "AGENTS.md"

	keyMCPServers  = "mcp_servers"
	keyCommand     = "command"
	keyArgs        = "args"
	keyEnv         = "env"
	keyEnvVars     = "env_vars"
	keyURL         = "url"
	keyHTTPHeaders = "http_headers"
	keyBearerEnv   = "bearer_token_env_var"
)

// Manifest-only data — a rule's render: map and an MCP server's
// credentials: list — reaches this adapter through model.RuleSpec.Render and
// model.MCPServerSpec.Credentials. Both are empty for a component read off a
// machine (a scan sees files and values, not requirements) and populated by
// restore's pack→component wiring.
//
// They live on the neutral model rather than in an adapter-local interface
// over packio types so that adapters never depend on the pack file format:
// the model is the lingua franca between the two directions, and an adapter
// that imported packio would couple tool-specific code to the wire format.

// portableSettings are the config.toml keys agentpack is willing to write
// from a settings component: they describe *how* a user works (which model,
// how much approval, which profile), not *where* they work.
//
// It is an allowlist, and deliberately so. config.toml is a mixed file the
// user also hand-edits, and the failure modes of guessing wrong are asymmetric:
// refusing an unknown key costs a warning the user can act on, while writing
// one blindly can hand a pack author a way to reconfigure someone's sandbox,
// their trusted-directory list, or which of their environment variables reach
// a subprocess. Extending the list is a one-line change once a key has been
// checked against Codex's config reference.
var portableSettings = map[string]bool{
	"model":                    true,
	"model_provider":           true,
	"model_providers":          true,
	"model_context_window":     true,
	"model_max_output_tokens":  true,
	"model_reasoning_effort":   true,
	"model_reasoning_summary":  true,
	"model_verbosity":          true,
	"approval_policy":          true,
	"sandbox_mode":             true,
	"sandbox_workspace_write":  true,
	"profile":                  true,
	"profiles":                 true,
	"history":                  true,
	"file_opener":              true,
	"hide_agent_reasoning":     true,
	"show_raw_agent_reasoning": true,
	"disable_response_storage": true,
	"project_doc_max_bytes":    true,
	"tools":                    true,
}

// machineLocalSettings explains why a few well-known keys are refused, so the
// warning says something more useful than "not portable".
var machineLocalSettings = map[string]string{
	keyMCPServers:              "MCP servers are planned from mcp_server components, one table at a time, so they merge without disturbing each other",
	"shell_environment_policy": "it decides which of this machine's environment variables — secrets included — reach the processes Codex spawns",
	"notify":                   "it names a program on this machine to execute",
	"projects":                 "it records which of this machine's directories are trusted",
}

// Plan turns neutral components into the file operations that would configure
// Codex CLI on this machine. It writes nothing: reading bundled pack content
// and the existing config.toml is all the filesystem access it does, and every
// intended change comes back as an engine.Op for the executor to render,
// confirm, back up and apply (docs/architecture.md → "Plan/apply split").
//
// Placement follows docs/research/tool-config-matrix.md § Codex CLI:
//
//	mcp servers  merge into ~/.codex/config.toml at mcp_servers.<name>
//	rules        ~/.codex/AGENTS.md, or <project>/AGENTS.md at project scope
//	commands     ~/.codex/prompts/<name>.md
//	settings     merge into ~/.codex/config.toml, one top-level key per op
//	skills       no Codex-native equivalent — warned, never invented
//	agents       no Codex-native equivalent — warned, never invented
//
// config.toml is a *mixed* file: the user's model, approval policy and
// profiles live beside the [mcp_servers.*] tables. Every write into it is
// therefore an OpMergeValue at a key path and never a whole-file replace,
// which would take the rest of their configuration with it.
//
// Everything Codex cannot place comes back as a model.Warning naming the
// component. A pack that targets four tools should degrade honestly on the one
// that has nowhere to put a skill, rather than drop it in silence.
func (a *Adapter) Plan(components []model.Component, opts engine.PlanOpts) (engine.Plan, []model.Warning, error) {
	// PlanOpts.Home is injectable so tests never plan against a real home; the
	// adapter's own home is the fallback for callers that construct opts
	// without one.
	home := opts.Home
	if home == "" {
		home = a.home
	}
	if home == "" {
		return engine.Plan{Tool: model.ToolCodex}, nil,
			fmt.Errorf("planning for codex: no home directory to plan against (set PlanOpts.Home)")
	}
	// A plan is rendered, confirmed and only then applied, so its paths must
	// not depend on anyone's working directory at apply time.
	home, err := filepath.Abs(home)
	if err != nil {
		return engine.Plan{Tool: model.ToolCodex}, nil, fmt.Errorf("planning for codex: resolving home %q: %w", opts.Home, err)
	}
	project := opts.ProjectDir
	if project != "" {
		if project, err = filepath.Abs(project); err != nil {
			return engine.Plan{Tool: model.ToolCodex}, nil,
				fmt.Errorf("planning for codex: resolving project directory %q: %w", opts.ProjectDir, err)
		}
	}

	p := &planner{
		opts:       opts,
		home:       home,
		project:    project,
		plan:       engine.Plan{Tool: model.ToolCodex},
		configPath: filepath.Join(home, codexDir, configFile),
	}

	for _, comp := range components {
		if comp == nil {
			continue
		}
		p.add(comp)
	}
	return p.plan, p.warnings, nil
}

// planner accumulates one Plan. It exists so the per-kind helpers can share
// the resolved roots and the "warn once" state without threading five
// arguments through each of them.
type planner struct {
	opts    engine.PlanOpts
	home    string
	project string

	plan     engine.Plan
	warnings []model.Warning

	configPath string

	promptsDirPlanned bool
	commentsChecked   bool
}

func (p *planner) add(comp model.Component) {
	name := comp.Name()
	switch comp.Kind() {
	case model.KindMCPServer:
		spec, ok := mcpServerSpec(comp)
		if !ok {
			p.warnUnreadable(comp)
			return
		}
		p.planMCPServer(spec, credentialsOf(comp))
	case model.KindRule:
		spec, ok := ruleSpec(comp)
		if !ok {
			p.warnUnreadable(comp)
			return
		}
		p.planRule(spec, comp)
	case model.KindCommand:
		spec, ok := commandSpec(comp)
		if !ok {
			p.warnUnreadable(comp)
			return
		}
		p.planCommand(spec)
	case model.KindSetting:
		spec, ok := settingSpec(comp)
		if !ok {
			p.warnUnreadable(comp)
			return
		}
		p.planSetting(spec)
	case model.KindSkill, model.KindAgent:
		// Codex has no native skills or subagents (the config matrix says so
		// outright). Inventing a location — dropping a SKILL.md under
		// ~/.codex/skills, say — would write files Codex never reads and that
		// the user would later find and wonder about. Saying so is the honest
		// degradation a multi-tool pack needs.
		p.warnf("", "%s %q: Codex has no native location for %ss (docs/research/tool-config-matrix.md); skipped — restore it into a tool that supports them",
			comp.Kind(), name, comp.Kind())
	default:
		p.warnf("", "component %q has kind %q, which this adapter cannot place; skipped", name, comp.Kind())
	}
}

// --- MCP servers ------------------------------------------------------

// planMCPServer merges one [mcp_servers.<name>] table into config.toml.
//
// The operation is a merge at the key path mcp_servers.<name>, never a write
// of the file: config.toml holds the user's settings too, and the key path is
// exactly the addressable unit being replaced — the neighbouring servers and
// every setting outside the path are the executor's guarantee to preserve.
// MergeSet (not MergeDeep) is right *at* that path: a server that changed from
// stdio to a url would otherwise keep a stale command key and start neither
// way.
func (p *planner) planMCPServer(spec model.MCPServerSpec, creds []model.Credential) {
	name := spec.Name
	if name == "" {
		p.warnf("", "an MCP server component has no name; skipped (mcp_servers.<name> is the key it would merge at)")
		return
	}
	if spec.Scope == model.ScopeProject {
		// The config matrix lists no verified project-level MCP location for
		// Codex. Writing the server into ~/.codex/config.toml instead would
		// quietly promote a repo-scoped server to every session the user ever
		// runs, which is not what the pack asked for.
		p.warnf("", "mcp server %q is project-scoped, and Codex has no verified project-level MCP location (docs/research/tool-config-matrix.md); skipped rather than silently configured for every project", name)
		return
	}

	entry := map[string]any{}
	switch transportOf(spec) {
	case model.TransportStdio:
		if !p.planStdioServer(name, spec, creds, entry) {
			return
		}
	case model.TransportHTTP, model.TransportSSE:
		if !p.planRemoteServer(name, spec, creds, entry) {
			return
		}
	default:
		p.warnf("", "mcp server %q declares neither a command nor a url (transport %q); skipped — there is nothing Codex could start or connect to", name, spec.Transport)
		return
	}

	p.mergeIntoConfig([]string{keyMCPServers, name}, entry, engine.MergeSet, "mcp server "+name)
}

// planStdioServer fills a local-process entry: command, args, env.
//
// Credential injection prefers indirection. Codex's env table takes literal
// values, but its sibling env_vars takes variable *names* that Codex forwards
// from its own environment — so a declared credential is planned as
// env_vars = ["GITHUB_TOKEN"] and the secret never reaches config.toml at all.
// That is docs/security.md threat 4's "prefer env-var indirection where the
// tool supports expansion", and it is what Codex's own MCP documentation asks
// for ("never hardcode or interpolate your environment variables"). It has a
// cost the warning states out loud: the variable has to be exported where
// Codex runs, which a value the user typed at a prompt is not.
func (p *planner) planStdioServer(name string, spec model.MCPServerSpec, creds []model.Credential, entry map[string]any) bool {
	if spec.Command == "" {
		p.warnf("", "mcp server %q uses the stdio transport but declares no command; skipped", name)
		return false
	}
	entry[keyCommand] = spec.Command
	if len(spec.Args) > 0 {
		entry[keyArgs] = append([]string(nil), spec.Args...)
	}
	if len(spec.Env) > 0 {
		env := make(map[string]any, len(spec.Env))
		for _, k := range sortedStringKeys(spec.Env) {
			env[k] = spec.Env[k]
		}
		entry[keyEnv] = env
	}
	if len(spec.Headers) > 0 {
		p.warnf("", "mcp server %q is stdio but carries headers (%s); Codex has no header to send on a local process, so they are not planned",
			name, strings.Join(sortedStringKeys(spec.Headers), ", "))
	}

	var envVars []string
	seen := map[string]bool{}
	for _, cred := range creds {
		switch {
		case cred.Env != "":
			if seen[cred.Env] {
				continue
			}
			seen[cred.Env] = true
			envVars = append(envVars, cred.Env)
			p.warnCredentialIndirection(name, cred.Env, fmt.Sprintf("%s = [%q]", keyEnvVars, cred.Env))
		case cred.Header != "":
			p.warnf("", "mcp server %q: credential %q is a header, but the server is stdio and Codex sends no headers to a local process; not planned", name, cred.Header)
		default:
			p.warnf("", "mcp server %q declares a credential with neither an env var nor a header; it names no injection point, so it cannot be planned", name)
		}
	}
	if len(envVars) > 0 {
		entry[keyEnvVars] = envVars
	}
	return true
}

// planRemoteServer fills a remote entry: url, plus whatever auth Codex models.
//
// Two injection points, and only one of them can use indirection:
//
//   - A credential declared as an env var is planned as
//     bearer_token_env_var = "NAME" — Codex reads the variable itself and
//     sends it as a bearer token, so nothing secret is written. Codex models
//     exactly one such variable per server, so a second env credential is
//     warned about rather than silently dropped.
//   - A credential declared as a *header* has no variable name to point at.
//     Codex's env_http_headers maps a header to an environment variable name,
//     but the manifest declares a header name, and inventing a variable would
//     produce a config referencing something nobody exports. So the resolved
//     value is written into http_headers, rendered through the credential's
//     format ("Bearer {value}"): local tool config is where docs/security.md
//     threat 4 allows a secret to land, and the file is planned 0600.
//
// Confirmed against Codex's MCP documentation: url, bearer_token_env_var,
// http_headers, env_http_headers, env_vars. Not confirmed against a running
// Codex binary, and older builds may predate env_vars — the credential warning
// tells the user to export the variable either way, which is also the remedy
// if their Codex ignores the key.
func (p *planner) planRemoteServer(name string, spec model.MCPServerSpec, creds []model.Credential, entry map[string]any) bool {
	if spec.URL == "" {
		p.warnf("", "mcp server %q uses a remote transport but declares no url; skipped", name)
		return false
	}
	entry[keyURL] = spec.URL
	if spec.Transport == model.TransportSSE {
		p.warnf("", "mcp server %q declares the sse transport; Codex models remote servers as a single url entry (streamable HTTP), so it is planned as one — verify it connects", name)
	}
	if len(spec.Env) > 0 {
		p.warnf("", "mcp server %q is remote but carries env vars (%s); Codex starts no process for it, so they are not planned",
			name, strings.Join(sortedStringKeys(spec.Env), ", "))
	}

	headers := make(map[string]any, len(spec.Headers)+len(creds))
	for _, k := range sortedStringKeys(spec.Headers) {
		headers[k] = spec.Headers[k]
	}

	bearer := ""
	for _, cred := range creds {
		switch {
		case cred.Env != "":
			if bearer != "" {
				p.warnf("", "mcp server %q: Codex models one environment-variable credential per remote server (%s), which is already %q; %q is not planned",
					name, keyBearerEnv, bearer, cred.Env)
				continue
			}
			bearer = cred.Env
			entry[keyBearerEnv] = cred.Env
			p.warnCredentialIndirection(name, cred.Env, fmt.Sprintf("%s = %q", keyBearerEnv, cred.Env))
		case cred.Header != "":
			headers[cred.Header] = p.headerCredentialValue(name, cred)
		default:
			p.warnf("", "mcp server %q declares a credential with neither an env var nor a header; it names no injection point, so it cannot be planned", name)
		}
	}
	if len(headers) > 0 {
		entry[keyHTTPHeaders] = headers
	}
	return true
}

// headerCredentialValue renders the value Codex will send for a header
// credential: the resolved secret through the credential's format, or — when
// the resolver produced nothing — a placeholder naming the injection point.
//
// Never an empty string, and never a dropped server: a config.toml with
// "Bearer ${Authorization}" in it fails loudly and legibly at the server,
// whereas an absent header or an empty one fails somewhere the user cannot see
// and cannot fix (docs/engine PlanOpts.Credentials states the same contract).
func (p *planner) headerCredentialValue(server string, cred model.Credential) string {
	value, ok := p.opts.Credentials[cred.Header]
	if !ok || strings.TrimSpace(value) == "" {
		p.warnf("", "mcp server %q: credential %q has no resolved value; the server is still planned and %s.%s references the injection point, but it will not authenticate until you fill it in",
			server, cred.Header, keyHTTPHeaders, cred.Header)
		return renderCredentialFormat(cred.Format, "${"+cred.Header+"}")
	}
	p.warnf("", "mcp server %q: the resolved value for header %q is written into %s (Codex has no environment-variable indirection for a header credential); the file is created 0600 and backed up before any change",
		server, cred.Header, shortPath(p.configPath, p.home))
	return renderCredentialFormat(cred.Format, value)
}

// warnCredentialIndirection reports what indirection means for the user: the
// secret is not in the file, so the variable has to be in the environment.
// Both branches warn, because both need an action — the unresolved one to
// supply a value at all, the resolved one to make sure the value the resolver
// found (from a keychain or a prompt, which Codex cannot see) is exported.
func (p *planner) warnCredentialIndirection(server, envVar, planned string) {
	if value, ok := p.opts.Credentials[envVar]; ok && strings.TrimSpace(value) != "" {
		p.warnf("", "mcp server %q: %s references %s by name — agentpack does not write the resolved secret into config.toml — so make sure %s is exported in the environment Codex runs in",
			server, planned, envVar, envVar)
		return
	}
	p.warnf("", "mcp server %q: credential %q has no resolved value; the server is still planned with %s, and Codex will start without the credential until %s is set in its environment",
		server, envVar, planned, envVar)
}

// --- rules, prompts, settings -----------------------------------------

// planRule writes the instruction file Codex reads: ~/.codex/AGENTS.md
// globally, <project>/AGENTS.md at project scope.
func (p *planner) planRule(spec model.RuleSpec, comp model.Component) {
	name := spec.Name
	if name == "" {
		name = "(unnamed)"
	}
	file, ok := p.ruleFileName(comp, name)
	if !ok {
		return
	}
	root, ok := p.scopeRoot(spec.Scope, "rule", name)
	if !ok {
		return
	}
	content, ok := p.content(spec.Path, "rule "+name)
	if !ok {
		return
	}
	p.plan.Add(engine.Op{
		Kind:        p.fileKind(),
		Path:        filepath.Join(root, file),
		Content:     content,
		Description: "rule " + name,
	})
}

// ruleFileName picks the filename Codex consumes. The manifest's render: map
// is authoritative when the component carries one (one logical rule renders as
// CLAUDE.md for one tool and AGENTS.md for another); AGENTS.md is the default
// for a component that carries no map, or none for codex, because that is the
// only instruction file Codex reads.
func (p *planner) ruleFileName(comp model.Component, name string) (string, bool) {
	rendered := agentsFile
	if rule, ok := comp.(model.Rule); ok {
		if v := strings.TrimSpace(rule.Spec.Render[model.ToolCodex]); v != "" {
			rendered = v
		}
	}
	clean := filepath.Clean(filepath.FromSlash(rendered))
	// A render entry names a path *inside* the target root. One that is
	// absolute, or that climbs out with .., would let a pack write anywhere on
	// the machine, so it is refused rather than clamped (docs/security.md
	// threat 2: nothing installs silently, and nothing installs off-target).
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" ||
		clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		p.warnf("", "rule %q renders to %q for codex, which is not a path inside the target directory; skipped", name, rendered)
		return "", false
	}
	return clean, true
}

// planCommand writes one reusable prompt to ~/.codex/prompts/<name>.md, the
// name being the slash command the user types.
func (p *planner) planCommand(spec model.CommandSpec) {
	name := spec.Name
	if name == "" {
		p.warnf("", "a command component has no name; skipped (the name is the prompt's filename and the slash command)")
		return
	}
	if spec.Scope == model.ScopeProject {
		p.warnf("", "command %q is project-scoped, and Codex reads prompts only from ~/.codex/prompts (docs/research/tool-config-matrix.md); skipped rather than installed for every project", name)
		return
	}
	// The name becomes a filename. A separator or a .. in it would place the
	// file outside the prompts directory, so it is refused, not sanitized:
	// silently renaming a component changes what the user has to type.
	if strings.ContainsAny(name, `/\`) || name == ".." || strings.HasPrefix(name, ".") {
		p.warnf("", "command %q cannot be a prompt filename (it would not land in ~/.codex/prompts); skipped", name)
		return
	}
	content, ok := p.content(spec.Path, "command "+name)
	if !ok {
		return
	}

	dir := filepath.Join(p.home, codexDir, promptsDir)
	if !p.promptsDirPlanned {
		// Emitted explicitly so the preview shows the directory a first
		// restore creates, rather than having it appear as a side effect of
		// the first file write.
		p.promptsDirPlanned = true
		p.plan.Add(engine.Op{Kind: engine.OpCreateDir, Path: dir, Description: "codex prompts directory"})
	}
	p.plan.Add(engine.Op{
		Kind:        p.fileKind(),
		Path:        filepath.Join(dir, name+".md"),
		Content:     content,
		Description: "prompt " + name,
	})
}

// planSetting merges the portable subset of a settings document into
// config.toml, one top-level key per operation.
//
// One operation per key, rather than one for the whole document, because the
// key path is what makes a merge legible in the preview ("merge model") and
// reviewable in isolation. Table values merge deeply so the user's own entries
// under profiles or history survive; scalars and arrays are set, because there
// is no union of two arrays that stays idempotent on re-apply.
func (p *planner) planSetting(spec model.SettingSpec) {
	name := spec.Name
	if name == "" {
		name = "(unnamed)"
	}
	if spec.Scope == model.ScopeProject {
		p.warnf("", "setting %q is project-scoped, and Codex keeps settings only in ~/.codex/config.toml (docs/research/tool-config-matrix.md); skipped rather than applied to every project", name)
		return
	}
	if len(spec.Values) == 0 {
		p.warnf("", "setting %q carries no values; nothing to merge", name)
		return
	}
	for _, key := range sortedAnyKeys(spec.Values) {
		value := spec.Values[key]
		if value == nil {
			// The executor has no delete operation on purpose: a restore adds
			// and updates config, it never removes what it did not put there.
			p.warnf("", "setting %q: key %q has no value; skipped (restore never deletes config it did not write)", name, key)
			continue
		}
		if reason, ok := machineLocalSettings[key]; ok {
			p.warnf("", "setting %q: %q is not portable — %s; skipped", name, key, reason)
			continue
		}
		if !portableSettings[key] {
			p.warnf("", "setting %q: %q is not in the portable subset this adapter knows how to place in config.toml; skipped rather than written blind", name, key)
			continue
		}
		strategy := engine.MergeSet
		if _, isTable := value.(map[string]any); isTable {
			strategy = engine.MergeDeep
		}
		p.mergeIntoConfig([]string{key}, value, strategy, fmt.Sprintf("setting %s (%s)", key, name))
	}
}

// --- shared plumbing --------------------------------------------------

// mergeIntoConfig appends one surgical merge into ~/.codex/config.toml.
func (p *planner) mergeIntoConfig(keyPath []string, value any, strategy engine.MergeStrategy, description string) {
	p.warnAboutComments()
	p.plan.Add(engine.Op{
		Kind:     engine.OpMergeValue,
		Path:     p.configPath,
		Format:   engine.FormatTOML,
		KeyPath:  keyPath,
		Value:    value,
		Strategy: strategy,
		// Only used when the file does not exist yet: a plan can embed a
		// resolved credential in http_headers, so a config.toml agentpack
		// creates should be no more readable than the backups of it
		// (docs/security.md threat 4). An existing file keeps its own mode.
		Perm:        0o600,
		Description: description,
	})
}

// warnAboutComments tells the user, once, that merging will cost them their
// annotations. The executor's merge preserves data — unrelated settings and
// servers survive, which is the guarantee that matters — but it re-encodes the
// document, so comments and key order do not (docs/security.md threat 3). A
// hand-annotated config.toml is worth more than the round trip, which is why
// the user should hear about it before they confirm rather than after.
// Reading the file here is safe: planning never writes.
func (p *planner) warnAboutComments() {
	if p.commentsChecked {
		return
	}
	p.commentsChecked = true
	raw, err := os.ReadFile(p.configPath)
	if err != nil || !hasTOMLComments(raw) {
		return
	}
	p.warnf(p.configPath, "merging re-encodes this file: your settings and other MCP servers are preserved, but its comments and key order are not — the copy taken in ~/.agentpack/backups before the write is the way back (a comment-preserving TOML editor is tracked as backlog P3.13)")
}

// scopeRoot resolves the directory a scoped component lands in.
func (p *planner) scopeRoot(scope model.Scope, kind, name string) (string, bool) {
	if scope == model.ScopeProject {
		if p.project == "" {
			p.warnf("", "%s %q is project-scoped but this restore was given no project directory; skipped (agentpack does not guess one)", kind, name)
			return "", false
		}
		return p.project, true
	}
	return filepath.Join(p.home, codexDir), true
}

// fileKind is OpCreateFile normally: the executor refuses to overwrite a file
// that exists with different content, which is what turns "restore would
// clobber my AGENTS.md" into a refusal the user can answer instead of a loss
// they discover later. Replace mode is that answer, and it is an explicit
// choice made above the adapter (PlanOpts.Replace).
func (p *planner) fileKind() engine.OpKind {
	if p.opts.Replace {
		return engine.OpReplaceFile
	}
	return engine.OpCreateFile
}

// content reads a component's file. An absolute path is used as-is (a
// component scanned off this machine); a relative one resolves against
// PlanOpts.PackDir, which is where a pack's bundled content lives.
//
// A file that cannot be read becomes a warning and no operation, rather than
// an error that abandons the whole plan: one unreadable bundled rule should
// not cost the user the MCP servers and prompts in the same pack, and the
// warning says exactly what did not make it.
func (p *planner) content(path, ref string) ([]byte, bool) {
	if path == "" {
		p.warnf("", "%s has no source path; skipped (nothing to write)", ref)
		return nil, false
	}
	full := path
	if !filepath.IsAbs(full) {
		if p.opts.PackDir == "" {
			p.warnf("", "%s has the relative source path %q and this restore was given no pack directory to resolve it against; skipped", ref, path)
			return nil, false
		}
		full = filepath.Join(p.opts.PackDir, filepath.FromSlash(path))
	}
	data, err := os.ReadFile(full)
	if err != nil {
		p.warnf(full, "%s could not be read, so it is not planned: %v", ref, err)
		return nil, false
	}
	return data, true
}

func (p *planner) warnUnreadable(comp model.Component) {
	p.warnf("", "component %q reports kind %q but carries no %s spec this adapter can read (%T); skipped",
		comp.Name(), comp.Kind(), comp.Kind(), comp)
}

func (p *planner) warnf(path, format string, args ...any) {
	p.warnings = append(p.warnings, model.Warning{Path: path, Message: fmt.Sprintf(format, args...)})
}

// --- component accessors ----------------------------------------------
//
// Plan takes []model.Component, so a component arrives either as one of the
// neutral structs or as something wrapping one to carry manifest-only data
// (see Rendered and MCPCredentials). Each accessor handles both: the concrete
// neutral type, and any type that exposes the same spec by method. Anything
// else is a caller bug and becomes a warning rather than a panic.

func mcpServerSpec(c model.Component) (model.MCPServerSpec, bool) {
	switch v := c.(type) {
	case model.MCPServer:
		return v.Spec, true
	case interface{ MCPServerSpec() model.MCPServerSpec }:
		return v.MCPServerSpec(), true
	}
	return model.MCPServerSpec{}, false
}

func ruleSpec(c model.Component) (model.RuleSpec, bool) {
	switch v := c.(type) {
	case model.Rule:
		return v.Spec, true
	case interface{ RuleSpec() model.RuleSpec }:
		return v.RuleSpec(), true
	}
	return model.RuleSpec{}, false
}

func commandSpec(c model.Component) (model.CommandSpec, bool) {
	switch v := c.(type) {
	case model.Command:
		return v.Spec, true
	case interface{ CommandSpec() model.CommandSpec }:
		return v.CommandSpec(), true
	}
	return model.CommandSpec{}, false
}

func settingSpec(c model.Component) (model.SettingSpec, bool) {
	switch v := c.(type) {
	case model.Setting:
		return v.Spec, true
	case interface{ SettingSpec() model.SettingSpec }:
		return v.SettingSpec(), true
	}
	return model.SettingSpec{}, false
}

// credentialsOf returns the credential declarations a component carries, or
// none. A server scanned off a machine carries values, not requirements, so
// "none" is the normal answer outside a restore.
func credentialsOf(c model.Component) []model.Credential {
	if srv, ok := c.(model.MCPServer); ok {
		return srv.Spec.Credentials
	}
	return nil
}

// --- small helpers ----------------------------------------------------

// transportOf resolves the transport, falling back to what the server's own
// fields imply. A pack written by an older exporter can carry an empty or
// unknown transport, and a server with a command is plainly stdio — the same
// inference the scanner makes in the other direction.
func transportOf(spec model.MCPServerSpec) model.Transport {
	if spec.Transport.Valid() {
		return spec.Transport
	}
	switch {
	case spec.Command != "":
		return model.TransportStdio
	case spec.URL != "":
		return model.TransportHTTP
	}
	return ""
}

// renderCredentialFormat applies a credential's format template
// ("Bearer {value}"), which is the non-secret shape the manifest keeps beside
// the injection point.
func renderCredentialFormat(format, value string) string {
	if format == "" {
		return value
	}
	return strings.ReplaceAll(format, "{value}", value)
}

// hasTOMLComments reports whether a document carries comment lines.
func hasTOMLComments(raw []byte) bool {
	for _, line := range strings.Split(string(raw), "\n") {
		if commentIndex(line) >= 0 {
			return true
		}
	}
	return false
}

// commentIndex returns the offset of a '#' that opens a comment, or -1.
// Quoted strings are skipped so a '#' inside a value (a URL fragment, a
// colour) is not mistaken for one. Multi-line strings are not tracked: the
// cost of being wrong is one extra "you will lose your comments" warning,
// which is the safe direction to be wrong in.
func commentIndex(line string) int {
	inBasic, inLiteral := false, false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inBasic {
				i++ // an escaped character, quote included, is not a delimiter
			}
		case '"':
			if !inLiteral {
				inBasic = !inBasic
			}
		case '\'':
			if !inBasic {
				inLiteral = !inLiteral
			}
		case '#':
			if !inBasic && !inLiteral {
				return i
			}
		}
	}
	return -1
}

// shortPath abbreviates the home directory for a warning message. Plans carry
// absolute paths; prose about them reads better with a ~.
func shortPath(path, home string) string {
	if home == "" {
		return path
	}
	if prefix := home + string(filepath.Separator); strings.HasPrefix(path, prefix) {
		return "~" + string(filepath.Separator) + path[len(prefix):]
	}
	return path
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedAnyKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
