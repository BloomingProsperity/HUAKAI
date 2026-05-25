#!/usr/bin/env sh
set -eu

error() {
  echo "error: $*" >&2
}

if [ -z "${HUAKAI_HERMES_SHARED_SECRET:-}" ]; then
  error "HUAKAI_HERMES_SHARED_SECRET is required"
  exit 1
fi

BIND="${HUAKAI_HERMES_RUNNER_BIND:-0.0.0.0:8801}"
case "$BIND" in
  *:*)
    HOST="${BIND%:*}"
    PORT="${BIND##*:}"
    ;;
  *)
    error "HUAKAI_HERMES_RUNNER_BIND must be host:port"
    exit 1
    ;;
esac

if [ -z "$HOST" ] || [ -z "$PORT" ]; then
  error "HUAKAI_HERMES_RUNNER_BIND must include non-empty host and port"
  exit 1
fi

case "$PORT" in
  *[!0-9]*)
    error "HUAKAI_HERMES_RUNNER_BIND port must be numeric"
    exit 1
    ;;
esac

# 保持前台进程，交给 tini 转发信号。
exec uvicorn main:app --host "$HOST" --port "$PORT"
