# Security Model

Security is a founding constraint of agentpack, not a feature: the product's core promise is *"share your setup without sharing your secrets."*

## Threat model

| # | Threat | Mitigation |
|---|---|---|
| 1 | **Publisher secret leakage** — a saved/published pack contains the author's API keys, tokens, or credentials | Schema-level exclusion + redaction + scanning (below) |
| 2 | **Malicious packs** — a pack installs an MCP server or skill that exfiltrates data or runs hostile code on the installer's machine | Inspection-first UX, plan/confirm, provenance display (below) |
| 3 | **Local config damage** — restore corrupts or clobbers a working setup | Plan/apply split, merge-not-clobber, automatic backups, dry-run |
| 4 | **Credential mishandling on restore** — collected secrets end up somewhere they shouldn't | Secrets written only to local tool config / OS keychain; never to pack, lockfile, or logs |

## Threat 1: publisher secret leakage (the critical one)

Three independent layers, all mandatory:

### Layer 1 — schema exclusion (structural)

The pack manifest **has no field that can hold a secret value**. MCP env vars and headers are split: non-secret values go in `env:`, secrets become `credentials:` entries carrying only the *name* of what's required, a description, and where to obtain it. There is no place to put a token even on purpose.

### Layer 2 — redaction on export (heuristic)

During `save`, every value from scanned config passes the redactor. A value is treated as secret when **any** of:

- its key matches secret-name patterns, evaluated on name *segments* (split on `_`/`-`/camelCase, case-insensitive): `key`/`apikey` must equal a whole segment, while `token`, `secret`, `password`, `passwd`, `passphrase`, `credential`, `authorization` may also end one (`clientSecret`, `api_token`) — so `keybindings`, `keymap`, `hotkey`, `monkey`, `tokenizer` cannot false-positive
- its value matches known credential formats: `ghp_/gho_/github_pat_`, `sk-`, `xoxb-`, AWS `AKIA…`, JWTs, PEM blocks, connection strings with passwords
- its value exceeds an entropy threshold for its length (catches random API keys with unhelpful names)

Redacted values become `credentials` requirements. Uncertain cases are surfaced to the user during save: *"SUPABASE_URL — keep as plain env, or mark as credential?"* Defaults favor redaction.

### Layer 2.5 — bundling hygiene (what never enters a pack)

Redaction cleans *config values*. It says nothing about *files copied wholesale* into a pack, and that turned out to be the larger hole: bundling one real third-party skill copied 1.1 GB — including 726 MB of `node_modules`, a nested `.git`, and the skill's own test suite.

Two excluded categories are security fixes, not hygiene:

- **`.git/`** — `config` can hold a remote URL with an embedded token (`https://user:token@host/repo`), and the object store holds everything ever committed.
- **dotenv and credential files** — `.env`, `.env.local` and friends *are* credential files, as are `.npmrc` (registry auth), `.netrc` and `.pypirc`. `.env.example` and `.env.sample` are kept: they document required variables without carrying values.

The rest are excluded because they are reinstallable, regenerable, or not part of the portable environment: vendored dependencies, build output, caches, and the component's own test suite and CI config. Beyond size, this keeps a pack **inspectable** — a human is expected to read a pack before installing it, which is impossible when it carries thousands of vendored files.

Exclusions are always reported as warnings, never silent, so what landed in the pack is never mistaken for a byte-for-byte copy of the machine.

### Layer 3 — whole-pack scan (defense in depth)

After the pack directory is written, an independent scanner (gitleaks-style rules) runs over every file — including bundled skills, rules, and prompts, where users paste secrets more often than anyone admits. `agentpack validate` runs the same scan, so CI on a pack repo re-verifies on every commit.

Not every finding carries the same confidence. A match against a known credential format (`ghp_…`, a PEM block, a JWT, an AWS key, and the like) is near-certain and **always blocks**, regardless of where it appears. A match from the weaker assignment or entropy heuristics is *reviewable* when it occurs in bundled source, docs, or a test-fixture path — these are exactly where `KEY=value` and high-entropy shapes turn up without being real config (a JSX prop, a prose example, seeded fixture data), and dogfooding found them dominating real-world `save` runs (docs/backlog.md P2.9). A reviewable finding is **reported and summarized by file, not fatal**. Blocking on them was tried and abandoned: after bundling hygiene removed the vendored noise, a single real machine still produced hundreds of them, and a gate that fails on every real setup is a gate people switch off — a strictly worse outcome than one that reports honestly. `--strict` promotes reviewable findings to blocking, which is what CI over a curated pack should use. A human who has inspected one can waive it permanently with a `.agentpack-allow` entry (`<path>[:<line>]`, committed and reviewed like any other content) or a one-off `--allow-finding` flag.

The allowlist can never waive a high-confidence format match; there is no flag or file entry that silences one. That is deliberate and it has a cost: a component whose *source* legitimately contains credential patterns — a secret-redaction library, for instance — cannot be bundled at all. The answer there is to fix the file or leave that component out of the pack, never to weaken the guarantee.

### Seeded fixtures

Adapter fixtures and redactor tests seed fake secrets in realistic formats (`ghp_…`, `sk-…`, `AKIA…`, JWTs) so detection is tested against real token shapes. Every seeded fake embeds the string `FAKE`; the repository's `.gitleaks.toml` allowlists exactly that marker, so any secret-shaped string *without* it is a genuine finding and blocks the commit.

### Residual risk

Heuristics cannot catch every secret (e.g. a password that looks like a word, pasted inside a bundled prompt). Documentation and the save-flow UI state clearly: *review your pack before publishing; validate runs in CI; treat a leaked pack like any credential leak — rotate.*

## Threat 2: malicious packs

Installing a pack can mean installing MCP servers and skills that **execute code with the installer's privileges**. agentpack cannot make arbitrary third-party code safe, and does not claim to. What it guarantees:

- **Nothing installs silently.** Restore always shows the complete contents first: every skill, MCP server (with its exact `command`/`url`), rule, and permission change, plus every external service contacted and credential requested.
- **Provenance is visible.** Referenced sources show their origin (npm package, git repo, marketplace id). Bundled content is shown as files the user can open.
- **Credentials are per-server and explicit.** A pack requesting `GITHUB_TOKEN` for an MCP server named `github` whose command is an unrelated npm package is visible as exactly that.
- **No elevation.** agentpack itself never requests sudo/admin and writes only to tool config locations and its own directories.

Registry-era protections (signing, verified publishers, scanning of referenced sources, community reporting) are deferred with the registry itself — see [roadmap.md](roadmap.md).

## Threat 3: local config damage

- Restore is **plan → confirm → apply**; `--dry-run` stops at the plan.
- Applies **merge** into existing config; conflicts are shown, and replace requires explicit choice.
- Every file touched is first copied to `~/.agentpack/backups/<timestamp>/`, and the executor supports rollback of a failed apply.

## Threat 4: credential mishandling on restore

Resolution order for each declared credential: existing env var → OS keychain (macOS Keychain / libsecret / Windows Credential Manager) → interactive prompt (no echo).

Rules:

- Secrets are written **only** where the target tool needs them, preferring env-var indirection (`${GITHUB_TOKEN}`) where the tool supports expansion, falling back to the tool's own config file (matching the security posture the user already has).
- Secrets never appear in the lockfile, logs, error messages, or telemetry (there is no telemetry).
- Prompted secrets are offered for storage in the OS keychain so the next restore on that machine is non-interactive.

## Reporting a vulnerability

Open a private report via GitHub Security Advisories on this repository rather than a public issue.
