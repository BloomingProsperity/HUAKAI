# DEFERRED:quality-gate baseline 存量债与排清计划(2026-06-25)

| 项 | 内容 |
| --- | --- |
| 触发 | 后端 renew 审查(2026-06-24)S1「质量门允许 --update 洗 baseline」+ 实测发现后端 CI `test` job 已红一阵 |
| Owner 决策 | 2026-06-25 拍板「re-baseline 吸收 + 装锁 + 排清债」(三选一) |
| 关联 PR | PR-2(quality-gate baseline 锁死)+ 本文档 |

## 背景:为什么 baseline 被调高(Owner 批准的债吸收)

实测发现:后端 CI 的 `Go test + race + vet` job 在**所有分支**已 fail 一阵——根因是 `quality gate`
step 发现大量新 staticcheck/deadcode 不在 baseline 而 fail,**且该 step 一 fail,后续
`go test -race`、migration round-trip、perf gate 全被 skip**。即后端单元测试在 CI 里**根本没在跑**,
PR 一直无视红 CI 合并(CI 非强制门)。这比审查文档记录的"假绿"更糟。

Owner 拍板路线一:一次性把存量发现吸收进 baseline,**当场恢复 CI 真跑 go test**,同时装上"只降"
上限锁防再膨胀,存量债排进本文档分批清。

baseline 行数变化(staticcheck 2025.1.1 + deadcode,与 CI 同版本生成):

| baseline | 吸收前 | 吸收后 | 上限锁(SC_MAX/DC_MAX) |
| --- | --- | --- | --- |
| staticcheck | 93 | 174 | 174 |
| deadcode | 787 | 907 | 907 |

**这是一次 Owner 批准的债吸收,不是常态。** 上限锁后,baseline 只能降;调高需再次 Owner 批准 +
更新本文档。

## 存量债分类与排清优先级

### A. 真缺陷(优先清,非故意)— staticcheck

**✅ 批1 已清(2026-06-25,staticcheck baseline 174→168,SC_MAX 同步降至 168):**

- ✅ `internal/geminihttp/generate_content.go`:核实后——`r = r.WithContext(ctx)` 的回写值**确实从未被读取**
  (下游 resolveModel/planRoute 均直接收显式 `ctx`),非 context 失效,属死代码,已移除(SA4006+SA4017 消除);
  ST1005 error 串 "Gemini..."→"gemini..." 小写化。受影响包单测通过。
- ✅ `sql/embed.go`:SA9009 核实为**假阳性**——line 4 是包注释 prose(以「// go:embed」开头被误判),真 embed
  指令在 line 12 `//go:embed migrations/*.sql`(无空格、真生效)。改注释「go:embed」→「embed 指令」消除误报,embed 未受影响。
- ✅ `internal/subscription/store_memory.go` + `store_postgres_admin_ops.go`:SA4006 核实为 `newExpires := time.Time{}`
  的**零值初始化被 if/else 两分支必然覆盖**(非 money bug,newExpires 后续正常用于设过期),改 `var newExpires time.Time`
  纯重构、**money 行为不变**;subscription 单测 + 集成测试(含 store_postgres_admin_ops_integration)全通过。

**✅ 批2 已清(2026-06-25,staticcheck baseline 168→162,SC_MAX 同步降至 162):**

- ✅ ST1018(零宽字符 U+200B):核实后——`sensitiveobfuscate/obfuscate.go`(生产)的 `zwsp` 常量 +
  `obfuscate_test.go` 的同名常量 + `dispatch_body_controls_test.go` 的 `"b<ZWSP>anned"` 都是**有意**用零宽字符
  (混淆器拿 ZWSP 把敏感词切开)。改用 `\u200b` 转义字面(字节序列完全相同),消除源码里不可见字符的隐患;
  sensitiveobfuscate 单测全过、行为不变。
- ✅ SA1012(传 nil Context)分两类:
  - `error_rule_eval_test.go` ×5:惰性 nil ctx(`HandleUpstreamError` 内 `_ = ctx` 显式弃用),改 `context.TODO()`、行为不变。
  - `clientid_test.go` / `postgres_proxy_resolver_unit_test.go` ×3:**有意 nil 安全测试**(测 nil-context/nil-receiver 不 panic、
    不 fail-open),保留 nil,把无效的 `//nolint:staticcheck`(本仓无 golangci、对 standalone staticcheck 无效)改成
    staticcheck 原生 `//lint:ignore SA1012`,使豁免真正生效。

**✅ 批3 已清(2026-06-25,staticcheck baseline 162→156,SC_MAX 同步降至 156):**

- ✅ SA4000(`!=`/`==`/`||` 两侧表达式相同)×6:`cmd/gateway/retry_budget_wiring_test.go`、
  `internal/auditledger/acceptance_test.go`、`internal/provider/anthropic/session_id_test.go`(×2)、
  `internal/retrybudget/budget_test.go`、`internal/tlsfpresolve/resolver_test.go`。核实**均非笔误**:都是
  有副作用的有状态调用(budget.Allow 消费配额、cachedSessionID 缓存)或确定性守卫(auditLedgerAdvisoryLockKey/
  pickIndex 须 `f(x)==f(x)`)。改用「两次调用各存变量再比」(`a:=f(); b:=f(); if a!=b`)消除假阳,
  行为不变(受影响 5 包单测全过)。

**⚠ 本批副产物:修了 #166 引入的 CI 失败回归。** 跑 cmd/gateway 测试时发现 `openapi_consistency_test`
自 #166 起一直 fail——#166 加的 `/v1/me/usage-records` 路由没补进 `docs/openapi/openapi.yaml`(impl_only 非空)。
已在 spec 补该端点声明(镜像 `/v1/me/usage`,改 session 鉴权+跨全部 key 描述),cmd/gateway 测试恢复绿。
教训:**worktree 里裸 `staticcheck ./...` 因 buildvcs 报错跳过 cmd/gateway(被 norm_sc 的 `(compile)` 滤掉),
须带 `GOFLAGS=-buildvcs=false` 或用 quality-gate.sh 才看得全;且加后端路由后必跑 `go test ./cmd/gateway/`(openapi 一致性)。**

**staticcheck 类 A/批1-3 清债至此收尾**(剩类 B referral_reward money-coupled + 类 C deadcode,均 Owner-gated/需逐个判断)。

### B. 未接线的 money-coupled 子系统(Owner-gated 取舍)— deadcode

- `internal/community/invitation/referral_reward_*.go`:整套推荐奖励(referral reward)实现 unreachable
  (config/store/qualification 共数十个 unreachable func/const/type)。**money-coupled**(兑付 credit 余额 +
  billing ledger)。去留(接线启用 还是 删除)属 Owner-gated,不在排清里擅自动;待 Owner 单独定。

### C. 故意保留的备用实现 — deadcode

- 各 `*/memory_store.go`(alerting/announcement/channelhealth/passkey/subscription/usernotice/voucher/
  routeadmin 等):内存版 store 作为 postgres store 的对照/测试替身,生产未接线。多为有意保留。
  排清时决定:要么有测试消费(保留)、要么删。
- 各 `With*` options(WithClock/WithNow/WithCeremonyEngine 等):测试注入点,部分未被消费。
- 未接线的 `MountRoutes`/`NewRouter`(adminhttp channel/provider catalog、proxyadminhttp、
  modelbindingadminhttp、orphanreconcilehttp 等):建了未挂载的 admin 路由,属 §6 碰撞包/Owner-gated 路由波,
  按既有排期处理,不在本清债里擅自接线。

## 排清计划(分批,baseline 只降)

1. **批1(真 bug,本类 A)**:逐个核实 + 修 gemini/embed/subscription 的 SA 类真缺陷,修一项 baseline 减一项,
   每批后 `HUAKAI_ALLOW_BASELINE_REWRITE=1 scripts/quality-gate.sh --update` 并把 SC_MAX/DC_MAX **调低**。
2. **批2(故意 deadcode,本类 C)**:有测试消费的保留并加豁免说明;无消费的删除。baseline 随之下降。
3. **referral_reward(本类 B)**:单独 surface Owner 决定接线 or 删除(money-gated),不混入常规清债。

## 不变量

- baseline 只能降;任何调高 = 再次 Owner 批准 + 更新本文档(脚本 SC_MAX/DC_MAX 是审批面)。
- `quality-gate.sh --update` 在 CI 中被拒,本地需 `HUAKAI_ALLOW_BASELINE_REWRITE=1` 显式 opt-in。
- 排清只让 baseline 下降,不得借机吸收新债。
