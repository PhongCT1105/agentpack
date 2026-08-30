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

**Notes:** `config.toml` is TOML, mixed settings + MCP; adapter merges `[mcp_servers.*]` tables surgically. Skills/agents have no Codex-native equivalent yet — pack components targeting only Codex skip them with a warning.

**Remote-auth keys (confirmed against OpenAI's Codex MCP docs while building `Plan()`):** `url`, `bearer_token_env_var`, `http_headers`, `env_http_headers`, and the stdio pair `env` (literal values) / `env_vars` (names forwarded from Codex's own environment). The docs are explicit that you should never hardcode or interpolate environment variables, which is why the adapter prefers indirection everywhere it exists. Two residual unknowns: `env_vars` is newer than `env`, so an older Codex may ignore it, and it is unconfirmed whether Codex rejects unknown keys.

**Project-scoped MCP — matrix says `—`, current docs disagree.** Codex docs describe per-project MCP scoping via `.codex/config.toml` for trusted projects. This row is unverified against a running binary, so the adapter follows the matrix and *skips* project-scoped servers with a warning rather than writing an unverified path or silently promoting them to global. Tracked as P3.17.

## Cursor (`cursor`)

| Component | Global scope | Project scope |
|---|---|---|
| MCP servers | `~/.cursor/mcp.json` | `.cursor/mcp.json` |
| Rules / instructions | User Rules live in app-internal storage (settings DB) — **not portable in v1**; documented limitation | `.cursor/rules/*.mdc` (frontmatter: `description`, `globs`, `alwaysApply`); legacy `.cursorrules` (read, but export to `.mdc`) |
| Commands / prompts | — | `.cursor/commands/*.md` *(verify)* |

| Skills | `~/.cursor/skills/<name>/SKILL.md` | `.cursor/skills/<name>/SKILL.md` |

**Never port:** app-internal auth/session storage.

**Notes:** Cursor is the clearest case for the `render:` mapping in the manifest — a neutral rule renders to `.mdc` with generated frontmatter.

**Confirmed while building the adapter:**

- **`globs:` is not valid YAML as Cursor writes it.** Cursor's own rule editor emits `globs: **/*.ts,*.tsx` unquoted, which a strict YAML parser reads as an alias and rejects — so a naive parser reports nearly every auto-attached rule as malformed. The adapter pre-quotes that line and accepts the list, bare-comma, single-unquoted, quoted and CRLF forms.
- **Detect needs two signals.** The `cursor` shell command is opt-in on macOS, so a configured machine often has no binary on PATH; `~/.cursor` existing is the other half.
- Remote MCP entries can carry an OAuth `auth` block (`CLIENT_ID`, `CLIENT_SECRET`, `scopes`). Not modeled yet, and a real credential-injection point for `Plan()`.
- `.cursor/commands/*.md` is confirmed for **project** scope. Cursor's UI also refers to a global command library, but no doc names its path, so the global side is unimplemented and surfaces as an unmodeled-entry warning rather than vanishing.
- Cursor supports rule **folders** and nested `.cursor/rules` in subdirectories; the adapter models the flat top level and warns about the rest.
- Cursor also reads root and nested **`AGENTS.md`**, the same file the Codex adapter models. The Cursor adapter deliberately does *not* scan it — two adapters modeling one file would bundle the same content twice. Deduplicating a shared rule into one component with a multi-tool `render:` map is P3.14.
- `.cursorrules` is legacy: current Cursor docs no longer mention it. Still read, with a warning pointing at `.cursor/rules/*.mdc`.

## Gemini CLI (`gemini-cli`)

| Component | Global scope | Project scope |
|---|---|---|
| MCP servers | `~/.gemini/settings.json` → `mcpServers` | `.gemini/settings.json` → `mcpServers` |
| Rules / instructions | `~/.gemini/GEMINI.md` | `GEMINI.md` at repo root |
| Extensions | `~/.gemini/extensions/<name>/gemini-extension.json` — inventoried as a warning, **not modeled** | — |
| Settings | `~/.gemini/settings.json` | `.gemini/settings.json` |

**Notes (confirmed while building the adapter):**

- Gemini has no `type` field; transport is inferred from which key is present — `command` → stdio, **`httpUrl` → streamable HTTP, `url` → SSE**. The neutral model has a single `URL` field, so the adapter records which key it came from.
- `settings.json` ships in **two layouts**, both seen in the wild: flat (`theme`, `contextFileName`, …) and grouped into sections (`general`, `ui`, `tools`, `context`, `security`, `ide`). Both are handled.
- Extensions can bundle their own `mcpServers`. They are deliberately **not** flattened into MCP components: doing so would make `save` publish them as though the user had configured them. Each is surfaced as a warning naming it, its version, and its server count. Modeling them properly needs an extension kind in the neutral model and the manifest spec — neither exists yet.
- The portable-vs-state key lists were derived from documented settings across versions, not a live install; an unrecognized key is reported as unmodeled rather than carried, so drift fails safe.

**Never port:** `~/.gemini/oauth_creds.json`, `.env` files, caches.

## Cross-tool observations (drive the design)

1. **MCP server config is ~the same JSON shape everywhere** (`command`/`args`/`env` or `url`/`headers`) wrapped in different files/formats (JSON vs TOML) — which is what makes a neutral model workable.
2. **Rules are the same content with different filenames** (`CLAUDE.md` / `AGENTS.md` / `GEMINI.md` / `.mdc`) — hence `render:` in the spec.
3. **Mixed files are the norm** (`~/.claude.json`, `config.toml`, `settings.json` all blend portable config with app state) — hence surgical merge, never whole-file replace.
4. **Secrets sit inline in env blocks today** in every tool — confirming redaction-on-export as the critical path.
5. Real machines accumulate `settings.json.bak*`-style debris and broken MCP entries (expired keys) — `scan` should flag dead servers and ignore backup files.

## Deferred tools (post-v1 candidates)

Windsurf, VS Code Copilot (`.github/copilot-instructions.md`, MCP in VS Code settings), Zed, OpenCode, Amp, Goose. Each is "add an adapter + a section here."
