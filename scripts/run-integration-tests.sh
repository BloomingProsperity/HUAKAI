#!/usr/bin/env bash
# 全量集成测试入口 — 含真 PostgreSQL 的 _integration_test.go。
#
# Owner / CI nightly 跑这个。前置条件：
#   - 本地 / CI 已起 PG 容器 (make -C backend db-up)
#   - 已 apply migrations  (make -C backend db-migrate)
#   - 设置 HUAKAI_DATABASE_URL 指向上述 PG（缺省走 docker compose 默认地址）
#
# 跑两类测试：
#   1. fast suite (不带 tag) — 与 scripts/run-tests.sh 等价
#   2. integration_pg suite — 真 PG 的 _integration_test.go
#
# 用法：
#   scripts/run-integration-tests.sh
#   HUAKAI_DATABASE_URL="postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable" \
#     scripts/run-integration-tests.sh
#
# 文档：docs/dev-tests.md
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}/backend"

export GOCACHE="${GOCACHE:-${HOME}/.cache/go-build}"
mkdir -p "${GOCACHE}"

# DATABASE_URL 默认指向 docker-compose.dev.yml 起的本地 PG，覆盖请直接 export。
: "${HUAKAI_DATABASE_URL:=postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable}"
export HUAKAI_DATABASE_URL

# Phase 1：fast (与 PR 走的同一份)。
echo "[run-integration-tests] === Phase 1: fast suite ==="
echo "[run-integration-tests] GOCACHE=${GOCACHE}"
echo "[run-integration-tests] go test -race -count=1 -timeout 180s ./..."
go test -race -count=1 -timeout 180s ./...

# Phase 2：integration_pg (真 PG)。timeout 加宽，部分测试 ~分钟级。
echo "[run-integration-tests] === Phase 2: integration_pg suite ==="
echo "[run-integration-tests] HUAKAI_DATABASE_URL=${HUAKAI_DATABASE_URL}"
echo "[run-integration-tests] go test -tags=integration_pg -race -count=1 -timeout 600s ./..."
go test -tags=integration_pg -race -count=1 -timeout 600s ./...

echo "[run-integration-tests] OK — fast + integration_pg 全部通过"
