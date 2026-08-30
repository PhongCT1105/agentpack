# Authoring a pack

A pack is a plain directory with a manifest — designed to live in a Git
repository, be read by humans, and be restored by `agentpack`. This guide
covers creating one, by export or by hand, and keeping it valid and
secrets-free. The normative format reference is the
[pack manifest spec](../spec/pack-manifest.md).

## The fast path: export your machine

```bash
agentpack save --all my-setup
```

`save` scans every installed tool, converts the inventory into a pack, and
writes it to `my-setup/`. Along the way:

- **Secret values are redacted.** Env vars and headers whose key, value
  format, or entropy marks them as secret become `credentials` entries —
  the pack records *what* is needed (`GITHUB_TOKEN`), never the value.
  The redaction summary shows every decision. Exported credentials get a
  generated description; add `obtain_url` and a better `description` by
  hand before publishing — they are what make the pack installable.
- **Uncertain values redact by default.** A value like a Supabase project
  URL is neither clearly secret nor clearly safe. Rerun with
  `--review-uncertain` to decide each one interactively; the default
  answer (and every EOF or empty answer) still redacts.
- **Personal files are never exported.** `CLAUDE.local.md` and
  `settings.local.json` are your private overlay, not pack content.
- **The whole pack is scanned after writing.** Any suspected secret —
  including one pasted inside a bundled skill or prompt — blocks the save
  and removes the written directory. Fix the *source* file it points at
  and rerun.

A word on bundling: `save` copies authored content (skills, agents,
prompts, rules) into the pack. Bundled source, docs, and test fixtures are
where KEY=value and high-entropy shapes show up without being real
config — a JSX prop like `key={item.userId}`, a `password=...` line in a
prose example, seed data under a `testdata/` directory. The scanner
distinguishes these: a known credential format (`ghp_...`, a PEM block, a
JWT, and the like) always blocks no matter where it appears, but an
assignment- or entropy-shaped match in source, docs, or a test-fixture
path is *reviewable* rather than an unconditional dead end. `save`'s
output marks reviewable findings separately from the always-blocking
kind; after reading the file and confirming it is not a real secret,
rerun with `--allow-finding <path>:<line>` (repeatable; a path ending in
`/` waives a whole directory) to waive it. Waived findings are written to
`.agentpack-allow` in the pack, so commit that file and a later `validate`
— including in CI — honors it without repeating the flag.

## The deliberate path: write it by hand

A minimal pack is two files:

```
my-setup/
├── agentpack.yaml
└── rules/
    └── conventions.md
```

```yaml
apiVersion: agentpack/v0
kind: Pack
metadata:
  name: my-setup            # required: lowercase, digits, inner hyphens
  description: One-line summary for humans reading the pack.
targets: [claude-code, codex]
components:
  rules:
    - name: conventions
      source:
        bundled: rules/conventions.md
      scope: project
      render:               # the filename each tool consumes
        claude-code: CLAUDE.md
        codex: AGENTS.md
```

Run `agentpack validate my-setup` early and often — it checks everything
this guide describes and exits nonzero on any problem, so wire it into the
pack repository's CI:

```yaml
# .github/workflows/validate.yml (in your pack repo)
- run: go install github.com/PhongCT1105/agentpack/cmd/agentpack@latest
- run: agentpack validate .
```

## Components, briefly

Every component shares the same header fields:

```yaml
- name: unique-within-kind   # required
  description: one line      # optional
  scope: global | project    # default: global
  targets: [claude-code]     # optional override of pack-level targets
  optional: true             # installer may skip; default false
```

**Sources.** Skills, agents, commands, and rules need exactly one source:

```yaml
source:
  bundled: skills/my-skill        # path inside the pack (author-owned content)
# or
source:
  plugin: superpowers@claude-plugins-official
# or
source:
  npm: "skills"
  ref: vercel-labs/find-skills    # ref is only valid alongside npm
```

Prefer referencing over bundling for anything you did not write yourself.
Bundled paths are slash-separated, must stay inside the pack directory,
and must match their kind: a skill bundles a *directory* (containing
`SKILL.md`), everything else bundles a *file*. Conventional layout:
`skills/<name>/`, `agents/<name>.md`, `prompts/<name>.md`,
`rules/<name>.md`.

**MCP servers** are declared inline, and the schema is the security
boundary — there is no field that can hold a secret value:

```yaml
mcp_servers:
  - name: github
    transport: stdio            # stdio | http | sse
    command: npx                # required for stdio
    args: ["-y", "@modelcontextprotocol/server-github"]
    env:                        # non-secret values only
      GITHUB_API_URL: https://api.github.com
    credentials:                # what the installer must collect
      - env: GITHUB_TOKEN
        description: GitHub personal access token (repo scope)
        obtain_url: https://github.com/settings/tokens

  - name: supabase
    transport: http
    url: https://mcp.supabase.com/mcp   # required for http/sse
    headers:                    # non-secret headers only
      X-Client-Info: my-setup
    credentials:
      - header: Authorization
        format: "Bearer {value}"        # how the header is rebuilt on restore
        description: Supabase access token
        obtain_url: https://supabase.com/dashboard/account/tokens
```

Each credential names exactly one injection point (`env:` or `header:`);
`format` is only meaningful with `header`. Good `description` and
`obtain_url` values are what make a pack installable by a stranger.

**Settings** carry non-secret, portable tool preferences as a free-form
document (`values:`); adapters decide what applies. Avoid machine-specific
values (absolute paths, hostnames).

## The rules validate enforces

`agentpack validate <dir>` fails on any of:

- manifest missing, unknown fields, unknown `apiVersion`/`kind` (typos
  fail loudly rather than silently dropping config)
- invalid `metadata.name`; unknown tools in any `targets` or `render`;
  an unknown `scope` on any component
- duplicate or missing component names within a kind
- source problems: zero or multiple sources, `ref` without `npm`
- path problems in `bundled` and `render` values: escaping the pack,
  backslashes or absolute paths, missing bundled files, a skill that is
  not a directory (or vice versa)
- MCP shape problems: unknown transport, `stdio` without `command`,
  `http`/`sse` without `url`, malformed credentials
- **any symlink in the pack** — symlinked content cannot be secret-scanned
  but would be dereferenced by archives and restores, so it is banned
- **any suspected secret in any file**, bundled content included

The secret scan always runs, even when the manifest is broken. Excerpts in
the output are masked, so re-run `validate` after any fix to check it.

A finding from a known credential format always blocks — remove or
rephrase the offending content, there is no way around it. A finding from
the assignment or entropy channel in bundled source, docs, or a
test-fixture path is reviewable: after confirming it is not a real
secret, either commit a `.agentpack-allow` entry (`<path>[:<line>]` per
line, `#` comments allowed, a trailing `/` on the path waives a whole
directory) or pass `--allow-finding` for a one-off local check. CI should
rely on the committed file, not the flag, so the waiver is reviewable in
the same PR as the content it covers.

## Publishing

A pack is just files: push the directory to a Git repository. Keep
`agentpack validate` in that repository's CI so every commit re-verifies
the no-secrets guarantee. Treat a pack that ever contained a real secret
like any credential leak — rotate the credential; history retains it.
