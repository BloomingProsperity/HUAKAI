#!/usr/bin/env bash
# Worker auto-loop — run on EACH machine (1 local + 2 servers). Polls the shared
# ledger; when this machine ($COORD_AGENT) has assigned/bounced work, it invokes
# the local AI to do one task per the worker protocol in DISPATCH.md, then loops.
#
# Setup (per machine):
#   export COORD_URL="https://45.8.114.249:8443"
#   export COORD_TOKEN="<shared token>"
#   export COORD_CACERT="/path/to/coord-server.crt"
#   export COORD_AGENT="local-codex"   # unique per machine
#   # the command that runs YOUR local AI once, headless, on a prompt via stdin:
#   # local Windows codex MUST use gpt-5.5 + xhigh and NO fast mode (Owner rule):
#   export WORKER_AI_CMD="codex exec -m gpt-5.5 -c model_reasoning_effort=xhigh -"
#   # (other machines: claude -p, gemini, etc. — keep the high-reasoning tier)
# Then:  bash .coordination/worker-loop.sh
#
# The AI is handed the worker protocol + the current 'mine' board each cycle and
# is expected to do ONE task (start -> work -> codex self-review -> review), then
# this script loops. High-risk landing still parks at needs_owner for the Owner.
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
: "${COORD_AGENT:?set COORD_AGENT (unique per machine)}"
: "${COORD_URL:?set COORD_URL}"
: "${WORKER_AI_CMD:?set WORKER_AI_CMD to your headless AI invocation (reads prompt on stdin)}"
POLL="${WORKER_POLL_SECONDS:-60}"

prompt() {
  cat <<EOF
You are the HUAKAI worker AI "$COORD_AGENT" on this machine. Repo: $REPO.
Follow .coordination/DISPATCH.md (Worker protocol) and the repo CLAUDE.md / AGENTS.md exactly.
Do ONE task now:
1) read your board below; pick the highest-priority task assigned to you (prefer bounced ones).
2) read its spec_refs docs first; bash .coordination/task.sh start <id> (claims files).
3) implement; per commit run: codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh ; commit only with no S0/S1; discriminating tests (§14); clean-room (§11/§12).
4) when its acceptance is met: bash .coordination/task.sh review <id> "<commit + self-review result>". Do NOT mark done.
If blocked: bash .coordination/task.sh block <id> "<reason>". Never edit a file another agent holds.
Never stall waiting on the Owner: if something needs Owner sign-off (high-risk merge, missing info), bash .coordination/task.sh park <id> "<question>" and move on to the next task; do not wait more than 2 minutes.

Your current board:
$(bash "$DIR/task.sh" mine 2>&1)
EOF
}

echo "worker-loop: agent=$COORD_AGENT poll=${POLL}s  (Ctrl-C to stop)"
while true; do
  mine="$(bash "$DIR/task.sh" mine 2>&1 || true)"
  if echo "$mine" | grep -Eq '\[(assigned|in_progress)\b'; then
    echo "[$(date '+%H:%M:%S')] work available -> invoking $WORKER_AI_CMD"
    # Keep this machine's file lock alive while the AI works (edits can exceed
    # COORD_TTL); the heartbeat loop is killed as soon as the AI returns.
    ( while sleep 300; do python3 "$DIR/_coord.py" heartbeat "$COORD_AGENT" >/dev/null 2>&1; done ) &
    HB=$!
    prompt | (cd "$REPO" && eval "$WORKER_AI_CMD") || echo "  (AI run returned non-zero; will retry next cycle)"
    kill "$HB" 2>/dev/null || true
  else
    echo "[$(date '+%H:%M:%S')] no work for $COORD_AGENT; sleeping ${POLL}s"
  fi
  sleep "$POLL"
done
