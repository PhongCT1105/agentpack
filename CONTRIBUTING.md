# Contributing to agentpack

Thanks for your interest! agentpack is early — docs-first, pre-alpha — which means contributions have outsized impact right now.

## Most valuable contributions today

1. **Corrections to the [tool config matrix](docs/research/tool-config-matrix.md).** Adapters are built against that file. If you know where a tool *actually* stores its skills/MCP/rules config (or that an entry marked *(verify)* is right or wrong), a small PR there is gold.
2. **Spec feedback.** Read the [pack manifest spec](docs/spec/pack-manifest.md) and open an issue for anything ambiguous, missing, or wrong — especially around the credentials model.
3. **New tool entries.** Use a tool we haven't covered (Windsurf, Zed, Copilot, Goose…)? Add a section to the config matrix; that's the first half of an adapter.
4. **Code**, once Phase 1 opens: pick an unclaimed task from [docs/backlog.md](docs/backlog.md), following its conventions.

## Ground rules

- **No real secrets, ever** — not in code, fixtures, tests, issues, or example packs. Test fixtures use obviously-fake tokens in realistic formats (`ghp_FAKEFAKE…`). This is enforced by CI secret-scanning; a PR that trips it will be closed with a rotation reminder.
- **One task per PR**, small and reviewable. Follow the commit conventions in [docs/backlog.md](docs/backlog.md).
- **Tests come with the change.** Adapter work is fixture-driven (see [docs/architecture.md](docs/architecture.md) → Testing strategy).
- **Docs move with code.** If your change makes a doc wrong, fix the doc in the same PR.

## Development setup (Phase 1+)

```bash
git clone https://github.com/PhongCT1105/agentpack
cd agentpack
go build ./... && go test ./...
```

Go 1.22+. No other dependencies.

## Questions / discussion

Open a GitHub issue. Design discussions happen in issues against the relevant doc, so decisions stay searchable.

## Security

Found a vulnerability (especially anything that could leak secrets into a pack)? Please use GitHub Security Advisories on this repo — see [docs/security.md](docs/security.md) — rather than a public issue.
