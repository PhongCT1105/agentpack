# Roadmap

Phases ship in order; each has a crisp acceptance test. Granular, agent-executable tasks live in [backlog.md](backlog.md).

## Phase 0 — Foundation (this repo, now)

Vision, pack-manifest spec draft, architecture, security model, tool config matrix, contributor docs.

**Done when:** repo is public and a contributor can understand the format and the plan without talking to us.

## Phase 1 — `scan` (read-only inventory)

Go CLI skeleton; adapters for **claude-code** and **codex** implementing `Detect` + `Scan`; unified inventory output (table + `--json`).

**Done when:** on a machine with Claude Code and Codex configured, `agentpack scan` correctly lists skills, MCP servers, agents, rules, commands, and settings, split global/project, with zero writes to disk.

## Phase 2 — `save` + `validate` (pack export)

Neutral model → pack writer; secrets layer (redactor + whole-pack scanner); interactive component selection; `validate` for CI.

**Done when:** `agentpack save` on a real machine produces a pack that (a) passes `validate`, (b) contains **zero** secret values under adversarial fixtures (fake `ghp_…`, `sk-…`, AWS keys, JWTs seeded across configs *and* bundled files), and (c) declares the right `credentials` requirements.

## Phase 3 — `restore` (the loop closes)

Pack reader; plan/confirm/apply engine with backups and `--dry-run`; credential resolution (env → keychain → prompt); lockfile; cursor + gemini-cli adapters gain write support alongside claude-code and codex.

**Done when:** a pack saved on machine A restores on machine B (different OS), prompting only for credentials, and a re-scan of B matches the pack. Round-trip test green in CI on macOS + Linux + Windows.

## Phase 4 — Sharing via Git

`restore <git-url>`; pack-repo template (README generation from the manifest so a pack is self-describing on GitHub); `diff` command; polish for the two flagship example packs (e.g. *Full-Stack Startup Engineer*, *AI/ML Research Engineer*) maintained in separate repos as living demos.

**Done when:** a stranger can restore a pack from a GitHub URL in under 5 minutes, inspecting everything before applying.

## Phase 5 — Distribution & community

goreleaser: brew tap, curl installer, scoop; docs site; CONTRIBUTING deep-dive for adapter authors; announce.

**Done when:** `brew install agentpack` works and a third-party adapter PR has been merged.

## Phase 6 — Registry (the big bet, only after the loop is loved)

Hosted index of published packs: search, inspection UI, install counts, verified publishers, secret-scanning on publish, reporting. The registry serves discovery; the format stays open and Git remains a first-class transport.

**Explicitly deferred until Phases 1–5 prove demand.**

## Non-goals (for now)

- Sync daemon / background agents
- Managing tool *installation* itself (agentpack configures tools, it doesn't install Claude Code)
- Porting session history or conversational state
- Windows-only tools
