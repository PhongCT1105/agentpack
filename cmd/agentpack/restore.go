package main

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/PhongCT1105/agentpack/internal/model"
	"github.com/PhongCT1105/agentpack/internal/packio"
)

func newRestoreCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restore <dir>",
		Short: "Read a pack and show what restoring it would set up (preview only for now)",
		Long: `Restore reads a pack directory and renders everything a restore involves:
the full component contents, every credential you would be asked to provide
(the pack stores requirements, never values), and every external service the
restored environment would connect to or install from.

Invalid or secret-flagged packs are refused outright — anything restore
operates on must pass the same gate as 'agentpack validate'.

Applying the plan is not implemented yet: this command is a read-only
preview and changes nothing on this machine.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			pack, err := packio.ReadPack(args[0])
			var invalid *packio.InvalidPackError
			if errors.As(err, &invalid) {
				for _, issue := range invalid.Issues {
					fmt.Fprintf(out, "issue: %s\n", issue)
				}
				for _, f := range invalid.Findings {
					fmt.Fprintf(out, "suspected secret: %s:%d %s %s\n", f.Path, f.Line, f.Rule, f.Excerpt)
				}
				return fmt.Errorf("refusing to restore: %w", err)
			}
			if err != nil {
				return err
			}
			renderRestorePreview(out, pack)
			return nil
		},
	}
}

func renderRestorePreview(out io.Writer, pack *packio.Pack) {
	m := pack.Manifest

	renderPackHeader(out, m)
	fmt.Fprintln(out)
	renderPackContents(out, m)
	renderCredentialRequirements(out, m.CredentialRequirements())
	renderExternalServices(out, m.ExternalServices())

	if m.ProjectScoped() {
		fmt.Fprintln(out, "\nthis pack has project-scoped components: restore will ask for a target project directory")
	}
	fmt.Fprintln(out, "\nnothing was applied — restore apply is not implemented yet (this is a read-only preview)")
}

func renderPackHeader(out io.Writer, m *packio.Manifest) {
	if m.Metadata.Title != "" {
		fmt.Fprintf(out, "pack %s — %s\n", m.Metadata.Name, m.Metadata.Title)
	} else {
		fmt.Fprintf(out, "pack %s\n", m.Metadata.Name)
	}
	if m.Metadata.Description != "" {
		fmt.Fprintf(out, "  %s\n", sanitizeCell(strings.TrimSpace(m.Metadata.Description)))
	}
	var attribution []string
	if m.Metadata.Author != "" {
		attribution = append(attribution, "author: "+m.Metadata.Author)
	}
	if m.Metadata.License != "" {
		attribution = append(attribution, "license: "+m.Metadata.License)
	}
	if len(attribution) > 0 {
		fmt.Fprintf(out, "  %s\n", strings.Join(attribution, "  "))
	}
	if len(m.Targets) > 0 {
		fmt.Fprintf(out, "  targets: %s\n", joinTools(m.Targets))
	}
}

// renderPackContents lists every component grouped by manifest section, in
// manifest order. Detail lines show env/header values in full: a read pack
// has passed redaction and the secret scan, and the preview's job is to
// show exactly what a restore would write.
func renderPackContents(out io.Writer, m *packio.Manifest) {
	fmt.Fprintln(out, "contents:")
	c := m.Components

	type row struct {
		name, scope, detail string
	}
	section := func(header string, rows []row) {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(out, "  %s:\n", header)
		tw := tabwriter.NewWriter(out, 4, 4, 2, ' ', 0)
		for _, r := range rows {
			lines := strings.Split(r.detail, "\n")
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", r.name, r.scope, lines[0])
			for _, line := range lines[1:] {
				fmt.Fprintf(tw, "    \t\t%s\n", line)
			}
		}
		tw.Flush()
	}

	var rows []row
	for _, s := range c.Skills {
		rows = append(rows, row{s.Name, string(s.EffectiveScope()), sourceDetail(s.Source) + metaSuffix(s.ComponentMeta)})
	}
	section("skills", rows)

	rows = nil
	for _, srv := range c.MCPServers {
		rows = append(rows, row{srv.Name, string(srv.EffectiveScope()), mcpDetail(srv)})
	}
	section("mcp_servers", rows)

	rows = nil
	for _, a := range c.Agents {
		rows = append(rows, row{a.Name, string(a.EffectiveScope()), sourceDetail(a.Source) + metaSuffix(a.ComponentMeta)})
	}
	section("agents", rows)

	rows = nil
	for _, r := range c.Rules {
		detail := sourceDetail(r.Source) + metaSuffix(r.ComponentMeta)
		if len(r.Render) > 0 {
			var renders []string
			for _, tool := range sortedRenderTools(r.Render) {
				renders = append(renders, fmt.Sprintf("%s → %s", tool, r.Render[tool]))
			}
			detail += "\nrenders: " + strings.Join(renders, ", ")
		}
		rows = append(rows, row{r.Name, string(r.EffectiveScope()), detail})
	}
	section("rules", rows)

	rows = nil
	for _, cm := range c.Commands {
		rows = append(rows, row{cm.Name, string(cm.EffectiveScope()), sourceDetail(cm.Source) + metaSuffix(cm.ComponentMeta)})
	}
	section("commands", rows)

	rows = nil
	for _, s := range c.Settings {
		detail := "values: " + strings.Join(sortedAnyKeys(s.Values), ", ")
		rows = append(rows, row{s.Name, string(s.EffectiveScope()), detail + metaSuffix(s.ComponentMeta)})
	}
	section("settings", rows)
}

func renderCredentialRequirements(out io.Writer, reqs []packio.CredentialRequirement) {
	if len(reqs) == 0 {
		fmt.Fprintln(out, "\ncredentials to collect: none")
		return
	}
	fmt.Fprintf(out, "\ncredentials to collect (%d) — the pack stores none of these values:\n", len(reqs))
	for _, req := range reqs {
		cred := req.Credential
		switch {
		case cred.Env != "":
			fmt.Fprintf(out, "  %s (env var)\n", cred.Env)
		case cred.Format != "":
			fmt.Fprintf(out, "  %s (header, %q)\n", cred.Header, cred.Format)
		default:
			fmt.Fprintf(out, "  %s (header)\n", cred.Header)
		}
		line := "for MCP server " + req.Server
		if cred.Description != "" {
			line += ": " + cred.Description
		}
		fmt.Fprintf(out, "      %s\n", line)
		if cred.ObtainURL != "" {
			fmt.Fprintf(out, "      obtain: %s\n", cred.ObtainURL)
		}
	}
}

func renderExternalServices(out io.Writer, svcs []packio.ExternalService) {
	if len(svcs) == 0 {
		fmt.Fprintln(out, "\nexternal services: none")
		return
	}
	fmt.Fprintf(out, "\nexternal services (%d) — the restored environment connects to or installs from:\n", len(svcs))
	tw := tabwriter.NewWriter(out, 4, 4, 2, ' ', 0)
	for _, svc := range svcs {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", svc.ComponentRef, svc.Kind, svc.Ref)
	}
	tw.Flush()
}

// sourceDetail renders a component source the way the manifest spells it.
func sourceDetail(src packio.Source) string {
	switch {
	case src.Plugin != "":
		return "plugin: " + src.Plugin
	case src.NPM != "":
		if src.Ref != "" {
			return fmt.Sprintf("npm: %s (%s)", src.NPM, src.Ref)
		}
		return "npm: " + src.NPM
	case src.Bundled != "":
		return "bundled: " + src.Bundled
	default:
		return ""
	}
}

// mcpDetail renders an MCP server as transport + endpoint on the first
// line, then env, headers, and credential injection points on their own
// lines.
func mcpDetail(srv packio.MCPServer) string {
	var b strings.Builder
	b.WriteString(string(srv.Transport))
	if srv.Command != "" {
		b.WriteString(": " + srv.Command)
		if len(srv.Args) > 0 {
			b.WriteString(" " + strings.Join(srv.Args, " "))
		}
	} else if srv.URL != "" {
		b.WriteString(": " + srv.URL)
	}
	b.WriteString(metaSuffix(srv.ComponentMeta))
	for _, k := range sortedKeys(srv.Env) {
		fmt.Fprintf(&b, "\nenv %s=%s", k, srv.Env[k])
	}
	for _, k := range sortedKeys(srv.Headers) {
		fmt.Fprintf(&b, "\nheader %s=%s", k, srv.Headers[k])
	}
	for _, cred := range srv.Credentials {
		if cred.Env != "" {
			fmt.Fprintf(&b, "\nneeds credential: %s (env)", cred.Env)
		} else {
			fmt.Fprintf(&b, "\nneeds credential: %s (header)", cred.Header)
		}
	}
	return b.String()
}

// metaSuffix appends the optional marker and per-component target override.
func metaSuffix(meta packio.ComponentMeta) string {
	var s string
	if meta.Optional {
		s += "  [optional]"
	}
	if len(meta.Targets) > 0 {
		s += "  [targets: " + joinTools(meta.Targets) + "]"
	}
	return s
}

func joinTools(tools []model.ToolID) string {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = string(t)
	}
	return strings.Join(names, ", ")
}

func sortedRenderTools(m map[model.ToolID]string) []model.ToolID {
	keys := make([]model.ToolID, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
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
