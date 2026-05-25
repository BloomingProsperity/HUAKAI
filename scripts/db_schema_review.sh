#!/usr/bin/env sh
set -eu

ROOT="${1:-backend/sql}"

if [ ! -d "$ROOT" ]; then
  echo "schema review root not found: $ROOT" >&2
  exit 2
fi

violations="$(grep -RInE '(^|[[:space:],])("[^"]*"|[a-zA-Z_][a-zA-Z0-9_]*)[[:space:]]+(TEXT|BYTEA|JSONB|JSON)([[:space:],)]|$)' "$ROOT" \
  | grep -Ei '(^|[[:space:]_,"])(prompt|completion|raw_body|body|content|message_content|tool_input|tool_output|upstream_body)("|_content)?[[:space:]]+(TEXT|BYTEA|JSONB|JSON)' || true)"

if [ -n "$violations" ]; then
  echo "F-PRIV-001 schema review failed: raw user-data-shaped columns are forbidden" >&2
  echo "$violations" >&2
  exit 1
fi

exit 0
