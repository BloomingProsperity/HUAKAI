#!/usr/bin/env bash
# This file is agent-facing and authoritative.
#
# Claude Code PreToolUse hook for Edit / Write / MultiEdit.
# Reads Claude Code's JSON event from stdin and scans the entire tool_input
# blob for known secret patterns. Exits 2 (block) if a match is found.
#
# Patterns mirror .gemini/hooks/block-secrets.sh. Both files must be kept
# in sync until a shared pattern source is introduced (Phase 1 task).

set -euo pipefail

input=$(cat)

patterns=(
  'sk-[A-Za-z0-9_-]{20,}'
  'AKIA[0-9A-Z]{16}'
  'ghp_[A-Za-z0-9_]{30,}'
  'xox[baprs]-[A-Za-z0-9-]{20,}'
  '-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----'
)

for pattern in "${patterns[@]}"; do
  if printf '%s' "$input" | grep -E -i "$pattern" > /dev/null 2>&1; then
    echo "Blocked: tool input contains a probable secret matching: $pattern" >&2
    echo "If this is a placeholder, use a clearly fake value such as sk-FAKE_REPLACE_ME." >&2
    exit 2
  fi
done

exit 0
