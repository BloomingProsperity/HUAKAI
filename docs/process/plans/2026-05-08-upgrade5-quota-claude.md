# 2026-05-08 Upgrade #5 — 二阶段 quota with tier_max_multiplier (claude lane plan)

## Owner directive

"多线并行" — Owner 选 P0 升级路线推进。#5 是核心收费/合规升级。

## 当前现状（基于 schema 与 quota 已有码扫描）

- `backend/sql/migrations/` 已有 quota 相关表（待 codex/sonnet 扫描确认精确字段）
- 当前 quota 是单一限额，VIP 与免费用户共池
- 计费走 `Settler` interface（在 `dispatch_smoke_test.go` 出现）

## 升级目标

- **Stage 1 (base quota)**：免费/默认用户共享 base quota 池
- **Stage 2 (tier_max_multiplier)**：付费 tier (paid_pro / enterprise / 等) 在自身 base 用尽后，自动扩展到 base × multiplier，**不挤占免费池**
- 区分免费 / 付费 / 企业 / VIP 多档，每档独立 multiplier
- multiplier 来源：tenant 配置（DB），失败时 fail-closed（不让 multiplier 缺失变 unlimited）

## Scope

**In**:
- `tenants` 或 `tenant_tier` 表加 `tier_quota_multiplier` 字段（NUMERIC，默认 1.0）
- `quota_engine` 包：核心二阶段判定函数 `Allow(tenantID, tier, requestedTokens) → (allow bool, reason)`
- middleware: chat_completions_handler / 等业务 handler 调用前注入 quota 检查
- 计费 settler: 扣账 + 累计已用量
- migrations: up + down + 守约 down 验证查询

**Out**:
- 不动 ratelimit (短窗口) — 与 quota (长窗口/账单) 是两个层
- 不引入 Redis (用 PG advisory lock + counter table; 引依赖需 Owner 确认)
- 不动 admin UI (P2 任务)
- 不实施 fingerprint / 反爬层 (execution_boundary_c 暂停)

## Atomic 拆分

| atomic | 内容 | 估时 |
|---|---|---|
| **U5-A** | schema migration: tier_quota_multiplier + quota_usage table 或扩展现有 | 60-90 min |
| **U5-B** | core engine `quota_engine` package + Allow() / Charge() / Reset() | 90-120 min |
| **U5-C** | handler middleware 接入 + 失败 fail-closed | 60-90 min |
| **U5-D** | 二阶段语义测试矩阵（base 用尽 → multiplier 扩展 / multiplier 也用尽 → 拒绝 / 跨 tier 不串线 / 并发扣账无 race） | 60-90 min |

总: 4-6 小时跨 4 atomic.

## Success criteria

1. 普通用户 base quota 用尽后被拒绝（429），不影响付费用户
2. 付费用户 base 用尽后扩展到 base × multiplier，扩展额度独立
3. 并发扣账（10+ goroutine）无 double-spend / 漏算
4. multiplier=0 / NULL 时 fail-closed (拒绝，不让其变 unlimited)
5. migration up + down 可重复跑无副作用

## Blast radius

**最高级**——动 schema + 钱包流：
- 错误 `Allow()` 逻辑可能导致免费用户白嫖（financial loss）
- 错误 `Charge()` 可能 double-spend 或漏算
- migration down 必须 idempotent 且无数据丢失

## Failure modes

1. 双扣 (race in Charge) → 用 PG row-level lock 或 advisory lock
2. multiplier 越界 (例如设为 1000x，绕过付费门槛) → CHECK constraint <= 100
3. tier 升级 / 降级 时 quota 计算 → 测试 tier 切换边界
4. 与现有 ratelimit 冲突 (ratelimit 已限 → quota 也算) → 文档明确两者层级关系
5. 跨 tenant 汇聚 vs 独立 → 决策点见下

## Decision points

| 项 | 选项 | 推荐 |
|---|---|---|
| multiplier 挂在哪一层 | user / tenant / route / tier | **tier**（最符合付费档语义）|
| quota 跨账号汇聚还是独立 | tenant 内汇聚 vs account 独立 | **tenant 汇聚**（公司付费就全员共享额度）|
| 失败时 fail-open 还是 fail-closed | open=继续 / closed=拒绝 | **fail-closed**（防白嫖；监控告警 ops 修）|
| multiplier 形态 | 整数 multiplier / 绝对 max_tokens | **multiplier**（兼顾成本调整方便）|
| Stage 1 pool 范围 | 全免费用户共池 vs 各 tenant 一池 | **各 tenant 一池**（防恶意 tenant 拖垮全平台）|

## 设计大纲

### Schema (U5-A)

```sql
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS tier text NOT NULL DEFAULT 'free';
ALTER TABLE tenants ADD COLUMN IF NOT EXISTS tier_quota_multiplier numeric(6,2) NOT NULL DEFAULT 1.0
    CHECK (tier_quota_multiplier >= 0 AND tier_quota_multiplier <= 100);

CREATE TABLE quota_period (
    tenant_id bigint REFERENCES tenants(id),
    period_start timestamptz NOT NULL,
    base_used bigint NOT NULL DEFAULT 0,
    multiplier_used bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (tenant_id, period_start)
);
```

### Core engine (U5-B)

```go
package quota

type Verdict struct {
    Allow      bool
    Stage      Stage  // "base" | "multiplier" | "denied"
    Reason     string // 运维可读
    Remaining  int64
}

type Engine interface {
    // Allow 判断 + 不预扣（reservation 在 commit 时 Charge）
    Allow(ctx context.Context, tenantID int64, tokens int64) (Verdict, error)
    // Charge 实际扣账（成功 commit 后调）
    Charge(ctx context.Context, tenantID int64, tokens int64) error
    // Refund 失败回滚（如 stream 半路 client 断 → 已计但未实交付）
    Refund(ctx context.Context, tenantID int64, tokens int64) error
}
```

### Middleware 集成 (U5-C)

在 `ChatCompletionsHandler` 主流程前: `quota.Allow()` → 失败 429 + `quota_exceeded` body；成功后路由到 forwarder；forwarder 完成后 `quota.Charge()`；client 半路断/超时退款 `quota.Refund()`。

## 测试矩阵 (U5-D)

1. base used < base limit → allow stage=base
2. base used == base limit, tier=free → deny
3. base used == base limit, tier=paid → allow stage=multiplier
4. base + multiplier 用尽, tier=paid → deny
5. multiplier=NULL → fail-closed deny
6. 并发 100 goroutine 扣账 → 总扣账 == 100 × tokens (无 double-spend / 漏算)
7. tier 切换 free→paid → 立即可使用 multiplier
8. migration up + down + up + down → schema 等价

## 引用源

- HUAKAI 内部：现有 quota / settler 接口（待精确扫描确认）
- 思路启发（不 verbatim）：Stripe usage-based billing 文档、AWS service quotas (二阶段 burst) 思路
- 严禁读 sub2api / new-api reference 实现源（CLAUDE.md #11）

Lane: claude
Time: 2026-05-08T<UTC>
