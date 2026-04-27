#!/usr/bin/env bash
# This file is agent-facing and authoritative.

set -euo pipefail

status=0
files=()

if [ "$#" -gt 0 ]; then
  files=("$@")
else
  while IFS= read -r line; do
    [ -n "$line" ] && files+=("$line")
  done
fi

patterns=(
  'sk-[A-Za-z0-9_-]{20,}'
  'AKIA[0-9A-Z]{16}'
  'ghp_[A-Za-z0-9_]{30,}'
  'xox[baprs]-[A-Za-z0-9-]{20,}'
  '-----BEGIN (RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----'
  '(api[_-]?key|secret|token|password)[[:space:]]*[:=][[:space:]]*["'\'']?[A-Za-z0-9_./+=-]{16,}'
)

for file in "${files[@]}"; do
  [ -f "$file" ] || continue
  for pattern in "${patterns[@]}"; do
    if grep -E -n -i "$pattern" "$file" >/dev/null 2>&1; then
      echo "Blocked possible secret in: $file" >&2
      status=1
      break
    fi
  done
done

if [ "$status" -ne 0 ]; then
  echo "Remove secrets and use placeholders or documented secret-management flow." >&2
fi

exit "$status"
