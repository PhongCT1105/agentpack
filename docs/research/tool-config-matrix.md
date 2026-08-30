# Tool Config Matrix

Where each supported tool stores each kind of component. **This file is the source of truth adapters are built against.** Corrections are among the most valuable contributions to this project — these layouts change as tools evolve; entries marked *(verify)* need confirmation against current tool versions before the adapter ships.

Paths are macOS/Linux; Windows equivalents to be added per adapter (`%USERPROFILE%`, `%APPDATA%`).

## Claude Code (`claude-code`)

| Component | Global scope | Project scope |
|---|---|---|
| Skills | `~/.claude/skills/<name>/SKILL.md` | `.claude/skills/<name>/SKILL.md` |
| MCP servers | `~/.claude.json` → `mcpServers` (user scope); per-project `mcpServers` entries inside `~/.claude.json` (local scope) | `.mcp.json` at repo root (project scope, shareable) |
| Agents | `~/.claude/agents/*.md` | `.claude/agents/*.md` |
| Rules / instructions | `~/.claude/CLAUDE.md` | `CLAUDE.md` at repo root; `CLAUDE.local.md` (personal, gitignored) |
| Commands / prompts | `~/.claude/commands/*.md` | `.claude/commands/*.md` |
| Settings (permissions, hooks, env, model) | `~/.claude/settings.json` | `.claude/settings.json` (shared) and `.claude/settings.local.json` (personal) |
| Plugins | `~/.claude/plugins/` (marketplaces + installed plugin cache; installed set recorded in plugin config) *(verify exact install-state file)* | — |

**Never port:** OAuth/session state inside `~/.claude.json`, `~/.claude/.credentials.json` / OS keychain entries, `history.jsonl`, `projects/`, `sessions/`, caches.

**Notes:** `.mcp.json` supports `${VAR}` env expansion — the preferred credential-injection point on restore. `~/.claude.json` is a mixed file (MCP config + app state + per-project state); the adapter must surgically read/merge only `mcpServers` keys, never rewrite the whole file.

## Codex CLI (`codex`)

| Component | Global scope | Project scope |
|---|---|---|
| MCP servers | `~/.codex/config.toml` → `[mcp_servers.<name>]` tables | — *(verify: project-level MCP support)* |
| Rules / instructions | `~/.codex/AGENTS.md` | `AGENTS.md` at repo root (nested `AGENTS.md` files also read) |
| Commands / prompts | `~/.codex/prompts/*.md` | — |
| Settings (model, approval policy, profiles) | `~/.codex/config.toml` | — |

**Never port:** `~/.codex/auth.json` (and backups of it), caches, session state.

**Notes:** `config.toml` is TOML, mixed settings + MCP; adapter merges `[mcp_servers.*]` tables surgically. Skills/agents have no Codex-native equivalent yet — pack components targeting only Codex skip them with a warning. Remote-server entries may also carry `bearer_token_env_var` / `http_headers` keys *(verify against current Codex)* — the scanner ignores them today; the `Plan()` work (P3.5) should model them as credential injection points.

## Cursor (`cursor`)

| Component | Global scope | Project scope |
|---|---|---|
| MCP servers | `~/.cursor/mcp.json` | `.cursor/mcp.json` |
| Rules / instructions | User Rules live in app-internal storage (settings DB) — **not portable in v1**; documented limitation | `.cursor/rules/*.mdc` (frontmatter: `description`, `globs`, `alwaysApply`); legacy `.cursorrules` (read, but export to `.mdc`) |
| Commands / prompts | — | `.cursor/commands/*.md` *(verify)* |

**Never port:** app-internal auth/session storage.

**Notes:** Cursor is the clearest case for the `render:` mapping in the manifest — a neutral rule renders to `.mdc` with generated frontmatter.

## Gemini CLI (`gemini-cli`)

| Component | Global scope | Project scope |
|---|---|---|
| MCP servers | `~/.gemini/settings.json` → `mcpServers` | `.gemini/settings.json` → `mcpServers` |
| Rules / instructions | `~/.gemini/GEMINI.md` | `GEMINI.md` at repo root |
| Extensions | `~/.gemini/extensions/` *(verify structure)* | — |
| Settings | `~/.gemini/settings.json` | `.gemini/settings.json` |

**Never port:** `~/.gemini/oauth_creds.json`, `.env` files, caches.

## Cross-tool observations (drive the design)

1. **MCP server config is ~the same JSON shape everywhere** (`command`/`args`/`env` or `url`/`headers`) wrapped in different files/formats (JSON vs TOML) — which is what makes a neutral model workable.
2. **Rules are the same content with different filenames** (`CLAUDE.md` / `AGENTS.md` / `GEMINI.md` / `.mdc`) — hence `render:` in the spec.
3. **Mixed files are the norm** (`~/.claude.json`, `config.toml`, `settings.json` all blend portable config with app state) — hence surgical merge, never whole-file replace.
4. **Secrets sit inline in env blocks today** in every tool — confirming redaction-on-export as the critical path.
5. Real machines accumulate `settings.json.bak*`-style debris and broken MCP entries (expired keys) — `scan` should flag dead servers and ignore backup files.

## Deferred tools (post-v1 candidates)

Windsurf, VS Code Copilot (`.github/copilot-instructions.md`, MCP in VS Code settings), Zed, OpenCode, Amp, Goose. Each is "add an adapter + a section here."
