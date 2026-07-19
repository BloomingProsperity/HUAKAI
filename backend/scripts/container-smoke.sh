#!/usr/bin/env bash
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
suffix="${GITHUB_RUN_ID:-local}-$$"
image="huakai-container-smoke:${suffix}"
network="huakai-container-smoke-${suffix}"
postgres="huakai-container-smoke-pg-${suffix}"
redis="huakai-container-smoke-redis-${suffix}"
gateway="huakai-container-smoke-gateway-${suffix}"
db_password="huakai-container-smoke"
credential_key="MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE="

cleanup() {
  docker rm -f "${gateway}" "${postgres}" "${redis}" >/dev/null 2>&1 || true
  docker network rm "${network}" >/dev/null 2>&1 || true
  if [[ "${HUAKAI_KEEP_SMOKE_IMAGE:-false}" != "true" ]]; then
    docker image rm -f "${image}" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

wait_postgres() {
  for _ in $(seq 1 60); do
    if docker exec "${postgres}" pg_isready -U huakai -d huakai >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "PostgreSQL 在 60 秒内未就绪" >&2
  return 1
}

wait_ready() {
  for _ in $(seq 1 90); do
    if docker exec "${gateway}" wget -qO- http://127.0.0.1:8080/readyz | grep -q '"status":"ok"'; then
      return 0
    fi
    sleep 1
  done
  echo "gateway 在 90 秒内未就绪" >&2
  docker logs "${gateway}" >&2 || true
  return 1
}

assert_not_ready() {
  if docker exec "${gateway}" wget -qO- http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
    echo "依赖故障时 /readyz 仍返回成功" >&2
    return 1
  fi
}

wait_container_exit() {
  for _ in $(seq 1 30); do
    if [[ "$(docker inspect -f '{{.State.Running}}' "${gateway}")" == "false" ]]; then
      local exit_code
      exit_code="$(docker inspect -f '{{.State.ExitCode}}' "${gateway}")"
      if [[ "${exit_code}" == "0" ]]; then
        echo "子进程异常退出后容器错误地以 0 退出" >&2
        return 1
      fi
      return 0
    fi
    sleep 1
  done
  echo "子进程退出后容器仍在运行" >&2
  return 1
}

kill_child() {
  local executable="$1"
  docker exec "${gateway}" bash -ceu '
    target="$1"
    for pid in $(cat /proc/1/task/1/children); do
      cmdline=$(tr "\0" " " < "/proc/${pid}/cmdline")
      if [[ "${cmdline}" == *"${target}"* ]]; then
        kill -KILL "${pid}"
        exit 0
      fi
    done
    echo "未找到子进程 ${target}" >&2
    exit 1
  ' -- "${executable}"
}

assert_both_children() {
  docker exec "${gateway}" bash -ceu '
    children=$(cat /proc/1/task/1/children)
    [[ $(wc -w <<<"${children}") -eq 2 ]]
    all=""
    for pid in ${children}; do
      all+="$(tr "\0" " " < "/proc/${pid}/cmdline")\n"
    done
    [[ "${all}" == *"/usr/local/bin/huakai-gateway"* ]]
    [[ "${all}" == *"/usr/local/bin/huakai-tls-sidecar"* ]]
  '
}

docker build --pull -f "${repo_root}/backend/Dockerfile" -t "${image}" "${repo_root}"
docker network create "${network}" >/dev/null
docker run -d --name "${postgres}" --network "${network}" \
  -e POSTGRES_USER=huakai \
  -e POSTGRES_PASSWORD="${db_password}" \
  -e POSTGRES_DB=huakai \
  postgres:15 >/dev/null
docker run -d --name "${redis}" --network "${network}" redis:7-alpine >/dev/null
wait_postgres

docker run -d --name "${gateway}" --network "${network}" \
  -e HUAKAI_ADDR=:8080 \
  -e HUAKAI_RELEASE_MODE=dev \
  -e HUAKAI_AUTO_MIGRATE=true \
  -e HUAKAI_DATABASE_URL="postgres://huakai:${db_password}@${postgres}:5432/huakai?sslmode=disable" \
  -e HUAKAI_REDIS_URL="redis://${redis}:6379/0" \
  -e HUAKAI_RATE_LIMIT_REDIS_URL="redis://${redis}:6379/0" \
  -e HUAKAI_CREDENTIAL_KEY_B64="${credential_key}" \
  "${image}" >/dev/null

wait_ready
assert_both_children

docker stop -t 5 "${redis}" >/dev/null
assert_not_ready
docker start "${redis}" >/dev/null
wait_ready

kill_child /usr/local/bin/huakai-tls-sidecar
wait_container_exit
docker start "${gateway}" >/dev/null
wait_ready

kill_child /usr/local/bin/huakai-gateway
wait_container_exit
docker start "${gateway}" >/dev/null
wait_ready

docker stop -t 20 "${gateway}" >/dev/null
if [[ "$(docker inspect -f '{{.State.ExitCode}}' "${gateway}")" != "0" ]]; then
  echo "SIGTERM 优雅关停没有以 0 退出" >&2
  exit 1
fi

echo "单镜像双进程、依赖就绪、异常退出重启与优雅关停 smoke 通过"
