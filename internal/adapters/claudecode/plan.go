package claudecode

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// defaultRuleFile is the instruction file Claude Code reads when a pack's
// rule does not say (docs/research/tool-config-matrix.md → Claude Code).
const defaultRuleFile = "CLAUDE.md"

// The adapter can write, not only read: it satisfies engine.Planner as well
// as engine.Adapter. Asserted here so a signature drift fails to compile
// rather than at the call site that wires restore together.
var _ engine.Planner = (*Adapter)(nil)

// Plan turns neutral components into the file operations that install them
// for Claude Code. It is Scan's mirror image: the same path knowledge from
// docs/research/tool-config-matrix.md, applied in the other direction.
//
// Plan writes nothing. It reads the pack's bundled content — that is the
// only filesystem access it performs — and returns intent; engine.Executor
// is the single thing that writes, which is what gives every operation its
// backup, dry run and rollback (docs/architecture.md → "Plan/apply split").
//
// Two refusals are deliberate and load-bearing:
//
//   - A project-scoped component with no opts.ProjectDir is skipped with a
//     warning. Guessing a project directory would write a pack's config into
//     whatever happened to be nearby.
//   - ~/.claude.json is never replaced wholesale, in any mode. It is a mixed
//     file — MCP servers blended with app state, OAuth state and per-project
//     history — so an MCP server goes in as a merge at mcpServers.<name> and
//     nothing else in the document is touched (docs/security.md threat 3).
//
// Anything the adapter cannot place becomes a model.Warning; nothing is
// dropped silently. The error return is reserved for a failure that makes
// the whole plan untrustworthy — bundled content that exists but cannot be
// read — because a plan that quietly installs less than the pack promises is
// worse than one that refuses.
func (a *Adapter) Plan(components []model.Component, opts engine.PlanOpts) (engine.Plan, []model.Warning, error) {
	p := &planner{
		opts: opts,
		home: opts.Home,
		plan: engine.Plan{Tool: model.ToolClaudeCode},
	}
	// PlanOpts.Home is the injectable one (tests must never plan against a
	// real home); the adapter's own home is the fallback for callers that
	// left it empty.
	if p.home == "" {
		p.home = a.home
	}

	for _, comp := range components {
		if err := p.add(comp); err != nil {
			return engine.Plan{}, p.warnings, err
		}
	}
	return p.plan, p.warnings, nil
}

// planner accumulates one Plan call's operations and warnings.
type planner struct {
	opts     engine.PlanOpts
	home     string
	plan     engine.Plan
	warnings []model.Warning
}

func (p *planner) add(comp model.Component) error {
	switch c := comp.(type) {
	case model.Skill:
		return p.addSkill(c)
	case model.MCPServer:
		p.addMCPServer(c)
		return nil
	case model.Agent:
		return p.addMarkdown(model.KindAgent, c.Spec.Scope, "agents", c.Spec.Name, c.Spec.Path)
	case model.Command:
		return p.addMarkdown(model.KindCommand, c.Spec.Scope, "commands", c.Spec.Name, c.Spec.Path)
	case model.Rule:
		return p.addRule(c)
	case model.Setting:
		p.addSetting(c)
		return nil
	default:
		p.warnf("", "%s %q is a component kind the claude-code adapter cannot apply; skipped",
			comp.Kind(), comp.Name())
		return nil
	}
}

// addSkill installs a bundled skill directory under <root>/.claude/skills/<name>/.
//
// The tree becomes one OpCreateDir per directory plus one OpCreateFile per
// file rather than a single "copy this tree" operation: per-file granularity
// is what lets the executor detect a conflict on exactly the file the user
// edited, and lets rollback put the machine back exactly as it was
// (internal/engine/plan.go → OpKind).
func (p *planner) addSkill(s model.Skill) error {
	name := s.Spec.Name
	root, ok := p.scopeRoot(s.Scope(), model.KindSkill, name)
	if !ok {
		return nil
	}
	if !p.safeName(model.KindSkill, name) {
		return nil
	}
	src, ok := p.bundledPath(model.KindSkill, name, s.Spec.Dir)
	if !ok {
		return nil
	}

	info, err := os.Stat(src)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.warnf(src, "skill %q has no bundled content at this path; skipped", name)
		return nil
	case err != nil:
		return fmt.Errorf("reading bundled skill %q: %w", name, err)
	case !info.IsDir():
		p.warnf(src, "skill %q must be a directory of files; skipped", name)
		return nil
	}

	dest := filepath.Join(claudeDir(root), "skills", name)
	desc := "skill " + name
	p.plan.Add(engine.Op{Kind: engine.OpCreateDir, Path: dest, Description: desc})
	return p.addTree(src, dest, desc)
}

// addTree walks a bundled directory, emitting an operation per entry in
// lexical order so a plan is deterministic and reviewable.
func (p *planner) addTree(src, dest, desc string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil // the root directory op is already emitted
		}
		target := filepath.Join(dest, rel)

		switch {
		case d.IsDir():
			// Emitted explicitly so a directory a skill needs but leaves
			// empty still appears in the preview and still gets created.
			p.plan.Add(engine.Op{Kind: engine.OpCreateDir, Path: target, Description: desc})
			return nil
		case !d.Type().IsRegular():
			// Packs are written with symlinks resolved, so a non-regular
			// file here is content agentpack cannot carry across machines.
			p.warnf(path, "%s: not a regular file; skipped", desc)
			return nil
		}

		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("reading bundled %s: %w", desc, readErr)
		}
		p.plan.Add(engine.Op{
			Kind:        p.fileKind(),
			Path:        target,
			Content:     content,
			Perm:        filePerm(d),
			Description: desc,
		})
		return nil
	})
}

// addMarkdown installs a single bundled markdown file (an agent or a command)
// at <root>/.claude/<subdir>/<name>.md. The name is the file Claude Code
// shows the user — the agent it invokes, the slash command it offers — so it
// comes from the component, not from whatever the pack called the file.
func (p *planner) addMarkdown(kind model.Kind, scope model.Scope, subdir, name, src string) error {
	root, ok := p.scopeRoot(scope, kind, name)
	if !ok {
		return nil
	}
	if !p.safeName(kind, name) {
		return nil
	}
	resolved, ok := p.bundledPath(kind, name, src)
	if !ok {
		return nil
	}
	dest := filepath.Join(claudeDir(root), subdir, withMarkdownExt(name))
	return p.addFile(resolved, dest, string(kind)+" "+name)
}

// addRule installs instruction content at the path this tool reads it from.
//
// The filename comes from the component's render map, which is the manifest's
// answer to "one logical rule, many tools": the same content is CLAUDE.md for
// Claude Code and AGENTS.md for Codex (docs/spec/pack-manifest.md → Rules).
// A rule handed to this adapter without a claude-code entry still gets
// installed — at the documented default — with a warning, because the pack
// clearly meant it for this tool and dropping it would lose content.
func (p *planner) addRule(r model.Rule) error {
	name := r.Spec.Name
	root, ok := p.scopeRoot(r.Scope(), model.KindRule, name)
	if !ok {
		return nil
	}

	rel := r.Spec.Render[model.ToolClaudeCode]
	if rel == "" {
		p.warnf("", "rule %q does not say how claude-code renders it; using %s", name, defaultRuleFile)
		rel = defaultRuleFile
	}
	local := filepath.FromSlash(rel)
	if strings.ContainsRune(rel, '\\') || !filepath.IsLocal(local) {
		// A render path that escapes its root would write outside the
		// user's config on a hand-written or hostile pack.
		p.warnf("", "rule %q renders to %q, which is not a relative path inside the target; skipped", name, rel)
		return nil
	}

	src, ok := p.bundledPath(model.KindRule, name, r.Spec.Path)
	if !ok {
		return nil
	}
	// Global instructions live inside ~/.claude; a project's live at its
	// root, next to the code they describe (config matrix → Claude Code).
	base := root
	if r.Scope() == model.ScopeGlobal {
		base = claudeDir(root)
	}
	return p.addFile(src, filepath.Join(base, local), "rule "+name)
}

// addFile emits one file operation from bundled content.
func (p *planner) addFile(src, dest, desc string) error {
	content, err := os.ReadFile(src)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.warnf(src, "%s has no bundled content at this path; skipped", desc)
		return nil
	case err != nil:
		return fmt.Errorf("reading bundled %s: %w", desc, err)
	}
	p.plan.Add(engine.Op{
		Kind:        p.fileKind(),
		Path:        dest,
		Content:     content,
		Description: desc,
	})
	return nil
}

// addSetting merges a pack's portable settings into settings.json, one
// operation per top-level key.
//
// Per key, not per document, for two reasons: a merge operation must name a
// key path (a whole-document merge has no legible preview line and no
// conflict boundary), and the deep strategy then preserves what the user
// keeps under the same key — adding permissions.allow must not drop their
// permissions.deny.
func (p *planner) addSetting(s model.Setting) {
	name := s.Spec.Name
	root, ok := p.scopeRoot(s.Scope(), model.KindSetting, name)
	if !ok {
		return
	}
	if len(s.Spec.Values) == 0 {
		p.warnf("", "setting %q carries no values; nothing to apply", name)
		return
	}

	path := filepath.Join(claudeDir(root), "settings.json")
	keys := make([]string, 0, len(s.Spec.Values))
	for k := range s.Spec.Values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		value := s.Spec.Values[key]
		if value == nil {
			// There is no delete operation: restore adds and updates config,
			// it never removes what it did not put there.
			p.warnf(path, "setting %q has a null value at %q; skipped (restore never deletes config)", name, key)
			continue
		}
		p.plan.Add(engine.Op{
			Kind:        engine.OpMergeValue,
			Path:        path,
			Format:      engine.FormatJSON,
			KeyPath:     []string{key},
			Value:       value,
			Strategy:    p.settingsStrategy(),
			Description: "setting " + name,
		})
	}
}

// addMCPServer merges one server into the file Claude Code reads for that
// scope: ~/.claude.json → mcpServers (user scope) or <project>/.mcp.json
// (project scope, the shareable one).
//
// It is always a merge at mcpServers.<name>, never a file write — including
// under opts.Replace. ~/.claude.json holds the user's app state, OAuth state
// and per-project history alongside their MCP config; rewriting it to add a
// server would be data loss, and "replace" means replacing *this server's*
// entry, which the key path already expresses.
func (p *planner) addMCPServer(s model.MCPServer) {
	spec := s.Spec
	path, ok := p.mcpPath(spec.Scope, spec.Name)
	if !ok {
		return
	}

	entry := map[string]any{}
	if spec.Transport.Valid() {
		entry["type"] = string(spec.Transport)
	} else {
		p.warnf(path, "mcp server %q has unknown transport %q; wrote the entry without a type", spec.Name, spec.Transport)
	}

	env := copyStrings(spec.Env)
	headers := copyStrings(spec.Headers)

	switch {
	case spec.Transport == model.TransportHTTP, spec.Transport == model.TransportSSE:
		if spec.URL == "" {
			p.warnf(path, "mcp server %q is %s but has no url; skipped", spec.Name, spec.Transport)
			return
		}
		entry["url"] = spec.URL
	case spec.Command != "":
		entry["command"] = spec.Command
		if len(spec.Args) > 0 {
			entry["args"] = append([]string(nil), spec.Args...)
		}
	case spec.URL != "":
		// An unrecognized transport that still names a url is addressable as
		// a remote server; the missing type was already warned about.
		entry["url"] = spec.URL
	default:
		p.warnf(path, "mcp server %q has no command or url; nothing to start, skipped", spec.Name)
		return
	}

	for _, cred := range spec.Credentials {
		p.injectCredential(path, spec, cred, env, headers)
	}
	if len(env) > 0 {
		entry["env"] = env
	}
	if len(headers) > 0 {
		entry["headers"] = headers
	}

	p.plan.Add(engine.Op{
		Kind:        engine.OpMergeValue,
		Path:        path,
		Format:      engine.FormatJSON,
		KeyPath:     []string{"mcpServers", spec.Name},
		Value:       entry,
		Strategy:    engine.MergeSet,
		Description: "mcp server " + spec.Name,
	})
}

// injectCredential writes one credential into the server entry being built.
//
// Where Claude Code expands environment variables, agentpack writes the
// *reference* rather than the secret: .mcp.json supports ${VAR}, so a
// project-scope server gets "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"} and
// the token stays in the installer's environment — which matters because
// .mcp.json is the shareable file, the one that gets committed
// (docs/research/tool-config-matrix.md → Claude Code notes; docs/security.md
// threat 4). The consequence is that the variable has to be exported when
// Claude Code runs: a value the resolver read from the keychain or a prompt
// lives in agentpack's process, not in the user's shell, and restore is what
// tells them so.
//
// ~/.claude.json is not documented to expand anything, so a user-scope server
// gets the resolved value — the same posture Claude Code itself writes, in a
// file that is private to the user.
//
// A header credential never uses indirection: the manifest names a *header*,
// not an environment variable, so there is no variable name to reference. Its
// value is the credential's format with the secret substituted ("Bearer
// {value}" → "Bearer <secret>").
//
// An unresolved credential is never silently dropped and never written as an
// empty string — an empty token fails inside the tool, far from the cause.
// The entry keeps a visible placeholder naming the injection point, and the
// warning says what to supply.
func (p *planner) injectCredential(path string, spec model.MCPServerSpec, cred model.Credential, env, headers map[string]string) {
	switch {
	case cred.Env != "" && cred.Header != "":
		p.warnf(path, "mcp server %q declares a credential as both env %q and header %q; skipped (it names no single injection point)",
			spec.Name, cred.Env, cred.Header)
		return
	case cred.Env == "" && cred.Header == "":
		p.warnf(path, "mcp server %q declares a credential with no env or header; skipped (it names no injection point)", spec.Name)
		return
	}

	name := cred.Env
	if cred.Header != "" {
		name = cred.Header
	}
	// A blank resolution is not a resolution: it would write an empty
	// credential, which is indistinguishable from a broken one at runtime.
	value, resolved := p.opts.Credentials[name]
	if strings.TrimSpace(value) == "" {
		resolved = false
	}

	if cred.Env != "" {
		if expandsEnv(spec.Scope) {
			env[cred.Env] = envRef(cred.Env)
			if !resolved {
				p.warnCredential(path, spec.Name, cred, name, "environment variable",
					fmt.Sprintf("wrote the %s reference", envRef(cred.Env)))
			}
			return
		}
		if resolved {
			env[cred.Env] = value
			return
		}
		env[cred.Env] = envRef(cred.Env)
		p.warnCredential(path, spec.Name, cred, name, "environment variable",
			fmt.Sprintf("wrote the %s placeholder, which this file does not expand", envRef(cred.Env)))
		return
	}

	if resolved {
		headers[cred.Header] = renderCredential(cred.Format, value)
		return
	}
	// The manifest's own {value} token is the placeholder: it is visibly
	// unfilled, so the header is obviously incomplete rather than subtly wrong.
	headers[cred.Header] = renderCredential(cred.Format, "")
	p.warnCredential(path, spec.Name, cred, name, "header",
		fmt.Sprintf("wrote %q as a placeholder", headers[cred.Header]))
}

// warnCredential reports an injection point left unfilled, carrying the
// pack's own description and obtain URL so the user is told what to get and
// where without opening the manifest.
func (p *planner) warnCredential(path, server string, cred model.Credential, name, kind, wrote string) {
	msg := fmt.Sprintf("mcp server %q: credential %s (%s) has no resolved value; %s — supply it before using this server",
		server, name, kind, wrote)
	if cred.Description != "" {
		msg += " (" + cred.Description + ")"
	}
	if cred.ObtainURL != "" {
		msg += " — obtain one at " + cred.ObtainURL
	}
	p.warnf(path, "%s", msg)
}

// renderCredential applies a credential's format template. An empty format
// means the whole value is the credential; an empty value leaves the {value}
// token in place as a visible placeholder.
func renderCredential(format, value string) string {
	if format == "" {
		format = "{value}"
	}
	if value == "" {
		return format
	}
	return strings.ReplaceAll(format, "{value}", value)
}

// envRef is the ${VAR} form Claude Code expands in .mcp.json.
func envRef(name string) string { return "${" + name + "}" }

// expandsEnv reports whether the file a scope writes to performs ${VAR}
// expansion. Project scope means .mcp.json, which does; user scope means
// ~/.claude.json, which is not documented to
// (docs/research/tool-config-matrix.md → Claude Code notes).
func expandsEnv(scope model.Scope) bool { return scope == model.ScopeProject }

// scopeRoot resolves the directory a scope's config lives under: the home
// directory, or the target project. A missing root is a skip with a warning,
// never a guess — writing a pack's project config into an arbitrary
// directory is worse than not writing it.
func (p *planner) scopeRoot(scope model.Scope, kind model.Kind, name string) (string, bool) {
	switch scope {
	case model.ScopeProject:
		if p.opts.ProjectDir == "" {
			p.warnf("", "%s %q is project-scoped but no project directory was given; skipped", kind, name)
			return "", false
		}
		return p.opts.ProjectDir, true
	default:
		if p.home == "" {
			p.warnf("", "%s %q is global but the home directory is unknown; skipped", kind, name)
			return "", false
		}
		return p.home, true
	}
}

// mcpPath is the file a scope's MCP servers are merged into.
func (p *planner) mcpPath(scope model.Scope, name string) (string, bool) {
	root, ok := p.scopeRoot(scope, model.KindMCPServer, name)
	if !ok {
		return "", false
	}
	if scope == model.ScopeProject {
		return projectMCPPath(root), true
	}
	return filepath.Join(root, ".claude.json"), true
}

// bundledPath resolves where a component's content sits. A manifest records
// a slash-separated path inside the pack (source.bundled), so a relative one
// is resolved against opts.PackDir; an absolute one — what a scanned
// inventory carries — is used as it stands.
func (p *planner) bundledPath(kind model.Kind, name, raw string) (string, bool) {
	if raw == "" {
		p.warnf("", "%s %q has no bundled content; skipped", kind, name)
		return "", false
	}
	local := filepath.FromSlash(raw)
	if filepath.IsAbs(local) {
		return local, true
	}
	if p.opts.PackDir == "" {
		p.warnf("", "%s %q refers to bundled content at %q but no pack directory was given; skipped", kind, name, raw)
		return "", false
	}
	if !filepath.IsLocal(local) {
		// A bundled path that climbs out of the pack would install a file
		// from anywhere on the installer's disk (docs/security.md threat 2).
		p.warnf("", "%s %q refers to %q, which escapes the pack directory; skipped", kind, name, raw)
		return "", false
	}
	return filepath.Join(p.opts.PackDir, local), true
}

// safeName rejects a component name that would place a file outside the
// directory it belongs in. Names become path elements, and a manifest is
// data from someone else's machine.
func (p *planner) safeName(kind model.Kind, name string) bool {
	if name == "" || !filepath.IsLocal(name) || strings.ContainsAny(name, `/\`) {
		p.warnf("", "%s %q has a name that cannot be used as a file name; skipped", kind, name)
		return false
	}
	return true
}

// fileKind is the operation content writes use. Creating is the default and
// refuses to overwrite a file the user changed; replace is the mode they
// chose explicitly, and the executor still backs the file up first.
func (p *planner) fileKind() engine.OpKind {
	if p.opts.Replace {
		return engine.OpReplaceFile
	}
	return engine.OpCreateFile
}

// settingsStrategy deep-merges by default, so the keys a user keeps under
// the same settings key survive. Replace mode sets the key outright — that
// is what the user asked for — but still only at that key path: the rest of
// settings.json is untouched either way.
func (p *planner) settingsStrategy() engine.MergeStrategy {
	if p.opts.Replace {
		return engine.MergeSet
	}
	return engine.MergeDeep
}

func (p *planner) warnf(path, format string, args ...any) {
	p.warnings = append(p.warnings, model.Warning{Path: path, Message: fmt.Sprintf(format, args...)})
}

// claudeDir is Claude Code's config directory under a home or project root.
func claudeDir(root string) string { return filepath.Join(root, ".claude") }

// withMarkdownExt gives a component name the .md extension the tool expects,
// without doubling one the name already carries.
func withMarkdownExt(name string) string {
	if strings.EqualFold(filepath.Ext(name), ".md") {
		return name
	}
	return name + ".md"
}

// filePerm keeps a bundled file's executable bit — a skill can carry a script
// — and otherwise leaves the mode to the executor's default.
func filePerm(d fs.DirEntry) fs.FileMode {
	info, err := d.Info()
	if err != nil || info.Mode().Perm()&0o111 == 0 {
		return 0
	}
	return 0o755
}

func copyStrings(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
