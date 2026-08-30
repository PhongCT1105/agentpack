# Vision: Portable Agentic Development Environments

## The problem

Modern developers increasingly build their coding workflows around AI agents — Claude Code, Codex, Cursor, Gemini CLI, and other agentic tools. Over time they accumulate a highly customized environment:

- MCP servers
- Agent skills
- Custom instructions and rules
- Reusable prompts and commands
- Specialized agents
- Tool integrations and permissions
- Environment-specific settings

This setup is **fragmented** across tools, folders, config formats, and machines. A developer can spend months improving their workflow, then lose most of it the moment they:

- buy a new computer
- switch between Windows, Linux, and macOS
- create a new development machine or cloud environment
- start a new repository
- join a new team

The knowledge of "which MCP servers do I use, which skills, where does each config live, how was each tool set up" exists only in the developer's memory and in scattered dotfiles.

The same fragmentation blocks **sharing**. You can publish an individual skill or MCP server today, but not the *entire environment that makes those pieces useful together*. Telling someone "this is the AI coding environment I use for full-stack development" currently means a blog post and an afternoon of manual installation.

## The idea

Make the agentic development environment a **first-class, portable, shareable artifact** — the way Git made source portable, package manifests made dependencies portable, and Docker made runtimes portable.

The full loop:

```
scan → understand → save → publish → share → restore
```

- **Scan**: read every supported tool's config on a machine and produce a unified inventory. Answer the question: *"What exactly is powering my AI coding workflow?"*
- **Understand**: present that inventory legibly — components grouped by kind, mapped to tools, split into global vs project scope, with required credentials and external services made explicit.
- **Save**: export the inventory as a *pack* — a portable, git-friendly, secrets-free representation of the environment.
- **Publish / share**: push a pack to a Git repo or (later) a community registry, where others can inspect it before installing. A pack is never a black box: what it installs, what it needs, and what it touches is visible up front.
- **Restore**: apply a pack on any machine. agentpack detects the required credentials, asks the installer for *their own* secrets, and writes the right config for each tool.

## Security as a founding constraint

MCP servers routinely require API keys, access tokens, and database credentials. A published setup must never contain the publisher's secrets.

The design rule: **packs describe credential requirements, never credential values.**

```
Original developer:  MCP config + private token
Published pack:      MCP config + "GITHUB_TOKEN required"
New developer:       pack + their own token
```

This is enforced structurally (secrets are never part of the pack schema) and defensively (redaction and secret-scanning on export). See [security.md](security.md).

## Who this is for

- **Developers with multiple machines / OSes** — save on one, restore on another.
- **Developers who share their workflow** — publish an opinionated environment ("Full-Stack Startup Engineer", "AI/ML Research Engineer") instead of a listicle.
- **Newcomers to agentic coding** — start from a proven, inspectable setup instead of assembling one component at a time. Community packs flatten the learning curve.
- **Teams** — onboard a developer with one restore command instead of a wiki page.

## What agentpack is not

- **Not a dotfiles manager.** It understands the *semantics* of agent configs (a skill, an MCP server, a rule), not just file paths. That is what makes cross-tool and cross-OS translation possible.
- **Not a package manager for individual components.** Skills and MCP servers already have distribution channels. agentpack composes them into environments.
- **Not a sync service.** v1 has no server. Packs are files; Git is the transport. A registry comes later, on top of the same open format.

## End state

A developer discovers another person's workflow, inspects everything it contains, installs it in minutes, provides their own credentials, and starts working with the same foundation. A community forms around sharing and improving these environments — especially for people just getting started with agentic development.
