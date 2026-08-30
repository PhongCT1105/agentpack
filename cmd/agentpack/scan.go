package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PhongCT1105/agentpack/internal/adapters/claudecode"
	"github.com/PhongCT1105/agentpack/internal/adapters/codex"
	"github.com/PhongCT1105/agentpack/internal/adapters/cursor"
	"github.com/PhongCT1105/agentpack/internal/adapters/gemini"
	"github.com/PhongCT1105/agentpack/internal/engine"
	"github.com/PhongCT1105/agentpack/internal/model"
)

// masked replaces env/header values in scan output: scan is read-only and
// local, but its output gets piped into files and issues, so raw secret
// values never leave the process. Keys stay visible — they are what a pack
// will later declare as credential requirements.
const masked = "***"

func defaultAdapters() []engine.Adapter {
	return []engine.Adapter{claudecode.New(), codex.New(), cursor.New(), gemini.New()}
}

func newScanCmd(adapters func() []engine.Adapter) *cobra.Command {
	var (
		jsonOut    bool
		projectDir string
	)
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Detect installed tools and list their portable components",
		Long: `Scan detects the supported AI coding tools on this machine and prints
their portable components (skills, MCP servers, agents, rules, commands,
settings) grouped by tool, kind, and scope. Read-only: nothing is modified.

Env and header values are always masked in output; only their names are
shown. Likely credential material inside MCP server args and URLs (header
flags, key=value pairs with secret-looking keys, URL userinfo and query
params) is masked too.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			scope := model.ScanScope{Global: true, ProjectDir: projectDir}
			results := engine.ScanAll(adapters(), scope)
			if jsonOut {
				return renderJSON(cmd, results)
			}
			renderTable(cmd, results)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "machine-readable output")
	cmd.Flags().StringVar(&projectDir, "project", defaultProjectDir(),
		"project directory to scan for project-scoped components (empty to skip)")
	return cmd
}

func defaultProjectDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func renderTable(cmd *cobra.Command, results []engine.ScanResult) {
	out := cmd.OutOrStdout()
	for i, res := range results {
		if i > 0 {
			fmt.Fprintln(out)
		}
		if !res.Installed {
			if res.Err != nil {
				fmt.Fprintf(out, "%s: error: %v\n", res.Tool, res.Err)
			} else {
				fmt.Fprintf(out, "%s: not detected\n", res.Tool)
			}
			continue
		}
		if res.Version != "" {
			fmt.Fprintf(out, "%s %s\n", res.Tool, res.Version)
		} else {
			fmt.Fprintf(out, "%s\n", res.Tool)
		}
		if res.Err != nil {
			// A scan error does not erase what was detected or the partial
			// inventory gathered before the failure.
			fmt.Fprintf(out, "  error: %v\n", res.Err)
		}

		if len(res.Inventory.Components) == 0 {
			fmt.Fprintln(out, "  (no portable components found)")
		}
		for _, kind := range model.Kinds() {
			comps := res.Inventory.ByKind(kind)
			if len(comps) == 0 {
				continue
			}
			for _, scope := range []model.Scope{model.ScopeGlobal, model.ScopeProject} {
				var scoped []model.Component
				for _, c := range comps {
					if c.Scope() == scope {
						scoped = append(scoped, c)
					}
				}
				if len(scoped) == 0 {
					continue
				}
				fmt.Fprintf(out, "  %s (%s)\n", kind, scope)
				tw := tabwriter.NewWriter(out, 4, 4, 2, ' ', 0)
				for _, c := range scoped {
					fmt.Fprintf(tw, "    %s\t%s\n", c.Name(), sanitizeCell(componentDetail(c)))
				}
				tw.Flush()
			}
		}
		if len(res.Inventory.Warnings) > 0 {
			fmt.Fprintln(out, "  warnings:")
			for _, w := range res.Inventory.Warnings {
				fmt.Fprintf(out, "    %s\n", w)
			}
		}
	}
}

// componentDetail is the second table column: enough to recognize the
// component, never a secret value.
func componentDetail(c model.Component) string {
	switch v := c.(type) {
	case model.Skill:
		return v.Spec.Description
	case model.MCPServer:
		var b strings.Builder
		b.WriteString(string(v.Spec.Transport))
		if v.Spec.Command != "" {
			b.WriteString(": " + v.Spec.Command)
			if len(v.Spec.Args) > 0 {
				b.WriteString(" " + strings.Join(maskArgs(v.Spec.Args), " "))
			}
		} else if v.Spec.URL != "" {
			b.WriteString(": " + maskURL(v.Spec.URL))
		}
		if keys := sortedKeys(v.Spec.Env); len(keys) > 0 {
			b.WriteString("  env: " + strings.Join(keys, ","))
		}
		if keys := sortedKeys(v.Spec.Headers); len(keys) > 0 {
			b.WriteString("  headers: " + strings.Join(keys, ","))
		}
		return b.String()
	case model.Agent:
		return v.Spec.Description
	case model.Command:
		return v.Spec.Description
	case model.Rule:
		return v.Spec.Path
	case model.Setting:
		return v.Spec.Path
	default:
		return ""
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// sanitizeCell keeps a table cell on one line: multi-line frontmatter
// descriptions would otherwise terminate the tabwriter row.
func sanitizeCell(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i] + " …"
	}
	return strings.ReplaceAll(s, "\t", " ")
}

// secretKeyRe marks arg/param keys that smell like credential material.
// Blunt by design until the P2.2 redactor exists.
var secretKeyRe = regexp.MustCompile(`(?i)(token|key|secret|password|passwd|credential|authorization|bearer)`)

// maskArgs hides likely credential material in MCP server args: the value
// following a header/credential flag, and the value part of key=value or
// key:value args whose key looks secret. Tokens commonly ride in args via
// patterns like `mcp-remote <url> --header "Authorization: Bearer …"`.
func maskArgs(args []string) []string {
	out := make([]string, len(args))
	maskNext := false
	for i, a := range args {
		lower := strings.ToLower(a)
		switch {
		case maskNext:
			out[i] = masked
			maskNext = false
		case lower == "-h" || lower == "--header" || lower == "--api-key" ||
			lower == "--token" || lower == "--password" || lower == "--bearer":
			out[i] = a
			maskNext = true
		default:
			out[i] = maskKVArg(a)
		}
	}
	return out
}

// maskKVArg masks the value part of a single key=value / key:value arg when
// the key looks secret (e.g. "Authorization: Bearer x", "api_key=x").
func maskKVArg(a string) string {
	sep := strings.IndexAny(a, "=:")
	if sep <= 0 {
		return a
	}
	key, val := a[:sep], a[sep+1:]
	if strings.TrimSpace(val) != "" && secretKeyRe.MatchString(key) {
		return key + a[sep:sep+1] + masked
	}
	return a
}

// maskURL strips credentials that can ride in a URL: userinfo and
// secret-looking query parameter values.
func maskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	changed := false
	if u.User != nil {
		u.User = url.User("redacted") // "***" would be percent-encoded here
		changed = true
	}
	q := u.Query()
	for k := range q {
		if secretKeyRe.MatchString(k) {
			q.Set(k, masked)
			changed = true
		}
	}
	if !changed {
		return raw
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func renderJSON(cmd *cobra.Command, results []engine.ScanResult) error {
	type warningJSON struct {
		Path    string `json:"path,omitempty"`
		Message string `json:"message"`
	}
	type toolJSON struct {
		Tool       model.ToolID     `json:"tool"`
		Installed  bool             `json:"installed"`
		Version    string           `json:"version,omitempty"`
		Error      string           `json:"error,omitempty"`
		Components []map[string]any `json:"components"`
		Warnings   []warningJSON    `json:"warnings"`
	}

	out := make([]toolJSON, 0, len(results))
	for _, res := range results {
		tj := toolJSON{
			Tool:       res.Tool,
			Installed:  res.Installed,
			Version:    res.Version,
			Components: []map[string]any{},
			Warnings:   make([]warningJSON, 0, len(res.Inventory.Warnings)),
		}
		for _, w := range res.Inventory.Warnings {
			tj.Warnings = append(tj.Warnings, warningJSON{Path: w.Path, Message: w.Message})
		}
		if res.Err != nil {
			tj.Error = res.Err.Error()
		}
		for _, c := range res.Inventory.Components {
			tj.Components = append(tj.Components, componentJSON(c))
		}
		out = append(out, tj)
	}

	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func componentJSON(c model.Component) map[string]any {
	m := map[string]any{
		"kind":  c.Kind(),
		"name":  c.Name(),
		"scope": c.Scope(),
	}
	switch v := c.(type) {
	case model.Skill:
		m["dir"] = v.Spec.Dir
		if v.Spec.Description != "" {
			m["description"] = v.Spec.Description
		}
	case model.MCPServer:
		m["transport"] = v.Spec.Transport
		if v.Spec.Command != "" {
			m["command"] = v.Spec.Command
		}
		if len(v.Spec.Args) > 0 {
			m["args"] = maskArgs(v.Spec.Args)
		}
		if len(v.Spec.Env) > 0 {
			m["env"] = maskValues(v.Spec.Env)
		}
		if v.Spec.URL != "" {
			m["url"] = maskURL(v.Spec.URL)
		}
		if len(v.Spec.Headers) > 0 {
			m["headers"] = maskValues(v.Spec.Headers)
		}
	case model.Agent:
		m["path"] = v.Spec.Path
		if v.Spec.Description != "" {
			m["description"] = v.Spec.Description
		}
	case model.Command:
		m["path"] = v.Spec.Path
		if v.Spec.Description != "" {
			m["description"] = v.Spec.Description
		}
	case model.Rule:
		m["path"] = v.Spec.Path
	case model.Setting:
		m["path"] = v.Spec.Path
	}
	return m
}

func maskValues(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = masked
	}
	return out
}
