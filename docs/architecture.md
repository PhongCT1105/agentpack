# Architecture

Status: draft, matches [pack-manifest spec v0.1](spec/pack-manifest.md).

## Shape

`agentpack` is a single Go binary. No daemon, no server, no runtime dependencies. v1 is entirely local; Git is the sharing transport.

```
┌────────────────────────────────────────────────────────┐
│                        CLI (cobra)                     │
│      scan · save · validate · restore · diff           │
├────────────────────────────────────────────────────────┤
│                     Core engine                        │
│   neutral component model · plan/apply · lockfile      │
├──────────────┬──────────────┬──────────────┬───────────┤
│ claude-code  │    codex     │    cursor    │ gemini-cli│   ← adapters
├──────────────┴──────────────┴──────────────┴───────────┤
│        secrets layer: redaction · scanning · prompt    │
└────────────────────────────────────────────────────────┘
```

## The neutral component model

The core never manipulates tool config directly. Everything is normalized into neutral types mirroring the manifest spec:

```go
type Component interface {
    Kind() Kind          // Skill | MCPServer | Agent | Rule | Command | Setting
    Name() string
    Scope() Scope        // Global | Project
}

type Inventory struct {   // result of a scan
    Tool       ToolID
    Components []Component
    Warnings   []Warning  // things we saw but could not model
}
```

`scan` produces `[]Inventory`; `save` converts inventories into a pack; `restore` converts a pack back into per-tool apply operations. The pack format is the wire format between the two directions.

## Adapters

One adapter per tool, implementing:

```go
type Adapter interface {
    ID() ToolID
    Detect() (installed bool, version string, err error)

    // Read tool config into the neutral model. Never writes.
    Scan(scope ScanScope) (Inventory, error)

    // Translate neutral components into concrete file operations.
    Plan(components []Component, opts PlanOpts) (Plan, error)
}
```

Key properties:

- **Scan is read-only.** No adapter writes during scan/save.
- **Plan/apply split.** Adapters return a `Plan` — a list of intended file operations (`create ~/.claude/skills/x`, `merge key into ~/.claude.json`) — which the engine renders for user confirmation, then executes with backups. Adapters never write files directly; only the engine's executor does, which centralizes backup, dry-run, and rollback.
- **Merge, don't clobber.** Applying a pack merges into existing config (e.g. adds entries to `mcpServers`) rather than replacing files, except where the user chooses replace mode. Every touched file is backed up to `~/.agentpack/backups/<timestamp>/` first.
- **Unknown content is preserved and reported.** Anything a scan doesn't understand becomes a `Warning` in the inventory, not silent data loss.

Where each adapter reads/writes is documented in [research/tool-config-matrix.md](research/tool-config-matrix.md) — that file is the source of truth adapters are built against.

## Secrets layer

Sits between adapters and the pack writer; described fully in [security.md](security.md).

- **On save**: every env var / header / config value passes through the redactor. Values matching secret heuristics become `credentials` requirements; the values are dropped. A whole-pack scanner then runs as a second line of defense and blocks save on findings.
- **On restore**: for each declared credential, resolve in order: existing local env var → OS keychain entry → interactive prompt. Resolved values are written only into local tool config (or referenced via env expansion where the tool supports it, e.g. `"env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}`), never into the pack or lockfile.

## Command surface (v1)

| Command | Behavior |
|---|---|
| `agentpack scan` | Detect installed tools, print unified inventory (table; `--json` for machines). Read-only. |
| `agentpack save <dir>` | Scan (or take `--from` a previous scan), select components (interactive or `--all`), write a pack directory. Runs redaction + secret scan. |
| `agentpack validate <dir>` | Schema + secret-scan a pack. CI-friendly. |
| `agentpack restore <ref>` | `<ref>` = local dir or git URL. Show pack contents + plan, prompt for credentials, confirm, apply with backups, write lockfile. `--dry-run` stops after the plan. |
| `agentpack diff <ref>` | Show what restore *would* change vs current machine state. |

Later (post-v1): `publish`, `search`, `update`, `uninstall`.

## Repository layout (planned)

```
agentpack/
├── cmd/agentpack/          # main + cobra commands
├── internal/
│   ├── model/              # neutral component model, manifest types
│   ├── engine/             # scan orchestration, plan/apply, lockfile, backups
│   ├── adapters/
│   │   ├── claudecode/
│   │   ├── codex/
│   │   ├── cursor/
│   │   └── gemini/
│   ├── secrets/            # redactor, scanner, credential resolver
│   └── packio/             # pack read/write, YAML schema, validation
├── docs/
└── testdata/               # sanitized fixture configs per tool (NO real secrets, ever)
```

## Testing strategy

- **Fixture-driven adapter tests**: each adapter has `testdata/` trees replicating real tool config layouts (sanitized). `Scan` fixtures → expected inventory golden files; `Plan` component sets → expected file-operation golden files.
- **Round-trip tests**: scan fixtures → save → restore into a temp dir → re-scan → inventories must match.
- **Secret-leak tests are release-blocking**: fixtures seeded with fake tokens (in recognizable formats: `ghp_…`, `sk-…`, AWS keys, JWTs, high-entropy strings) must never survive into a saved pack.
- Cross-platform CI (macOS, Linux, Windows) since path handling is core to the product.

## Design principles

1. **Secrets-free by construction** — the pack schema cannot hold a secret value; redaction is enforcement, not policy.
2. **Read-only until confirmed** — scan/save/validate never modify tool config; restore always plans first.
3. **Legible over magical** — every pack is inspectable; every apply shows its plan; warnings over silent drops.
4. **Adapters are the extension point** — supporting a new tool touches `internal/adapters/` and the config matrix doc, nothing else.
5. **YAGNI** — no registry, no sync, no signing until the local loop is excellent.
