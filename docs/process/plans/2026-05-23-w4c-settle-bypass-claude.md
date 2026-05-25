# W4c 计划（Claude lane）—— Settle 旁路堵塞

## 一、目标

W4c 把 trust-chain fail-closed 落到 money-path 入口三条路径（总线 / direct-settle / cache-hit）同时堵。前置 W4a-4 已交 AuditLedgerResult 三态 + DLQ intent；W4c 完成「事件引用 + 三处路径校验」闭环。

spec 来源：`docs/process/plans/2026-05-22-w4-trust-ledger.md:489` §7。

## 二、范围（文件级）

1. `backend/internal/eventbus/types.go`
   - 新增 `RequestCompletionEvent.AuditLedgerDLQRef string`。
   - `normalized()` 增加 `releaseMode` 形参（spec §7 rev2，eventbus 不反向 import `cmd/gateway`）。

2. `backend/internal/eventbus/audit_ref.go`（新文件）
   - 新增 `ValidateMoneyPathAuditRef(event *RequestCompletionEvent, releaseMode ReleaseMode) error`。

3. `backend/internal/gatewayhttp/chat_completions_billing.go:155-168`（冻结包，只改既有）
   - direct-settle 前调 `ValidateMoneyPathAuditRef`。

4. `backend/internal/gatewayhttp/chat_completions_handler_headers.go:183-199`（冻结包，只改既有）
   - cache-hit 前调 `ValidateMoneyPathAuditRef`。

5. `backend/internal/observability/audit_logger_handler.go:69`
   - `requireRef` 改为「`AuditLedgerID` 或 `AuditLedgerDLQRef` 之一非空」。

6. `backend/cmd/gateway/middleware.go:192-194`
   - production 默认启用 `WithRequiredAuditRef()`。

7. Feature-flag 灰度逃生口（D1 决定介质）
   - 启用即写一条结构化 mandatory reconciliation 日志，带 `RR-W4-001` 引用。

8. `docs/10_RISK_REGISTER.md`
   - 新增 `RR-W4-001` 占位（reconciliation 机制本体留后续切片）。

## 三、冻结包合规

- `eventbus` / `cmd/gateway` / `observability`：非冻结，可加新文件。
- `gatewayhttp`：冻结，仅改既有 `chat_completions_billing.go` 与 `chat_completions_handler_headers.go`，不加新源文件；测试加既有 `_test.go` 或单独配套（测试文件不算新功能）。
- `gateway` / `proto`：本切片不触。

## 四、三路径覆盖证明

三处都加 `ValidateMoneyPathAuditRef`：

1. 总线：`eventbus.normalized()`
   - `Kind==RequestCompletion` 时 + `releaseMode` 形参。

2. direct-settle：`gatewayhttp/chat_completions_billing.go:155-168`
   - `settleCompletion()` 在 bus nil / no handler / closed / 队列满分支校验。

3. cache-hit：`gatewayhttp/chat_completions_handler_headers.go:183-199`
   - `CommitCacheHit()` 路径校验。

## 五、风险测试

共 5 条，每条配 mutation 自检：

1. T1：money-path event 两引用皆空 + production → `normalized()` 返回 `ErrMoneyPathNoAuditRef`。
   - Mutation：删 `normalized()` 内 `ValidateMoneyPathAuditRef` 调用 → T1 变红。

2. T2：direct-settle 路径（stub bus=nil）+ 两引用皆空 + production → billing repo `Settle()` 调用次数 == 0。
   - 判别 fixture：断言调用次数。
   - Mutation：删 direct-settle 内校验 → 调用次数 == 1 → T2 变红。

3. T3：cache-hit 路径 + 两引用皆空 + production → `CommitCacheHit()` 调用次数 == 0。
   - Mutation：删 cache-hit 内校验 → T3 变红。

4. T4：三路径带 `AuditLedgerDLQRef != ""` → 全部放行（`Settle` / `CommitCacheHit` 各调一次）。
   - 判别性：与 T1/T2/T3 同 fixture，仅 `DLQRef` 字段从 `""` 翻成 `"dlq:xyz"` → 行为反转。

5. T5：dev 模式 + 两引用皆空 → 三路径全放行。
   - 判别性：与 T1/T2/T3 同 fixture，仅 `releaseMode` 从 production 翻 dev → 行为反转。

## 六、提交切片

一 commit 一模块，严守 `commit_naming_v2`，无 PASS/阶段号：

1. C1：eventbus AuditLedgerDLQRef 事件字段与有效账本引用校验
   - `types.go` 加字段 + `audit_ref.go` 新文件 + `normalized()` 形参迁移 + 调用点同步 + 单测 T1/T4/T5（总线分支）。

2. C2：gatewayhttp settle 旁路堵塞与 cache-hit 账本引用校验
   - `billing.go` + `handler_headers.go` 改既有 + 单测 T2/T3/T4（direct-settle / cache-hit 分支）。

3. C3：cmd/gateway requireRef 默认启用与灰度逃生口
   - `middleware.go` 启用 + `observability/audit_logger_handler.go` requireRef 改写 + feature-flag 实现 + `docs/10_RISK_REGISTER.md` `RR-W4-001` 占位。

## 七、验证

- `cd backend && GOCACHE=$HOME/.cache/go-build go build ./...`
- `go test ./internal/eventbus/... ./internal/gatewayhttp/... ./internal/observability/... ./cmd/gateway/... -race -count=1`
- `go test ./...`（全量）
- 每个新测试 mutation 自检，交付报告逐测写明。
- codex per-commit review 无 S0/S1。

## 八、决策点（执行前必须拍板）

1. D1：feature-flag 介质
   - env（`HUAKAI_DISABLE_AUDIT_REF_GUARD=1`）/ config 字段 / 不要 feature-flag。

2. D2：direct-settle / cache-hit 校验不过时 fallback
   - 落 DLQ operator_review / 结构化 ERROR 留爆头 / 两者皆做。

3. D3：releaseMode 注入方式
   - `normalized()` 形参显式传 / 模块级 `SetReleaseMode` 全局 / `context.Value`。

4. D4：`RR-W4-001`
   - 仅本切片占位 / 同时给定 reconciliation 切片归属与起点波次估计。

## 九、时间估算

- plan synth：约 30 min。
- implementation（3 commit）：2-3 h。
- test + mutation：约 1 h。
- codex per-commit review：1.5 h。
- 合计：5-6 h（单工作日）。

## 十、风险与缓解

1. R1：`normalized()` `releaseMode` 形参导致全仓 `normalized()` 调用点改。
   - 缓解：实现首步 grep `\\.normalized\\(` 列调用点清单，blast radius 量化后再决 D3。

2. R2：`gatewayhttp` 冻结约束 + 测试加在哪。
   - 缓解：若既有 `_test.go` 不存在，加配套 `_test.go` 不算新功能，但仍须 codex review 卡确认。

3. R3：feature-flag 启用 = 信任链事实破产。
   - 缓解：默认关 + 启用即结构化 ERROR + 计入告警面。

4. R4：direct-settle fallback 落 DLQ operator_review。
   - 缓解：DLQ `EventKind` 表是否已含 operator_review 需在实现首步验证（若无，加迁移）。

## 十一、范围外

- DLQ reconciliation 机制本体（留后续切片）。
- W4a-4 P2 碎票 ① 流式 Persisted trailer 漏 `X-HUAKAI-Verify` / `X-HUAKAI-Sig-Fingerprint`。
- W4a-4 P2 碎票 ② Forward 扫到 `[DONE]` / `message_stop` 即定稿（spec §C-13 边界，先澄清后实现）。
