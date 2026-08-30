#!/usr/bin/env bash
# The Night Shift — autonomous overnight build loop for agentpack.
# Design: docs/loop.md   Worker contract: loop/runner-prompt.md
#
# Usage:
#   ./loop/overnight.sh          run until backlog done or failure brake trips
#   ./loop/overnight.sh --once   run exactly one iteration (validation)
#
# Env knobs:
#   ITER_TIMEOUT      seconds before a hung worker is killed   (default 7200)
#   MAX_FAILURES      consecutive no-commit iterations allowed (default 3)
#   LOOP_CLAUDE_ARGS  extra args for worker sessions (e.g. --model opus)
set -uo pipefail

# Keep the Mac awake for the whole run: re-exec under caffeinate once.
if [ -z "${LOOP_CAFFEINATED:-}" ] && command -v caffeinate >/dev/null 2>&1; then
  export LOOP_CAFFEINATED=1
  exec caffeinate -dimsu "$0" "$@"
fi

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO"

ONCE=false
[ "${1:-}" = "--once" ] && ONCE=true

ITER_TIMEOUT="${ITER_TIMEOUT:-7200}"
MAX_FAILURES="${MAX_FAILURES:-3}"
BACKLOG="docs/backlog.md"
RUN_DATE="$(date +%Y-%m-%d-%H%M)"
LOG_DIR="$REPO/loop/logs/$RUN_DATE"
REPORT="$LOG_DIR/REPORT.md"
mkdir -p "$LOG_DIR"

log() { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*" | tee -a "$LOG_DIR/driver.log"; }
report() { printf '%s\n' "$*" >> "$REPORT"; }

# ---------- preflight ----------------------------------------------------
fail_preflight() { log "PREFLIGHT FAILED: $*"; exit 1; }

command -v go >/dev/null || fail_preflight "go not installed"
command -v git >/dev/null || fail_preflight "git not installed"
command -v claude >/dev/null || fail_preflight "claude CLI not installed"
command -v gitleaks >/dev/null || log "WARN: gitleaks missing — workers fall back to pattern scan"
gh auth status >/dev/null 2>&1 || fail_preflight "gh not authenticated"
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || fail_preflight "not a git repo"
[ -f "$BACKLOG" ] || fail_preflight "$BACKLOG missing"
git fetch -q origin main || fail_preflight "cannot reach origin"

log "night shift starting in $REPO (once=$ONCE, iter_timeout=${ITER_TIMEOUT}s, max_failures=$MAX_FAILURES)"
{
  echo "# Night shift report — $RUN_DATE"
  echo ""
  echo "| iter | result | landed | note | duration |"
  echo "|---|---|---|---|---|"
} > "$REPORT"

# ---------- iteration machinery ------------------------------------------
run_worker() { # $1 = iteration log file
  local worker_log="$1"
  ( claude -p "$(cat "$REPO/loop/runner-prompt.md")" \
      --dangerously-skip-permissions \
      ${LOOP_CLAUDE_ARGS:-} \
      > "$worker_log" 2>&1 ) &
  local pid=$!
  local waited=0
  while kill -0 "$pid" 2>/dev/null; do
    sleep 15
    waited=$((waited + 15))
    if [ "$waited" -ge "$ITER_TIMEOUT" ]; then
      log "watchdog: killing hung worker (pid $pid) after ${waited}s"
      kill "$pid" 2>/dev/null; sleep 5; kill -9 "$pid" 2>/dev/null
      echo "LOOP_RESULT: status=timeout landed=none note=killed-by-watchdog" >> "$worker_log"
      return 1
    fi
  done
  wait "$pid"
}

iter=0
failures=0
last_task=""
same_task_count=0
stop_reason=""

while :; do
  iter=$((iter + 1))

  if ! grep -q '^- \[ \]' "$BACKLOG"; then
    stop_reason="backlog complete — no unchecked tasks remain"
    break
  fi

  start_origin="$(git rev-parse origin/main)"
  worker_log="$LOG_DIR/iter-$(printf '%02d' "$iter").log"
  next_task="$(grep -m1 '^- \[ \]' "$BACKLOG" | sed 's/^- \[ \] //' | cut -c1-60)"
  log "iteration $iter: next unchecked task: $next_task"
  t0=$(date +%s)

  run_worker "$worker_log"
  worker_rc=$?

  t1=$(date +%s)
  dur="$(( (t1 - t0) / 60 ))m"

  # objective success check: did origin/main advance?
  git fetch -q origin main 2>/dev/null
  end_origin="$(git rev-parse origin/main)"
  result_line="$(grep -a 'LOOP_RESULT:' "$worker_log" | tail -1 || true)"
  status="$(echo "$result_line" | sed -n 's/.*status=\([a-z_]*\).*/\1/p')"
  landed="$(echo "$result_line" | sed -n 's/.*landed=\([^ ]*\).*/\1/p')"
  note="$(echo "$result_line" | sed -n 's/.*note=\(.*\)/\1/p' | cut -c1-80)"

  if [ "$end_origin" != "$start_origin" ]; then
    failures=0
    log "iteration $iter LANDED: ${landed:-unknown} (${status:-ok}) in $dur"
    report "| $iter | ${status:-ok} | ${landed:-?} | ${note:-} | $dur |"
  else
    failures=$((failures + 1))
    log "iteration $iter landed NOTHING (rc=$worker_rc, status=${status:-none}) — failure streak $failures/$MAX_FAILURES"
    report "| $iter | ${status:-failed} | none | ${note:-see iter log} | $dur |"
  fi

  # progress-illusion guard: same task picked 3x in a row
  current_task="$(grep -m1 '^- \[ \]' "$BACKLOG" 2>/dev/null | sed 's/^- \[ \] //' | cut -c1-60 || true)"
  if [ -n "$current_task" ] && [ "$current_task" = "$last_task" ]; then
    same_task_count=$((same_task_count + 1))
  else
    same_task_count=0
  fi
  last_task="$current_task"

  [ "$status" = "backlog_done" ] && { stop_reason="worker reported backlog done"; break; }
  [ "$failures" -ge "$MAX_FAILURES" ] && { stop_reason="failure brake: $failures consecutive iterations landed nothing"; break; }
  [ "$same_task_count" -ge 3 ] && { stop_reason="progress-illusion brake: same task pending after 3 landing iterations"; break; }
  $ONCE && { stop_reason="--once mode"; break; }

  sleep 20
done

# ---------- morning report ------------------------------------------------
{
  echo ""
  echo "**Stopped:** $stop_reason"
  echo "**Iterations:** $iter"
  echo ""
  echo "## Remaining unchecked tasks"
  grep '^- \[ \]' "$BACKLOG" || echo "(none — backlog complete)"
  echo ""
  echo "## Blocked tasks"
  grep '^- \[!\]' "$BACKLOG" || echo "(none)"
} >> "$REPORT"

log "night shift stopped: $stop_reason"
log "report: $REPORT"
