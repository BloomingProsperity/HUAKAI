# 2026-05-23 W4c Settle 旁路堵塞综合计划

本文件综合 Claude lane 与 Codex lane，并按 Owner 本轮 4 个决策锁定执行口径。并行计划合成符合项目规则：独立 plan 之后写无后缀 authoritative plan，执行只能从合成计划开始；规则见 `AGENTS.md:307-321`。Claude lane 把 W4c 定义为三条 money-path 入口同时堵住，见 `docs/process/plans/2026-05-23-w4c-settle-bypass-claude.md:3-7`；Codex lane 明确三路径必须在落钱前拥有 `AuditLedgerID + AuditSignatureFingerprint` 或 `AuditLedgerDLQRef`，见 `docs/process/plans/2026-05-23-w4c-settle-bypass-codex.md:3-7`。

## 1. 目标

W4c 的目标是关闭 B-12：completion money-path 在 production 模式下不能缺少有效账本引用后继续 settle / commit。spec 明确当前 `normalized()` 只校验事件基础字段、audit logger 只看 `AuditLedgerID` 且 gateway 默认未启用 required-ref，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:491-496`。spec 还明确仅改 eventbus 不够：`settleCompletion()` 的 bus nil / no handler / closed / queue full 会直接 `Settle()`，cache-hit 会直接 `CommitCacheHit()`，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:497-502`。

有效账本引用按 spec 锁定为二选一：`AuditLedgerID != "" && AuditSignatureFingerprint != ""`，或 `AuditLedgerDLQRef != ""`；DLQRef 分支不要求 fingerprint，production 下两者都不满足即拒绝，dev/test 放行，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:503-509`。W4c 必须在三处调用同一校验：eventbus `normalized()`、direct-settle 的 `Settle()` 前、cache-hit 的 `CommitCacheHit()` 前；release mode 由调用方注入，eventbus 不反向 import `cmd/gateway`，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:510-520`。

成功标准：三路径在 production 缺 ref 时都不 settle / commit，reserving claim 被 abort，对客户端返回结构化 HTTP 500，记录结构化 ERROR；带 persisted ref 或 DLQRef 时保持原功能；dev/test 与显式 escape flag 行为可测且有告警面。W4 总体验收要求 build、受影响包 race、全量 test、风险测试 mutation 自检、codex per-commit review，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:576-585`。

## 2. 文件级范围

| Commit | 路径 | 新/既有 | 当前锚点 | 责任 |
| --- | --- | --- | --- | --- |
| C1 | `backend/internal/eventbus/types.go` | 既有 | `RequestCompletionEvent` 字段 `backend/internal/eventbus/types.go:58-76`；`Config` `backend/internal/eventbus/types.go:132-142`；`normalized()` `backend/internal/eventbus/types.go:212-232` | 新增 `AuditLedgerDLQRef string`；新增 `Config.AuditRefPolicy *AuditRefPolicy`；`normalized(policy)` 对 request-completion money-path 调 validator。 |
| C1 | `backend/internal/eventbus/bus.go` | 既有 | `Bus.cfg` `backend/internal/eventbus/bus.go:17-19`；唯一 production `normalized()` 调用点 `backend/internal/eventbus/bus.go:86-94` | `Emit()` 把 `b.cfg.AuditRefPolicy` 传入 `event.normalized(policy)`；Claude 量化 R1：当前 production 调用点只有 `backend/internal/eventbus/bus.go:93`。 |
| C1 | `backend/internal/eventbus/audit_ref.go` | 新 | 非冻结包 | 定义 `AuditRefPolicy` object type，包含 release mode 与 escape flag；定义 typed error（建议 `ErrAuditRefMissing`）与 `ValidateMoneyPathAuditRef(event, policy)`。eventbus 不 import `cmd/gateway`，符合 spec `docs/process/plans/2026-05-22-w4-trust-ledger.md:518-520`。 |
| C1 | `backend/internal/eventbus/audit_ref_test.go` 或 `backend/internal/eventbus/bus_test.go` | 新或既有 | 非冻结包；现有 bus 测试 helper `backend/internal/eventbus/bus_test.go:428-438` | eventbus policy / validator 风险测试。新文件只在非冻结包 eventbus。 |
| C2 | `backend/internal/gatewayhttp/chat_completions_handler.go` | 既有 | `ChatHandlerDeps` `backend/internal/gatewayhttp/chat_completions_handler.go:35-60` | 增加 `AuditRefPolicy *eventbus.AuditRefPolicy` 字段；不新增 gatewayhttp 文件。 |
| C2 | `backend/internal/gatewayhttp/chat_completions_billing.go` | 既有 | event 构造 `backend/internal/gatewayhttp/chat_completions_billing.go:71-87`；direct settle `backend/internal/gatewayhttp/chat_completions_billing.go:156-169`；local release fn `backend/internal/gatewayhttp/chat_completions_billing.go:306-308` | 事件填 `AuditLedgerDLQRef`；所有 direct `Settle()` 前调用同一 validator；校验失败 abort claim、返回 typed error；删除或改走注入 policy，避免本地 release 判定漂移。 |
| C2 | `backend/internal/gatewayhttp/chat_completions_handler_headers.go` | 既有 | direct cache commit `backend/internal/gatewayhttp/chat_completions_handler_headers.go:183-199`；cache settle 分支 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:240-253` | `CommitCacheHit()` 前构造同等 audit-ref event 并校验；后半段复用 `settleCompletion()`。 |
| C2 | `backend/internal/gatewayhttp/chat_completions_billing_test.go` | 既有测试 | 现有 audit ledger 测试 `backend/internal/gatewayhttp/chat_completions_billing_test.go:81-150`；recording DLQ `backend/internal/gatewayhttp/chat_completions_billing_test.go:219-231` | 只追加 direct-settle 风险测试；不得新增 gatewayhttp `_test.go`。 |
| C2 | `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go` | 既有测试 | `recordingSettler` `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:19-56`；cache-hit baseline `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:58-97` | 只追加 cache-hit 风险测试；不得新增 gatewayhttp `_test.go`。 |
| C3 | `backend/internal/config/eventbus.go` | 既有 | config struct `backend/internal/config/eventbus.go:19-29`；env parse `backend/internal/config/eventbus.go:31-75`；bool parser `backend/internal/config/eventbus.go:78-86` | 解析 `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF`，默认 false；无 admin/DB flag storage。 |
| C3 | `backend/cmd/gateway/wiring.go` | 既有 | deps `backend/cmd/gateway/wiring.go:44-85`；runtime options `backend/cmd/gateway/wiring.go:114-120`；deps 构造 `backend/cmd/gateway/wiring.go:194-219` | 在启动期构造一个 `*eventbus.AuditRefPolicy`，放入 deps/runtime；同一实例供 bus.Config 与 ChatHandlerDeps 使用。 |
| C3 | `backend/cmd/gateway/middleware.go` | 既有 | build bus `backend/cmd/gateway/middleware.go:167-205`；当前 audit logger 未开 required ref `backend/cmd/gateway/middleware.go:191-196` | eventbus.Config 注入同一 policy；audit logger 默认 `WithRequiredAuditRef()`；escape flag true 时启动 WARN，日志字段含 env var name。 |
| C3 | `backend/cmd/gateway/routes.go` | 既有 | `chatHandlerDeps()` `backend/cmd/gateway/routes.go:92-114` | 把同一 policy 注入 `ChatHandlerDeps`。 |
| C3 | `backend/cmd/gateway/config.go` | 既有 | production 判定 `backend/cmd/gateway/config.go:53-55` | 只作为 cmd/gateway 构造 policy 的 release mode 来源；eventbus 不读取 env。 |
| C3 | `backend/internal/observability/audit_logger_handler.go` | 既有 | required-ref 旧逻辑 `backend/internal/observability/audit_logger_handler.go:63-83` | `requireRef` 改为 `AuditLedgerID` 或 `AuditLedgerDLQRef` 任一存在；不伪造 fingerprint。 |
| C3 | `backend/internal/observability/audit_logger_handler_test.go` | 新 | 非冻结包 | required-ref 接受 DLQRef、拒双空的判别测试。 |
| C3 | `backend/cmd/gateway/wiring_test.go` | 既有测试 | production env 测试风格 `backend/cmd/gateway/wiring_test.go:31-66` | 覆盖 env flag parse / startup WARN / 单 policy 注入；如需要 config 包可新增非冻结 `backend/internal/config/eventbus_test.go`。 |
| C3 | `docs/15_RELEASE_GATES.md` | 既有 docs | release gates 表 `docs/15_RELEASE_GATES.md:11-24` | 增加 production release gate：`HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF` 必须 false。 |
| C3 | `docs/10_RISK_REGISTER.md` | 既有 docs | 风险表 `docs/10_RISK_REGISTER.md:17-24`；规则 `docs/10_RISK_REGISTER.md:39-41` | 若 D3 schema gate 选择 fallback，新增 `RR-W4-001` mandatory reconciliation roadmap/risk row；若 DLQ operator_review path 可安全承载，记录为 mitigated evidence。 |

不在本计划创建新 migration，不新增 EventKind，不新增 gatewayhttp/gateway/proto 文件，不改 `LICENSE`、auth core、billing ledger schema、quota enforcement。

## 3. 三路径覆盖证明

1. 总线路径：`Bus.Emit()` 在派发 handler 前调用 `event.normalized()`，当前锚点是 `backend/internal/eventbus/bus.go:86-94`。这是 production call site 的唯一代码调用点，blast radius 是把 `event.normalized()` 改成 `event.normalized(b.cfg.AuditRefPolicy)` 一处；校验逻辑落在 eventbus 包内。

2. Direct-settle 路径：`settleCompletion()` 在 `CompletionBus == nil` 时直接 `d.Settler.Settle()`，bus 返回 no handler / closed / queue full 时也直接 settle，见 `backend/internal/gatewayhttp/chat_completions_billing.go:156-176`。C2 必须在每个 direct `Settle()` 前先调用 `eventbus.ValidateMoneyPathAuditRef(event, d.AuditRefPolicy)`；失败时 abort claim，不调用 `Settle()`。

3. Cache-hit direct commit 路径：`serveL2CacheHit()` 在 reserve 已有但 acquire 前直接 `CommitCacheHit()`，见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:183-199`；该分支在写 200 body 之前执行，见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:202-208`。C2 必须在 `CommitCacheHit()` 前校验；失败时 abort claim，不调用 `CommitCacheHit()`，不写成功响应。cache-hit 后半段走 `settleCompletion()`，见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:240-253`，自然复用 direct-settle 校验。

一致性要求：audit logger 当前只看 `AuditLedgerID`，见 `backend/internal/observability/audit_logger_handler.go:69-70`；cmd/gateway 当前注册 `NewAuditLoggerHandler` 时未传 `WithRequiredAuditRef()`，见 `backend/cmd/gateway/middleware.go:191-196`。C3 必须把 required-ref 语义同步为 `LedgerID || DLQRef`，否则 bus 侧接受 DLQRef 而 audit handler 拒绝，形成 policy drift。

## 4. 冻结包合规

`backend/internal/gatewayhttp`、`backend/internal/gateway`、`backend/internal/proto` 已冻结，拆分前禁止新增文件，见 `AGENTS.md:546-556`。计划/spec 若新建文件必须逐个写明目标包，并确认不是冻结包；给冻结包新增文件必须在 codex review 中标 HIGH 结构违规，见 `AGENTS.md:558-568`。W4 spec 也明确新文件只能进非冻结包，`gateway` / `gatewayhttp` / `proto` 冻结，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:554-555`。

本计划对冻结包只改既有文件：`chat_completions_handler.go`、`chat_completions_billing.go`、`chat_completions_handler_headers.go`、`chat_completions_billing_test.go`、`chat_completions_handler_cache_test.go`。gatewayhttp 新 `_test.go` 不允许；新增 helper 与测试文件只落 eventbus、observability、config 等非冻结包。`backend/internal/gateway` 与 `backend/internal/proto` 本切片不触碰。

## 5. 风险测试

测试质量执行项目硬规则：测试必须能在目标缺陷出现时变红，必须做 mutation 自检，fixture 必须判别，不能用 nil stub 掩盖风险，见 `AGENTS.md:576-592`；spec 规定测试必须给出判别性例子，见 `AGENTS.md:601-608`。以下每条使用 paired fixture，只改变被测字段或 policy 位。

1. `eventbus production 双空引用拒绝`：同一个 valid event，A 为 production policy 且 `AuditLedgerID/AuditSignatureFingerprint/AuditLedgerDLQRef` 全空，`Emit` 返回 typed missing-ref error 且 handler 未调用；B 只加 `AuditLedgerDLQRef="audit_ledger_dlq:1"`，handler 被调用。mutation 自检：删除 `normalized()` 内 validator 调用后，A 会错误调用 handler，测试变红。

2. `eventbus persisted 分支必须 ID 和 fingerprint 同时存在`：同一 production event，A 只有 `AuditLedgerID="ledger-1"` 且 fingerprint 空，拒绝；B 只额外加 `AuditSignatureFingerprint="fp"`，放行。mutation 自检：把 validator 改成只看 LedgerID 后，A 会错误放行，测试变红。

3. `eventbus DLQRef 分支不要求 fingerprint`：同一 production event，A 只有 `AuditLedgerDLQRef` 且 fingerprint 空，放行；B 清空 DLQRef 且其他字段不变，拒绝。mutation 自检：误把 DLQRef 分支也要求 fingerprint 后，A 会失败，测试变红；该语义来自 spec `docs/process/plans/2026-05-22-w4-trust-ledger.md:503-509`。

4. `direct-settle bus=nil 缺引用不 Settle 并 abort`：在 `chat_completions_billing_test.go` 用 existing `recordingSettler` 风格构造 production policy + `CompletionBus:nil`。A 双空引用返回 `audit_ref_missing`/`settle_error` typed error、`settler.calls==0`、`aborts==1`；B 只加 DLQRef，`settler.calls==1`、`aborts==0`。mutation 自检：删 bus nil 分支前 validator 后，A 会记录 settle call，测试变红。

5. `direct-settle fallback 错误分支缺引用不 Settle`：构造 bus 返回 `ErrNoHandlers`、`ErrBusClosed` 或 `ErrQueueFull` 中至少一个 fallback error；A 双空引用不 settle 并 abort；B 只加 persisted `LedgerID+Fingerprint` 后 settle。mutation 自检：只保护 `CompletionBus==nil` 而漏 `shouldDirectSettleFallback()`，A 会 settle，测试变红；fallback 条件见 `backend/internal/gatewayhttp/chat_completions_billing.go:172-176`。

6. `cache-hit direct CommitCacheHit 缺引用不 commit`：在 `chat_completions_handler_cache_test.go` 针对 direct cache-hit commit 入口。A production policy + ledgerResult 映射到三 ref 全空，`cacheHitCommits==0`、`aborts==1`、response 为 structured 500；B 只加 DLQRef，`cacheHitCommits==1`。mutation 自检：删除 `CommitCacheHit()` 前 validator 后，A 会 commit，测试变红；当前 direct commit baseline 见 `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:89-96`。

7. `dev/test 模式双空引用三路径放行`：同一 event / settleReq / cacheReq，只把 policy release mode 从 production 改为 non-production；bus、direct-settle、cache-hit 均放行。mutation 自检：删 dev 豁免后 dev case 失败；误让 production 也走 dev 分支时，测试 1、4、6 会变红。

8. `escape flag 只在显式开启时放行且记录 ERROR`：同一 production missing-ref fixture，A flag false 不 settle/commit；B 只把 policy escape flag 打开，允许 settle/commit，但捕获结构化 ERROR 日志，字段包含 request_id、tenant_id、route_id、missing ref details 与 env var `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF`。mutation 自检：忽略 flag 会让 B 不落钱；忘记日志会让 B 的日志断言为空，测试变红。

9. `audit logger required-ref 接受 DLQRef`：在 observability 测试中，A `WithRequiredAuditRef()` + 双空引用返回 `ErrAuditRefMissing`；B 只加 `AuditLedgerDLQRef` 成功；C 只加 LedgerID 成功。mutation 自检：保留旧逻辑只看 `AuditLedgerID` 时，B 失败，测试变红；旧逻辑见 `backend/internal/observability/audit_logger_handler.go:69-70`。

10. `cmd/gateway 构造并注入同一个 policy 实例`：在 `wiring_test.go` 或同包测试中设置 `HUAKAI_RELEASE_MODE=production` 与 escape flag，断言 build bus 的 config 与 `chatHandlerDeps()` 看到同一 `*eventbus.AuditRefPolicy` 实例或等价 identity token，且 startup WARN 只在 escape flag true 时出现。mutation 自检：让 bus 与 ChatHandlerDeps 各自构造 policy，修改一侧 flag 后另一侧不变，测试变红。

11. `config env parse 默认 false / true / invalid`：在 config 包测试中，空 env 得 false，`true/on/1` 得 true，非法值返回明确错误且包含 env var 名。mutation 自检：默认 true 或忽略非法值都会使对应 case 变红；现有 bool parser 风格见 `backend/internal/config/eventbus.go:78-86`。

12. `D3 schema gate 测试`：见第 12 节。若选择 DLQ operator_review path，测试必须证明 settle-only payload 可被 admin/replay 路径安全保留而不会被 audit-ledger worker 当 PreparedEntry decode；若选择 fallback，测试必须证明不会 enqueue DLQ，只写 ERROR 与 `RR-W4-001`。mutation 自检：把 chosen path 反转，测试变红。

## 6. Commit 切片

提交标题固定为 `<英文模块> <中文说明>`，无 type prefix、无 stage number、无 PASS；W4 spec 对 W4c commit 模块与标题规则见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:587-596`。

1. `eventbus 强制账本引用策略`
   - 改 `eventbus/types.go`、`eventbus/bus.go`。
   - 新增 `eventbus/audit_ref.go` 与 eventbus 风险测试。
   - 引入 `AuditRefPolicy`、`AuditLedgerDLQRef`、typed missing-ref error、validator、bus Config 注入面。
   - 不触 gatewayhttp。

2. `gatewayhttp 堵住结算旁路`
   - 改既有 gatewayhttp 实现文件与既有 gatewayhttp 测试文件。
   - 在 direct-settle 与 cache-hit direct commit 前调用同一 validator。
   - 校验失败 abort reserving claim、返回 HTTP 500 structured error、写 structured ERROR。
   - C2 开工前必须先完成第 12 节 D3 payload-schema verification gate，并按 gate 结果执行 DLQ operator_review path 或 RR-W4-001 fallback。

3. `cmd-gateway 注入策略并同步审计门禁`
   - 改 `internal/config/eventbus.go`、`cmd/gateway/wiring.go`、`cmd/gateway/middleware.go`、`cmd/gateway/routes.go`、`cmd/gateway/config.go`。
   - 把同一个 `*eventbus.AuditRefPolicy` 注入 bus.Config 与 ChatHandlerDeps。
   - 同步 observability required-ref 语义；新增 observability 测试。
   - 更新 `docs/15_RELEASE_GATES.md` 检查 escape flag production false；按 D3 gate 结果更新 `docs/10_RISK_REGISTER.md`。

每个 commit 前跑对应 targeted tests；stage 后运行 `codex exec review --uncommitted --full-auto`。项目 per-commit review 要求见 `AGENTS.md:487-503`；money-path / billing 相关变更属于高关注 review 面，见 `AGENTS.md:512-518`。

## 7. 验证命令

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/eventbus -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/gatewayhttp -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/config ./internal/observability ./cmd/gateway -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/dlq ./internal/auditledger -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go build ./...
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./... -count=1
```

```bash
codex exec review --uncommitted --full-auto
```

若 D3 选择 DLQ operator_review path 且涉及 DLQ store 状态写入，增加本地 PG integration 验证；如果仅走 fallback，不新增 migration、不跑 migration gate。最终交付报告逐条列出第 5 节测试的 mutation self-check 结果。

## 8. Owner 决策落地

D1 — release-mode injection：锁定为 `eventbus.AuditRefPolicy` object type，字段承载 release mode 与 escape flag。`cmd/gateway` 启动时构造一个 policy 实例，同一实例注入 `eventbus.Config` 与 `ChatHandlerDeps`；eventbus 不 import `cmd/gateway`。spec 要求 release mode 由调用方注入、eventbus 不反向 import cmd/gateway，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:513-520`。当前 repo 只有 `cmd/gateway` 的 `releaseModeProduction()` 与 gatewayhttp 本地同名逻辑，见 `backend/cmd/gateway/config.go:53-55`、`backend/internal/gatewayhttp/chat_completions_billing.go:306-308`；C3 后 policy 是 authoritative source，gatewayhttp 本地 release 判定要删除或路由到 policy。

D2 — client semantics on validation failure：锁定为不 settle/commit，abort reserving claim，对客户端返回 HTTP 500 structured error（`audit_ref_missing` 或既有 `settle_error` / `cache_settle_error`），并写 structured ERROR，字段至少包含 request_id、tenant_id、route_id、missing ref details。spec 要求校验不过不 settle 并记 mandatory reconciliation，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:521-524`；Settler 已有 tenant-scoped Abort 接口，见 `backend/internal/billing/billing.go:35-38`；现有 public error catalog 已有 `settle_error` / `cache_settle_error`，见 `backend/internal/clienterr/catalog.go:76-78`，但如新增 `audit_ref_missing` 必须同步 catalog。

D3 — mandatory reconciliation landing：锁定先尝试用现有 DLQ EventKind `audit_ledger_entry` 和 Status `operator_review`，不新增 EventKind、不新增 migration。当前 EventKind 已存在于 `backend/internal/dlq/types.go:13-21`，`StatusOperatorReview` 已存在于 `backend/internal/dlq/types.go:31-40`，且 `audit_ledger_entry` lane/replica status 已按主写 intent 处理，见 `backend/internal/dlq/types.go:98-119`。但 C2 前必须完成第 12 节 schema gate；若 W4a-2 payload 不能 forward-compatible 表达 settle-only intent without PreparedEntry，则执行 in-plan fallback：不 enqueue DLQ，只写 structured ERROR，并在 `docs/10_RISK_REGISTER.md` 增加 `RR-W4-001` mandatory reconciliation roadmap/risk。spec 允许最次结构化 ERROR + RR-W4-001，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:521-524`。

D4 — feature-flag escape hatch：锁定 env var `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF`，默认 false，在 `backend/internal/config/eventbus.go` 解析。true 时 startup WARN 日志必须含红/高亮格式的 env var 名，每次 validation bypass 都写 structured ERROR；release gate doc 必须检查 production 保持 false。spec 要求 feature flag 可临时放宽、启用即写 mandatory reconciliation 记录，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:528-529`；release gates 中 Billing/Security/Release Decision Gate 是承载点，见 `docs/15_RELEASE_GATES.md:21-24`。

## 9. 范围外

W4a-4 P2 票 ①：流式 Persisted trailer 补 `X-HUAKAI-Verify` / `X-HUAKAI-Sig-Fingerprint`，明确不在 W4c；Claude lane 与 Codex lane 均列为范围外，见 `docs/process/plans/2026-05-23-w4c-settle-bypass-claude.md:132-136`、`docs/process/plans/2026-05-23-w4c-settle-bypass-codex.md:137-143`。

W4a-4 P2 票 ②：Forward 扫到 `[DONE]` / `message_stop` 即定稿的 C-13 边界澄清与实现，明确不在 W4c；同上引用 `docs/process/plans/2026-05-23-w4c-settle-bypass-claude.md:132-136`、`docs/process/plans/2026-05-23-w4c-settle-bypass-codex.md:137-143`。

不处理 W4b B-13/B-15，不改 Merkle / redaction / verify handler；W4b 与 W4a/W4c 的切片顺序和职责见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:89-99`。不新增 DB migration；W4 spec 中唯一 DLQ CHECK 迁移属于 W4a/DLQ 范围，非 W4c 默认范围，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:552-553`。

## 10. 风险与缓解

| 风险 | 具体失败方式 | 缓解 |
| --- | --- | --- |
| policy drift | eventbus 用 production，gatewayhttp 本地函数读到 dev，三路径行为不一致；当前已有 `cmd/gateway` 与 gatewayhttp 两处 release 判定，见 `backend/cmd/gateway/config.go:53-55`、`backend/internal/gatewayhttp/chat_completions_billing.go:306-308`。 | D1 单一 `*eventbus.AuditRefPolicy` 实例；C3 测试验证 bus 与 ChatHandlerDeps 共享实例。 |
| direct-settle 旁路残留 | 只改 `normalized()`，bus nil 或 fallback error 仍直接 `Settle()`；当前代码见 `backend/internal/gatewayhttp/chat_completions_billing.go:156-176`。 | C2 在每个 direct `Settle()` 前校验；测试 4/5 区分 bus nil 与 fallback error。 |
| cache-hit 已返回成功但未结算 | 校验若放到 `WriteHeader` 后，客户看到 200 但 money-path 未 durable；当前 commit 在写 header 前，见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:197-208`。 | C2 校验必须在 `CommitCacheHit()` 前；失败时 abort + 500。 |
| D3 payload schema 不匹配 | 现有 audit ledger DLQ producer 只 marshal `PreparedEntry`，见 `backend/internal/auditledger/dlq_producer.go:15-35`；worker 会 decode payload 再 Prepare/Append，见 `backend/internal/auditledger/dlq_worker.go:16-31`。settle-rejected-before-ledger 可能没有 PreparedEntry。 | 第 12 节 gate 先验证；不能 forward-compatible 表达时执行 RR-W4-001 fallback，不假造 PreparedEntry。 |
| operator_review 状态落不下去 | `dlq.Store.Enqueue` 当前 INSERT status 硬编码 `'pending'`，见 `backend/internal/dlq/store.go:263-269`；`operator_review` 只在 MarkFailed 中写，见 `backend/internal/dlq/store.go:194-219`。 | 若 D3 选择 DLQ path，C1/C2 必须提供无 migration 的 operator_review enqueue/mark 机制并测试；否则执行 fallback。 |
| escape flag 变永久缺口 | 运维打开 `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF` 后 production 继续允许无 ref 落钱。 | 默认 false；startup WARN 高亮 env var；每次 bypass ERROR；`docs/15_RELEASE_GATES.md` 增 production false gate。 |
| 冻结包结构违规 | 为 cache/direct 测试在 gatewayhttp 新增 `_test.go`。 | 只追加现有 gatewayhttp 测试文件；新增 helper 放非冻结包；codex review 按 `AGENTS.md:560-568` 阻断违规。 |
| 弱测试给假信心 | fixture 只看 status，删除 guard 后仍绿。 | 第 5 节每条都有 paired fixture 与 mutation self-check；测试质量规则见 `AGENTS.md:576-608`。 |

## 11. 时间估

计划合成：45-60 分钟。W4c 实施：5-7 小时墙钟，其中 C1 eventbus policy/validator 60-90 分钟，D3 payload-schema gate 30-45 分钟，C2 gatewayhttp direct/cache 120-150 分钟，C3 cmd/gateway + config + observability + docs 90-120 分钟，targeted/race/full tests 与 per-commit review 90-150 分钟。若 D3 schema gate 证明需要改变 DLQ store API 才能 operator_review enqueue，仍不得新增 migration；若无法保持 forward-compatible，按第 12 节 fallback 收敛，不扩大到 schema work。

## 12. D3 payload-schema verification gate

C2 开工前必须先做这个 gate，并把结论写入 C2 commit body 或相邻 review note。目标不是新增功能，而是验证 W4a-2 的 `EventKind=audit_ledger_entry` payload 是否能承载 settle-only intent without `PreparedEntry`。

已观察的当前状态：

- `EventKindAuditLedgerEntry` 已存在，见 `backend/internal/dlq/types.go:13-21`。
- `StatusOperatorReview` 已存在，见 `backend/internal/dlq/types.go:31-40`。
- audit-ledger DLQ producer 目前把 `PreparedEntry` marshal 成 payload，见 `backend/internal/auditledger/dlq_producer.go:15-35`。
- audit-ledger DLQ worker 目前按 ledger payload decode，再重新 `PrepareEntry` 并 `Append`，见 `backend/internal/auditledger/dlq_worker.go:16-31`。
- `dlq.Store.Enqueue` 当前把 status 写死为 pending，见 `backend/internal/dlq/store.go:263-269`；`MarkFailed` 可把记录转为 operator_review，见 `backend/internal/dlq/store.go:194-219`。

Gate 判定标准：

1. 可以构造一个 `audit_ledger_entry` DLQ 记录，Status 为 `operator_review`，payload 为 settle-only intent（request_id、tenant_id、claim_id、route_id、missing ref details、failure code、created_at），不包含也不伪造 `PreparedEntry`。
2. 该记录不会被 `auditledger.NewDLQHandler` 当成待 append ledger entry 自动 replay；operator/admin list 能看到并保留 payload。
3. 不需要新 EventKind，不需要新 migration，不需要高风险 schema 改动。
4. 测试能证明 chosen path：删除 operator_review 标记或让 worker误处理 settle-only payload 时测试变红。

Locked decision table：

| Gate 结果 | C2/C3 执行 |
| --- | --- |
| 四项标准全部满足 | 使用现有 `EventKindAuditLedgerEntry` enqueue operator_review 记录；payload 明确标记 settle-only intent；per-commit review 必须验证没有 PreparedEntry 伪造、没有新 EventKind、没有 migration。 |
| 任一标准不满足 | 执行 fallback：不 enqueue DLQ；每次 validation failure/bypass 写 structured ERROR；`docs/10_RISK_REGISTER.md` 增 `RR-W4-001` mandatory reconciliation roadmap/risk；per-commit review 必须验证没有半可用 DLQ enqueue。 |

本 gate 是 W4c 的硬前置，不是实现时临场判断。当前代码证据显示 payload 与 worker 都偏 PreparedEntry-only，执行者必须用测试或代码读证据确认是否已有 forward-compatible seam；不能为满足 DLQ 目标而伪造未签 ledger entry。

## 13. Clean-room

本计划只读 HUAKAI 内部代码与 HUAKAI docs，没有读取任何外部 reference project source。AGENTS clean-room 规则明确 HUAKAI internal code / docs 不需要 reference-source lane guard，见 `AGENTS.md:418-422`；W4 spec 也说明整波只改 HUAKAI 内部代码、不读参照项目源码，无 clean-room 约束，见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:598-601`。

Clean-room 风险结论：无外部源码输入，无 AGPL/GPL/LGPL 结构、命名、实现迁移风险。本切片仅实现 HUAKAI 自有 trust ledger / eventbus / gatewayhttp 行为闭环。
