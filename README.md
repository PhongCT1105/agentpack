# agentpack

**Pack up your AI coding environment. Restore it anywhere. Share it with anyone.**

Developers build heavily customized environments around agentic coding tools — Claude Code, Codex, Cursor, Gemini CLI, and more. Skills, MCP servers, custom agents, rules, prompts, permissions. That setup takes months to refine, and it is scattered across dozens of config files in different formats on one machine.

Switch computers, boot into another OS, or onboard a teammate, and you rebuild it all by hand — from memory.

`agentpack` makes the whole setup **portable**:

```
scan → understand → save → restore → share
```

```bash
# See everything powering your AI coding workflow, across all tools
agentpack scan

# Save it as a portable pack (secrets stripped, credentials declared)
agentpack save --all my-setup

# On a new machine: restore it, providing your own credentials
agentpack restore my-setup

# Or start from someone else's published setup
agentpack restore github.com/someone/fullstack-pack
```

*`scan`, `save`, and `validate` work today; `restore` is in progress — see [Status](#status).*

## Try it today: `agentpack scan`

The scanner is implemented for Claude Code and Codex CLI. Build from source (Go 1.27+):

```bash
go install github.com/PhongCT1105/agentpack/cmd/agentpack@latest
agentpack scan             # scans global config + the current directory
agentpack scan --json      # machine-readable
agentpack scan --project ~/work/api   # pick the project scope explicitly
```

Scan is read-only, and env/header values are always masked — only their names appear in output. Sample output (abbreviated):

```
$ agentpack scan
claude-code 2.0.44
  skill (global)
    brainstorming  Explore user intent and requirements before implementation
    code-review
  skill (project)
    deploy-check   Verify staging health before promoting a deploy
  mcp_server (global)
    github  stdio: npx -y @modelcontextprotocol/server-github  env: GITHUB_API_URL,GITHUB_TOKEN
    linear  http: https://mcp.linear.app/mcp  headers: Authorization
  agent (global)
    db-migrator  Plans and applies database migrations safely
  rule (project)
    CLAUDE.md        /home/user/projects/demo/CLAUDE.md
    CLAUDE.local.md  /home/user/projects/demo/CLAUDE.local.md
  command (global)
    review  Run a structured code review over the current diff
  setting (global)
    settings.json  /home/user/.claude/settings.json
  warnings:
    /home/user/.claude.json: mcpServers.legacy command "old-mcp" not found on this machine; server may be dead
    /home/user/.claude/commands/workflows: subdirectories are not modeled; skipped

codex 0.45.0
  mcp_server (global)
    github  stdio: npx -y @modelcontextprotocol/server-github  env: GITHUB_TOKEN
  rule (global)
    AGENTS.md  /home/user/.codex/AGENTS.md
  command (global)
    review  Structured review of the current diff
```

Warnings flag what scan saw but could not model — dead MCP servers, unknown config keys, unscannable files — so nothing vanishes silently.

`save` and `validate` work today:

```bash
agentpack save --all my-setup          # export a secrets-free pack
agentpack save --all --review-uncertain my-setup   # decide borderline values yourself
agentpack validate my-setup            # schema + secret scan; nonzero exit for CI
```

Saving redacts every secret into a credential *requirement* (the value is
never stored), skips personal files (`CLAUDE.local.md`,
`settings.local.json`), and re-scans the written pack as a final gate — a
known credential format anywhere always blocks the save. Lower-confidence
findings in bundled source, docs, or test fixtures (a JSX `key={...}` prop,
a prose example) are reviewable: `save` reports them separately, and
`--allow-finding <path>[:<line>]` waives one after you've checked it isn't
real, recording the waiver in `.agentpack-allow` for CI to reuse. See the
[pack-authoring guide](docs/guides/authoring.md). `restore` is the next
phase of the [backlog](docs/backlog.md).

## Why

You can share source code with Git, dependencies with package manifests, and containers with Docker. But your *AI development environment* — the combination of skills, MCP servers, agents, and rules that makes those tools useful together — still lives in fragmented, machine-local config.

Today, sharing a workflow means writing a blog post: *"install these 12 skills, configure these 5 MCP servers, copy these rules…"*

With agentpack it means sharing one thing:

> "This is my development setup."

## How it works

**1. Scan.** agentpack reads the config of every supported AI coding tool on your machine and builds a unified inventory: which skills are installed, which MCP servers are configured, which agents/rules/commands exist, which tool each belongs to, and what is global vs project-specific.

**2. Save.** The inventory is exported as a *pack* — a git-friendly directory with a manifest ([spec](docs/spec/pack-manifest.md)). Secrets are **never** written into a pack. An MCP server that needs a token is saved as a *requirement*:

```yaml
mcp_servers:
  - name: github
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    credentials:
      - env: GITHUB_TOKEN
        description: GitHub personal access token (repo scope)
```

**3. Restore.** On another machine (or for another person), agentpack reads the pack, shows a full plan of what will be installed and which credentials are required, prompts for those credentials locally, and writes the correct config files for each target tool. Nothing is applied without an explicit plan/confirm step.

**4. Share.** A pack is just files — push it to a Git repo and anyone can restore from it. A community registry of inspectable, opinionated setups ("Full-Stack Startup Engineer", "AI/ML Research Engineer") is on the [roadmap](docs/roadmap.md).

## Security model

- **Secrets never leave your machine.** Packs contain credential *requirements*, never credential *values*. The exporter redacts known secret fields and runs entropy/pattern scanning as a safety net. See [docs/security.md](docs/security.md).
- **No black boxes.** Before restoring, you see exactly what a pack contains: every skill, MCP server, rule, permission, and external service involved.
- **Plan before apply.** Restore is a two-step plan/confirm flow, like Terraform. Existing local config is backed up before any write.

## Supported tools

| Tool | Skills | MCP servers | Agents | Rules/instructions | Commands/prompts | Scan status |
|---|---|---|---|---|---|---|
| Claude Code | ✅ | ✅ | ✅ | ✅ (CLAUDE.md) | ✅ | **implemented** |
| Codex CLI | – | ✅ | – | ✅ (AGENTS.md) | ✅ | **implemented** |
| Cursor | – | ✅ | – | ✅ (.cursor/rules) | – | planned (v1) |
| Gemini CLI | – | ✅ | – | ✅ (GEMINI.md) | – | planned (v1) |

The neutral component model and per-tool adapters are described in [docs/architecture.md](docs/architecture.md). Adding a tool means adding one adapter.

## Status

**Pre-alpha.** Phases 1 and 2 are implemented: `agentpack scan` works for Claude Code and Codex CLI, and `agentpack save` / `agentpack validate` produce and check secrets-free packs — three-layer secret protection (schema exclusion, redaction with interactive review of uncertain values, release-blocking whole-pack scanning) is in place and fixture-tested. Phase 3 (restore) is next. Built in the open, task by task — see [docs/roadmap.md](docs/roadmap.md) and [docs/backlog.md](docs/backlog.md).

## Documentation

- [Vision](docs/vision.md) — the problem and the larger goal
- [Pack manifest spec (draft v0.1)](docs/spec/pack-manifest.md) — the portable setup format
- [Pack-authoring guide](docs/guides/authoring.md) — creating packs by export or by hand
- [Architecture](docs/architecture.md) — CLI design, adapters, component model
- [Security](docs/security.md) — threat model and credential handling
- [Tool config matrix](docs/research/tool-config-matrix.md) — where every tool keeps its config
- [Roadmap](docs/roadmap.md) · [Backlog](docs/backlog.md)

## Contributing

Contributions are welcome — especially new tool adapters and corrections to the config matrix. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
