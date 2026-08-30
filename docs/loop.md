# The Night Shift — autonomous overnight build loop

Status: active. This documents how agentpack builds itself overnight. The machinery lives in [`loop/`](../loop/).

## What it is

A local, unattended loop that works through [backlog.md](backlog.md) task by task: each iteration launches a **fresh headless Claude session** that picks the next task, implements it with TDD, has the diff reviewed by a subagent, and lands it as one pushed commit. Git is the only memory between iterations — checkboxes in the backlog, the commit history, and [loop-log.md](loop-log.md) are how one iteration hands off to the next.

```
preflight ─▶ ┌ iteration ──────────────────────────────┐
             │ fresh claude -p, cwd = repo             │─▶ commit pushed?  ─▶ streak reset, next iteration
             │ picks ≤3 [indep] tasks, subagents,      │─▶ nothing landed? ─▶ streak+1 (brake at 3)
             │ TDD ▸ review ▸ scan ▸ commit ▸ push     │─▶ backlog empty?  ─▶ final report, stop
             └─────────────────────────────────────────┘
```

## Design decisions

- **Venue: local headless driver** (`loop/overnight.sh`), wrapped in `caffeinate`. A crash loses nothing; re-running resumes from git state.
- **Topology: one worker session per iteration, multi-agent inside.** The worker lands tasks one at a time — each fully green, reviewed, committed, and pushed before the next begins — continuing until a phase boundary or until its context grows heavy, and may run up to 3 adjacent `[indep]` tasks as parallel implementer subagents (git worktrees for isolation), plus a reviewer subagent over every diff. Commits land serially from one process — no cross-process git races. (Validated in practice: the first worker session landed all 11 Phase 1 tasks this way.)
- **Fresh context every iteration.** No context rot at hour six; the runner prompt (`loop/runner-prompt.md`) is the entire worker contract.
- **Permissions: full autonomy, scoped.** Workers run with permissions bypassed, constrained by the contract: cwd pinned to the repo; no sudo, no global installs, no force-push, no writes outside the repo, no GitHub settings changes. Every push is preceded by a gitleaks scan — this project's own security rule applies to its builder.
- **Stop rules: failure brake only, no time limit** (owner's choice). The loop stops when: the backlog has no unchecked tasks, **3 consecutive iterations land nothing**, the **same task** is picked 3 times in a row (progress illusion guard), or the owner kills it. A per-iteration watchdog (default 2h) kills hung sessions. A **model usage limit stops the loop immediately** rather than burning the failure budget — every worker dies identically on it, so retrying is pointless; the report names the limit and suggests relaunching with `LOOP_CLAUDE_ARGS="--model <other>"`.
- **Blocked ≠ failed.** A worker that cannot complete a task marks it `[!] blocked: reason` in the backlog and commits that marker — the loop moves on instead of re-hitting the wall, and the morning human unblocks it.
- **Token posture: generous.** Thoroughness is explicitly preferred over economy: real implementations, real fixtures, reviewer pass every iteration.

## Success detection (objective)

The driver trusts git, not self-reports: an iteration succeeded only if `origin/main` advanced during it. The worker also emits a final `LOOP_RESULT: status=... landed=... note=...` line, parsed for the report and the same-task guard.

## Known constraint: workflow scope

The local gh token lacks the `workflow` scope, so pushes containing `.github/workflows/` are rejected. Contract rule: CI workflow files are staged in `docs/ci/` with a note in the loop log; a human moves them into place (one-time `gh auth refresh -s workflow`) later.

## Morning artifacts

- `loop/logs/<date>/REPORT.md` — landed / blocked / stop reason (local, gitignored)
- `loop/logs/<date>/iter-NN.log` — full transcript per iteration (local)
- [`docs/loop-log.md`](loop-log.md) — committed one-line-per-task journal
- The commit stream on GitHub — one reviewable commit per task

## Operating it

```bash
./loop/overnight.sh --once     # single iteration (validation)
./loop/overnight.sh            # run until backlog done or brake trips
tail -f loop/logs/<date>/iter-*.log   # watch live
```

Environment knobs: `ITER_TIMEOUT` (seconds, default 7200), `MAX_FAILURES` (default 3), `LOOP_CLAUDE_ARGS` (extra flags for the worker sessions, e.g. `--model`).

## Run history

- **2026-08-30, 03:11–04:38** — landed Phase 1 (P1.1–P1.11) in the validation run and all of Phase 2 (P2.1–P2.8) in one 80-minute iteration, 19 per-task commits. Stopped when the workers hit a Fable 5 usage limit; the in-flight P3.1 work was recovered, verified, and landed by hand. Two lessons fed back into the machinery: workers can land a whole phase per session (contract now says so), and usage limits must fast-fail (they now do).
