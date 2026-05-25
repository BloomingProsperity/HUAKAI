# 2026-05-23 W4c settle 旁路堵塞 / B-12 Codex 独立计划

## 1. 目标重述

W4c 要把 completion money-path 的账本引用要求从「事件总线上的局部检查」提升为「所有能落钱的入口共同强制」。当前 spec 指出 B-12 的风险不是只有 `RequestCompletionEvent.normalized()` 校验不足,还包括 `settleCompletion()` 在总线不可用时直接 `Settle()`、以及 L2 cache 命中时直接 `CommitCacheHit()` 两条旁路;production 模式下这三条路径都必须在落钱前拥有有效账本引用,即 `AuditLedgerID + AuditSignatureFingerprint` 或 `AuditLedgerDLQRef` 任一分支成立,否则不能 settle / commit。依据: `docs/process/plans/2026-05-22-w4-trust-ledger.md:491`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:497`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:503`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:510`.

我的设计:在 `eventbus` 包定义一个小的 `AuditRefPolicy` + `ValidateMoneyPathAuditRef` 校验面,由调用方注入 release mode / 临时逃生 flag;`eventbus` 不 import `cmd/gateway`,避免依赖倒置。总线、direct-settle、cache-hit commit 三处只共享这个校验函数,不共享业务 handler。`cmd/gateway` 默认给 audit logger 开启 required ref,并把 required-ref 语义同步成 LedgerID 或 DLQRef 任一存在即可。spec 对 releaseMode 形参和放置包的约束见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:513`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:518`;当前 `cmd/gateway` production 判定来自 `HUAKAI_RELEASE_MODE` 见 `backend/cmd/gateway/config.go:53`, gatewayhttp 也有本地同名判定见 `backend/internal/gatewayhttp/chat_completions_billing.go:306`.

## 2. 文件级范围

| 路径 | 新/既有 | 范围 | 责任 |
|---|---:|---|---|
| `backend/internal/eventbus/types.go` | 既有 | `RequestCompletionEvent` 字段区 `:58-76`, `normalized()` `:212-232` | 新增 `AuditLedgerDLQRef string`;把 `normalized()` 改为接收 policy/release mode 并在 RequestCompletion money-path 调 `ValidateMoneyPathAuditRef`。 |
| `backend/internal/eventbus/bus.go` | 既有 | `Bus`/`Config` 使用点 `:17-29`, `Emit()` `:86-99` | 在 `Config` 保存 audit-ref policy;`Emit()` 用 bus 配置注入 release mode,而不是让 eventbus 读 `cmd/gateway`。 |
| `backend/internal/eventbus/audit_ref.go` | 新 | 非冻结包新文件 | 放 `AuditRefPolicy`, typed error, `ValidateMoneyPathAuditRef(event, policy)`;只表达引用有效性,不碰 billing。W4 §8 明确 eventbus 可加新文件: `docs/process/plans/2026-05-22-w4-trust-ledger.md:554`. |
| `backend/internal/eventbus/audit_ref_test.go` | 新 | 非冻结包新测试 | 覆盖 production/dev、persisted/DLQRef/缺失引用、feature flag 分支。 |
| `backend/internal/config/eventbus.go` | 既有 | config struct/env parse `:19-29`, `LoadEventBus()` `:31-75` | 加临时逃生 flag 配置,建议 env 名 `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF`;默认 false。 |
| `backend/cmd/gateway/middleware.go` | 既有 | `buildCompletionEventBus()` `:167-205` | 构造 eventbus policy,把 `releaseModeProduction()` 和 escape flag 注入 bus;注册 audit logger 时默认 `WithRequiredAuditRef()`。当前未启用 required ref 见 `:192-196`。 |
| `backend/cmd/gateway/config.go` | 既有 | `releaseModeProduction()` `:53-54` | 仅作为调用方 release mode 源;不新增 eventbus 反向 import。 |
| `backend/cmd/gateway/wiring.go` | 既有 | deps/runtime options `:44-85`, runtime options `:114-120`, deps 构造 `:194-218` | 如选择把同一个 policy 传给 gatewayhttp,在 deps 中持有 policy,并从 loaded eventBus config 填充。 |
| `backend/cmd/gateway/routes.go` | 既有 | `chatHandlerDeps()` `:92-114` | 给 `ChatHandlerDeps` 注入同一个 policy,确保 direct-settle/cache-hit 和 bus 一致。 |
| `backend/internal/gatewayhttp/chat_completions_handler.go` | 既有 | `ChatHandlerDeps` `:40-60` | 增加 `MoneyPathAuditRefPolicy eventbus.AuditRefPolicy` 或等价字段;不在 gatewayhttp 新建文件。 |
| `backend/internal/gatewayhttp/chat_completions_billing.go` | 既有 | event 构造 `:71-87`, direct settle `:156-169`, ledger helpers `:190-202`, ledger submit production check `:238-263` | event 带 `AuditLedgerDLQRef`;`settleCompletion()` 在 bus nil/fallback `Settle()` 前调用 validator;必要时把 submit/settle production 判定统一到注入 policy。 |
| `backend/internal/gatewayhttp/chat_completions_handler_headers.go` | 既有 | cache hit direct commit `:183-199`, cache-hit settleCompletion 分支 `:240-253` | direct `CommitCacheHit()` 前构造同等 audit-ref event 并校验;后段 `settleCompletion()` 分支自然复用 billing helper;event 加 DLQRef。 |
| `backend/internal/gatewayhttp/chat_completions_billing_test.go` | 既有测试 | existing production audit tests `:117-150`, stubs `:197-231` | 追加 direct-settle bus=nil/fallback 风险测试;冻结包内不加新文件。 |
| `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go` | 既有测试 | recording settler `:19-56`, cache-hit commit baseline `:58-97` | 追加 cache-hit missing-ref 不 commit、DLQRef commit 的判别性测试;冻结包内不加新文件。 |
| `backend/internal/observability/audit_logger_handler.go` | 既有 | required ref `:63-83` | `requireRef` 从只看 `AuditLedgerID` 改成 LedgerID 或 DLQRef 任一非空;observation 可保留 LedgerID/fingerprint,不伪造 fingerprint。 |
| `backend/internal/observability/audit_logger_handler_test.go` | 新 | 非冻结包新测试 | 覆盖 `WithRequiredAuditRef()` 对 DLQRef 的接受和双空拒绝。 |
| `backend/cmd/gateway/wiring_test.go` | 既有测试 | production env 测试风格 `:31-66` | 增加 buildCompletionEventBus 默认 required ref / policy 注入测试,或放到现有 gateway wiring 测试文件。 |

## 3. 冻结包合规检查

`AGENTS.md` 明确 `backend/internal/gatewayhttp`, `backend/internal/gateway`, `backend/internal/proto` 已冻结,在拆分前禁止新增任何文件;修 bug 改冻结包既有文件允许,新增功能要落到新包或非冻结包。依据: `AGENTS.md:546`, `AGENTS.md:551`, `AGENTS.md:555`, `AGENTS.md:560`.

本计划不在 `gatewayhttp/gateway/proto` 下新增任何文件。`gatewayhttp` 只修改既有实现文件和既有测试文件: `chat_completions_billing.go`, `chat_completions_handler_headers.go`, `chat_completions_handler.go`, `chat_completions_billing_test.go`, `chat_completions_handler_cache_test.go`。新增文件只在非冻结包 `eventbus` 和 `observability`;W4 §8 也明确新文件只能进非冻结包,并点名 eventbus/cmd/gateway 可加、gateway/gatewayhttp/proto 冻结: `docs/process/plans/2026-05-22-w4-trust-ledger.md:554`.

关于新 `_test.go`:我按硬规则把它视为“新增文件”。因此 `gatewayhttp` 不加新测试文件,只追加到现有测试文件。若 Owner 要求按测试文件例外处理,需要显式确认;默认不例外。

## 4. 三路径覆盖证明

| 路径 | 当前入口 | 当前风险 | W4c 计划覆盖 |
|---|---|---|---|
| 总线 | `Bus.Emit()` 在派 handler 前调用 `event.normalized()`;见 `backend/internal/eventbus/bus.go:86`, `backend/internal/eventbus/bus.go:93`。当前 normalized 只补 kind/id 并检查 ID/TenantID;见 `backend/internal/eventbus/types.go:212`, `backend/internal/eventbus/types.go:226`。 | 两个账本引用为空也能进 billing/audit handlers。 | `Emit()` 传 `b.cfg.AuditRefPolicy`, `normalized(policy)` 在 RequestCompletion 上调用 `ValidateMoneyPathAuditRef`。 |
| direct-settle | `settleCompletion()` 在 `CompletionBus == nil` 时直接 `d.Settler.Settle`;bus 返回 no handler/closed/queue full 时也直接 settle;见 `backend/internal/gatewayhttp/chat_completions_billing.go:156`, `backend/internal/gatewayhttp/chat_completions_billing.go:160`, `backend/internal/gatewayhttp/chat_completions_billing.go:163`, `backend/internal/gatewayhttp/chat_completions_billing.go:172`。 | 只改 eventbus normalized 不会触达这里。 | 每个 `Settle()` 前调用同一 validator;失败则返回 typed error,调用方不写成功响应。 |
| cache-hit direct commit | `serveL2CacheHit()` 在已有 reserve/account 时构造 `billing.SettleRequest` 并直接 `CommitCacheHit`;见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:183`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:197`。后半段另有走 `settleCompletion()` 的 cache 分支;见 `backend/internal/gatewayhttp/chat_completions_handler_headers.go:240`。 | direct commit 不经 eventbus,也不经 `settleCompletion()`。 | `CommitCacheHit()` 前用 ledgerResult 构造 RequestCompletionEvent 并调 validator;后半段由 `settleCompletion()` 复用。 |

配套一致性: audit logger 目前 required ref 只看 `AuditLedgerID`(`backend/internal/observability/audit_logger_handler.go:69`),cmd/gateway 注册时也没开 `WithRequiredAuditRef()`(`backend/cmd/gateway/middleware.go:192`);W4c 要同时改,否则 bus 侧校验和 audit handler 语义不一致。

## 5. 风险测试

测试质量遵守“测试必须能在它守的缺陷出现时变红”和判别性 fixture 要求: `AGENTS.md:579`, `AGENTS.md:583`, `AGENTS.md:586`, `AGENTS.md:594`。每个测试都使用 paired fixture,只改被测字段,不引入无关噪声。

1. `eventbus production 双空引用拒绝`:在 `eventbus/audit_ref_test.go` 或 `bus_test.go` 增加 case,同一个 valid event 只改变 `AuditLedgerDLQRef`。A: `ReleaseMode=production`, LedgerID/Fingerprint/DLQRef 全空 → `Emit` 返回 typed missing-ref error且 handler 未被调用;B:只加 `AuditLedgerDLQRef="audit_ledger_dlq:1"` → success。mutation 自检:删除 `normalized()` 内 validator 调用后,A 会错误成功并调用 handler,测试变红。

2. `eventbus persisted 分支必须同时有 ID 和 fingerprint`:同一 event 在 production 下,A 只有 `AuditLedgerID="ledger-1"` 无 fingerprint → reject;B 只额外加 `AuditSignatureFingerprint="fp"` → success。mutation 自检:把 validator 写成“LedgerID 非空即可”后,A 会错误通过,测试变红。

3. `eventbus DLQRef 不要求 fingerprint`:同一 production event,A 只有 `AuditLedgerDLQRef` 且 fingerprint 空 → success;B 把 DLQRef 清空且其他字段不变 → reject。mutation 自检:误把 DLQRef 分支也要求 fingerprint 后,A 失败,测试变红。这个 case 专门守 spec rev4: `docs/process/plans/2026-05-22-w4-trust-ledger.md:556`.

4. `direct-settle bus=nil 双空引用不 Settle`:在 `chat_completions_billing_test.go` 用 existing `recordingSettler`/新小 stub 调 `settleCompletion(ctx, deps{CompletionBus:nil}, event)`。A production policy + 双空引用 → 返回 missing-ref,`settler.calls==0`;B 只加 `AuditLedgerDLQRef` → `settler.calls==1`。mutation 自检:删掉 bus nil 分支前的 validator,A 会记录 settle call,测试变红。

5. `direct-settle fallback 错误分支双空引用不 Settle`:构造返回 `eventbus.ErrQueueFull` 或 closed bus 的 path,A 双空引用 → 不 settle;B 只加 persisted `AuditLedgerID+Fingerprint` → settle。mutation 自检:只保护 `CompletionBus==nil` 而漏掉 `shouldDirectSettleFallback` 分支时,A 会 settle,测试变红。当前 fallback 条件见 `backend/internal/gatewayhttp/chat_completions_billing.go:172`.

6. `cache-hit CommitCacheHit 双空引用不 commit`:在 `chat_completions_handler_cache_test.go` 针对 direct cache-hit commit 入口;A production policy + ledgerResult 映射到 LedgerID/Fingerprint/DLQRef 全空 → `cacheHitCommits==0`,response 为 structured settle/audit error;B 只加 `AuditLedgerDLQRef` → `cacheHitCommits==1`。mutation 自检:删除 `CommitCacheHit()` 前 validator 后,A 会 commit,测试变红。当前 direct commit 基线见 `backend/internal/gatewayhttp/chat_completions_handler_cache_test.go:89`.

7. `dev 模式双空引用三路径放行`:同一 event/settleReq/cacheReq,只把 policy 从 production 改为 dev/test;总线、direct-settle、cache-hit 都放行。mutation 自检:删掉 dev 豁免或把 zero-value policy 当 production 后,dev case 失败;若误让 production 也走 dev 分支,前 1/4/6 会变红。

8. `feature flag 逃生必须伴随 mandatory reconciliation 记录`:同一 production missing-ref fixture,A flag off → 不 settle/commit;B 只把 `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF=true` 或 policy bool 打开 → 允许落钱但必须产生结构化 ERROR/mandatory reconciliation 证据。mutation 自检:忽略 flag 会让 B 不落钱;忘记记录会让 B 的 reconciliation/assert 为空,测试变红。

9. `AuditLogger required ref 接受 DLQRef`:在 observability 测试中,A `WithRequiredAuditRef()` + 双空引用 → `ErrAuditRefMissing`;B 只加 `AuditLedgerDLQRef` → success;C 只加 LedgerID → success。mutation 自检:保留旧逻辑只看 `AuditLedgerID` 时,B 失败,测试变红。当前旧逻辑见 `backend/internal/observability/audit_logger_handler.go:69`.

## 6. 提交切片

提交标题遵守 W4 §11:一 commit 一模块,标题 `<英文模块> <中文说明>`,无 type、无 PASS、无阶段号;见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:587`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:594`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:596`。

1. `eventbus 强制 money-path 账本引用`
   - 字段、policy、validator、normalized/bus wiring、eventbus tests。
2. `gatewayhttp 堵住 settle 与 cache-hit 旁路`
   - event 传播 DLQRef、direct-settle/cache-hit 校验、冻结包既有测试追加。
3. `cmd-gateway 注入账本引用策略`
   - env flag parse/wiring、bus config、chat deps policy、default production mode 注入。
4. `observability 同步 audit 引用要求`
   - audit logger `LedgerID || DLQRef` required-ref 语义、cmd/gateway 默认 `WithRequiredAuditRef()`、observability/cmd tests。

每个 commit 前:先跑对应 targeted tests,stage 后跑 `codex exec review --uncommitted --full-auto`;HIGH 必修,MED 修或在 commit body 说明。项目 per-commit review 纪律见 `AGENTS.md:560`, `AGENTS.md:603`.

## 7. 验证命令

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/eventbus -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/gatewayhttp -race -count=1
```

```bash
cd backend && GOCACHE=$HOME/.cache/go-build go test ./internal/observability ./cmd/gateway -race -count=1
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

W4 验收要求 build、受影响包 race、全量 suite、risk tests、mutation 自检和 codex review;见 `docs/process/plans/2026-05-22-w4-trust-ledger.md:576`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:579`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:581`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:585`。

## 8. 需要 Owner 决策的问题

1. release mode / policy 注入选哪种?
   - 选项 A:新增 `eventbus.AuditRefPolicy`,由 `cmd/gateway` 和 `gatewayhttp.ChatHandlerDeps` 注入同一个 policy;eventbus 不读 env、不 import cmd/gateway。推荐 A。
   - 选项 B:eventbus/gatewayhttp 各自直接读 `HUAKAI_RELEASE_MODE`。实现少,但 policy drift 风险高。

2. 逃生 feature flag 的名字和存储选哪种?
   - 选项 A:临时 env `HUAKAI_TRUST_LEDGER_ALLOW_MISSING_MONEY_REF`,在 `internal/config/eventbus.go` 解析,默认 false,启动日志标红。推荐 A。
   - 选项 B:接入未来 DB/admin feature flag。更正统,但本 slice 会扩大 schema/admin surface。

3. production 校验失败后客户端如何完成?
   - 选项 A:不 settle/commit,Abort claim,返回 structured 500 (`audit_ref_missing`/`settle_error`),并写 structured ERROR。推荐 A。
   - 选项 B:不 settle/commit,返回缓存/上游 200,只走后台补偿。这个会让成功响应绕过 durable settlement,不推荐。

4. mandatory reconciliation 记录落哪里?
   - 选项 A:复用现有 DLQ/obs DLQ 写 operator-review 类事件,不改 schema。若现有 kind 足够表达,推荐 A。
   - 选项 B:先结构化 ERROR + RR-W4-001 mandatory roadmap,因为当前 reconciliation handler 是内存 dual-run且 Handle no-op: `backend/internal/observability/reconciliation_handler.go:43`, `backend/internal/observability/reconciliation_handler.go:70`, `backend/internal/observability/reconciliation_handler.go:207`。
   - 选项 C:新增 durable reconciliation schema。高风险,应另开 Owner 确认,不并入 W4c 默认执行。

5. `gatewayhttp` 新 `_test.go` 是否有例外?
   - 选项 A:无例外,所有 gatewayhttp 测试追加到既有 test 文件。推荐 A,符合 `AGENTS.md:548`。
   - 选项 B:Owner 显式允许新 test file。需要在计划/commit 里记录例外原因。

## 9. 明确不在范围

- 不处理 W4a-4 carryover P2 票 1:流式 Persisted trailer 补 `X-HUAKAI-Verify` / `X-HUAKAI-Sig-Fingerprint`。输入证据来自 `git show 83cb548 --format=%B`。
- 不处理 W4a-4 carryover P2 票 2:Forward 扫到 `[DONE]` / `message_stop` 即定稿的 C-13 边界澄清与实现。输入证据来自 `git show 83cb548 --format=%B`。
- 不重做 W4a-4 已完成的“流式账本移到终态发与脱离客户端取消”;`git show de88368 --format=%B` 显示该 commit 已跑 `go build ./...` 和 `go test ./...`。
- 不做 W4b B-13/B-15,不改 Merkle/脱敏/verify handler,这些属于 spec 其他小切片: `docs/process/plans/2026-05-22-w4-trust-ledger.md:117`, `docs/process/plans/2026-05-22-w4-trust-ledger.md:161`。
- 默认不新增数据库迁移、不改 billing ledger schema、不改 quota/auth core、不改 `LICENSE`。W4 §8 的唯一 DLQ CHECK 迁移属于 W4a/DLQ 范围,不是 W4c 默认范围: `docs/process/plans/2026-05-22-w4-trust-ledger.md:552`。

## 10. 风险与缓解

| 风险 | 具体失败方式 | 缓解 |
|---|---|---|
| policy drift | eventbus 认为 dev,gatewayhttp 认为 production,导致三路径行为不一致。 | 用单一 `AuditRefPolicy` 类型,cmd/gateway 同源注入;测试覆盖 bus/direct/cache 同一 policy。 |
| import cycle | eventbus 为读 production mode import `cmd/gateway` 或 gatewayhttp。 | eventbus 只接收 policy 参数/Config;当前 eventbus 只 import stdlib/billing/dlq,见 `backend/internal/eventbus/types.go:3`, `backend/internal/eventbus/types.go:10`;保持这个方向。 |
| DLQRef 被误当成未签名非法 | Deferred 条目没有 fingerprint,若 validator 要求 fingerprint 会把合法 DLQ intent 拦掉。 | 专门测试 DLQRef without fingerprint;spec 明确 Deferred fingerprint 未定: `docs/process/plans/2026-05-22-w4-trust-ledger.md:556`. |
| cache-hit 已写 200 后才发现不能 commit | 如果校验放在 `WriteHeader` 后,就会对客户显示成功但没有 durable money-path。 | 校验必须在 `CommitCacheHit()` 前,且该 branch 当前在写 header/body 前执行: `backend/internal/gatewayhttp/chat_completions_handler_headers.go:197`, `backend/internal/gatewayhttp/chat_completions_handler_headers.go:202`. |
| direct-settle 校验失败留下 reserving claim | 上游已返回成功但 settle 被阻止,如果不 abort/记录,claim 卡住。 | Owner 决策后实现 Abort + structured error + mandatory reconciliation;当前 cache-hit 分支已有 audit ledger error abort 形态可复用思路: `backend/internal/gatewayhttp/chat_completions_handler_headers.go:171`. |
| 逃生 flag 变成永久信任链缺口 | 运维打开后 production 继续允许双空引用落钱。 | flag 默认 false;启用必须写 mandatory reconciliation/ERROR,启动日志标出 env 名;后续 release gate 检查 flag 关闭。 |
| 冻结包结构违规 | 为 cache/direct 测试在 gatewayhttp 新增 `_test.go`。 | 只追加现有 test 文件;新增 helper 放 eventbus 非冻结包。 |

## 11. 时间估计

计划撰写本身:约 45-60 分钟。

执行 W4c:约 4-6 小时墙钟,其中 eventbus policy/validator 45-75 分钟,gatewayhttp direct/cache 90-120 分钟,cmd/gateway+observability wiring 60-90 分钟,测试与 mutation 自检 90-120 分钟,全量验证与 codex review 45-75 分钟。若 Owner 选择新增 durable reconciliation schema,另加 1-2 天且需要高风险确认。
