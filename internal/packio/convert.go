package packio

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/secrets"
)

// ConvertOptions controls inventory → pack conversion.
type ConvertOptions struct {
	// Name becomes metadata.name; required, lowercase [a-z0-9-].
	Name string

	// TreatUncertainAsSecret decides uncertain redactor verdicts
	// (docs/security.md layer 2). nil means always redact — the safe
	// default; the P2.7 prompt flow supplies an interactive callback.
	TreatUncertainAsSecret func(key, value string, v secrets.Verdict) bool
}

// Bundle is one copy instruction: content the pack carries with it.
type Bundle struct {
	FromPath string // absolute path on the scanned machine
	ToPath   string // slash-separated path inside the pack
}

// Redaction records one value the conversion refused to store, for the
// save UI. It deliberately does not carry the value.
type Redaction struct {
	Component string // "<kind>/<name>"
	Key       string // env var, header, or settings key
	Verdict   secrets.Verdict
	Action    string // "credential" (moved to credentials) or "dropped"
}

// ConvertResult is a pack ready to write: manifest, content to copy, and
// everything the user should be told about.
type ConvertResult struct {
	Manifest   *Manifest
	Bundles    []Bundle
	Redactions []Redaction
	Warnings   []model.Warning
}

var packNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// Convert normalizes scanned inventories into a pack: values pass the
// secrets redactor (secret env vars and headers become credentials,
// secret settings values are dropped), authored content becomes bundle
// copy instructions, and names are slugged and deduplicated. Conversion is
// mechanical — deciding *which* components to include is the save
// command's job (personal files like settings.local.json are filtered
// there, not here).
func Convert(invs []model.Inventory, opts ConvertOptions) (*ConvertResult, error) {
	if !packNameRe.MatchString(opts.Name) {
		return nil, fmt.Errorf("invalid pack name %q: need lowercase letters, digits, and inner hyphens", opts.Name)
	}
	treatUncertain := opts.TreatUncertainAsSecret
	if treatUncertain == nil {
		treatUncertain = func(string, string, secrets.Verdict) bool { return true }
	}

	c := &converter{
		res: &ConvertResult{Manifest: &Manifest{
			APIVersion: APIVersion,
			Kind:       KindPack,
			Metadata:   Metadata{Name: opts.Name},
		}},
		treatUncertain: treatUncertain,
		names:          map[model.Kind]map[string]bool{},
	}

	present := map[model.ToolID]bool{}
	for _, inv := range invs {
		present[inv.Tool] = true
	}
	for _, tool := range model.Tools() { // canonical order
		if present[tool] {
			c.res.Manifest.Targets = append(c.res.Manifest.Targets, tool)
		}
	}

	for _, inv := range invs {
		for _, comp := range inv.Components {
			c.add(inv.Tool, comp)
		}
	}
	return c.res, nil
}

type converter struct {
	res            *ConvertResult
	treatUncertain func(string, string, secrets.Verdict) bool
	names          map[model.Kind]map[string]bool
}

func (c *converter) add(tool model.ToolID, comp model.Component) {
	switch v := comp.(type) {
	case model.Skill:
		name := c.resolveName(model.KindSkill, v.Spec.Name, v.Spec.Scope, tool)
		to := "skills/" + name
		c.bundle(v.Spec.Dir, to)
		c.res.Manifest.Components.Skills = append(c.res.Manifest.Components.Skills, Skill{
			ComponentMeta: c.meta(name, v.Spec.Description, v.Spec.Scope, tool),
			Source:        Source{Bundled: to},
		})
	case model.Agent:
		name := c.resolveName(model.KindAgent, v.Spec.Name, v.Spec.Scope, tool)
		to := "agents/" + name + ".md"
		c.bundle(v.Spec.Path, to)
		c.res.Manifest.Components.Agents = append(c.res.Manifest.Components.Agents, Agent{
			ComponentMeta: c.meta(name, v.Spec.Description, v.Spec.Scope, tool),
			Source:        Source{Bundled: to},
		})
	case model.Command:
		name := c.resolveName(model.KindCommand, v.Spec.Name, v.Spec.Scope, tool)
		to := "prompts/" + name + ".md"
		c.bundle(v.Spec.Path, to)
		c.res.Manifest.Components.Commands = append(c.res.Manifest.Components.Commands, Command{
			ComponentMeta: c.meta(name, v.Spec.Description, v.Spec.Scope, tool),
			Source:        Source{Bundled: to},
		})
	case model.Rule:
		name := c.resolveName(model.KindRule, v.Spec.Name, v.Spec.Scope, tool)
		to := "rules/" + name + ".md"
		c.bundle(v.Spec.Path, to)
		c.res.Manifest.Components.Rules = append(c.res.Manifest.Components.Rules, Rule{
			ComponentMeta: c.meta(name, "", v.Spec.Scope, tool),
			Source:        Source{Bundled: to},
			// Render preserves the filename the scanned tool consumes.
			Render: map[model.ToolID]string{tool: filepath.Base(v.Spec.Path)},
		})
	case model.Setting:
		name := c.resolveName(model.KindSetting, v.Spec.Name, v.Spec.Scope, tool)
		values := c.redactValues(model.KindSetting, name, v.Spec.Values)
		c.res.Manifest.Components.Settings = append(c.res.Manifest.Components.Settings, Setting{
			ComponentMeta: c.meta(name, "", v.Spec.Scope, tool),
			Values:        values,
		})
	case model.MCPServer:
		c.addMCPServer(tool, v.Spec)
	default:
		c.warnf("", "unsupported component kind %q (%s); skipped", comp.Kind(), comp.Name())
	}
}

func (c *converter) addMCPServer(tool model.ToolID, spec model.MCPServerSpec) {
	if !spec.Transport.Valid() {
		c.warnf("", "MCP server %q has unknown transport %q; skipped", spec.Name, spec.Transport)
		return
	}
	name := c.resolveName(model.KindMCPServer, spec.Name, spec.Scope, tool)
	ref := string(model.KindMCPServer) + "/" + name

	srv := MCPServer{
		ComponentMeta: c.meta(name, "", spec.Scope, tool),
		Transport:     spec.Transport,
		Command:       spec.Command,
		Args:          spec.Args,
		URL:           spec.URL,
	}

	// Env/credentials split (docs/security.md layer 2). Keys are sorted so
	// conversion is deterministic.
	for _, k := range sortedKeys(spec.Env) {
		if c.isSecret(ref, k, spec.Env[k]) {
			srv.Credentials = append(srv.Credentials, Credential{
				Env:         k,
				Description: fmt.Sprintf("required by MCP server %q (redacted on save)", name),
			})
			continue
		}
		if srv.Env == nil {
			srv.Env = map[string]string{}
		}
		srv.Env[k] = spec.Env[k]
	}
	for _, k := range sortedKeys(spec.Headers) {
		if c.isSecret(ref, k, spec.Headers[k]) {
			srv.Credentials = append(srv.Credentials, Credential{
				Header:      k,
				Format:      headerFormat(spec.Headers[k]),
				Description: fmt.Sprintf("required by MCP server %q (redacted on save)", name),
			})
			continue
		}
		if srv.Headers == nil {
			srv.Headers = map[string]string{}
		}
		srv.Headers[k] = spec.Headers[k]
	}

	// Args and URLs have no credential injection point in the manifest, so
	// a secret in them cannot be redacted structurally — the value ships in
	// the pack unless the user fixes their tool config. Warn honestly: the
	// whole-pack scan blocks confirmed secrets, but the uncertain band can
	// slip through it.
	for _, arg := range spec.Args {
		// Flag-style args are assignments: classify --token=X as (token, X).
		key, value, isAssign := strings.Cut(arg, "=")
		if !isAssign {
			key, value = "", arg
		}
		if secrets.Classify(strings.TrimLeft(key, "-"), value).Level != secrets.Plain {
			c.warnf("", "MCP server %q: an argument may contain a secret; move it to an env var so it can be redacted (the pack scan cannot always catch argument values)", name)
			break
		}
	}
	if spec.URL != "" {
		switch secrets.Classify("", spec.URL).Level {
		case secrets.Secret:
			c.warnf("", "MCP server %q: URL contains credentials; remove them (the pack scan will block saving as-is)", name)
		case secrets.Uncertain:
			c.warnf("", "MCP server %q: URL contains a high-entropy component; verify it is not a secret before publishing (the pack scan cannot always catch it)", name)
		}
	}

	c.res.Manifest.Components.MCPServers = append(c.res.Manifest.Components.MCPServers, srv)
}

// isSecret classifies one key/value and records the redaction when the
// value must not be stored.
func (c *converter) isSecret(componentRef, key, value string) bool {
	verdict := secrets.Classify(key, value)
	secret := verdict.Level == secrets.Secret ||
		(verdict.Level == secrets.Uncertain && c.treatUncertain(key, value, verdict))
	if secret {
		c.res.Redactions = append(c.res.Redactions, Redaction{
			Component: componentRef, Key: key, Verdict: verdict, Action: "credential",
		})
	}
	return secret
}

// redactValues deep-copies a settings document, dropping any value the
// redactor flags. Settings have no credentials field by design — a secret
// setting is simply not portable.
func (c *converter) redactValues(kind model.Kind, name string, values map[string]any) map[string]any {
	ref := string(kind) + "/" + name
	cleaned, _ := c.redactAny(ref, "", values)
	m, _ := cleaned.(map[string]any)
	return m
}

// redactAny returns a cleaned copy of v and whether to keep it. key is the
// nearest enclosing map key (array elements inherit their parent's).
func (c *converter) redactAny(ref, key string, v any) (any, bool) {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for _, k := range sortedAnyKeys(val) {
			if cleaned, keep := c.redactAny(ref, k, val[k]); keep {
				out[k] = cleaned
			}
		}
		return out, true
	case []any:
		out := make([]any, 0, len(val))
		for _, elem := range val {
			if cleaned, keep := c.redactAny(ref, key, elem); keep {
				out = append(out, cleaned)
			}
		}
		return out, true
	case string:
		verdict := secrets.Classify(key, val)
		if verdict.Level == secrets.Secret ||
			(verdict.Level == secrets.Uncertain && c.treatUncertain(key, val, verdict)) {
			c.res.Redactions = append(c.res.Redactions, Redaction{
				Component: ref, Key: key, Verdict: verdict, Action: "dropped",
			})
			return nil, false
		}
		return val, true
	default:
		return val, true
	}
}

// headerFormat reconstructs the non-secret shape of a redacted header:
// "Bearer {value}" for scheme-prefixed values (the spec's format field),
// empty when the whole value is the secret.
func headerFormat(value string) string {
	scheme, _, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok {
		return ""
	}
	switch strings.ToLower(scheme) {
	case "bearer", "basic", "token":
		return scheme + " {value}"
	}
	return ""
}

func (c *converter) meta(name, description string, scope model.Scope, tool model.ToolID) ComponentMeta {
	m := ComponentMeta{
		Name:        name,
		Description: description,
		Targets:     []model.ToolID{tool},
	}
	if scope != model.ScopeGlobal && scope != "" {
		m.Scope = scope // global is the manifest default; omit it
	}
	return m
}

func (c *converter) bundle(from, to string) {
	c.res.Bundles = append(c.res.Bundles, Bundle{FromPath: from, ToPath: to})
}

func (c *converter) warnf(path, format string, args ...any) {
	c.res.Warnings = append(c.res.Warnings, model.Warning{
		Path: path, Message: fmt.Sprintf(format, args...),
	})
}

// resolveName slugs a scanned component name and makes it unique within
// its kind, preferring readable disambiguators (scope, tool) over numbers.
func (c *converter) resolveName(kind model.Kind, raw string, scope model.Scope, tool model.ToolID) string {
	base := slugify(raw)
	if base == "" {
		base = slugify(string(kind))
	}
	taken := c.names[kind]
	if taken == nil {
		taken = map[string]bool{}
		c.names[kind] = taken
	}
	candidates := []string{base}
	if scope != "" {
		candidates = append(candidates, base+"-"+string(scope))
	}
	candidates = append(candidates, base+"-"+slugify(string(tool)))
	if scope != "" {
		candidates = append(candidates, base+"-"+slugify(string(tool))+"-"+string(scope))
	}
	for _, cand := range candidates {
		if !taken[cand] {
			taken[cand] = true
			return cand
		}
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("%s-%d", base, i)
		if !taken[cand] {
			taken[cand] = true
			return cand
		}
	}
}

var slugSepRe = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = slugSepRe.ReplaceAllString(strings.ToLower(s), "-")
	return strings.Trim(s, "-")
}

func sortedKeys(m map[string]string) []string {
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

// WritePack writes a converted pack into dir, which must not exist yet or
// must be an empty directory. After writing, the whole-pack secret scan
// (docs/security.md layer 3) runs as a final gate: any finding removes the
// written pack and fails the save — a leaky pack never stays on disk.
func WritePack(dir string, res *ConvertResult) ([]secrets.Finding, error) {
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return nil, fmt.Errorf("writing pack: %s exists and is not a directory", dir)
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return nil, fmt.Errorf("writing pack: %w", readErr)
		}
		if len(entries) > 0 {
			return nil, fmt.Errorf("writing pack: %s is not empty", dir)
		}
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return nil, fmt.Errorf("writing pack: %w", mkErr)
		}
	default:
		return nil, fmt.Errorf("writing pack: %w", err)
	}

	fail := func(e error) ([]secrets.Finding, error) {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			e = fmt.Errorf("%w (cleanup also failed, manually remove %s: %v)", e, dir, rmErr)
		}
		return nil, e
	}

	data, err := EncodeManifest(res.Manifest)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFilename), data, 0o644); err != nil {
		return fail(fmt.Errorf("writing manifest: %w", err))
	}

	for _, b := range res.Bundles {
		if err := copyBundle(b.FromPath, filepath.Join(dir, filepath.FromSlash(b.ToPath))); err != nil {
			return fail(fmt.Errorf("bundling %s: %w", b.FromPath, err))
		}
	}

	findings, err := secrets.ScanPack(dir)
	if err != nil {
		return fail(fmt.Errorf("scanning written pack: %w", err))
	}
	if len(findings) > 0 {
		err := fmt.Errorf("save blocked: %d suspected secret(s) in the written pack", len(findings))
		// The contract is that a leaky pack never stays on disk; a failed
		// removal must be loud, not silent.
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			err = fmt.Errorf("%w (cleanup also failed, manually remove %s: %v)", err, dir, rmErr)
		}
		return findings, err
	}
	return nil, nil
}

// copyBundle copies a file, or a directory tree of regular files (symlinks
// and other specials are skipped, matching the scanner's coverage).
func copyBundle(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(from, to, info.Mode().Perm())
	}
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		return copyFile(path, filepath.Join(to, rel), fi.Mode().Perm())
	})
}

func copyFile(from, to string, perm fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	src, err := os.Open(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
