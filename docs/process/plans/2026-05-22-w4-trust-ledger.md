# W4 强制账本引用与完整性 —— 实施 spec

> 补救波 W4。源:`docs/process/plans/2026-05-22-audit-remediation-wave.md` 第 54 行。
> 覆盖 6 个发现:GW-07(Zone A)、B-12 / B-13 / B-15(Zone B #12/#13/#15)、
> C-13 / C-14(Zone C #13/#14)。**信任链核心敏感波。**
> Owner 决策(2026-05-22)已定:账本写失败 = **启动 fail-closed + 运行时
> durable-DLQ**(见 `项目记忆 project_trust_ledger_failclosed_policy`)。
> 本 spec 前置一次写全(含已知难点 + 跨发现交互),目标 ≤2 轮 review。

## 1. 背景与核心风险

审计/信任账本是 HUAKAI「商家不能做假」的命根子:每笔请求都要有一条**签名的
账本条目**证明它真实发生过。W4 修六个让信任链可被静默绕过 / 被污染 / 被误读
的口子:

- 账本/签名器没配 → 请求照样计费交付,无签名条目(GW-07 / C-14)。
- 账本 append 失败 → 只 warning,不补救(C-14)。
- money-path completion 事件不要求账本引用 → billing settle 可在无账本时发生(B-12)。
- 脱敏失败被忽略 → 未净化链路数据被永久写进 append-only 账本(B-13)。
- 读取时吞掉 JSON / Merkle 结构错误 → 损坏行当正常条目返回(B-15)。
- 流式账本用首字节时间当完成时间 → 签名记录与真实交付窗口不符(C-13)。

## 2. 切分(小切片闭合纪律)

W4 拆三个独立闭合切片:

- **W4a — 账本写入路径 fail-closed + DLQ + 流式完成时间**(GW-07 / C-13 / C-14)。
  「账本写不进就进 durable DLQ,请求放行;流式账本用真实完成时间」。
- **W4b — auditledger 存储完整性**(B-13 / B-15)。全部落 `auditledger` 包:
  脱敏失败不得污染账本;读取时损坏行必须报错不得静默。
- **W4c — completion 事件强制账本引用**(B-12)。money-path 事件必须带账本引用
  (真实 LedgerID 或 DLQ 待补引用),空引用拒绝。

W4a 先做(它定义了 DLQ 待补引用的形态,W4c 依赖它)。再 W4b、W4c。
合计 ~3 天。

## 3. 核心决策 —— 账本写失败 = 启动 fail-closed + 运行时 durable-DLQ

Owner 2026-05-22 已定。展开:

- **启动期**:production 模式下 `AuditLedger` + `Signer` 必须配好且 `AuditLedger`
  不是 `NoopLedger`。未满足 → `cmd/gateway` 启动直接失败。dev/test 模式不强制
  (允许 nil / Noop)。
- **运行时**:某次 `AuditLedger.Append()` 失败(DB 抖动等)→ 那条**已签名**的
  账本条目进 durable DLQ(复用 `internal/dlq`),保证事后一定补写;**请求放行**。
- 复用 `internal/dlq`:新增 `dlq.EventKind` 值 `audit_ledger_entry`(若已有
  贴近的值可复用,codex 在实现时确认);DLQ payload 携带完整已签名 `LedgerEntry`
  的 JSON,worker 后续重放 `Append`。
- 实现位置:DLQ 入队 helper 放 `auditledger` 包**新文件**(`auditledger` 不是
  冻结包,可加新文件);`gatewayhttp` / `gateway` 调用方改既有文件即可。

## 4. W4a 逐发现 spec

### GW-07 — buffered 路径账本静默跳过(S1)

- 证据:`gatewayhttp/chat_completions_billing.go:237-248` `submitAuditLedgerEntry`
  在 `d.AuditLedger == nil` / `d.Signer == nil` 时 `appendTrustChainWarning` +
  `return nil, nil`;`:266-269` `Append` 失败时 `return nil, err`(调用方目前
  如何处理 err 须 codex 核实,但 nil 分支是静默跳过)。
- 修复:
  1. **启动检查(新)**:`cmd/gateway` 启动期,production 模式断言
     `AuditLedger != nil && Signer != nil && !isNoopLedger(AuditLedger)`,
     否则启动失败并打印明确原因。dev/test 模式跳过。
  2. **nil 分支**:启动检查保证 production 不会到这。保留 nil 分支只服务
     dev/test(warning + return nil 可接受,因为 dev 无信任链需求)。
  3. **Append 失败分支**:`Append` 返回 error 时,不再直接 `return nil, err`
     让上层失败 —— 改为把已签名的 `entry` 投 durable DLQ
     (`auditledger` 新 DLQ helper),记一条 `audit_ledger_deferred` 协议损耗,
     返回一个**带 DLQ 待补引用的结果**(见 §6 跨发现交互),请求继续。
- 风险测试(判别性):
  1. 注入一个 `Append` 必失败的 fake ledger → 断言:请求**仍成功交付**、
     DLQ 收到一条携带完整签名 entry 的事件、返回结果带 DLQ 待补引用。
  2. mutation 自检:把 DLQ 入队那行删掉 → 测试必须变红(DLQ 收不到事件)。

### C-14 — 流式路径账本缺失/失败只 warning(S1)

- 证据:`gateway/forwarder.go:640-653` `emitStreamingLedger` 对 `AuditLedger==nil`
  / `NoopLedger` / `Signer==nil` 三种情况只 `warnLedgerLoss` + return;
  `:676-680` `Append` 失败也只 `warnLedgerLoss` + return。
- 修复:与 GW-07 同决策 B。
  1. nil / Noop / signer-nil:由启动检查在 production 兜住;运行期到这只可能是
     dev/test,warning 保留。
  2. `:676` `Append` 失败:已签名的 `entry` 投 durable DLQ(同一 helper),
     `warnLedgerLoss` 仍保留作为可观测信号,但**不再是唯一动作**。
- 风险测试:fake ledger `Append` 失败 → 流式请求仍完成交付、DLQ 收到签名 entry。
  mutation:删 DLQ 入队 → 变红。

### C-13 — 流式账本用首字节时间当完成时间(S1)

- 证据:`gateway/forwarder.go:601-632` `streamingLedgerHeaderWriter` 在
  `WriteHeader` / 首次 `Write` 调 `ensureLedger(time.Now())`;`:611-622`
  `ensureLedger` 用该时间触发 `before(completedAt)` 回调 → hop chain 把首字节
  时间当 response completion 计算 duration。长流式(首 token 500ms、实际完成
  60s)→ 签名账本宣称 response hop ~500ms 完成。
- 修复:**账本在流真正终结时发出,用真实完成时间**。
  - 移除「首字节触发账本」语义:`streamingLedgerHeaderWriter` 的
    `ensureLedger` 不再在 `WriteHeader`/`Write` 触发账本完成。
  - 账本发出点移到流终态(`Forward` 扫描到 terminal 事件 / `Forward` 返回时),
    `completedAt` = 真实流结束时间。`emitStreamingLedger` 的 `completedAt` 实参
    传真实终结时间。
  - **已知难点**:若存在「header 已提交但流中途断」的情况,仍需一条账本。
    本 spec 取**单条账本 @ 流终态**(无论正常终结还是中断,`Forward` 返回点
    都发一次,`completedAt` = 该点时间)。不拆「header commitment + completion」
    两条账本(更复杂,收益小)。codex 实现时若发现单条方案漏掉某终结路径
    (如客户端断连),必须在 review 指出。
- 风险测试(判别性):构造一个首字节早、终结晚的流(mock:首 chunk 后
  sleep 再终结)→ 断言账本 hop chain 的 completion 时间 ≈ 终结时间,不是首字节
  时间;两者差值必须 > 一个明确阈值。mutation:把账本触发改回首字节 → 变红。

## 5. W4b 逐发现 spec

### B-13 — 脱敏失败被忽略,污染 append-only 账本(S1)

- 证据:`auditledger/privacy.go:17-37` `sanitizeLedgerEntry` 在 redactor 失败 /
  `len(raw)==0` 时返回**原始 entry** + err;`auditledger/postgres.go:113`
  `entry, _ = sanitizeLedgerEntry(ctx, entry)` 丢弃 err;Memory append 同形态
  (`ledger.go:85/88`,codex 核实)。
- 修复:**脱敏失败 → 写最小化 fallback,绝不写原始链路数据**。
  - `sanitizeLedgerEntry` 失败时,调用方写入的 entry 必须把 `HopChain` /
    `ModelChain` / `TenantScopeRef` **清空**(最小化 fallback),并置一个明确
    标记(如 entry 上新增 `RedactionDropped bool` 或一条结构化 loss),表示
    「此条目链路数据因脱敏失败被丢弃」。账本条目**仍签名仍 append**(信任链
    连续性不断),只是不含敏感链路。
  - 选最小化 fallback 而非「脱敏失败就 fail append」:后者会让确定性脱敏失败
    (payload shape 问题)在 DLQ 里无限重试。最小化 fallback 是确定性收敛的。
  - `postgres.go` / `ledger.go` 不得再 `_ =` 丢弃 sanitize err;必须据 err
    走最小化 fallback 分支。
- 风险测试(判别性):注入一个对特定 payload 必失败的 fake redactor,entry 的
  HopChain 含敏感 marker → 断言:写进账本的 entry HopChain/ModelChain/
  TenantScopeRef 为空、不含 marker、`RedactionDropped` 为真、条目仍有有效签名。
  mutation:把 fallback 清空逻辑去掉(恢复 `entry, _ =`)→ 账本含 marker → 变红。

### B-15 — 读取吞掉 JSON / Merkle 结构错误(S2)

- 证据:`auditledger/postgres.go:380-384` `scanLedgerEntry` 对 `hop_chain` /
  `model_chain` 的 `json.Unmarshal` 用 `_ =` 丢弃错误;`prev_merkle_root` /
  `merkle_root` 长度非 32 时(`:385+`,codex 核实)只保留 zero root。
- 修复:
  - `scanLedgerEntry`:`hop_chain` / `model_chain` 的 `json.Unmarshal` 返回 error
    → 整个 scan 返回明确的 `ErrLedgerEntryCorrupt`(新 sentinel error),
    不再静默。
  - `prev_merkle_root` / `merkle_root` 长度非 0 且非 32 → 同样返回
    `ErrLedgerEntryCorrupt`(长度 0 是合法的「首条目无 prev」,要区分)。
  - verify API(`audit_verify_handler.go`)把 `ErrLedgerEntryCorrupt` 映射成
    HTTP 500 + 稳定 code `ledger_corrupt`(用 W3 的 clienterr 目录登记此 code),
    并打 ERROR 日志(可观测,便于运维发现存储损坏)。
- **已知难点**:区分「合法空 root」(首条目 prev_merkle_root 为空)与「损坏」
  (长度 1-31 或 33+)。只有**非 0 且非 32** 才算损坏。
- 风险测试(判别性):
  1. 构造一行 `hop_chain` 是坏 JSON → 断言 `scanLedgerEntry` 返回
     `ErrLedgerEntryCorrupt`,不是一个 HopChain 为空的「正常」entry。
  2. 构造 `merkle_root` 长度 16 → 断言返回 `ErrLedgerEntryCorrupt`。
  3. 构造首条目 `prev_merkle_root` 长度 0 → 断言**不报** corrupt(合法)。
  mutation:把 corruption 检查去掉(恢复 `_ =`)→ 测试 1/2 变红。

## 6. W4c 逐发现 spec + 跨发现交互

### B-12 — completion 事件不强制账本引用(S1,trust-chain bypass)

- 证据:`eventbus/types.go:212-233` `normalized()` 只校验 `Kind` / `ID` /
  `TenantID > 0`,不要求 `ClaimID` / `AuditLedgerID` / `AuditSignatureFingerprint`
  (这些字段存在于 `types.go:58-72`);`observability/audit_logger_handler.go:69`
  只在 `requireRef == true` 时拒绝空 `AuditLedgerID`,而 gateway 用默认构造
  未启用 `WithRequiredAuditRef()`(`cmd/gateway/middleware.go:192-194`)。
- 修复:
  1. `normalized()`:当 `Kind == EventKindRequestCompletion`(money-path)时,
     额外要求 `ClaimID > 0` 且**账本引用非空**(见下方跨发现交互定义)。
     不满足 → 返回 `ErrInvalidEvent`(带具体缺失字段)。
  2. `cmd/gateway` 注册 audit logger handler 时**默认启用**
     `WithRequiredAuditRef()`。
  3. 灰度逃生口:一个 feature-flag 可临时放宽 #1/#2,但启用该 flag 必须
     同时写一条 mandatory reconciliation 记录(codex 实现时若无现成
     reconciliation 机制,放一条 TODO + 路线图项 RR-W4-001,不阻断)。
- 风险测试(判别性):
  1. money-path completion 事件 `AuditLedgerID` 空、无 DLQ 待补引用 →
     断言 `normalized()` 返回 `ErrInvalidEvent`。
  2. 带真实 `AuditLedgerID` → 通过。
  3. 带 DLQ 待补引用(见下)→ 通过。
  mutation:把 normalized 的新校验删掉 → 测试 1 变红。

### 跨发现交互(W4a ↔ W4c)—— 必须读

W4a 决策 B 的后果:`Append` 失败时账本条目进 DLQ,**此时没有持久化的
`LedgerID`**。但请求放行了,billing 仍要 settle,completion 事件仍要发。
若 W4c 的 `normalized()` 死板要求 `AuditLedgerID != ""`,会把这种合法的
「账本在 DLQ 待补」事件拒掉。

**统一定义「账本引用」**:money-path completion 事件的账本引用 = 满足以下
**任一**即为非空:
- `AuditLedgerID != ""`(账本已持久化),或
- 一个明确的 **DLQ 待补引用**:新增字段 `AuditLedgerDLQRef string`(W4a 入队
  时回填 DLQ 事件 id),非空表示「账本条目已签名、在 DLQ id=X 等待补写」。

`normalized()` 校验:money-path 事件必须 `AuditLedgerID != "" || AuditLedgerDLQRef
!= ""`,两者皆空才拒绝。`AuditSignatureFingerprint` 在两种情况下都应有(条目
在入 DLQ 前已签名,fingerprint 已知)—— 因此 fingerprint 仍强制非空。
audit logger handler 的 `requireRef` 检查同步改成「两者之一非空」。

W4a 的 `submitAuditLedgerEntry` / `emitStreamingLedger` 在走 DLQ 分支时,
必须回填 `AuditLedgerDLQRef` + `AuditSignatureFingerprint` 到 env/event,
供 completion 事件携带。这条是 W4a 与 W4c 的契约,W4a 切片实现时就要落字段。

## 7. 已知难点清单(review 不必再"发现")

1. W4a↔W4c:DLQ 待补引用 `AuditLedgerDLQRef` 是契约字段,W4a 落、W4c 校验(§6)。
2. C-13:单条账本 @ 流终态,不拆两条;codex 须确认覆盖客户端断连等终结路径。
3. B-13:脱敏失败取最小化 fallback(确定性收敛),不取 fail-append(会无限重试)。
4. B-15:Merkle root 长度 0 合法(首条目),只有非 0 非 32 才算损坏。
5. 启动检查只在 production 模式强制;dev/test 允许 nil/Noop ledger。
6. DLQ 复用 `internal/dlq`;新 `EventKind` 或复用贴近值由 codex 实现时定。
7. 新文件只能进非冻结包(`auditledger` / `cmd/gateway` / `eventbus` 可加;
   `gateway` / `gatewayhttp` / `proto` 冻结,只改既有文件)。

## 8. 验收标准

W4a / W4b / W4c 各自:
- `cd backend && GOCACHE=$HOME/.cache/go-build go build ./...` exit 0。
- 改动包 + 受影响包 `go test ... -race -count=1` exit 0;最后全量 `go test ./...`。
- 每个发现的「风险测试」全部新增并通过;**每个测试过 mutation 自检**
  (按 `AGENTS.md` §Test Quality Discipline),codex 交付报告逐测试写明自检结果。
- codex per-commit review 无 S0/S1 真实缺陷。

## 9. 提交方式(一 commit 一模块)

- W4a:可能跨 `auditledger`(新 DLQ helper)/ `gatewayhttp` / `gateway` /
  `cmd/gateway` —— 按模块拆 commit,DLQ helper 与其首个调用方可同 commit。
- W4b:`auditledger` 一个 commit(privacy + postgres + ledger + verify handler
  的 corrupt 映射;verify handler 在 `gatewayhttp` 冻结包,改既有文件 OK,
  可单独 commit 或并入说明)。
- W4c:`eventbus` + `cmd/gateway` + `observability` —— 按模块拆。
- 标题 `<英文模块> <中文说明>`,无 type/阶段号/PASS;结尾 Co-Authored-By。

## 10. clean-room

W4 全部改 HUAKAI 内部代码(`backend/`),不读参照项目源码 —— 无 clean-room
约束。收尾对照阶段(W4 整波闭合后)才读参照项目的审计/账本模块。

---
作者:Claude。日期:2026-05-22。源波计划已 parallel-draft + 交叉评审;
本 spec 是该已批准波的实施细化。Owner 已定 fail-closed 决策(§3)。
