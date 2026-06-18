# Plan — 自助 /quota 多维窗口读 (F-OPS-001 parity L2→L3)

- 日期: 2026-06-18
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」)
- 基线: origin/feat/frontend-portal @ cc1c32ca
- 分支: feat/quota-multimetric-windows

## 背景 + 真现状核实 (禁止凭记忆)

用户自助 `GET /v1/me/quota`(mequotahttp)当前**只返 cost_usd 窗口**。底层 sqlc 查询 `ListCurrentQuotaWindowsForScope`
(backend/sql/queries/quota.sql:36-71)WHERE 写死 `qp.metric = 'cost_usd'`(:71)。配额引擎其实已把
requests/cost_usd/tokens_estimated/concurrency 当一等 policy metric(quota/types.go:34, policy.go:152),
窗口表 quota_windows 已为所有 metric 存计数器。`CurrentWindowRead` 已携带 `Metric` 字段(pg_store_map.go
currentWindowReadFromDB 设 Metric: policy.Metric),只是 handler 的 windowView 没暴露(因现在全是 cost_usd)。

→ F-OPS-001 L2→L3 = 把 /quota 从「只 cost_usd 窗口」补到 **requests + tokens_estimated 窗口维度**。

**concurrency 明确延后**(它是 slot 模型 quota_concurrency_slots,非 window 累积,不同数据形状)—— 三镜像独立验证此 split
(下文),记 DEFERRED 不硬塞。

非 money/auth/schema(列与 policy metric 已存,无迁移)/avoidance(mequotahttp 与 proxies 分支 0 碰撞,已核);只读。

## #16 三镜像研究 (clean-room specifier lane, 已完成)

### 首引 recency (#12, 核验 2026-06-18 UTC; 三者皆 live, 见 [[parity-audit-2026-06-18]] 同 SHA)
sub2api@e34ad2b / new-api@1ac0f58 / CLIProxyAPI@2a050dc(archived/disabled=false, pushed_at 均 2026-06-18)。

「per-key 多维(请求数/token/费用)配额或用量读」:
- **sub2api@e34ad2b (默认 tiebreaker)**: 把「quota(USD-only 限额+窗口)」与「usage stats(多维分析)」拆成两套数据模型+两个端点。
  配额读(server/routes/user.go:49 `/user/platform-quotas`; handler/api_key_handler.go:113 `/keys/:id`)**只返 USD**
  (cap/used/窗口重置);多维读(handler/usage_handler.go:305 `/usage/stats`)返 requests+token+cost **但无 cap/remaining/窗口**。
  **无单一读融合三维**;**requests/token 无任何 cap**(限额只在 USD)。concurrency 仅在并发拒绝错误路径(concurrency_error_response.go:12)体现,
  从不入读。
- **new-api@1ac0f58**: token 行只存 cost/quota 整数(model/token.go:14-28),**无 per-token 请求/ token 计数**;两个单 token 读
  (controller/token.go:97,118)**只返 quota 维**(其中一个单 token 状态读甚至把已用量硬编 0)。多维仅经 logs 聚合(controller/log.go:125
  的自助多维统计读)返 quota + rpm + tpm,**rpm/tpm 硬窗到最近 60s**(model/log.go:569)= 瞬时速率而非周期总量,且是另一个端点。concurrency 不入读。
- **CLIProxyAPI@2a050dc**: **no-equivalent(已证)** —— api-key-usage(api_key_usage.go:45)只报 success/failed 请求数且 keyed by
  **上游凭据**非 client key;无 client-key quota/remaining 概念;usage record 带 token 但 fire-and-forget drain 非聚合读。

### 取舍 + HUAKAI 融合升级 delta
| 维度 | sub2api | new-api | CLIProxyAPI | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| 多维配额读 | quota(USD-only cap+窗口)与 usage(多维无 cap)分两端点 | token 读只 quota;多维经 logs 聚合(rpm/tpm 60s 窗) | no-equiv | **一个 /quota 读返 requests/tokens/cost 三维各自 cap+consumed+remaining+窗口**;引擎把三者当统一 window-accumulation metric | 架构(统一多维配额窗口 vs 镜像的 cost-cap-vs-usage-analytics 二分) |
| requests/token 限额 | 无(只 USD cap) | 无(只 quota) | 无 | **有**(quota_policies 支持 requests/tokens metric+窗口+cap) | 架构 |
| concurrency 入读 | 否(slot/拒绝) | 否 | n/a | 否(同延后,镜像验证此 split 正确) | — |

镜像均把 concurrency 排除在配额读外 → **验证 HUAKAI 延后 concurrency 是对的**。HUAKAI 暴露三维 cap+remaining 于一读 = **超出**
两镜像表面(它们 requests/token 无 cap、需另端点),非追平。

## 实现范围 (success criteria) — 不偏移: 老路径行为字节级不变
- **sqlc 查询参数化(单查询,非复制)**: backend/sql/queries/quota.sql 的 `ListCurrentQuotaWindowsForScope` WHERE
  `qp.metric = 'cost_usd'` → `qp.metric = ANY(@metrics::text[])`(mirror 既有 ANY 模式)。`sqlc generate`(/home/ubuntu/go/bin/sqlc)重生成。
- **store 双方法**: 老 `ListCurrentWindowsForScope`(pg_store.go)内部传 `Metrics: []string{"cost_usd"}` → 行为与
  `= 'cost_usd'` 等价(`x = ANY(ARRAY['cost_usd'])` ≡ `x = 'cost_usd'`),**subscription(purchase.go:226)/key-control
  (key_control_service.go:127)零行为变化**;新 `ListCurrentWindowsForScopeMetrics(ctx,...,metrics []Metric)` 传请求 metric。
- **mequotahttp**: Store 接口换成新方法;handler 用 [MetricRequests, MetricCostUSD, MetricTokensEstimated] 调它;
  windowView += `metric` 字段;toWindowView 投影 `w.Metric`(已在 CurrentWindowRead)。
- **OpenAPI**: /v1/me/quota 响应 windowView += metric 字段(枚举 requests/cost_usd/tokens_estimated)。

强测试(变异验证): handler(stub 返三维窗口 → 响应含 metric + 三维 Cap/Consumed/Remaining 各异防假绿)+ store 集成
(integration_pg: 播 requests+tokens+cost policies+windows → 新方法返三维、老方法仍只 cost_usd[守 subscription 不变]、值各异)。

## blast radius
- quota.sql 1 查询参数化 + sqlc 重生成;pg_store.go(老方法 1 行 + 新方法)+ store 接口 + mequotahttp(handler+接口+测试)
  + openapi.yaml。**老查询语义经 ANY(['cost_usd']) 字节级等价 + 测试守护**;**不动 subscription/key-control/裁决/settle**;
  无 schema 迁移;只读。concurrency 延后记 DEFERRED。

## 门禁
ultracode 对抗审查零 S0/S1 → 重跑干净基线 fail 0(含 cmd/gateway OpenAPI 一致性 + quota 集成真 DB)→ squash → ff。

## Clean-room 出处 (#11(d))
- **Source files read** (paraphrase-only, 无逐字标识符):
  - sub2api@e34ad2b: server/routes/user.go, handler/api_key_handler.go, handler/usage_handler.go, concurrency_error_response.go
  - new-api@1ac0f58: model/token.go, controller/token.go, controller/log.go, model/log.go
  - CLIProxyAPI@2a050dc: api_key_usage.go
- **Lane**: specifier (读源 → 行为摘要; 由三个独立 specifier-lane agent 产出)
- **Agent**: Claude PM (orchestrator)
- **UTC**: 2026-06-18
