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
agentpack save my-setup

# On a new machine: restore it, providing your own credentials
agentpack restore my-setup

# Or start from someone else's published setup
agentpack restore github.com/someone/fullstack-pack
```

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

## Supported tools (planned for v1)

| Tool | Skills | MCP servers | Agents | Rules/instructions | Commands/prompts |
|---|---|---|---|---|---|
| Claude Code | ✅ | ✅ | ✅ | ✅ (CLAUDE.md) | ✅ |
| Codex CLI | – | ✅ | – | ✅ (AGENTS.md) | ✅ |
| Cursor | – | ✅ | – | ✅ (.cursor/rules) | – |
| Gemini CLI | – | ✅ | – | ✅ (GEMINI.md) | – |

The neutral component model and per-tool adapters are described in [docs/architecture.md](docs/architecture.md). Adding a tool means adding one adapter.

## Status

**Pre-alpha, docs-first.** This repository currently contains the specification, architecture, and roadmap. Implementation (a Go CLI) is being built in the open, task by task — see [docs/roadmap.md](docs/roadmap.md) and [docs/backlog.md](docs/backlog.md).

## Documentation

- [Vision](docs/vision.md) — the problem and the larger goal
- [Pack manifest spec (draft v0.1)](docs/spec/pack-manifest.md) — the portable setup format
- [Architecture](docs/architecture.md) — CLI design, adapters, component model
- [Security](docs/security.md) — threat model and credential handling
- [Tool config matrix](docs/research/tool-config-matrix.md) — where every tool keeps its config
- [Roadmap](docs/roadmap.md) · [Backlog](docs/backlog.md)

## Contributing

Contributions are welcome — especially new tool adapters and corrections to the config matrix. See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT](LICENSE)
