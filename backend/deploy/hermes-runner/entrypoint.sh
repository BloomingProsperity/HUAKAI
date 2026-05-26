#!/usr/bin/env sh
set -eu

error() {
  echo "error: $*" >&2
}

JWT_KEY_READY=0
if [ -n "${HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR:-}" ]; then
  if [ ! -d "$HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR" ]; then
    error "HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR must be an existing directory"
    exit 1
  fi
  for JWT_PUBLIC_KEY in "$HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR"/*.pem; do
    if [ -f "$JWT_PUBLIC_KEY" ] && [ -r "$JWT_PUBLIC_KEY" ]; then
      JWT_KEY_READY=1
      break
    fi
  done
  if [ "$JWT_KEY_READY" -ne 1 ]; then
    error "HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR must contain at least one readable .pem file"
    exit 1
  fi
fi

if [ -n "${HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH:-}" ] || [ -n "${HUAKAI_HERMES_JWT_KID:-}" ]; then
  if [ -z "${HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH:-}" ] || [ -z "${HUAKAI_HERMES_JWT_KID:-}" ]; then
    error "Hermes runner requires both HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH and HUAKAI_HERMES_JWT_KID when either is set"
    exit 1
  fi
  if [ ! -f "$HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH" ] || [ ! -r "$HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH" ]; then
    error "HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH must be an existing readable file"
    exit 1
  fi
  JWT_KEY_READY=1
fi

if [ "$JWT_KEY_READY" -ne 1 ]; then
  error "Hermes runner requires HUAKAI_HERMES_JWT_PUBLIC_KEYS_DIR or both HUAKAI_HERMES_JWT_PUBLIC_KEY_PATH and HUAKAI_HERMES_JWT_KID"
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
