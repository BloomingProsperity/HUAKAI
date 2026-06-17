# Wave2 — 告警规则 alert-rules 写 CRUD 前端（计划留痕）

- 日期：2026-06-17
- 切片：Slice 8（Wave2 admin 后台补全；收口 alerting 面）
- 分支：`feat/admin-alert-rules`（base `origin/feat/frontend-portal` @ 1eeaa71d）
- 协调锁：`claude-alertrules`
- 选刀依据：proxies 仍活跃 → 避让 provider/channel；按覆盖审计推荐 A 类，alerting 面 events/silences 已覆盖、rules 仅 list（且 adminOps 的 listAlertRules 是 dead code）→ 本刀补 rules 写 CRUD 收口 alerting。

## 后端权威契约（读真码，禁止凭记忆）

- `backend/internal/alertinghttp/rule_handlers.go` + `mount.go` + `alerting/service.go` + `alerting/types.go`
- 端点（前缀 `/v1/admin/alert-rules`）：GET list / POST create(tenant 在 body) / GET {id} / PUT {id} / DELETE {id}(204)
- 鉴权：platform_admin 或 tenant_operator；platform_admin 必带 tenant_id，operator 用 scope。
- 校验 validateRule（service.go）：name 非空；**metric_type 或 metric 至少一**（metricKeyForRule）；comparator∈{gt,gte,lt,lte}；threshold 有限（拒 NaN/Inf；0/负合法）；severity∈{info,warning,critical}（空默认 info）；window_seconds>0；sustained/cooldown≥0；filters=map[string]string。
- DisallowUnknownFields；分页 limit 1-500 默认 50。MetricType 枚举当前仅 `cpu_usage_percent`。

## 借鉴对照（CLEAN-ROOM §11/§12/§16，仅功能形态，未抄码；源经核实）

| 维度 | sub2api@e34ad2b(LGPL) | new-api@1ac0f58(AGPL) | CLIProxyAPI@2a050dc | HUAKAI delta · 维度 |
|---|---|---|---|---|
| 告警规则 | **有**完整 ops 告警规则 CRUD（`routes/admin.go:148-151` GET/POST/PUT/DELETE alert-rules）| **无**告警规则系统（仅渠道健康监控）| 无（纯中继，无等价物）| 按**租户**隔离 + 指标阈值规则（metric/comparator/threshold/window/sustained/cooldown/severity/notify_email/filters）+ DisallowUnknownFields（`alertinghttp/rule_handlers.go`、`alerting/service.go validateRule`）· **生态升级**：多租户可配置告警规则 |

## 文件

1. `frontend/lib/api/alert-rule-form.ts`（新，零依赖）：COMPARATORS/RULE_SEVERITIES/METRIC_TYPES + parseFilters + validateAlertRuleForm + buildCreateBody（tenant 在 body）/buildUpdateBody（部分编辑、无 tenant_id/id）。
2. `frontend/lib/api/adminAlertRules.ts`（新，客户端）：list/get/create/update/delete（自带 adminPut/adminDelete）。专属写 CRUD，不动 adminOps（其 listAlertRules 为 dead code）。
3. `frontend/app/admin/alert-rules/page.tsx`（新）：列表+分页 / 增改弹窗（全字段）/ 删除。**不动 Sidebar.tsx**。
4. `frontend/lib/api/alert-rule-form.test.ts`（新）：纯逻辑单测 + 接线源文断言，全部变异验证。
5. `frontend/package.json`：加 `test:alert-rule`。

## 成功标准 / 风险

- tsc exit 0；`test:alert-rule` 全绿；邻测不破。
- 测试判别性变异实测（10 点）：comparator/severity 白名单、metric_type|metric 二选一、threshold 有限、window>0、sustained/cooldown≥0、filters string-map、tenant 双值防自引用、数值字段是 number 非 string、键集省略、list 锚定 opts.tenant_id、create builder/tenant 位置 —— 每个变异转红、还原绿。
- 对抗审查无未结 S0/S1。
- 爆炸半径：纯前端新增文件，不改后端/共享文件/Sidebar；告警规则=可观测性配置，无 money/auth/quota 直接面。低风险。
