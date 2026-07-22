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

if [ -z "${HUAKAI_HERMES_MCP_URL:-}" ]; then
  error "Hermes runner requires HUAKAI_HERMES_MCP_URL"
  exit 1
fi

HERMES_BINARY="${HUAKAI_HERMES_BINARY:-hermes}"
if ! command -v "$HERMES_BINARY" >/dev/null 2>&1; then
  error "Hermes runner cannot find the configured hermes binary"
  exit 1
fi

EXPECTED_HERMES_VERSION="0.19.0"
if ! HERMES_PACKAGE_VERSION="$(python3 -c 'from importlib.metadata import version; print(version("hermes-agent"))')"; then
  error "Hermes runner cannot read the installed hermes-agent version"
  exit 1
fi
if [ "$HERMES_PACKAGE_VERSION" != "$EXPECTED_HERMES_VERSION" ]; then
  error "Hermes runner requires hermes-agent ${EXPECTED_HERMES_VERSION}"
  exit 1
fi

WORK_ROOT="${HUAKAI_HERMES_WORK_ROOT:-/run/huakai-hermes}"
case "$WORK_ROOT" in
  /*) ;;
  *)
    error "HUAKAI_HERMES_WORK_ROOT must be an absolute path"
    exit 1
    ;;
esac
mkdir -p "$WORK_ROOT"
chmod 700 "$WORK_ROOT"

if ! python3 -c '
import os
import sys
from urllib.parse import urlsplit

value = os.environ.get("HUAKAI_HERMES_MCP_URL", "").strip().rstrip("/")
try:
    parts = urlsplit(value)
    valid = (
        parts.scheme in {"http", "https"}
        and bool(parts.hostname)
        and parts.username is None
        and parts.password is None
        and not parts.query
        and not parts.fragment
        and parts.path == "/internal/hermes/mcp"
    )
except ValueError:
    valid = False
sys.exit(0 if valid else 1)
' >/dev/null 2>&1; then
  error "Hermes runner configuration is invalid"
  exit 1
fi

if ! python3 -c '
import asyncio

from official_runner import RunnerConfig
from official_tool_surface import verify_official_tool_surface

asyncio.run(verify_official_tool_surface(RunnerConfig.from_env()))
' >/dev/null 2>&1; then
  error "Hermes runner tool surface is not restricted to HUAKAI MCP"
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
