package packio

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/secrets"
)

// Issue is one spec violation found by ValidatePack. Ref names what is
// wrong ("skills/dup", "metadata.name"); Message says how.
type Issue struct {
	Ref     string
	Message string
}

func (i Issue) String() string {
	if i.Ref == "" {
		return i.Message
	}
	return i.Ref + ": " + i.Message
}

// ValidatePack checks a pack directory against the spec: manifest schema,
// name validity and uniqueness, source shape, bundled paths, MCP server
// shape — and always the whole-pack secret scan. err reports only
// I/O-level failure (the directory itself unreadable); everything wrong
// with the pack comes back as issues and findings, both of which make the
// pack invalid.
//
// allow supplements the pack's own dir/AllowlistFilename, when present, as
// the review flow's allowlist (docs/backlog.md P2.9): entries a caller has
// decided waive specific findings (e.g. from --allow-finding). Only
// Reviewable findings can ever be matched — a high-confidence match always
// blocks regardless of allow. blocking is what still fails validation;
// allowed is what the merged allowlist waived, for transparent reporting.
func ValidatePack(dir string, allow []secrets.AllowEntry) ([]Issue, []secrets.Finding, []secrets.Finding, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("validating pack: %w", err)
	}
	if !info.IsDir() {
		return nil, nil, nil, fmt.Errorf("validating pack: %s is not a directory", dir)
	}

	var issues []Issue
	data, err := os.ReadFile(filepath.Join(dir, ManifestFilename))
	switch {
	case errors.Is(err, fs.ErrNotExist):
		issues = append(issues, Issue{Message: ManifestFilename + " not found: not a pack directory"})
	case err != nil:
		return nil, nil, nil, fmt.Errorf("validating pack: %w", err)
	default:
		m, decodeErr := DecodeManifest(data)
		if decodeErr != nil {
			issues = append(issues, Issue{Ref: ManifestFilename, Message: decodeErr.Error()})
		} else {
			issues = append(issues, validateManifest(dir, m)...)
		}
	}

	// Symlinks (and other non-regular files) are invisible to the secret
	// scanner but would be dereferenced by archivers and future restores —
	// content could ship without ever being scanned. A pack is plain files
	// only.
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type().IsRegular() {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		issues = append(issues, Issue{
			Ref:     filepath.ToSlash(rel),
			Message: "not a regular file (symlinks cannot be secret-scanned and are not allowed in a pack)",
		})
		return nil
	})
	if walkErr != nil {
		return issues, nil, nil, fmt.Errorf("validating pack: %w", walkErr)
	}

	// The secret scan always runs (docs/security.md layer 3): a schema
	// violation must not mask a leak.
	findings, err := secrets.ScanPack(dir)
	if err != nil {
		return issues, nil, nil, fmt.Errorf("validating pack: %w", err)
	}

	// A committed dir/AllowlistFilename (docs/backlog.md P2.9 review flow)
	// applies automatically, same as CI would see it; allow supplements it
	// for one-off local overrides.
	fileAllow, err := loadAllowlistFile(dir)
	if err != nil {
		return issues, nil, nil, fmt.Errorf("validating pack: %w", err)
	}
	merged := append(append([]secrets.AllowEntry(nil), fileAllow...), allow...)
	blocking, allowedFindings, _, _ := secrets.FilterAllowed(findings, merged)
	return issues, blocking, allowedFindings, nil
}

// loadAllowlistFile reads dir/AllowlistFilename, returning no entries (not
// an error) when it does not exist — most packs have none.
func loadAllowlistFile(dir string) ([]secrets.AllowEntry, error) {
	data, err := os.ReadFile(filepath.Join(dir, AllowlistFilename))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	entries, err := secrets.ParseAllowlist(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", AllowlistFilename, err)
	}
	return entries, nil
}

func validateManifest(dir string, m *Manifest) []Issue {
	var issues []Issue
	add := func(ref, format string, args ...any) {
		issues = append(issues, Issue{Ref: ref, Message: fmt.Sprintf(format, args...)})
	}

	if !packNameRe.MatchString(m.Metadata.Name) {
		add("metadata.name", "invalid name %q: need lowercase letters, digits, and inner hyphens", m.Metadata.Name)
	}
	for _, target := range m.Targets {
		if !target.Valid() {
			add("targets", "unknown tool %q", target)
		}
	}

	checkMeta := func(ref string, meta ComponentMeta, seen map[string]bool) {
		if meta.Name == "" {
			add(ref, "name is required")
		} else if seen[meta.Name] {
			add(ref, "duplicate name %q within its kind", meta.Name)
		}
		seen[meta.Name] = true
		if meta.Scope != "" && !meta.Scope.Valid() {
			add(ref, "unknown scope %q", meta.Scope)
		}
		for _, target := range meta.Targets {
			if !target.Valid() {
				add(ref, "unknown tool %q in targets", target)
			}
		}
	}

	// checkSource validates the exactly-one-source rule and, for bundled
	// sources, that the path stays inside the pack, exists, and has the
	// right shape (skills are directories, everything else files).
	checkSource := func(ref string, src Source, wantDir bool) {
		primaries := 0
		for _, s := range []string{src.Plugin, src.NPM, src.Bundled} {
			if s != "" {
				primaries++
			}
		}
		if primaries != 1 {
			add(ref, "needs exactly one source (plugin, npm, or bundled); got %d", primaries)
		}
		if src.Ref != "" && src.NPM == "" {
			add(ref, "source ref requires npm")
		}
		if src.Bundled == "" {
			return
		}
		// Pack paths are slash-separated on every platform; a backslash
		// would be a filename on unix but a separator (and potential
		// escape) on Windows — verdicts must not depend on the OS.
		if strings.ContainsRune(src.Bundled, '\\') {
			add(ref, "bundled path %q must use forward slashes", src.Bundled)
			return
		}
		if !filepath.IsLocal(filepath.FromSlash(src.Bundled)) {
			add(ref, "bundled path %q escapes the pack directory", src.Bundled)
			return
		}
		info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(src.Bundled)))
		if err != nil {
			add(ref, "bundled path %q does not exist in the pack", src.Bundled)
			return
		}
		if wantDir && !info.IsDir() {
			add(ref, "bundled path %q must be a directory", src.Bundled)
		}
		if !wantDir && info.IsDir() {
			add(ref, "bundled path %q must be a file", src.Bundled)
		}
	}

	seen := map[string]bool{}
	for _, s := range m.Components.Skills {
		ref := "skills/" + s.Name
		checkMeta(ref, s.ComponentMeta, seen)
		checkSource(ref, s.Source, true)
	}

	seen = map[string]bool{}
	for _, srv := range m.Components.MCPServers {
		ref := "mcp_servers/" + srv.Name
		checkMeta(ref, srv.ComponentMeta, seen)
		if !srv.Transport.Valid() {
			add(ref, "unknown transport %q", srv.Transport)
		}
		switch srv.Transport {
		case model.TransportStdio:
			if srv.Command == "" {
				add(ref, "stdio transport requires a command")
			}
		case model.TransportHTTP, model.TransportSSE:
			if srv.URL == "" {
				add(ref, "%s transport requires a url", srv.Transport)
			}
		}
		for i, cred := range srv.Credentials {
			credRef := fmt.Sprintf("%s.credentials[%d]", ref, i)
			if (cred.Env == "") == (cred.Header == "") {
				add(credRef, "needs exactly one of env or header")
			}
			if cred.Format != "" && cred.Header == "" {
				add(credRef, "format requires header")
			}
		}
	}

	seen = map[string]bool{}
	for _, a := range m.Components.Agents {
		ref := "agents/" + a.Name
		checkMeta(ref, a.ComponentMeta, seen)
		checkSource(ref, a.Source, false)
	}

	seen = map[string]bool{}
	for _, r := range m.Components.Rules {
		ref := "rules/" + r.Name
		checkMeta(ref, r.ComponentMeta, seen)
		checkSource(ref, r.Source, false)
		for _, tool := range sortedToolKeys(r.Render) {
			rendered := r.Render[tool]
			if !tool.Valid() {
				add(ref, "render names unknown tool %q", tool)
			}
			if rendered == "" || strings.ContainsRune(rendered, '\\') || !filepath.IsLocal(filepath.FromSlash(rendered)) {
				add(ref, "render path %q for %s must be a relative slash-separated path", rendered, tool)
			}
		}
	}

	seen = map[string]bool{}
	for _, c := range m.Components.Commands {
		ref := "commands/" + c.Name
		checkMeta(ref, c.ComponentMeta, seen)
		checkSource(ref, c.Source, false)
	}

	seen = map[string]bool{}
	for _, s := range m.Components.Settings {
		ref := "settings/" + s.Name
		checkMeta(ref, s.ComponentMeta, seen)
	}

	return issues
}

func sortedToolKeys(m map[model.ToolID]string) []model.ToolID {
	keys := make([]model.ToolID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}
