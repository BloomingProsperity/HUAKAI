#!/usr/bin/env bash
# integration-pg.sh — 对每个带 integration_pg 标签的包,用一份"全新迁移库"隔离跑测试。
#
# 背景:这些 integration 测试各自假设一个纯净、已迁移的数据库(等于开发者单包跑的方式),
# 并非为"多包共享一个库"而设计。直接 `go test -tags=integration_pg ./...` 让 55 个包并行
# 共享一个 DB,会因跨包状态污染产生假阳——已实测复现 4 例:
#   - billing:SERIALIZABLE 事务被别包并发事务撞出 40001;
#   - adminquotahttp:断言 user_balances 计数不变,被别包并发插入的余额行打破;
#   - tenancy×2:别包建的 user 占用默认租户致删租户 FK 冲突 / 默认租户已被别包种好。
# 这些都不是产品缺陷,而是共享库隔离不足。本脚本用 template 库为每个包秒级克隆一份纯净迁移库,
# 串行跑,确保隔离——既不假阳(干净库),也不假绿(真连库真跑、无 DB 直接失败)。
set -uo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)" # -> backend/
export GOFLAGS=-buildvcs=false

PGHOST="${PGHOST:-localhost}"
PGPORT="${PGPORT:-5432}"
PGUSER="${PGUSER:-huakai}"
PGPASSWORD="${PGPASSWORD:-huakai}"
export PGPASSWORD
TEMPLATE_DB="${HUAKAI_IT_TEMPLATE_DB:-huakai_it_template}"
RUN_DB="${HUAKAI_IT_RUN_DB:-huakai_it_run}"
MIGRATIONS="sql/migrations"
TIMEOUT="${HUAKAI_IT_TIMEOUT:-3m}"
RACE="${HUAKAI_IT_RACE:--race}" # 置空可关 race(本地快速冒烟用)

psql_admin() { psql -h "$PGHOST" -p "$PGPORT" -U "$PGUSER" -d postgres -v ON_ERROR_STOP=1 -q "$@"; }
dsn() { echo "postgres://$PGUSER:$PGPASSWORD@$PGHOST:$PGPORT/$1?sslmode=disable"; }

command -v migrate >/dev/null 2>&1 || { echo "FATAL: 缺 migrate CLI"; exit 2; }

echo ">> 准备 template 库 $TEMPLATE_DB(迁移一次,后续各包从它克隆)"
psql_admin -c "DROP DATABASE IF EXISTS $RUN_DB WITH (FORCE)"
psql_admin -c "DROP DATABASE IF EXISTS $TEMPLATE_DB WITH (FORCE)"
psql_admin -c "CREATE DATABASE $TEMPLATE_DB"
migrate -path "$MIGRATIONS" -database "$(dsn "$TEMPLATE_DB")" up

PKGS=$(grep -rl "//go:build integration_pg" --include=*_test.go internal | xargs -n1 dirname | sort -u | sed 's#^#./#')
# 防假绿:发现不到 integration_pg 包说明发现逻辑坏了(标签改名/路径变动),必须 fail-loud
# 而非"0 包全绿"静默放行——这正是本 job 要消灭的假绿形态。
if [ -z "$PKGS" ]; then
  echo "FATAL: 未发现任何 integration_pg 包,发现逻辑可能已失效(拒绝假绿)"
  exit 2
fi
total=$(echo "$PKGS" | wc -l)
echo ">> 共 $total 个 integration_pg 包,逐包克隆纯净库串行跑(race=$RACE)"

fail=0
failed_pkgs=""
i=0
for pkg in $PKGS; do
  i=$((i + 1))
  # 从 template 克隆纯净库(秒级,免重迁)。克隆要求 template 无活动连接——串行下恒成立。
  psql_admin -c "DROP DATABASE IF EXISTS $RUN_DB WITH (FORCE)" >/dev/null
  psql_admin -c "CREATE DATABASE $RUN_DB TEMPLATE $TEMPLATE_DB" >/dev/null
  printf '[%d/%d] %s\n' "$i" "$total" "$pkg"
  if ! HUAKAI_DATABASE_URL="$(dsn "$RUN_DB")" HUAKAI_TEST_DATABASE_URL="$(dsn "$RUN_DB")" \
    HUAKAI_SKIP_PERF_LATENCY_GATE=1 \
    go test -tags=integration_pg $RACE -count=1 -timeout "$TIMEOUT" "$pkg"; then
    fail=1
    failed_pkgs="$failed_pkgs $pkg"
  fi
done

psql_admin -c "DROP DATABASE IF EXISTS $RUN_DB WITH (FORCE)" >/dev/null
if [ "$fail" = 0 ]; then
  echo ">> integration_pg PASS($total 包全绿)"
else
  echo ">> integration_pg FAIL,失败包:$failed_pkgs"
fi
exit $fail
