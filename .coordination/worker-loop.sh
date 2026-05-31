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
UPDATE_BRANCH="${COORD_UPDATE_BRANCH:-fix/hermes-phase-1-e33d940}"

# Auto-update coordination scripts from origin each cycle so future changes propagate
# without a manual restart. Surgically refreshes ONLY .coordination/ from origin/<landing>
# (never merges the worker's branch or touches its in-flight work), then re-execs itself
# if THIS script changed (syntax-checked first so a bad push can't kill the loop).
# Disable with COORD_AUTO_UPDATE=0. Best-effort: any failure leaves the running loop intact.
self_update(){
  [ "${COORD_AUTO_UPDATE:-1}" = "1" ] || return 0
  git -C "$REPO" fetch -q origin "$UPDATE_BRANCH" 2>/dev/null || return 0
  local before after
  before="$(sha1sum "$DIR/worker-loop.sh" 2>/dev/null | cut -d' ' -f1)"
  git -C "$REPO" restore --source="origin/$UPDATE_BRANCH" --worktree -- .coordination/ 2>/dev/null \
    || git -C "$REPO" checkout -q "origin/$UPDATE_BRANCH" -- .coordination/ 2>/dev/null || return 0
  after="$(sha1sum "$DIR/worker-loop.sh" 2>/dev/null | cut -d' ' -f1)"
  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    if bash -n "$DIR/worker-loop.sh" 2>/dev/null; then
      echo "[$(date '+%H:%M:%S')] worker-loop.sh updated from origin -> re-exec new version"
      exec bash "$DIR/worker-loop.sh"
    else
      echo "[$(date '+%H:%M:%S')] updated worker-loop.sh has a syntax error; keeping current version"
    fi
  fi
}

prompt() {
  cat <<EOF
You are the HUAKAI worker AI "$COORD_AGENT" on this machine. Repo: $REPO.
Follow .coordination/DISPATCH.md (Worker protocol) and the repo CLAUDE.md / AGENTS.md exactly.
Do ONE task now:
1) read your board below; pick the highest-priority task assigned to you (prefer bounced ones).
2) read its spec_refs docs first; bash .coordination/task.sh start <id> (claims files).
3) implement; per commit run: codex exec review --uncommitted -m gpt-5.5 -c model_reasoning_effort=xhigh ; commit only with no S0/S1; discriminating tests (§14); clean-room (§11/§12).
4) when its acceptance is met: FIRST push your commit to origin on a per-task branch work/<id-lowercase> (e.g. git push origin HEAD:work/s1-005) so the cloud dispatcher can fetch and cross-brain audit it -- if you only commit locally and never push, the dispatcher cannot see the code and WILL bounce it. Then: bash .coordination/task.sh review <id> "branch work/<id> @ <commit sha> + self-review result" (note MUST include the branch name + SHA). Do NOT mark done and do NOT ff-merge to the landing branch yourself -- the dispatcher merges after the audit passes.
If blocked: bash .coordination/task.sh block <id> "<reason>". Never edit a file another agent holds.
Never stall waiting on the Owner: if something needs Owner sign-off (high-risk merge, missing info), bash .coordination/task.sh park <id> "<question>" and move on to the next task; do not wait more than 2 minutes.

Your current board:
$(bash "$DIR/task.sh" mine 2>&1)
EOF
}

# Report this worker's heartbeat + state to the coord server each poll, so the
# /console panel shows it online and what it is doing (symmetric to the dispatcher).
# Without this the Owner cannot tell a dead worker-loop from an idle one. Best-effort.
wpush(){ # $1=state $2=detail [$3=event_kind $4=event_text]
  ST="$1" DT="$2" EK="${3:-}" ET="${4:-}" python3 - <<'PY' 2>/dev/null || true
import os,json,ssl,urllib.request
url=os.environ.get("COORD_URL",""); tok=os.environ.get("COORD_TOKEN",""); ca=os.environ.get("COORD_CACERT","")
if not url or not tok: raise SystemExit
body={"agent":os.environ.get("COORD_AGENT","worker"),"state":os.environ["ST"],"detail":os.environ["DT"][:500]}
if os.environ.get("EK"): body["event"]={"kind":os.environ["EK"],"text":os.environ.get("ET","")[:2000]}
ctx=ssl.create_default_context(cafile=ca) if ca else ssl.create_default_context()
op=urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ctx))
req=urllib.request.Request(url+"/dispatcher/status",data=json.dumps(body).encode(),
    headers={"Authorization":"Bearer "+tok,"Content-Type":"application/json"})
try: op.open(req,timeout=10).read()
except Exception: pass
PY
}

echo "worker-loop: agent=$COORD_AGENT poll=${POLL}s update=${UPDATE_BRANCH}  (Ctrl-C to stop)"
while true; do
  self_update    # pull latest .coordination scripts (+ re-exec self if changed) before each poll
  mine="$(bash "$DIR/task.sh" mine 2>&1 || true)"
  if echo "$mine" | grep -Eq '\[(assigned|in_progress)\b'; then
    tid="$(printf '%s\n' "$mine" | grep -E '\[(assigned|in_progress)' | head -1 | sed -E 's/^[[:space:]]*\[[^]]*\][[:space:]]*//' | awk '{print $1}')"
    echo "[$(date '+%H:%M:%S')] work available -> invoking $WORKER_AI_CMD"
    wpush working "在做 ${tid:-任务}" start "领到活,开始执行 ${tid}"
    # Keep this machine's file lock alive AND refresh the panel heartbeat while the
    # AI works (edits can exceed COORD_TTL); this loop is killed when the AI returns.
    ( while sleep 120; do python3 "$DIR/_coord.py" heartbeat "$COORD_AGENT" >/dev/null 2>&1; wpush working "在做 ${tid:-任务}(进行中)"; done ) &
    HB=$!
    prompt | (cd "$REPO" && eval "$WORKER_AI_CMD") || echo "  (AI run returned non-zero; will retry next cycle)"
    kill "$HB" 2>/dev/null || true
    wpush idle "上个任务跑完,轮询中" done "结束一轮 ${tid}"
  else
    echo "[$(date '+%H:%M:%S')] no work for $COORD_AGENT; sleeping ${POLL}s"
    wpush idle "空闲 · 轮询中(暂无分配)"
  fi
  sleep "$POLL"
done
