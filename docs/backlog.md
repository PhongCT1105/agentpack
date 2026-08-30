# Backlog

Granular tasks, ordered within each phase. Written so that an autonomous agent (or a human) can pick up exactly one task, complete it, and commit — see conventions below. Phases are defined in [roadmap.md](roadmap.md).

## Conventions for anyone (human or agent) working this backlog

1. **One task = one commit** (or one small PR). Commit message: `P<phase>.<task>: <summary>` (e.g. `P1.3: claude-code adapter Detect()`).
2. **TDD**: write the failing test first; fixtures live in `testdata/`, sanitized, with *fake* secrets only (use the seeded formats from docs/security.md).
3. **Do not reorder within a phase** without recording why in the commit body. Tasks marked `[indep]` may run in parallel with their neighbors.
4. **Every task ends green**: `go build ./... && go vet ./... && go test ./...` must pass before commit.
5. If a task reveals the spec/architecture is wrong, **update the doc in the same commit** and note the change in the commit body.
6. Mark completed tasks here with `[x]` in the same commit.

## Phase 1 — scan

- [x] P1.1 `go mod init github.com/PhongCT1105/agentpack`; cobra skeleton with `version` command; CI workflow (build+vet+test on macOS/Linux/Windows)
- [x] P1.2 `internal/model`: Component kinds, Scope, Inventory, Warning types + unit tests
- [x] P1.3 claude-code adapter: `Detect()` (+fixtures) `[indep]`
- [x] P1.4 claude-code adapter: `Scan` skills (global+project) from fixture trees
- [x] P1.5 claude-code adapter: `Scan` MCP servers (`~/.claude.json` user scope, `.mcp.json` project scope) — surgical read, mixed-file fixture
- [x] P1.6 claude-code adapter: `Scan` agents, commands, rules (CLAUDE.md), settings
- [x] P1.7 codex adapter: `Detect()` + `Scan` MCP servers from `config.toml` `[indep]`
- [x] P1.8 codex adapter: `Scan` AGENTS.md (global+project) + prompts
- [x] P1.9 `agentpack scan` command: table output grouped by tool/kind/scope; `--json`
- [ ] P1.10 scan warnings: dead MCP servers (missing command), backup-file debris ignored, unknown keys surfaced
- [ ] P1.11 README: replace "planned" scan section with real usage + sample output

## Phase 2 — save + validate

- [ ] P2.1 `internal/packio`: manifest Go types matching spec v0.1; YAML marshal/unmarshal round-trip tests
- [ ] P2.2 `internal/secrets`: redactor (key patterns, value formats, entropy) + exhaustive table tests
- [ ] P2.3 `internal/secrets`: whole-pack scanner; adversarial fixture suite (release-blocking test tag)
- [ ] P2.4 inventory → pack conversion incl. env/credentials split and bundled-content copying
- [ ] P2.5 `agentpack save` command: component selection (`--all` first, interactive later), writes pack dir
- [ ] P2.6 `agentpack validate` command: schema + name uniqueness + bundled paths + secret scan; nonzero exit for CI
- [ ] P2.7 uncertain-secret prompt flow (`SUPABASE_URL` problem) with redact-by-default
- [ ] P2.8 docs: pack-authoring guide (`docs/guides/authoring.md`)

## Phase 3 — restore

- [ ] P3.1 pack reader + `restore` plan rendering (no apply yet): full contents, credentials, external services
- [ ] P3.2 `internal/engine`: executor — file ops, backups to `~/.agentpack/backups/<ts>/`, rollback on failure
- [ ] P3.3 credential resolver: env → keychain (go-keyring) → prompt; env-expansion injection where supported
- [ ] P3.4 claude-code adapter `Plan()`: skills, MCP (merge into `.mcp.json`/`~/.claude.json`), agents, commands, rules, settings
- [ ] P3.5 codex adapter `Plan()`: MCP into `config.toml` tables, AGENTS.md, prompts
- [ ] P3.6 cursor adapter: Detect/Scan/Plan (mcp.json, `.cursor/rules/*.mdc` with `render:`) `[indep]`
- [ ] P3.7 gemini-cli adapter: Detect/Scan/Plan (settings.json, GEMINI.md) `[indep]`
- [ ] P3.8 lockfile write/read; re-apply idempotence test
- [ ] P3.9 round-trip integration test: scan→save→restore→re-scan equality (CI, all 3 OSes)
- [ ] P3.10 `agentpack diff`

## Phase 4 — sharing

- [ ] P4.1 `restore <git-url>` (clone to cache, then normal restore)
- [ ] P4.2 pack README generation from manifest (`agentpack save --readme`)
- [ ] P4.3 flagship example pack #1 (separate repo) + link from README
- [ ] P4.4 flagship example pack #2 (separate repo)

## Phase 5 — distribution

- [ ] P5.1 goreleaser config: binaries + brew tap + curl installer + scoop
- [ ] P5.2 versioning policy + CHANGELOG automation
- [ ] P5.3 adapter-author guide (`docs/guides/new-adapter.md`)
- [ ] P5.4 docs site (simple; README-first remains the rule)

Phase 6 (registry) is intentionally not broken down yet.
