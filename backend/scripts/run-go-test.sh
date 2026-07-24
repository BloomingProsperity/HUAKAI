#!/usr/bin/env bash
# run-go-test.sh — wrapper around `go test` that survives Smart App Control hiccups.
#
# Why this exists:
#   On this Windows 11 box Smart App Control (SAC) is enforced
#   (HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy\VerifiedAndReputablePolicyState=1).
#   Freshly compiled, unsigned Go test binaries occasionally trip SAC's
#   cloud-reputation gate and abort with one of:
#       fork/exec ...\xxx.test.exe: An Application Control policy has blocked this file.
#       fork/exec ...\xxx.test.exe: Permission denied
#   The block is per-binary-hash and short-lived; a retry usually succeeds.
#
# What it does:
#   1. Pins GOTMPDIR / GOCACHE / GOMODCACHE inside C:\HUAKAI\repo\backend
#      (Defender-excluded; reduces AV scan churn).
#   2. Runs `go test -tags=integration_pg "$@"` (default target ./... if none given).
#   3. Retries up to 3 times if (and only if) the failure matches a SAC-block
#      signature. Other failures (real test failures, compile errors) are not retried.
#
# 长期处理方式由管理员调整 Smart App Control，详见 docs/dev-tests.md。

set -u

# --- locate repo root ---------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT" || exit 2

# --- env ----------------------------------------------------------------------
# Translate to Windows-style paths because go.exe is a native Windows binary
# and does not understand /c/HUAKAI/...
WIN_ROOT='C:\HUAKAI\repo\backend'
export GOTMPDIR="${GOTMPDIR:-${WIN_ROOT}\\.tmp}"
export GOCACHE="${GOCACHE:-${WIN_ROOT}\\.gotmp\\cache}"
export GOMODCACHE="${GOMODCACHE:-${WIN_ROOT}\\.gotmp\\mod}"

mkdir -p "$REPO_ROOT/.tmp" "$REPO_ROOT/.gotmp/cache" "$REPO_ROOT/.gotmp/mod"

GO_BIN="${GO_BIN:-/c/Program Files/Go/bin/go.exe}"
if [[ ! -x "$GO_BIN" ]]; then
  GO_BIN="$(command -v go || true)"
fi
if [[ -z "$GO_BIN" ]]; then
  echo "run-go-test.sh: cannot find go.exe" >&2
  exit 127
fi

# --- args ---------------------------------------------------------------------
# If caller passed any positional args, use them verbatim; otherwise default to
# the whole module with the integration_pg build tag.
if [[ $# -eq 0 ]]; then
  set -- -tags=integration_pg ./...
fi

MAX_ATTEMPTS=3
LOG_FILE="$(mktemp -t run-go-test.XXXXXX.log)"
trap 'rm -f "$LOG_FILE"' EXIT

attempt=1
while (( attempt <= MAX_ATTEMPTS )); do
  echo "[run-go-test] attempt $attempt/$MAX_ATTEMPTS: go test $*"
  echo "[run-go-test]   GOTMPDIR=$GOTMPDIR"
  echo "[run-go-test]   GOCACHE=$GOCACHE"

  # Tee through to stdout/stderr while also capturing for SAC-pattern grep.
  # We rely on go test's own exit code.
  set +e
  "$GO_BIN" test -count=1 "$@" 2>&1 | tee "$LOG_FILE"
  rc=${PIPESTATUS[0]}
  set -e

  if (( rc == 0 )); then
    exit 0
  fi

  # Match the two known SAC-induced failure signatures.
  if grep -Eq 'An Application Control policy has blocked this file|fork/exec.*\.test\.exe.*([Pp]ermission denied|Application Control)' "$LOG_FILE"; then
    echo "[run-go-test] SAC block detected (rc=$rc); retrying after short backoff..."
    sleep $(( attempt * 2 ))
    attempt=$(( attempt + 1 ))
    continue
  fi

  echo "[run-go-test] non-SAC failure (rc=$rc); not retrying."
  exit "$rc"
done

echo "[run-go-test] exhausted $MAX_ATTEMPTS attempts; giving up."
echo "[run-go-test] 可考虑关闭 Smart App Control，详见 docs/dev-tests.md"
exit 75   # EX_TEMPFAIL
