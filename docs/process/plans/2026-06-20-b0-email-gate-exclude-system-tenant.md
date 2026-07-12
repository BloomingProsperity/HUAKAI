# B0 修复:生产 email 门排除系统伪租户(tenant 0)

日期:2026-06-20
分支:`fix/b0-email-gate-exclude-system-tenant`(base `feat/frontend-portal` @085492c9)
授权:Owner 签字"按推荐来"(采纳 surface 的选项 1)。多角度调查 workflow `wkl63lxvv`(4 agent/三家对照/对抗边界/裁决,高置信、零 S0/S1)。

## 1. 问题(B0,S1 真 blocker)

全新迁移库 production 模式拒启:迁移 `0030_pricing_versions_public_scope` 播种 `tenant id=0 'public-pricing' status=active`(公开定价 scope 的**系统哨兵**,非真客户;迁移 0008 注释直呼 'tenant_id=0 sentinel')。生产 email 门 `ValidateProductionReleaseGate`(`internal/email/sender_factory.go:213`)→ `ListActiveTenantIDs`(`internal/email/settings_store.go:112-135`,SQL `WHERE status='active' AND deleted_at IS NULL`,**不排除 id≤0**)遍历所有 active 租户要求各自配齐 SMTP;但唯一配置入口 admin API `PUT /v1/admin/email/settings` 对 `tenant_id≤0` 两层硬拒(handler:185-187 + store Save/List)。→ tenant 0 永远过不了门 = 生产永久拒启,无运维 workaround。

## 2. 修法(最小正确,additive,可逆,不动数据/schema)

`internal/email/settings_store.go` 的 `ListActiveTenantIDs` SQL 追加 `AND id > 0`,排除 id≤0 系统伪租户;加中文注释说明意图。

```
SELECT id FROM tenants WHERE status = 'active' AND deleted_at IS NULL AND id > 0 ORDER BY id
```

## 3. 为何正确且完整(调查证据)

- **唯一 id≤0 租户是 0030 哨兵**:`tenants.id` 是 bigserial 从 1 递增(0001:16),自动取号永不产 0/负;全仓唯一显式非正 id INSERT 就是 0030。无任何合法 id≤0 真客户租户(对抗 lane `negativeOrZeroLegitTenant=false`)。
- **语义与既有口径一致**:`tenancy/bootstrap.go:81` 工作租户判定本就是 `WHERE id > 0 AND deleted_at IS NULL`;`bootstrap_integration_test.go:30` 注释"既有工作租户(id>0)"。`id>0` 是本仓"工作租户"既定不变量。
- **消死锁**:admin 配置 API 两层拒 tenant_id≤0;加过滤让"门要求配置的集合"恰等于"API 能配置的集合"。
- **不丢功能**:tenant 0 无 users,邮件只经 `user.TenantID` 发(sender_factory.go:73-166),哨兵从不发验证邮件。
- **不碰定价 scope**:不改 tenant 0 那行(status 原样保留);公开定价读 `billing_pricing_versions.is_public` 不读 `tenants.status`。
- **接口专属、唯一消费者**:`ListActiveTenantIDs` 是 `internal/email` 包 `SettingsStore` 接口方法(非通用租户 API);唯一生产消费者 = `ValidateProductionReleaseGate`,自动受益;另两处是测试桩,不受 SQL 改动影响。
- 对抗 lane 5 条攻击(误伤合法负 id 租户/tenant0 需发邮件/破坏定价 scope/清空列表误判/测试遮蔽)**全被驳回**。

## 4. 测试(变异证)

email 包**当前无 integration_pg 测试**;现有 `TestAT_EMAIL_006_007` 用 fakeStore 绕过 SQL,**无法判别** SQL 过滤(删了仍绿)。故**新增** `internal/email/settings_store_integration_test.go`(`//go:build integration_pg`):
- 复刻 `openTestPool`/`hideWorkingTenants`/`deleteTenant` 范式;隔离既有工作租户模拟干净库。
- 确保哨兵 tenant 0 active;造一个正数 active 工作租户并经真 `Save` 写加密 SMTP(verify=true)。
- **AssertA**:`ListActiveTenantIDs` 结果**不含 0**、**含**该正数租户。
- **AssertB**:`ValidateProductionReleaseGate` 返回 nil(0 被排除,正数租户配齐)。
- **变异**:去掉 `AND id > 0` → AssertA 含 0(RED)+ AssertB 因 tenant 0 未配置失败(RED)。两条都判别。

跑法:`HUAKAI_DATABASE_URL=postgres://huakai:huakai@localhost:5432/huakai?sslmode=disable go test -tags=integration_pg -count=1 ./internal/email/`

## 5. 验收 + 门禁

- build/vet 绿;codebudget 不超(改 1 行 + 新增 1 测试文件)。
- 变异证测 RED→GREEN。
- 对抗审查零 S0/S1 + 干净基线(`-count=1`)。
- **production 新库一把起栈实测**:迁移→production 直接起(tenant 0 不再卡门)→ healthy(只要至少一个正数工作租户配了 email;tenant 1 'default' 由 bootstrap 种,运维配它即可)。

## 6. 范围外(单独 surface,不在本 PR)

- **门降级为惰性**(三家 sub2api/new-api 都把 email 未配做成请求时惰性错误、无启动门):default-behavior flip = Owner-gated,**单独问 Owner**,本 PR 只做解锁过滤。
- **CI 是否真跑 email 包 integration_pg**:需确认(否则新测不执行);纳入方案 A/CI follow-up。
- 方案 A(prod compose migrate sidecar + .env.example + README 更正 + 部署文档):**另起独立 PR**。
