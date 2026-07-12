# Wave2 — 告警静默 alert-silences admin CRUD 前端（计划留痕）

- 日期：2026-06-17
- 切片：Slice 7（Wave2 admin 后台补全）
- 分支：`feat/admin-alert-silences`（base `origin/feat/frontend-portal` @ 7ae74f9e）
- 协调锁：`claude-alertsilences`
- 选刀依据：proxies 分支仍活跃 → 避让 provider/channel/代理；复查 adminhttp，alerting 面【部分覆盖】（adminOps.ts 已有 listAlertEvents/listAlertRules/manualResolveAlertEvent 只读+消解），缺口=alert-rules 写 CRUD（schema 复杂，留后续刀）+ **alert-silences 全套（完全零覆盖，最小自包含）**→ 本刀做 alert-silences。

## 后端权威契约（读真码，禁止凭记忆）

- `backend/internal/alertinghttp/silence_handlers.go` + `mount.go` + `helpers.go` + `alerting/service.go`
- 端点（前缀 `/v1/admin/alert-silences`）：
  - GET `?tenant_id&limit&offset` → `{object:"alert_silences_list",items,limit,offset}`
  - POST（tenant_id 在 **body**）→ 201 silence
  - DELETE `/{id}?tenant_id` → 204 No Content
- 鉴权：platform_admin 或 tenant_operator（helpers.go resolveAdmin）；platform_admin 必带 tenant_id，tenant_operator 用 scope（tenantFromValue/tenantFromQuery）。
- 校验（service.go validateSilence:418）：tenant>0；rule_id 若给须>0（可选）；starts_at/ends_at 非零且 **ends 严格晚于 starts**；reason/platform/group_id/region 自由（trim，不强制非空）。
- 请求体 **DisallowUnknownFields**（helpers.go decodeRequest）→ 前端只能发已知字段。
- 分页 limit 1-500 默认 50；body MaxBytes 64KB。

## 借鉴对照（CLEAN-ROOM §11/§12/§16，仅功能形态，未抄码；源经核实）

| 维度 | sub2api@e34ad2b(LGPL) | new-api@1ac0f58(AGPL) | CLIProxyAPI@2a050dc | HUAKAI delta · 维度 |
|---|---|---|---|---|
| 告警静默 | **有**完整 ops 告警系统含作用域静默：`CreateAlertSilence`(`service/ops_alerts.go:127`) 按 rule/platform/group/region 作用域 + `IsAlertSilenced` 抑制(`:154`)，POST `/admin/ops/alert-silences`(`routes/admin.go:155`) | **无**告警静默系统（无 alert-rule/silence；仅渠道健康监控） | 无（纯中继，无等价物） | 与 sub2api 作用域静默对齐（rule/platform/group/region）+ **按租户隔离** + starts/ends **时间窗** + DisallowUnknownFields 严格请求体（`alertinghttp/silence_handlers.go`、`alerting/service.go:209-228,418`）· **生态升级**：多租户运维静默 |

注：sub2api 默认 tiebreaker（最成熟）。作用域维度同（rule/platform/group/region），但 **sub2api 强制 rule_id>0 + 非空 platform**（ops_alerts.go:137-141），HUAKAI 全作用域【可选】（仅强制 tenant+时间窗），允许「租户级全静默」——这是 HUAKAI 的行为 delta（更宽松）+ 租户隔离。alert-rules 写 CRUD（metric/comparator/severity/threshold/多窗口/filters，schema 复杂）留作后续独立刀。

## 文件（每个标注落点）

1. `frontend/lib/api/alert-silence-form.ts`（新，零依赖）：isProvidedDate、parsePositiveInt、validateAlertSilenceForm（starts/ends 必填 + ends>starts 严格 + rule_id 正整数）、buildSilenceBody（tenant 在 body、精确键集、时间→RFC3339、可选字段省略）。
2. `frontend/lib/api/adminAlertSilences.ts`（新，客户端）：list（apiGet）/create（apiPost）/delete（自带 adminDelete，204→void）。
3. `frontend/app/admin/alert-silences/page.tsx`（新）：列表+分页 / 新建弹窗（reason/starts/ends/rule_id/platform/group_id/region）/ 删除。无编辑（后端无 update）。**不动 Sidebar.tsx**（避让 proxies 分支）。
4. `frontend/lib/api/alert-silence-form.test.ts`（新）：纯逻辑单测 + 接线源文断言，全部变异验证。
5. `frontend/package.json`：加 `test:alert-silence`。

## 成功标准 / 风险

- tsc exit 0；`test:alert-silence` 全绿；邻测不破。
- 测试判别性变异实测（9 点）：parsePositiveInt 各非法形态、starts/ends 必填、跨字段 ends>starts（含严格相等）、rule_id 守门、tenant 双值防自引用、toRFC3339 用 datetime-local 形 fixture 证真规整、可选字段省略键、list 锚定 opts.tenant_id、create builder/tenant 位置 —— 每个变异转红、还原绿。
- 对抗审查无未结 S0/S1。
- 爆炸半径：纯前端新增文件，不改后端/共享文件/Sidebar；告警静默=可观测性运维，无 money/auth/quota 直接面。低风险。
