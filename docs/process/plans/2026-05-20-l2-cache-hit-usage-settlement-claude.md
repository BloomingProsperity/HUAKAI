# 2026-05-20 l2-cache-hit-usage-settlement-claude

> CLAUDE.md #9 方案文档 + #10 平行交叉。本文件是 Claude 的实施方案。codex 的
> 独立草案见同目录 `-codex.md`。两份草案独立产出后对比,见下 §平行交叉记录。

| 项 | 内容 |
|---|---|
| Owner directive | "讨论这条路的设计" → 三方综合后选定方向 B → "走 B,先写方案文档再实施" |
| Scope (In) | `usage_records` schema 迁移(0043);sqlc 重生;`CommitCacheHit` 增写 provider-less usage 行;receipt 源 / admin 用量 / obs / DLQ 兼容可空 provider;P2-1 幂等命中 L2 重放;P3 endpoint 版本段;acceptance tests |
| Scope (Out) | 完整持久化 idempotency replay store(Phase E);把幂等从 claim 拆出的大重构(长期);billing_ledger_claims schema(其 provider_account_id/acquisition_token 已可空);参考项目源码;Rust;前端 |
| Success criteria | S1 缓存命中写一条 `settlement_source=response_cache_l2` 的 $0 usage 行;S2 缓存命中请求 GET receipt 返 200($0 收据)而非 202;S3 缓存命中出现在 admin 用量列表;S4 带 Idempotency-Key 的缓存命中重试返 200(L2 重放)而非 409;S5 CHECK 约束:provider 路径两列必非空、cache 路径必为空;S6 普通 provider 请求行为零回归;S7 v16 review 该路径 0 P1/P2 |
| Time estimate | 2-3 个工作日(迁移+sqlc 0.5d / CommitCacheHit 0.5d / receipt+obs 1d / DLQ 0.25d / P2-1 0.5d / P3 trivial / 测试 0.5-1d) |
| Blast radius | **钱路表 `usage_records` schema 迁移**——高风险。影响 receipt / 退款 / admin 用量 / obs / DLQ replay。sqlc 重生使 `InsertUsageRecord` 的 provider 参数变指针,波及全部插入点(编译器可捕获)。 |
| Decision points | (D1) schema 可空——已 Owner 确认走 B;(D2) P2-1 现做还是延 Phase E——**现做**(sub2api 已有完整幂等重放,延后即已知弱于 sub2api);(D3) down 迁移在已有 cache-hit 行后是 fail-fast 还是有损——选 fail-fast。 |

## 平行交叉记录(CLAUDE.md #10)

| | Claude 初稿 | codex 独立草案(bbv2h5c5r) | 参考平台调研 | sub2api 实证 |
|---|---|---|---|---|
| 缓存命中写 usage 行? | ❌ 不写(receipt 改从 billing_event 推导) | ✅ 写(provider-less) | ✅ LiteLLM/Portkey/Helicone/LLMGateway 4/4 全写 | 无响应缓存(不适用) |
| schema 改动 | 不改 | provider_account_id+acquisition_token 受约束可空 | 它们 usage 表本就容忍无上游 | —— |

**冲突 → 解决:** Claude 初稿(不改 schema)被 codex 的 grep 否决——依赖
"committed claim 必有 usage_record" 的下游有 8 处(receipt 派生 / GET receipt /
receipt 校验 / 错配退款 / admin 用量 / obs ListUsage / DLQ replay / 烟测),
不改 schema 等于给 8 处各打特判 = treadmill 本身。参考平台 4/4 印证"缓存命中
应有 usage 行"。三方收敛到 **方向 B**。

**sub2api 实证补充**(`sub2api@dbc8ae65`):sub2api 把幂等(独立
`idempotency_records` 表,存完整响应体,`idempotency_repo.go:21/57/180`)与
计费(请求完成后单次事务,`usage_billing_repo.go:22/108`,无预留阶段)**拆成
两条独立路**;HUAKAI 把幂等+预留+审计揉进一行 claim,这是缓存 treadmill 的
结构性根源。HUAKAI 两阶段更强(防并发超扣配额——sub2api 的已知瓶颈),保留。
但 sub2api 已有完整幂等重放,HUAKAI 不能把 P2-1 延到 Phase E,否则已知弱于
sub2api → **D2 定为现做**。

## 根因(为什么这条路连续 3 轮出问题)

3 个硬约束同时成立:C1 幂等键↔指纹绑定必须先于服务任何响应 → claim 必须在
查缓存前建;C2 缓存命中不能占 pool slot → 缓存检查在 acquire 之前;C3
`usage_records.provider_account_id` NOT NULL → 预获取缓存命中无账号写不了
usage 行。结果:缓存命中是"有 claim、无上游账号、无 usage_record 的 $0 请求",
与"为消耗上游账号的请求设计"的钱路机器形状不契合。v13/v14/v15 每轮只补一个
下游,所以下一轮又冒一个连锁点。方向 B 通过"让缓存命中也有 usage 行"消除根因。

## 实施分解

### 1. 迁移 0043(db,高风险,需 Owner 已确认)

`backend/sql/migrations/0043_usage_records_cache_hit_settlement.up.sql`:
- `usage_records` ADD COLUMN `settlement_source text NOT NULL DEFAULT 'provider_upstream'`
  (现存行全部 provider_upstream,DEFAULT 自动回填正确)。
- `usage_records` ALTER `provider_account_id` DROP NOT NULL。
- `usage_records` ALTER `acquisition_token` DROP NOT NULL。
- ADD CHECK `usage_records_settlement_source_chk`:
  `(settlement_source='provider_upstream' AND provider_account_id IS NOT NULL AND acquisition_token IS NOT NULL)`
  `OR (settlement_source='response_cache_l2' AND provider_account_id IS NULL AND acquisition_token IS NULL)`
  —— 任何其它 settlement_source 值两支都不满足 → 被拒,顺带封死值域。
- 0040 的复合 FK `(tenant_id, provider_account_id)` 是 MATCH SIMPLE(默认):
  provider_account_id 为 NULL 时整条 FK 跳过校验 → **无需改 FK**。
- `.down.sql`:DROP CHECK → DROP COLUMN → `SET NOT NULL`。已有 cache-hit 行时
  `SET NOT NULL` 会 fail-fast 报错(D3:不静默丢数据;回滚前需先 ETL 清 $0
  cache-hit 行)。

### 2. sqlc 重生(db)

`provider_account_id` 可空后 `InsertUsageRecordParams.ProviderAccountID` 变
`*int64`;新增 `SettlementSource` 参数。`InsertUsageRecord` 查询补
`settlement_source` 列。**编译器会标出所有插入点**:Settle、Abort、新
CommitCacheHit。

> 一 commit 一模块例外:迁移+sqlc 单独 commit 会因 billing 调用方类型不匹配
> 断编译,故 commit 1 = 迁移+sqlc+最小调用方适配(db+billing 原子,与之前
> CommitCacheHit 接口变更同理)。

### 3. `CommitCacheHit` 增写 usage 行(billing)

`CommitCacheHit` 签名扩展为携带一个 cache-hit 用量草案(TenantID/ClaimID/
AuditRequestID/RequestedModel/UpstreamModel/Provider/Draft/Fingerprint/
SnapshotVersion)。在同一 Tx 内,UpdateClaimCommitted + InsertBillingEvent 之后
增写一条 `InsertUsageRecord`:`SettlementSource=response_cache_l2`、
`ProviderAccountID=nil`、`AcquisitionToken` 无效、`ActualCost=0`、tokens/model
取自缓存 envelope、`RoutingReason` 含 `cache_hit:true`。handler 侧
`serveL2CacheHit` 用已有的 `nonStreamingUsageDraft(cachedEnv, ...)` 构造草案。

### 4. receipt 源容忍空 provider(audit)

`receipt_storage_pgx.go` / `receipt_formatter.go`:cache-hit usage 行现在存在,
`LEFT JOIN usage_records` 能命中 → receipt 正常派生 $0 收据。调整:provider/
account 字段按可空读取,cache-hit 收据标 `settlement_source=response_cache_l2`、
$0、无逐 token 明细。`ErrReceiptUnavailable` 仅保留给真正 DLQ-pending 情况。

### 5. admin 用量 / obs 让缓存命中可见(observability + gatewayhttp)

审计 `observability.sql`、`obs_queries.sql`:若对 `provider_accounts` 用 INNER
JOIN 会把 cache-hit 行挤掉 → 改 LEFT JOIN;`repository.go` /
`admin_observability_handler.go` 按可空 scan provider。目标:缓存命中出现在
用量视图(消费透明)。

### 6. DLQ payload 兼容可空(dlq)

`UsageRecordPayload` + `marshalUsageRecordPayload` + `dlq/handlers.go` replay:
provider/account 字段改可空、带 `settlement_source`。仅当 cache-hit usage
插入失败回退 DLQ 时才触发,但必须正确。

### 7. P2-1 幂等命中 L2 重放(gatewayhttp,D2 = 现做)

`reserveClaim` 的 `IdempotencyHit` 分支:fingerprint 已校验后,不立即 409 ——
先用 `ex.cacheVendor/upstreamModelID/body` 构造 L2 key 查缓存;命中 → 以重放
模式返回缓存 body + 200(claim 已 committed,不再 CommitCacheHit、不新增
billing/usage/receipt);未命中 → 仍回 `409 replay_without_cache`。完整"无
cache 也能重放"的持久 store 留 Phase E。

### 8. P3 endpoint 版本段(provider)

`apiEndpointSuffix` 改取**首个** API 版本段而非最后一个(`v2-pro` 这类模型名
含 `v数字` 不再被误判为版本段)。trivial。

## Acceptance Tests

- AT-1 缓存命中写 `settlement_source=response_cache_l2`、provider_account_id
  NULL、cost 0 的 usage 行。
- AT-2 缓存命中请求 → GET receipt → 200 $0 收据(非 202)。
- AT-3 缓存命中出现在 admin 用量列表。
- AT-4 带 Idempotency-Key 的缓存命中重试 → 200(L2 重放)。
- AT-5 L2 entry 被逐出后同 key 重试 → 409 replay_without_cache。
- AT-6 CHECK:provider_upstream 行 provider_account_id 置 NULL → 被拒;
  response_cache_l2 行置非空 → 被拒。
- AT-7 P3:模型名 `v2-pro` 不破坏 gemini passthrough endpoint。
- AT-8 回归:普通 provider 请求仍写 provider_upstream usage 行、释放 slot。

## Failure modes & 缓解

- 迁移在生产已有跨租户/孤儿数据时失败 → pre-prod 状态,失败即 fail-fast,
  先 ETL;已与 0040/0041 同模式。
- sqlc 重生波及面被低估 → 编译器强制全标出,逐一适配。
- obs 查询漏改 JOIN → cache-hit 行在用量视图隐身 → AT-3 兜底。
- CHECK 约束写错使普通路径被误拒 → AT-6 + AT-8 双向兜底。
- P2-1 重放误把非缓存命中也当缓存重放 → 仅 L2 实命中才重放,未命中回退 409。

## 回滚

每个 commit 独立可 `git revert`。迁移 0043 有 `.down.sql`,但已写入
cache-hit usage 行后 down 会 fail-fast(D3)——需先清 $0 cache-hit 行。

## Commit 序列(一 commit 一模块)

1. `db` 迁移 0043 + sqlc 重生 + billing 最小调用方适配(原子,防断编译)
2. `billing` CommitCacheHit 增写 provider-less usage 行
3. `audit` receipt 源容忍空 provider
4. `observability` admin 用量 / obs 让缓存命中可见
5. `dlq` payload 兼容可空 provider
6. `gateway` P2-1 幂等命中 L2 重放
7. `provider` P3 endpoint 首版本段
8. 测试随各模块 commit 或末尾统一(AT-1..8)

每 commit 前 `go build ./... && go vet ./... && go test -race` 相关包;
全部完成跑 v16 codex review;review 清 → 推送 origin/claude/phase-1。

## Pre-execution checklist

- [ ] Owner 过目本方案 + `-codex.md`,确认实施
- [ ] 确认 `backend/sql/migrations/` 最大编号仍为 0042(否则顺延)
- [ ] 确认 sqlc 工具链可用(不可用则停下报告,不手改生成文件)
- [ ] 按 commit 序列实施,每步 build/vet/test 留证据(禁假绿)
