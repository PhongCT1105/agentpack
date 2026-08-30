# Loop log

- 2026-08-30T09:32Z P1.1: go module + cobra skeleton with version command; CI workflow parked in docs/ci/ci.yml (token lacks workflow scope — human must git mv to .github/workflows/)
- 2026-08-30T09:40Z P1.2: internal/model — ToolID/Kind/Scope enums, Component interface, Inventory, Warning + table tests
- 2026-08-30T09:48Z P1.3: claudecode adapter Detect() — binary-on-PATH + ~/.claude|~/.claude.json probes, version parsing, fixture homes
- 2026-08-30T10:02Z P1.4: claudecode Scan skills global+project — ScanScope/Skill types in model, frontmatter description, symlink-following, warnings for broken dirs
- 2026-08-30T10:15Z P1.5: claudecode Scan MCP servers — Transport/MCPServer in model, surgical mixed-file read of ~/.claude.json + .mcp.json, transport inference, warnings for local-scope/unknown/misshapen entries
- 2026-08-30T10:30Z P1.6: claudecode Scan agents/commands/rules/settings — Agent/Command/Rule/Setting in model; personal files (CLAUDE.local.md, settings.local.json) scanned as components by design, save filters later; subdirs warn
