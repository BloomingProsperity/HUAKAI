#!/usr/bin/env bash
set -Eeuo pipefail

socket_path="${HUAKAI_TRANSPORT_SIDECAR_SOCKET:-/run/huakai/tls-sidecar.sock}"
export HUAKAI_TRANSPORT_SIDECAR_SOCKET="${socket_path}"
export HUAKAI_TLS_SIDECAR_SOCKET="${socket_path}"

mkdir -p "$(dirname "${socket_path}")"

/usr/local/bin/huakai-tls-sidecar "${socket_path}" &
sidecar_pid=$!
gateway_pid=""

terminate_children() {
  trap - TERM INT
  if [[ -n "${gateway_pid}" ]]; then
    kill -TERM "${gateway_pid}" 2>/dev/null || true
  fi
  kill -TERM "${sidecar_pid}" 2>/dev/null || true
  if [[ -n "${gateway_pid}" ]]; then
    wait "${gateway_pid}" 2>/dev/null || true
  fi
  wait "${sidecar_pid}" 2>/dev/null || true
}

graceful_stop() {
  terminate_children
  exit 0
}
trap graceful_stop TERM INT

sidecar_ready=false
for _ in $(seq 1 50); do
  if /usr/local/bin/huakai-tls-sidecar --check "${socket_path}"; then
    sidecar_ready=true
    break
  fi
  if ! kill -0 "${sidecar_pid}" 2>/dev/null; then
    wait "${sidecar_pid}" || true
    echo "Rust TLS sidecar 在就绪前退出" >&2
    exit 1
  fi
  sleep 0.1
done

if [[ "${sidecar_ready}" != "true" ]]; then
  echo "Rust TLS sidecar 在 5 秒内未就绪" >&2
  terminate_children
  exit 1
fi

/usr/local/bin/huakai-gateway &
gateway_pid=$!

set +e
exited_pid=""
wait -n -p exited_pid "${sidecar_pid}" "${gateway_pid}"
exit_status=$?
set -e

if [[ "${exit_status}" -eq 0 ]]; then
  if [[ "${exited_pid}" == "${sidecar_pid}" ]]; then
    echo "Rust TLS sidecar 意外正常退出，触发容器重启" >&2
  else
    echo "Go gateway 意外正常退出，触发容器重启" >&2
  fi
  exit_status=1
fi

terminate_children
exit "${exit_status}"
