#!/usr/bin/env bash
# Dispatcher auto-loop — runs on ONE box that has the `claude` CLI (the cross-brain
# auditor). Polls the shared ledger; when a task is in `review` (or an idle machine
# has assignable backlog), it invokes `claude -p` HEADLESS to run ONE dispatcher
# round per .coordination/dispatcher-round.md, then loops. This is the TRUE 24/7
# trigger: it does not need an interactive Claude session open.
#
# Symmetry with worker-loop.sh: workers PULL their assigned work by polling; the
# dispatcher PULLS review work by polling. Assigning / marking-review IS the trigger;
# the polling loop on the other side picks it up within one poll interval.
#
# Setup (on the dispatcher box):
#   source ~/.config/huakai-coord/client.env      # COORD_URL/COORD_TOKEN/COORD_CACERT/COORD_AGENT
#   export PATH="$HOME/.local/bin:$PATH"           # so `claude` resolves
#   bash .coordination/dispatcher-loop.sh
#
# Safety: claude runs with --permission-mode bypassPermissions (headless autonomy —
# no human to approve each Bash/Edit). The standing safety gates still hold inside the
# round: only AUDIT-PASSED work is merged; high-risk lands at needs_owner (Owner-only);
# a flock serializes rounds so two never overlap. Stop with: pkill -f dispatcher-loop.sh
set -uo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/.." && pwd)"
: "${COORD_URL:?set COORD_URL (source client.env)}"
: "${COORD_TOKEN:?set COORD_TOKEN (source client.env)}"
# codex P1 (credential isolation): the headless dispatcher AI runs with bypassPermissions.
# If it inherited COORD_OWNER_TOKEN from the launching shell, it could self-`task.sh approve`
# needs_owner / money-path tasks, fully bypassing the Owner gate (the likely root cause of a
# security task auto-landing). The daemon must NEVER hold the Owner token — scrub it from this
# process (and therefore every claude child it spawns) before anything else runs.
unset COORD_OWNER_TOKEN
POLL="${DISPATCH_POLL_SECONDS:-90}"
# L1/L3 no-stall: even with NOTHING in `review`, run one dispatcher round every SWEEP
# seconds so the round playbook (dispatcher-round.md step 4) still feeds idle workers from
# open backlog and catches blocked/stuck/spinning tasks. Before this fix the loop only ran
# a round when review>0, so an idle worker + open backlog could sleep forever. The round
# self-decides "nothing to dispatch" and stops, so an empty sweep is cheap + idempotent.
SWEEP="${IDLE_SWEEP_SECONDS:-900}"
CLAUDE="${CLAUDE_BIN:-$HOME/.local/bin/claude}"
LOCK="$DIR/.dispatcher.lock"
ROUND_PROMPT="Execute ONE HUAKAI auto-dispatcher round now. Read $REPO/.coordination/dispatcher-round.md and follow it exactly; COORD_* env is already exported in your process. Run exactly one round then stop. Keep the closing status line short and in Chinese."

command -v "$CLAUDE" >/dev/null 2>&1 || { echo "FATAL: claude CLI not found at $CLAUDE"; exit 1; }
# Report state (+ optional event) to the coord server's /dispatcher/status so the
# /console panel shows live status + logs without the Owner touching a shell; echoes
# the control flag (run|pause) the panel last set. Cert-pinned, no-proxy, best-effort.
push(){ # $1=state $2=detail [$3=event_kind $4=event_text]
  ST="$1" DT="$2" EK="${3:-}" ET="${4:-}" python3 - <<'PY' 2>/dev/null || echo unknown
import os,json,ssl,urllib.request
url=os.environ["COORD_URL"]; tok=os.environ["COORD_TOKEN"]; ca=os.environ.get("COORD_CACERT","")
body={"agent":os.environ.get("COORD_AGENT","server-a"),"state":os.environ["ST"],"detail":os.environ["DT"][:500]}
if os.environ.get("EK"): body["event"]={"kind":os.environ["EK"],"text":os.environ.get("ET","")[:4000]}
ctx=ssl.create_default_context(cafile=ca) if ca else ssl.create_default_context()
op=urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ctx))
req=urllib.request.Request(url+"/dispatcher/status",data=json.dumps(body).encode(),
    headers={"Authorization":"Bearer "+tok,"Content-Type":"application/json"})
# fail-closed (codex P2): if control is unreadable, emit 'unknown' (NOT 'run') so the loop
# refuses to audit/merge until control is confirmed 'run' — a paused Owner must never be
# overridden by a control-plane timeout / cert / auth drift.
try: print(json.loads(op.open(req,timeout=12).read()).get("control","run"))
except Exception: print("unknown")
PY
}

echo "dispatcher-loop: poll=${POLL}s sweep=${SWEEP}s claude=$CLAUDE  (Ctrl-C / pkill to stop)"
# One serialized (flock) dispatcher round; echoes the tail of its output. Both the review
# trigger and the idle-backlog sweep call this — same round, same playbook.
# Owner rule: dispatcher audits with the STRONGEST tier — opus (latest, = 4.8) + max effort
# (highest reasoning, above xhigh). Overridable via CLAUDE_MODEL/CLAUDE_EFFORT but defaults
# are the strongest available so a misconfigured box still runs top-tier.
run_round(){ ( flock -n 9 || { echo "(another round running)"; exit 0; }; cd "$REPO"; printf '%s\n' "$ROUND_PROMPT" | "$CLAUDE" -p --model "${CLAUDE_MODEL:-opus}" --effort "${CLAUDE_EFFORT:-max}" --permission-mode bypassPermissions 2>&1 | tail -40 ) 9>"$LOCK"; }
last_sweep=0   # epoch of last round; 0 => first loop sweeps immediately so a cold start feeds idle workers
while true; do
  board="$(bash "$DIR/task.sh" list 2>&1 || true)"
  nrev="$(printf '%s\n' "$board" | grep -cE '\[review' || true)"; nrev="${nrev:-0}"
  ctl="$(push idle "轮询中 · 待审 ${nrev}")"
  if [ "$ctl" != "run" ]; then
    # fail-closed (codex P2): proceed ONLY when control is explicitly 'run'. 'pause' OR an
    # unreadable control plane ('unknown' from a timeout/auth drift) => do NOT audit/merge.
    echo "[$(date '+%H:%M:%S')] control='$ctl' (not run); idling ${POLL}s"
    push paused "调度暂停/控制面不可读(ctl=${ctl})· ${nrev} 待审挂起"
    sleep "$POLL"; continue
  fi
  now_ts="$(date +%s)"
  if [ "$nrev" -gt 0 ] 2>/dev/null; then
    echo "[$(date '+%H:%M:%S')] ${nrev} review task(s) -> running one dispatcher round"
    push auditing "发现 ${nrev} 个待审,开始审核一轮"
    out="$(run_round)"
    push idle "审核轮结束 · 待审 ${nrev}" round "$out"
    last_sweep="$now_ts"
  elif [ "$(( now_ts - last_sweep ))" -ge "$SWEEP" ]; then
    # L1+L3 no-stall: no review work, but periodically run a round anyway so idle workers
    # with open backlog get fed (dispatcher-round.md step 4) and blocked/stuck/spinning
    # tasks get caught. An empty sweep stops itself, so this is safe to run on a timer.
    echo "[$(date '+%H:%M:%S')] no review; idle-backlog sweep -> running one dispatcher round"
    push auditing "无待审 · 跑一轮巡检(空闲worker/开放backlog/卡住任务)"
    out="$(run_round)"
    push idle "巡检轮结束" round "$out"
    last_sweep="$now_ts"
  else
    echo "[$(date '+%H:%M:%S')] nothing in review; next idle sweep in $(( SWEEP - (now_ts - last_sweep) ))s"
  fi
  sleep "$POLL"
done
