#!/usr/bin/env bash
# quality-gate.sh — block NEW staticcheck findings or NEW deadcode vs committed baseline.
# Ratchet: existing findings grandfathered (baseline files); only NEW issues fail CI.
#
# baseline 只能下降(2026-06-25 加固,对应 renew 审查 S1「质量门允许 --update 洗 baseline」):
#   - SC_MAX/DC_MAX 是 baseline 行数硬上限常量(=当前提交值)。baseline 一旦膨胀超过上限,
#     正常 gate 直接失败 —— 杜绝用 `--update` 把新债洗进基线绕过门禁。
#   - 上限只能在本脚本里显式调低(清债后);若确需调高,必须在 PR diff 里显式改这两个常量
#     (= Owner 可见可审的批准面),并在 docs/process/reviews/DEFERRED-*.md 记录理由。
#   - `--update` 在 CI 中被拒;本地也必须显式 HUAKAI_ALLOW_BASELINE_REWRITE=1 才能重写,
#     且重写【不】自动调高上限 —— 膨胀的重写仍会被上限拦下,逼人要么修、要么显式改常量。
# Regenerate baseline after a real cleanup(本地):
#   HUAKAI_ALLOW_BASELINE_REWRITE=1 scripts/quality-gate.sh --update
set -uo pipefail
cd "$(cd "$(dirname "$0")/.." && pwd)" # -> backend/
export GOFLAGS=-buildvcs=false
SC_BASE="scripts/staticcheck-baseline.txt"
DC_BASE="scripts/deadcode-baseline.txt"
# baseline 行数硬上限(只能在此显式调低;调高 = Owner-gated,见文件头注释)。
# 2026-06-25 Owner 拍板「re-baseline 吸收+装锁+排清债」:把已红一阵、被 PR 无视合并的存量
# 270 项新发现一次性吸收进 baseline(staticcheck 93→174 / deadcode 787→907),恢复 CI 真跑
# go test;上限锁在吸收后的值防再膨胀;存量债排进 DEFERRED 排清(见 docs/process/reviews/)。
# 2026-06-25 DEFERRED 批1 清债:修 gemini(SA4017/SA4006/ST1005)+ subscription newExpires(SA4006×2)
# + sql/embed.go 注释假阳(SA9009),staticcheck baseline 174→168,上限同步调低。
# 2026-06-25 DEFERRED 批2 清债:SA1012 nil ctx(error_rule_eval 5 处惰性 nil→context.TODO();
# clientid/postgres 有意 nil 测试 //nolint 改 //lint:ignore SA1012)+ ST1018 把零宽字符 ZWSP
# 字面改用 backslash-u200b 转义(obfuscate 生产 + 2 测试),baseline 168->162,上限同步调低。
# 2026-06-25 DEFERRED 批3 清债:SA4000 自反比较 ×6(均非笔误,有状态 budget.Allow/cachedSessionID
# 或确定性守卫 auditLedgerAdvisoryLockKey/pickIndex)改「两次调用各存变量再比」,baseline 162->156,上限同步调低。
# 2026-06-26 删 referral_reward 平行死实现(community/invitation/referral_reward_store+config.go,零生产消费的
# 休眠双花地雷):连带移除其 staticcheck findings(156->94)与 deadcode 条目(907->873),两上限同步调低。
# 2026-06-29 订阅自动续费 worker(money,默认关 KNOB):Store 接口新增 ListAutoRenewDue/
# TryAutoRenewSubscription,内存实现 memoryStore 经 `var _ Store` 编译断言被强制实现这两个方法,
# 但 memoryStore 是 test-fake、从生产 main 不可达 → deadcode 必然命中(与已收录的 31 个 memoryStore
# 兄弟方法同类,删不掉/不接生产)。按本文件头规定的 deferral 正路 +2 进 baseline 并显式调高 DC_MAX
# 873->875(Owner 可见可审);非死代码堆积,是接口契约的内存实现。
# 2026-07-02 role 制单登录共享测试脚手架 internal/adminsessionauthtest/support.go:该包被 10 个
# 写分级 _test.go 真调用(Resolver/Status 等),但 `deadcode ./...` 不带 -test 看不到跨包测试引用
# → 必然误命中(与上述 memoryStore test-fake 同类)。全量重算基线净 +4(+5 脚手架项,-1 voucher
# WithBurstLimiter 已不再死),DC_MAX 875->879(Owner 可见可审);非死代码堆积,是测试专用脚手架。
# 2026-07-12 B6/B7 收口清债+补录:真死码删除(vertexsa cache 层未接线——adapter 直用 Mint、
# email 枚举助手零消费者、credentialworker 测试专用 wrapper 改由测试直调底层);补录 3 项——
# logsink WithBatch/WithQueueSize 被 3 个 _test.go 真调用(deadcode 不带 -test 看不到,与
# memoryStore/adminsessionauthtest 先例同类),antigravity validateRefreshOAuthConfig 属
# env-gated 车道家族(全部调用者已在 baseline)。DC_MAX 879->882(Owner 可见可审)。
# 2026-07-14 绑定三字段 arc 收口清债+补录:真死码删除(accountadvanced SpecsJSON 零消费者、
# poolaccountadmin BuildMixedRiskParams 平行建参路径从未接线——handler 走 insertProviderAccountWithMixedRiskCheck
# 内联建参)+修 S1016(channel catalog 测试行转换)。补录 2 项:accountadvanced Specs/Keys 被
# cmd/gateway 跨包契约守卫(前端 mirror/OpenAPI/SQL 覆盖三测试)真调用,deadcode 不带 -test 看不到,
# 与 memoryStore/adminsessionauthtest/logsink 先例同类。全量重算收割他片已清债务,净 882→811、
# staticcheck 94→93,两上限同步调低(棘轮只降)。
# 2026-07-14 fallback_class 第 2 步清债:chat executor 已生产接线 TargetClass、
# IsTerminal、AllowTransition，三条分阶段补录自然出列，DC_MAX 814->811。
SC_MAX=93
DC_MAX=811
GOBIN="$(go env GOPATH)/bin"
command -v "$GOBIN/staticcheck" >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@2025.1.1 >/dev/null 2>&1
command -v "$GOBIN/deadcode" >/dev/null 2>&1 || go install golang.org/x/tools/cmd/deadcode@latest >/dev/null 2>&1
# normalize: strip :line:col so the baseline tolerates code movement; drop VCS/compile noise
norm_sc() { "$GOBIN/staticcheck" ./... 2>/dev/null | grep -vE "error obtaining VCS status|buildvcs|\(compile\)$" | sed -E "s/:[0-9]+:[0-9]+:/: /" | sort -u; }
norm_dc() { "$GOBIN/deadcode" ./... 2>/dev/null | sed -E "s/:[0-9]+:[0-9]+:/: /" | sort -u; }

if [ "${1:-}" = "--update" ]; then
  # CI 中一律拒绝重写 baseline(GitHub Actions 自动设 CI=true),杜绝在流水线里洗债。
  if [ -n "${CI:-}" ]; then
    echo "REFUSED: 禁止在 CI 中用 --update 重写 baseline(会把债务洗白)。请本地修复后再提交。"
    exit 2
  fi
  # 本地也要显式 opt-in,避免随手 --update 把债务祖父化。
  if [ "${HUAKAI_ALLOW_BASELINE_REWRITE:-}" != "1" ]; then
    echo "REFUSED: 重写 baseline 需显式 HUAKAI_ALLOW_BASELINE_REWRITE=1(确保是清债后的有意为之)。"
    exit 2
  fi
  norm_sc >"$SC_BASE"
  norm_dc >"$DC_BASE"
  sc_n=$(wc -l <"$SC_BASE")
  dc_n=$(wc -l <"$DC_BASE")
  echo "updated baselines: staticcheck=$sc_n deadcode=$dc_n"
  if [ "$sc_n" -gt "$SC_MAX" ] || [ "$dc_n" -gt "$DC_MAX" ]; then
    echo "WARNING: 重写后 baseline 超过上限(SC_MAX=$SC_MAX DC_MAX=$DC_MAX),正常 gate 会失败。"
    echo "         请改为清债;若确需放宽,显式调高脚本中 SC_MAX/DC_MAX 并在 DEFERRED 文档记录理由。"
  fi
  exit 0
fi

fail=0

# 上限闸:baseline 行数不得超过硬上限(只能降)。膨胀(洗债)立即拦下。
sc_n=$(wc -l <"$SC_BASE" 2>/dev/null || echo 0)
dc_n=$(wc -l <"$DC_BASE" 2>/dev/null || echo 0)
if [ "$sc_n" -gt "$SC_MAX" ]; then
  echo "FAIL: staticcheck baseline 膨胀($sc_n > 上限 $SC_MAX)。baseline 只能下降;修复新增 finding,勿洗进基线。"
  fail=1
else echo "OK staticcheck baseline 行数 $sc_n ≤ 上限 $SC_MAX"; fi
if [ "$dc_n" -gt "$DC_MAX" ]; then
  echo "FAIL: deadcode baseline 膨胀($dc_n > 上限 $DC_MAX)。baseline 只能下降;接线/删除死代码,勿洗进基线。"
  fail=1
else echo "OK deadcode baseline 行数 $dc_n ≤ 上限 $DC_MAX"; fi

new_sc=$(comm -23 <(norm_sc) <(sort -u "$SC_BASE" 2>/dev/null))
if [ -n "$new_sc" ]; then
  echo "FAIL: new staticcheck findings (not in baseline):"
  echo "$new_sc" | sed "s/^/  + /"
  fail=1
else echo "OK staticcheck: no new findings (baseline $sc_n)"; fi
new_dc=$(comm -23 <(norm_dc) <(sort -u "$DC_BASE" 2>/dev/null))
if [ -n "$new_dc" ]; then
  echo "FAIL: new deadcode (not in baseline):"
  echo "$new_dc" | sed "s/^/  + /"
  fail=1
else echo "OK deadcode: no new unreachable symbols (baseline $dc_n)"; fi
[ "$fail" = 0 ] && echo "quality-gate PASS" || echo "quality-gate FAIL — fix new issues or justify+rebaseline"
exit $fail
