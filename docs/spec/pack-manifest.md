# Pack Manifest Specification — draft v0.1

Status: **draft**. Everything here is open for revision until v1.0. Field names may change; the structural rules (especially the secrets rules) will not.

## Overview

A **pack** is a portable, secrets-free representation of an agentic development environment. It is a plain directory, designed to live in a Git repository:

```
my-setup/
├── agentpack.yaml          # the manifest (this spec)
├── skills/                 # bundled skill directories (optional)
│   └── my-custom-skill/
│       └── SKILL.md
├── rules/                  # bundled instruction files (optional)
│   ├── CLAUDE.md
│   └── AGENTS.md
├── prompts/                # bundled reusable prompts/commands (optional)
│   └── review.md
└── agents/                 # bundled agent definitions (optional)
    └── db-migrator.md
```

The manifest lists every component of the environment. Components either **reference** an installable source (an npm package, a git repo, a marketplace id) or **bundle** content in the pack directory. Referencing is preferred; bundling is for content the developer authored themselves.

## Top-level structure

```yaml
apiVersion: agentpack/v0
kind: Pack

metadata:
  name: fullstack-startup
  title: Full-Stack Startup Engineer
  description: >
    Frontend, debugging, testing, code review, GitHub integration,
    browser automation, and database tools for full-stack work.
  author: PhongCT1105
  license: MIT
  tags: [fullstack, react, supabase, github]

# Which tools this pack can configure. An installer may apply a subset.
targets: [claude-code, codex, cursor, gemini-cli]

components:
  skills: []          # see Components → Skills
  mcp_servers: []     # see Components → MCP servers
  agents: []          # see Components → Agents
  rules: []           # see Components → Rules
  commands: []        # see Components → Commands
  settings: []        # see Components → Settings
```

### `apiVersion` and `kind`

Required. `apiVersion: agentpack/v0` while this spec is a draft. Breaking manifest changes bump the version; the CLI refuses versions it does not understand.

### `metadata`

`name` is required: lowercase, `[a-z0-9-]`, unique within a registry namespace. Everything else is optional but strongly recommended for published packs.

### `targets`

The tools this pack knows how to configure. Adapter names are the canonical ids: `claude-code`, `codex`, `cursor`, `gemini-cli` (extensible). During restore, components apply only to targets that are installed (or explicitly selected).

## Scope

Every component has an optional `scope`:

- `global` (default) — the user-level config (e.g. `~/.claude/`, `~/.codex/`)
- `project` — applied into a project directory at restore time (e.g. `.claude/`, `.cursor/rules/`)

A pack containing `project`-scoped components asks for a target directory during restore.

## Components

Common fields for every component:

```yaml
- name: unique-within-kind     # required
  description: one line        # optional, shown during inspection
  scope: global | project      # default: global
  targets: [claude-code]       # optional override of pack-level targets
  optional: true               # installer may skip; default false
```

### Skills

```yaml
skills:
  - name: superpowers
    source:                       # exactly one source type
      plugin: superpowers@claude-plugins-official   # a plugin marketplace ref
  - name: find-skills
    source:
      npm: "skills"               # installed via `npx skills add ...`
      ref: vercel-labs/find-skills
  - name: my-custom-skill
    source:
      bundled: skills/my-custom-skill    # path inside the pack
```

### MCP servers

The security-critical component. **The schema has no field for a secret value.** Environment variables are split into plain values and credential requirements:

```yaml
mcp_servers:
  - name: github
    transport: stdio              # stdio | http | sse
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:                          # non-secret env only
      GITHUB_API_URL: https://api.github.com
    credentials:                  # secret env, values NEVER stored
      - env: GITHUB_TOKEN
        description: GitHub personal access token (repo scope)
        obtain_url: https://github.com/settings/tokens

  - name: supabase
    transport: http
    url: https://mcp.supabase.com/mcp
    headers:                      # non-secret headers only (mirrors env)
      X-Client-Info: agentpack
    credentials:
      - header: Authorization     # secret sent as a header
        format: "Bearer {value}"
        description: Supabase access token
        obtain_url: https://supabase.com/dashboard/account/tokens
```

Rules:

1. A conforming exporter MUST move any env var or header that matches secret heuristics (see [security.md](../security.md)) into `credentials`, discarding the value. `env:` and `headers:` hold what remains — non-secret values only.
2. A conforming installer MUST prompt for each credential (or read it from the local environment/keychain) and store it only in local tool config or OS secret storage — never back into the pack.
3. `credentials[].env` / `credentials[].header` names the injection point; `description` and `obtain_url` tell the installer what to get and where.

### Agents

```yaml
agents:
  - name: db-migrator
    source:
      bundled: agents/db-migrator.md
    targets: [claude-code]
```

### Rules

Instruction/rule files (CLAUDE.md, AGENTS.md, GEMINI.md, `.cursor/rules/*.mdc`). agentpack treats a rule as content plus per-tool placement; one logical rule can map to multiple tools:

```yaml
rules:
  - name: engineering-conventions
    source:
      bundled: rules/conventions.md
    scope: project
    render:                        # how each target consumes it
      claude-code: CLAUDE.md
      codex: AGENTS.md
      gemini-cli: GEMINI.md
      cursor: .cursor/rules/conventions.mdc
```

### Commands

Reusable prompts / slash commands:

```yaml
commands:
  - name: review
    source:
      bundled: prompts/review.md
    targets: [claude-code, codex]
```

### Settings

Non-secret, portable tool settings (permissions allowlists, model preferences, hooks). Stored as a per-tool document; adapters decide what is safe and meaningful to port:

```yaml
settings:
  - name: claude-permissions
    targets: [claude-code]
    values:
      permissions:
        allow:
          - "Bash(npm run test:*)"
          - "Bash(gh pr view:*)"
```

Machine-specific values (absolute paths, hostnames) SHOULD be excluded by exporters or parameterized as `{{home}}` / `{{project}}` template variables.

## Lockfile (restore record)

Restore writes `agentpack.lock.json` **locally** (not part of the published pack) recording what was applied where, at which version, for clean re-apply/uninstall/upgrade. Schema TBD in v0.2.

## Validation

`agentpack validate <dir>` checks a pack against this spec: schema validity, unique names, source resolvability, bundled paths existing, and — always — a secret scan over the entire pack directory. A pack that fails the secret scan MUST NOT be published.

## Open questions (to resolve before v1.0)

- Version pinning of referenced sources (npm semver ranges vs exact pins vs lockfile-only).
- Pack composition (`extends:` another pack) — deferred, likely post-v1.
- Signing/integrity for registry distribution (sigstore?) — deferred until the registry exists.
- Windows path/template semantics for project-scoped components.
