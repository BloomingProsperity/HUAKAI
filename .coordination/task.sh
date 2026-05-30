#!/usr/bin/env bash
# Task-dispatch client for the shared ledger on the coord server.
# Reads COORD_URL / COORD_TOKEN / COORD_CACERT / COORD_AGENT from the environment
# (same vars as check.sh/claim.sh/release.sh). See .coordination/README.md.
#
# Worker verbs:
#   task.sh mine                      list tasks assigned to $COORD_AGENT
#   task.sh list [status=X] [wave=Y]  list all tasks (optionally filtered)
#   task.sh show   <id>               full contract: detail/acceptance/spec_refs/files
#   task.sh start  <id>               claim the task's files + mark in_progress
#   task.sh review <id> ["notes"]     mark review (you finished; releases the file lock)
#   task.sh block  <id> "reason"      mark blocked (releases the file lock)
#   task.sh heartbeat [agent]         refresh your file lock during a long edit
#
# Dispatcher (Claude PM) verbs:
#   task.sh assign <id> <agent> <verify_rounds> ["verify_notes"]   (server rejects vr<3)
#   task.sh conflicts <files-csv>     does a scope overlap any active/parked/blocked task?
#   task.sh pass   <id> ["notes"]     audit passed  -> done (non-risk only)
#   task.sh bounce <id> "review_notes" audit failed -> back to assigned
#   task.sh park   <id> ["notes"]     high-risk / needs-Owner -> needs_owner
#   task.sh load   <tasks.json>       bulk create/assign from a JSON array
# Owner-only verb (needs COORD_OWNER_TOKEN):
#   task.sh approve <id>              approve a parked task -> done
set -euo pipefail
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
sub="${1:-}"; shift || true
case "$sub" in
  mine)      exec python3 "$DIR/_coord.py" tasks mine ;;
  list)      exec python3 "$DIR/_coord.py" tasks "$@" ;;
  show)      exec python3 "$DIR/_coord.py" task-show "$@" ;;
  start)     exec python3 "$DIR/_coord.py" task-start "$@" ;;
  review)    exec python3 "$DIR/_coord.py" task-review "$@" ;;
  block)     exec python3 "$DIR/_coord.py" task-block "$@" ;;
  heartbeat) exec python3 "$DIR/_coord.py" heartbeat "$@" ;;
  assign)    exec python3 "$DIR/_coord.py" task-assign "$@" ;;
  conflicts) exec python3 "$DIR/_coord.py" task-conflicts "$@" ;;
  pass)      exec python3 "$DIR/_coord.py" task-pass "$@" ;;
  bounce)    exec python3 "$DIR/_coord.py" task-bounce "$@" ;;
  park)      exec python3 "$DIR/_coord.py" task-park "$@" ;;
  load)      exec python3 "$DIR/_coord.py" task-load "$@" ;;
  approve)   exec python3 "$DIR/_coord.py" task-approve "$@" ;;
  *) echo "usage: task.sh {mine|list|show|start|review|block|heartbeat|assign|conflicts|pass|bounce|park|load|approve} ..." >&2; exit 1 ;;
esac
