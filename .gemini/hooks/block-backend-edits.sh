#!/usr/bin/env bash
# This file is agent-facing and authoritative.

set -euo pipefail

blocked='(^|/)(src/core|src/api|src/db|src/shared|src/plugins)(/|$)|(^|/)LICENSE$'

status=0
inputs=()

if [ "$#" -gt 0 ]; then
  inputs=("$@")
else
  while IFS= read -r line; do
    [ -n "$line" ] && inputs+=("$line")
  done
fi

for path in "${inputs[@]}"; do
  normalized="${path//\\//}"
  if [[ "$normalized" =~ $blocked ]]; then
    echo "Blocked Gemini backend edit: $path" >&2
    status=1
  fi
done

if [ "$status" -ne 0 ]; then
  echo "Gemini may edit frontend UI and operations dashboard files only unless explicitly assigned backend scope." >&2
fi

exit "$status"
