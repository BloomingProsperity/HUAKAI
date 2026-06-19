# Plan — /usage/overview 补 raw success_count + error_count (生态 parity 完整性切片)

- 日期: 2026-06-19
- 作者: Claude PM (autonomous; Owner「你定但不能偏移」+「别反复问」; 收尾挖矿队列候选A)
- 基线: origin/feat/frontend-portal @ a97e62fd
- 分支: feat/usage-overview-counts

## 背景 (禁止凭记忆 — 真码已核)

收尾挖矿队列候选A: `/v1/admin/usage/overview` totals 暴露不完整。
- producer 真实: AggregateUsageOverviewTotalsRow.SuccessCount(db/billing/usage_analytics.sql.go:631 `success_count`, 扫 :645)。
- gap: overview handler 读了 row.SuccessCount 但只喂 successRateText 算 success_rate 串(overview_handler.go:133), overviewTotals struct(:25-32)无 success_count/error_count → 运维拿不到失败请求绝对数(只能从 rate*requests 反推, 有舍入误差)。
- **包内不对称(强一致论据)**: 同包 perf-metrics/summary **已暴露 raw** RequestCount+ErrorCount(perf_metrics_handler.go:46-47/217-218), 且用**完全相同的派生** errorCount = RequestCount - SuccessCount(perf_metrics_handler.go:168)。本切片就是把这个既有派生应用到 overview, 非新发明。

disjoint(仅 usageanalyticshttp + openapi, **无 db/sqlc query 改**——SuccessCount 已在 row), 无 schema 迁移/money/auth/avoidance; 与 proxies 0 碰撞(已核)。

## #16 三镜像 (specifier lane, 本轮新探针 #16-usage-overview-counts 完成)
「admin usage/dashboard overview(窗口聚合)是否暴露 raw success_count + error_count vs 仅 rate」:
- **sub2api@e34ad2b**: 其 **Ops dashboard overview**(/api/v1/admin/ops/dashboard/overview)**暴露一等整数 success_count + error_count_total + error_rate**, 由两条窗口聚合算(COUNT 成功日志表 + COUNT FILTER status>=400 错误日志表), request_total=success+error(ops_dashboard_models.go:32-69, ops_repo_dashboard.go:55-148)。其朴素 dashboard/stats 只有 total_requests 无拆分(dashboard_handler.go:79-104)。
- **new-api@1ac0f58**: 日数据/用量聚合与日志统计读法只数**成功消费类日志行**(按消费类型过滤), 错误走单独错误日志路径不进聚合 → 有请求数但**无一等 error_count、无 rate**(controller/usedata.go:13-67, model/log.go:528-571)。
- **CLIProxyAPI@2a050dc**: **no-equivalent**——纯中继, usage 仅 per-request 事件分发(manager.go:16-56), 有 per-record Failed bool 但从不聚合, 无 overview 端点。

### HUAKAI delta — 生态/parity(诚实小完整性补)
| 维度 | sub2api | new-api | CLIProxy | HUAKAI delta | dimension |
|---|---|---|---|---|---|
| overview 暴露 raw success_count | ✓(ops overview 一等整数) | ✗(只数成功行不标注) | ✗(无 overview) | ✓ 补齐 | 生态(运维完整性) |
| overview 暴露 raw error_count | ✓(COUNT FILTER 错误表) | ✗ | ✗ | ✓ 补齐(=requests-success) | 生态 |
- **delta**: 把 overview totals 补上 raw success_count + error_count → **与最强参考 sub2api 的 ops overview 对齐**, 超过 new-api/CLIProxy。诚实定性: 这是补完整性达 parity, 非新能力。HUAKAI 复用同包既有 error=requests-success 派生(perf_metrics_handler.go:168)保持包内一致。

## 实现范围 (success criteria)
- overview_handler.go: overviewTotals struct + SuccessCount/ErrorCount 字段;overviewTotalsFromRow 映射(SuccessCount=row.SuccessCount, ErrorCount=row.RequestCount-row.SuccessCount, 同 perf_metrics 派生不另加 guard 保持一致)。
- openapi.yaml: overview totals schema + success_count + error_count。
- 测试(变异验证): 扩既有 TestOverviewTotalsTrendWindowAndRatesAreDiscriminating(fixture 已有 4 in-window 请求/3 成功/1 upstream_5xx)断 success_count==3 && error_count==1;删 SuccessCount 映射→0≠3 红(已证)。success_rate 0.7500 单独区分不了 3/1-of-4 vs 75/25-of-100, 故 raw count 才 discriminating。

## blast radius
- 仅 overview_handler.go(+其 test)+ openapi.yaml。无 db/sqlc/迁移/依赖/money/auth/schema。codebudget: +~4 行远 < 600。
- error=requests-success 不加负数 guard: success 是 requests 子集(SQL COUNT FILTER 成功 ⊆ COUNT 全部), 不变量成立;同 perf_metrics_handler.go:168 既有做法。

## 门禁
ultracode 对抗审查零 S0/S1 → 干净基线 fail 0(含 cmd/gateway OpenAPI consistency) → squash → ff。

## Clean-room 出处 (#11(d))
- Source files read: sub2api@e34ad2b {handler/admin/dashboard_handler.go, handler/admin/ops_dashboard_handler.go, service/usage_service.go, service/ops_dashboard_models.go, repository/ops_repo_dashboard.go, service/ops_metrics_collector.go};
  new-api@1ac0f58 {controller/usedata.go, controller/log.go, model/usedata.go, model/log.go};
  CLIProxyAPI@2a050dc {sdk/cliproxy/usage/manager.go, sdk/api/handlers/(tree)}
- 首引 recency#12: 三 SHA 同 [[parity-audit-2026-06-18]] 已核 active@2026-06-18(GitHub API 沙箱不可达, 复用并记 SHA)。
- Lane: specifier(独立 agent #16-usage-overview-counts). Agent: Claude PM. UTC: 2026-06-19
