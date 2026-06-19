# Plan — L2 响应缓存指标接入 Prometheus+告警快照 (F-CACHE-001 激活可观测性)

- 日期: 2026-06-19
- 作者: Claude PM (autonomous; Owner 选 F-CACHE-001 方向; 核实发现 exact-key L2 缓存已建+parked, 走 B-first 激活可观测切片)
- 基线: origin/feat/frontend-portal @ 0071d2d7
- 分支: feat/bridge-l2-cache-metrics

## 背景 (禁止凭记忆 — 真码已核, 2 调查 agent)

Owner 选 F-CACHE-001 响应缓存方向。核实(记忆 [[verify-own-state-not-just-mirrors]]): **exact-key 非流式 L2 响应缓存早已建+合并在 feat/frontend-portal**(backend/internal/cache/ + gatewayhttp seams), flag 默认关 [PARKED]。

**调查结论(2 只读 agent)**:
- **parked 原因 = awaiting Owner 激活决策, 非未决正确性问题**: [PARKED] 约定(docs/01-STANDARD-PROCESS.md:33「高危 money/auth/schema→park 待 Owner」)= 已建已测已审已合、默认关待 Owner 签字, 因它触 billing ledger。开发期发现的顾虑(intra-tenant 投毒 b3d5dd5f、tenant-mismatch b72abdba、跨协议 key 碰撞 8224d6c3)**均在同提交链修掉+变异测试**。
- **billing-on-hit 正确**: 命中按 $0 cost 结算(ActualCost=zero)+ 全 usage_record/billing_event/receipt(SettlementSource='response_cache_l2'), 无双扣/无零成本却扣费; Owner 确认方向 + 4/4 参考项目(LiteLLM/Portkey/Helicone/LLMGateway)同写 $0 spend row。
- **激活可观测缺口(activation-readiness)**: L2 cache hit/miss/size 指标(cachemetrics/l2.go:32-34 expvar.Map, 按 vendor=X,model=Y 标签)**只到 /debug/vars + admin /stats, 未进 otelbridge.bridgeCounters() → 不进 Prometheus 导出、不进告警快照 → 运维无法对缓存命中率/容量压力设告警规则**。对比: 无关的 prompt-cache cache_token_count 已 bridge(expvarbridge.go:138-146)。

**本切片(B-first 激活可观测, 非激活默认)**: 把 L2 cache hit/miss/size bridge 进 otelbridge → Prometheus + 告警快照, 运维启用缓存前/中能对其健康设告警。**不翻默认 flag(行为变更=Owner-gate)**。

disjoint(仅 otelbridge, 与 proxies 0 碰撞, 同 PR#48/#49 已验); 无 schema/money/auth; 不动 cache 核心/网关/billing。

## #16 (引用 PR#48/#49 已建 bridge 模式, 非重跑)
本切片是把 HUAKAI **自有既有** L2 expvar 指标路由进 HUAKAI 自有告警引擎 = 内部 plumbing, 非参考派生新功能(被观测的 L2 缓存本身已在 2026-05-15 设计计划 #16'd 引 LiteLLM/Portkey/Helicone)。bridge-to-alert 机制的 #16 已在 PR#48(runtime gauges)/#49(budget fail-open)做: 三镜像无一把内部运维信号(fail-open/runtime/cache-health)做成一等 alert-rule subject(sub2api alert enum 无此键/new-api 无通用引擎/CLIProxy 无)→ HUAKAI bridge-to-alert-engine 是 架构 delta。本切片复用同机制扩 L2 cache 指标, 不另起 reference-derived 设计。

## 设计 + 实现范围 (success criteria)
- otelbridge/expvarbridge.go: 新 readExpvarMapSum(name) 助手(汇总 labeled expvar.Map 全 *expvar.Int → 单 flat 值) + bridgeCounters() 加 3 条: huakai_cache_l2_hit_total / miss_total(单调计数, 跨标签求和仍单调)/ size_bytes(gauge, 同 dlq depth gauge 经此 bridge 之先例)。
- 测试(变异验证): TestL2CacheMetricsBridgedToPrometheusAndAlertSnapshot —— 每 map 2 标签证 SUM(非读单 key); 同一测试覆 Prometheus scrape + 告警 snapshot 两腿避 expvar 跨测试污染。变异: sum→单值(last-wins)→ hit=4≠7 红(已证, grep 确认 1 行变); 删 bridge 条目→指标缺→红。

## blast radius
- 仅 otelbridge/expvarbridge.go(+test)。bridgeCounters() 同供 RegisterBridge(Prometheus ObservableCounter)+ 告警 Snapshot; size_bytes gauge 经 counter instrument 语义略不精=既有 dlq depth 先例非新债。ops002_bridge_test 按具体名匹配非总数, +3 不破。无迁移/依赖/money/auth/schema/openapi(指标非 API 响应)。codebudget: +~25 行远 < 600。
- **不激活缓存**(HUAKAI_CACHE_L2_ENABLED 默认仍关); 缓存关时这些 expvar map 为空 → bridge 读出 0(正常)。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI) → squash → ff。
**big-build 分阶段**: 本切片是 F-CACHE-001 激活可观测半。后续 surface Owner: 缓存已建+安全+billing 正确+现可观测可告警, 激活=翻 HUAKAI_CACHE_L2_ENABLED(行为变更需 Owner 签字)+ 注意 in-memory(单实例/重启丢)+ 非流式 only + scope=apikey 默认。或 Owner 若要语义缓存(A)另起。

## Clean-room 出处 (#11(d))
- 内部 plumbing 无新读参考源; 被观测 L2 缓存的 #16 已在 docs/process/plans/2026-05-15-f-cache-001-l2-cache-{claude,codex}.md(引 LiteLLM/Portkey/Helicone)。bridge-to-alert #16 见 [[parity-audit-2026-06-18]] PR#48/#49 结论。
- Lane: 本切片纯 HUAKAI 内部, 无 reference 源读。Agent: Claude PM. UTC: 2026-06-19
